# S3 — What can SQLite's FTS5 do for us?

**Question.** Four things the P1 schema assumes: is the `trigram` tokenizer compiled into the
modernc.org/sqlite build; does an external-content FTS table stay in step with its base table
through insert/delete/update triggers; what do `porter unicode61` and `unicode61 tokenchars` do to
identifiers; do `highlight()`, `bm25()` and `integrity-check` work on external-content tables.

**Run.** 2026-10-02, `make spike NAME=s3-fts5` (`spikes/s3-fts5/main.go`).

**Numbers.**

| Check | Result |
|---|---|
| `tokenize='trigram'` | available; `Callback` matches inside `useCallback` |
| Stemmed index (`porter unicode61 remove_diacritics 2`) | `review` and `reviewing` both match both chunks (stemming works); `net/http` and `ERR_CONN_RESET` match because punctuation splits them into `net`, `http`, `err`, `conn`, `reset` (so the stemmed index also matches a query for just `http` or `conn`) |
| Exact index (`unicode61 tokenchars '_.:-/'`) | `net/http`, `useCallback`, `ERR_CONN_RESET` match as whole tokens; `http`, `conn` and `review` do **not** (identifiers stay whole; no stemming) |
| Triggers on external-content tables | after `UPDATE` the old term is gone and the new one is found; after `DELETE` nothing remains |
| `highlight()` | works: `Second chunk about [authentication] reviews` |
| `bm25()` | works (score is ~0 with a two-row corpus, as expected for a term in one of two rows) |
| `INSERT INTO t(t) VALUES ('integrity-check')` | works on an external-content table |

**Decision.** P1 uses exactly this pair: `chunks_fts` with `porter unicode61 remove_diacritics 2`
for words, and `chunks_fts_exact` with `unicode61 tokenchars '_.:-/'` for identifiers (OD-2 option
A). Trigram is available if the P3 exact and version-pinned eval slices show substring queries
matter; it costs about 3× the index size. Matched terms for the explain contract come from
`highlight()`.
