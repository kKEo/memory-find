# The search pipeline

Every `search`, from the MCP tool, the CLI or the UI, runs the same pipeline in
`internal/retrieve`:

![The scope filter runs inside every arm. Up to six arms rank candidates in parallel, up to 100 each: keyword, exact, semantic, fact, and the routed entity and graph arms, each with its weight. Reciprocal rank fusion, recency, abstention, then cutoff, limit and token budget produce the results, with a trace and, on explain, a why per result.](../images/search-pipeline.svg)

## 1. Scope filter

Namespaces, kinds, sources, library, version, tags, dates and minimum trust are applied
**inside each arm's SQL, before top-k**. A filtered search therefore never loses a result to a
candidate that was filtered out later. The live filter excludes superseded and forgotten
records. Under `as_of`, it is replaced by "recorded on or before T and not superseded before T".

## 2. Arms

| Arm | Index | Runs when |
|---|---|---|
| `keyword` | FTS5, Porter stemmer, BM25, over text and its section header | Always, except `mode: semantic` |
| `exact` | FTS5 that keeps identifiers whole (`net/http`, `ERR_CONN_RESET`) | `mode: exact`, or `auto` when the query looks like an identifier |
| `semantic` | Cosine similarity over unit vectors in a plain SQLite table | A model is loaded and passages have vectors for it |
| `fact` | Facts matched by keyword or meaning; a hit votes for its evidence passage | Always |
| `entity` | Passages mentioning entities the query names | Routed: the query names 2+ known entities, or 1 with relational phrasing |
| `graph` | Personalised PageRank over the mention graph, one walk per named entity | Same routing; reports only passages not every seed mentions directly |

Each arm returns up to `fetch_depth` candidates (100 by default).

## 3. Fusion

Weighted reciprocal rank fusion: a passage's score is the sum over arms of
`weight / (k + rank)`, with `k = 60`. Default weights are semantic 0.5, keyword 0.5, exact 0.3,
fact 0.4, entity 0.4 and graph 0.5. The `minmax` profile uses score fusion instead.

## 4. Recency

For kinds that age (`note`, `conversation` by default), the score is multiplied by
`floor + (1 − floor) · 0.5^(age_days / half_life)`, with floor 0.8 and half-life 90 days.
Versioned documents and code do not age.

## 5. Abstention and relevance bands

A candidate whose only evidence is a semantic similarity below `semantic_floor` is dropped. If
nothing survives, the response has zero results, a `reason` and a `hint`. That is a deliberate
"not in the knowledge base", not an error.

`relevance` is the best raw cosine for a result, interpreted against the **model's** bands. For
the default model, `granite-small-r2`, strong is 0.88, moderate 0.80 and weak 0.72, because
unrelated text already scores about 0.63 under it. `score` orders one list and is not
comparable across queries; `relevance` is.

## 6. Cutoff, limit and budget

The list ends at the first result, past `min_results` (3), whose score drops by more than
`cutoff_gap` (half) from the previous one. It is then capped at `limit` (default 10, maximum 100),
and then packed into `max_tokens`. The response always reports `truncated`, the count of ranked
results that did not fit, and `narrow_hint`, the scope field that would shorten the list.

## 7. Explain and trace

With `response_format: explain` (MCP) or `--explain` (CLI), every result carries `why`: each
arm's rank, raw score and contribution, the fused and final score, relevance band, provenance
and freshness. Every response carries a `trace`: arms run and why, candidates and latency per
arm, filter counts, cutoff kind, budget use and degraded state. The UI renders the same
structures, and a test asserts that the numbers are identical.

## Degraded mode

If the embedding model is not available, because it is downloading, failed to load or has no
vectors yet, search runs without the semantic arm. It sets `degraded{flag, reason}`, and the
`memors_search_degraded_total` metric counts it. Writes made in that state queue their vectors.
`memors-mcp backfill` or the next server start embeds them.

Tuning these constants: [Profiles and constants](../tuning/profiles.md). Full formulas:
[architecture §5](https://github.com/kKEo/memors/blob/master/docs/architecture.md#5-the-read-path-how-search-decides).
