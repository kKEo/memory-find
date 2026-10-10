# Files on disk

| Path | Created by | Mode | Contents |
|---|---|---|---|
| `$MEMORS_HOME/` | First writing command | `0700` | Base directory (default `~/.memors-mcp`) |
| `$MEMORS_HOME/kb/` | First writing command | `0700` | Knowledge-base files |
| `$MEMORS_HOME/kb/<name>.db` | First writing command | `0600` | The SQLite database: all layers, logs and audit |
| `$MEMORS_HOME/kb/<name>.db-wal` | SQLite, while open | `0600` | Write-ahead log, checkpointed into the main file |
| `$MEMORS_HOME/kb/<name>.db-shm` | SQLite, while open | `0600` | Shared-memory index for the WAL |
| `$MEMORS_HOME/profiles.json` | You | yours | Optional profile overrides |
| `$MEMORS_HOME/http-token` | `serve --http` in token mode, `memors-mcp http-token` | `0600` | The HTTP bearer token |
| `$MEMORS_HOME/run/` | First `serve --http` | `0700` | Run files of the HTTP servers running now |
| `$MEMORS_HOME/tray.json`, `tray.lock`, `logs/` | memors-tray (macOS app), if you use it | `0600` / `0700` | Its settings, its single-instance lock, and the logs of the servers it starts; see [memors-tray](../install/tray.md) |
| `$MEMORS_HOME/run/serve-<pid>.json` | `serve --http`, while it serves | `0600` | Where and how to reach that server: URL, bound address, auth mode, token file path (never the token), knowledge base, version, instance id. Removed when the server stops; a server killed with `SIGKILL` leaves it behind, and memors-tray deletes it once the process is gone |
| `~/.cache/memors-mcp/models/<org>_<model>/` | First use of a model | `0755` | Model files from Hugging Face |
| `~/.cache/memors-mcp/models/<org>_<model>.ok` | Completed download | — | Marker; without it the download is redone |

A directory that already existed with wider permissions is not tightened.

## What is inside the database

| Group | Tables |
|---|---|
| L0–L2 | `sources`, `documents`, `chunks`, `chunks_fts` and `chunks_fts_exact` (FTS5), `chunk_vecs` (one row per passage per model) |
| L3 | `facts`, `facts_fts`, `fact_vecs` |
| L4 | `entities`, `entity_aliases`, `mentions`, `merge_candidates`, `edges` |
| L5 | `pages`, `pages_fts`, `page_sources`, `page_vecs`, `work_items` |
| Bookkeeping | `namespaces`, `models`, `jobs`, `audit`, `query_log`, `call_log` |

Every column is described in
[schema.md](https://github.com/kKEo/memors/blob/master/docs/schema.md). The file is plain
SQLite. You can inspect it with the `sqlite3` shell, for example
`sqlite3 ~/.memors-mcp/kb/default.db '.tables'`. Do not write to it by hand: triggers keep the
indexes consistent only for writes made through memors-mcp.

## Nothing else

Apart from the run files above, memors-mcp writes no PID files, no lock files of its own, no
logs to disk and no temporary files outside the model cache. Logs go to stderr. Redirect them
yourself if you want them on disk.
