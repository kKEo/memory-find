package kb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kKEo/memory-find/internal/embedding"
)

func TestReadDocumentChunkAndSource(t *testing.T) {
	s, _ := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	res, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	text, prov, err := s.Read(ctx, res.URI)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ERR_CONN_RESET") || prov.SourceURI != "https://example.com/i" || prov.Version != "v1.8.0" || prov.Trust != TrustAgent || prov.Namespace != "grpc" {
		t.Fatalf("read doc: prov %+v", prov)
	}
	ctext, cprov, err := s.Read(ctx, "memo://chunk/1")
	if err != nil {
		t.Fatal(err)
	}
	if ctext == "" || cprov.Title != "gRPC Interceptors" {
		t.Fatalf("read chunk: %q %+v", ctext, cprov)
	}
	if _, _, err := s.Read(ctx, "memo://source/"+res.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Read(ctx, "memo://doc/nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing doc err = %v", err)
	}
	if _, _, err := s.Read(ctx, "https://not-memo"); err == nil {
		t.Fatal("non-memo uri accepted")
	}
}

func TestListNewestFirstWithFilters(t *testing.T) {
	s, _ := openTestStore(t, nil)
	ctx := context.Background()
	if _, err := s.Ingest(ctx, docInput("grpc", "https://example.com/a", "# A\n\nalpha\n")); err != nil {
		t.Fatal(err)
	}
	note := IngestInput{Namespace: "personal", Content: "a note\n", Source: SourceInput{Title: "Note", Kind: KindNote, Origin: OriginUserSaid}, Trust: TrustUser, Channel: ChannelCLI}
	if _, err := s.Ingest(ctx, note); err != nil {
		t.Fatal(err)
	}
	all, err := s.List(ctx, ListOptions{})
	if err != nil || len(all) != 2 {
		t.Fatalf("list all: %d %v", len(all), err)
	}
	if all[0].Kind != KindNote { // newest first
		t.Errorf("order: %+v", all)
	}
	notes, _ := s.List(ctx, ListOptions{Kind: KindNote})
	if len(notes) != 1 || notes[0].Namespace != "personal" {
		t.Errorf("kind filter: %+v", notes)
	}
	ns, _ := s.List(ctx, ListOptions{Namespace: "grpc"})
	if len(ns) != 1 || ns[0].Title != "gRPC Interceptors" {
		t.Errorf("namespace filter: %+v", ns)
	}
}

func TestStatusAndVerify(t *testing.T) {
	s, _ := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	res, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.SchemaVersion != 4 || st.Sources != 1 || st.LiveDocuments != 1 || st.Chunks != res.Chunks || st.DefaultModel != "hash" || st.PendingEmbeddings["hash"] != 0 || len(st.Namespaces) != 1 {
		t.Fatalf("status %+v", st)
	}
	rep, err := s.Verify(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Clean() {
		t.Fatalf("expected clean verify, got %v", rep.Problems)
	}

	// Knock out a vector and check verify notices and repairs it.
	if _, err := s.db.Exec(`DELETE FROM chunk_vecs WHERE chunk_id = 1`); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(ctx)
	if st.PendingEmbeddings["hash"] != 1 {
		t.Errorf("pending after delete = %d", st.PendingEmbeddings["hash"])
	}
	rep, _ = s.Verify(ctx, true)
	if rep.Clean() || len(rep.Repaired) != 1 {
		t.Fatalf("verify --repair: %+v", rep)
	}
	if n, err := s.Backfill(ctx); err != nil || n != 1 {
		t.Fatalf("backfill after repair: %d %v", n, err)
	}
	if rep, _ = s.Verify(ctx, false); !rep.Clean() {
		t.Fatalf("still dirty: %v", rep.Problems)
	}
}

func TestExportRoundTripYieldsNoNewRevisions(t *testing.T) {
	s, db := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	if _, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc())); err != nil {
		t.Fatal(err)
	}
	note := IngestInput{Namespace: "personal", Content: "# Workflow\n\nRun make check: always.\n", Source: SourceInput{Title: "Workflow: notes", Kind: KindNote, Origin: OriginUserSaid}, Trust: TrustUser, Channel: ChannelCLI}
	if _, err := s.Ingest(ctx, note); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	res, err := s.Export(ctx, dir, ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 2 || len(res.Namespaces) != 2 {
		t.Fatalf("export: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "grpc", "_index.md")); err != nil {
		t.Fatal("index missing")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*", "*", "*.md"))
	if len(files) != 2 {
		t.Fatalf("files: %v", files)
	}
	before := count(t, db, `SELECT COUNT(*) FROM documents`)
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		fm, body := ParseExported(string(raw))
		if fm.MemoURI == "" || fm.Namespace == "" {
			t.Fatalf("front matter not parsed in %s: %+v", f, fm)
		}
		_, id, _ := ParseURI(fm.MemoURI)
		in := IngestInput{Namespace: fm.Namespace, Content: body, DocumentID: id,
			Source: SourceInput{URI: fm.SourceURI, Title: fm.Title, Kind: fm.Kind, Library: fm.Library, Version: fm.Version, Origin: fm.Origin},
			Trust:  TrustUser, Channel: ChannelCLI, Context: fm.Context}
		r, err := s.Ingest(ctx, in)
		if err != nil {
			t.Fatalf("re-import %s: %v", f, err)
		}
		if !r.Dedup {
			t.Errorf("re-import of %s created revision %d", f, r.Revision)
		}
	}
	if after := count(t, db, `SELECT COUNT(*) FROM documents`); after != before {
		t.Fatalf("documents grew from %d to %d", before, after)
	}
}

func TestParseExportedWithoutFrontMatter(t *testing.T) {
	fm, body := ParseExported("# Plain\n\ntext\n")
	if fm.MemoURI != "" || body != "# Plain\n\ntext\n" {
		t.Fatalf("%+v %q", fm, body)
	}
	if slug("gRPC Interceptors: Ordering!") != "grpc-interceptors-ordering" {
		t.Fatal(slug("gRPC Interceptors: Ordering!"))
	}
}
