# Changelog

All notable changes. Tags follow semantic versioning; each tag has an eval report in
`docs/eval/` from `v0.6.0` on.

## [Unreleased]

- Documentation: two GitBook-compatible guides under `docs/guide/`: a plain-English user guide
  (quick start, everyday use, trust, privacy, use cases) and an operator guide (install,
  configuration reference, tuning, lifecycle, metric catalogue, Prometheus rules and alerts,
  backup, troubleshooting). Built with mdBook (`make docs`, `make docs-serve`) and deployed to
  GitHub Pages from `master` by `.github/workflows/docs.yml`.
- `scripts/docs-check.sh`: fails when a `MEMO_*` variable, a command or a metric in the code is
  missing from the operator guide, or a guide link is broken.
- CI: the Windows test job is disabled for now; release archives for Windows are still built.
- Web UI ingest console (`/ingest`, `/ingest/{id}`): every `memo-mcp ingest` batch and MCP
  `ingest` call is recorded as a run with live progress (running / done / failed / stalled) and
  stats: documents written, unchanged and failed, chunks, embedded and pending vectors, bytes,
  duration and docs/s, plus one row per document. The page reloads itself while a run is in
  progress (no JS; the CSP is unchanged). `/status` lists the last five runs. Schema migration 5
  adds `ingest_runs` and `ingest_run_items`.
- `memo-mcp serve --http 127.0.0.1:8765` (`MEMO_HTTP_ADDR`): one long-running MCP server over
  streamable HTTP at `/mcp` for any number of clients (`claude mcp add --transport http`), with
  the web UI and `/metrics` on the same port. Loopback only unless `--allow-remote`. Only in
  this mode the UI gains a **Live** page: active clients, calls in flight (an ingest links to
  its run), background backfill/reindex state, and per-tool calls, errors and p50/p95 latency
  plus ingest throughput since start. `/ingest` also lists ingest calls still in flight.
- `serve --http` authentication: a bearer token (`<MEMO_HOME>/http-token`, 0600, printed by the
  new `memo-mcp http-token [--rotate]`) protects `/mcp`, the UI and `/metrics`, and is on by
  default whenever the server is reachable from elsewhere. Browsers log in with a single-use
  link printed at startup (or by pasting the token) and get an HttpOnly, SameSite=Strict
  session cookie. HTTPS with `--tls-cert`/`--tls-key`, optional mTLS with `--tls-client-ca`,
  `--behind-proxy` for a TLS-terminating proxy, and `--public-url`. Plain HTTP on a
  non-loopback address and unauthenticated exposure are refused.
- Fix MCP registry publishing: `server.json` no longer sets `registryBaseUrl` on the MCPB package.

## [1.4.0] — 2026-10-03 — measuring the server itself (P10)

- `internal/obs`: stdlib metrics registry, Prometheus text format, runtime subset, loopback
  metrics server, structured-logging setup.
- `memo-mcp serve --metrics-addr` / `MEMO_METRICS_ADDR`; UI `/metrics`; `memo-mcp metrics`.
- Metrics for every MCP request and tool call, every search and arm, every audited write,
  embeddings, graph cache, and table-count gauges.
- `log/slog` on stderr with `MEMO_LOG_FORMAT` and `MEMO_LOG_LEVEL`; one line per tool call and
  per search; all ad-hoc stderr prints replaced.
- The MCP `logging` capability is no longer advertised (deprecated; decision D-O).
- Migration 4 `call_log`; `memo-mcp log calls`; `log tail` and the UI `/log` page show tool
  calls next to searches; pruning covers both.
- Privacy sentence amended: local metrics and logs are not telemetry.

## [1.3.0] — 2026-10-03 — a knowledge base you can read (P9)

- `memo-mcp ui`: read-only web face on loopback, server-rendered, no JavaScript. Search with
  the explain and trace tables, documents and passages with provenance and history, the facts
  timeline with `as_of`, entity neighbourhoods, pages with sources, status, lint, the query
  log, the eval report. GET only; Host-header allowlist; `--allow-remote` required for a
  non-loopback address.
- A test asserts the UI's numbers equal the retrieval service's for the same query.
- `memo-mcp migrate`; read-only commands refuse a file at an older schema with that advice.
- `memo-mcp graph rebuild [--ns]` re-extracts mentions for files written before 1.1.

## [1.2.0] — 2026-10-03 — compaction and pages (P8)

- Migration 3: `pages`, `page_sources`, `page_vecs`, `pages_fts`, `work_items`.
- Pages are written only by the calling agent: `compact` proposes work items (page, stale,
  conflict, merge, duplicate) with full payloads; `submit` stores pages as derived
  (`is_inference`) with their sources, resolves conflicts under the trust rule, records merge
  decisions; `dry_run` shows the diff, the omission check and the corruption check.
- Staleness: a revised or forgotten source marks dependent pages stale with the reason.
- `lint`: contradictions, orphan entities, missing pages, stale pages, expired facts.
- `search granularity=page`; `read` and the `memo://page/{id}` resource; `status.pages`.
- CLI `compact`, `submit`, `lint`, `pages ls`; `export --md` writes `<ns>/pages/`.
- Optional `compact --executor ollama` (loopback, constrained JSON); nothing else depends on it.
- Eval: lint recall on five planted defect kinds, omission and corruption checks, raw-row
  checksum. Ten tools.

## [1.1.0] — 2026-10-03 — entities and graph, as an index (P7)

- Migration 2: `entities`, `entity_aliases`, `mentions`, `merge_candidates`, `edges`.
- Heuristic entity extraction on ingest; client-supplied `entities[]`/`relations[]`; fact
  subjects linked from `about[]`.
- Deterministic entity resolution with a review queue (`memo-mcp graph merges|merge|reject`).
- Entity arm and graph arm (personalised PageRank, product scoring), routed into `auto` for
  multi-entity or relational questions; `mode=graph`; `why.graph`, `trace.entities`.
- Tool `explore` (eight tools), resource `memo://entity/{id}`, `status.graph`; CLI `explore`.
- Eval: multi-hop slice, graph-tax column, update-stream and merge-precision tests; ablation
  profiles `text-only`, `no-graph`.
- Fixed: the fact arm had no weight in the default profile; a one-word overlap could match a
  fact.

## [1.0.0] — 2026-10-02

The 1.0 contract: tool names and parameters, the `memo://` address scheme, the explain field
names, the export front matter (`docs/architecture.md`).

- CI matrix on ubuntu, macos and windows; lint as a gate; PR CI never downloads a model.
- Nightly real-model eval workflow with a cached model directory.
- GoReleaser release pipeline: five archives, version from the tag, checksums.
- `server.json` and registry publishing through GitHub OIDC on tag.
- `docs/architecture.md`, this changelog, README rewrite.

## [0.9.0] — 2026-10-02 — the agent surface (P5)

- Every tool description carries an example.
- `search` returns `truncated` and `narrow_hint` in every format; the text mirror says it.
- `response_format: detailed` shows the passage with its neighbours.
- `promote` asks the human through a multi round-trip elicitation (SEP-2322); accepted answers
  apply with audit channel `elicitation`, otherwise the CLI command is returned.
- MCP resources mirror the read tools (`memo://doc|chunk|source|fact/{id}`,
  `memo://ns/{namespace}/index`, `memo://index`) with cache hints.
- `memo-mcp export --index`, `memo-mcp log replay`, `SKILL.md`.

## [0.8.0] — 2026-10-02 — provenance, trust and time (P4)

- Facts: `remember` (add-only, `supersedes` keeps history), `forget` (tombstones, `redact`),
  `promote`; CLI `remember`, `forget`, `facts ls --as-of`, `trust ls|promote|demote`,
  `read --history`.
- `as_of` on search and facts; forgotten records hidden even under `as_of`.
- Fact arm in fusion ("facts as extra keys"); `granularity: fact` with trust-first tiebreak.
- Trust transitions enforced by channel; `ErrNeedsHuman` carries the CLI command.
- Stopwords dropped from keyword queries. Eval slices: knowledge-update, temporal, conflict,
  revocation, write-loss, poisoning.

## [0.7.0] — 2026-10-02 — the lab (P3)

- Eval v2: knowledge-base corpus, categories, cost columns, paired per-query gate, abstention
  rate, `memo-mcp eval`.
- Embedding model registry (`memo-mcp model ls|smoke|pull|use`, `MEMO_MODEL`); vectors for
  several models coexist; `memo-mcp reindex`.
- Default model: granite-embedding-small-english-r2 (OD-6), chosen in the bake-off.
- Ranking profiles with derivations (`memo-mcp profiles show`, `$MEMO_HOME/profiles.json`,
  `MEMO_PROFILE`); per-model relevance bands.
- Optional cross-encoder reranker (`MEMO_RERANK=1`), not promoted (failed the gate).

## [0.6.0] — 2026-10-02 — search that explains itself (P2)

- `internal/retrieve`: keyword, exact and semantic arms, weighted RRF, chunk-to-document
  aggregation, recency for notes and conversations, gap cutoff, semantic-only floor, token
  budget, `exclude_ids`.
- The explain contract (`why`, `trace`) shared by CLI and MCP; structured abstention.
- Typed MCP tools `ingest`, `search`, `read`, `status` with output schemas and annotations;
  the six journal tools removed.
- Opt-in query log (`MEMO_QUERY_LOG=1`; `log tail|show|prune`).

## [0.5.0] — 2026-10-02 — the store (P1)

- New knowledge-base schema (`docs/schema.md`): sources, documents with revisions, chunks with
  three indexes, facts, models, jobs, audit. `PRAGMA application_id`; v0 journal files refused.
- Markdown-aware chunker; batch embedder interface; embedding outside the transaction with a
  backfill queue.
- CLI `ingest`, `read`, `ls`, `verify`, `export --md`, `backfill`, `status`.

## [0.4.0] — 2026-10-02 — reset the map (P0)

- Repository renamed to `github.com/kKEo/memory-find`; binary stays `memo-mcp`.
- go-sdk v1.8.0 (protocol 2026-07-28); version from the tag; CLI skeleton; `MEMO_KB`,
  `MEMO_HOME`; deprecated `JOURNAL_*` aliases.
- `.golangci.yml`, minimal CI, `signal.NotifyContext`; stats crash on fractional means fixed.
- Spikes S1–S4 (go-sdk, vector storage, FTS5, embedding models).

## [0.3.0] — 2026-09-04 — review Phases 0–1 (retroactive tag)

- Hardening of the journal-era server and the first eval harness and baseline.

## Before 0.3.0 (2026-05 to 2026-06)

- Go rewrite of obra/private-journal-mcp: six journal tools, MiniLM embeddings, FTS5, `--stats`.
