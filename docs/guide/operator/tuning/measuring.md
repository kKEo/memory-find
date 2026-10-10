# Measuring a change

Every retrieval change in memors-mcp's own history was measured against a labelled baseline.
Use the same tools before you change a constant, a profile or a model.

## 1. The built-in benchmark

```bash
memors-mcp eval --models hash,granite-small-r2 --profiles default,precise,code --corpus all
memors-mcp eval --models potion --profiles all --format md > potion.md
memors-mcp eval --explain-failures            # one line per missed query, and why
```

It loads two labelled corpora into a throwaway knowledge base, runs every query under each
model and profile combination, and prints quality next to cost:

| Column | Meaning |
|---|---|
| recall@1/5/10 | Share of relevant items found in the top k |
| MRR | Mean reciprocal rank of the first relevant item |
| nDCG@10 | Ranking quality, rewarding relevant items near the top |
| abstention | Share of no-answer queries that correctly returned nothing |
| p50 ms, tokens p50 | Median latency and response size |

`hash` is the deterministic test embedder and needs no download. Other model ids download real
models. The corpora are the project's own fixtures, so they tell you about **relative** effects
(this profile against that one), not about your data.

## 2. Your own queries

1. Turn on the opt-in log in the server's environment: `MEMORS_QUERY_LOG=1`.
2. Use the system normally for a while.
3. Export what was asked:
   ```bash
   memors-mcp log replay --n 200 > candidates.jsonl
   ```
   Each line is an unlabelled eval query, with the query and the addresses that were returned.
4. Label the relevant addresses, then compare runs under different profiles in the UI or with
   `memors-mcp search --profile <p> --format json`.

## 3. Reading one result

```bash
memors-mcp explain "how do we rotate credentials"                  # ranking table for every hit
memors-mcp explain "how do we rotate credentials" memo://chunk/812  # the full why for one hit
```

What to look for:

| Field | Tells you |
|---|---|
| `arms[]` | Which arms saw the result, at what rank, and what each contributed |
| `recency_factor` | Whether age pushed it down |
| `relevance`, `band` | Whether the model thinks it is about the question at all |
| trace `filtered` | How many candidates scope, revocation, minimum trust or the semantic floor removed |
| trace `cutoff` | Whether the list ended on a gap, the limit or the budget |
| trace `degraded` | Whether a capability was missing |

The UI's search page shows the same table, with links.

## 4. In production

After changing a profile or a model, watch these in Prometheus:

- `memors_search_abstentions_total` and `memors_search_results`: is the system answering less?
- `memors_search_duration_seconds`: did latency move?
- `memors_search_cutoff_total{kind}`: are lists ending differently?

See [Prometheus: scrape, rules, alerts](../monitoring/prometheus.md).
