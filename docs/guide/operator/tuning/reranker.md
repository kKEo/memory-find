# The reranker

A cross-encoder can re-score the top results by reading the query and each passage together.
memo-mcp ships one as an **opt-in experiment**:

| Setting | Value |
|---|---|
| Model | `cross-encoder/ms-marco-MiniLM-L6-v2` (Apache-2.0, ~91 MB), id `ms-marco-minilm` |
| Enable | `MEMO_RERANK=1` (server and UI), or `search --rerank` (CLI) |
| Choose another | `MEMO_RERANKER=<id>` |
| Used by | Profiles with rerank on: `precise`, which re-scores the top 30 |

## Why it is off by default

It failed its promotion gate. In the bake-off it **lowered** nDCG@10 by about 0.2 and added
0.7 to 4 seconds per query:

| Embedder | Corpus | default nDCG@10 | precise + rerank nDCG@10 | p50 ms |
|---|---|---|---|---|
| minilm | notes | 0.977 | 0.732 | 806 |
| minilm | kb | 0.882 | 0.724 | 2,333 |
| granite-small-r2 | notes | 0.978 | 0.676 | 3,987 |

The pure-Go pipeline cannot pass sentence-pair segment ids, so the model's scores are
compressed near zero. Details:
[spike S8](https://github.com/kKEo/memory-find/blob/master/docs/spikes/S8-reranker.md).

## If you try it

```bash
MEMO_RERANK=1 MEMO_PROFILE=precise memo-mcp ui
```

Watch `memo_search_rerank_duration_seconds` and the trace's `rerank{model, top_n, latency_ms}`.
If the reranker fails to load, the server continues without it. Search then reports
`degraded` with reason `reranker_failed`.
