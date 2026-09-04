package journal

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

func TestMigrateSetsUserVersion(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != len(migrations) {
		t.Errorf("user_version = %d, want %d", v, len(migrations))
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestMigrateRejectsNewerSchema(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`PRAGMA user_version = 999999`); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(context.Background(), db); err == nil {
		t.Fatal("expected error opening a database from a newer schema version, got nil")
	}
}

// TestMigrateBackfillsFTSForPreExistingEntries covers the case of a
// database that has the "entries" table (with rows already in it) but
// predates entries_fts — an early on-disk shape confirmed to still exist.
func TestMigrateBackfillsFTSForPreExistingEntries(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE entries (
		    id TEXT PRIMARY KEY,
		    created_at INTEGER NOT NULL,
		    content TEXT NOT NULL,
		    sections TEXT NOT NULL DEFAULT '[]'
		)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO entries (id, created_at, content, sections) VALUES (?, ?, ?, ?)`,
		"legacy-entry-1", 0, "some legacy content predating fts", "[]",
	); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var ftsEntryID string
	err = db.QueryRow(`SELECT entry_id FROM entries_fts WHERE content MATCH 'legacy'`).Scan(&ftsEntryID)
	if err != nil {
		t.Fatalf("expected backfilled FTS row: %v", err)
	}
	if ftsEntryID != "legacy-entry-1" {
		t.Errorf("entry_id = %q, want %q", ftsEntryID, "legacy-entry-1")
	}
}
