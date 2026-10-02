<!-- mcp-name: io.github.kKEo/memory-find -->

# memo-mcp

A local [MCP](https://modelcontextprotocol.io) server that gives Claude a private, searchable journal. Single Go binary, no cloud, no CGo — one SQLite file per project, hybrid keyword+semantic search, and a small enough codebase (about 1,800 lines of production Go, plus a 650-line eval harness and 2,200 lines of tests) to read end to end.

It's a Go rewrite of [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp) (TypeScript), keeping the same journal categories and tool names (plus one extra tool, `journal_stats`) while replacing per-entry markdown files with a single SQLite database and a from-scratch pure-Go embedding pipeline.

> **Where this is going.** The journal is being rebuilt as a measurable, explainable local
> knowledge base for agents and people. The plan is [`docs/roadmap.md`](docs/roadmap.md); the
> research behind it is [`docs/knowledge-base-sota.md`](docs/knowledge-base-sota.md). Everything
> below describes the binary as it is today.

## What it does

Claude gets six tools:

| Tool | Purpose |
|---|---|
| `process_thoughts` | Write to any combination of six private categories: reflections, observations, project notes, user context, technical insights, world knowledge |
| `search_journal` | Hybrid keyword + semantic search over past entries |
| `read_journal_entry` | Read one entry in full by ID |
| `list_recent_entries` | Newest-first listing with excerpts |
| `read_recent_entries` | Full content of the N most recent entries |
| `journal_stats` | Entry count, date range, section usage, embedding coverage, storage size |

Everything is stored locally. There is exactly one outbound network call in the whole system: downloading the ~90MB embedding model from Hugging Face on first run. After that, nothing leaves the machine.

## How search works

`search_journal` runs two retrieval strategies and fuses them:

- **Vector search** — the query and every entry are embedded with `sentence-transformers/all-MiniLM-L6-v2` (384 dimensions, run locally via [hugot](https://github.com/knights-analytics/hugot)'s pure-Go ONNX backend — no CGo, no system ONNX Runtime), compared via [sqlite-vec](https://github.com/asg017/sqlite-vec) KNN.
- **Keyword search** — SQLite FTS5 (BM25), matching on *any* query word, not requiring all of them.

The two ranked lists are combined with weighted reciprocal rank fusion (vector 0.6, keyword 0.4), then a bounded recency boost (×0.8 to ×1.0, halving every 90 days) nudges newer entries up; it can move a result several places, so it is more than a tie-breaker. This is a hybrid design, not pure vector similarity. One known limitation of the current weights: once ten or more entries have embeddings, an entry that matches only by keyword cannot reach the first page of results at the default limit, because the keyword arm's best fused score sits below every vector candidate's. The keyword arm reorders results; it does not yet add new ones. The redesign in `docs/roadmap.md` (phase P2) fixes this.

Section filters are resolved in SQL first and applied inside the keyword query. The vector query cannot take the filter, so it over-fetches up to 2,000 nearest neighbours and filters them in Go; a filtered search is therefore exact only while the journal has fewer than about 2,000 embedded entries. Date filtering exists in the search code but is not yet exposed by any tool.

## Storage

One SQLite file per `JOURNAL_TOKEN`, at `~/.memo-mcp/<token>.db` (or under `$JOURNAL_PATH` if set). Directories and files memo-mcp creates are restricted to the owner (`0700`/`0600`); a directory that already existed with wider permissions is not tightened. WAL mode is on, so two processes touching the same token (e.g. two concurrent Claude Code sessions) don't collide.

The token is explicit rather than inferred from the working directory — set `JOURNAL_TOKEN` per project (in the MCP server config, not the shell) and each project gets its own isolated journal. There's no cross-project sharing by default.

## Setup

Requires Go 1.26+.

```bash
git clone https://github.com/kKEo/memory-find
cd memory-find
make build
```

This produces a single `memo-mcp` binary (`CGO_ENABLED=0`, ~30MB, no runtime dependencies).

Add it to Claude Code or Claude Desktop's MCP config:

```json
{
  "mcpServers": {
    "memo-journal": {
      "command": "/path/to/memo-mcp",
      "env": {
        "JOURNAL_TOKEN": "my-project"
      }
    }
  }
}
```

On first start memo-mcp downloads the ~90MB embedding model before it begins answering MCP requests; it prints a single "Downloading embedding model (first run only)..." line to stderr and no progress bar. If your client times out on that first start, run `memo-mcp --redownload-model` once from a terminal. If a download is interrupted, memo-mcp detects the incomplete cache and retries on the next run — it doesn't need to be deleted by hand.

### CLI flags

Running the binary directly (rather than as an MCP server) supports:

- `--stats` — print journal statistics and exit (no model load, no network; note that it opens the database read-write, so a mistyped `JOURNAL_TOKEN` creates an empty journal)
- `--redownload-model` — force a fresh model download, discarding any cached copy, and exit

### Environment variables

| Variable | Required | Purpose |
|---|---|---|
| `JOURNAL_TOKEN` | yes | Selects which journal database to open. Letters, digits, `.`, `_`, `-` only. |
| `JOURNAL_PATH` | no | Overrides the storage directory (default `~/.memo-mcp`) |

## How this differs from Claude Code's built-in memory

Claude Code ships with its own memory (Auto Memory, and the API-level file-based memory tool for custom agents). Those are good defaults and, for most people, probably the right choice.

memo-mcp exists as something different: a small, fully local, fully readable retrieval system you can inspect and measure, not a black box. The scoring constants are named at the top of `internal/search/search.go` (the recency floor and weight, `0.8` and `0.2`, are still inline literals in `recencyFactor`), the whole write path is one file, and there's nothing hidden behind a managed service. The eval harness in `internal/eval/` records a retrieval baseline that every change is checked against. If you want to understand *why* a memory system returns what it returns, or experiment with retrieval strategies on your own data, that's what this is for.

## Privacy

- All processing is local — embeddings run in-process, search runs in SQLite.
- No network calls after the one-time model download.
- No telemetry, no analytics, no external logging.
- Source is small enough to read in full; nothing is obfuscated or minified.
- Two things to know: the journal is a plaintext SQLite file that anyone with access to your home directory can read, and everything the model writes or searches passes through the MCP host as tool input, so it is as private as that host. If the embedding model is unavailable, search silently degrades to keyword-only and entries written in that state never get a vector (there is no backfill yet).

## Project status

This is currently a working, tested MCP server undergoing active hardening. The plan for where it goes next is [`docs/roadmap.md`](docs/roadmap.md); the research behind it is [`docs/knowledge-base-sota.md`](docs/knowledge-base-sota.md). Contributions and issues welcome.

## License

MIT — see [`LICENSE`](LICENSE). Derived from [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp), also MIT.
