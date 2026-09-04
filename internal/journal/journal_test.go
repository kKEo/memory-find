package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestWriteThoughtsIDIsUUIDv7(t *testing.T) {
	db := testDB(t)
	mgr := NewManager(db, nil)

	id, err := mgr.WriteThoughts(context.Background(), ThoughtInput{Observations: "id format check"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
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

// lengthLimitedEmbedder simulates the real embedding backend's behavior on
// oversized input: the pure-Go WordPiece tokenizer path doesn't clamp long
// input itself, so text beyond the model's word-piece limit reaches the
// ONNX graph and errors there. This stands in for that failure mode
// without needing the actual ~90MB model.
type lengthLimitedEmbedder struct {
	maxRunes int
}

func (e *lengthLimitedEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if len([]rune(text)) > e.maxRunes {
		return nil, fmt.Errorf("simulated tokenizer overflow: input has %d runes, limit is %d", len([]rune(text)), e.maxRunes)
	}
	return fixedVec(384), nil
}

// TestLongEntryStillGetsEmbedded is the regression test for the headline
// bug this project shipped with: real journal entries are commonly
// 5000-6000+ characters (multiple sections in one process_thoughts call),
// which is enough to overflow the embedding model's input limit and fail
// silently — the entry saves, but with no embedding, so it is permanently
// invisible to vector search. truncateForEmbedding's interim cap (see
// journal.go) exists specifically to keep that from happening.
func TestLongEntryStillGetsEmbedded(t *testing.T) {
	db := testDB(t)
	// 2000 matches the "well beyond the true ~512 word-piece limit, but
	// still comfortably above maxEmbedInputRunes" zone: if
	// truncateForEmbedding is doing its job, the text this embedder sees
	// is at most maxEmbedInputRunes long and this limit is never hit.
	emb := &lengthLimitedEmbedder{maxRunes: 2000}
	mgr := NewManager(db, emb)

	longSection := strings.Repeat("This entry describes a long debugging session in detail. ", 120) // ~6800 chars
	if len(longSection) < 6000 {
		t.Fatalf("test fixture too short to exercise the bug: %d chars", len(longSection))
	}

	id, err := mgr.WriteThoughts(context.Background(), ThoughtInput{
		ProjectNotes: longSection,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM entry_embeddings WHERE entry_id = ?`, id).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected the long entry to have an embedding row, got %d", count)
	}
}

func TestTruncateForEmbedding(t *testing.T) {
	short := "a short entry"
	if got := truncateForEmbedding(short); got != short {
		t.Errorf("short text should be unchanged, got %q", got)
	}

	long := strings.Repeat("x", maxEmbedInputRunes+500)
	got := truncateForEmbedding(long)
	if len([]rune(got)) != maxEmbedInputRunes {
		t.Errorf("expected truncation to %d runes, got %d", maxEmbedInputRunes, len([]rune(got)))
	}

	// Rune safety: truncating multi-byte text must not split a rune.
	multibyte := strings.Repeat("日本語", maxEmbedInputRunes) // 3x over budget in runes
	gotMB := truncateForEmbedding(multibyte)
	if !utf8.ValidString(gotMB) {
		t.Errorf("truncateForEmbedding produced invalid UTF-8")
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

func TestValidateToken(t *testing.T) {
	cases := []struct {
		token   string
		wantErr bool
	}{
		{"my-project", false},
		{"my_project.v2", false},
		{"a", false},
		{"", true},
		{".", true},
		{"..", true},
		{"../escape", true},
		{"a/b", true},
		{"a\\b", true},
		{"tok en", true},
	}
	for _, c := range cases {
		err := validateToken(c.token)
		if (err != nil) != c.wantErr {
			t.Errorf("validateToken(%q) error = %v, wantErr %v", c.token, err, c.wantErr)
		}
	}
}

func TestOpenDBRejectsPathTraversal(t *testing.T) {
	base := t.TempDir()
	_, err := OpenDB("../escape", base)
	if err == nil {
		t.Fatal("expected error for path-traversal token, got nil")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(base), "escape.db")); statErr == nil {
		t.Fatal("OpenDB created a file outside the storage directory")
	}
}

func TestOpenDBPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits not meaningful on windows")
	}
	base := filepath.Join(t.TempDir(), "storage")
	db, err := OpenDB("perm-check", base)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	dirInfo, err := os.Stat(base)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("storage dir perm = %o, want 0700", perm)
	}

	dbInfo, err := os.Stat(filepath.Join(base, "perm-check.db"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dbInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file perm = %o, want 0600", perm)
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
