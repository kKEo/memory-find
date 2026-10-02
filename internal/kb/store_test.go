package kb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/kKEo/memory-find/internal/embedding"
)

func openTestStore(t *testing.T, emb embedding.Embedder) (*Store, *sql.DB) {
	t.Helper()
	db, err := Open(context.Background(), t.TempDir(), "t", Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewStore(db, emb), db
}

func longDoc() string {
	var sb strings.Builder
	sb.WriteString("# gRPC Interceptors\n\nIntro paragraph about interceptors in grpc-go.\n\n## Ordering\n\n")
	for i := 0; i < 40; i++ {
		sb.WriteString("Interceptors run in registration order; the auth interceptor should come before logging so denied calls are not logged twice. ")
	}
	sb.WriteString("\n\n## Errors\n\nWhen a handler returns ERR_CONN_RESET the client sees codes.Unavailable.\n")
	return sb.String()
}

func docInput(ns, uri, content string) IngestInput {
	return IngestInput{
		Namespace: ns, Content: content,
		Source: SourceInput{URI: uri, Title: "gRPC Interceptors", Kind: KindDoc, Library: "grpc/grpc-go", Version: "v1.8.0", Origin: OriginWeb},
		Trust:  TrustAgent, Actor: "test-client", Channel: ChannelTool,
	}
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestIngestStoresChunksVectorsAndAudit(t *testing.T) {
	s, db := openTestStore(t, embedding.NewHashEmbedder(32))
	res, err := s.Ingest(context.Background(), docInput("grpc", "https://example.com/interceptors", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Dedup || res.Revision != 1 || res.Chunks < 3 || res.Embedded != res.Chunks || res.Pending != 0 {
		t.Fatalf("unexpected result %+v", res)
	}
	if !strings.HasPrefix(res.URI, "memo://doc/") {
		t.Errorf("uri %q", res.URI)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM chunks WHERE document_id = ?`, res.DocumentID); n != res.Chunks {
		t.Errorf("chunks in db %d != %d", n, res.Chunks)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM chunk_vecs WHERE model_id = 'hash'`); n != res.Chunks {
		t.Errorf("vectors %d != %d", n, res.Chunks)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM models WHERE id = 'hash' AND is_default = 1`); n != 1 {
		t.Errorf("model row missing or not default")
	}
	// Every chunk is at most 400 estimated tokens and carries its header.
	rows, err := db.Query(`SELECT est_tokens, context_header, section_path FROM chunks`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var est int
		var header, section string
		if err := rows.Scan(&est, &header, &section); err != nil {
			t.Fatal(err)
		}
		if est > 400 {
			t.Errorf("chunk over cap: %d", est)
		}
		if !strings.HasPrefix(header, "gRPC Interceptors") {
			t.Errorf("header %q lacks title", header)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// Keyword indexes see the content; the exact index keeps the identifier.
	if n := count(t, db, `SELECT COUNT(*) FROM chunks_fts WHERE chunks_fts MATCH 'interceptor'`); n == 0 {
		t.Error("stemmed index empty")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM chunks_fts_exact WHERE chunks_fts_exact MATCH '"ERR_CONN_RESET"'`); n != 1 {
		t.Errorf("exact index: %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit WHERE op = 'ingest' AND channel = 'tool' AND target_uri = ?`, res.URI); n != 1 {
		t.Errorf("audit rows: %d", n)
	}
}

func TestIngestDedupsIdenticalContentAndRevisesChanged(t *testing.T) {
	s, db := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	first, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()+"   \n\n")) // normalises to the same bytes
	if err != nil {
		t.Fatal(err)
	}
	if !again.Dedup || again.DocumentID != first.DocumentID {
		t.Fatalf("expected dedup, got %+v", again)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM documents`); n != 1 {
		t.Fatalf("documents after dedup: %d", n)
	}

	changed, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()+"\n## Changelog\n\nAdded SetCacheable.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Dedup || changed.Revision != 2 || changed.SourceID != first.SourceID {
		t.Fatalf("expected revision 2 of the same source, got %+v", changed)
	}
	var superseded string
	if err := db.QueryRow(`SELECT superseded_by FROM documents WHERE id = ?`, first.DocumentID).Scan(&superseded); err != nil || superseded != changed.DocumentID {
		t.Fatalf("revision 1 not superseded by revision 2: %q %v", superseded, err)
	}
	// Old revision's chunks stay (history) but only the new revision is live.
	if n := count(t, db, `SELECT COUNT(*) FROM documents WHERE deleted_at IS NULL AND superseded_by IS NULL`); n != 1 {
		t.Errorf("live documents: %d", n)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit WHERE op = 'revise'`); n != 1 {
		t.Errorf("revise audit rows: %d", n)
	}
}

// A new version of the same URL is a new revision even when the text is
// identical (owner decision: versions are revisions).
func TestIngestNewVersionIsNewRevision(t *testing.T) {
	s, _ := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	in := docInput("grpc", "https://example.com/i", longDoc())
	if _, err := s.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Source.Version = "v1.9.0"
	res, err := s.Ingest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Dedup || res.Revision != 2 {
		t.Fatalf("expected revision 2 for the new version, got %+v", res)
	}
}

func TestNoteWithoutURIAndExplicitUpdate(t *testing.T) {
	s, db := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	note := IngestInput{Namespace: "personal", Content: "Remember: run make check before pushing.\n",
		Source: SourceInput{Title: "Workflow note", Kind: KindNote, Origin: OriginUserSaid}, Trust: TrustUser, Channel: ChannelCLI}
	first, err := s.Ingest(ctx, note)
	if err != nil {
		t.Fatal(err)
	}
	// A second note with the same text is a different note (no URI to match on).
	second, err := s.Ingest(ctx, note)
	if err != nil || second.Dedup || second.SourceID == first.SourceID {
		t.Fatalf("second note should be independent: %+v %v", second, err)
	}
	// Updating names the document explicitly.
	upd := note
	upd.DocumentID = first.DocumentID
	upd.Content = "Remember: run make check AND make eval before pushing.\n"
	updated, err := s.Ingest(ctx, upd)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SourceID != first.SourceID || updated.Revision != 2 {
		t.Fatalf("expected revision 2 of the first note, got %+v", updated)
	}
	var uri sql.NullString
	if err := db.QueryRow(`SELECT uri FROM sources WHERE id = ?`, first.SourceID).Scan(&uri); err != nil {
		t.Fatal(err)
	}
	if uri.Valid {
		t.Errorf("note source should have NULL uri, got %q", uri.String)
	}
}

func TestIngestWithoutEmbedderQueuesJobAndBackfillDrainsIt(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir(), "t", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	noEmb := NewStore(db, nil)
	res, err := noEmb.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedded != 0 || res.Pending != res.Chunks || res.JobID == "" {
		t.Fatalf("expected everything pending with a job, got %+v", res)
	}
	if n := count(t, db, `SELECT COUNT(*) FROM jobs WHERE kind = 'embed' AND state = 'queued'`); n != 1 {
		t.Fatalf("queued jobs: %d", n)
	}

	// Later, with a working embedder, the backlog drains.
	withEmb := NewStore(db, embedding.NewHashEmbedder(32))
	n, err := withEmb.Backfill(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != res.Chunks {
		t.Fatalf("backfilled %d, want %d", n, res.Chunks)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM chunk_vecs`); got != res.Chunks {
		t.Fatalf("vectors after backfill: %d", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM jobs WHERE state = 'done'`); got != 1 {
		t.Fatalf("job not marked done")
	}
}

func TestIngestEmbedFailureIsNeverSilent(t *testing.T) {
	s, db := openTestStore(t, embedding.NewFailingEmbedder(errors.New("model exploded")))
	res, err := s.Ingest(context.Background(), docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != res.Chunks || res.JobID == "" {
		t.Fatalf("failure must leave a job and a pending count: %+v", res)
	}
	var reason string
	if err := db.QueryRow(`SELECT error FROM jobs WHERE id = ?`, res.JobID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reason, "model exploded") {
		t.Errorf("job error = %q", reason)
	}
}

func TestIngestValidation(t *testing.T) {
	s, _ := openTestStore(t, nil)
	bad := []IngestInput{
		{Namespace: "../x", Content: "x", Source: SourceInput{Kind: KindDoc, Origin: OriginWeb}, Trust: TrustAgent, Channel: ChannelTool},
		{Namespace: "ok", Content: "x", Source: SourceInput{Kind: "wiki", Origin: OriginWeb}, Trust: TrustAgent, Channel: ChannelTool},
		{Namespace: "ok", Content: "x", Source: SourceInput{Kind: KindDoc, Origin: OriginWeb}, Trust: "curated-by-me", Channel: ChannelTool},
		{Namespace: "ok", Content: "   ", Source: SourceInput{Kind: KindDoc, Origin: OriginWeb}, Trust: TrustAgent, Channel: ChannelTool},
	}
	for i, in := range bad {
		if _, err := s.Ingest(context.Background(), in); err == nil {
			t.Errorf("input %d accepted", i)
		}
	}
}

func TestVectorRoundTrip(t *testing.T) {
	v := []float32{0.5, -1.25, 3}
	got := DecodeVector(EncodeVector(v))
	for i := range v {
		if got[i] != v[i] {
			t.Fatal("round trip mismatch")
		}
	}
}

// Switching models: vectors for several models coexist, reindex fills the
// gaps for the configured embedder, and the default is an explicit choice.
func TestReindexAndSetDefaultModel(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir(), "t", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	a := NewStore(db, embedding.NewHashEmbedder(32))
	res, err := a.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	// A second embedder with a different model id and dimension.
	b := NewStore(db, &namedEmbedder{HashEmbedder: embedding.NewHashEmbedder(16), id: "other"})
	n, err := b.Reindex(ctx, nil)
	if err != nil || n != res.Chunks {
		t.Fatalf("reindex: %d %v", n, err)
	}
	if n, _ = b.Reindex(ctx, nil); n != 0 {
		t.Fatalf("second reindex should do nothing, did %d", n)
	}
	models, err := a.InstalledModels(ctx)
	if err != nil || len(models) != 2 {
		t.Fatalf("installed models: %+v %v", models, err)
	}
	if models[0].ID != "hash" || !models[0].IsDefault || models[0].Vectors != res.Chunks {
		t.Fatalf("default should still be hash: %+v", models[0])
	}
	if err := a.SetDefaultModel(ctx, "other", "test", ChannelCLI); err != nil {
		t.Fatal(err)
	}
	if id, _ := a.DefaultModelID(ctx); id != "other" {
		t.Fatalf("default = %q", id)
	}
	if err := a.SetDefaultModel(ctx, "unknown", "test", ChannelCLI); err == nil {
		t.Fatal("unknown model accepted as default")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit WHERE op = 'model-use'`); n != 1 {
		t.Fatalf("audit rows for model-use: %d", n)
	}
}

type namedEmbedder struct {
	*embedding.HashEmbedder
	id string
}

func (n *namedEmbedder) Info() embedding.ModelInfo {
	i := n.HashEmbedder.Info()
	i.ID = n.id
	return i
}
