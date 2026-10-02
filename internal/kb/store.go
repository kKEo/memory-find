package kb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kKEo/memory-find/internal/chunk"
	"github.com/kKEo/memory-find/internal/embedding"
)

// Trust tiers and origins (docs/schema.md §7). Trust is assigned by the
// channel a write arrives through; origin is what the writer declared.
const (
	TrustAgent   = "agent"
	TrustUser    = "user"
	TrustCurated = "curated"

	OriginWeb          = "web"
	OriginUserSaid     = "user-said"
	OriginAgentDerived = "agent-derived"

	ChannelTool        = "tool"
	ChannelElicitation = "elicitation"
	ChannelCLI         = "cli"
	ChannelWorker      = "worker"
)

// Kinds of source.
const (
	KindDoc          = "doc"
	KindNote         = "note"
	KindCode         = "code"
	KindConversation = "conversation"
)

// embedBatchSize is how many chunks go to the model in one pass.
const embedBatchSize = 16

// Store is the knowledge base: a migrated database plus an optional
// embedder. A nil embedder means writes are indexed for keywords only and
// their vectors are queued as `embed` jobs.
type Store struct {
	db       *sql.DB
	embedder embedding.Embedder
	chunking chunk.Options
	now      func() time.Time
}

// NewStore wraps an opened database. embedder may be nil.
func NewStore(db *sql.DB, embedder embedding.Embedder) *Store {
	s := &Store{db: db, embedder: embedder, chunking: chunk.Options{}, now: time.Now}
	if embedder != nil {
		if max := embedder.Info().MaxTokens; max > 0 && (s.chunking.Max == 0 || s.chunking.Max > max) {
			// Leave headroom under the model's limit: the token estimate is
			// approximate and the context header is prepended at embed time.
			s.chunking.Max = int(math.Min(400, float64(max)*0.75))
		}
	}
	return s
}

// DB exposes the underlying connection for read-side packages.
func (s *Store) DB() *sql.DB { return s.db }

// Embedder returns the configured embedder (may be nil).
func (s *Store) Embedder() embedding.Embedder { return s.embedder }

// SourceInput describes where content came from.
type SourceInput struct {
	URI       string // URL or path the client fetched; empty for notes
	Title     string
	Kind      string // doc|note|code|conversation
	Library   string
	Version   string
	Etag      string
	FetchedAt time.Time // zero means now
	TTL       time.Duration
	Origin    string // web|user-said|agent-derived
	Tags      []string
}

// IngestInput is one ingest call.
type IngestInput struct {
	Namespace string
	Content   string
	Source    SourceInput
	Context   string // optional client-written sentence prepended to every chunk header
	// DocumentID, when set, revises that document (notes have no URI, so
	// an update names the document). Empty for new content.
	DocumentID string
	// Trust is set by the caller from its channel, never from client input.
	Trust   string
	Actor   string
	Channel string
}

// IngestResult reports what happened.
type IngestResult struct {
	SourceID   string
	DocumentID string
	Revision   int
	Dedup      bool // identical content already stored; nothing written
	Chunks     int
	Embedded   int
	Pending    int    // chunks whose vector is queued in a job
	JobID      string // the embed job, if any
	URI        string // memo://doc/<id>
}

// Ingest stores a document: normalise → hash → dedup or new revision →
// chunk → (triggers index FTS) → embed outside the transaction → vectors →
// audit. See docs/schema.md.
func (s *Store) Ingest(ctx context.Context, in IngestInput) (*IngestResult, error) {
	if err := ValidateName(in.Namespace); err != nil {
		return nil, fmt.Errorf("namespace: %w", err)
	}
	if err := validateEnum("kind", in.Source.Kind, KindDoc, KindNote, KindCode, KindConversation); err != nil {
		return nil, err
	}
	if err := validateEnum("origin", in.Source.Origin, OriginWeb, OriginUserSaid, OriginAgentDerived); err != nil {
		return nil, err
	}
	if err := validateEnum("trust", in.Trust, TrustAgent, TrustUser, TrustCurated); err != nil {
		return nil, err
	}
	if err := validateEnum("channel", in.Channel, ChannelTool, ChannelElicitation, ChannelCLI, ChannelWorker); err != nil {
		return nil, err
	}
	content := normalise(in.Content)
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("content is empty")
	}
	hash := contentHash(content)
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

	// Locate the source: by document id (note update), by (namespace, uri),
	// or create one.
	var sourceID, prevDocID string
	var prevRevision int
	var prevHash, prevVersion sql.NullString
	switch {
	case in.DocumentID != "":
		err = tx.QueryRowContext(ctx, `SELECT d.source_id, d.id, d.revision, s.content_hash, d.version FROM documents d JOIN sources s ON s.id = d.source_id WHERE d.id = ? AND d.superseded_by IS NULL AND d.deleted_at IS NULL`, in.DocumentID).
			Scan(&sourceID, &prevDocID, &prevRevision, &prevHash, &prevVersion)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("document %s not found or not live", in.DocumentID)
		}
	case in.Source.URI != "":
		err = tx.QueryRowContext(ctx, `SELECT s.id, d.id, d.revision, s.content_hash, d.version FROM sources s LEFT JOIN documents d ON d.source_id = s.id AND d.superseded_by IS NULL AND d.deleted_at IS NULL WHERE s.namespace = ? AND s.uri = ?`, in.Namespace, in.Source.URI).
			Scan(&sourceID, &nullStr{&prevDocID}, &nullInt{&prevRevision}, &prevHash, &prevVersion)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
			sourceID = ""
		}
	}
	if err != nil {
		return nil, fmt.Errorf("look up source: %w", err)
	}

	docURI := func(id string) string { return "memo://doc/" + id }

	if sourceID != "" && prevDocID != "" && prevHash.Valid && prevHash.String == hash && nullEq(prevVersion, in.Source.Version) {
		return &IngestResult{SourceID: sourceID, DocumentID: prevDocID, Revision: prevRevision, Dedup: true, URI: docURI(prevDocID)}, nil
	}

	fetchedAt := in.Source.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = now
	}
	if sourceID == "" {
		sourceID = uuid.Must(uuid.NewV7()).String()
		tags, _ := json.Marshal(nonNil(in.Source.Tags))
		_, err = tx.ExecContext(ctx, `INSERT INTO sources(id, namespace, uri, title, kind, library, content_hash, etag, fetched_at, ttl_s, trust, origin, tags_json, created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			sourceID, in.Namespace, nullIfEmpty(in.Source.URI), in.Source.Title, in.Source.Kind, nullIfEmpty(in.Source.Library), hash,
			nullIfEmpty(in.Source.Etag), fetchedAt.UnixMilli(), nullDuration(in.Source.TTL), in.Trust, in.Source.Origin, string(tags), nowMs)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE sources SET content_hash = ?, etag = COALESCE(?, etag), fetched_at = ?, title = CASE WHEN ? != '' THEN ? ELSE title END WHERE id = ?`,
			hash, nullIfEmpty(in.Source.Etag), fetchedAt.UnixMilli(), in.Source.Title, in.Source.Title, sourceID)
	}
	if err != nil {
		return nil, fmt.Errorf("write source: %w", err)
	}

	docID := uuid.Must(uuid.NewV7()).String()
	revision := prevRevision + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO documents(id, source_id, revision, version, content, context, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
		docID, sourceID, revision, nullIfEmpty(in.Source.Version), content, nullIfEmpty(in.Context), nowMs, nowMs)
	if err != nil {
		return nil, fmt.Errorf("write document: %w", err)
	}
	if prevDocID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE documents SET superseded_by = ?, updated_at = ? WHERE id = ?`, docID, nowMs, prevDocID); err != nil {
			return nil, fmt.Errorf("supersede previous revision: %w", err)
		}
	}

	title := in.Source.Title
	chunks := chunk.Split(content, s.chunking)
	ids, err := replaceChunks(ctx, tx, docID, title, in.Context, chunks)
	if err != nil {
		return nil, err
	}
	op := "ingest"
	if prevDocID != "" {
		op = "revise"
	}
	if err := writeAudit(ctx, tx, nowMs, in.Actor, in.Channel, op, docURI(docID), map[string]any{"source": sourceID, "revision": revision, "chunks": len(ids), "content_hash": hash, "trust": in.Trust}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	res := &IngestResult{SourceID: sourceID, DocumentID: docID, Revision: revision, Chunks: len(ids), URI: docURI(docID)}

	// Vectors: embed outside any transaction, then store. Never swallowed:
	// a failure or a missing embedder leaves a queued job and a count the
	// caller and `status` can see.
	embedded, jobID, err := s.embedChunks(ctx, ids, chunks, title, in.Context)
	if err != nil {
		return nil, err
	}
	res.Embedded = embedded
	res.Pending = len(ids) - embedded
	res.JobID = jobID
	return res, nil
}

// replaceChunks is the single mutation point for a document's chunks: it
// deletes the old rows (the FTS triggers follow) and inserts the new ones,
// returning their ids in order.
func replaceChunks(ctx context.Context, tx *sql.Tx, docID, title, docContext string, chunks []chunk.Chunk) ([]int64, error) {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks WHERE document_id = ?`, docID); err != nil {
		return nil, fmt.Errorf("clear chunks: %w", err)
	}
	ids := make([]int64, 0, len(chunks))
	for _, c := range chunks {
		header := chunk.ContextHeader(title, c.SectionPath, docContext)
		res, err := tx.ExecContext(ctx, `INSERT INTO chunks(document_id, ord, section_path, text, context_header, est_tokens, lang) VALUES (?,?,?,?,?,?,?)`,
			docID, c.Ord, c.SectionPath, c.Text, header, c.EstTokens, nullIfEmpty(c.Lang))
		if err != nil {
			return nil, fmt.Errorf("insert chunk %d: %w", c.Ord, err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// embedText is what the model sees for a chunk: its context header, then
// the passage.
func embedText(title string, c chunk.Chunk, docContext string) string {
	h := chunk.ContextHeader(title, c.SectionPath, docContext)
	if h == "" {
		return c.Text
	}
	return h + "\n" + c.Text
}

// embedChunks embeds the chunks in batches and stores the vectors. On any
// failure (or without an embedder) the remaining chunk ids are queued as an
// `embed` job. Returns how many were embedded and the job id, if any.
func (s *Store) embedChunks(ctx context.Context, ids []int64, chunks []chunk.Chunk, title, docContext string) (int, string, error) {
	if len(ids) == 0 {
		return 0, "", nil
	}
	if s.embedder == nil {
		jobID, err := s.enqueueEmbed(ctx, ids, "no embedder configured")
		return 0, jobID, err
	}
	modelID, err := s.ensureModel(ctx)
	if err != nil {
		return 0, "", err
	}
	embedded := 0
	for start := 0; start < len(ids); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		texts := make([]string, 0, end-start)
		for _, c := range chunks[start:end] {
			texts = append(texts, embedText(title, c, docContext))
		}
		vecs, err := s.embedder.EmbedBatch(ctx, texts, embedding.RoleDocument)
		if err != nil {
			jobID, qerr := s.enqueueEmbed(ctx, ids[start:], err.Error())
			if qerr != nil {
				return embedded, "", qerr
			}
			return embedded, jobID, nil
		}
		if err := s.storeVectors(ctx, modelID, ids[start:end], vecs); err != nil {
			return embedded, "", err
		}
		embedded += len(vecs)
	}
	return embedded, "", nil
}

// storeVectors writes one batch of vectors in a short transaction.
func (s *Store) storeVectors(ctx context.Context, modelID string, ids []int64, vecs [][]float32) error {
	if len(ids) != len(vecs) {
		return fmt.Errorf("storeVectors: %d ids, %d vectors", len(ids), len(vecs))
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO chunk_vecs(chunk_id, model_id, embedding) VALUES (?,?,?)`, id, modelID, EncodeVector(vecs[i])); err != nil {
			return fmt.Errorf("store vector for chunk %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// ensureModel upserts the embedder's model row and returns its id.
func (s *Store) ensureModel(ctx context.Context) (string, error) {
	info := s.embedder.Info()
	if info.ID == "" {
		return "", errors.New("embedder reports no model id")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO models(id, name, hf_repo, hf_revision, onnx_path, external_data_path, dim, max_tokens, query_prefix, doc_prefix, normalize, licence, installed_at, is_default)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, CASE WHEN (SELECT COUNT(*) FROM models WHERE is_default = 1) = 0 THEN 1 ELSE 0 END)
		ON CONFLICT(id) DO UPDATE SET dim = excluded.dim, max_tokens = excluded.max_tokens`,
		info.ID, info.Name, info.HFRepo, info.HFRevision, info.OnnxPath, nullIfEmpty(info.ExternalDataPath), info.Dim, info.MaxTokens,
		info.QueryPrefix, info.DocPrefix, boolInt(info.Normalize), info.Licence, s.now().UnixMilli())
	if err != nil {
		return "", fmt.Errorf("upsert model: %w", err)
	}
	return info.ID, nil
}

// DefaultModelID returns the id of the model queries should use, or "".
func (s *Store) DefaultModelID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM models WHERE is_default = 1 LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) enqueueEmbed(ctx context.Context, ids []int64, reason string) (string, error) {
	items, _ := json.Marshal(ids)
	jobID := uuid.Must(uuid.NewV7()).String()
	now := s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs(id, kind, scope, state, created_at, updated_at, items_json, error) VALUES (?,?,?,?,?,?,?,?)`,
		jobID, "embed", "chunks", "queued", now, now, string(items), reason)
	if err != nil {
		return "", fmt.Errorf("enqueue embed job: %w", err)
	}
	return jobID, nil
}

// Backfill drains queued `embed` jobs: embeds their chunks and stores the
// vectors. It returns how many chunks it embedded. Call it after start-up
// and after a model change.
func (s *Store) Backfill(ctx context.Context) (int, error) {
	if s.embedder == nil {
		return 0, nil
	}
	modelID, err := s.ensureModel(ctx)
	if err != nil {
		return 0, err
	}
	jobs, err := s.pendingEmbedJobs(ctx)
	if err != nil {
		return 0, err
	}

	total := 0
	for _, j := range jobs {
		n, err := s.embedPending(ctx, modelID, j.items)
		total += n
		state, msg := "done", sql.NullString{}
		if err != nil {
			state, msg = "failed", sql.NullString{String: err.Error(), Valid: true}
		}
		if _, uerr := s.db.ExecContext(ctx, `UPDATE jobs SET state = ?, error = ?, updated_at = ? WHERE id = ?`, state, msg, s.now().UnixMilli(), j.id); uerr != nil {
			return total, uerr
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

type embedJob struct {
	id    string
	items []int64
}

func (s *Store) pendingEmbedJobs(ctx context.Context) ([]embedJob, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, items_json FROM jobs WHERE kind = 'embed' AND state IN ('queued','failed') ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []embedJob
	for rows.Next() {
		var j embedJob
		var items string
		if err := rows.Scan(&j.id, &items); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(items), &j.items)
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// embedPending embeds the given chunk ids that still lack a vector for
// modelID.
func (s *Store) embedPending(ctx context.Context, modelID string, ids []int64) (int, error) {
	done := 0
	for start := 0; start < len(ids); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		var batchIDs []int64
		var texts []string
		for _, id := range ids[start:end] {
			var text, header string
			var have int
			err := s.db.QueryRowContext(ctx, `SELECT c.text, c.context_header, (SELECT COUNT(*) FROM chunk_vecs v WHERE v.chunk_id = c.id AND v.model_id = ?) FROM chunks c WHERE c.id = ?`, modelID, id).Scan(&text, &header, &have)
			if errors.Is(err, sql.ErrNoRows) || have > 0 {
				continue // chunk gone (document revised) or already embedded
			}
			if err != nil {
				return done, err
			}
			batchIDs = append(batchIDs, id)
			if header != "" {
				text = header + "\n" + text
			}
			texts = append(texts, text)
		}
		if len(batchIDs) == 0 {
			continue
		}
		vecs, err := s.embedder.EmbedBatch(ctx, texts, embedding.RoleDocument)
		if err != nil {
			return done, err
		}
		if err := s.storeVectors(ctx, modelID, batchIDs, vecs); err != nil {
			return done, err
		}
		done += len(batchIDs)
	}
	return done, nil
}

func writeAudit(ctx context.Context, tx *sql.Tx, ts int64, actor, channel, op, target string, detail map[string]any) error {
	if actor == "" {
		actor = channel
	}
	d, _ := json.Marshal(detail)
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit(ts, actor, channel, op, target_uri, detail_json) VALUES (?,?,?,?,?,?)`, ts, actor, channel, op, target, string(d)); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

func ensureNamespace(ctx context.Context, tx *sql.Tx, name string, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO namespaces(name, created_at) VALUES (?, ?) ON CONFLICT(name) DO NOTHING`, name, now)
	return err
}

// normalise makes content byte-stable: LF line endings, trailing
// whitespace stripped from lines, exactly one trailing newline.
func normalise(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EncodeVector stores a float32 vector as the little-endian blob sqlite-vec
// reads directly.
func EncodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// DecodeVector reverses EncodeVector.
func DecodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func validateEnum(field, got string, allowed ...string) error {
	for _, a := range allowed {
		if got == a {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s, got %q", field, strings.Join(allowed, "|"), got)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullDuration(d time.Duration) any {
	if d <= 0 {
		return nil
	}
	return int64(d / time.Second)
}

func nullEq(n sql.NullString, s string) bool {
	if !n.Valid {
		return s == ""
	}
	return n.String == s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullStr / nullInt scan NULL into "" / 0.
type nullStr struct{ p *string }

func (n *nullStr) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*n.p = ""
	case string:
		*n.p = x
	case []byte:
		*n.p = string(x)
	default:
		return fmt.Errorf("nullStr: unexpected %T", v)
	}
	return nil
}

type nullInt struct{ p *int }

func (n *nullInt) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*n.p = 0
	case int64:
		*n.p = int(x)
	default:
		return fmt.Errorf("nullInt: unexpected %T", v)
	}
	return nil
}

// InstalledModel is a row of the models table with its vector count.
type InstalledModel struct {
	embedding.ModelInfo
	IsDefault   bool
	Vectors     int
	InstalledAt time.Time
}

// InstalledModels lists the models that have rows in this knowledge base.
func (s *Store) InstalledModels(ctx context.Context) ([]InstalledModel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.name, m.hf_repo, m.dim, m.max_tokens, m.licence, m.is_default, m.installed_at,
		(SELECT COUNT(*) FROM chunk_vecs v WHERE v.model_id = m.id) FROM models m ORDER BY m.is_default DESC, m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InstalledModel
	for rows.Next() {
		var m InstalledModel
		var def int
		var at int64
		if err := rows.Scan(&m.ID, &m.Name, &m.HFRepo, &m.Dim, &m.MaxTokens, &m.Licence, &def, &at, &m.Vectors); err != nil {
			return nil, err
		}
		m.IsDefault = def == 1
		m.InstalledAt = time.UnixMilli(at).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetDefaultModel makes modelID the model queries use. The model's row must
// exist (it is created the first time that embedder stores a vector, or by
// Reindex). Vectors for other models are kept, so switching back is free.
func (s *Store) SetDefaultModel(ctx context.Context, modelID, actor, channel string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM models WHERE id = ?`, modelID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("model %q has no vectors here yet; run `memo-mcp reindex --model %s` first", modelID, modelID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE models SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END`, modelID); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, s.now().UnixMilli(), actor, channel, "model-use", "memo://model/"+modelID, map[string]any{"model": modelID}); err != nil {
		return err
	}
	return tx.Commit()
}

// Reindex embeds every live chunk that lacks a vector for the configured
// embedder's model ("re-embed, don't re-chunk"), creating the model row if
// needed. It returns how many vectors it wrote. Progress is resumable: a
// second run only does what the first left undone.
func (s *Store) Reindex(ctx context.Context, progress func(done, total int)) (int, error) {
	if s.embedder == nil {
		return 0, errors.New("reindex needs an embedding model")
	}
	modelID, err := s.ensureModel(ctx)
	if err != nil {
		return 0, err
	}
	missing, err := s.missingVectors(ctx, modelID)
	if err != nil {
		return 0, err
	}
	done := 0
	for start := 0; start < len(missing); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(missing) {
			end = len(missing)
		}
		n, err := s.embedPending(ctx, modelID, missing[start:end])
		done += n
		if progress != nil {
			progress(done, len(missing))
		}
		if err != nil {
			return done, err
		}
	}
	return done, nil
}

// SetNow replaces the clock (tests, eval fixtures with explicit dates).
func (s *Store) SetNow(f func() time.Time) { s.now = f }
