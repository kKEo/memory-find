# Part IV: Retrieval tuning

The defaults are measured, not guessed. Each constant has a written derivation, and every
release carries an eval report. Tune only when you have a reason, and **measure before and
after**.

- [Profiles and constants](profiles.md): the shipped profiles and what each constant does.
- [Embedding models](models.md): the model registry, switching models, and what it costs.
- [The reranker](reranker.md): an opt-in cross-encoder, and why it is off.
- [Measuring a change](measuring.md): the eval harness, the query log, and reading `explain`.

| Symptom | First lever |
|---|---|
| Identifier or code queries miss | `code` profile, or raise `weights.exact` |
| Paraphrased questions miss | Check `degraded` and the model; try a stronger model |
| Old notes crowd out new ones | `recency` profile, or a shorter `half_life_days` |
| Lists are too long or too short | `cutoff_gap`, `min_results`, or the request's `limit` |
| "No results" when there should be some | `semantic_floor`, and the model's bands; look at `filtered` in the trace |
