# Commands and flags

Running `memo-mcp` with no arguments is the same as `memo-mcp serve`. `memo-mcp --help` prints
the summary. Flags may come before or after positional arguments.

**Opens** shows how each command opens the knowledge base:

- **rw**: creates and migrates the file.
- **rw, no create**: migrates, but refuses a missing file.
- **ro**: never writes, and refuses a missing or out-of-date file.

## Server and UI

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp serve` | rw | MCP server on stdio. `--metrics-addr 127.0.0.1:PORT` exposes `/metrics` on loopback (default `$MEMO_METRICS_ADDR`, off when empty). Starts a background backfill and reindex for the current model. `--http 127.0.0.1:PORT` (default `$MEMO_HTTP_ADDR`) serves MCP over HTTP at `/mcp` instead of stdio, with the web UI and its live pages at `/` and metrics at `/metrics`, all on one port. `--allow-remote` permits a non-loopback `--http` address; it then also needs TLS (`--tls-cert`/`--tls-key`, or `--behind-proxy`), `--public-url`, and authentication. `--auth token\|none` (default `token` when remote or behind a proxy), `--token-file`, `--tls-client-ca` for mTLS. See [Connecting MCP clients](../install/clients.md#one-shared-server-over-http) |
| `memo-mcp http-token` | rw | Prints the bearer token for `serve --http`, creating `<MEMO_HOME>/http-token` (0600) when missing. `--rotate` replaces it; restart the server afterwards. `--file` picks another path |
| `memo-mcp ui` | ro | Read-only web UI. `--addr` (default `127.0.0.1:0`, a random port; the URL is printed), `--allow-remote` permits a non-loopback address, `--no-model` gives keyword-only search |

## Writing

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp ingest <file\|dir\|->` | rw | Add markdown documents. `--ns` (default `default`), `--kind doc\|note\|code\|conversation`, `--uri`, `--title`, `--library`, `--version`, `--trust user\|curated` (default `user`), `--origin web\|user-said\|agent-derived`, `--context "<sentence>"`, `--embed=false` queues vectors instead of embedding now |
| `memo-mcp remember "<fact>"` | rw | Record a fact. `--ns`, `--about a,b`, `--valid-from`, `--valid-to` (YYYY-MM-DD), `--supersedes memo://fact/..`, `--evidence memo://chunk/n`, `--trust user\|curated`, `--origin`, `--no-model` |
| `memo-mcp forget <memo://doc/..\|memo://fact/..>` | rw | Retire a record. `--reason "<why>"` (required), `--redact` also erases the text |
| `memo-mcp trust ls` | ro | List records above `agent` trust |
| `memo-mcp trust promote <uri> --to user\|curated` | rw | Raise trust (human channel; audited) |
| `memo-mcp trust demote <uri> --to agent\|user` | rw | Lower trust (audited) |

## Reading and search

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp search "<q>"` | rw, no create | Hybrid search. `--mode auto\|hybrid\|keyword\|exact\|semantic`, `--ns a,b`, `--library`, `--version`, `--kind a,b`, `--min-trust agent\|user\|curated`, `--limit` (10), `--granularity chunk\|document`, `--format table\|json\|md`, `--explain`, `--max-tokens` (8000), `--profile` (default `$MEMO_PROFILE`), `--rerank` (default `$MEMO_RERANK`), `--no-model` |
| `memo-mcp explain "<q>" [<memo://...>]` | rw, no create | `search --explain`; with an address, the full explanation for that one result. Same flags as `search` |
| `memo-mcp read <memo://...>` | ro | Print any record with its provenance. `--history` prints the revision or supersession chain |
| `memo-mcp ls` | ro | Live documents, newest first. `--ns`, `--kind`, `--since YYYY-MM-DD`, `--limit` (50), `--json` |
| `memo-mcp facts ls` | ro | Facts. `--ns`, `--as-of YYYY-MM-DD`, `--history` includes replaced facts, `--json` |
| `memo-mcp explore <name>` | ro | Walk the graph from one entity. `--ns`, `--hops 1\|2`, `--as-of`, `--json` |
| `memo-mcp export --md <dir>` | ro | Markdown files with front matter, plus an `_index.md` per namespace. `--ns` |
| `memo-mcp export --index` | ro | A compact index for `AGENTS.md` and `CLAUDE.md`. `--ns`, `--library name[@version]`, `--max-bytes` (8192) |

## Graph and compaction

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp graph merges` | ro | Merge-candidate review queue. `--state open\|merged\|rejected\|all` (default `open`) |
| `memo-mcp graph merge <id>` / `memo-mcp graph reject <id>` | rw | Decide a merge candidate |
| `memo-mcp graph rebuild` | rw | Re-extract entity mentions. `--ns` |
| `memo-mcp compact` | rw | Create or list work items. `--ns`, `--kinds page,stale,conflict,merge,duplicate`, `--lint`, `--json`, `--executor ollama`, `--apply` (with an executor; dry run otherwise), `--allow-remote` |
| `memo-mcp submit <item-id>` | rw | Complete a work item. Pages: `--content-file f.md\|-`, `--title`. Conflicts: `--keep memo://fact/..`. Merges: `--accept` or `--reject`. Any item: `--skip`, `--reason`, `--dry-run` |
| `memo-mcp lint` | ro | Contradictions, orphan entities, missing and stale pages, expired facts. `--ns`, `--json` |
| `memo-mcp pages ls` | ro | Curated pages. `--ns`, `--stale`, `--json` |

## Maintenance

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp status` | ro | File, schema version, counts, model, jobs, namespaces |
| `memo-mcp migrate` | rw, no create | Bring the file to this binary's schema version |
| `memo-mcp verify` | rw, no create | Check documents without chunks, orphan vectors and facts, missing vectors for the current model, and FTS integrity. `--repair` queues missing vectors and rebuilds broken indexes |
| `memo-mcp backfill` | rw, no create | Embed passages whose vectors are pending |
| `memo-mcp reindex` | rw, no create | Embed every passage lacking a vector for a model. `--model <id>` (default `$MEMO_MODEL`) |

## Observability

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp metrics` | ro | Snapshot from the file: gauges, plus per-tool and search statistics from the opt-in logs. `--json`, `--since` (24h) |
| `memo-mcp log tail` | rw, no create | Recent searches, then recent tool calls. `--n` (20) |
| `memo-mcp log calls` | rw, no create | Recent tool calls with latency and outcome. `--n` (50) |
| `memo-mcp log show <id>` | rw, no create | One logged search with its full trace |
| `memo-mcp log replay` | rw, no create | Logged searches as JSON lines, ready to label as eval queries. `--n` (200) |
| `memo-mcp log prune` | rw, no create | Keep at most 10,000 rows and 30 days in both logs |

## Models, profiles and evaluation

| Command | Opens | Purpose and flags |
|---|---|---|
| `memo-mcp model ls` | ro | Registry: id, dimension, licence, stored vectors, notes; marks the selected model |
| `memo-mcp model smoke <id>\|--all` | none | Prove a model loads under the pure-Go backend, and time it |
| `memo-mcp model pull <id>` | none | Download a model and check that it loads |
| `memo-mcp model use <id>` | rw, no create | Record the knowledge base's default model. Also set `MEMO_MODEL` in the client config |
| `memo-mcp model redownload [<id>]` | none | Discard the cached copy and download again. **Without an id it re-downloads `minilm`**, so pass the id you use |
| `memo-mcp profiles show [<name>]` | none | Every ranking constant with its derivation |
| `memo-mcp eval` | temporary | Benchmark on built-in labelled corpora. `--models hash,minilm,…`, `--profiles default,…\|all`, `--corpus notes\|kb\|all`, `--format table\|md\|json`, `--explain-failures`, `--rerank`, `--agent-proxy` |
| `memo-mcp version` | none | Build version, MCP protocol version, Go version, model directory |

## Exit codes and output

- `0` on success. `1` on a runtime error, printed as `fatal: …` on stderr. `2` on a usage error.
- Results go to stdout, and diagnostics and logs to stderr. `--json` output is stable enough to
  script against.
- The legacy flags `--stats` and `--redownload-model` still work for one more release, with a
  deprecation warning.
