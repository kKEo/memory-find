# Troubleshooting runbook

Start with these three commands. They answer most questions:

```bash
memo-mcp version                       # which build, protocol, model dir
MEMO_KB=<name> memo-mcp status         # file, schema, counts, model, jobs
MEMO_KB=<name> memo-mcp verify         # integrity
```

## The client does not show memo's tools

| Check | Fix |
|---|---|
| `/mcp` in Claude Code, or the developer settings in Claude Desktop, shows an error | Read the server's stderr; the reason is the last `fatal:` line |
| `command` is relative or uses `~` | Use an absolute path |
| `fatal: invalid name …` | `MEMO_KB` must match `^[A-Za-z0-9._-]{1,64}$` |
| `fatal: … address already in use` | Another session holds the same `--metrics-addr`. See [the metrics endpoint](../monitoring/metrics-endpoint.md) |
| `fatal: database schema is at version N, but this binary only supports up to version M` | The file was written by a newer memo-mcp; upgrade the binary |
| macOS refuses to run the binary | `xattr -d com.apple.quarantine /path/to/memo-mcp` |

## Search returns nothing, or too little

| Check | Meaning, and fix |
|---|---|
| Response has a `reason` and zero results | Deliberate abstention: nothing passed the semantic floor. Check with `memo-mcp search "<q>" --mode keyword` |
| `status` shows 0 documents, or a different count than you expect | Wrong `MEMO_KB` or `MEMO_HOME`: the CLI and the client are not looking at the same file |
| trace `filtered.by_scope` is high | The request's scope (namespace, version, library, minimum trust) excludes the answer |
| `degraded` is set | No model: see the next section |
| `truncated` is above 0 | The token budget cut results; raise `max_tokens` or narrow with `narrow_hint` |
| Lists stop after 3 results | Gap cutoff (`cutoff.kind = gap`); see `cutoff_gap` in [Profiles](../tuning/profiles.md) |

## Search is degraded

| Check | Fix |
|---|---|
| stderr shows `embedding unavailable…` | Model download or load failed; the error follows. Retry with `memo-mcp model pull <id>` |
| Download interrupted or corrupted | `memo-mcp model redownload <id>` (pass the id) |
| `memo_kb_pending_embeddings` above 0 | Backlog after a model switch or a degraded write: `memo-mcp backfill`, or `reindex --model <id>` |
| No network by design | Pre-seed the cache: [Air-gapped installs](../install/air-gapped.md) |

## Slow searches

1. `memo-mcp explain "<q>"`, or the UI: see `latency_ms_per_arm` in the trace.
2. `semantic` is slow: query embedding dominates. Consider `potion`, or accept about 0.2 s.
3. `graph` is slow on its first call: the namespace graph is built once. Check
   `memo_graph_cache_total{event="build"}`. Repeated builds mean frequent writes.
4. Reranker on? `MEMO_RERANK=1` adds 0.7–4 s. Turn it off.

## Slow writes

Embedding dominates: about 0.6 s per passage with the default model. For bulk loads, use
`ingest --embed=false`, then `backfill`.

## "database is locked"

Another process held the write lock for more than 5 seconds. This is rare, because writes are
short and embedding runs outside the lock. Look for a long `graph rebuild`, `VACUUM` or a
manual `sqlite3` session holding a transaction. Never put the file on a network file system.

## Read-only commands fail with "run memo-mcp migrate"

The file is older than the binary. Run `memo-mcp migrate`, or any writing command, once.

## The UI is unreachable

- Use the exact URL it printed. The Host header must match the bound address.
- It binds loopback only. From another machine, use an SSH tunnel
  (`ssh -L 9470:127.0.0.1:9470 host`) rather than `--allow-remote`. The UI has no
  authentication and shows everything in the knowledge base.

## Promote never shows a dialog

The client does not support elicitation, so the tool result contains a `memo-mcp trust promote …`
command for a human to run. `memo_mcp_elicitations_total{outcome="unsupported"}` counts these.

## Reporting a bug

Include `memo-mcp version`, the `status` output, the stderr lines from
`MEMO_LOG_LEVEL=debug`, and, for ranking issues, `memo-mcp explain "<query>"`. Do not include
document text you would not share.
