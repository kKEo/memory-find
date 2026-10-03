package kb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Layer L5: pages and work items (roadmap P8). A page is curated markdown
// the calling agent wrote from chunks. The server never writes page text; it
// proposes work items, stores what the agent submits with is_inference = 1
// and the chunks it was built from, and marks pages stale when those chunks'
// documents are revised.

// Page is one curated page.
type Page struct {
	ID           string     `json:"id"`
	URI          string     `json:"uri"`
	Namespace    string     `json:"namespace"`
	Kind         string     `json:"kind"` // entity | topic | overview
	SubjectID    string     `json:"subject_id,omitempty"`
	Title        string     `json:"title"`
	Content      string     `json:"content"`
	BuiltAt      time.Time  `json:"built_at"`
	BuiltFromRev int        `json:"built_from_rev"`
	Stale        bool       `json:"stale"`
	StaleReason  string     `json:"stale_reason,omitempty"`
	IsInference  bool       `json:"is_inference"`
	Trust        string     `json:"trust"`
	Actor        string     `json:"actor,omitempty"`
	Sources      []string   `json:"sources"` // chunk addresses
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// PageInput is what the agent submits.
type PageInput struct {
	Namespace string
	Kind      string
	SubjectID string // entity id for entity pages
	Title     string
	Content   string
	Sources   []int64 // chunk ids the page was built from
	Actor     string
	Channel   string
}

const (
	PageKindEntity   = "entity"
	PageKindTopic    = "topic"
	PageKindOverview = "overview"
)

// WritePage creates or rebuilds a page. A page with the same namespace,
// kind and subject is replaced in place (built_from_rev + 1), so an entity
// has one page. Trust follows the channel cap (tool writes are agent). The
// page is embedded for granularity=page search when an embedder is present.
func (s *Store) WritePage(ctx context.Context, in PageInput) (*Page, error) {
	if in.Namespace == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("page needs namespace, title and content")
	}
	if err := validateEnum("kind", in.Kind, PageKindEntity, PageKindTopic, PageKindOverview); err != nil {
		return nil, err
	}
	if len(in.Sources) == 0 {
		return nil, errors.New("a page must cite at least one source chunk")
	}
	if in.Channel == "" {
		in.Channel = ChannelTool
	}
	trust := channelCap(in.Channel)
	now := s.now()
	nowMs := now.UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO namespaces(name, created_at) VALUES (?, ?)`, in.Namespace, nowMs); err != nil {
		return nil, err
	}
	// Validate sources exist.
	for _, id := range in.Sources {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks WHERE id = ?`, id).Scan(&n); err != nil || n == 0 {
			return nil, fmt.Errorf("source memo://chunk/%d: %w", id, ErrNotFound)
		}
	}
	var id string
	rev := 1
	op := "page_write"
	if in.SubjectID != "" {
		var prev sql.NullString
		var prevRev sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT id, built_from_rev FROM pages WHERE namespace = ? AND kind = ? AND subject_id = ? AND deleted_at IS NULL`, in.Namespace, in.Kind, in.SubjectID).Scan(&prev, &prevRev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if prev.Valid {
			id, rev, op = prev.String, int(prevRev.Int64)+1, "page_rebuild"
		}
	}
	if id == "" {
		id = uuid.Must(uuid.NewV7()).String()
		_, err = tx.ExecContext(ctx, `INSERT INTO pages(id, namespace, kind, subject_id, title, content, built_at, built_from_rev, stale, is_inference, trust, actor) VALUES (?,?,?,?,?,?,?,?,0,1,?,?)`,
			id, in.Namespace, in.Kind, nullIfEmpty(in.SubjectID), in.Title, in.Content, nowMs, rev, trust, in.Actor)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE pages SET title = ?, content = ?, built_at = ?, built_from_rev = ?, stale = 0, stale_reason = NULL, trust = ?, actor = ? WHERE id = ?`,
			in.Title, in.Content, nowMs, rev, trust, in.Actor, id)
		if err == nil {
			_, err = tx.ExecContext(ctx, `DELETE FROM page_sources WHERE page_id = ?`, id)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("write page: %w", err)
	}
	for _, c := range in.Sources {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO page_sources(page_id, chunk_id) VALUES (?, ?)`, id, c); err != nil {
			return nil, err
		}
	}
	if err := writeAudit(ctx, tx, nowMs, in.Actor, in.Channel, op, "memo://page/"+id, map[string]any{"kind": in.Kind, "subject": in.SubjectID, "sources": len(in.Sources), "rev": rev, "trust": trust}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// Vector for page search: best effort, never blocks the write.
	if s.embedder != nil {
		if modelID, err := s.ensureModel(ctx); err == nil {
			if v, err := s.embedder.Embed(ctx, in.Title+"\n"+in.Content); err == nil {
				_, _ = s.db.ExecContext(ctx, `INSERT OR REPLACE INTO page_vecs(page_id, model_id, embedding) VALUES (?,?,?)`, id, modelID, EncodeVector(v))
			}
		}
	}
	return s.ReadPage(ctx, id)
}

// ReadPage loads one page with its sources.
func (s *Store) ReadPage(ctx context.Context, id string) (*Page, error) {
	var p Page
	var subject, staleReason, deletedReason sql.NullString
	var built, deleted sql.NullInt64
	var stale, inf int
	err := s.db.QueryRowContext(ctx, `SELECT id, namespace, kind, subject_id, title, content, built_at, built_from_rev, stale, stale_reason, is_inference, trust, actor, deleted_at, deleted_reason FROM pages WHERE id = ?`, id).
		Scan(&p.ID, &p.Namespace, &p.Kind, &subject, &p.Title, &p.Content, &built, &p.BuiltFromRev, &stale, &staleReason, &inf, &p.Trust, &p.Actor, &deleted, &deletedReason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memo://page/%s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	p.URI = "memo://page/" + p.ID
	p.SubjectID, p.StaleReason = subject.String, staleReason.String
	p.BuiltAt = time.UnixMilli(built.Int64).UTC()
	p.Stale, p.IsInference = stale == 1, inf == 1
	if deleted.Valid {
		return nil, &Forgotten{URI: p.URI, At: time.UnixMilli(deleted.Int64).UTC(), Reason: deletedReason.String}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_id FROM page_sources WHERE page_id = ? ORDER BY chunk_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	p.Sources = []string{}
	for rows.Next() {
		var c int64
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		p.Sources = append(p.Sources, fmt.Sprintf("memo://chunk/%d", c))
	}
	return &p, rows.Err()
}

// PageFilter narrows ListPages.
type PageFilter struct {
	Namespace string
	Kind      string
	StaleOnly bool
	Limit     int
}

// ListPages lists live pages, newest build first.
func (s *Store) ListPages(ctx context.Context, f PageFilter) ([]Page, error) {
	q := `SELECT id FROM pages WHERE deleted_at IS NULL`
	var args []any
	if f.Namespace != "" {
		q += ` AND namespace = ?`
		args = append(args, f.Namespace)
	}
	if f.Kind != "" {
		q += ` AND kind = ?`
		args = append(args, f.Kind)
	}
	if f.StaleOnly {
		q += ` AND stale = 1`
	}
	q += ` ORDER BY built_at DESC, rowid DESC`
	if f.Limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, f.Limit)
	}
	ids, err := s.idList(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := make([]Page, 0, len(ids))
	for _, id := range ids {
		p, err := s.ReadPage(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, nil
}

// PageStats counts the layer for status.
type PageStats struct {
	Pages         int `json:"pages"`
	StalePages    int `json:"stale_pages"`
	OpenWorkItems int `json:"open_work_items"`
}

// InvalidateFact ends a fact's validity because a conflict was resolved
// against it: it stays readable as history (as_of), the winner is recorded,
// and the reason is audited. Trust rule: a channel may invalidate only facts
// at or below its cap, and never a fact more trusted than the winner.
func (s *Store) InvalidateFact(ctx context.Context, loserID, winnerID, reason, actor, channel string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var loserTrust, winnerTrust string
	var invalidated sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT trust, invalidated_at FROM facts WHERE id = ? AND deleted_at IS NULL`, loserID).Scan(&loserTrust, &invalidated); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("memo://fact/%s: %w", loserID, ErrNotFound)
	} else if err != nil {
		return err
	}
	if invalidated.Valid {
		return fmt.Errorf("memo://fact/%s is already invalidated", loserID)
	}
	if err := tx.QueryRowContext(ctx, `SELECT trust FROM facts WHERE id = ? AND deleted_at IS NULL`, winnerID).Scan(&winnerTrust); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("memo://fact/%s: %w", winnerID, ErrNotFound)
	} else if err != nil {
		return err
	}
	if trustRank[loserTrust] > trustRank[winnerTrust] {
		return fmt.Errorf("conflict: the losing fact (%s) is more trusted than the winner (%s); a human decides", loserTrust, winnerTrust)
	}
	if trustRank[loserTrust] > trustRank[channelCap(channel)] {
		return &ErrNeedsHuman{Op: "invalidate", URI: "memo://fact/" + loserID, Trust: loserTrust, Command: "memo-mcp facts invalidate memo://fact/" + loserID + " --by memo://fact/" + winnerID}
	}
	nowMs := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE facts SET invalidated_at = ?, superseded_by = ? WHERE id = ?`, nowMs, winnerID, loserID); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, nowMs, actor, channel, "resolve_conflict", "memo://fact/"+loserID, map[string]any{"winner": "memo://fact/" + winnerID, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit()
}

// WorkItem is one unit of compaction work for the calling agent.
type WorkItem struct {
	ID        string          `json:"id"`
	Namespace string          `json:"namespace"`
	Kind      string          `json:"kind"` // page | stale | conflict | merge | duplicate
	Subject   string          `json:"subject"`
	Payload   json.RawMessage `json:"payload"`
	State     string          `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// UpsertWorkItem records an open item unless one with the same kind and
// subject is already open (the recurrence trigger decides whether to call
// this at all).
func (s *Store) UpsertWorkItem(ctx context.Context, ns, kind, subject string, payload any) (*WorkItem, bool, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	var existing string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM work_items WHERE kind = ? AND subject = ? AND state = 'open'`, kind, subject).Scan(&existing)
	if err == nil {
		if _, err := s.db.ExecContext(ctx, `UPDATE work_items SET payload_json = ? WHERE id = ?`, string(b), existing); err != nil {
			return nil, false, err
		}
		w, err := s.ReadWorkItem(ctx, existing)
		return w, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	id := uuid.Must(uuid.NewV7()).String()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO work_items(id, namespace, kind, subject, payload_json, state, created_at) VALUES (?,?,?,?,?,'open',?)`, id, ns, kind, subject, string(b), s.now().UnixMilli()); err != nil {
		return nil, false, err
	}
	w, err := s.ReadWorkItem(ctx, id)
	return w, true, err
}

// ReadWorkItem loads one item.
func (s *Store) ReadWorkItem(ctx context.Context, id string) (*WorkItem, error) {
	var w WorkItem
	var payload string
	var result sql.NullString
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT id, namespace, kind, subject, payload_json, state, created_at, result_json FROM work_items WHERE id = ?`, id).Scan(&w.ID, &w.Namespace, &w.Kind, &w.Subject, &payload, &w.State, &created, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("work item %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	w.Payload = json.RawMessage(payload)
	w.CreatedAt = time.UnixMilli(created).UTC()
	if result.Valid {
		w.Result = json.RawMessage(result.String)
	}
	return &w, nil
}

// ListWorkItems lists items, open first.
func (s *Store) ListWorkItems(ctx context.Context, ns, kind, state string, limit int) ([]WorkItem, error) {
	q := `SELECT id FROM work_items WHERE 1=1`
	var args []any
	if ns != "" {
		q += ` AND namespace = ?`
		args = append(args, ns)
	}
	if kind != "" {
		q += ` AND kind = ?`
		args = append(args, kind)
	}
	if state != "" {
		q += ` AND state = ?`
		args = append(args, state)
	}
	q += ` ORDER BY state = 'open' DESC, created_at, id`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	ids, err := s.idList(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := make([]WorkItem, 0, len(ids))
	for _, id := range ids {
		w, err := s.ReadWorkItem(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, nil
}

// CloseWorkItem records the outcome of an item.
func (s *Store) CloseWorkItem(ctx context.Context, id, state string, result any, actor string) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE work_items SET state = ?, result_json = ?, decided_at = ?, actor = ? WHERE id = ? AND state = 'open'`, state, string(b), s.now().UnixMilli(), actor, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("work item %s is not open", id)
	}
	return nil
}

// FactsAbout returns the live facts whose subject is the entity.
func (s *Store) FactsAbout(ctx context.Context, entityID string) ([]Fact, error) {
	ids, err := s.idList(ctx, `SELECT id FROM facts WHERE subject_entity_id = ? AND deleted_at IS NULL AND invalidated_at IS NULL ORDER BY recorded_at`, entityID)
	if err != nil {
		return nil, err
	}
	var out []Fact
	for _, id := range ids {
		f, err := s.ReadFact(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, nil
}

// EntitiesWithMentions lists a namespace's entities with at least min live
// mentions, most mentioned first, with the id of their current page if any.
func (s *Store) EntitiesWithMentions(ctx context.Context, ns string, min int) ([]EntityPageRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.canonical, e.type, COUNT(*) AS n,
		(SELECT p.id FROM pages p WHERE p.namespace = e.namespace AND p.kind = 'entity' AND p.subject_id = e.id AND p.deleted_at IS NULL),
		(SELECT p.stale FROM pages p WHERE p.namespace = e.namespace AND p.kind = 'entity' AND p.subject_id = e.id AND p.deleted_at IS NULL),
		(SELECT COUNT(*) FROM page_sources ps JOIN pages p ON p.id = ps.page_id WHERE p.namespace = e.namespace AND p.kind = 'entity' AND p.subject_id = e.id AND p.deleted_at IS NULL)
		FROM entities e JOIN mentions m ON m.entity_id = e.id JOIN chunks c ON c.id = m.chunk_id JOIN documents d ON d.id = c.document_id
		WHERE e.namespace = ? AND d.deleted_at IS NULL AND d.superseded_by IS NULL
		GROUP BY e.id HAVING n >= ? ORDER BY n DESC, e.canonical`, ns, min)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntityPageRow
	for rows.Next() {
		var r EntityPageRow
		var page sql.NullString
		var stale, srcs sql.NullInt64
		if err := rows.Scan(&r.Entity.ID, &r.Entity.Canonical, &r.Entity.Type, &r.Entity.Mentions, &page, &stale, &srcs); err != nil {
			return nil, err
		}
		r.Entity.Namespace, r.Entity.URI = ns, "memo://entity/"+r.Entity.ID
		r.PageID, r.PageStale, r.PageSources = page.String, stale.Int64 == 1, int(srcs.Int64)
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntityPageRow is an entity with its page state.
type EntityPageRow struct {
	Entity      Entity
	PageID      string
	PageStale   bool
	PageSources int
}

// ChunkText is a live chunk's text with its document, for near-duplicate detection.
type ChunkText struct {
	ID    int64
	DocID string
	Title string
	Text  string
}

// LiveChunks returns the live chunks of a namespace.
func (s *Store) LiveChunks(ctx context.Context, ns string) ([]ChunkText, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.document_id, s.title, c.text FROM chunks c JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id WHERE s.namespace = ? AND d.deleted_at IS NULL AND d.superseded_by IS NULL ORDER BY c.id`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChunkText
	for rows.Next() {
		var c ChunkText
		if err := rows.Scan(&c.ID, &c.DocID, &c.Title, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// OrphanEntities lists entities with no live mention and no live fact about them.
func (s *Store) OrphanEntities(ctx context.Context, ns string) ([]Entity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.canonical, e.type FROM entities e WHERE e.namespace = ? AND NOT EXISTS (
		SELECT 1 FROM mentions m JOIN chunks c ON c.id = m.chunk_id JOIN documents d ON d.id = c.document_id WHERE m.entity_id = e.id AND d.deleted_at IS NULL AND d.superseded_by IS NULL)
		AND NOT EXISTS (SELECT 1 FROM facts f WHERE f.subject_entity_id = e.id AND f.deleted_at IS NULL AND f.invalidated_at IS NULL) ORDER BY e.canonical`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entity
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.Canonical, &e.Type); err != nil {
			return nil, err
		}
		e.Namespace, e.URI = ns, "memo://entity/"+e.ID
		out = append(out, e)
	}
	return out, rows.Err()
}

// Namespaces lists namespace names.
func (s *Store) Namespaces(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM namespaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// idList runs a one-column id query.
func (s *Store) idList(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
