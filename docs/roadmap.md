# memo-mcp — Review & Roadmap

> **Status (2026-10-02):** this file was renamed from `review-roadmap.md` and
> is being rewritten as the forward-looking knowledge-base roadmap. Until that
> rewrite lands, the text below is the 2026-09-04 review: its Phases 0–1
> shipped the same day (commits `94f81ad`, `73fb9d4`), so the defects in
> "Context" are mostly fixed, and Phases 2–7 are superseded by
> [`knowledge-base-sota.md`](knowledge-base-sota.md) §7.

## Context

`memo-mcp` is a ~1,270-line pure-Go MCP server that gives Claude a private, searchable journal: 6 tools over stdio, local all-MiniLM-L6-v2 embeddings via hugot's pure-Go backend, one SQLite file per `JOURNAL_TOKEN` with `sqlite-vec` KNN + FTS5 BM25 fused by weighted RRF. It's a port of the TypeScript `obra/private-journal-mcp`. Four commits, last one 2026-06-10; dormant since.

The project has to teach (there's a 26-slide Slidev deck) and ideally also be useful. This review confronts it with the state of the field as of 2026-09-04 and lays out how to get there.

**Decisions taken** (from the review conversation): no fixed talk venue, so optimize for the artifact being genuinely good; front-load correctness and honesty; position as a **retrieval lab / eval playground**; distribute publicly including the official MCP registry.

### Headline finding: `search_journal` returns nothing, for every query, on real data

Reading the actual databases (read-only):

```
~/.memo-mcp/cas-cloud.db : 1 entry, 5615 chars, entry_embeddings_rowids = 0 rows
~/.memo-mcp/spson-cas.db : 1 entry, 6268 chars, entry_embeddings_rowids = 0 rows
PRAGMA user_version = 0, journal_mode = delete
```

**Every real entry has no embedding.** Three defects compound:

1. Entries are 5–6k chars (~1400–1600 word-pieces). `hftokenizer` — the WordPiece tokenizer on the `CGO_ENABLED=0` path — **ignores `MaxLen` entirely** (`MaxAllowedTokens` is enforced only in `tokenizer_rust.go`, which is excluded at `CGO_ENABLED=0`). So an oversized `input_ids` hits a BERT graph with a 512-row position table, `Embed` errors, and `journal.go:138` prints a stderr warning and **commits the entry with no embedding**. The tool reports success.
2. `fts5Escape` (`search.go:216-226`) quotes each term and joins with a space — in FTS5 the implicit operator is **AND**. Verified: `MATCH '"typescript" "frustration"'` → 0 rows; `OR` → 1 row. A natural-language query — the only kind the tool's own description invites — requires every term in one entry. The BM25 arm is dead for realistic queries.
3. Both arms empty → `Search` hits `if len(vecRanks) == 0 && len(bm25Ranks) == 0 { return nil, nil }` → *"No relevant entries found."*

And the test suite is green, because all three `mockEmbedder`s return a **constant vector** for every input, making the KNN arm degenerate. `TestSearchReturnsResults` asserts "3 results" and passes while real search returns zero.

That is the whole argument for the repositioning, and it's the best teaching story the project has: *you cannot ship retrieval you cannot measure.* Build the lab because without it, this is what you get.

### Where the project stands vs. the field

**Ahead of its own documentation.** The shipped design is hybrid vector+BM25 RRF with recency — exactly the 2026 consensus (hybrid beats vector-only; Weaviate measures ~17% Success@1 gain). But `slides.md`, `spec/go-implementation-plan.md` and `docs/graphrag-evolution-plan.md` all describe a pure-vector system. Slide 5 argues *against* keyword search that supplies 40% of the shipped score; slide 20, "Where Pure Vector Search Breaks Down", describes a system two commits out of date.

**Behind on protocol.** MCP **2026-07-28** landed: stateless core, `server/discover` replacing the initialize handshake, multi-round-trip requests, cacheable list results, a formal extensions framework; roots/sampling/logging deprecated; `ping`/`logging/setLevel`/`resources/subscribe` removed. Full Go support is in **go-sdk v1.7.0**. The project pins **v1.6.0**, whose `latestProtocolVersion` is `2025-11-25` (`2026-06-30` exists as a constant but isn't in `supportedProtocolVersions`). The server is also tools-only — no resources, prompts, annotations or structured output.

**Behind on the embedding model.** all-MiniLM-L6-v2 was SOTA in 2022. EmbeddingGemma-300M (768-dim, Matryoshka-truncatable to 128, official ONNX exports, best under 500M params) and Qwen3-Embedding-0.6B are the 2026 small-model defaults. 384 is hardcoded into the vec0 schema, so the model isn't swappable.

**Squeezed on positioning.** Claude Code has shipped Auto Memory on by default since v2.1.59 (Feb 2026), Anthropic ships a file-based memory tool, and Mem0/Zep/Cognee all expose MCP memory servers. "A private journal for Claude" no longer differentiates. What memo-mcp uniquely offers is that it's small enough to read end to end, has no cloud and no cgo, and stores everything in one greppable SQLite file — **a retrieval system you can open up, measure, and swap parts in.** You cannot measure Auto Memory. That's the pitch.

**The GraphRAG plan overclaims.** `docs/graphrag-evolution-plan.md` (8–10 weeks) analyses a "current state" that no longer exists, so its Phase 3 proposes inventing a hybrid score that already ships; its success metric baselines against vector-only; its `exp(-d/30)` "30-day half-life" is really ~20.8 days (the slides inherited the error). The 2026 consensus is layered/routed retrieval — graph pays off on ~15% of queries and taxes the other 85%. Graph belongs here as *one selectable strategy in the lab*, benchmarked against the others.

### Confirmed defects

All verified against source, dependency internals, or empirically.

| # | Defect | Where |
|---|---|---|
| **D1** | **Long entries are never embedded, silently.** `hftokenizer` ignores `MaxLen`; oversized input errors inside the ONNX graph; the entry commits without a vector. This is a *correctness* bug, not a quality issue — and it's why the live DBs have zero embeddings. | `embedding.go`, `journal.go:136-148` |
| **D2** | **`fts5Escape` is conjunctive.** Space-joined quoted terms ⇒ AND. Kills the BM25 arm for natural-language queries. A punctuation-only query reduces to `MATCH ''` (syntax error), swallowed by `if err == nil`. | `search.go:216-226, 90-106` |
| **D3** | **Typed-nil interface panic.** `NewHugotEmbedder` returns `*HugotEmbedder`; the nil pointer is laundered into the `Embedder` interface at `main.go:61-62`, so `if m.embedder != nil` is true and `Embed` runs on a nil receiver → `e.mu.Lock()` panics. The `if embedder != nil` at line 57 *is* correct — it tests the concrete type. The guard that looks protective is the one that works; the two that matter are implicit. | `main.go:53-62` |
| **D4** | **Recency decay is inert, and scores render non-monotonic.** Decay is applied after the sort and after `break`; `results` is never re-sorted. Worse, max-normalization takes the max over all of `results`, not `results[0]` — so the list can display `1. [0.982] … 3. [1.000]`. | `search.go:152-211` |
| **D5** | **Search hard-fails on embedder error** instead of falling back to BM25 — the opposite of the write path. | `search.go:60-64` |
| **D6** | **Filters applied after two rounds of top-k truncation.** Each arm has its own `LIMIT`, the union is re-truncated, *then* section/date filters run in Go. A section-filtered search can return zero results when many entries match. | `search.go:68, 92, 148-179` |
| **D7** | **Max-normalized scores destroy absolute relevance.** Top hit is always ~1.000, no floor. Note RRF is rank-based and dimensionless, so you *cannot* threshold on the fused score — you need the raw cosine, which the code scans into `distance` and discards. Since `WithNormalization()` gives unit vectors and vec0 defaults to L2, `cos = 1 − L2²/2` exactly: calibration is available today with zero schema change. | `search.go:76-82, 199-211` |
| **D8** | **Poisoned model cache.** Readiness = directory existence; `DownloadModel` copies files one at a time, so interruption leaves a partial dir that's never repaired. No force-redownload. | `embedding.go:79-83` |
| **D9** | **`jsonschema:"required,…"` leaks into the description.** `jsonschema-go@v0.4.3 infer.go:336` sets `fs.Description = tag` verbatim; requiredness derives from the absence of `omitempty`. The model literally reads `"required,Natural language search query"`. | `server.go:52,58` |
| **D10** | **Unsanitized `JOURNAL_TOKEN` in a path** (`token+".db"` — `../../tmp/x` escapes the base dir), and a "PRIVATE" journal created `0755`/`0644`, world-readable. | `journal.go:64-90` |
| **D11** | **Embedding runs inside the write transaction** — 100-500 ms of held write lock, with `MaxOpenConns(1)`, no WAL and no `busy_timeout`. ~1000× wider a SQLITE_BUSY window than necessary. | `journal.go:112-152` |
| **D12** | **No update/delete/forget path.** Append-only; no way to correct a memory. "Knowledge update" and "contradiction resolution" are standard LongMemEval/BEAM categories. | schema-wide |
| **D13** | **No stemming.** `tokenize` unset ⇒ `unicode61`. Verified: `porter` matches `review`→"reviewing" (1 row), default doesn't (0 rows). Standalone FTS also duplicates all content with no sync path. | `journal.go:46` |
| **D14** | **No embedding backfill.** Entries written while the embedder was down are invisible forever. `journal_stats` reports the gap but offers no remedy. | — |
| **D15** | **`.gitignore` is `memo-mcp` with no leading slash** → git silently ignores any *new* file under `cmd/memo-mcp/`. | `.gitignore:1` |
| **D16** | **Zero tests for `internal/server` and `cmd/`**, and `embedding_test.go` only tests its own mock. The three `mockEmbedder`s return constant vectors, so the KNN arm is degenerate and the suite is blind to D1/D2/D4/D6. | — |
| **D17** | Hygiene: no WAL/`busy_timeout`; unchecked `Scan` in `InitDB` (an error there can duplicate the whole FTS index); byte-based `truncateAtBoundary` splits UTF-8 runes; `gofmt -l` flags `journal.go` and `server.go`; dead `downloadFileWithProgress` makes `progressbar` a dep used only by dead code (and the promised progress bar doesn't exist); `SearchOptions.DateRange` implemented but unreachable from any tool; no cap on `limit`; hand-rolled UUIDv7 while `google/uuid` is already in the graph; `err == sql.ErrNoRows` instead of `errors.Is`. | various |

**Two things that are *not* defects.** Returning `(nil, nil, err)` from handlers is correct — `ToolHandlerFor` packs errors into `CallToolResult.Content` with `IsError` set. And `missingPenalty = 1000` already behaves as ~zero (`0.6/1060 ≈ 5.7e-4`); simplify it to "absent ⇒ 0" for readability, not correctness.

---

## Roadmap

Six phases, ~16–20 focused dev days. Each ends in a taggable state.

### Phase 0 — Make it work, and make the docs true (~2 days)

Order matters: 0.2 → 0.3 must precede the rest, because 0.2 is what lets later phases change the schema at all.

**0.1 Hygiene** — gofmt (`journal.go:96`, `server.go:111`, the entire `gofmt -l` surface); delete `downloadFileWithProgress` and drop `progressbar` (`go mod tidy` also sheds `go-isatty`, `colorstring`, `uniseg`); replace `newUUIDv7()` with `uuid.NewV7()`; fix `.gitignore` to `/memo-mcp` plus `.idea/`, `*.db`, `coverage.out`; Makefile gains `fmt`/`vet`/`lint`/`cover`/`test-race` and `test-verbose` in `.PHONY`.

**0.2 Migration scaffold + SQLite hygiene** — D11, D17. New `internal/journal/migrate.go` using `PRAGMA user_version` (currently 0 everywhere) with an ordered `[]migration`, one transaction each, and a **forward-version guard** (without it an old binary opening a v3 DB no-ops its `CREATE TABLE IF NOT EXISTS` and then silently mis-queries). `VACUUM INTO '<db>.v<N>.bak'` before the first structural migration. Configure pragmas via DSN so they apply to every connection:

```go
dsn := "file:" + dbPath +
    "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" +
    "&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
```

`_txlock=immediate` takes the write lock at `BEGIN`, converting SQLite's unrecoverable upgrade deadlock into a plain `busy_timeout` wait. Move `Embed` above `BeginTx`. Fix the unchecked `Scan`s. **Two distinct v0 shapes exist in the wild** — `cas-cloud.db` has no `entries_fts` — so migration 1 must introspect `sqlite_master`, not assume.

**0.3 Kill the typed-nil trap structurally** — D3. Don't add a nil check; make the shape impossible. Extract `buildEmbedder(ctx) (embedding.Embedder, func(), error)` returning a *genuinely nil interface* on failure, plus a belt-and-braces `if e == nil || e.pipeline == nil` at the top of `Embed`, before `e.mu.Lock()` (a nil-receiver call is legal; it's the mutex deref that panics).

**0.4 FTS disjunction** — D2. Three lines, the single largest recall win available. `strings.FieldsFunc` on non-alphanumerics (which also strips FTS5 operators and removes the `MATCH ''` path), drop 1-char terms, `OR`-join. Stop swallowing FTS errors — log and continue on the vector arm.

**0.5 Re-sort after recency** — D4. Hoist the decay into the candidate loop, `sort.SliceStable`, *then* truncate. Requires `created_at` in the candidate struct — fetch it with one batched `SELECT ... WHERE id IN (?,…)`, killing the N+1 at `search.go:157` in the same change.

**0.6 Filter pushdown** — D6, interim. Materialise an allowed-ID set, add `AND entry_id IN (…)` to the FTS arm. The vector arm can't use it yet (`entry_embeddings` has a `TEXT PRIMARY KEY`, so sqlite-vec's `rowid IN` pushdown isn't addressable); over-fetch with expansion up to a ~1024 cap until Phase 2's integer-keyed table removes the workaround.

**0.7 BM25 fallback + input guards** — D5 (fall back on embed error), D10 (`^[A-Za-z0-9._-]{1,64}$` token validation, `0700`/`0600` perms), D17 (cap `limit`, rune-safe truncation).

**0.8 Atomic model download** — D8. Readiness = a `.memo-model-ok` sentinel with a file manifest, not directory existence. Download into `.tmp-<pid>-<rand>/`, verify, write sentinel, `os.Rename`. On sentinel-present-but-`NewPipeline`-fails: delete sentinel, log "cache appears corrupt", retry once — the recovery path that's missing today. `--redownload-model`. `flock` on `modelDir/.lock` (`gofrs/flock` is already indirect via hugot) so two first-run sessions don't both pull 90 MB. Inject the downloader as a func field so Phase 1 can test it with zero network.

**0.9 Documentation honesty** — half the point of this phase.
- Write the **README** that has never existed: what it actually does *today* (hybrid RRF, not pure vector), install, `JOURNAL_TOKEN`, first-run model download, and an explicit "how this differs from Claude Code's built-in Auto Memory". Include the `mcp-name: io.github.kmaziarz/memo-mcp` marker now so registry publishing is later a no-op.
- Add LICENSE (MIT, matching the TS original) and credit `obra/private-journal-mcp`.
- Move `spec/go-implementation-plan.md` to `docs/history/` with a header marking it pre-rename (it still says `private-journal-mcp`, `~/.private-journal/`, "5 tools", "~15-20MB binary").
- Fix the slides: 6 tools not 5; slide 12 must show the BM25 leg, RRF and recency; slide 5 must stop arguing against keyword search the product uses; slide 20 must be re-premised; slide 10's Mermaid `\n` literals don't render as line breaks; slide 17's `0.847` is unreachable under max-normalization.
- Commit `docs/` and `spec/` — all the roadmap thinking is currently untracked.

**Exit:** `make fmt vet test-race` clean; a real 6k-char entry gets an embedding; a two-word natural-language query returns results. **Tag `v0.3.0`.**

### Phase 1 — The measurement foundation (~3-4 days)

*Nothing in Phase 2 is safe without this.*

**1.1 Deterministic fake embedder** — `internal/embedding/fake.go`, exported so all packages share it. **Not a constant vector** — a random-projection bag-of-words: each token maps to a fixed pseudo-random unit vector (FNV-1a-seeded), summed with sublinear tf weighting, L2-normalised. Shared vocabulary ⇒ high cosine; disjoint ⇒ ~orthogonal; unit norm ⇒ `cos = 1 − L2²/2` holds, so threshold logic is genuinely exercised. Deterministic, instant, no network. Its honest limitation is no synonymy, so golden labels must be lexically achievable and paraphrase cases go behind `//go:build realmodel`, nightly only. Add `NewFailingEmbedder(err)` for degradation paths.

**1.2 End-to-end MCP tests** — `internal/server/server_test.go` in `package server` (reaches unexported `s.mcp`; no production API change needed). `mcp.NewInMemoryTransports()`, server connects before client, real temp-file DB so WAL and migrations are exercised.
- `TestListToolsGolden` — marshal `tools/list` to `testdata/tools.golden.json` with a `-update` flag. Catches D9 and gives every protocol PR a reviewable diff. Highest value-per-line in the suite.
- `TestEachToolRoundTrip` (table-driven over all six), `TestToolErrorsAreToolErrors`, and stubs for `TestStructuredContentValidates` / `TestAnnotations` that activate in Phase 4.

**1.3 Golden-set retrieval eval** — `internal/eval/`, the seed of the whole repositioning. ~60 deliberately adversarial fixture entries: near-duplicates and paraphrases; stem variants; **at least three >6000 chars** sized like the real data; entries whose answer is in the *last* section; section-skew (one topic in ~40 `reflections` and once in `world_knowledge`); a knowledge-update pair for Phase 3; dates spread over two years. ~40 labelled queries including deliberate **no-match** cases. Report recall@{1,5,10}, MRR, nDCG@10, mean rank of labelled irrelevants; compare against `testdata/baseline.json` with a 2% tolerance and a `-update-baseline` flag.

**1.4 Defect regression tests** — each fails today: `TestScoresMonotonicallyDecreasing` (D4, cleanest signal), `TestRecencyAffectsOrdering`, `TestSectionFilterFindsEntryOutsideTopK` (D6), `TestFTSIsDisjunctive` + `TestPorterStemming` (D2/D13), `TestLongEntryFullyEmbedded` (D1 — 8000 chars, every chunk embedded, a phrase from the *final* section retrievable), `TestNilEmbedderDegradesGracefully` (D3), `TestModelSentinelRecovery` (D8), `TestConcurrentWriters`, `TestMigrateFromV0Shapes` over two checked-in fixture DBs mirroring the real shapes.

Budget ~2h to rewrite the existing count-based assertions (`len(results) == 3`) into identity-based ones once the hash embedder makes distances differentiate.

### Phase 2 — Retrieval quality, measured (~4-5 days)

**2.0 Spike vec0 first (2h).** The embedded sqlite-vec is **v0.1.9** (confirmed in `modernc.org/sqlite@v1.50.0/vec/`) and exposes `KNN_ROWID_IN`, metadata constraints, partition/auxiliary columns, `distance_metric=cosine`. Verify empirically: (a) `chunk_id INTEGER PRIMARY KEY` as rowid alias; (b) whether `rowid IN (<subquery>)` works or only literal lists; (c) cosine returns `1−cos_sim`; (d) `CREATE VIRTUAL TABLE` rolls back cleanly in a transaction.

**If (b) fails, take the plain-table fallback** — `chunk_vectors(chunk_id INTEGER PRIMARY KEY, embedding BLOB)` with `vec_distance_cosine(...)` in an ordinary join. vec0 v0.1.x KNN is **already exhaustive** (no ANN index, just a SIMD-friendly blob layout), so this costs a constant factor, not an asymptotic class — single-digit ms at 10k chunks — and buys arbitrary SQL filter correctness. *Given the corpus size, lean toward taking the fallback unconditionally and eliminating the pushdown risk entirely.*

**2.1 `internal/chunk`** — D1. Split on `## <section>` headers (already semantic units, 1:1 with the `sections` JSON), then paragraphs greedily packed into ≤180-token windows (hard cap 400, well under 512 even if the estimator is 25% off) with 40-token overlap; sentence-split oversized paragraphs (reuse the `.?!` scan in `truncateAtBoundary`); hard rune split as last resort. Store unprefixed text; prepend `section + ": "` at embed time so the vector carries section context. Token estimation must work without the 90 MB model: `asciiWords*1.4 + nonASCIIRunes*1.2 + punctRuns*0.5`, script-aware because a char cap breaks on CJK and code blocks. **Runtime backstop required** — since `hftokenizer` provably won't clamp, `Embed` must retry with halved text (≤2×) and surface a distinguishable `ErrInputTooLong`.

**2.2 Migration v2** — `chunks(id INTEGER PK, entry_id REFERENCES entries ON DELETE CASCADE, ord, section, text, est_tokens, embedded_at)`; **external-content** `chunks_fts` (`content='chunks'`, `tokenize='porter unicode61 remove_diacritics 2'`) with AI/AD/AU triggers — this resolves D13's integrity half, since text lives in one place and sync is enforced by SQLite rather than by remembering to write matching Go; `chunk_embeddings` vec0 with `distance_metric=cosine`. Backfill `chunks` deterministically from `entries.content` (text splitting only, no model). Drop `entries_fts` and `entry_embeddings` (verified empty in the real DBs; log the discarded count). **Virtual tables get no FK cascade**, so centralise all mutation in one `replaceChunks(ctx, tx, entryID, chunks, vecs)` — one correct implementation beats five sites that must each remember the vec delete.

**2.3 Rewrite `Search`** — D4, D6, D7. Filter set → vector arm → FTS arm → fuse **at chunk level** → aggregate chunk→entry by **max** (sum rewards long entries with many mediocre chunks, exactly the length bias chunking removes) keeping the argmax chunk → recency **then** sort → similarity gate → limit. Four points:
- The winning chunk **is** the excerpt. `generateExcerpt`'s paragraph-scoring and preceding-header logic becomes redundant (retrieval already found the best passage; `chunks.section` supplies the header). Keep `truncateAtBoundary`, retire the rest — a net LOC reduction alongside a quality gain.
- **Report two numbers, sort by one.** `relevance` = best cosine (absolute, comparable across queries) is what the agent reasons about; the fused score is only the ordering key. Add a qualitative band (`strong` ≥0.60 / `moderate` ≥0.45 / `weak` ≥0.30) — an agent handles "3 weak matches" far better than "0.412". Delete the max-normalization block. `min_similarity` defaults to 0.30, tuned against the eval's no-match queries.
- Recency stays as the bounded `0.8 + 0.2·0.5^(age/90)` multiplier for continuity, applied before the sort, with `relevance` reported *pre*-recency so the signals stay separable. If the eval shows it hurting, demote to tie-breaker.
- Land as one PR with a before/after metrics table in the description. This is where the eval harness earns its keep.

**2.4 Backfill worker + `--reindex`** — D14. Batch 16 (`RunPipeline` already takes `[]string`, so batching is free throughput), background goroutine after `Migrate`, cancelled on shutdown, errors logged not fatal. Surface `pending_embeddings` in `journal_stats` so the degraded state is never invisible.

**2.5 `memo-mcp --verify [--repair]`** — orphan chunks, orphan vec rows, FTS `integrity-check`, entries with zero chunks. Cheap insurance for a store whose value proposition is "don't lose my memories".

**Exit: tag `v0.5.0`.** A >2000-token entry is findable by a phrase from its last section; the eval baseline is recorded.

### Phase 3 — The lab (~3-4 days)

*The repositioning. This is what makes the project both educational and unlike anything else in the MCP memory space.*

- **Config surface.** Every tuning constant is currently a compile-time literal: RRF `k=60`, `alphaVec=0.6`, `alphaBM25=0.4`, the 90-day half-life and its 0.2 weight, `fetchLimit`, chunk sizes, `min_similarity`. Lift into a config struct from env and/or TOML, with defaults documented *and their derivation explained*.
- **Pluggable embedders.** Store `dim` and `model_id` in a `models` table; make the vec0 dimension a migration parameter. Ship all-MiniLM-L6-v2 (small, fast) and **EmbeddingGemma-300M** (768-dim, Matryoshka-truncatable, official ONNX exports — note it doesn't support fp16, use fp32/q8/q4) so the deck can show a real quality/size/latency trade-off measured on the user's own corpus.
- **Selectable strategies** — `vector` | `bm25` | `hybrid-rrf` | `hybrid-weighted` (later `graph`), per query and per eval run, behind one interface so a new strategy is one file.
- **`memo-mcp eval` CLI** — run the harness against a real journal or the fixture corpus; print a strategy × metric table. The flagship demo, and the honest answer to "why not just use Auto Memory?": *because you can't measure that one.*
- Optionally align fixture categories with LongMemEval/LoCoMo (information extraction, temporal reasoning, knowledge update, multi-session, abstention) so results are legible to anyone following the benchmark literature.

### Phase 4 — Memory semantics (~2-3 days)

*D12. An append-only store isn't a memory system — it can't be corrected.*

**Migration v3** — `ALTER TABLE entries ADD COLUMN` for `updated_at`, `deleted_at`, `deleted_reason`, `supersedes`, `superseded_by`, `revision INTEGER NOT NULL DEFAULT 1`. Safe: every query in the codebase uses explicit column lists, never `SELECT *`, so old binaries don't break (though they'd show soft-deleted entries — declare downgrade unsupported past v2). No FK on the self-referential columns; enforce in Go and check in `--verify`.

**Manager methods**, all routing through `replaceChunks`:
- `UpdateThoughts` — reformat, bump `revision`, re-chunk, re-embed; triggers sync FTS.
- `ForgetEntry(id, reason, redact)` — default deletes chunk rows so content is genuinely unretrievable by search but keeps `entries.content` for audit; `redact=true` also clears content. Either way the row survives as a tombstone, so dangling references from earlier conversations resolve to "this was deleted" rather than "not found" — materially better agent behaviour.
- `SupersedeEntry(oldID, input)` — one transaction: new entry with `supersedes=oldID`; old row gets `superseded_by`, `deleted_at`, `deleted_reason='superseded'`. **This is the LongMemEval "knowledge update" primitive** and the highest-value item in the phase.

Every read path gains `AND deleted_at IS NULL` — the easiest place to introduce a leak, so make it a shared const and add an eval query asserting a forgotten entry never surfaces. Tools: `update_journal_entry`, `forget_journal_entry` (idempotent), `supersede_journal_entry`, plus `include_forgotten`/`include_superseded` flags and revision-chain fields on `read_journal_entry`.

### Phase 5 — Protocol currency (~3-4 days, parallelisable with 2-4)

*The highest-value teaching content in the project — almost nothing in the Go MCP ecosystem demonstrates 2026-07-28 yet.*

- **go-sdk v1.6.0 → v1.7.0.** `AddTool`, `NewServer`, `NewInMemoryTransports`, `AddResourceTemplate`, `AddPrompt` are unchanged. `ToolAnnotations.ReadOnlyHint`/`IdempotentHint` are now always serialized — the golden test from 1.2 will surface this as a diff, which is it doing its job. Seven `MCPGODEBUG` escape hatches exist for legacy behaviour (removed in v1.9.0); don't set any, but comment them so a future debugging session finds them. Add a conformance test asserting `2026-07-28` is negotiated, and `2025-11-25` when the client advertises only that.
- **D9** — strip the bogus `required,` prefixes; the schema assertions lock it in.
- **Annotations** — five of six tools are read-only ⇒ `ReadOnlyHint: true, OpenWorldHint: ptr(false)` (a private journal is a closed world, and the SDK defaults `openWorldHint` to *true*, so setting it false is a real signal). Add `Title` to each.
- **Output schemas + `structuredContent`** — change each handler's `Out` from `any` to a concrete struct; the SDK then derives the schema and populates `StructuredOutput`, preserving your explicit prose `Content`. Purely additive. Include `relevance`, `band`, RFC3339 timestamps (stop making the model parse ms epochs), `matched_section`, `revision`/`superseded_by`, and a `degraded` field so "your embeddings aren't ready yet" is legible to the agent instead of manifesting as mysteriously bad recall. Add the missing `date_from`/`date_to` args here — `SearchOptions.DateRange` is already implemented, it just has no way in.
- **Resources** — `memo://entry/{id}` template plus static `memo://recent` and `memo://stats`; have search results carry their URI so an agent can pivot from search to resource read without a second tool call. **Prompts** — `journal_review(days)`, `journal_recall(topic)`. **Completions** — the `sections` arg has exactly six valid values; ~20 lines removes a whole class of typo-driven empty results. Together this takes the server from tools-only to a full demonstration of the protocol surface, which is exactly what a teaching artifact should show.
- **Streamable HTTP** behind `--http`, with `StreamableHTTPOptions{Stateless: true}` (required — otherwise clients negotiate down to 2025-11-25). Refactor `Run` into `RunStdio` + `HTTPHandler`. **Security is not optional**: default `127.0.0.1:8765`, refuse non-loopback without an explicit `--http-allow-remote`, require a bearer token (`MEMO_HTTP_TOKEN`, *not* `JOURNAL_TOKEN` — that's a DB selector that appears in filenames and is not a secret), log the effective bind and auth mode. Consider deferring `--http` past v1.0.0: it turns a private local journal into a network service, and these requirements are easy to get subtly wrong under release pressure.
- **`-ldflags` version** — delete the hardcoded `"2.0.0"`, which corresponds to no tag and will mislead anyone debugging a registry install.

### Phase 6 — Ship (~2 days)

- **CI** — matrix over ubuntu/macos/windows × Go 1.26.x: `gofmt -l` must be empty, `go vet`, `golangci-lint` (include `errcheck`, `bodyclose`, `rowserrcheck`, `sqlclosecheck` — the last three directly target the bug class at `search.go:94-106`), `go test -race`, `CGO_ENABLED=0 go build`. **PR CI must never download a model** — the fake embedder is the enabler; guard real-model tests behind `//go:build realmodel`, nightly only. Cache `~/go/pkg/mod`; the hugot/gomlx graph dominates wall time.
- **GoReleaser** — `CGO_ENABLED=0`, darwin/linux × amd64/arm64 + windows/amd64, `-s -w` (32 MB → ~24-26 MB). The 90 MB model is fetched at runtime, so archives stay small — which makes first-run network a prominent README item and is exactly why 0.8's atomic download matters for the release story.
- **`server.json` + `mcp-publisher`** (GitHub OIDC from Actions) to the **official MCP registry**, which feeds Smithery, PulseMCP, Docker Hub and VS Code's `@mcp` view. The README `mcp-name:` marker from 0.9 is the ownership proof. Note Claude Code's `/mcp` doesn't browse this registry — Anthropic's Connectors Directory and plugin marketplace are separate submissions.
- **`docs/RETRIEVAL.md`** — fusion formula, thresholds, chunk parameters, current eval baseline. Cheap while it's fresh; it's what makes the next retrieval change an hour instead of a re-derivation.
- **Tag `v1.0.0`** — and reconcile it with the `"2.0.0"` the server currently reports. Tagging v1.0.0 on today's code would publish a server whose primary tool returns "No relevant entries found." for every query.

### Phase 7 — Graph as a strategy (optional, later)

Rewrite `docs/graphrag-evolution-plan.md` before building any of it (see Context for why its premises are stale), then implement entity extraction + traversal as **one more selectable strategy** measured against the others — so the deck can show honestly where graph wins and where it costs. Keep the cross-project global graph opt-in, and note in the docs that it partially reverses the founding "the token IS the namespace" decision. If LLM-based extraction is ever added, commit to local-only (Ollama) or the "no network calls, no cloud" promise breaks.

---

## Sequencing

```
0.1 ─┬─ 0.2 ─┬─ 0.3, 0.4, 0.5, 0.6, 0.7 ─┬─ 1.1 ─ 1.2 ─ 1.3 ─ 1.4 ─ 2.0 ─ 2.1 ─ 2.2 ─ 2.3 ─ 2.4 ─ 2.5 ─ 3 ─┐
     └─ 0.8 ─┘        0.9 (any time) ────┘                                        └─ 4 ────────────────────┼─ 6
                                          └────────────── 5 (parallel) ──────────────────────────────────┘
```

Hard constraints: **0.2 gates every schema change** — build it before you need it. **1.1 gates all of Phase 1 and CI** — it's the keystone. **1.3 gates 2.3** — rewriting the fusion without measurement is the one genuinely reckless move available. **2.2 gates Phase 4** (migrations are ordered). Phase 5 touches only `internal/server` and `cmd/`, so it can run in parallel. Phase 5's annotations and output schemas should land *after* 2.3 and Phase 4 so they describe the final tool set.

| Milestone | Contents | Tag |
|---|---|---|
| **M1** ~1 wk | Phase 0 + 1.1/1.2 — *search actually works* | `v0.3.0` |
| **M2** ~2.5 wk | 1.3/1.4 + Phase 2 — *retrieval is measurable and good* | `v0.5.0` |
| **M3** ~4 wk | Phases 3, 4, 5 — *the lab; memory is editable; protocol is current* | `v0.9.0` |
| **M4** ~5 wk | Phase 6 — *shipped* | `v1.0.0` + registry |

If forced to cut: drop Phase 5's resources/prompts/completions and Phase 4's `redact` mode. Everything else compounds.

---

## Verification

- **Every phase:** `make fmt vet test-race cover` clean, and `make eval` shows no recall@k / MRR regression against the recorded baseline.
- **Phase 0:** write a 6000-char entry and confirm `entry_embeddings` gains a row (it doesn't today). Search two unrelated words and get results (you don't today). Delete `~/.cache/memo-mcp/models`, block network, start, call both tools — must degrade to BM25-only with a warning, **not** panic. Interrupt a download mid-flight and restart — must recover. `JOURNAL_TOKEN=../escape` rejected. `ls -l ~/.memo-mcp` shows `0700`/`0600`.
- **Phase 1:** the regression tests in 1.4 fail before their fixes and pass after; `-update-baseline` records the eval baseline.
- **Phase 2:** a >2000-token entry is retrievable by a phrase from its *last* section. Migrate both real v0 DBs (the one with `entries_fts` and the one without) and confirm byte-identical `entries` content. A no-match query returns zero results.
- **Phase 3:** `memo-mcp eval --strategy vector,bm25,hybrid-rrf` prints a comparison table; switch model in config, `--reindex`, re-run, compare.
- **Phase 4:** supersede an entry — old hidden by default, reachable with the flag, revision chain walkable; FTS and vec stay in sync after delete (`--verify` clean).
- **Phase 5:** connect a v1.7.0 client over stdio and stateless HTTP; `tools/list` shows no `required,` in descriptions, annotations and output schemas present; read a `memo://entry/{id}` resource; an older client still negotiates down to 2025-11-25.
- **Phase 6:** `mcp-publisher publish --dry-run` validates; install the released binary on a clean machine and connect it to Claude Code.
- **End to end:** `claude mcp add`, hold a real session, write and retrieve memories across a restart.

## Critical files

`internal/search/search.go` (D2/D4/D5/D6/D7) · `internal/journal/journal.go` (schema, write path, D1/D10/D11) · `internal/embedding/embedding.go` (D1/D8) · `cmd/memo-mcp/main.go` (D3) · `internal/server/server.go` (all protocol work) · `Makefile` · `.gitignore`

New: `internal/journal/migrate.go` · `internal/embedding/fake.go` · `internal/chunk/` · `internal/server/server_test.go` · `internal/eval/` · `README.md` · `LICENSE` · `server.json` · `.goreleaser.yaml` · `.github/workflows/ci.yml` · `docs/RETRIEVAL.md`

## Open decisions

1. **Take the plain-table `vec_distance_cosine` fallback in 2.0 unconditionally?** Trades a constant-factor slowdown (single-digit ms at this corpus size) for eliminating all vec0 pushdown risk. Leaning yes.
2. **Does `--http` belong in v1.0.0?** It turns a private local journal into a network service. Deferring to v1.1 removes a whole class of release-pressure security mistakes.
