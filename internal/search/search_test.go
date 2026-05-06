package search

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
)

type mockEmbedder struct {
	vec []float32
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, nil
}

var _ embedding.Embedder = (*mockEmbedder)(nil)

func fixedVec(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(i) * 0.001
	}
	return v
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := journal.InitDB(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedEntries(t *testing.T, db *sql.DB, emb embedding.Embedder) {
	t.Helper()
	mgr := journal.NewManager(db, emb)

	entries := []journal.ThoughtInput{
		{Reflections: "I feel frustrated with TypeScript type errors today."},
		{ProjectNotes: "The auth service needs a complete rewrite. Current implementation leaks sessions."},
		{TechnicalInsights: "Dependency injection makes testing much easier in Go."},
	}

	for _, e := range entries {
		if _, err := mgr.WriteThoughts(context.Background(), e); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestSearchReturnsResults(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.Search(context.Background(), "TypeScript frustration", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestListRecent(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.ListRecent(context.Background(), 2, 30)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestReadEntry(t *testing.T) {
	db := testDB(t)
	mgr := journal.NewManager(db, nil)

	id, err := mgr.WriteThoughts(context.Background(), journal.ThoughtInput{
		Reflections: "This is a test entry.",
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(db, nil)
	content, err := svc.ReadEntry(context.Background(), id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestReadEntryNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)

	_, err := svc.ReadEntry(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestReadRecentEntries(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.ReadRecentEntries(context.Background(), 5)
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Content == "" {
			t.Error("expected non-empty content")
		}
	}
}

func TestGenerateExcerpt(t *testing.T) {
	text := "This is a long text about TypeScript frustration and debugging problems in the codebase."

	excerpt := generateExcerpt(text, "TypeScript", 50)
	if excerpt == "" {
		t.Fatal("expected non-empty excerpt")
	}

	excerpt2 := generateExcerpt(text, "", 20)
	if len(excerpt2) > 24 { // 20 + "..."
		t.Errorf("excerpt too long: %q", excerpt2)
	}
}

func TestSectionFilter(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.Search(context.Background(), "test", SearchOptions{
		Limit:    10,
		Sections: []string{"reflections"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	for _, r := range results {
		found := false
		for _, s := range r.Sections {
			if s == "reflections" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected reflections section in result, got %v", r.Sections)
		}
	}
}
