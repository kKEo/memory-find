# System overview

## One binary, three processes

`memo-mcp` is one statically linked Go binary (`CGO_ENABLED=0`, about 30 MB). The same
binary runs in three roles, which are **separate processes** that share only the database file:

| Role | Started by | Talks over | Lifetime |
|---|---|---|---|
| **MCP server** (`memo-mcp` or `memo-mcp serve`) | The MCP client (Claude Code, Claude Desktop, …) | stdio: JSON-RPC on stdin/stdout, logs on stderr | As long as the client session |
| **Web UI** (`memo-mcp ui`) | An operator | HTTP on a loopback port, GET only | Until Ctrl-C |
| **CLI** (`memo-mcp <command>`) | An operator or a script | Terminal | One command |

Each MCP client session usually starts its own server process. Several processes can hold the
same file open at once; SQLite WAL mode and a 5-second busy timeout serialise writers.

```
 MCP client ── stdio ──▶ memo-mcp serve ─┐
 operator ── browser ──▶ memo-mcp ui ────┼──▶ $MEMO_HOME/kb/<name>.db  (SQLite, WAL)
 operator ── shell ────▶ memo-mcp <cmd> ─┘
                                          └─▶ ~/.cache/memo-mcp/models  (embedding models)
```

## Inside the process

| Package | Responsibility |
|---|---|
| `internal/server` | The ten MCP tools and the `memo://` resources; request middleware (metrics, logs, call log) |
| `internal/cli` | Commands, configuration resolution, serve and UI wiring |
| `internal/retrieve` | The search pipeline: arms, fusion, recency, cutoff, budget, explain |
| `internal/kb` | Schema and migrations, ingest, facts, trust, time, graph tables, pages, export |
| `internal/embedding` | The model registry, download, in-process inference (hugot / GoMLX, pure-Go ONNX) |
| `internal/graph`, `internal/compact` | Entity extraction and resolution; compaction work items and checks |
| `internal/ui` | The read-only web UI (`html/template`, embedded assets) |
| `internal/obs` | Metrics registry, Prometheus exposition, logging setup |

## The ten MCP tools

| Tool | Writes? | Purpose |
|---|---|---|
| `ingest` | yes | Store a markdown document with provenance; same content is a no-op, changed content a new revision |
| `search` | no* | Hybrid search with optional per-result explanations |
| `read` | no | Dereference a `memo://` address as a passage, section or document |
| `remember` | yes | Record one fact with evidence and validity dates; `supersedes` corrects an old one |
| `forget` | yes | Retire a document or fact with a reason (agent-written records only) |
| `promote` | via a human | Ask a human to raise trust, through an elicitation dialog or a CLI command |
| `explore` | no | Walk the entity graph from one name |
| `compact` | work items | Propose pages, refreshes, conflict and merge decisions, duplicates |
| `submit` | yes | Hand back a page or a decision for a work item; checked before it is stored |
| `status` | no | Namespaces, model, pending vectors, jobs, graph and page counts |

\* `search` writes only to the opt-in query log.

Resources mirror the addresses (`memo://doc/{id}`, `memo://chunk/{id}`, …), and
`memo://index` returns a one-line-per-document index under 8 KB.

## The one network call

The first time a process needs an embedding model that is not cached, it downloads the model
files from Hugging Face into `~/.cache/memo-mcp/models`. Nothing else in the default
configuration opens an outbound connection. The optional Ollama executor and the
`--metrics-addr` listener are explicit opt-ins, and both are loopback-only by default. See
[Air-gapped installs](../install/air-gapped.md) to avoid even the download.

Design reference: [architecture §1–§4](https://github.com/kKEo/memory-find/blob/master/docs/architecture.md).
