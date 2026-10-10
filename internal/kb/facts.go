package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kKEo/memors/internal/embedding"
)

// Trust tiers in order; a channel's cap is the highest tier it may act on
// without a human (docs/schema.md §7).
var trustRank = map[string]int{TrustAgent: 0, TrustUser: 1, TrustCurated: 2}

// channelCap is the highest trust a channel may write or retire on its own.
// Tool calls act on agent records only; the CLI and elicitation act on all.
func channelCap(channel string) string {
	switch channel {
	case ChannelTool:
		return TrustAgent
	default:
		return TrustCurated
	}
}

// ErrNeedsHuman is returned when a tool call tries to retire or raise a
// record above its channel's cap. The message carries the CLI command that
// does it, so a client without elicitation support can show it.
type ErrNeedsHuman struct {
	Op, URI, Trust, Command string
}

func (e *ErrNeedsHuman) Error() string {
	return fmt.Sprintf("%s on %s needs a human: the record is trust=%s and tool calls may only act on trust=agent records. Run `%s` or confirm through the client", e.Op, e.URI, e.Trust, e.Command)
}

// RememberInput is one atomic fact.
type RememberInput struct {
	Namespace   string
	Statement   string
	About       []string // names the fact is about; matched by the fact arm before entities exist (P7)
	ValidFrom   *time.Time
	ValidTo     *time.Time
	Supersedes  string // memo://fact/<id> of the fact this one replaces
	EvidenceURI string // memo://chunk/<n> that backs it
	Origin      string
	Trust       string // set by the channel
	Actor       string
	Channel     string
}

// Fact is a stored fact.
type Fact struct {
	ID              string     `json:"id"`
	URI             string     `json:"uri"`
	Namespace       string     `json:"namespace"`
	Statement       string     `json:"statement"`
	About           []string   `json:"about,omitempty"`
	ValidFrom       *time.Time `json:"valid_from,omitempty"`
	ValidTo         *time.Time `json:"valid_to,omitempty"`
	RecordedAt      time.Time  `json:"recorded_at"`
	InvalidatedAt   *time.Time `json:"invalidated_at,omitempty"`
	SupersededBy    string     `json:"superseded_by,omitempty"`
	EvidenceChunkID *int64     `json:"evidence_chunk_id,omitempty"`
	EvidenceURI     string     `json:"evidence_uri,omitempty"`
	Trust           string     `json:"trust"`
	Origin          string     `json:"origin"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
	DeletedReason   string     `json:"deleted_reason,omitempty"`
}

// Remember stores a fact. Facts are only ever added: when Supersedes names an
// older fact, that fact is invalidated (invalidated_at, superseded_by) and
// kept as history. The new fact is indexed for keywords (trigger) and, when
// an embedder is configured, for meaning.
func (s *Store) Remember(ctx context.Context, in RememberInput) (*Fact, error) {
	if err := ValidateName(in.Namespace); err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	stmt := strings.TrimSpace(in.Statement)
	if stmt == "" {
		return nil, errors.New("statement is empty")
	}
	if err := validateEnum("trust", in.Trust, TrustAgent, TrustUser, TrustCurated); err != nil {
		return nil, err
	}
	if err := validateEnum("origin", in.Origin, OriginWeb, OriginUserSaid, OriginAgentDerived); err != nil {
		return nil, err
	}
	if err := validateEnum("channel", in.Channel, ChannelTool, ChannelElicitation, ChannelCLI, ChannelWorker); err != nil {
		return nil, err
	}
	var evidence sql.NullInt64
	if in.EvidenceURI != "" {
		kind, id, err := ParseURI(in.EvidenceURI)
		if err != nil || kind != "chunk" {
			return nil, fmt.Errorf("evidence_uri must be a memo://chunk/<n> address")
		}
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("evidence_uri: bad chunk id")
		}
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE id = ?`, n).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, fmt.Errorf("evidence chunk %s does not exist", in.EvidenceURI)
		}
		evidence = sql.NullInt64{Int64: n, Valid: true}
	}
	now := s.now().UTC()
	nowMs := now.UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := ensureNamespace(ctx, tx, in.Namespace, nowMs); err != nil {
		return nil, err
	}
	var oldID string
	if in.Supersedes != "" {
		kind, id, err := ParseURI(in.Supersedes)
		if err != nil || kind != "fact" {
			return nil, fmt.Errorf("supersedes must be a memo://fact/<id> address")
		}
		var oldTrust string
		var invalidated sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT trust, invalidated_at FROM facts WHERE id = ? AND deleted_at IS NULL`, id).Scan(&oldTrust, &invalidated)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%s: %w", in.Supersedes, ErrNotFound)
		}
		if err != nil {
			return nil, err
		}
		if invalidated.Valid {
			return nil, fmt.Errorf("%s is already superseded", in.Supersedes)
		}
		if trustRank[oldTrust] > trustRank[channelCap(in.Channel)] {
			return nil, &ErrNeedsHuman{Op: "supersede", URI: in.Supersedes, Trust: oldTrust, Command: "memors-mcp remember --supersedes " + in.Supersedes + " ..."}
		}
		oldID = id
	}
	about, _ := json.Marshal(nonNil(in.About))
	f := &Fact{ID: uuid.Must(uuid.NewV7()).String(), Namespace: in.Namespace, Statement: stmt, About: in.About, ValidFrom: in.ValidFrom, ValidTo: in.ValidTo, RecordedAt: now, Trust: in.Trust, Origin: in.Origin, EvidenceURI: in.EvidenceURI}
	f.URI = "memo://fact/" + f.ID
	if evidence.Valid {
		f.EvidenceChunkID = &evidence.Int64
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO facts(id, namespace, statement, about_json, valid_from, valid_to, recorded_at, evidence_chunk_id, trust, origin) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.Namespace, stmt, string(about), msOrNil(in.ValidFrom), msOrNil(in.ValidTo), nowMs, evidence, in.Trust, in.Origin)
	if err != nil {
		return nil, fmt.Errorf("insert fact: %w", err)
	}
	if err := s.linkFactSubject(ctx, tx, f.Namespace, f.ID, in.About, nowMs); err != nil {
		return nil, err
	}
	if oldID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE facts SET invalidated_at = ?, superseded_by = ? WHERE id = ?`, nowMs, f.ID, oldID); err != nil {
			return nil, fmt.Errorf("supersede: %w", err)
		}
		if err := writeAudit(ctx, tx, s.metrics, nowMs, in.Actor, in.Channel, "supersede", "memo://fact/"+oldID, map[string]any{"by": f.URI}); err != nil {
			return nil, err
		}
	}
	if err := writeAudit(ctx, tx, s.metrics, nowMs, in.Actor, in.Channel, "remember", f.URI, map[string]any{"trust": in.Trust, "evidence": in.EvidenceURI, "supersedes": in.Supersedes}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Vector outside the transaction; a failure leaves a keyword-only fact
	// (the fact arm still finds it) and a note in the audit.
	if s.embedder != nil {
		if modelID, err := s.ensureModel(ctx); err == nil {
			if vecs, err := s.embedder.EmbedBatch(ctx, []string{stmt}, embedding.RoleDocument); err == nil && len(vecs) == 1 {
				_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO fact_vecs(fact_id, model_id, embedding) VALUES (?,?,?)`, f.ID, modelID, EncodeVector(vecs[0]))
			}
		}
	}
	return f, nil
}

// ForgetInput retires a document or fact.
type ForgetInput struct {
	URI     string // memo://doc/<id> or memo://fact/<id>
	Reason  string
	Redact  bool // also clear the content
	Actor   string
	Channel string
}

// Forget tombstones a record: it leaves every index (a document's chunks are
// deleted, which the FTS triggers and the vector table's cascade follow) and
// is never served again, not even under as_of; `read` on its address says
// when and why. Content is kept for audit unless Redact. A tool call may
// only forget agent-trust records (docs/schema.md §7).
func (s *Store) Forget(ctx context.Context, in ForgetInput) error {
	kind, id, err := ParseURI(in.URI)
	if err != nil {
		return err
	}
	if strings.TrimSpace(in.Reason) == "" {
		return errors.New("a reason is required")
	}
	if err := validateEnum("channel", in.Channel, ChannelTool, ChannelElicitation, ChannelCLI, ChannelWorker); err != nil {
		return err
	}
	nowMs := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var trust string
	var deleted sql.NullInt64
	switch kind {
	case "doc":
		err = tx.QueryRowContext(ctx, `SELECT s.trust, d.deleted_at FROM documents d JOIN sources s ON s.id = d.source_id WHERE d.id = ?`, id).Scan(&trust, &deleted)
	case "fact":
		err = tx.QueryRowContext(ctx, `SELECT trust, deleted_at FROM facts WHERE id = ?`, id).Scan(&trust, &deleted)
	case "page":
		err = tx.QueryRowContext(ctx, `SELECT trust, deleted_at FROM pages WHERE id = ?`, id).Scan(&trust, &deleted)
	default:
		return fmt.Errorf("forget supports memo://doc, memo://fact and memo://page addresses, not %q", kind)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", in.URI, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if deleted.Valid && !in.Redact {
		return nil // idempotent
	}
	if trustRank[trust] > trustRank[channelCap(in.Channel)] {
		return &ErrNeedsHuman{Op: "forget", URI: in.URI, Trust: trust, Command: fmt.Sprintf("memors-mcp forget %s --reason %q", in.URI, in.Reason)}
	}
	op := "forget"
	if in.Redact {
		op = "redact"
	}
	switch kind {
	case "doc":
		// Pages built from this document lose a source: stale, with the reason.
		staleRes, err := tx.ExecContext(ctx, `UPDATE pages SET stale = 1, stale_reason = ? WHERE deleted_at IS NULL AND stale = 0 AND id IN (SELECT ps.page_id FROM page_sources ps JOIN chunks c ON c.id = ps.chunk_id WHERE c.document_id = ?)`, "source forgotten: "+in.URI, id)
		if err != nil {
			return err
		}
		if n, _ := staleRes.RowsAffected(); n > 0 {
			s.metrics.pagesStale.Add(float64(n))
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE document_id = ?`, id); err != nil {
			return err
		}
		q := `UPDATE documents SET deleted_at = COALESCE(deleted_at, ?), deleted_reason = ?, updated_at = ? WHERE id = ?`
		if in.Redact {
			q = `UPDATE documents SET deleted_at = COALESCE(deleted_at, ?), deleted_reason = ?, updated_at = ?, content = '[redacted]' WHERE id = ?`
		}
		if _, err := tx.ExecContext(ctx, q, nowMs, in.Reason, nowMs, id); err != nil {
			return err
		}
	case "fact":
		if _, err := tx.ExecContext(ctx, `DELETE FROM fact_vecs WHERE fact_id = ?`, id); err != nil {
			return err
		}
		q := `UPDATE facts SET deleted_at = COALESCE(deleted_at, ?), deleted_reason = ? WHERE id = ?`
		if in.Redact {
			q = `UPDATE facts SET deleted_at = COALESCE(deleted_at, ?), deleted_reason = ?, statement = '[redacted]' WHERE id = ?`
		}
		if _, err := tx.ExecContext(ctx, q, nowMs, in.Reason, id); err != nil {
			return err
		}
	case "page":
		q := `UPDATE pages SET deleted_at = COALESCE(deleted_at, ?), deleted_reason = ? WHERE id = ?`
		if in.Redact {
			q = `UPDATE pages SET deleted_at = COALESCE(deleted_at, ?), deleted_reason = ?, content = '[redacted]' WHERE id = ?`
		}
		if _, err := tx.ExecContext(ctx, q, nowMs, in.Reason, id); err != nil {
			return err
		}
	}
	if err := writeAudit(ctx, tx, s.metrics, nowMs, in.Actor, in.Channel, op, in.URI, map[string]any{"reason": in.Reason, "trust": trust}); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTrust raises or lowers a record's trust. Raising is `promote`; it is
// only ever called from the CLI or after an accepted elicitation (the tool
// returns the command instead). Lowering is `demote`, CLI only.
func (s *Store) SetTrust(ctx context.Context, uri, to, actor, channel string) error {
	if err := validateEnum("trust", to, TrustAgent, TrustUser, TrustCurated); err != nil {
		return err
	}
	if channel == ChannelTool {
		return &ErrNeedsHuman{Op: "promote", URI: uri, Trust: to, Command: fmt.Sprintf("memors-mcp trust promote %s --to %s", uri, to)}
	}
	kind, id, err := ParseURI(uri)
	if err != nil {
		return err
	}
	nowMs := s.now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var from string
	var res sql.Result
	switch kind {
	case "doc", "source":
		q := `SELECT s.trust FROM sources s WHERE s.id = ?`
		if kind == "doc" {
			q = `SELECT s.trust FROM sources s JOIN documents d ON d.source_id = s.id WHERE d.id = ?`
		}
		if err := tx.QueryRowContext(ctx, q, id).Scan(&from); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%s: %w", uri, ErrNotFound)
			}
			return err
		}
		if kind == "doc" {
			res, err = tx.ExecContext(ctx, `UPDATE sources SET trust = ? WHERE id = (SELECT source_id FROM documents WHERE id = ?)`, to, id)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE sources SET trust = ? WHERE id = ?`, to, id)
		}
	case "fact":
		if err := tx.QueryRowContext(ctx, `SELECT trust FROM facts WHERE id = ?`, id).Scan(&from); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%s: %w", uri, ErrNotFound)
			}
			return err
		}
		res, err = tx.ExecContext(ctx, `UPDATE facts SET trust = ? WHERE id = ?`, to, id)
	default:
		return fmt.Errorf("trust applies to memo://doc, memo://source and memo://fact addresses, not %q", kind)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s: %w", uri, ErrNotFound)
	}
	op := "promote"
	if trustRank[to] < trustRank[from] {
		op = "demote"
	}
	if err := writeAudit(ctx, tx, s.metrics, nowMs, actor, channel, op, uri, map[string]any{"from": from, "to": to}); err != nil {
		return err
	}
	return tx.Commit()
}

// FactFilter selects facts for ListFacts.
type FactFilter struct {
	Namespace      string
	AsOf           *time.Time // show what was believed at this time (history), nil = live only
	IncludeHistory bool       // include superseded facts regardless of time
	Limit          int
}

// ListFacts returns facts newest first. Forgotten facts are never listed.
func (s *Store) ListFacts(ctx context.Context, f FactFilter) ([]Fact, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	q := `SELECT id, namespace, statement, about_json, valid_from, valid_to, recorded_at, invalidated_at, superseded_by, evidence_chunk_id, trust, origin FROM facts WHERE deleted_at IS NULL`
	var args []any
	if f.Namespace != "" {
		q += ` AND namespace = ?`
		args = append(args, f.Namespace)
	}
	switch {
	case f.AsOf != nil:
		q += ` AND recorded_at <= ? AND (invalidated_at IS NULL OR invalidated_at > ?)`
		args = append(args, f.AsOf.UnixMilli(), f.AsOf.UnixMilli())
	case !f.IncludeHistory:
		q += ` AND invalidated_at IS NULL`
	}
	q += ` ORDER BY recorded_at DESC LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fact
	for rows.Next() {
		fact, err := scanFact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *fact)
	}
	return out, rows.Err()
}

// ReadFact returns one fact; a forgotten fact returns *Forgotten.
func (s *Store) ReadFact(ctx context.Context, id string) (*Fact, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, namespace, statement, about_json, valid_from, valid_to, recorded_at, invalidated_at, superseded_by, evidence_chunk_id, trust, origin, deleted_at, deleted_reason FROM facts WHERE id = ?`, id)
	var f Fact
	var about string
	var vf, vt, inv, ev, del sql.NullInt64
	var sup, reason sql.NullString
	var rec int64
	if err := row.Scan(&f.ID, &f.Namespace, &f.Statement, &about, &vf, &vt, &rec, &inv, &sup, &ev, &f.Trust, &f.Origin, &del, &reason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("memo://fact/%s: %w", id, ErrNotFound)
		}
		return nil, err
	}
	f.URI = "memo://fact/" + f.ID
	if del.Valid {
		return nil, &Forgotten{URI: f.URI, At: time.UnixMilli(del.Int64), Reason: reason.String}
	}
	fillFact(&f, about, vf, vt, rec, inv, sup, ev)
	return &f, nil
}

type factScanner interface{ Scan(dest ...any) error }

func scanFact(r factScanner) (*Fact, error) {
	var f Fact
	var about string
	var vf, vt, inv, ev sql.NullInt64
	var sup sql.NullString
	var rec int64
	if err := r.Scan(&f.ID, &f.Namespace, &f.Statement, &about, &vf, &vt, &rec, &inv, &sup, &ev, &f.Trust, &f.Origin); err != nil {
		return nil, err
	}
	f.URI = "memo://fact/" + f.ID
	fillFact(&f, about, vf, vt, rec, inv, sup, ev)
	return &f, nil
}

func fillFact(f *Fact, about string, vf, vt sql.NullInt64, rec int64, inv sql.NullInt64, sup sql.NullString, ev sql.NullInt64) {
	_ = json.Unmarshal([]byte(about), &f.About)
	f.RecordedAt = time.UnixMilli(rec).UTC()
	f.ValidFrom, f.ValidTo, f.InvalidatedAt = tsPtr(vf), tsPtr(vt), tsPtr(inv)
	f.SupersededBy = sup.String
	if ev.Valid {
		f.EvidenceChunkID = &ev.Int64
		f.EvidenceURI = fmt.Sprintf("memo://chunk/%d", ev.Int64)
	}
}

func tsPtr(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.UnixMilli(n.Int64).UTC()
	return &t
}

func msOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// HistoryEntry is one step in a document's revision chain or a fact's
// supersession chain, oldest first.
type HistoryEntry struct {
	URI       string     `json:"uri"`
	Revision  int        `json:"revision,omitempty"`
	Version   string     `json:"version,omitempty"`
	At        time.Time  `json:"at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
	Live      bool       `json:"live"`
	Forgotten string     `json:"forgotten,omitempty"`
	Summary   string     `json:"summary"`
}

// History walks the chain a document or fact belongs to.
func (s *Store) History(ctx context.Context, uri string) ([]HistoryEntry, error) {
	kind, id, err := ParseURI(uri)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "doc":
		var sourceID string
		if err := s.db.QueryRowContext(ctx, `SELECT source_id FROM documents WHERE id = ?`, id).Scan(&sourceID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
			}
			return nil, err
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id, revision, version, created_at, updated_at, superseded_by, deleted_at, deleted_reason, substr(content, 1, 80) FROM documents WHERE source_id = ? ORDER BY revision`, sourceID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []HistoryEntry
		for rows.Next() {
			var e HistoryEntry
			var did string
			var version, sup, reason sql.NullString
			var created, updated int64
			var del sql.NullInt64
			var head string
			if err := rows.Scan(&did, &e.Revision, &version, &created, &updated, &sup, &del, &reason, &head); err != nil {
				return nil, err
			}
			e.URI, e.Version, e.At = "memo://doc/"+did, version.String, time.UnixMilli(created).UTC()
			e.Live = !sup.Valid && !del.Valid
			if sup.Valid {
				t := time.UnixMilli(updated).UTC()
				e.RetiredAt = &t
			}
			if del.Valid {
				e.Forgotten = fmt.Sprintf("forgotten on %s: %s", time.UnixMilli(del.Int64).UTC().Format("2006-01-02"), reason.String)
			}
			e.Summary = strings.Join(strings.Fields(head), " ")
			out = append(out, e)
		}
		return out, rows.Err()
	case "fact":
		// Walk back to the root, then forward.
		cur := id
		for {
			var prev sql.NullString
			err := s.db.QueryRowContext(ctx, `SELECT id FROM facts WHERE superseded_by = ?`, cur).Scan(&prev)
			if errors.Is(err, sql.ErrNoRows) || !prev.Valid {
				break
			}
			if err != nil {
				return nil, err
			}
			cur = prev.String
		}
		var out []HistoryEntry
		for cur != "" {
			var e HistoryEntry
			var stmt string
			var rec int64
			var inv, del sql.NullInt64
			var sup, reason sql.NullString
			err := s.db.QueryRowContext(ctx, `SELECT statement, recorded_at, invalidated_at, superseded_by, deleted_at, deleted_reason FROM facts WHERE id = ?`, cur).Scan(&stmt, &rec, &inv, &sup, &del, &reason)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) && len(out) == 0 {
					return nil, fmt.Errorf("%s: %w", uri, ErrNotFound)
				}
				return nil, err
			}
			e.URI, e.At, e.Summary = "memo://fact/"+cur, time.UnixMilli(rec).UTC(), stmt
			e.RetiredAt = tsPtr(inv)
			e.Live = !inv.Valid && !del.Valid
			if del.Valid {
				e.Forgotten = fmt.Sprintf("forgotten on %s: %s", time.UnixMilli(del.Int64).UTC().Format("2006-01-02"), reason.String)
			}
			out = append(out, e)
			cur = sup.String
		}
		return out, nil
	default:
		return nil, fmt.Errorf("history applies to memo://doc and memo://fact addresses, not %q", kind)
	}
}

// TrustRow is one line of `memors-mcp trust ls`.
type TrustRow struct {
	Trust     string
	Documents int
	Facts     int
}

// TrustSummary counts live documents and facts per trust tier.
func (s *Store) TrustSummary(ctx context.Context) ([]TrustRow, error) {
	var out []TrustRow
	for _, tier := range []string{TrustCurated, TrustUser, TrustAgent} {
		var r TrustRow
		r.Trust = tier
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d JOIN sources s ON s.id = d.source_id WHERE s.trust = ? AND d.deleted_at IS NULL AND d.superseded_by IS NULL`, tier).Scan(&r.Documents); err != nil {
			return nil, err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM facts WHERE trust = ? AND deleted_at IS NULL AND invalidated_at IS NULL`, tier).Scan(&r.Facts); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
