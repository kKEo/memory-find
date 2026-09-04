package journal

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations describes every schema change this binary knows how to apply,
// in order. PRAGMA user_version records how far a given database file has
// been brought forward; Migrate walks it the rest of the way to
// len(migrations).
//
// Each migration runs in its own transaction. Schema changes here are
// expected to accumulate over time (see the project roadmap) — this file
// should only ever grow new entries at the end, never rewrite old ones.
var migrations = []migration{
	{1, "baseline_schema", migrateV1BaselineSchema},
}

type migration struct {
	version int
	name    string
	apply   func(ctx context.Context, tx *sql.Tx) error
}

// Migrate brings db up to the schema version this binary expects, running
// any migrations the database file hasn't seen yet. It refuses to touch a
// database that was written by a newer binary, since running old migration
// logic (or none at all) against unknown future schema could corrupt it.
func Migrate(ctx context.Context, db *sql.DB) error {
	var current int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	if current > len(migrations) {
		return fmt.Errorf(
			"database schema is at version %d, but this binary only supports up to version %d; upgrade memo-mcp",
			current, len(migrations),
		)
	}

	for _, m := range migrations[current:] {
		if err := runMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}

	return nil
}

func runMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := m.apply(ctx, tx); err != nil {
		return err
	}

	// PRAGMA user_version does not accept a bound parameter. m.version is
	// our own int literal from the migrations slice above, never user
	// input, so building the statement with Sprintf is safe.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}

	return tx.Commit()
}

const baselineSchema = `
CREATE TABLE IF NOT EXISTS entries (
    id TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    content TEXT NOT NULL,
    sections TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries(created_at);
`

// migrateV1BaselineSchema brings a database up to the shape the project has
// shipped since its first release: entries + entry_embeddings (vec0) +
// entries_fts (fts5), with entries_fts backfilled for any pre-existing rows
// that predate it.
//
// Two shapes of unversioned (pre-migration) database exist in the wild —
// some early databases were created before entries_fts existed at all — so
// everything here uses IF NOT EXISTS / existence checks rather than
// assuming a starting shape.
func migrateV1BaselineSchema(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, baselineSchema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE VIRTUAL TABLE IF NOT EXISTS entry_embeddings USING vec0(entry_id TEXT PRIMARY KEY, embedding float[384])`,
	); err != nil {
		return fmt.Errorf("create vec table: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(entry_id UNINDEXED, content)`,
	); err != nil {
		return fmt.Errorf("create fts table: %w", err)
	}

	var ftsCount, entryCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries_fts`).Scan(&ftsCount); err != nil {
		return fmt.Errorf("count fts rows: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&entryCount); err != nil {
		return fmt.Errorf("count entries: %w", err)
	}
	if ftsCount == 0 && entryCount > 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entries_fts(entry_id, content) SELECT id, content FROM entries`,
		); err != nil {
			return fmt.Errorf("backfill fts: %w", err)
		}
	}

	return nil
}
