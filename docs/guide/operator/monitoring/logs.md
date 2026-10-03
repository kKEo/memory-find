# Logs

memo-mcp logs with Go's `log/slog`, **to stderr only**. In server mode, stdout carries the MCP
JSON-RPC stream and is never written to by anything else; a test enforces this.

| Variable | Values | Default |
|---|---|---|
| `MEMO_LOG_FORMAT` | `text`, `json` | `text` |
| `MEMO_LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |

An unknown value falls back to the default and prints one warning.

## What is logged

| Level | Event | Fields |
|---|---|---|
| INFO | `tool call`: one per MCP tool call | `tool`, `client`, `latency_ms`, `outcome`, `n_results`, `tokens_out`, and `error_class` and `err` on failure |
| INFO | `search`: one per search, including CLI and UI searches | `mode`, `resolved`, `granularity`, `outcome`, `n_results`, `truncated`, `cutoff`, `latency_ms`, `profile`, and `degraded` and `entities` when present |
| DEBUG | `search detail` | `routing`, `latency_ms_per_arm`, `candidates_per_arm`, `scope` |
| INFO | Start-up and background work | `metrics listening`, `backfilled chunk vectors`, `embedded passages for the current model`, `reranker loaded` |
| WARN | Degraded or deprecated | `embedding unavailable…`, `backfill failed`, `reindex failed`, `reranker unavailable…`, download retries, licence notes, deprecated variables and flags |
| ERROR | Unexpected failures | — |

Logs never contain document text, fact statements, or forget reasons. **Search log lines do not
include the query text.**

Example, with `MEMO_LOG_FORMAT=json`:

```json
{"time":"2026-10-03T16:23:45.136+02:00","level":"INFO","msg":"search","mode":"auto","resolved":"semantic+keyword+fact","granularity":"chunk","outcome":"results","n_results":1,"truncated":0,"cutoff":"none","latency_ms":"461.8","profile":"default"}
```

`latency_ms` is a string with one decimal place.

## Where logs end up

- **Claude Code** shows a stdio server's stderr. Run `claude --debug` to see it live.
- **Claude Desktop** writes each server's stderr to its own log file. On macOS this is
  `~/Library/Logs/Claude/mcp-server-memo.log`.
- **CLI commands** print logs to your terminal's stderr. Every `memo-mcp search` prints its
  INFO `search` line. Use `MEMO_LOG_LEVEL=warn` for quiet interactive use, or `2>/dev/null`.
- To capture a server's logs yourself, wrap the binary in a small script:

  ```bash
  #!/bin/sh
  exec /path/to/memo-mcp "$@" 2>>"$HOME/.memo-mcp/serve.log"
  ```

  Point the client's `command` at the script. Rotate the file with your usual tool.

## MCP logging capability

memo-mcp does **not** advertise the MCP `logging` capability. It is deprecated in protocol
`2026-07-28`, and stdio hosts already surface stderr. Do not expect log notifications over the
protocol.
