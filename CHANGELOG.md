# Changelog

All notable changes. Tags follow semantic versioning; each tag has an eval report in
`docs/eval/` from `v0.6.0` on.

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
