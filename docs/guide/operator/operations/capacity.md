# Capacity and performance

Numbers measured on an Apple-silicon laptop with the shipped defaults. They are a guide to
proportions, not a benchmark of your hardware.

## Disk

| Item | Size |
|---|---|
| Fixed overhead of an empty knowledge base | ~0.3 MB |
| Per passage, before vectors (text, two FTS5 indexes, graph rows) | ~4.4 KB |
| Per passage per 384-dimension model (`granite-small-r2`, `minilm`) | ~1.5 KB |
| Per passage per 768-dimension model | ~3 KB |
| Typical passages per KB of markdown | ~1 per 0.9 KB |
| Default model in the cache | ~195 MB |

Example: this project's `docs/` and `articles/` folders (756 KB of markdown) became 101
documents and 818 passages, in a 3.6 MB file before vectors. Vectors for one 384-dimension model
add about 1.2 MB more.

## Time

| Operation | Cost |
|---|---|
| Ingest without embedding | ~60 documents per second |
| Embedding a passage, `granite-small-r2` | ~0.6 s |
| Embedding a passage, `minilm` | ~0.25 s |
| Embedding, `potion` | near instant |
| Search p50, `granite-small-r2` (includes embedding the query) | ~0.2 s |
| Search p50, `potion` | ~6 ms |
| Search p50, keyword-only | ~1–3 ms |
| Reranker (`precise` + `MEMO_RERANK=1`) | +0.7–4 s per query |

Query embedding dominates search latency. Retrieval itself is milliseconds at these sizes.
`memo_search_arm_duration_seconds{arm}` shows the split on your data.

## Memory

A server process holds the embedding model in memory: a few hundred MB for `granite-small-r2`,
less for `potion`. It also holds one in-memory mention graph per namespace it has searched with
graph routing. `go_memstats_sys_bytes` shows the total.

## Scaling guidance

- Everything is single-file SQLite with exact (brute-force) vector search. Tens of thousands of
  passages per knowledge base are comfortable. Beyond about 100,000 passages, watch
  `memo_search_arm_duration_seconds{arm="semantic"}`, and split knowledge bases by project.
- Bulk loads: ingest with `--embed=false`, then `backfill`. Or choose `potion` for fast,
  slightly weaker semantics.
- Several sessions on one file are fine. Writes are serialised and short; embedding happens
  outside the write lock.
