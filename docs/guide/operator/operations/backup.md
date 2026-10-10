# Backup and restore

A knowledge base is one SQLite file in WAL mode. Back it up with SQLite's own tools, not with a
plain file copy while processes have it open: a copy of the main file alone can miss committed
transactions that are still in the `-wal` file.

## While memors-mcp is running (safe online backup)

```bash
DB=~/.memors-mcp/kb/my-project.db
sqlite3 "$DB" ".backup '/backups/my-project-$(date +%F).db'"
# or, which also compacts the copy:
sqlite3 "$DB" "VACUUM INTO '/backups/my-project-$(date +%F).db'"
```

Both take a consistent snapshot while readers and writers continue. Writers wait at most
memors-mcp's 5-second busy timeout. `sqlite3` creates the copy with your umask, typically `0644`.
Restrict it with `chmod 600`, because the backup holds everything the knowledge base holds.

## While nothing is running

When no memors-mcp process has the file open, `-wal` and `-shm` are empty or absent, and copying
`<name>.db` is enough. If a `-wal` file with content remains, copy it alongside, or run
`sqlite3 <name>.db 'PRAGMA wal_checkpoint(TRUNCATE)'` first.

## What to back up

| Path | Back up? |
|---|---|
| `$MEMORS_HOME/kb/*.db` | **Yes**: everything lives here, including logs and audit |
| `$MEMORS_HOME/profiles.json` | Yes, if you use it |
| `~/.cache/memors-mcp/models/` | No: it is re-downloadable. Keep a copy only for air-gapped machines |

## Restore

1. Stop every process using the knowledge base: close the client sessions, the UI and
   running commands.
2. Remove the stale `-wal` and `-shm` files next to the target, if present.
3. Copy the backup to `$MEMORS_HOME/kb/<name>.db`, then `chmod 600` it.
4. Check it:
   ```bash
   MEMORS_KB=<name> memors-mcp verify
   MEMORS_KB=<name> memors-mcp status
   ```

A backup taken by an older binary is migrated forward on first write. A backup from a newer
binary is refused by an older one.

## A human-readable copy

`memors-mcp export --md <dir>` writes every live document as markdown with full provenance front
matter. It is not a complete backup: there are no facts, history, graph or logs. But it is
readable without memors-mcp, and re-ingesting it creates no new revisions. See
[Exporting](export.md).
