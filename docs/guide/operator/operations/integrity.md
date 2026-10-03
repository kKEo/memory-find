# Integrity and repair

## `verify`

```bash
memo-mcp verify
memo-mcp verify --repair
```

| Check | Problem reported | `--repair` does |
|---|---|---|
| Every live document has passages | `N live document(s) have no chunks` | Reports only; re-ingest the source |
| Vectors point at existing passages | `N vector(s) point at missing chunks` | Reports only |
| Facts point at existing evidence | `N fact(s) point at missing evidence chunks` | Reports only |
| Every live passage has a vector for the current model | `N live chunk(s) have no vector for model X` | Queues an embed job; run `memo-mcp backfill` |
| FTS5 index integrity | `<index> failed integrity-check` | Rebuilds the index |

The command prints `ok: no problems found`, or one `problem:` line per finding and one
`repaired:` line per fix.

## `backfill` and `reindex`

| Command | Embeds | When |
|---|---|---|
| `memo-mcp backfill` | Passages queued for vectors, from `--embed=false`, a degraded write or `verify --repair` | After bulk loads, or after the model was unavailable |
| `memo-mcp reindex [--model id]` | Every live passage lacking a vector for the model | After switching models |

Both are resumable and safe to interrupt. The server runs both in the background at start.

## SQLite-level checks

```bash
sqlite3 ~/.memo-mcp/kb/my-project.db 'PRAGMA integrity_check'   # expect: ok
sqlite3 ~/.memo-mcp/kb/my-project.db 'PRAGMA user_version'      # schema version
```

## When to run what

| After… | Run |
|---|---|
| A crash or power loss | `verify`, then `PRAGMA integrity_check` |
| Restoring a backup | `verify` |
| Bulk ingest with `--embed=false` | `backfill` |
| Changing `MEMO_MODEL` | `reindex --model <id>` |
| Upgrading across 1.0 to 1.1 | `graph rebuild` |
| Large redactions | `VACUUM` with no process attached |
