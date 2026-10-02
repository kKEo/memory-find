# S2 — vec0 virtual table or a plain table?

**Question.** For `chunk_vecs`, is sqlite-vec's `vec0` virtual table worth its sharp edges (no
foreign keys, one fixed dimension per table, filter pushdown rules we already tripped over), or is
a plain table scanned with `vec_distance_cosine()` fast enough? Rule set before running: plain
table unless vec0 is more than 3× faster at 50k chunks *and* `rowid IN (subquery)` pushes down.

**Run.** 2026-10-02, `make spike NAME=s2-vectors` (`spikes/s2-vectors/main.go`), random unit
vectors, top-30 query, median of 7 runs, Apple Silicon laptop, modernc.org/sqlite v1.50.0 with
sqlite-vec v0.1.9.

**Numbers.**

| chunks | dim | vec0 KNN p50 | plain scan p50 | ratio |
|---|---|---|---|---|
| 10,000 | 384 | 10.9 ms | 19.5 ms | 1.79× |
| 10,000 | 768 | 22.0 ms | 33.0 ms | 1.50× |
| 50,000 | 384 | 58.8 ms | 98.0 ms | 1.67× |
| 50,000 | 768 | 104.8 ms | 165.4 ms | 1.58× |

Semantics: `vec_distance_cosine` returns `1 − cos` (identical vectors → 0, orthogonal → 1);
`rowid IN (subquery)` **does** push down into a vec0 KNN query in this build (returned exactly the
allowed ids), as does a literal list; `CREATE VIRTUAL TABLE` inside a transaction rolls back
cleanly.

**Decision.** **Plain table** (`chunk_vecs(chunk_id, model_id, embedding BLOB)` with
`vec_distance_cosine`). vec0 is 1.5–1.8× faster, well under the 3× bar, and the plain table gives
real foreign keys, arbitrary SQL filters before top-k, and any dimension per model row. At 50k
chunks the plain scan is about 100 ms for 384 dims; above roughly 100k chunks revisit with a vec0
adapter behind the `Arm` interface (OD-1). Both the pushdown result and the rollback result mean a
vec0 adapter would be straightforward if ever needed.
