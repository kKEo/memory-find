<!-- mcp-name: io.github.kKEo/memory-find -->

# memo-mcp

A local [MCP](https://modelcontextprotocol.io) server that gives agents a measurable, explainable knowledge base: one Go binary, no cloud, no CGo, one SQLite file per knowledge base. Documents and facts go in with provenance; searches come back as one ranked list from four retrieval arms, each result with its address, trust and a reason for its rank. A human reads the same knowledge base from the terminal or as exported markdown.

It began as a Go rewrite of [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp) and was rebuilt from scratch as a knowledge base in 2026-10. Version 1.0 fixes the contract: tool names and parameters, the `memo://` addresses, the explain fields and the export format. How search decides is written down in [`docs/architecture.md`](docs/architecture.md); the plan for the research layers (graph, compaction, web UI) is [`docs/roadmap.md`](docs/roadmap.md); the research behind it is [`docs/knowledge-base-sota.md`](docs/knowledge-base-sota.md).

## What it does

Claude (or any MCP client) gets ten tools over one knowledge base:

| Tool | Purpose |
|---|---|
| `ingest` | Store a document the agent fetched or wrote, as markdown; identical content is a no-op, changed content or a new version becomes a new revision |
| `search` | Find passages by words, exact identifiers and meaning, fused into one list; `response_format: explain` says why each result ranked |
| `read` | Dereference a `memo://` address: a passage, its section, or the whole document under a token budget |
| `remember` | Record one atomic fact with evidence and validity dates; to correct a fact, pass `supersedes` and the old one is kept as history |
| `forget` | Retire a document or fact with a reason; it leaves every index and its address resolves to "forgotten on … because …". Tool calls may only retire records written by tools |
| `promote` | Ask to raise a record's trust; tool calls cannot do it themselves. Clients that can show a dialog ask the human directly (excerpt, source, target level); others get the command a human runs |
| `explore` | Walk the graph index from one named thing: the passages that mention it and the things mentioned alongside it, each with evidence addresses |
| `compact` | Propose tidying work: entities that deserve a page, stale pages, facts that disagree, near-duplicate names and passages. Each item carries the passages and facts needed; the server never writes a page |
| `submit` | Hand back a page, a conflict decision or a merge decision. Pages are stored as derived (`is_inference`) with the passages they cite; the omission check reports facts the page left out; `dry_run` shows the diff first |
| `status` | Namespaces, the embedding model, pending vectors, background jobs, graph and page counts |

The same addresses are readable as MCP resources (`memo://doc/{id}`, `memo://chunk/{id}`, `memo://source/{id}`, `memo://fact/{id}`), and `memo://index` or `memo://ns/{namespace}/index` give a one-line-per-document view under 8 KB for the start of a session. [`SKILL.md`](SKILL.md) tells an agent how to use the tools well; `memo-mcp export --index` prints the same index for an `AGENTS.md` or `CLAUDE.md` file.

Everything is stored locally. There is exactly one outbound network call in the whole system: downloading the embedding model from Hugging Face on first start. After that, nothing leaves the machine. The server never fetches URLs; the agent fetches and passes the text.

## How search works

`search` runs up to three retrieval arms over the same pre-filtered set of live passages and fuses them:

- **Keyword arm** — SQLite FTS5 with the Porter stemmer and BM25 scoring, over the passage and its section header. "review" finds "reviewing".
- **Exact arm** — a second FTS5 index that keeps identifiers whole (`useCallback`, `net/http`, `ERR_CONN_RESET`). Added automatically when the query looks like code.
- **Semantic arm** — the query and every passage are embedded with the configured model (default `granite-small-r2`, IBM granite-embedding-small-english-r2, 384 dimensions, run locally via [hugot](https://github.com/knights-analytics/hugot)'s pure-Go ONNX backend) and compared by cosine similarity in a plain SQLite table. `memo-mcp model ls` lists the alternatives, including the instant static model `potion`.

The three ranked lists are combined with reciprocal rank fusion at equal weights (a keyword-only hit at rank 1 ties a vector hit at rank 1, so the keyword arm can add results rather than only reorder them), passages are aggregated to documents by their best passage, notes and conversations get a bounded recency boost (×0.8 to ×1.0, halving every 90 days; versioned docs do not age), the list is cut at the first large score gap, and results are packed to the requested token budget. A passage whose only evidence is a semantic similarity below the weak band (0.30) is dropped, so a question about nothing in the corpus returns zero results with a reason and a hint instead of a page of noise.

Two **structural arms** join when the question names two or more known things or asks how things relate: the entity arm returns the passages that mention the named entities, and the graph arm walks the mention graph with personalised PageRank from each named entity and returns the passages all the walks agree on, which is how a question about the Billing Service finds the replication page that never names it. `explore` walks the same graph by hand. Both are routed rather than always on because an always-on entity arm made plain lookups worse.

Curated **pages** are a third thing to search (`granularity: page`): markdown an agent wrote from passages through `compact` and `submit`, stored as derived, citing the passages it was built from, and marked stale the moment one of those passages' documents is revised or forgotten.

A fourth text arm matches **facts** recorded with `remember` and votes for their evidence passage ("facts as extra keys"); `granularity: fact` returns the facts themselves. `as_of` answers with what the knowledge base believed at a date: superseded revisions and replaced facts that were current then. Forgotten records are never returned, not even under `as_of`. Scope filters (namespaces, kinds, sources, library, version, tags, dates, minimum trust) are applied inside every arm's query, before ranking, so a filtered search never loses a result. Each result carries its provenance and a relevance band; with `response_format: explain` it also carries the per-arm ranks and contributions, the recency factor, and a per-query trace (which arms ran and why, what the scope excluded, where the list was cut). The terminal shows the same numbers: `memo-mcp search "<q>" --explain` and `memo-mcp explain "<q>" memo://chunk/<n>`.

## Storage

One SQLite file per `MEMO_KB` name, at `~/.memo-mcp/kb/<name>.db` (or under `$MEMO_HOME/kb/` if set); the deprecated `JOURNAL_TOKEN` keeps opening `~/.memo-mcp/<token>.db`. Directories and files memo-mcp creates are restricted to the owner (`0700`/`0600`); a directory that already existed with wider permissions is not tightened. WAL mode is on, so two processes touching the same token (e.g. two concurrent Claude Code sessions) don't collide.

The name is explicit rather than inferred from the working directory — set `MEMO_KB` per project (in the MCP server config, not the shell) and each project gets its own isolated knowledge base. Inside one file, namespaces are shelves that a search spans by default; separate files are the privacy boundary.

## Setup

**From a release.** Download the archive for your platform from [releases](https://github.com/kKEo/memory-find/releases), unpack `memo-mcp` somewhere on your `PATH`, and run `memo-mcp version`. Archives exist for macOS and Linux (amd64, arm64) and Windows (amd64). The server is also listed in the MCP registry as `io.github.kKEo/memory-find`.

**From source.** Requires Go 1.26+.

```bash
git clone https://github.com/kKEo/memory-find
cd memory-find
make build
```

This produces a single `memo-mcp` binary (`CGO_ENABLED=0`, ~30MB, no runtime dependencies).

With Claude Code:

```bash
claude mcp add memo --env MEMO_KB=my-project -- /path/to/memo-mcp
```

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

On first start memo-mcp downloads the default embedding model (granite-embedding-small-english-r2, Apache-2.0, about 140MB) into `~/.cache/memo-mcp/models` and prints one line to stderr. Until the model is ready, search runs keyword-only and says so (`degraded`); documents written meanwhile get their vectors when the model arrives. To download ahead of time run `memo-mcp model pull granite-small-r2`. An interrupted download is detected and retried on the next run.

### Commands

Running the binary with no arguments starts the MCP server on stdio. From the terminal:

- `memo-mcp ingest <file|dir|-> [--ns --kind --uri --title --library --version --trust --context --embed=false]` — add markdown documents; identical content is a no-op, changed content becomes a new revision
- `memo-mcp search "<query>" [--mode auto|hybrid|keyword|exact|semantic --ns --library --version --kind --limit --format table|json|md --explain --no-model]` — search; `--explain` adds why each result ranked
- `memo-mcp explain "<query>" <memo://chunk/n>` — the full explanation for one result
- `memo-mcp log tail|show <id>|replay|prune` — the opt-in query log (`MEMO_QUERY_LOG=1`); `replay` prints logged searches as unlabelled eval candidates (one JSON object per line) ready to be labelled and added to a corpus
- `memo-mcp remember "<fact>" [--ns --about --valid-from --valid-to --supersedes --evidence --trust user|curated]` — record a fact (CLI writes are trust `user`; `curated` must be typed)
- `memo-mcp forget <memo://...> --reason "<why>" [--redact]` — retire a document or fact; the reason is kept and shown
- `memo-mcp facts ls [--ns --as-of YYYY-MM-DD --history]` — list facts, or what was believed on a date
- `memo-mcp trust ls | promote <uri> --to user|curated | demote <uri> --to agent|user` — the human channel for trust; every change is audited
- `memo-mcp explore <name> [--ns --hops 1|2 --as-of --json]` — walk the graph index from one entity: its passages and the entities mentioned alongside it, with evidence addresses
- `memo-mcp graph merges [--state open|merged|rejected|all] | merge <id> | reject <id>` — the review queue of near-duplicate entity names; nothing is merged without a human decision
- `memo-mcp compact [--ns --kinds page,stale,conflict,merge,duplicate --lint --json]` — propose compaction work; `--executor ollama [--apply]` writes the page items with a local model (`MEMO_OLLAMA_URL`, `MEMO_OLLAMA_MODEL`; dry run unless `--apply`)
- `memo-mcp submit <item-id> [--content-file page.md | --keep <memo://fact/..> | --accept|--reject | --skip] [--reason ..] [--dry-run]` — the human side of a work item
- `memo-mcp lint [--ns]` — contradictions, orphan entities, missing or stale pages, expired facts; changes nothing
- `memo-mcp pages ls [--ns --stale]` — list curated pages
- `memo-mcp read <memo://doc/...> [--history]` — print a document, chunk, source or fact with its provenance, or its revision chain
- `memo-mcp ls [--ns --kind --since --json]` — list live documents, newest first
- `memo-mcp export --md <dir> [--ns]` — write markdown files with front-matter provenance (opens in Obsidian; re-importing yields no new revisions)
- `memo-mcp export --index [--ns --library x@v --max-bytes 8192]` — print a compact index (title, address, kind, version, trust per document; facts summarised) sized for `AGENTS.md`/`CLAUDE.md`; lines that do not fit are counted in a footer
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
| `MEMO_MODEL` | no | Embedding model id from `memo-mcp model ls` (default `granite-small-r2`). Queries use that model's vectors; run `memo-mcp reindex` after switching |
| `MEMO_PROFILE` | no | Ranking profile (default `default`); see `memo-mcp profiles show` |
| `MEMO_OLLAMA_URL`, `MEMO_OLLAMA_MODEL` | no | Optional local model for `memo-mcp compact --executor ollama`; loopback only unless `--allow-remote`. Nothing else uses it |
| `MEMO_RERANK` | no | `1` loads the cross-encoder reranker (`cross-encoder/ms-marco-MiniLM-L6-v2`, Apache-2.0, ~91 MB); only profiles with rerank on (`precise`) use it |
| `JOURNAL_TOKEN` | deprecated | Old name selector: opens `<JOURNAL_PATH or ~/.memo-mcp>/<token>.db` exactly as before, with a warning. Honoured for one release. |
| `JOURNAL_PATH` | deprecated | Old base directory override, only with `JOURNAL_TOKEN` |

## How this differs from Claude Code's built-in memory

Claude Code ships with its own memory (Auto Memory, and the API-level file-based memory tool for custom agents). Those are good defaults and, for most people, probably the right choice.

memo-mcp exists as something different: a small, fully local, fully readable retrieval system you can inspect and measure, not a black box. Every ranking constant is a named profile field with a written derivation (`memo-mcp profiles show`), every result can explain its rank (`response_format: explain`, `memo-mcp explain`), every record carries where it came from and who vouched for it, and the eval harness in `internal/eval/` records a baseline that every change is checked against query by query. If you want to understand *why* a memory system returns what it returns, or experiment with retrieval strategies on your own data, that's what this is for.

## Reading further

- [`docs/architecture.md`](docs/architecture.md): layers, the write and read paths, the formulas, profiles, the explain contract, the address scheme.
- [`docs/schema.md`](docs/schema.md): every table and column in plain words; trust transitions; what `as_of` can see.
- [`docs/eval/`](docs/eval/): one measured report per tag.
- [`articles/`](articles/): one article per phase, written for beginners: [why rebuild instead of migrate](articles/why-rebuild-instead-of-migrate.md), [designing the knowledge schema](articles/designing-the-knowledge-schema.md), [search that explains itself](articles/search-that-explains-itself.md), [the embedder is the biggest lever](articles/the-embedder-is-the-biggest-lever.md), [provenance, trust and time](articles/provenance-trust-and-time.md), [designing tools for agents](articles/designing-tools-for-agents.md), [shipping a pure-Go MCP server](articles/shipping-a-pure-go-mcp-server.md), [graph as an index, not an oracle](articles/graph-as-an-index-not-an-oracle.md).
- [`articles/compaction-without-a-server-llm.md`](articles/compaction-without-a-server-llm.md): how the agent writes the wiki and the server keeps it honest.
- [`CHANGELOG.md`](CHANGELOG.md).

## Privacy

- All processing is local — embeddings run in-process, search runs in SQLite.
- No network calls after the one-time model download.
- No telemetry, no analytics, no external logging.
- Source is small enough to read in full; nothing is obfuscated or minified.
- Raising trust needs a human. `promote` uses MCP elicitation: the client shows a dialog with the excerpt, source and target level, and only an accepted dialog applies the change. A hook or setting that auto-accepts elicitation dialogs removes that protection; if you configure one, treat `user` and `curated` records as no more trusted than `agent` ones.
- Two things to know: the knowledge base is a plaintext SQLite file that anyone with access to your home directory can read, and everything the model writes or searches passes through the MCP host as tool input, so it is as private as that host. If the embedding model is unavailable, search runs keyword-only and says so (`degraded`), and documents written in that state get their vectors when `memo-mcp backfill` or the next server start runs.

## Project status

1.0: the core knowledge base is complete and measured. What is stable, and what 1.x adds (graph as an index, compaction and pages, an optional read-only web UI), is in [`docs/roadmap.md`](docs/roadmap.md). Not planned: HTTP transport, a server-side LLM, importing the v0 journal files. Contributions and issues welcome.

## License

MIT — see [`LICENSE`](LICENSE). Derived from [obra/private-journal-mcp](https://github.com/obra/private-journal-mcp), also MIT.
