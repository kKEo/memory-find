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
L4  graph        entities, aliases, which passage mentions which entity, optional typed edges (1.1)
L3  facts        one-sentence claims with a validity window and an evidence passage
L2  chunks       passages of about 200 estimated tokens, indexed three ways
L1  documents    the text as ingested, one row per revision
L0  sources      where it came from: URL or file, library, version, hash, trust, origin
     bookkeeping namespaces, models, jobs, audit, query_log
```

L4 arrived in 1.1 as migration 2; L5 (pages) is reserved for 1.2 and arrives as an additive
migration. Every layer points down: a fact points at its evidence chunk, a chunk at its
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
conflicting facts score within 10% of each other.

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

## 12. Not in 1.0

HTTP transport (OD-11), a server-side LLM, prompts, compaction and pages (1.2), the web UI
(1.3). The graph arm and `explore` arrived in 1.1 as additive changes. The MCP `profile` parameter. Importing v0 journal files.
