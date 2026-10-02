# S8 — Does the cross-encoder reranker help? (OD-7 gate)

**Question.** Roadmap OD-7 lets a cross-encoder reranker into the `precise` profile only if it
raises nDCG@10 by at least 0.02 on eval v2 at acceptable latency. Does
`cross-encoder/ms-marco-MiniLM-L6-v2`, run through hugot's text-classification pipeline on the
pure-Go backend, clear that bar?

**Run.** 2026-10-02. `memo-mcp eval --models hash,minilm,potion,granite-small-r2 --profiles
default,precise --rerank` (the `precise` profile re-scores the top 30 fused results), plus a
four-passage probe (`spikes/s8-rerank`) to check the scores are meaningful at all.

**Numbers.**

| Embedder | corpus | default nDCG@10 | precise + rerank nDCG@10 | p50 ms default → precise |
|---|---|---|---|---|
| hash | notes | 0.968 | 0.780 | 12 → 798 |
| minilm | notes | 0.977 | 0.732 | 128 → 806 |
| potion | notes | 0.966 | 0.748 | 6 → 800 |
| granite-small-r2 | notes | 0.978 | 0.676 | 206 → 3,987 |
| minilm | kb | 0.882 | 0.724 | 110 → 2,333 |
| potion | kb | 0.882 | 0.686 | 6 → 1,089 |

The probe shows the model does discriminate (sourdough query: 0.0048 for the sourdough passage
against 0.0001 for the others; a ramen query flips the order), but every score is tiny:
sigmoid outputs of 0.001–0.005 mean strongly negative logits for *all* pairs, including the
right one. That is the signature of a degraded input encoding: the pipeline receives
`"query [SEP] passage"` as one string, so the tokenizer does not emit the segment ids
(`token_type_ids` = 1 for the passage) a cross-encoder was trained with. Weak, compressed scores
reorder the top 30 almost at random, which is what the tables show.

**Decision.** **Rerank fails the gate; it is not promoted.** The `precise` profile keeps
`rerank=true` so the machinery stays testable, but the reranker is only ever attached with
`MEMO_RERANK=1` or `--rerank`, so no default path uses it. Cost alone would have been
disqualifying on this backend: 700–3,800 ms per query. Revisit when (a) hugot exposes sentence-pair
encoding or the tokenizer step can be done in memo-mcp, and (b) a faster backend arrives. Until
then the best "precise" setting measured is the `minmax` fusion profile, not a reranker.
