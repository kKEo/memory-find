package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations lists every schema change this binary knows how to apply, in
// order. PRAGMA user_version records how far a file has been brought; this
// slice only ever grows at the end. See docs/schema.md §9.
var migrations = []migration{
	{1, "knowledge_base_v1", migrateV1},
	{2, "graph_v2", migrateV2},
	{3, "pages_v3", migrateV3},
	{4, "call_log_v4", migrateV4},
	{5, "ingest_runs_v5", migrateV5},
}

type migration struct {
	version int
	name    string
	apply   func(ctx context.Context, tx *sql.Tx) error
}

// Migrate brings db up to the schema version this binary expects. The
// current version is read inside the same BEGIN IMMEDIATE transaction that
// applies each step, so two processes upgrading the same file cannot both
// apply a migration (the second one re-reads the version after the first
// commits and finds nothing to do).
func Migrate(ctx context.Context, db *sql.DB) error {
	for {
		applied, err := migrateOne(ctx, db)
		if err != nil {
			return err
		}
		if !applied {
			return nil
		}
	}
}

// migrateOne applies the next pending migration, if any, and reports
// whether it did.
func migrateOne(ctx context.Context, db *sql.DB) (bool, error) {
	tx, err := db.BeginTx(ctx, nil) // _txlock=immediate: takes the write lock now
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var current int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return false, fmt.Errorf("read schema version: %w", err)
	}
	switch {
	case current < 0:
		return false, fmt.Errorf("database schema version %d is invalid", current)
	case current > len(migrations):
		return false, fmt.Errorf("database schema is at version %d, but this binary only supports up to version %d; upgrade memo-mcp", current, len(migrations))
	case current == len(migrations):
		return false, nil
	}

	m := migrations[current]
	if err := m.apply(ctx, tx); err != nil {
		return false, fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
	}
	// PRAGMA statements take no bound parameters; m.version is our own
	// literal, never user input.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
		return false, fmt.Errorf("set schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit migration %d: %w", m.version, err)
	}
	return true, nil
}

// SchemaVersion returns the file's user_version.
func SchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}
