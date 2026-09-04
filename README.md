<!-- mcp-name: io.github.kmaziarz/memo-mcp -->

# memo-mcp

A local [MCP](https://modelcontextprotocol.io) server that gives Claude a private, searchable journal. Single Go binary, no cloud, no CGo — one SQLite file per project, hybrid keyword+semantic search, and a small enough codebase (~1,500 lines) to read end to end.

It's a Go rewrite of [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp) (TypeScript), keeping the same tool surface and journal categories while replacing per-entry markdown files with a single SQLite database and a from-scratch pure-Go embedding pipeline.

## What it does

Claude gets six tools:

| Tool | Purpose |
|---|---|
| `process_thoughts` | Write to any combination of six private categories: reflections, observations, project notes, user context, technical insights, world knowledge |
| `search_journal` | Hybrid keyword + semantic search over past entries |
| `read_journal_entry` | Read one entry in full by ID |
| `list_recent_entries` | Chronological listing with excerpts |
| `read_recent_entries` | Full content of the N most recent entries |
| `journal_stats` | Entry count, date range, section usage, embedding coverage, storage size |

Everything is stored locally. There is exactly one outbound network call in the whole system: downloading the ~90MB embedding model from Hugging Face on first run. After that, nothing leaves the machine.

## How search works

`search_journal` runs two retrieval strategies and fuses them:

- **Vector search** — the query and every entry are embedded with `sentence-transformers/all-MiniLM-L6-v2` (384 dimensions, run locally via [hugot](https://github.com/knights-analytics/hugot)'s pure-Go ONNX backend — no CGo, no system ONNX Runtime), compared via [sqlite-vec](https://github.com/asg017/sqlite-vec) KNN.
- **Keyword search** — SQLite FTS5 (BM25), matching on *any* query word, not requiring all of them.

The two ranked lists are combined with weighted reciprocal rank fusion, then a mild recency tie-breaker nudges newer entries up. This is a hybrid design, not pure vector similarity — keyword matching handles exact terms and identifiers (error codes, symbol names) that embeddings tend to blur together, while vector search handles paraphrase and semantic similarity that keyword matching misses entirely.

Section and date filters run as part of the same query, not as a post-filter over a truncated candidate list — so a filtered search sees every matching entry, not just whichever ones happened to rank near the top of an unfiltered pass.

## Storage

One SQLite file per `JOURNAL_TOKEN`, at `~/.memo-mcp/<token>.db` (or under `$JOURNAL_PATH` if set). Directory and file permissions are restricted to the owner (`0700`/`0600`). WAL mode is on, so two processes touching the same token (e.g. two concurrent Claude Code sessions) don't collide.

The token is explicit rather than inferred from the working directory — set `JOURNAL_TOKEN` per project (in the MCP server config, not the shell) and each project gets its own isolated journal. There's no cross-project sharing by default.

## Setup

Requires Go 1.26+.

```bash
git clone https://github.com/kmaziarz/memo-mcp
cd memo-mcp
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

The first tool call that needs embeddings triggers a one-time model download (progress goes to stderr). If it's interrupted, memo-mcp detects the incomplete cache and retries on the next run — it doesn't need to be deleted by hand.

### CLI flags

Running the binary directly (rather than as an MCP server) supports:

- `--stats` — print journal statistics and exit (no model load, no network)
- `--redownload-model` — force a fresh model download, discarding any cached copy, and exit

### Environment variables

| Variable | Required | Purpose |
|---|---|---|
| `JOURNAL_TOKEN` | yes | Selects which journal database to open. Letters, digits, `.`, `_`, `-` only. |
| `JOURNAL_PATH` | no | Overrides the storage directory (default `~/.memo-mcp`) |

## How this differs from Claude Code's built-in memory

Claude Code ships with its own memory (Auto Memory, and the API-level file-based memory tool for custom agents). Those are good defaults and, for most people, probably the right choice.

memo-mcp exists as something different: a small, fully local, fully readable retrieval system you can inspect and measure, not a black box. Every scoring constant is a named constant in `internal/search/search.go`, the whole write path is one file, and there's nothing hidden behind a managed service. If you want to understand *why* a memory system returns what it returns, or experiment with retrieval strategies on your own data, that's what this is for.

## Privacy

- All processing is local — embeddings run in-process, search runs in SQLite.
- No network calls after the one-time model download.
- No telemetry, no analytics, no external logging.
- Source is small enough to read in full; nothing is obfuscated or minified.

## Project status

This is currently a working, tested MCP server undergoing active hardening — see [`docs/`](docs/) for the design history and roadmap. Contributions and issues welcome.

## License

MIT — see [`LICENSE`](LICENSE). Derived from [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp), also MIT.
