# profiles.json

`$MEMO_HOME/profiles.json` changes ranking constants without rebuilding the binary. It is read
at start-up by **`memo-mcp serve` and `memo-mcp ui`**. A missing file is fine. A malformed
file stops the process with the path and the JSON error.

> **Note.** In v1.4, the CLI `search`, `explain` and `eval` commands do not read
> `profiles.json`. They only see the built-in profiles. Test an override through the UI, which
> uses the same pipeline as the server, or by running the server.

## Shape

The file is a JSON object mapping a profile name to the fields to change. Only fields that are
present are applied.

- An **existing** name, such as `default`, is modified in place.
- A **new** name creates a profile. It starts from `base`, or from `default` when `base` is
  absent.

```json
{
  "default": {
    "weights": { "exact": 0.4 }
  },
  "team-docs": {
    "base": "precise",
    "weights": { "semantic": 0.6, "keyword": 0.4 },
    "cutoff_gap": 0.4,
    "recency_kinds": [],
    "derivation": "Docs-heavy KB: favour meaning over wording; no recency."
  }
}
```

Select it with `MEMO_PROFILE=team-docs` in the server's environment.

## Fields

| Field | Type | Meaning | Shipped default |
|---|---|---|---|
| `base` | string | Profile to copy for a new name | `default` |
| `weights` | map of arm to number | Arm weights; arms: `semantic`, `keyword`, `exact`, `fact`, `entity`, `graph`. Merged into the base's weights; `0` disables an arm | 0.5 / 0.5 / 0.3 / 0.4 / 0.4 / 0.5 |
| `rrf_k` | integer | RRF damping constant | 60 |
| `fusion` | `"rrf"` or `"minmax"` | Rank fusion, or min-max score fusion | `rrf` |
| `fetch_depth` | integer | Candidates per arm before fusion | 100 |
| `half_life_days` | number | Recency half-life | 90 |
| `recency_floor` | number 0–1 | Fraction of score the oldest item keeps | 0.8 |
| `recency_kinds` | list of kinds | Kinds that age; `[]` turns recency off | `["note","conversation"]` |
| `cutoff_gap` | number 0–1 | Stop before a drop larger than this fraction | 0.5 |
| `min_results` | integer | Never gap-cut below this many results | 3 |
| `semantic_floor` | number | Drop semantic-only candidates below this cosine | 0.30 |
| `bands` | `[strong, moderate, weak]` | Relevance bands for the generic profile; exactly three numbers | `[0.60, 0.45, 0.30]` |
| `derivation` | string | Your reason, shown by `profiles show` | — |

The reranker switch, its depth and graph routing are not overridable. Use the `precise` profile
as a `base` to get reranking. A model's own relevance bands, set in the model registry, take
precedence over `bands`.

## Checking an override

```bash
memo-mcp profiles show team-docs   # built-in profiles only in v1.4; see the note above
memo-mcp ui                        # search there with MEMO_PROFILE=team-docs set; the trace shows the profile
```

Before adopting an override, measure it. See [Measuring a change](../tuning/measuring.md).
