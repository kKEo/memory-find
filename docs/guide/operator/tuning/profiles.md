# Profiles and constants

A **profile** is a named set of ranking constants. The server and the UI use `MEMO_PROFILE`,
default `default`. The CLI takes `search --profile <name>`. MCP clients cannot pick a profile per
request in 1.x.

```bash
memo-mcp profiles show            # every profile, every constant, with its derivation
memo-mcp profiles show precise
```

## Shipped profiles

| Profile | Weights: sem / kw / exact / fact / entity / graph | Other differences | Use for |
|---|---|---|---|
| `default` | 0.5 / 0.5 / 0.3 / 0.4 / 0.4 / 0.5 | — | General use |
| `precise` | same as default | `fetch_depth` 200, `cutoff_gap` 0.6, rerank on when a reranker is attached | Rarely worded answers; shorter, surer lists |
| `recency` | same as default | Every kind ages; half-life 30 days; floor 0.6 | "What did I write lately" |
| `code` | 0.4 / 0.4 / 0.6 / 0.4 / 0.5 / 0.5 | No recency | Identifier-heavy questions |
| `minmax` | same as default | Min-max score fusion instead of RRF | Experiment |
| `text-only` | same as default | No entity or graph arm (the 1.0 arms) | Ablation |
| `no-graph` | same as default | Entity arm without the graph walk | Ablation |
| `keyword-only` | keyword 1.0 only | — | Ablation; the baseline to beat |
| `semantic-only` | semantic 1.0 only | — | Ablation |

## The constants

| Constant | Default | Raise it to… | Lower it to… |
|---|---|---|---|
| `weights.<arm>` | see above | Let that arm's ranking count for more | Mute an arm (`0` disables it) |
| `rrf_k` | 60 | Flatten the difference between rank 1 and rank 10 | Reward top ranks more |
| `fetch_depth` | 100 | Keep rarely worded hits in the fused list, at some latency cost | Speed up large KBs |
| `half_life_days` | 90 | Age more slowly | Favour recent items more strongly |
| `recency_floor` | 0.8 | Make age matter less | Make age matter more (floor is the minimum kept) |
| `recency_kinds` | note, conversation | Age more kinds | `[]` turns recency off |
| `cutoff_gap` | 0.5 | Cut later, giving longer lists | Cut sooner at a score cliff |
| `min_results` | 3 | Always show more before a gap cut | Allow single-answer lists |
| `semantic_floor` | 0.30 | Abstain more readily on vague matches | Keep weaker meaning-only matches |
| `bands` | 0.60 / 0.45 / 0.30 | Generic relevance bands; per-model bands override them | — |

## Worked example: fusion

A passage at keyword rank 1 and semantic rank 2 under `default`:

```
keyword   0.5 / (60 + 1) = 0.008197
semantic  0.5 / (60 + 2) = 0.008065
fused                    = 0.016262
```

A keyword-only hit at rank 1 scores 0.008197, the same as a semantic-only hit at rank 1. Equal
weights therefore let either arm put a result on the first page. That is the main reason the
default is balanced.

## Graph routing

The `entity` and `graph` arms run only when the query names two or more known entities, or one
entity with relational phrasing ("depends on", "related to", …). The trace's `routing_reason`
says why they ran. Ablations show that routing them on every query hurts single-entity
lookups. That is why routing is not a tunable constant.

## Customising

Overrides go in [`profiles.json`](../config/profiles-json.md). The derivations behind each
default are in
[architecture §5–§6](https://github.com/kKEo/memory-find/blob/master/docs/architecture.md#5-the-read-path-how-search-decides)
and the [eval reports](https://github.com/kKEo/memory-find/tree/master/docs/eval).
