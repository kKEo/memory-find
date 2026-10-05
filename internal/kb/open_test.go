package kb

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

func TestOpenCreatesClaimedFileAtVersion1(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(context.Background(), dir, "demo", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var appID int64
	if err := db.QueryRow(`PRAGMA application_id`).Scan(&appID); err != nil {
		t.Fatal(err)
	}
	if appID != ApplicationID {
		t.Fatalf("application_id = %#x, want %#x", appID, ApplicationID)
	}
	v, err := SchemaVersion(context.Background(), db)
	if err != nil || v != 5 {
		t.Fatalf("user_version = %d (%v), want 5", v, err)
	}
	for _, table := range []string{"namespaces", "models", "jobs", "audit", "query_log", "sources", "documents", "chunks", "chunk_vecs", "facts", "fact_vecs", "chunks_fts", "chunks_fts_exact", "facts_fts", "ingest_runs", "ingest_run_items"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = ?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}
	if info, err := os.Stat(Path(dir, "demo")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file perms: %v %v", info.Mode(), err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		db, err := Open(context.Background(), dir, "demo", Options{})
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db.Close()
	}
}

func TestReadOnlyOpenNeverCreates(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(context.Background(), dir, "missing", Options{ReadOnly: true})
	if !errors.Is(err, ErrNoSuchKB) {
		t.Fatalf("err = %v, want ErrNoSuchKB", err)
	}
	if _, statErr := os.Stat(Path(dir, "missing")); !os.IsNotExist(statErr) {
		t.Fatal("read-only open created a file")
	}
}

// Owner decision 5: nothing is migrated. A v1 journal (both real on-disk
// shapes) is refused with a clear error, and left untouched.
func TestRejectsLegacyJournalFile(t *testing.T) {
	for _, fixture := range []string{"v0-with-fts.db", "v0-without-fts.db"} {
		src := filepath.Join("testdata", fixture)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if err := os.WriteFile(Path(dir, "old"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = Open(context.Background(), dir, "old", Options{})
		if !errors.Is(err, ErrNotKnowledgeBase) {
			t.Fatalf("%s: err = %v, want ErrNotKnowledgeBase", fixture, err)
		}
		after, _ := os.ReadFile(Path(dir, "old"))
		if string(after) != string(data) {
			t.Fatalf("%s: legacy file was modified", fixture)
		}
	}
}

func TestInvalidNameRejected(t *testing.T) {
	for _, bad := range []string{"", "..", "a/b", "../x", "名前"} {
		if _, err := Open(context.Background(), t.TempDir(), bad, Options{}); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

func TestMigrateRefusesFutureAndNegativeVersions(t *testing.T) {
	for _, v := range []int{-1, 99} {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "x.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("PRAGMA user_version = " + itoa(v)); err != nil {
			t.Fatal(err)
		}
		if err := Migrate(context.Background(), db); err == nil {
			t.Errorf("version %d accepted", v)
		}
		db.Close()
	}
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// The FTS triggers must keep both keyword indexes in step with chunks.
func TestChunkTriggersKeepIndexesInSync(t *testing.T) {
	db, err := Open(context.Background(), t.TempDir(), "demo", Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := int64(1)
	mustExec(t, db, `INSERT INTO namespaces(name, created_at) VALUES ('ns', ?)`, now)
	mustExec(t, db, `INSERT INTO sources(id, namespace, kind, fetched_at, trust, origin, created_at) VALUES ('s1','ns','note',?, 'user','user-said',?)`, now, now)
	mustExec(t, db, `INSERT INTO documents(id, source_id, revision, content, created_at, updated_at) VALUES ('d1','s1',1,'x',?,?)`, now, now)
	mustExec(t, db, `INSERT INTO chunks(document_id, ord, section_path, text, context_header) VALUES ('d1',0,'Interceptors > Ordering','call useCallback when reviewing interceptors','Guide > Interceptors > Ordering')`)

	count := func(q string, arg string) int {
		var n int
		if err := db.QueryRow(q, arg).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM chunks_fts WHERE chunks_fts MATCH ?`, "review"); n != 1 {
		t.Errorf("stemmed 'review' -> %d, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM chunks_fts WHERE chunks_fts MATCH ?`, "ordering"); n != 1 {
		t.Errorf("context header word 'ordering' -> %d, want 1", n)
	}
	if n := count(`SELECT COUNT(*) FROM chunks_fts_exact WHERE chunks_fts_exact MATCH ?`, `"useCallback"`); n != 1 {
		t.Errorf("exact 'useCallback' -> %d, want 1", n)
	}
	mustExec(t, db, `UPDATE chunks SET text = 'totally different now' WHERE id = 1`)
	if n := count(`SELECT COUNT(*) FROM chunks_fts WHERE chunks_fts MATCH ?`, "review"); n != 0 {
		t.Errorf("after update, 'review' -> %d, want 0", n)
	}
	mustExec(t, db, `DELETE FROM documents WHERE id = 'd1'`) // cascades to chunks
	if n := count(`SELECT COUNT(*) FROM chunks_fts WHERE chunks_fts MATCH ?`, "different"); n != 0 {
		t.Errorf("after cascade delete, 'different' -> %d, want 0", n)
	}
	var chunks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunks); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 {
		t.Errorf("chunks not cascaded: %d", chunks)
	}
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
