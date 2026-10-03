# Signals overview

| Signal | Where | On by default | Scope |
|---|---|---|---|
| **Metrics** (Prometheus text 0.0.4) | `serve --metrics-addr` and UI `GET /metrics` | In memory, always; exposed only when asked | The **process** that serves them |
| **Logs** (`log/slog`, text or JSON) | stderr | Yes, at `info` | The process |
| **Call log and query log** | `call_log` and `query_log` tables | No (`MEMO_QUERY_LOG=1`) | The **file**: every process writing to it |
| **Snapshot** (`memo-mcp metrics`) | stdout | On demand | The file: gauges plus log statistics |
| **Audit** | `audit` table | Always | The file: every write |

## The per-process caveat

The MCP server, the UI and each CLI command are separate processes. Counters such as
`memo_mcp_tool_calls_total` live in the memory of the process that served the calls:

- A **server** process exists only while its client session is open. Its counters start at
  zero with each session. Scrape it while it runs; Prometheus `rate()` handles the resets.
- The **UI** has its own counters, mostly `memo_ui_*`. Like every process, it also serves the
  `memo_kb_*` gauges, which are read from the file.
- **History across sessions** comes from the file: `memo-mcp metrics` (with `MEMO_QUERY_LOG=1`)
  and the `memo_kb_*` gauges.

## Recommended setups

| Goal | Setup |
|---|---|
| Glance at health now | `memo-mcp metrics`, or `memo-mcp status` |
| Trends of knowledge-base size, backlog and pages | Run `memo-mcp ui --addr 127.0.0.1:9470` as a long-lived process and scrape its `/metrics` for `memo_kb_*` |
| Live tool and search latency, errors, degraded mode | Add `--metrics-addr` to **one** server entry and scrape it while sessions run |
| Per-call history and slow-call forensics | `MEMO_QUERY_LOG=1`, then `memo-mcp log calls` and `memo-mcp metrics --since 7d` |

## Naming

- Prefix `memo_`; counters end in `_total`; units are suffixes (`_seconds`, `_bytes`), and
  seconds are never milliseconds.
- Labels are bounded enums: tool names, outcomes, arms, models, namespaces. Never query
  text, ids or paths.
- OpenTelemetry mapping: replace `_` with `.` and drop `_total`. For example,
  `memo_mcp_tool_calls_total` maps to `memo.mcp.tool_calls`.
