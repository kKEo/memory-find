# memo-mcp architecture

*The 1.0 contract (2026-10-02). What this file describes is stable across 1.x releases: tool
names and parameters, the `memo://` address scheme, the explain field names, the export front
matter, and the formulas below. Numbers quoted are the shipped defaults; `memo-mcp profiles
show` prints the live values with their derivations.*

Plain words first. Terms in *italics* are defined in the roadmap glossary (`roadmap.md` §15).

## 1. What it is

A single Go binary that speaks the Model Context Protocol over stdio and keeps one SQLite file
per knowledge base. An agent stores documents and facts in it, searches them with three
retrieval methods fused into one list, reads any result by address, and gets told why each result
ranked. A human reads the same knowledge base from the terminal or as exported markdown. Nothing
leaves the machine except one download of the embedding model.

```
 agent (MCP client) ── stdio ──▶ internal/server   seven typed tools, resources
                                      │
 human (terminal) ─────────────▶ internal/cli      the same operations as commands
                                      │
                                 internal/retrieve  arms → fusion → recency → cutoff → budget → explain
                                      │
                                 internal/kb        schema, ingest, facts, trust, time, export
                                      │
                                 SQLite file        FTS5 (two indexes) + vectors (plain table) + facts
                                      ▲
                                 internal/embedding hugot / GoMLX, pure-Go ONNX; model registry
```

## 2. Layers of knowledge

```
L5  pages        curated markdown an agent wrote from passages, cited and stale-aware (1.2)
L4  graph        entities, aliases, which passage mentions which entity, optional typed edges (1.1)
L3  facts        one-sentence claims with a validity window and an evidence passage
L2  chunks       passages of about 200 estimated tokens, indexed three ways
L1  documents    the text as ingested, one row per revision
L0  sources      where it came from: URL or file, library, version, hash, trust, origin
     bookkeeping namespaces, models, jobs, audit, query_log
```

L4 arrived in 1.1 as migration 2 and L5 in 1.2 as migration 3, both additive. Every layer points down: a fact points at its evidence chunk, a chunk at its
document, a document at its source. L0–L2 are deterministic and can be rebuilt from the sources;
L3 is additive and is invalidated, never deleted, when what it claims changes.

Full column-by-column schema: `schema.md`.

## 3. Addresses

| Address | Resolves to |
|---|---|
| `memo://source/<id>` | the latest live document of a source |
| `memo://doc/<id>` | one document revision, with provenance |
| `memo://chunk/<n>` | one passage; `read` with `granularity: section` adds its neighbours |
| `memo://fact/<id>` | one fact with its evidence address and its two clocks |
| `memo://entity/<id>` | one entity: aliases, the passages that mention it, its neighbours with evidence (1.1) |
| `memo://page/<id>` | one curated page with the passages it was built from and its stale flag (1.2) |
| `memo://ns/<namespace>/index`, `memo://index` | one line per document and fact, under 8 KB |

Document and fact ids are UUIDv7 (time-ordered); chunk ids are integers. A forgotten record's
address still resolves, to "forgotten on … because …". The same addresses are MCP resources.

## 4. The write path

`ingest(content, namespace, source{uri, title, kind, library, version, origin, tags}, context?)`

1. Normalise line endings and whitespace; hash the content (SHA-256).
2. Same hash as the source's current revision: no-op, the result says `dedup: true`.
   Different: a new revision; the previous one is marked superseded and stays readable with
   `as_of`.
3. Split into chunks (`internal/chunk`): headings, then paragraphs, then sentences, then a hard
   split. Target 200 estimated tokens, cap 400 (clamped to the model's limit), about 40 tokens of
   overlap. A fenced code block is never split. Each chunk gets a context header
   `title > section path` that the keyword index also sees.
4. Keyword indexes are filled by triggers inside the transaction. Embeddings are computed
   outside the transaction in batches of 16, then stored; if the model is unavailable the chunks
   are queued as a job (`status.pending_embeddings`) and `backfill` or the next start embeds
   them. A write never reports success it did not get.
5. One audit row: who (`actor`), through which channel (`tool`, `cli`, `elicitation`,
   `worker`), did what, to which record.

Trust is set by channel, not by request: tool writes are `agent`; the CLI writes `user` by
default and `curated` only when typed. Origin (`web`, `user-said`, `agent-derived`) is a separate
label the writer declares.

## 5. The read path: how search decides

`search(query | queries[], mode, scope, granularity, response_format, max_tokens, exclude_ids,
as_of)`

**Arms.** Up to four ranked lists are produced over the same pre-filtered set of live passages:

| Arm | Index | Score | When it runs |
|---|---|---|---|
| keyword | FTS5, `porter unicode61`, over text and context header | BM25 | always, except `mode: semantic` |
| exact | FTS5, `unicode61 tokenchars '_.:-/'` (identifiers kept whole) | BM25 | `mode: exact`, or `auto` when the query looks like an identifier |
| semantic | `chunk_vecs` plain table, `vec_distance_cosine` on unit vectors | cosine similarity | when a model is loaded and the passage has a vector for it |
| fact | facts matched by keyword or by meaning | BM25 or cosine | always; a matched fact votes for its evidence passage; a keyword-only fact hit must cover half the query's content words |
| entity | `mentions` of the entities the query names | mention weight × selectivity | routed: the query names two or more known entities, or one with relational phrasing (1.1) |
| graph | personalised PageRank over the in-memory mention graph, one walk per named entity | product of per-seed mass | same routing; reports only passages that not every seed mentions directly (1.1) |

Scope filters (namespaces, kinds, sources, library, version, tags, dates, minimum trust) are
part of every arm's SQL, before top-k, so a filtered search never loses a result. The live
filter (`deleted_at IS NULL AND superseded_by IS NULL`) is replaced under `as_of` by
"recorded on or before T and not superseded before T"; forgotten records are excluded in both.

**Fusion.** Weighted *reciprocal rank fusion*: a passage's fused score is the sum over arms of
`weight / (k + rank)`, with `k = 60` and default weights semantic 0.5, keyword 0.5, exact 0.3,
fact 0.4, entity 0.4, graph 0.5. Equal semantic and keyword weights mean a keyword-only hit at rank 1 ties a vector hit
at rank 1 and can reach the first page. Worked example, one passage at keyword rank 1 and
semantic rank 2:

```
keyword   0.5 / (60 + 1) = 0.008197
semantic  0.5 / (60 + 2) = 0.008065
fused                    = 0.016262
```

The `minmax` profile replaces this with score fusion: each arm's raw scores are scaled to 0..1
and summed with the same weights. Chunk scores are aggregated to documents by their best chunk,
which becomes the cited passage.

**Recency.** For kinds that age (`note`, `conversation`), the fused score is multiplied by
`0.8 + 0.2 · 0.5^(age_days / 90)`: an item keeps at least 80% of its score however old it is,
and the bonus halves every 90 days. Versioned documents and code do not age. Age is clamped at
zero, so a future date gives no boost. The factor is reported separately in `why`.

**Cutoff.** Results are sorted by final score. The list ends at the first result, past the third,
whose score drops by more than half from the one before (`cutoff_gap 0.5`, `min_results 3`),
then at `limit` (default 10, maximum 100), then at the token budget.

**Abstention.** A passage whose only evidence is a semantic similarity below the model's weak
band is dropped. If nothing survives, the response has zero results, a `reason` and a `hint`,
and `degraded` says whether a capability was missing.

**Relevance bands.** `relevance` is the best raw cosine for the result, interpreted against the
model's own bands (strong, moderate, weak). The shipped defaults for the generic profile are
0.60 / 0.45 / 0.30; the registry overrides them per model (granite-small-r2: 0.88 / 0.80 / 0.72,
because unrelated text already scores about 0.63 under that model). `score` is the ordering key
and is not comparable across queries; `relevance` is.

**Budget.** Results are packed into `max_tokens`. Every format reports `truncated` (how many
ranked results did not fit) and `narrow_hint` (which scope field would shorten the list).

**Granularities.** `chunk` returns passages; `document` (default) one result per document with
its best passage; `fact` returns facts, ordered by score with trust as the tiebreak when two
conflicting facts score within 10% of each other; `page` (1.2) returns curated pages by keyword
and meaning, each marked `is_inference` and `stale` when a source changed since it was built.

**Reranking.** Opt-in (`MEMO_RERANK=1`, profile `precise`): a cross-encoder re-scores the top 30.
It did not pass its promotion gate in the bake-off (`docs/eval/v0.7.0.md`, spike S8) and is
off by default.

## 6. Profiles

A profile is the set of constants above with a one-paragraph derivation each. Shipped:
`default`, `precise` (deeper fetch, rerank when attached), `recency`, `code` (exact arm 0.6,
no recency), `keyword-only`, `semantic-only`, `text-only` (the 1.0 arms), `no-graph` (entity
arm without the walk), `minmax`. `GraphAuto` (default on) is the routing switch for the
structural arms. Overrides live in
`$MEMO_HOME/profiles.json`; `MEMO_PROFILE` selects one for the server. The profile is not an
MCP parameter in 1.0.

## 7. The explain contract

CLI and MCP render the same two structures; a test asserts the numbers are identical.

**`why`, one per result** (`response_format: explain`):

| Field | Meaning |
|---|---|
| `uri`, `chunk{uri, ord, section_path, est_tokens}`, `document{uri, title, revision}` | what is being explained |
| `arms[]{arm, rank, raw, raw_kind, contribution, matched_terms}` | each arm's view: its rank, its own score (cosine or bm25), what it contributed to the fused score, the terms it matched |
| `fused`, `recency_factor`, `final`, `rank` | the arithmetic, in order |
| `relevance`, `band` | best raw cosine and its band; `keyword-only` when the semantic arm did not see it |
| `provenance{source_uri, version, library, kind, fetched_at, trust, origin, namespace}` | where it came from |
| `freshness{stale, ttl_expired}` | whether the source has a newer revision or an expired TTL |
| `time{valid_from, valid_to, recorded_at, invalidated_at, superseded_by, as_of_applied}` | the two clocks, for facts |
| `rerank{model, score, before_rank}` | only when a reranker ran |
| `graph{seeds[], ppr_score, hops, hub_penalised}` | only when the graph arm saw the passage: the entities the walks started from, the product score, whether a seed mentions it directly, whether a hub was capped (1.1) |

**`trace`, one per query:**

| Field | Meaning |
|---|---|
| `mode_requested`, `mode_resolved`, `arms_run`, `routing_reason` | what ran and why |
| `profile`, `model_id` | which constants and which vectors |
| `candidates_per_arm`, `latency_ms_per_arm` | how much each arm returned and cost |
| `filtered{by_scope, live_docs_in_scope, by_revocation, by_min_trust, by_semantic_floor}` | what the filters removed before ranking |
| `cutoff{kind: gap|budget|limit|none, position, gap}` | why the list ended where it did |
| `budget{max_tokens, used, truncated_count, narrow_hint}` | token packing |
| `degraded{flag, reason}`, `as_of`, `rerank{model, top_n, latency_ms}` | missing capabilities, the time view, the reranker |
| `entities[]` | the entity names the query matched (1.1) |

## 8. Facts, trust and time

- `remember(statement, namespace, about[], evidence_uri, valid_from, valid_to, supersedes)` adds
  a fact; `supersedes` invalidates the old one, which stays readable under `as_of`.
- Two clocks: *valid time* (when the statement is true in the world) and *recorded time* (when
  the knowledge base learned it). `as_of` filters on recorded time and, for facts with a
  validity window, on valid time.
- `forget(uri, reason, redact?)` tombstones a document or fact: it leaves every index, stays
  hidden under `as_of`, and its address explains itself. `redact` also clears the text.
- Trust transitions by channel: a tool call may create `agent` records and forget or supersede
  only `agent` records; raising trust needs a human, through the CLI (`memo-mcp trust promote`)
  or an elicitation dialog the client shows (`promote`). The table is in `schema.md` §7.

## 9. Embedding models

`internal/embedding/registry.go` lists the candidates with licence, dimension, token limit,
prefixes and bands. The default is `granite-small-r2` (IBM granite-embedding-small-english-r2,
384 dimensions, Apache-2.0), chosen by the rule in roadmap OD-6 from the bake-off in
`docs/eval/v0.7.0.md`. Vectors for several models coexist in `chunk_vecs(chunk_id, model_id,
embedding)`, so switching (`MEMO_MODEL`, `memo-mcp model use`) is a resumable `reindex`, not a
re-chunk. Models run in-process through hugot's pure-Go ONNX backend; `potion` is a static
model2vec table with no ONNX at all, the instant tier.

## 10. The human face

- CLI: `search --explain`, `explain`, `read`, `ls`, `facts ls --as-of`, `trust`, `log tail|replay`.
- `export --md <dir>` writes `<namespace>/<kind>/<slug>-<shortid>.md` with YAML front matter
  (`memo_uri`, `source_uri`, `title`, `kind`, `namespace`, `library`, `version`, `revision`,
  `content_hash`, `fetched_at`, `trust`, `origin`, `context`) and an `_index.md` per namespace.
  Re-importing the export produces zero new revisions.
- `export --index` prints the ≤ 8 KB index for `AGENTS.md`/`CLAUDE.md`; `SKILL.md` tells an
  agent how to use the tools.
- `memo-mcp ui` (1.3) serves the same store read-only on loopback: search with the explain
  table, documents, passages, facts timeline, entities, pages, status, lint, log, eval. It
  calls the same `retrieve.Service` with the same request shape the agent gets, and a test
  asserts the rendered numbers equal the service's. GET only; Host header checked against the
  bound address (DNS-rebinding defence); non-loopback binding needs `--allow-remote`.

## 11. Measurement

`internal/eval` holds two labelled corpora (79 notes, 29 queries; 316 knowledge-base documents
plus 6 facts, 27 queries) with categories (lookup, exact, long-document, paraphrase, abstention,
knowledge-update, temporal, conflict, revocation, poisoning, …). The CI gate runs them with a
deterministic hash embedder and compares query by query against `testdata/baseline.json`: a
single query may not drop a rank band and no category mean may fall by more than 0.02. Reports
per tag live in `docs/eval/`. `memo-mcp eval` runs the same harness with real models.

Current baselines (hash embedder, `default` profile, `docs/eval/v0.9.0.md`):

| corpus | recall@5 | MRR | nDCG@10 | abstention |
|---|---|---|---|---|
| notes | 0.957 | 1.000 | 0.975 | 1.00 |
| knowledge base | 1.000 | 1.000 | 1.000 | 1.00 |

## 11a. The graph index (1.1)

`explore(entity, hops, as_of)` walks from one entity: its passages, then the entities that
share passages with it (fixed-depth joins, one or two hops), each with up to three evidence
addresses and a typed relation when the client supplied one. Extraction is a ladder: heuristics
(backticked spans, dotted/camel/snake identifiers, headings, capitalised names) on every ingest;
client-declared `entities[]` and `relations[]`; later rungs only if measured. Resolution is
deterministic: same key → same entity; near key (3-gram Jaccard ≥ 0.8, not short, not differing
only in a number) → a merge candidate for a human; otherwise a new entity. Spike S6 and
`docs/eval/v1.1.0.md` hold the numbers.

## 11b. Compaction and pages (1.2)

The server never writes a page. `compact(namespace, kinds, lint)` scans and records work items
the calling agent can do: an entity with three or more live passages and no page (or whose
page is stale, or which gained two or more passages since the page was built: the recurrence
trigger), two live facts about one subject whose validity windows overlap and whose statements
differ (with the rule the server would apply: trust first, then recency), an open merge
candidate, two near-duplicate passages from different documents (word 3-gram Jaccard ≥ 0.75).
Each payload carries the passages, facts and previous page, so the work needs no second call.
`submit(item_id, …, dry_run)` stores a page with `is_inference = 1` and its sources, invalidates
the losing fact of a conflict under the trust rule, or records a merge decision; the report
carries a line diff against the previous page, the omission check (recorded facts about the
subject whose content words are mostly absent from the page) and the corruption check (page
sentences that share fewer than half their content words with any source). `lint` reports
contradictions, orphan entities, missing pages, stale pages and expired facts with addresses.
Raw rows are never touched; `docs/eval/v1.2.0.md` holds the checksum proof. An optional local
model (`memo-mcp compact --executor ollama`) can write page items from the terminal; no other
code path knows it exists.

## 11c. Observability (1.4)

Local only, by construction: metrics are pulled from a loopback address, logs go to stderr,
the call log is a table in the knowledge-base file. None of it is telemetry.

**Metrics** come from a stdlib-only registry (`internal/obs`: counters, gauges, histograms with
labels, Prometheus text format 0.0.4, a curated `runtime/metrics` subset). Names follow
Prometheus conventions (`memo_` prefix, `_total` for counters, `_seconds`/`_bytes` units) and
map onto OpenTelemetry names by replacing `_` with `.`. Labels are bounded enums only.

| Family | Labels | Meaning |
|---|---|---|
| `memo_mcp_requests_total`, `memo_mcp_requests_in_flight` | method, outcome | every MCP request |
| `memo_mcp_tool_calls_total`, `memo_mcp_tool_call_duration_seconds`, `memo_mcp_tool_result_tokens` | tool, outcome (ok, tool_error, input_required, error) | tool calls and their size |
| `memo_mcp_tool_errors_total` | tool, class (not_found, forgotten, needs_human, canceled, invalid_args, internal) | why tool calls failed |
| `memo_mcp_elicitations_total` | tool, outcome (asked, accept, decline, cancel, unsupported) | human questions |
| `memo_mcp_resource_reads_total`, `memo_mcp_sessions_total` | kind, outcome; client | resources and sessions |
| `memo_search_total`, `memo_search_duration_seconds`, `memo_search_results` | mode_requested, mode_resolved, granularity, outcome | searches |
| `memo_search_arm_duration_seconds`, `memo_search_arm_candidates` | arm | each retrieval arm |
| `memo_search_cutoff_total`, `memo_search_truncated_results_total`, `memo_search_abstentions_total`, `memo_search_degraded_total` | kind; reason | how lists ended and why nothing came back |
| `memo_search_entities_matched`, `memo_search_rerank_duration_seconds`, `memo_graph_cache_total`, `memo_graph_build_duration_seconds` | model; event | structure and reranking |
| `memo_store_writes_total` | op, channel | every audited write |
| `memo_store_ingests_total`, `memo_store_ingest_duration_seconds`, `memo_store_chunks_written_total`, `memo_store_document_bytes`, `memo_store_vectors_stored_total`, `memo_store_embed_batches_total`, `memo_store_jobs_total`, `memo_store_mentions_linked_total`, `memo_store_pages_marked_stale_total`, `memo_store_work_items_total` | outcome; model; kind, event | the write path |
| `memo_embed_duration_seconds`, `memo_embed_texts_total`, `memo_embed_errors_total`, `memo_embed_batch_size`, `memo_embed_model_downloads_total`, `memo_embed_model_load_seconds`, `memo_embed_model_loaded` | model, role (query, doc); outcome; backend | the embedder |
| `memo_kb_*` gauges (`documents_live`, `chunks`, `facts`, `entities`, `pages`, `pages_stale`, `work_items_open`, `jobs_queued`, `pending_embeddings{model}`, `db_size_bytes`, `namespace_documents{namespace}`, …) | — | table counts, read at scrape time and cached for 5 s |
| `memo_ui_requests_total`, `memo_ui_request_duration_seconds` | route, status | the web UI |
| `go_*`, `process_start_time_seconds`, `memo_build_info{version, go_version, mcp_protocol, goos, goarch}` | — | runtime and build |

Metrics are per process. The serving process is the scrape target
(`memo-mcp serve --metrics-addr 127.0.0.1:PORT`, loopback only, GET `/metrics` only, Host
header checked); the UI serves its own `/metrics`; `memo-mcp metrics` prints a file-backed
snapshot (the gauges plus call-log statistics) and says so.

**Logs** are `log/slog` on stderr (`MEMO_LOG_FORMAT`, `MEMO_LOG_LEVEL`): one Info line per
tool call and per search, Warn for degraded modes, downloads and licence notes. The MCP
`logging` capability is not advertised (deprecated in 2026-07-28; decision D-O).

**Call log** (migration 4, `call_log`): with `MEMO_QUERY_LOG=1`, one row per tool call with
client, tool, latency, outcome, error class, result count, tokens out and an allowlisted
argument summary (never `content`, `statement`, `reason` or `context`). `memo-mcp log calls`,
`memo-mcp log tail`, UI `/log`; pruned with the search log at 10k rows or 30 days.

## 12. Not in 1.0

HTTP transport for MCP (OD-11), a server-side LLM, prompts. The graph arm and `explore`
arrived in 1.1, compaction and pages (`compact`, `submit`, `granularity=page`) in 1.2, the
read-only web UI in 1.3, all as additive changes; the tool count is ten. The UI's HTTP server
is not an MCP transport: it has no tools and no mutating route. The MCP `profile` parameter. Importing v0 journal files.
