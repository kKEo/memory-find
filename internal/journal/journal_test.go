package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

type mockEmbedder struct {
	vec []float32
	err error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, m.err
}

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
	if err := InitDB(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWriteAndReadBack(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	mgr := NewManager(db, emb)

	id, err := mgr.WriteThoughts(context.Background(), ThoughtInput{
		Reflections:  "I noticed patterns in the code today.",
		ProjectNotes: "The auth middleware needs refactoring.",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	var content string
	var sectionsJSON string
	err = db.QueryRow("SELECT content, sections FROM entries WHERE id = ?", id).Scan(&content, &sectionsJSON)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}

	if !contains(content, "I noticed patterns") {
		t.Errorf("content missing reflections text: %s", content)
	}
	if !contains(content, "auth middleware") {
		t.Errorf("content missing project_notes text: %s", content)
	}

	var sections []string
	json.Unmarshal([]byte(sectionsJSON), &sections)
	if len(sections) != 2 {
		t.Errorf("expected 2 sections, got %d: %v", len(sections), sections)
	}
}

func TestWriteNoContent(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil)

	_, err := mgr.WriteThoughts(context.Background(), ThoughtInput{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestWriteWithoutEmbedder(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil)

	id, err := mgr.WriteThoughts(context.Background(), ThoughtInput{
		Observations: "Something interesting.",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty ID")
	}
}

func TestUUIDv7Format(t *testing.T) {
	id := newUUIDv7()
	if len(id) != 36 {
		t.Errorf("expected 36 char UUID, got %d: %s", len(id), id)
	}
	if id[14] != '7' {
		t.Errorf("expected version 7 at position 14, got %c", id[14])
	}
}

func TestFormatMarkdown(t *testing.T) {
	content, sections := formatMarkdown(ThoughtInput{
		Reflections:    "thought one",
		WorldKnowledge: "fact two",
	})
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0] != "reflections" || sections[1] != "world_knowledge" {
		t.Errorf("unexpected sections: %v", sections)
	}
	if !contains(content, "## reflections") || !contains(content, "## world_knowledge") {
		t.Errorf("content missing headers: %s", content)
	}
}

func TestWritePopulatesFTS(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil)

	id, err := mgr.WriteThoughts(context.Background(), ThoughtInput{
		ProjectNotes: "Testing FTS5 population with UUIDv7 identifiers.",
	})
	if err != nil {
		t.Fatal(err)
	}

	var ftsEntryID, ftsContent string
	err = db.QueryRow(`SELECT entry_id, content FROM entries_fts WHERE content MATCH '"UUIDv7"'`).Scan(&ftsEntryID, &ftsContent)
	if err != nil {
		t.Fatalf("FTS query: %v", err)
	}
	if ftsEntryID != id {
		t.Errorf("FTS entry_id = %q, want %q", ftsEntryID, id)
	}
	if !searchString(ftsContent, "UUIDv7") {
		t.Errorf("FTS content missing UUIDv7: %s", ftsContent)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
