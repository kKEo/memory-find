package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned when an address resolves to nothing.
var ErrNotFound = errors.New("not found")

// Forgotten reports that a record exists but was deliberately removed from
// service; its message carries when and why, so a stale reference resolves
// to something meaningful rather than "not found".
type Forgotten struct {
	URI    string
	At     time.Time
	Reason string
}

func (f *Forgotten) Error() string {
	return fmt.Sprintf("%s was forgotten on %s: %s", f.URI, f.At.UTC().Format("2006-01-02"), f.Reason)
}

// Provenance is the chain from a record back to its source.
type Provenance struct {
	SourceURI string    `json:"source_uri,omitempty"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Library   string    `json:"library,omitempty"`
	Version   string    `json:"version,omitempty"`
	FetchedAt time.Time `json:"fetched_at"`
	Trust     string    `json:"trust"`
	Origin    string    `json:"origin"`
	Namespace string    `json:"namespace"`
	Revision  int       `json:"revision"`
	Hash      string    `json:"content_hash"`
	Tags      string    `json:"tags,omitempty"`
}

// Document is a read result at document granularity.
type Document struct {
	ID         string
	URI        string
	SourceID   string
	Content    string
	Context    string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Superseded string // id of the newer revision, if any
	Prov       Provenance
}

// ChunkRead is a read result at chunk granularity.
type ChunkRead struct {
	ID            int64
	URI           string
	DocumentID    string
	DocumentURI   string
	Ord           int
	SectionPath   string
	Text          string
	ContextHeader string
	EstTokens     int
	Lang          string
	Prov          Provenance
}

// ParseURI splits a memo:// address into kind and id.
func ParseURI(uri string) (kind, id string, err error) {
	rest, ok := strings.CutPrefix(uri, "memo://")
	if !ok {
		return "", "", fmt.Errorf("not a memo:// address: %q", uri)
	}
	kind, id, ok = strings.Cut(rest, "/")
	if !ok || id == "" {
		return "", "", fmt.Errorf("malformed address: %q", uri)
	}
	return kind, id, nil
}

const provSelect = `s.uri, s.title, s.kind, s.library, d.version, s.fetched_at, s.trust, s.origin, s.namespace, d.revision, s.content_hash, s.tags_json`

func scanProv(p *Provenance, uri, library, version *sql.NullString, fetched *int64) []any {
	return []any{uri, &p.Title, &p.Kind, library, version, fetched, &p.Trust, &p.Origin, &p.Namespace, &p.Revision, &p.Hash, &p.Tags}
}

func finishProv(p *Provenance, uri, library, version sql.NullString, fetched int64) {
	p.SourceURI, p.Library, p.Version = uri.String, library.String, version.String
	p.FetchedAt = time.UnixMilli(fetched).UTC()
	if p.Tags == "[]" {
		p.Tags = ""
	}
}

// ReadDocument returns a document by id (any revision). A tombstoned
// document returns *Forgotten.
func (s *Store) ReadDocument(ctx context.Context, id string) (*Document, error) {
	var d Document
	var uri, library, version, ctxt, superseded, delReason sql.NullString
	var fetched, created, updated int64
	var deleted sql.NullInt64
	dest := []any{&d.ID, &d.SourceID, &d.Content, &ctxt, &created, &updated, &deleted, &delReason, &superseded}
	dest = append(dest, scanProv(&d.Prov, &uri, &library, &version, &fetched)...)
	err := s.db.QueryRowContext(ctx, `SELECT d.id, d.source_id, d.content, d.context, d.created_at, d.updated_at, d.deleted_at, d.deleted_reason, d.superseded_by, `+provSelect+`
		FROM documents d JOIN sources s ON s.id = d.source_id WHERE d.id = ?`, id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memo://doc/%s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	d.URI = "memo://doc/" + d.ID
	if deleted.Valid {
		return nil, &Forgotten{URI: d.URI, At: time.UnixMilli(deleted.Int64), Reason: delReason.String}
	}
	d.Context, d.Superseded = ctxt.String, superseded.String
	d.CreatedAt, d.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	finishProv(&d.Prov, uri, library, version, fetched)
	return &d, nil
}

// ReadChunk returns one chunk with its provenance.
func (s *Store) ReadChunk(ctx context.Context, id int64) (*ChunkRead, error) {
	var c ChunkRead
	var uri, library, version, lang sql.NullString
	var fetched int64
	var deleted sql.NullInt64
	var delReason sql.NullString
	dest := []any{&c.ID, &c.DocumentID, &c.Ord, &c.SectionPath, &c.Text, &c.ContextHeader, &c.EstTokens, &lang, &deleted, &delReason}
	dest = append(dest, scanProv(&c.Prov, &uri, &library, &version, &fetched)...)
	err := s.db.QueryRowContext(ctx, `SELECT c.id, c.document_id, c.ord, c.section_path, c.text, c.context_header, c.est_tokens, c.lang, d.deleted_at, d.deleted_reason, `+provSelect+`
		FROM chunks c JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id WHERE c.id = ?`, id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memo://chunk/%d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	c.URI = fmt.Sprintf("memo://chunk/%d", c.ID)
	c.DocumentURI = "memo://doc/" + c.DocumentID
	if deleted.Valid {
		return nil, &Forgotten{URI: c.DocumentURI, At: time.UnixMilli(deleted.Int64), Reason: delReason.String}
	}
	c.Lang = lang.String
	finishProv(&c.Prov, uri, library, version, fetched)
	return &c, nil
}

// Read dereferences any memo:// address to text plus provenance. It is what
// the `read` tool and CLI command call.
func (s *Store) Read(ctx context.Context, uri string) (text string, prov Provenance, err error) {
	kind, id, err := ParseURI(uri)
	if err != nil {
		return "", Provenance{}, err
	}
	switch kind {
	case "doc":
		d, err := s.ReadDocument(ctx, id)
		if err != nil {
			return "", Provenance{}, err
		}
		return d.Content, d.Prov, nil
	case "chunk":
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return "", Provenance{}, fmt.Errorf("chunk id %q is not a number", id)
		}
		c, err := s.ReadChunk(ctx, n)
		if err != nil {
			return "", Provenance{}, err
		}
		return c.Text, c.Prov, nil
	case "source":
		d, err := s.latestDocumentOfSource(ctx, id)
		if err != nil {
			return "", Provenance{}, err
		}
		return d.Content, d.Prov, nil
	default:
		return "", Provenance{}, fmt.Errorf("unsupported address kind %q in %s", kind, uri)
	}
}

func (s *Store) latestDocumentOfSource(ctx context.Context, sourceID string) (*Document, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM documents WHERE source_id = ? ORDER BY revision DESC LIMIT 1`, sourceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("memo://source/%s: %w", sourceID, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	return s.ReadDocument(ctx, id)
}

// ListOptions filters List.
type ListOptions struct {
	Namespace string
	Kind      string
	Since     time.Time
	Limit     int
}

// ListEntry is one row of List: the live revision of a source.
type ListEntry struct {
	DocumentID string
	URI        string
	Title      string
	Kind       string
	Namespace  string
	Library    string
	Version    string
	Revision   int
	Chunks     int
	UpdatedAt  time.Time
	Trust      string
}

// List returns live documents, newest first.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]ListEntry, error) {
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	q := `SELECT d.id, s.title, s.kind, s.namespace, s.library, d.version, d.revision, d.updated_at, s.trust,
	        (SELECT COUNT(*) FROM chunks c WHERE c.document_id = d.id)
	      FROM documents d JOIN sources s ON s.id = d.source_id
	      WHERE d.deleted_at IS NULL AND d.superseded_by IS NULL`
	var args []any
	if opts.Namespace != "" {
		q += ` AND s.namespace = ?`
		args = append(args, opts.Namespace)
	}
	if opts.Kind != "" {
		q += ` AND s.kind = ?`
		args = append(args, opts.Kind)
	}
	if !opts.Since.IsZero() {
		q += ` AND d.updated_at >= ?`
		args = append(args, opts.Since.UnixMilli())
	}
	q += ` ORDER BY d.updated_at DESC LIMIT ?`
	args = append(args, opts.Limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ListEntry
	for rows.Next() {
		var e ListEntry
		var library, version sql.NullString
		var updated int64
		if err := rows.Scan(&e.DocumentID, &e.Title, &e.Kind, &e.Namespace, &library, &version, &e.Revision, &updated, &e.Trust, &e.Chunks); err != nil {
			return nil, err
		}
		e.Library, e.Version = library.String, version.String
		e.UpdatedAt = time.UnixMilli(updated).UTC()
		e.URI = "memo://doc/" + e.DocumentID
		out = append(out, e)
	}
	return out, rows.Err()
}

// Status is what `memo-mcp status` and the `status` tool report.
type Status struct {
	Path              string
	SizeMB            float64
	SchemaVersion     int
	Namespaces        []NamespaceStat
	Sources           int
	LiveDocuments     int
	Revisions         int
	Chunks            int
	Facts             int
	DefaultModel      string
	PendingEmbeddings map[string]int // model id → chunks without a vector
	JobsQueued        int
	JobsFailed        int
	LastWrite         time.Time
}

// NamespaceStat is a per-namespace row in Status.
type NamespaceStat struct {
	Name        string
	Description string
	Documents   int
	Chunks      int
}

// Status gathers counts in one read transaction so they are consistent.
func (s *Store) Status(ctx context.Context) (*Status, error) {
	st := &Status{PendingEmbeddings: map[string]int{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	one := func(dst any, q string, args ...any) error {
		return tx.QueryRowContext(ctx, q, args...).Scan(dst)
	}
	if err := one(&st.SchemaVersion, `PRAGMA user_version`); err != nil {
		return nil, err
	}
	if err := one(&st.Sources, `SELECT COUNT(*) FROM sources`); err != nil {
		return nil, err
	}
	if err := one(&st.LiveDocuments, `SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL AND superseded_by IS NULL`); err != nil {
		return nil, err
	}
	if err := one(&st.Revisions, `SELECT COUNT(*) FROM documents`); err != nil {
		return nil, err
	}
	if err := one(&st.Chunks, `SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id = c.document_id WHERE d.deleted_at IS NULL AND d.superseded_by IS NULL`); err != nil {
		return nil, err
	}
	if err := one(&st.Facts, `SELECT COUNT(*) FROM facts WHERE deleted_at IS NULL AND invalidated_at IS NULL`); err != nil {
		return nil, err
	}
	if err := one(&st.JobsQueued, `SELECT COUNT(*) FROM jobs WHERE state IN ('queued','running')`); err != nil {
		return nil, err
	}
	if err := one(&st.JobsFailed, `SELECT COUNT(*) FROM jobs WHERE state = 'failed'`); err != nil {
		return nil, err
	}
	var last sql.NullInt64
	if err := one(&last, `SELECT MAX(ts) FROM audit`); err != nil {
		return nil, err
	}
	if last.Valid {
		st.LastWrite = time.UnixMilli(last.Int64).UTC()
	}
	var def sql.NullString
	if err := one(&def, `SELECT id FROM models WHERE is_default = 1 LIMIT 1`); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	st.DefaultModel = def.String

	if err := pendingByModel(ctx, tx, st.PendingEmbeddings); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `SELECT n.name, n.description,
		(SELECT COUNT(*) FROM documents d JOIN sources s ON s.id = d.source_id WHERE s.namespace = n.name AND d.deleted_at IS NULL AND d.superseded_by IS NULL),
		(SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id WHERE s.namespace = n.name AND d.deleted_at IS NULL AND d.superseded_by IS NULL)
		FROM namespaces n ORDER BY n.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ns NamespaceStat
		if err := rows.Scan(&ns.Name, &ns.Description, &ns.Documents, &ns.Chunks); err != nil {
			return nil, err
		}
		st.Namespaces = append(st.Namespaces, ns)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var path string
	if err := one(&path, `SELECT file FROM pragma_database_list() WHERE name = 'main'`); err == nil {
		st.Path = path
		if info, err := os.Stat(path); err == nil {
			st.SizeMB = float64(info.Size()) / (1024 * 1024)
		}
	}
	return st, nil
}

func pendingByModel(ctx context.Context, tx *sql.Tx, out map[string]int) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.id,
		(SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id = c.document_id
		   WHERE d.deleted_at IS NULL AND d.superseded_by IS NULL
		     AND NOT EXISTS (SELECT 1 FROM chunk_vecs v WHERE v.chunk_id = c.id AND v.model_id = m.id))
		FROM models m`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		out[id] = n
	}
	return rows.Err()
}

// missingVectors lists live chunks without a vector for modelID.
func (s *Store) missingVectors(ctx context.Context, modelID string) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id FROM chunks c JOIN documents d ON d.id = c.document_id
			WHERE d.deleted_at IS NULL AND d.superseded_by IS NULL
			  AND NOT EXISTS (SELECT 1 FROM chunk_vecs v WHERE v.chunk_id = c.id AND v.model_id = ?)`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var missing []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		missing = append(missing, id)
	}
	return missing, rows.Err()
}

// VerifyReport lists integrity problems. An empty report means clean.
type VerifyReport struct {
	Problems []string
	Repaired []string
}

// Clean reports whether nothing was found.
func (r *VerifyReport) Clean() bool { return len(r.Problems) == 0 }

// Verify checks the store's integrity: documents without chunks, chunks
// without vectors for the default model, vectors whose chunk is gone,
// facts whose evidence is gone, and the FTS indexes' own integrity check.
// With repair, it re-queues missing vectors as an embed job.
func (s *Store) Verify(ctx context.Context, repair bool) (*VerifyReport, error) {
	r := &VerifyReport{}
	add := func(format string, args ...any) { r.Problems = append(r.Problems, fmt.Sprintf(format, args...)) }

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d WHERE d.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM chunks c WHERE c.document_id = d.id)`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		add("%d live document(s) have no chunks", n)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunk_vecs v WHERE NOT EXISTS (SELECT 1 FROM chunks c WHERE c.id = v.chunk_id)`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		add("%d vector(s) point at missing chunks", n)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM facts f WHERE f.evidence_chunk_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM chunks c WHERE c.id = f.evidence_chunk_id)`).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		add("%d fact(s) point at missing evidence chunks", n)
	}
	modelID, err := s.DefaultModelID(ctx)
	if err != nil {
		return nil, err
	}
	if modelID != "" {
		missing, err := s.missingVectors(ctx, modelID)
		if err != nil {
			return nil, err
		}
		if len(missing) > 0 {
			add("%d live chunk(s) have no vector for model %s", len(missing), modelID)
			if repair {
				if _, err := s.enqueueEmbed(ctx, missing, "verify --repair"); err != nil {
					return nil, err
				}
				r.Repaired = append(r.Repaired, fmt.Sprintf("queued an embed job for %d chunk(s)", len(missing)))
			}
		}
	}
	for _, t := range []string{"chunks_fts", "chunks_fts_exact", "facts_fts"} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(%s) VALUES ('integrity-check')`, t, t)); err != nil {
			add("%s failed integrity-check: %v", t, err)
			if repair {
				if _, err := s.db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s(%s) VALUES ('rebuild')`, t, t)); err == nil {
					r.Repaired = append(r.Repaired, "rebuilt "+t)
				}
			}
		}
	}
	return r, nil
}
