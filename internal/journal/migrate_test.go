package journal

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
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

func copyFixtureDB(t *testing.T, name string) string {
	t.Helper()
	src, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	dstPath := filepath.Join(t.TempDir(), name)
	dst, err := os.Create(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	return dstPath
}

// TestMigrateFromV0Shapes runs Migrate against two checked-in fixture
// databases mirroring shapes confirmed to exist in real installs: one that
// predates entries_fts entirely, and one where entries_fts already exists
// and is already populated. Both must end up at the current schema
// version with entries_fts fully populated and entries content untouched.
func TestMigrateFromV0Shapes(t *testing.T) {
	cases := []struct {
		name        string
		fixture     string
		wantEntries int
	}{
		{name: "predates entries_fts entirely", fixture: "v0-without-fts.db", wantEntries: 2},
		{name: "entries_fts already present and populated", fixture: "v0-with-fts.db", wantEntries: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := copyFixtureDB(t, tc.fixture)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			var beforeVersion int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&beforeVersion); err != nil {
				t.Fatal(err)
			}
			if beforeVersion != 0 {
				t.Fatalf("fixture %s: expected user_version=0 before migration, got %d", tc.fixture, beforeVersion)
			}

			// Capture entries content before migration to assert it
			// survives byte-identical.
			beforeContent := map[string]string{}
			rows, err := db.Query(`SELECT id, content FROM entries`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id, content string
				if err := rows.Scan(&id, &content); err != nil {
					t.Fatal(err)
				}
				beforeContent[id] = content
			}
			rows.Close()
			if len(beforeContent) != tc.wantEntries {
				t.Fatalf("fixture %s: expected %d entries, got %d", tc.fixture, tc.wantEntries, len(beforeContent))
			}

			if err := Migrate(context.Background(), db); err != nil {
				t.Fatalf("migrate %s: %v", tc.fixture, err)
			}

			var afterVersion int
			if err := db.QueryRow(`PRAGMA user_version`).Scan(&afterVersion); err != nil {
				t.Fatal(err)
			}
			if afterVersion != len(migrations) {
				t.Errorf("fixture %s: user_version after migration = %d, want %d", tc.fixture, afterVersion, len(migrations))
			}

			for id, want := range beforeContent {
				var got string
				if err := db.QueryRow(`SELECT content FROM entries WHERE id = ?`, id).Scan(&got); err != nil {
					t.Fatalf("fixture %s: read back %s: %v", tc.fixture, id, err)
				}
				if got != want {
					t.Errorf("fixture %s: entry %s content changed by migration:\nbefore: %q\nafter:  %q", tc.fixture, id, want, got)
				}
			}

			// entries_fts must exist with exactly one row per entry,
			// regardless of whether it existed (and was already
			// populated) before migration or not.
			var ftsCount int
			if err := db.QueryRow(`SELECT COUNT(*) FROM entries_fts`).Scan(&ftsCount); err != nil {
				t.Fatalf("fixture %s: entries_fts missing or unqueryable after migration: %v", tc.fixture, err)
			}
			if ftsCount != tc.wantEntries {
				t.Errorf("fixture %s: entries_fts has %d rows after migration, want %d", tc.fixture, ftsCount, tc.wantEntries)
			}
		})
	}
}
