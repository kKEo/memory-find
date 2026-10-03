# Call and query logs

With `MEMO_QUERY_LOG=1` in a process's environment, that process records:

- every **search** in `query_log`: arguments, the addresses returned, scores and the trace;
- every **MCP tool call** in `call_log`: client, tool, latency, outcome, error class, result
  count, tokens out and an argument summary.

Both tables live **in the knowledge-base file**, so they persist across sessions and processes.
They are off by default.

## What is stored, and what never is

| Stored | Never stored |
|---|---|
| Search query text and its scope, mode, granularity and `as_of` | Passage, document or page text returned |
| Returned addresses and scores | `content` of `ingest` (only its length, as `content_len`) |
| Tool name, client name, latency, outcome, error class | `statement` of `remember` |
| Allowlisted arguments: ids, namespaces, flags, dates, and for `ingest` the `source` object (URI, title, kind, library, version) | `reason` of `forget` and `submit`, and `context` |

The argument summary is an allowlist per tool, so a new argument is not logged until it is added
explicitly. **Search queries are stored verbatim.** Treat the logs as sensitive as the
questions people ask.

## Reading the logs

```bash
memo-mcp log tail --n 20            # recent searches, then recent tool calls
memo-mcp log calls --n 50           # tool calls: time, client, tool, latency, results, tokens, args
memo-mcp log show 128               # one search with its full trace
memo-mcp log replay --n 200         # searches as JSON lines, for building eval queries
memo-mcp metrics --since 7d         # per-tool calls, errors, p50/p95/max latency; search aggregates
memo-mcp metrics --json | jq .
```

The UI's `/log` page shows both tables.

## Retention

The logs are not pruned automatically. Prune them on a schedule:

```bash
memo-mcp log prune                  # both logs: keep at most 10,000 rows and nothing older than 30 days
```

For example, with cron:

```cron
0 3 * * *  MEMO_KB=my-project /path/to/memo-mcp log prune
```

## Querying with SQL

```bash
sqlite3 -header -column ~/.memo-mcp/kb/my-project.db "
  SELECT tool, COUNT(*) AS calls, SUM(ok = 0) AS errors, ROUND(AVG(latency_ms)) AS avg_ms
  FROM call_log WHERE ts > (strftime('%s','now','-1 day') * 1000)
  GROUP BY tool ORDER BY calls DESC"
```

`ts` is in Unix milliseconds. Column reference:
[schema §5.8](https://github.com/kKEo/memory-find/blob/master/docs/schema.md).
