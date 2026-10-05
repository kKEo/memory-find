package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaV5 is migration 5: ingest runs. One row per CLI batch or MCP ingest
// call, with running counters the web UI polls to show progress, plus one
// row per document the run touched. Only counts, names and addresses are
// stored, never the content.
const schemaV5 = `
CREATE TABLE ingest_runs (
    id          TEXT PRIMARY KEY,
    channel     TEXT NOT NULL,
    actor       TEXT NOT NULL DEFAULT '',
    namespace   TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL CHECK (state IN ('running','done','failed')),
    started_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    finished_at INTEGER,
    total       INTEGER NOT NULL DEFAULT 0,
    done        INTEGER NOT NULL DEFAULT 0,
    written     INTEGER NOT NULL DEFAULT 0,
    unchanged   INTEGER NOT NULL DEFAULT 0,
    failed      INTEGER NOT NULL DEFAULT 0,
    chunks      INTEGER NOT NULL DEFAULT 0,
    embedded    INTEGER NOT NULL DEFAULT 0,
    pending     INTEGER NOT NULL DEFAULT 0,
    bytes       INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_ingest_runs_started ON ingest_runs(started_at);

CREATE TABLE ingest_run_items (
    id       INTEGER PRIMARY KEY,
    run_id   TEXT NOT NULL REFERENCES ingest_runs(id) ON DELETE CASCADE,
    seq      INTEGER NOT NULL,
    name     TEXT NOT NULL DEFAULT '',
    uri      TEXT NOT NULL DEFAULT '',
    outcome  TEXT NOT NULL CHECK (outcome IN ('new','revision','unchanged','error')),
    revision INTEGER NOT NULL DEFAULT 0,
    chunks   INTEGER NOT NULL DEFAULT 0,
    embedded INTEGER NOT NULL DEFAULT 0,
    pending  INTEGER NOT NULL DEFAULT 0,
    bytes    INTEGER NOT NULL DEFAULT 0,
    ms       INTEGER NOT NULL DEFAULT 0,
    error    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_ingest_run_items_run ON ingest_run_items(run_id, seq);
`

func migrateV5(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, schemaV5); err != nil {
		return fmt.Errorf("create ingest_runs: %w", err)
	}
	return nil
}
