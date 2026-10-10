# Forget and redact

```bash
memors-mcp forget memo://doc/0199… --reason "superseded by the 2026 policy"
memors-mcp forget memo://fact/0199… --reason "wrong owner" --redact
```

| | `forget` | `forget --redact` |
|---|---|---|
| Leaves every index (keyword, exact, vectors, graph) | yes | yes |
| Excluded from search, including under `as_of` | yes | yes |
| Address resolves to "forgotten on … because …" | yes | yes |
| Text kept in the file | yes | **no**: cleared |
| Pages built from it | marked stale | marked stale |
| Audit row | yes | yes |

- `--reason` is required and is shown to anyone who dereferences the address later.
- MCP tool calls may forget only `agent`-trust records. The CLI may forget anything.

## Reclaiming disk space

SQLite does not shrink the file when rows are cleared. After large redactions, compact it while
no process has the file open:

```bash
sqlite3 ~/.memors-mcp/kb/my-project.db 'VACUUM'
```

`VACUUM` also removes freed pages that might still hold redacted bytes. Run it after redacting
secrets.
