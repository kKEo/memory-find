<!-- mcp-name: io.github.kKEo/memory-find -->

# memo-mcp

A local [MCP](https://modelcontextprotocol.io) server that gives agents a measurable, explainable knowledge base. Single Go binary, no cloud, no CGo — one SQLite file per knowledge base, three retrieval arms fused into one ranked list, provenance and trust on every record, and a codebase small enough to read end to end.

It started as a Go rewrite of [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp) (TypeScript) and has since been rebuilt as a knowledge base: documents with provenance instead of journal entries, three retrieval arms instead of one vector, and an evaluation harness that measures every change.

> **Where this is going.** The plan is [`docs/roadmap.md`](docs/roadmap.md) (phases P0–P2 are
> built; P3 adds the measurement lab and the embedding-model bake-off); the research behind it is
> [`docs/knowledge-base-sota.md`](docs/knowledge-base-sota.md). Everything below describes the
> binary as it is today.

## What it does

Claude (or any MCP client) gets four tools over one knowledge base:

| Tool | Purpose |
|---|---|
| `ingest` | Store a document the agent fetched or wrote, as markdown; identical content is a no-op, changed content or a new version becomes a new revision |
| `search` | Find passages by words, exact identifiers and meaning, fused into one list; `response_format: explain` says why each result ranked |
| `read` | Dereference a `memo://` address: a passage, its section, or the whole document under a token budget |
| `status` | Namespaces, the embedding model, pending vectors, background jobs |

Everything is stored locally. There is exactly one outbound network call in the whole system: downloading the ~90MB embedding model from Hugging Face on first start. After that, nothing leaves the machine. The server never fetches URLs; the agent fetches and passes the text.

## How search works

`search` runs up to three retrieval arms over the same pre-filtered set of live passages and fuses them:

- **Keyword arm** — SQLite FTS5 with the Porter stemmer and BM25 scoring, over the passage and its section header. "review" finds "reviewing".
- **Exact arm** — a second FTS5 index that keeps identifiers whole (`useCallback`, `net/http`, `ERR_CONN_RESET`). Added automatically when the query looks like code.
- **Semantic arm** — the query and every passage are embedded with `sentence-transformers/all-MiniLM-L6-v2` (384 dimensions, run locally via [hugot](https://github.com/knights-analytics/hugot)'s pure-Go ONNX backend) and compared by cosine distance in a plain SQLite table.

The three ranked lists are combined with reciprocal rank fusion at equal weights (a keyword-only hit at rank 1 ties a vector hit at rank 1, so the keyword arm can add results rather than only reorder them), passages are aggregated to documents by their best passage, notes and conversations get a bounded recency boost (×0.8 to ×1.0, halving every 90 days; versioned docs do not age), the list is cut at the first large score gap, and results are packed to the requested token budget. A passage whose only evidence is a semantic similarity below the weak band (0.30) is dropped, so a question about nothing in the corpus returns zero results with a reason and a hint instead of a page of noise.

Scope filters (namespaces, kinds, sources, library, version, tags, dates, minimum trust) are applied inside every arm's query, before ranking, so a filtered search never loses a result. Each result carries its provenance and a relevance band; with `response_format: explain` it also carries the per-arm ranks and contributions, the recency factor, and a per-query trace (which arms ran and why, what the scope excluded, where the list was cut). The terminal shows the same numbers: `memo-mcp search "<q>" --explain` and `memo-mcp explain "<q>" memo://chunk/<n>`.

## Storage

One SQLite file per `MEMO_KB` name, at `~/.memo-mcp/kb/<name>.db` (or under `$MEMO_HOME/kb/` if set); the deprecated `JOURNAL_TOKEN` keeps opening `~/.memo-mcp/<token>.db`. Directories and files memo-mcp creates are restricted to the owner (`0700`/`0600`); a directory that already existed with wider permissions is not tightened. WAL mode is on, so two processes touching the same token (e.g. two concurrent Claude Code sessions) don't collide.

The name is explicit rather than inferred from the working directory — set `MEMO_KB` per project (in the MCP server config, not the shell) and each project gets its own isolated knowledge base. Inside one file, namespaces are shelves that a search spans by default; separate files are the privacy boundary.

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
    "memo": {
      "command": "/path/to/memo-mcp",
      "env": {
        "MEMO_KB": "my-project"
      }
    }
  }
}
```

On first start memo-mcp downloads the ~90MB embedding model before it begins answering MCP requests; it prints a single "Downloading embedding model (first run only)..." line to stderr and no progress bar. If your client times out on that first start, run `memo-mcp --redownload-model` once from a terminal. If a download is interrupted, memo-mcp detects the incomplete cache and retries on the next run — it doesn't need to be deleted by hand.

### Commands

Running the binary with no arguments starts the MCP server on stdio. From the terminal:

- `memo-mcp ingest <file|dir|-> [--ns --kind --uri --title --library --version --trust --context --embed=false]` — add markdown documents; identical content is a no-op, changed content becomes a new revision
- `memo-mcp search "<query>" [--mode auto|hybrid|keyword|exact|semantic --ns --library --version --kind --limit --format table|json|md --explain --no-model]` — search; `--explain` adds why each result ranked
- `memo-mcp explain "<query>" <memo://chunk/n>` — the full explanation for one result
- `memo-mcp log tail|show <id>|prune` — the opt-in query log (`MEMO_QUERY_LOG=1`)
- `memo-mcp read <memo://doc/...>` — print a document, chunk or source with its provenance
- `memo-mcp ls [--ns --kind --since --json]` — list live documents, newest first
- `memo-mcp export --md <dir> [--ns]` — write markdown files with front-matter provenance (opens in Obsidian; re-importing yields no new revisions)
- `memo-mcp verify [--repair]` — check chunks, vectors and indexes
- `memo-mcp backfill` — embed chunks whose vectors are pending
- `memo-mcp status` — print knowledge-base statistics (read-only; never creates a file)

The lab:

- `memo-mcp eval [--models hash,minilm,potion,granite-small-r2 --profiles default,all --corpus notes|kb|all --format table|md|json --explain-failures --rerank --agent-proxy]` — load the fixture corpora into a throwaway knowledge base with each model, run the labelled queries under each profile, print quality (recall, MRR, nDCG, abstention) next to cost (latency, tokens). `hash` is the deterministic test embedder; other ids download real models.
- `memo-mcp model ls | smoke <id>|--all | pull <id> | use <id> | redownload [<id>]` — the embedding-model registry: list candidates with licences, prove which ones load under the pure-Go backend and how fast, download, or make one the knowledge base's default.
- `memo-mcp reindex [--model <id>]` — embed every passage that lacks a vector for a model ("re-embed, don't re-chunk"); vectors for several models coexist, so switching back is free.
- `memo-mcp profiles show [<name>]` — print every ranking constant of a profile with its derivation. Profiles: `default`, `precise` (deeper fetch, cross-encoder rerank when attached), `recency`, `code`, `minmax` (score fusion instead of rank fusion), and the ablations `keyword-only`, `semantic-only`. Overrides live in `$MEMO_HOME/profiles.json`.
- `memo-mcp version` — print the build version, the MCP protocol version, the Go version and the model directory
- `memo-mcp model redownload` — force a fresh model download, discarding any cached copy, and exit

The old spellings `--stats` and `--redownload-model` still work for one release and print a deprecation warning.

### Environment variables

| Variable | Required | Purpose |
|---|---|---|
| `MEMO_KB` | no | Selects the database to open; the file is `$MEMO_HOME/kb/<name>.db`. Letters, digits, `.`, `_`, `-` only. Default `default`. |
| `MEMO_HOME` | no | Base directory (default `~/.memo-mcp`) |
| `MEMO_QUERY_LOG` | no | `1` keeps an opt-in log of searches (arguments, result addresses, scores, trace; never passage text) in the same file |
| `MEMO_MODEL` | no | Embedding model id from `memo-mcp model ls` (default `minilm`). Queries use that model's vectors; run `memo-mcp reindex` after switching |
| `MEMO_PROFILE` | no | Ranking profile (default `default`); see `memo-mcp profiles show` |
| `MEMO_RERANK` | no | `1` loads the cross-encoder reranker (`cross-encoder/ms-marco-MiniLM-L6-v2`, Apache-2.0, ~91 MB); only profiles with rerank on (`precise`) use it |
| `JOURNAL_TOKEN` | deprecated | Old name selector: opens `<JOURNAL_PATH or ~/.memo-mcp>/<token>.db` exactly as before, with a warning. Honoured for one release. |
| `JOURNAL_PATH` | deprecated | Old base directory override, only with `JOURNAL_TOKEN` |

## How this differs from Claude Code's built-in memory

Claude Code ships with its own memory (Auto Memory, and the API-level file-based memory tool for custom agents). Those are good defaults and, for most people, probably the right choice.

memo-mcp exists as something different: a small, fully local, fully readable retrieval system you can inspect and measure, not a black box. The scoring constants are named at the top of `internal/search/search.go` (the recency floor and weight, `0.8` and `0.2`, are still inline literals in `recencyFactor`), the whole write path is one file, and there's nothing hidden behind a managed service. The eval harness in `internal/eval/` records a retrieval baseline that every change is checked against. If you want to understand *why* a memory system returns what it returns, or experiment with retrieval strategies on your own data, that's what this is for.

## Privacy

- All processing is local — embeddings run in-process, search runs in SQLite.
- No network calls after the one-time model download.
- No telemetry, no analytics, no external logging.
- Source is small enough to read in full; nothing is obfuscated or minified.
- Two things to know: the knowledge base is a plaintext SQLite file that anyone with access to your home directory can read, and everything the model writes or searches passes through the MCP host as tool input, so it is as private as that host. If the embedding model is unavailable, search runs keyword-only and says so (`degraded`), and documents written in that state get their vectors when `memo-mcp backfill` or the next server start runs.

## Project status

This is currently a working, tested MCP server undergoing active hardening. The plan for where it goes next is [`docs/roadmap.md`](docs/roadmap.md); the research behind it is [`docs/knowledge-base-sota.md`](docs/knowledge-base-sota.md). Contributions and issues welcome.

## License

MIT — see [`LICENSE`](LICENSE). Derived from [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp), also MIT.
