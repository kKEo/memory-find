package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaV4 is migration 4: the per-call log (roadmap P10). One row per MCP
// tool call when MEMORS_QUERY_LOG=1, next to the per-search query_log. It
// stores timing, outcome and an allowlisted summary of the arguments, never
// the content that was written or the text that came back.
const schemaV4 = `
CREATE TABLE call_log (
    id                INTEGER PRIMARY KEY,
    ts                INTEGER NOT NULL,
    client            TEXT NOT NULL DEFAULT '',
    method            TEXT NOT NULL,
    tool              TEXT NOT NULL DEFAULT '',
    latency_ms        INTEGER NOT NULL DEFAULT 0,
    ok                INTEGER NOT NULL DEFAULT 1,
    error_class       TEXT NOT NULL DEFAULT '',
    n_results         INTEGER NOT NULL DEFAULT 0,
    tokens_out        INTEGER NOT NULL DEFAULT 0,
    args_summary_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_call_log_ts ON call_log(ts);
CREATE INDEX idx_call_log_tool_ts ON call_log(tool, ts);
`

func migrateV4(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, schemaV4); err != nil {
		return fmt.Errorf("create call_log: %w", err)
	}
	return nil
}
