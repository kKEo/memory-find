# memo-mcp roadmap — from journal to explainable knowledge base

*Written 2026-10-02 at HEAD `375f24c`. Supersedes the 2026-09-04 review that used to live in
this file (`git show 375f24c:docs/review-roadmap.md`). Research basis:
[`knowledge-base-sota.md`](knowledge-base-sota.md). Owner decisions taken 2026-10-02 are listed
in §9.2.*

---

## 1. What this document is and how to read it

This is the forward-looking plan for turning memo-mcp, today a private journal for Claude, into a
**measurable, local knowledge base for agents and people**. It is not a review. The review that
found the original bugs is summarised in §4 and lives in git history.

**How to read it, by role**

- *New to retrieval or to this project?* Read §2 (what we are building), §3 (four short stories),
  §5 (the rules we follow), then for each phase only the **Goal**, **Why** and **Concepts you
  meet** fields. Every technical word is in *italics* the first time it appears and is defined in
  one plain sentence in the glossary (§15).
- *Engineer picking up a phase?* Read §4.5 (the hardening backlog), §6 (spikes), the phase's
  **What gets built** and **Exit criteria**, then §9 (decisions) and §12 (verification).
- *Owner?* §8 (sequencing and what to cut) and §9.3 (open decisions, each with a recommendation).

**The learning-path promise.** One phase is roughly one Go package, one article in `articles/`,
one git tag and one eval report in `docs/eval/`. Each phase teaches one cluster of ideas and
leaves a number behind that proves it worked.

**Conventions.** Each phase block has the same eleven fields in the same order: Story, Goal,
Why, What gets built, Concepts you meet, Explainability in this phase, Exit criteria, Rollback,
Tag, Learning artifact, Effort. Tags are *semantic versions*. Effort is in focused dev-days and
includes a half-day hardening slot per phase. Paths are relative to the repository root.

---

## 2. Positioning: what memo-mcp becomes

memo-mcp becomes a knowledge base that an AI agent fills on demand ("learn library X version Y",
"remember this finding") and that both agents and humans can search, read and audit. It is one
MCP server plus one command-line tool over one SQLite file. It is pure Go with no cloud, no C
compiler and exactly one network call ever (downloading an embedding model).

Three properties make it different from the memory features built into agent products:

1. **Raw sources are kept losslessly.** Everything derived (facts, entity pages) points back to
   the exact passage it came from. Knowledge is corrected by adding and retiring records, never by
   destroying them.
2. **Every result can explain itself.** On request a search says which retrieval method matched,
   which passage, from which source and version, how trusted it is and how fresh it is.
3. **Everything is measured.** A built-in evaluation harness turns "search feels better" into a
   table of numbers, so every change to ranking, chunking or the embedding model is a before/after
   comparison. The calling agent is the only LLM in the system; the server does deterministic work
   (chunk, index, fuse, filter, explain) and reports on itself.

Humans get the same store through `memo-mcp export --md` (plain markdown files that open in any
editor or Obsidian), terminal commands (`search`, `read`, `explain`, `ls`) and, later and
optionally, a read-only local web page.

The knowledge base is built fresh in this repository and binary (owner decision 3). The parts of
the journal that earned their place are kept: the eval harness, the deterministic fake embedder,
the migration scaffold, the embedding pipeline and the SQLite hygiene.

---

## 3. Four stories the phases are cut from

Each story names the first phase in which it becomes demoable.

**(a) A coding agent learns a library.** The agent fetches the docs itself (it already has web
tools), then calls `ingest(content, source{uri, title, version: "v1.8.0", kind: "doc", origin:
"web"}, namespace: "go-sdk")`. Later it calls `search("set ttl on a resource", scope{library:
"go-sdk", version: "v1.8.0"}, max_tokens: 1500)`, gets short identifiers and one-liners, then
`read("memo://chunk/812")` for the passage it needs. It stores what it learned with
`remember("SetCacheable sets ttlMs on list results", about: ["SetCacheable"], evidence_uri:
"memo://chunk/812")`. When a result surprises it, it asks `search(..., response_format:
"explain")` and sees why the top hit beat the second. Demoable in P2; complete in P5.

**(b) A chatbot answers with citations.** `search("what changed in the 2026-07-28 spec",
response_format: "detailed", granularity: "page")` returns citation-ready items with URI,
title, source and content; the chatbot writes a summary with footnotes and calls `read` on a page
for depth. Demoable at chunk and fact level in P5; curated pages arrive in P8.

**(c) A human browses.** `memo-mcp export --md ~/kb` writes one markdown file per document and
page, each with front matter saying where it came from; the folder opens in Obsidian.
`memo-mcp search "interceptor order" --ns grpc --explain` prints a ranking table in the terminal,
and `memo-mcp explain "interceptor order" memo://chunk/812` prints the full reasoning for one hit.
Demoable in P1 (export) and P2 (search, explain); a web page in P9.

**(d) A researcher measures.** `memo-mcp eval --models minilm,granite-small-r2 --profiles
default,precise --format md > docs/eval/v0.7.0.md` compares embedding models and ranking
profiles on the same labelled questions, with quality and cost side by side, and the researcher
writes the bake-off article. Demoable in P3.

---

## 4. Where we are today

### 4.1 What shipped (2026-09-04, commits `94f81ad` and `73fb9d4`)

The 2026-09 review found that `search_journal` returned nothing on real data: entries longer than
a few hundred words silently never got an embedding, and the keyword query required every word to
match. It listed seventeen defects (D1–D17) and a seven-phase plan. Phases 0 and 1 shipped the
same day:

- the headline bugs were fixed (keyword terms are now OR-joined; a typed-nil embedder can no
  longer panic; recency is applied before sorting; filters are resolved in SQL where the vector
  table allows it; the model download is atomic and recovers from interruption; the `required,`
  leak in tool descriptions is gone; tokens are validated and files are `0700`/`0600`);
- the **measurement foundation** was built: a deterministic *hash embedder*
  (`internal/embedding/fake.go`) with real geometry instead of a constant vector; an eval harness
  (`internal/eval/`) with 79 fixture entries, 29 labelled queries and a recorded baseline; a
  *golden test* of the MCP `tools/list` response (`internal/server/testdata/tools.golden.json`);
  a migration scaffold (`internal/journal/migrate.go`); and two fixture databases reproducing the
  two real on-disk shapes.

The story is told in [`../articles/the-measurement-foundation.md`](../articles/the-measurement-foundation.md).
The exit tag `v0.3.0` was never cut.

What did **not** finish: long entries are still truncated to 1,500 runes before embedding (a
stopgap, not chunking); there is no raw relevance score (the top hit always shows `1.000`); no
update, forget or supersede; no stemming; no embedding backfill; the vector arm cannot push
filters; no `realmodel` test tier; the server reports version `"2.0.0"` though no tag exists.

### 4.2 What the binary is right now (verified 2026-10-02, HEAD `375f24c`, before P0)

> P0 changed several rows below: go-sdk is now v1.8.0 (protocol 2026-07-28), the version comes
> from the git tag, the binary has subcommands, `MEMO_KB`/`MEMO_HOME` replace the journal
> variables, the module path is `github.com/kKEo/memory-find`, and the stats crash is fixed. The
> schema, tools and search constants are unchanged until P1/P2.


| Fact | Value |
|---|---|
| Commits / tags | 8 commits, HEAD `375f24c`, **no git tags** |
| Reported version | `Version: "2.0.0"` at `internal/server/server.go:29` (matches nothing) |
| Toolchain and deps | go 1.26.0; go-sdk **v1.6.0** (research target: v1.8.0, spec 2026-07-28); hugot v0.7.2; modernc.org/sqlite v1.50.0 with sqlite-vec v0.1.9 |
| MCP tools | `process_thoughts`, `search_journal`, `read_journal_entry`, `list_recent_entries`, `read_recent_entries`, `journal_stats`; text results only; no annotations, output schemas, resources or prompts |
| CLI | flags `--stats`, `--redownload-model`; no subcommands |
| Environment | `JOURNAL_TOKEN` (required), `JOURNAL_PATH` |
| Schema | one migration: `entries`, `entry_embeddings` (vec0, `float[384]`, L2 distance), `entries_fts` (standalone, default tokenizer, no stemming) |
| Search constants (`internal/search/search.go:48-80`) | `rrfK=60`, `alphaVec=0.6`, `alphaBM25=0.4`, `defaultSearchLimit=10`, `maxSearchLimit=200`, `fetchMultiplier=3`, `minFetchLimit=30`, `maxFetchLimit=2000`, `maxSQLINParams=500`, `recencyHalfLifeDays=90` (factor `0.8 + 0.2·0.5^(age/90)`) |
| Eval baseline (`internal/eval/testdata/baseline.json`) | recall@1 0.72, recall@5 0.91, recall@10 0.93, MRR 0.89, nDCG@10 0.89 over 29 queries; tolerance 0.02 on the means |
| Size | 2,415 non-test Go lines in 6 packages; 72 test functions; no CI |

### 4.3 Reusable vs replaced

**Reused (generalised, not rewritten):** the migration runner (`migrate.go`: ordered
migrations, `PRAGMA user_version`, forward-version guard); the DSN pragmas (`journal.go:64-69`:
WAL, `busy_timeout(5000)`, `synchronous(NORMAL)`, `foreign_keys(ON)`, `_txlock=immediate`); name
validation and file permissions; the hugot pipeline with atomic download, sentinel, lock,
injectable fetcher and corrupt-cache recovery (`internal/embedding/embedding.go`);
`HashEmbedder`/`FailingEmbedder`; `fuseRanks`, `fts5Query`, `recencyFactor`,
`truncateAtBoundary` from `search.go`; the in-memory MCP test session and golden mechanism; the
eval metrics and baseline/tolerance mechanism; the fixture corpus content; the two v0 fixture
databases (reused by the "refuse a legacy file" test).

**Replaced by design (owner decision 3):** the journal schema and the six journal tools; the six
thought categories; `truncateForEmbedding`; `generateExcerpt` (the winning chunk becomes the
excerpt); `normalizeScores`; the vec0 over-fetch workaround.

### 4.4 Maps

Old review phase → this roadmap: Phase 0, 1 → shipped (retro tag `v0.3.0`); Phase 2 → P1 + P2;
Phase 3 → P3; Phase 4 → P4; Phase 5 → P0 (SDK bump) + P5 (surface), `--http` deferred; Phase 6 →
P6; Phase 7 → P7; new → P8, P9.

Research step (SOTA §7) → phase: A → P1/P2; B → P1 (schema) and P4 (semantics); C → P3; D → P3
anchor, slices added in P4, P7, P8; E → P0 (SDK) + P2 (typed tools from day one) + P5 (final
surface); F → P4 (fact arm) + P7 (entities, graph); G → P8; H → P6. Two deliberate departures
from the research doc's letter: B is split (schema early because provenance must be in the first
migration; semantics after the embedding bake-off because the bake-off changes every later
baseline), and H ships before F and G (owner decision 7: 1.0 after the agent surface).

### 4.5 Hardening backlog

Issues confirmed in the current code by the 2026-10-02 audit. Each phase keeps a half-day slot
for issues found during it; anything unfixed is appended here with a target phase.

| Issue | Where today | Resolved in |
|---|---|---|
| `journal_stats` / `--stats` crash when the mean entry length is not a whole number (`AVG(LENGTH())` scanned into an integer; tests pass only because fixture lengths sum to a multiple of 3) | `search.go:620-623` | P0 |
| Keyword-only hits can never reach the first page once ten entries have vectors: the best keyword-only fused score (0.4/61) is below every vector candidate down to rank 31, and the fused list is cut to 30 before recency. BM25 reorders but never adds | `search.go:51-53, 153-155, 268` | P2 (fusion designed against this; eval fixtures with lexical-only matches) |
| Eval harness defects: an absent irrelevant on an empty result list scores rank 1 (punishes correct abstention); nDCG clamps k to the result length (inflates short lists); zero-relevant queries add fixed zeros; the mean-only 0.02 gate misses a two-relevant query losing its rank-1 hit; recency is unmeasured; long-entry queries share head vocabulary with the embedded prefix; HashEmbedder is hard-coded with no `realmodel` tier and no CLI; the loader is coupled to the journal API; no category or cost columns | `internal/eval/metrics.go:67-69, 78-81, 103`, `eval_test.go:28, 53, 93-104`, `corpus.go:103-104, 216` | P2 (metrics, loader), P3 (gate, slices, CLI, categories, cost) |
| Recency is not "mild": the ×0.8–1.0 factor moves vector-only candidates up to ~15 places; unclamped for future dates (×4.1 at one year in the future) | `search.go:414-418` | P2 |
| Degraded states never reach the agent: embed and FTS failures go to stderr only; a vec0 SQL error fails the whole search; both arms empty gives "No relevant entries found." | `search.go:133-150`, `server.go:152` | P2 |
| Writes report success without a vector and nothing backfills | `journal.go:136-174`, `server.go:128` | P1 |
| The 1,500-rune cap is not token-safe (JSON, code and CJK exceed 512 word-pieces well under it); query text is uncapped | `journal.go:220-236`, `search.go:127` | P1 |
| Filtered search is exact only within 2,000/500 bounds; comments and README claim exactness | `search.go:258-311` | P1 + P2 |
| No relevance floor: `"???"` returns ten results, the first scored `1.000` | `search.go:278-281, 427-429` | P2 |
| Model download runs synchronously before the MCP server starts; the lock-timeout branch is unreachable; stale `.download-*` dirs are never swept; hugot's verbose output would go to stdout, the MCP stream | `main.go:63-73`, `embedding.go:161-184` | P1, P3 |
| Model cache is wiped on any load error, before the lock, with no manifest or hash | `embedding.go:87-154` | P3 |
| Migration runner reads `user_version` outside the transaction; negative versions panic | `migrate.go:33-44` | P1 |
| Weak bounds: `sections` has no enum, whitespace-only inputs accepted, no content size cap, `days` unbounded | `server.go:134, 182-185, 228` | P2, P5 |
| Tests never use the production DSN despite comments saying so | `server_test.go:28-39`, `eval_test.go:44` | P1 |
| Unchecked errors; `make lint` has no `.golangci.yml` | `search.go:599-606`, `journal.go:239-240`, `Makefile:36-37` | P0 |
| Tool descriptions promise "Nobody but you will ever see this." while entries are plaintext SQLite visible to the MCP host; "chronological" means newest first | `server.go:43-48, 82, 87, 97` | P2 |
| No ranking explanation; the excerpt picker can omit the matched identifier | `search.go:28-35, 652-666` | P2 |
| Opening always creates and migrates; a mistyped token silently starts an empty journal; `--stats` is not read-only | `main.go:51-59`, `journal.go:59-81` | P1 |
| No signal handling or cancellable root context | `main.go:32` | P0 |
| `Embedder` is single-text and role-less; one inference mutex plus one DB connection means a batch backfill would block reads | `embedding.go:18-25, 57-58`, `journal.go:79` | P1, P3 |
| Model identity neither pinned nor recorded; a same-dimension model swap silently mixes vectors | `embedding.go:76, 124-128`, `migrate.go:99` | P1 |
| About twenty code comments contradict the code (`search.go:69-74, 97-100, 142-143, 178-181, 294-295`; `journal.go:93-95, 220-223`; `eval.go:32-34`; `corpus.go:29, 99-100`; `server_test.go:28-30, 287-300`) | various | P1, P2 (old code deleted; new code reviewed for comment truth) |
| Hard-coded version, no tags, no CI, stale "Phase N" comments, repository name mismatch (docs say `kmaziarz/memo-mcp`, remote is `kKEo/memory-find`) | `server.go:29`, `go.mod:1`, `.github/` absent | P0 |

---

## 5. Principles we do not re-litigate

These come from the research document (IDs in brackets) and the owner's decisions. Phases cite
them; they are not reopened.

- **Keep raw sources; every derived record cites the raw chunks it came from.** [D-A]
- **Two clocks, never delete.** Facts carry when they were true and when we learned it; a wrong
  fact is invalidated or superseded, not erased. [D-B]
- **No server-side LLM.** The calling agent is the LLM. A local Ollama executor is opt-in only.
  MCP sampling is not used. [D-C, D-L, decision 3]
- **The server never fetches URLs.** `ingest` takes content; the client fetches. The only network
  call is the model download. [decision 2]
- **Consolidation is explicit and its outputs are marked as inferences.** [D-D]
- **Graph as an index, as one routed search arm, never the default path.** [D-E, D-F]
- **Extraction is a ladder with no mandatory rung;** mention edges come first; entity resolution
  is deterministic and keeps aliases. [D-G, D-H, D-H2]
- **Models live in a table; the default embedder is Apache/MIT licensed;** EmbeddingGemma is
  opt-in with its licence shown. [D-I, decision 4]
- **A reranker ships only on a measured gain;** weighted RRF is the default fusion; cutoffs are
  gap-based, not absolute thresholds. [D-J, D-J2]
- **About seven defaulted parameters per tool, ten tools at most;** numeric knobs live in
  server-side profiles, never in the LLM-facing schema. [D-K, §6.3]
- **Two faces of one store:** typed tools, and a file-shaped face (export, resources, an index of
  at most 8 KB for AGENTS.md). [D-M]
- **Provenance and trust are in the first migration; revocation is a hard filter applied before
  ranking.** [D-N]
- **Protocol:** go-sdk v1.8.0, spec 2026-07-28, stateless; anything the server needs travels as an
  argument. [D-O]
- **Namespaces default to "all" for search; writes target exactly one.** Namespaces are a search
  scope, not a privacy boundary; separate database files (`MEMO_KB`) are the isolation boundary.
  [decision 5]
- **Trust is assigned by channel, not by claim.** Tool writes are capped at `agent`; raising trust
  needs a human through elicitation or the CLI, and is audited. [decision 6]
- **Pure Go, `CGO_ENABLED=0`, no cloud, no telemetry.** Local, pull-only metrics on a loopback
  address and logs on stderr are not telemetry: nothing leaves the machine. [D-P] This binds the
  memo-mcp binary. The optional macOS menu-bar app (`tray/`, its own module and release asset)
  links AppKit and WebKit through cgo and never enters the server's build.
- **Everything is measured with labelled recall/nDCG plus cost, never LLM-judge win rates.**
- **One explain contract, three faces.** CLI, MCP and the UI render the same `Why` and `Trace`
  Go structs, and tests assert the numbers are identical.

---

## 6. Spikes: unknowns measured before they shape the design

A *spike* is a short throwaway experiment that answers one question with a number before the
design depends on it. Spike code lives in `spikes/<name>/main.go` behind `//go:build spike`
(never in the binary or in PR CI). Each spike writes `docs/spikes/<id>.md` with the question, the
number and the decision.

| ID | Question in plain words | Gates | Pass criterion | Fallback | Runs in |
|---|---|---|---|---|---|
| S1 | Does go-sdk v1.8.0 break our tools, the in-memory test session or the golden file? Do `SetCacheable`, elicitation and output schemas work over stdio? | P0, P2, P5 | tests green; golden diff reviewed and explained; `2026-07-28` negotiated | stay on v1.7.0 for new tools; defer TTL hints to P5 | P0 |
| S2 | vec0 versus a plain `chunk_vecs` table with `vec_distance_cosine`: latency at 10k/50k/200k chunks × 384/768 dims; does `rowid IN (subquery)` push down; does `CREATE VIRTUAL TABLE` roll back in a transaction? | P1 schema (OD-1) | a latency table; rule: plain table unless vec0 is more than 3× faster at 50k and pushdown works | plain table | P0 |
| S3 | FTS5: is the trigram tokenizer present; does an external-content table with triggers round-trip; what does `porter unicode61` do to `net/http`, `useCallback`, `ERR_CONN_RESET`; do `highlight()`, `bm25()` and `integrity-check` work? | P1 schema (OD-2), P2 explain | one SQL proof per question | exact index = `unicode61 tokenchars '_.:-/'` | P0 |
| S4 | GoMLX op coverage: which candidate ONNX models load and embed sanely under `hugot.NewGoSession` (`sim(a,a') > sim(a,b)`), at what p50 latency for 256 tokens and batch 16? Candidates: MiniLM (control), granite-small-r2, granite-r2, granite-97m-multilingual-r2, arctic-embed-m-v2, Qwen3-Embedding-0.6B, EmbeddingGemma-300M q8, potion-retrieval-32M (no ONNX); rerankers ms-marco-MiniLM-L-6-v2, Ettin-17M/32M | P3 bake-off (D-I, D-J) | table: loads, sane, dims, p50 ms, licence; failures dropped | MiniLM and potion are the floor; the schema never depends on a dimension | P0 (quick), P3 (full) |
| S5 | Pure-Go tree-sitter (`odvcencio/gotreesitter`, `malivvan/tree-sitter` on wazero): parses Go, TypeScript and Python samples without panics; binary growth under 10 MB? | P1 code chunking (OD-17) | both criteria | heading- and fence-aware splitter plus `go/parser` | P1 |
| S6 | CSR adjacency and personalised PageRank on a 100k-node power-law graph: build time, power-iteration time, hub-capped fixed-depth joins versus recursive CTEs; is a Go Louvain available? | P7 graph arm | PPR under 50 ms at 100k nodes / 1M edges; adjacency load under 500 ms | cap hops at 1–2, cache per namespace, skip Louvain | P7 (done: `docs/spikes/S6-graph.md`, 10 ms build, 42 ms PPR) |
| S7 | Does elicitation over stdio work in Claude Code: does the dialog show, are accept and decline respected? | P5 `promote` | manual test recorded | `promote` returns the CLI command | P4/P5 (SDK side done: `docs/spikes/S7-elicitation.md`; live Claude Code check open) |

---

## 7. Phases

| Phase | Name | Tag | Effort (days) | Story | Research step |
|---|---|---|---|---|---|
| — | retro tag on `73fb9d4` | `v0.3.0` | 0 | — | review Phases 0–1 |
| P0 | Reset the map | `v0.4.0` | 2–3 | all | E (SDK) |
| P1 | The store | `v0.5.0` | 5–6 | a, c | A, B (schema) |
| P2 | Search that explains itself | `v0.6.0` | 5–6 | a, c | A, E (typed tools) |
| P3 | The lab: eval v2, embedding models, profiles | `v0.7.0` | 6–7 | d | C, D |
| P4 | Provenance, trust and time | `v0.8.0` | 4–5 | a, b | B (semantics), D, F (fact arm) |
| P5 | The agent surface | `v0.9.0` | 4 | a, b | E |
| P6 | Ship 1.0 | `v1.0.0` | 2–3 | all | H |
| P7 | Entities and graph, as an index | `v1.1.0` | 6 | a, b | F |
| P8 | Compaction and pages | `v1.2.0` | 5 | b | G |
| P9 | A knowledge base you can read (optional) | `v1.3.0` | 3 | c | — |

Total 42–48 focused dev-days with P9, 39–45 without. The 2026-09 review estimated 16–20 days for
less and shipped two phases in a month, so treat these as ranges, not promises.

### P0 — Reset the map · 2–3 days · `v0.4.0`

> **Status (2026-10-02): built.** Retro tag `v0.3.0` cut; module renamed; stats crash fixed with
> a test proven to fail on the old code; go-sdk v1.8.0 with an unchanged golden and a protocol
> conformance test; version from `-ldflags`; `internal/cli` with `serve | status | version |
> model redownload` and `MEMO_KB`/`MEMO_HOME`; signal handling; `.golangci.yml` (lint clean);
> minimal CI; Makefile targets; spikes S1–S3 decided (S2: plain table, OD-1 resolved; S3: option
> A confirmed, OD-2 resolved); S4 harness written, see `docs/spikes/S4-embedding-models.md`.
> Remaining before the tag: push so CI runs once, then `git tag -a v0.4.0`.


- **Story.** All four: the repository must tell the truth before anything is built on it.
- **Goal.** Anchor history with a tag, talk the current MCP protocol, report a true version, give
  the binary subcommands, run checks automatically, measure the four library unknowns, fix the one
  crash that affects users today, and point every document at this roadmap.
- **Why.** Every later phase rewrites the tool surface; doing that on go-sdk v1.6.0 means doing it
  twice. No tag has ever been cut and the server claims `"2.0.0"`. Nothing protects nine phases of
  work in CI. Spikes S1–S4 decide the shape of the two most expensive things to change later (the
  schema and the tool surface).
- **What gets built.**
  - An annotated retro tag `v0.3.0` on `73fb9d4` ("2026-09 review Phases 0–1: fixes and
    measurement foundation"), so versions match tags from here on (OD-12).
  - Documentation moves — **done on 2026-10-02 together with this file**: `review-roadmap.md`
    renamed to this file; `graphrag-evolution-plan.md` moved to `docs/history/` with one merged
    banner; links in `knowledge-base-sota.md` and the README fixed; the Makefile's stale "no
    server tests" comment corrected; `.gitignore` covers editor files; a README truth pass; a
    `docs/README.md` index; an errata header on the measurement article.
  - **Repository rename** (owner decision 9): the public repository is
    `github.com/kKEo/memory-find`; the binary stays `memo-mcp`. Change `go.mod:1` and every Go
    import path (`grep -rl 'github.com/kmaziarz/memo-mcp' --include='*.go'`), the clone commands
    in `slides.md:333, 590`, and verify the `mcp-name` marker's owner casing against the GitHub
    login before P6.
  - Fix the stats crash: scan `AVG(LENGTH(content))` into `sql.NullFloat64` and add a test with a
    fractional mean.
  - `go.mod`: go-sdk v1.6.0 → **v1.8.0** on the existing six tools (spike S1); regenerate
    `tools.golden.json` with `-update` and read the diff (annotations now serialise); a
    conformance test asserting protocol version `2026-07-28` is negotiated on stdio.
  - Version from `-ldflags "-X main.version=$(git describe --tags --always)"` with a
    `debug.ReadBuildInfo()` fallback; delete `"2.0.0"`; `memo-mcp version` prints version,
    protocol version, Go version and model directory.
  - `internal/cli/` skeleton using the standard library's `flag` with subcommand dispatch (OD-14):
    bare `memo-mcp` means `serve`, so existing MCP configs keep working; `serve | version |
    status | model redownload`; the old flags stay as aliases for one release.
  - Environment: `MEMO_KB` (database name, default `default`) → `$MEMO_HOME/kb/<name>.db`;
    `MEMO_HOME` (default `~/.memo-mcp`); `JOURNAL_TOKEN` and `JOURNAL_PATH` accepted as
    deprecated aliases with a warning. The P0 binary still opens the journal schema; the switch
    happens in P1.
  - `signal.NotifyContext` root context so SIGINT/SIGTERM close the database and the model
    session cleanly (needed before any background worker).
  - `.golangci.yml` enabling `errcheck`, `rowserrcheck`, `sqlclosecheck`, `bodyclose`, so
    `make lint` means something.
  - Minimal CI (`.github/workflows/ci.yml`, ubuntu only): `gofmt -l` empty, `go vet`,
    `golangci-lint`, `go test -race`, `CGO_ENABLED=0 go build`. **PR CI never downloads a
    model** (the hash embedder makes this possible); `//go:build realmodel` is reserved for a
    nightly job. The three-OS matrix and the release pipeline come in P6.
  - Makefile: `eval`, `golden-update`, `baseline-update`, `spike` targets; ldflags in `build`.
  - Spikes S1–S4 (about one day in total, bounded) with their `docs/spikes/*.md` decision notes.
  - Re-point the stale "Phase N" comments (`internal/eval/eval_test.go:33`,
    `internal/embedding/fake.go:29`) to roadmap phase names; comments inside packages that P2
    deletes can wait.
- **Concepts you meet.** *Git tag* (a named pointer to a commit) · *semantic version*
  (`vMAJOR.MINOR.PATCH` tells users what kind of change happened) · *protocol version
  negotiation* (client and server agree on a spec date) · *stateless core* (the server keeps
  nothing between calls) · *golden test* (a saved copy of exact output, diffed on every change) ·
  *spike* · *op coverage* (whether the pure-Go runtime implements every operation a model file
  needs) · *build tag* (a compile-time switch) · *CI* (checks that run on every push) · *linter*
  (a tool that flags unchecked errors and unsafe patterns).
- **Explainability in this phase.** `memo-mcp version` and the MCP implementation version report
  the real tag; every spike ends in a written number and decision; the SDK golden diff is reviewed
  in the pull request rather than regenerated blindly.
- **Exit criteria.** `git tag` shows `v0.3.0`; tests green on go-sdk v1.8.0; the conformance test
  passes; `memo-mcp version` prints the tag; CI is green with network disabled; the stats test
  with a fractional mean passes; `go.mod` and imports use the new module path and `go build`
  works; each `docs/spikes/S1..S4.md` ends with a decision line; the golden diff is committed with
  an explanation.
- **Rollback.** Spikes are build-tagged out; the SDK bump and the module rename are each one
  revertible commit; document moves are `git mv`.
- **Tag** `v0.4.0`.
- **Learning artifact.** `articles/why-rebuild-instead-of-migrate.md`: why a fresh schema beats
  incremental migration when there is no data to protect, and why you measure libraries before
  designing around them. Plus `docs/spikes/`.
- **Effort.** 2–3 days.

### P1 — The store · 5–6 days · `v0.5.0`

> **Status (2026-10-02): built.** `docs/schema.md` signed off (versions are revisions; stemmed
> index covers text and header; no default TTL). `internal/kb` (open with ownership probe and
> read-only/no-create modes, migration runner reading the version inside the transaction,
> migration 1 with triggers), `internal/chunk`, Embedder v2 (batch, roles, model info, halving
> backstop, download lock timeout and stale-dir sweep), `kb.Ingest` with dedup/revisions/jobs/
> audit, `Backfill`, `Read`/`List`/`Status`/`Verify`, markdown export with round-trip import;
> CLI `ingest | read | ls | export --md | verify | backfill | status`. Legacy journal packages
> remain until P2. Spike S5 deferred with a written decision. Remaining before the tag: commit,
> CI green, `git tag -a v0.5.0`.


- **Story.** (a) the ingest half; (c) export.
- **Goal.** Design the new database layout once and carefully, then build the way knowledge gets
  in: whole documents are kept, split into chunks, indexed for keywords, exact identifiers and
  meaning, and every piece records where it came from, when it was true and how much we trust it.
  Humans can already put files in and get markdown out.
- **Why.** Owner decision 3 (fresh format, no legacy constraints), D-A (keep raw), D-B (two
  clocks), D-N (provenance and trust in the first migration). Chunking is the real fix for the
  1,500-rune stopgap. Everything else sits on this schema, so it is designed in writing first.
- **Review checkpoint.** `docs/schema.md` — a layer diagram (L0 sources → L1 documents → L2
  chunks → L3 facts → L4 graph → L5 pages), an entity-relationship sketch, every column in plain
  words, the URI scheme, trust versus origin, the two clocks, what is indexed where, a trust
  transition table per channel — is written and **signed off by the owner before migration 1 is
  coded**. It documents the whole target schema; migration 1 creates L0–L3 plus bookkeeping. This
  is also where the research document's schema sketch is reconciled with its tool surface (see
  §14).
- **What gets built.**
  - `internal/kb/` replaces `internal/journal` (OD-13): `open.go` (the reused DSN pragmas;
    `PRAGMA application_id` set to a memo-mcp constant by migration 1; on open, a file that has
    tables but a different application id is refused with "this is a v1 memo-mcp journal, not a
    knowledge base; nothing is migrated"; read-only commands open with `mode=ro` and never create
    a file), `migrate.go` (scaffold moved verbatim, migrations restart at 1, `user_version` read
    inside the `BEGIN IMMEDIATE` transaction, negative versions rejected), `write.go` (`Ingest`
    and the single mutation point `replaceChunks(ctx, tx, docID, chunks, vecs)`), `verify.go`.
  - **Migration 1** (ids are UUIDv7 via `google/uuid`, already a dependency; chunks use an
    integer primary key):
    `namespaces(name, description, created_at)`;
    `sources(id, namespace, uri NULL for notes, title, kind doc|note|code|conversation, library,
    version, content_hash, etag, fetched_at, ttl_s, trust curated|user|agent, origin
    web|user-said|agent-derived, tags_json, created_at)`;
    `documents(id, source_id, revision, content, context, created_at, updated_at, deleted_at,
    deleted_reason, superseded_by)`;
    `chunks(id INTEGER PK, document_id, ord, section_path, text, context_header, est_tokens,
    lang)`;
    `chunks_fts` (external-content on `chunks`, `porter unicode61 remove_diacritics 2`, kept in
    step by insert/delete/update triggers);
    `chunks_fts_exact` (`unicode61 tokenchars '_.:-/'`, OD-2);
    `chunk_vecs(chunk_id, model_id, embedding BLOB, PRIMARY KEY (chunk_id, model_id))`, queried
    with `vec_distance_cosine` (OD-1, spike S2);
    `models(id, name, hf_repo, hf_revision, dim, max_tokens, query_prefix, doc_prefix,
    normalize, licence, sha256, installed_at, is_default)`;
    `facts(id, namespace, statement, subject_entity_id NULL, about_json, valid_from, valid_to,
    recorded_at, invalidated_at, superseded_by, evidence_chunk_id, trust, origin)`, `facts_fts`,
    `fact_vecs(fact_id, model_id, embedding)`;
    `jobs(id, kind ingest|embed|reindex|compact, scope, state, created_at, updated_at,
    items_json, error)`;
    `audit(ts, actor, channel tool|elicitation|cli|worker, op, target_uri, detail_json)`;
    `query_log(id, ts, args_json, mode, profile, model_id, n_results, top_uris_json, latency_ms,
    trace_json)` (written only when `MEMO_QUERY_LOG=1`).
    L4 (`entities`, `entity_aliases`, `mentions`, `edges`, `merge_candidates`) is migration 2 in
    P7; L5 (`pages`, `page_sources`, `page_vecs`, `work_items`) is migration 3 in P8.
  - Note identity: a note has no URL, so `sources.uri` is nullable for `kind=note`, its address
    is `memo://doc/<id>`, and updating a note is an explicit new revision of the same document
    rather than a hash comparison.
  - URI scheme: `memo://source/<id>`, `memo://doc/<id>`, `memo://chunk/<int>`,
    `memo://fact/<id>`; later `memo://entity/<id>`, `memo://page/<id>`,
    `memo://ns/<name>/index`. Ids are global, so every result also carries its `namespace`.
  - `internal/chunk/`: a structure-aware recursive splitter (headings → paragraphs → sentences →
    hard rune split); target about 200 estimated tokens, hard cap 400 clamped to the active
    model's maximum, one-sentence (about 40-token) overlap (OD-3); fenced code blocks are never
    split; `section_path` is the heading trail; `context_header = title > section path` is
    prepended at embed and index time; optional client-written `documents.context`; a model-free
    token estimator (`asciiWords*1.4 + nonASCIIRunes*1.2 + punctRuns*0.5`); a runtime backstop
    where `Embed` retries with halved text at most twice and returns `ErrInputTooLong` (the
    tokenizer provably does not clamp). `go/parser` for `.go` files; spike S5 decides tree-sitter
    (OD-17).
  - `internal/embedding/`: `Embedder` v2 with `EmbedBatch(ctx, []string)` (hugot's
    `RunPipeline` already takes a slice), `EmbedQuery` versus `EmbedDocument` roles (models may
    use prefixes), and `Info() ModelInfo`; `HashEmbedder` and `FailingEmbedder` implement both;
    the model row is upserted on first use with its pinned revision and sha256; embedding never
    runs while holding the database connection; temp download dirs are swept on start; the lock
    timeout is reachable; hugot's verbose output is never enabled (stdout is the MCP stream).
  - Write path `kb.Ingest`: normalise → `content_hash` → identical hash for the same `uri` and
    `version` is a no-op (`IngestResult.Dedup = true`) → a changed hash is a new `revision` and the
    old row gets `superseded_by` → chunk → FTS via triggers → embed outside the transaction in
    batches of 16 → vectors → `audit`. A vector-store failure is never swallowed: it fails the
    write or enqueues an `embed` job; a backfill worker drains `embed` jobs after start-up;
    `status` shows `pending_embeddings` per model. Trust by channel: tool writes are capped at
    `agent`; CLI writes default to `user`, `--trust curated` is explicit (OD-9).
  - CLI: `ingest <file|dir|-> [--ns --kind --uri --title --version --library --trust]`,
    `read <uri>`, `ls [--ns --kind --since --json]`, `verify [--repair]` (orphan chunks and
    vectors, FTS `integrity-check`, documents with zero chunks, chunks without vectors for the
    default model), `export --md <dir> [--ns]` (`<ns>/<kind>/<slug>-<shortid>.md` with YAML front
    matter: `memo_uri`, `source_uri`, `version`, `content_hash`, `fetched_at`, `trust`, `origin`,
    `namespace`, `revision`; chunk anchors as HTML comments; `_index.md` per namespace; importing
    the export yields zero new revisions).
  - No legacy importer (owner decision 5). The two fixture journal databases are kept only for
    `TestRejectsLegacyJournalFile`. The legacy `internal/journal` and `internal/search` packages
    stay alive until P2's eval is green over the new store.
  - Test helpers open through `kb.Open` on a temporary file, so WAL, `foreign_keys` and the
    single-connection pool are exercised.
- **Concepts you meet.** *Schema and migration* (numbered one-way steps recorded in
  `PRAGMA user_version`) · *application id* (a header integer saying which program owns the
  file) · *namespace* (a labelled shelf inside one file; searches span all shelves, writes go to
  one) · *source, document, chunk* (where it came from; the text as ingested; the indexed piece)
  · *revision* · *content hash* (a fingerprint; same text, same hash) · *provenance* (the chain
  from a record back to its source) · *trust tier versus origin* (what the channel guarantees
  versus what the writer claimed) · *bi-temporal* (two clocks: when it was true, when we learned
  it) · *tombstone and supersede* (hide by marking, never erase) · *external-content FTS table
  and triggers* (the index reads your real table and follows it automatically) · *context
  header* · *token* (the unit a model counts; about three-quarters of a word) · *UUIDv7*
  (time-sortable ids) · *idempotent write* (doing it twice changes nothing).
- **Explainability in this phase.** Every row carries provenance and trust; `ingest` prints what
  happened (N chunks, no-op versus new revision); `status` shows pending embeddings per model;
  `verify` reports integrity; `audit` records every write with its channel; `export --md` front
  matter exposes provenance to anyone with a text editor.
- **Exit criteria.** `docs/schema.md` signed off; a new database has the application id and
  `user_version = 1`; opening either fixture journal gives the clear refusal; a 6,000-character
  markdown file ingests into N chunks each at most 400 estimated tokens, all embedded, with
  `verify` and FTS `integrity-check` clean; identical content twice gives one document with
  `Dedup = true`; changed content gives revision 2 with revision 1 superseded; `export --md` then
  `ingest` of the export yields zero new revisions; the exported folder opens in Obsidian with
  working links; `status` on a missing database name does not create a file; `make check` green;
  `docs/spikes/S5.md` has a decision.
- **Rollback.** Fresh format, nothing in the wild; new packages land additively, old ones are
  deleted in P2.
- **Tag** `v0.5.0`.
- **Learning artifact.** `articles/designing-the-knowledge-schema.md` plus `docs/schema.md`.
- **Effort.** 5–6 days.

### P2 — Search that explains itself · 5–6 days · `v0.6.0`

> **Status (2026-10-02): built.** `internal/retrieve` (semantic, keyword and exact arms over
> the scoped live set, weighted RRF with **equal** arm weights after the H2 derivation, chunk→
> document aggregation by max, per-kind recency clamped at zero, gap cutoff, a semantic-only
> similarity floor so no-match queries abstain, token-budget packing, `exclude_ids`, the `Why`/
> `Trace` explain contract, opt-in query log); the MCP server rewritten to four typed tools
> (`ingest`, `search`, `read`, `status`) with annotations, output schemas, text mirrors and
> honest descriptions; CLI `search | explain | log`; the eval harness ported to the store with the
> H3a/H3b metric fixes, categories, first-hit arms and an abstention rate; the journal and
> search packages deleted. New baseline: recall@10 1.00, MRR 1.00, abstention 1.00
> (`docs/eval/v0.6.0.md`). Remaining before the tag: commit, CI green, `git tag -a v0.6.0`.


- **Story.** (a) search, read and explain; (c) the CLI.
- **Goal.** Find chunks three ways (by words, by exact identifier, by meaning), fuse the lists,
  and be able to say exactly why each result ranked where it did, over MCP with typed results
  and on the command line with the same numbers. Bring the eval back to life over the new store
  and delete the journal code.
- **Why.** Hybrid search at chunk level with a stemmed index and an exact index is the 2026
  consensus (SOTA §3.3, §3.4). Owner decision 4 (a per-result "why" on request plus a CLI
  `explain`), decision 2 (CLI early). Typed structured output and annotations from day one avoid
  a text-then-typed rewrite. The eval must be green at the end of every phase; this phase
  restores it. The current fusion lets keyword-only hits never reach the first page (§4.5), so
  fusion is designed against that case explicitly.
- **What gets built.**
  - `internal/retrieve/`: an `Arm` interface (`semantic` over `chunk_vecs`, `keyword` over
    `chunks_fts`, `exact` over `chunks_fts_exact`; `fact`, `entity` and `graph` arrive later);
    N-arm weighted RRF generalised from `fuseRanks` (`k = 60`, weights 0.6/0.4 carried over as
    the `default` profile's starting values and re-tuned in P3); chunk-level fusion aggregated to
    document by **max**, keeping the best chunk (this retires `generateExcerpt`;
    `truncateAtBoundary` stays); **no union cut before recency**, and per-arm ranks are always
    kept; a shared live filter (`deleted_at IS NULL AND superseded_by IS NULL`, `invalidated_at
    IS NULL`) and every scope filter (`namespaces[]`, `kinds[]`, `sources[]`, `library`,
    `version`, `tags[]`, `date_from`, `date_to`, `min_trust`) applied in SQL **before** top-k;
    recency as the bounded `0.8 + 0.2·0.5^(age/90)` factor applied before the sort, clamped to
    age ≥ 0, on for `note|conversation` and off for `doc|code` in the default profile (OD-4) and
    always reported separately; `relevance` = best raw cosine with bands strong ≥ 0.60 / moderate
    ≥ 0.45 / weak ≥ 0.30 (`null` with band `keyword-only` when the semantic arm did not run);
    a **gap-based autocut** replaces max-normalisation; `mode: auto|hybrid|keyword|exact|
    semantic` (`auto` adds `exact` for identifier-looking queries); `queries[]` fused RAG-Fusion
    style; `granularity: chunk|document`; `exclude_ids`; simple `max_tokens` packing (the full
    packer with footer comes in P5). Numeric knobs are read from a `Profile` struct (constants
    now, named profiles in P3; never in the LLM-facing schema).
  - **The explain contract** in `internal/retrieve/explain.go`, used unchanged by CLI, MCP and
    later the UI. Per result:
    `Why{uri, chunk{uri, ord, section_path, est_tokens}, document{uri, title, revision},
    arms[]{arm, rank|null, raw, raw_kind cosine|bm25, contribution, matched_terms[]}, fused,
    recency_factor, final, rank, relevance, band, provenance{source_uri, version, fetched_at,
    trust, origin, namespace}, freshness{stale, ttl_expired}}`. Per query:
    `Trace{mode_requested, mode_resolved, arms_run[], routing_reason, profile, model_id,
    candidates_per_arm, filtered{by_scope, by_revocation, by_min_trust}, cutoff{kind
    gap|budget|limit|none, position, gap}, budget{max_tokens, used, truncated_count,
    narrow_hint}, latency_ms_per_arm, degraded{flag, reason}, as_of}`. Matched terms come from
    FTS5 `highlight()`. P3 adds `profile`, `model` and `rerank`; P4 adds `time`; P7 adds
    `graph`; P8 adds `is_inference`.

    A worked example (RRF: a result at position r in an arm earns `weight / (60 + r)`; here
    0.6/62 = 0.00968 and 0.4/61 = 0.00656):

    ```json
    {"profile":{"name":"default","rrf_k":60,"weights":{"semantic":0.6,"keyword":0.4,"exact":0.0}},
     "arms":[{"arm":"semantic","rank":2,"raw":0.61,"raw_kind":"cosine","contribution":0.00968},
             {"arm":"keyword","rank":1,"raw":-4.2,"raw_kind":"bm25","contribution":0.00656,
              "matched_terms":["interceptor","auth"]}],
     "fused":0.01624,"recency_factor":1.0,"final":0.01624,"rank":1,"relevance":0.61,"band":"strong",
     "chunk":{"uri":"memo://chunk/812","section_path":"Interceptors > Ordering","ord":7,"est_tokens":188},
     "provenance":{"source_uri":"https://…","version":"v1.8.0","fetched_at":"2026-10-02T09:12:00Z",
                   "trust":"agent","origin":"web","namespace":"grpc-go"},
     "freshness":{"stale":false,"ttl_expired":false}}
    ```

    Structured abstention: `{results: [], degraded, reason: "no arm matched" | "all below gap" |
    "N excluded by scope", hint}`. `degraded` is true whenever the embedder is unavailable.
  - `internal/server/` rewrite: provisional but **typed** tools `ingest`, `search`, `read`,
    `status` (concrete output structs → output schema + structured content + a text mirror;
    `Title`; `ReadOnlyHint`, `IdempotentHint`, `DestructiveHint`, `OpenWorldHint: false`;
    retrieved text wrapped and labelled as data). `search` has exactly the research document's
    eight parameters, with `response_format: concise|detailed|explain` (OD-5): `explain` is
    `detailed` plus `why` per result and `trace` per query. `read(uri, max_tokens?, granularity
    chunk|section|document)` returns a chunk with its neighbours, its section, or the document
    under budget. Enums for `kind`, `mode`, `granularity` and `response_format` live in the
    schema. Descriptions state the real privacy contract (a local plaintext file; tool inputs are
    visible to the MCP host). The six journal tools are removed; the stubbed
    `TestStructuredContentValidates` and `TestAnnotations` activate; the golden is regenerated
    and the diff reviewed.
  - CLI: `search "<q>" [--mode --ns --library --version --limit --format table|json|md
    --explain]` and `explain "<q>" [<uri>]` (a ranking table for all hits, or the full `Why` for
    one), both thin wrappers over `retrieve.Service`; a test asserts that `memo-mcp explain` and
    `search(response_format = explain)` produce identical numbers.
  - Opt-in query log (owner decision 4): `MEMO_QUERY_LOG=1` writes `query_log`; `log tail | show
    <id> | prune`; retention 10k rows or 30 days; stores query text, arguments, result URIs and
    scores, latency and trace, never retrieved chunk text; excluded from export (OD-19).
  - Eval port: `FixtureEntry` becomes `FixtureDoc{Key, Source, Content, AgeDays}`; the 79
    journal fixtures become `kind=note` documents with `## section` headings so chunks have
    structure; the loader calls `kb.Ingest`; the same 29 queries run at document granularity.
    Metric fixes: an absent item on an empty result list is not scored as rank 1; nDCG's ideal
    uses `min(k, |relevant|)`, never the result length; zero-relevant queries get an abstention
    check instead of fixed zeros. Adjusted slices: the `long-entry-*` queries must now hit through
    the **semantic** arm (checked via explain), `stemming-gap-review` grows to `stem-1..3`, the
    `no-match-*` queries assert the structured abstention. New fixtures: a document whose only
    match is lexical and outside the vector top-30, and an unembedded document. The old
    `baseline.json` is archived as `baseline-journal-v0.3.0.json`; the new baseline is recorded;
    the article says plainly that the two are not comparable. Then `internal/journal` and
    `internal/search` are deleted in one commit.
- **Concepts you meet.** *Retrieval arm* · *BM25 and FTS5* (SQLite's keyword index and its
  scoring formula, which rewards rare words) · *stemming (porter)* ("review", "reviewing",
  "reviewer" become one term) · *exact index* (a second keyword index that keeps identifiers
  whole) · *embedding and vector* (text turned into numbers so similar meanings land close) ·
  *cosine similarity* (how aligned two vectors are, from −1 to 1) · *KNN* (the k nearest
  vectors) · *hybrid search* · *RRF* (each arm votes; position r earns `weight / (60 + r)`;
  votes are summed, so no score normalisation is needed) · *small-to-big* (match on a chunk,
  return its section or document) · *recency decay and half-life* · *autocut* (stop at the first
  large score gap rather than a fixed threshold) · *relevance band* · *abstention* (saying
  "nothing good enough" on purpose) · *degraded* · *structured output and output schema* ·
  *tool annotation* · *progressive disclosure* (identifiers first, content on demand) ·
  *pre-top-k filter* (narrow first, rank second, so filters never lose results) · *query log*.
- **Explainability in this phase.** `Why` per result and `Trace` per query on request;
  `relevance` is raw cosine plus a band, never a normalised number; structured abstention with a
  reason and a hint; `degraded` is explicit; the CLI `explain` prints the same numbers; the
  opt-in query log is inspectable with `log tail`.
- **Exit criteria.** `tools/list` shows exactly four tools with titles, annotations and output
  schemas and no description smells; a phrase from the last section of a 2,000-token document is
  found and explain shows a semantic-arm hit; `search --mode exact useCallback` finds the
  identifier; the lexical-only fixture reaches the first page; `response_format = explain`
  carries `why` for every result and the CLI/MCP equality test passes; a no-match query returns
  the structured abstention; `make eval` over the new store is at or above recall@5 0.91 and MRR
  0.89, with the stemming query at recall@5 = 1 and the three long-entry queries hitting through
  the semantic arm; `MEMO_QUERY_LOG=1` writes rows and unset writes none; `verify` is clean; the
  legacy packages are gone; `make check` is green.
- **Rollback.** The store format is unchanged from P1; tool changes are protocol-only and covered
  by the golden file.
- **Tag** `v0.6.0` (milestone M1: a fresh knowledge base you can fill, search and explain).
- **Learning artifact.** `articles/search-that-explains-itself.md` (RRF walked through with a
  real explain table; what provenance buys) plus `docs/eval/v0.6.0.md` (the first report,
  generated by the test harness until P3 ships the subcommand) plus a slides Part 1 refresh (tool
  table, architecture packages, "How Search Works" at chunk level, the storage slide).
- **Effort.** 5–6 days.

### P3 — The lab: eval v2, embedding models, profiles · 6–7 days · `v0.7.0`

> **Status (2026-10-02): built; bake-off numbers in `docs/eval/v0.7.0.md`.** Named profiles
> with written derivations and JSON overrides (`default`, `precise`, `recency`, `code`,
> `minmax`, `keyword-only`, `semantic-only`; `memo-mcp profiles show`); min-max score fusion as
> an alternative to RRF; the model registry (`memo-mcp model ls|smoke|pull|use|redownload`,
> `MEMO_MODEL`) with the hugot loader generalised to any ONNX export (external weight files,
> pinned revisions, Matryoshka truncation) and a pure-Go static model2vec embedder (potion);
> `memo-mcp reindex` with vectors for several models coexisting; the cross-encoder reranker
> (`internal/rerank`, ms-marco-MiniLM) attached by `MEMO_RERANK=1` and used by `precise`; eval
> v2: a second corpus (300-page library namespace, 5-note namespace, two versions of one page,
> tail-only long documents, aged notes), categories, cost columns, the agent-iterating proxy, a
> paired per-query gate, `memo-mcp eval`. Bake-off outcome: **granite-small-r2 is the new
> default** (OD-6), the reranker failed its gate (OD-7), potion is the instant tier, minmax fusion
> is one flag away (+0.03 on the KB corpus, a wash on notes; RRF stays default), per-model
> similarity bands fixed abstention for granite. Remaining before the tag: commit, CI green,
> `git tag -a v0.7.0`.


- **Story.** (d).
- **Goal.** Write down the kinds of questions a knowledge base must answer and build fixtures for
  them; make every tuning constant a named, documented setting; make the embedding model
  swappable and pick the default by numbers on your own corpus; ship `memo-mcp eval` as the
  instrument every later phase is judged with.
- **Why.** Changing only the embedder moved accuracy by 6 points in one 2026 study, more than
  separates whole memory architectures, and memo-mcp runs the model that lost (SOTA §1.5). The
  research document says to run this immediately after chunking and before provenance semantics,
  because it changes every later baseline. Raw weights belong in server-side profiles, not in the
  LLM schema (D-K). Eval v2 gates everything after it. This phase is the project's identity.
- **What gets built.**
  - **Eval v2** in `internal/eval/corpus_kb.go` (keeps the P2 corpus): two namespaces (one with
    about 300 documents, one with 5) to test that a large shelf does not drown a small one; a mini
    library in two versions with the same API and a changed signature; code identifiers and error
    codes; long documents whose distinguishing vocabulary appears only in the tail; fixtures
    spanning two years of ages; planted facts with validity intervals (scored in P4).
    Categories: `lookup`, `paraphrase` (nightly `realmodel` only, since the hash embedder has no
    synonymy), `exact`, `long-tail`, `version-pinned`, `cross-namespace`, `recency`, `abstention`;
    `knowledge-update`, `temporal`, `conflict` and `revocation` are labelled in P4; `multi-hop` is
    planted now and reported honestly low until P7. Metrics: recall@{1,5,10}, MRR, nDCG@10,
    per-category means, an abstention metric (the share of no-match queries that return the
    structured abstention), and cost columns (query p50/p95 ms, tokens returned, write ms per
    document, database MB, model MB). The gate is **paired per query**: fail if any query drops
    more than one rank band or any category mean drops by more than 0.02, not a mean-only check.
    Baselines every later feature must beat: BM25 only, vector only, hybrid default, and a
    deterministic two-round "search, then re-query with the top hit's section title" proxy for
    the "raw chunks plus an agent iterating" baseline (labelled a weak proxy). Hold the embedder
    fixed when comparing architectures.
  - `memo-mcp eval [--corpus fixture|<kb>] [--models a,b] [--profiles p,q] [--modes …]
    [--categories …] [--format table|json|md] [--explain <query_id>] [--explain-failures]
    [--update-baseline]`. `--explain <id>` prints each labelled relevant item's rank per arm (why
    it was missed); `--explain-failures` prints the `Why` block for every miss. `make eval` runs
    the fixture corpus with the hash embedder in CI in under two minutes; a nightly job runs the
    `realmodel` slice.
  - `internal/embedding/registry.go`: `Model{ID, Name, HFRepo, HFRevision, Dim, MaxTokens,
    License, QueryPrefix, DocPrefix, Normalize}`; download, sentinel, lock and recovery
    generalised to any model, with the cache wiped only on verified corruption (hash mismatch or
    parse error) and only under the lock; `memo-mcp model ls | smoke [<id>|--all] | pull <id> |
    use <id>`; `MEMO_MODEL`; vectors for several models coexist in `chunk_vecs`, so switching is
    a resumable `reindex --model <id>` job and switching back is a configuration change
    ("re-embed, don't re-chunk"); the backfill worker embeds outside the database connection.
    Candidates, gated by spike S4: granite-embedding-small-english-r2 (384-d, Apache; the
    provisional favourite because it is a drop-in dimension), granite-embedding-english-r2
    (768-d), granite-97m-multilingual-r2, arctic-embed-m-v2, Qwen3-Embedding-0.6B, MiniLM (the
    incumbent), EmbeddingGemma at 256-d q8 (opt-in, licence shown at download), and
    potion-retrieval-32M as `internal/embedding/static.go` (a lookup table plus mean pooling,
    about 150 lines, no ONNX; the instant tier and fallback).
  - `internal/retrieve/profiles.go`: `default`, `precise` (deeper fetch, optional rerank),
    `recency` (notes), `code` (exact arm weighted up, recency off), `keyword-only` and
    `semantic-only` (ablations). Each is a struct {rrf_k, arm weights, fusion rrf|minmax,
    half-life and weight, fetch depth, cutoff gap, rerank} with defaults in Go **and a
    one-paragraph derivation each**; optional overrides in `$MEMO_HOME/profiles.toml`; `memo-mcp
    profiles show`; `MEMO_PROFILE` for CLI and eval; `profile` is not exposed over MCP in 1.0
    (OD-16).
  - Optional `internal/rerank/`: a cross-encoder over the top 20–40 candidates inside `precise`
    only; ms-marco-MiniLM-L-6-v2 first (same architecture as the embedder, so known to load),
    Ettin-17M/32M if they pass S4; promoted only on the OD-7 gate.
  - Explain gains `profile`, `model_id`, per-arm latency, `rerank{before, after, score}` and
    `fusion`.
- **Concepts you meet.** *Recall@k, MRR, nDCG@10* (did a right answer appear in the top k; how
  high was the first right one; how good is the whole order) · *baseline and tolerance* ·
  *paired comparison* (compare the same query before and after, not just the averages) ·
  *category slice* · *ablation* (turn one part off to see what it contributed) · *cost column* ·
  *embedding model and dimension* · *Matryoshka* (a vector you can truncate and still use) ·
  *quantisation (q8)* · *static embeddings* (a lookup table instead of a network: instant,
  weaker) · *ONNX, hugot, GoMLX* (a portable model file; the Go runner; the pure-Go backend,
  about ten times slower than ONNX Runtime) · *op coverage* · *cross-encoder reranker* (a second
  model that reads query and passage together to rescore the top few) · *profile* · *fusion type
  (RRF versus min-max linear)* · *bake-off* · *p50/p95 latency* · *agent-iterating baseline*.
- **Explainability in this phase.** Every knob is named, derived and printable (`profiles
  show`); eval tables carry cost; `--explain-failures`; `status` shows the active model, its
  dimension, licence and pending vectors; the licence notice at download; `why.profile`,
  `why.model`, `why.rerank`.
- **Exit criteria.** `memo-mcp model smoke --all` prints pass or fail per candidate; `memo-mcp
  eval --models <passing> --profiles default,precise --format md` prints the category × metric ×
  cost matrix, committed as `docs/eval/v0.7.0.md`; a default is chosen by the OD-6 rule, or
  MiniLM stays with the numbers saying why; switching models and back leaves chunk counts
  unchanged; `profiles show` prints every constant with its derivation; baseline v2 is committed;
  the paired gate passes; the CI fixture eval runs under two minutes; the nightly `realmodel` job
  exists.
- **Rollback.** Vectors are keyed by model, so the previous default is one `model use` away;
  profiles are code.
- **Tag** `v0.7.0`.
- **Learning artifact.** `articles/the-embedder-is-the-biggest-lever.md` (bake-off tables,
  GoMLX latency, licence notes) plus `docs/eval/v0.7.0.md` plus a slide "Measuring retrieval".
- **Effort.** 6–7 days.

### P4 — Provenance, trust and time · 4–5 days · `v0.8.0`

> **Status (2026-10-02): built.** `kb.Remember` (add-only, supersession chains), `Forget`
> (tombstones that leave every index and stay hidden under `as_of`; `redact` clears text),
> `SetTrust` (promote/demote, CLI or elicitation only), `History`, `ListFacts --as-of`; a
> trust-transition rule enforced in code: a tool call may retire or supersede only `agent`
> records and never raise trust (`ErrNeedsHuman` carries the CLI command). Retrieval: the fact
> arm ("facts as extra keys"), `granularity=fact` with trust-first conflict ordering, `as_of`
> for documents and facts, stopwords dropped from keyword queries. Tools `remember`, `forget`,
> `promote` (seven in total) and `search.as_of`; CLI `remember | forget | facts ls | trust |
> read --history`. Eval slices: knowledge-update, temporal, fact-key, conflict, revocation,
> write-loss, poisoning (`docs/eval/v0.8.0.md`). Spike S7 (elicitation in a real client) ran
> in P5, where `promote` is wired to it. Remaining: commit, CI, `git tag -a v0.8.0`.


- **Story.** (a) `remember`; (b) correctness of cited answers.
- **Goal.** An agent can record an atomic fact with its evidence, replace it later without losing
  history, ask "what was true on this date", and is never served revoked knowledge by default. A
  human can raise a record's trust through a channel the agent cannot fake, and an agent cannot
  hide trusted knowledge on its own.
- **Why.** Facts beat raw chunks on knowledge-update and temporal questions only with explicit
  time (SOTA §1.2–1.3); invalidate-not-delete is the one write-path idea with consensus (D-B); a
  2026 study found no memory system enforces revocation by default (SOTA §4.3; D-N); any trust
  declared in a tool call is self-reported (SOTA §8.1, decision 6). Because search spans all
  namespaces by default, a poisoned agent must not be able to `forget` or supersede curated
  knowledge; trust therefore governs retiring records too, not only raising trust.
- **What gets built.** No migration (the tables exist since migration 1).
  - `kb.Remember(statement, about[], valid_from, supersedes, evidence_uri, namespace)` is
    add-only; `supersedes` sets `invalidated_at` and `superseded_by` on the old fact.
    `kb.Forget(uri, reason, redact)` tombstones a record: chunks leave every index through
    `replaceChunks`; content is kept for audit unless `redact`. `kb.Promote(uri, to)`. A
    **trust transition table** (in `docs/schema.md`): `forget`, `supersedes` and `promote` on a
    record whose trust is above the caller's channel cap require an accepted elicitation, or
    return the CLI command. **Forgotten records stay hidden even under `as_of`** (forget means
    "never serve this"); superseded and invalidated facts remain visible under `as_of` (that is
    history) (OD-20). `as_of = T` swaps the live filter for `recorded_at <= T AND (invalidated_at
    IS NULL OR invalidated_at > T)`. `read` resolves a tombstone to "forgotten on … because …",
    never "not found"; `read --history` walks revision and supersession chains.
  - A fact arm in RRF (`facts_fts` plus `fact_vecs`); a matched fact is returned **with** its
    evidence chunk.
  - Tools: `remember`, `forget`, `promote` (returns the CLI command until P5 wires elicitation);
    `search` gains `granularity = fact`, `as_of` and `scope.min_trust`. Tool count: seven.
  - CLI: `remember`, `forget`, `facts ls [--as-of]`, `trust ls | promote | demote <uri>` (audited
    with `channel = cli`), `read --history`; `export --md` front matter gains the validity window
    and supersession.
  - Eval slices: knowledge-update (the old/new pair becomes a real supersession chain: default
    returns only the new fact, `as_of` returns the old); temporal; abstention; revocation (a
    forgotten document never appears; the trace reports how many candidates the live filter
    removed); poisoning (a planted web-origin chunk with instruction-shaped text is returned
    wrapped as data and labelled `trust = agent`, and a tool call alone cannot raise trust or
    retire a curated fact); conflict (two facts with different trust); and **write loss measured
    separately from retrieval loss** (a planted fact must be recoverable from its evidence chunk
    even if `remember` was never called). Spike S7 runs now so P5 can plan on its result.
- **Concepts you meet.** *Atomic fact* (one claim in one sentence, with validity dates and an
  evidence chunk) · *bi-temporal* (`valid_from`/`valid_to` are world time; `recorded_at`/
  `invalidated_at` are system time) · *supersession chain* · *tombstone versus redaction* ·
  *revocation as a hard filter* (retired rows are removed before ranking, never merely ranked
  lower) · *`as_of`* ("what did we believe at time T") · *trust tier* (`agent < user <
  curated`, assigned by channel) · *origin* (a hint, not protection) · *channel* ·
  *elicitation* (the server asks the human through the client's UI; the model cannot answer) ·
  *memory poisoning* (tricking the agent into writing bad knowledge) · *audit log* · *write loss
  versus retrieval loss*.
- **Explainability in this phase.** `why.time{valid_from, valid_to, as_of_applied}`,
  `superseded_by`, `trust`, `revocation_filter_applied`; `trace.filtered.by_revocation` and
  `by_min_trust`; tombstones say why; every promotion and every retirement of a trusted record is
  in `audit` with its channel.
- **Exit criteria.** Supersede a fact: the old one is hidden by default, visible with `as_of`,
  and the chain is walkable via `read --history`; `forget` makes a record unretrievable by every
  arm and under `as_of`, `read` returns the tombstone note, `verify` is clean; `trust promote`
  from the CLI succeeds and is audited while a tool call cannot raise trust or forget a curated
  record; the revocation slice scores 100%; poisoning, knowledge-update, temporal and conflict
  slices are labelled and baselined; the write-loss column is present in the report.
- **Rollback.** Code-only phase; the tables already existed.
- **Tag** `v0.8.0`.
- **Learning artifact.** `articles/provenance-trust-and-time.md` (two clocks, why invalidate
  rather than delete, the poisoning threat model in plain words) plus a slide "Two clocks and
  three trust tiers" plus `docs/eval/v0.8.0.md`.
- **Effort.** 4–5 days.

### P5 — The agent surface · 4 days · `v0.9.0`

> **Status (2026-10-02): built.** Seven tool descriptions each carry an example; `search`
> returns the token-budget footer (`truncated`, `narrow_hint`) in every format and the text
> mirror says it; `detailed` shows a passage with its neighbours (small-to-big); `promote` asks
> the human through a multi round-trip elicitation on 2026-07-28 (the first call returns the
> question with excerpt, source, origin and target level; the retry carries the answer; the
> request state binds the answer to that exact promotion) and falls back to the CLI command on
> clients without the capability (spike S7, `docs/spikes/S7-elicitation.md`). Resources mirror
> the read tools (`memo://doc|chunk|source|fact/{id}` templates, `memo://ns/{namespace}/index`,
> static `memo://index`) with `SetCacheable` hints (tools 1 h, resource reads 1 min);
> `export --index` (≤ 8 KB, footer counts omitted lines); `SKILL.md`; `log replay` (logged
> searches as unlabelled eval candidates). Tests: resources list and read, elicitation accept,
> decline and no-capability, budget footer, neighbours, README tool table against the golden.
> Eval unchanged from v0.8.0 (protocol-only phase). Not done: a live check in Claude Code of
> template resolution and the dialog (recorded as open in S7). Remaining: commit, `git tag -a
> v0.9.0`.


- **Story.** (a) and (b) complete at chunk and fact level.
- **Goal.** Give coding agents and chatbots a small, typed, budgeted tool surface with examples,
  that returns citation-ready results, lets the agent dereference anything with one more call,
  asks the human before raising trust, and offers a passive file-shaped face for AGENTS.md.
- **Why.** Tool-selection accuracy collapses as the surface grows (SOTA §3.5); examples in
  descriptions raised accuracy from 72% to 90%; MCP 2026-07-28 is stateless, so "already read"
  travels as `exclude_ids` (D-O); an 8 KB AGENTS.md index scored 100% against 53% for skills in
  one vendor eval (SOTA §4.1; D-M). This phase runs after P3 and P4 so the descriptions describe
  the final parameters.
- **What gets built.**
  - Final descriptions with one or two examples each; the full token-budget packer (results
    packed to `max_tokens`, with a footer such as "12 more results; narrow with
    `scope.version`"); `exclude_ids` honoured; `detailed` returns neighbouring chunks; `promote`
    applies only after an accepted elicitation showing the excerpt, source URI, declared origin
    and target level (spike S7), otherwise it returns the CLI command; a README caveat that a
    user-configured auto-accept hook disables this protection.
  - Resources mirroring the tools: `memo://doc/{id}`, `memo://chunk/{id}`, `memo://fact/{id}`,
    `memo://source/{id}` templates and a static `memo://ns/{namespace}/index` (one-liners per
    document and page, reading `namespaces.description`); `SetCacheable` TTL hints (go-sdk
    v1.8.0); record whether Claude Code resolves resource templates.
  - `export --index [--ns …] [--library x@v] [--max-bytes 8192]`, an index of at most 8 KB for
    AGENTS.md or CLAUDE.md; a `SKILL.md` ("how to use this knowledge base": the search-then-read
    loop, scoping by version, when to `remember`), shipped as a plain file now and through the
    MCP skills extension when the Go SDK supports it; `log replay` turns logged queries into an
    unlabelled eval candidate set. Completions for enum parameters are an optional ~20-line
    item; prompts are not planned.
  - A conformance test for `2026-07-28` on stdio; the golden covers all seven tools;
    `TestStructuredContentValidates` covers every output type; a test asserts that the README tool
    table is generated from `tools.golden.json`.
- **Concepts you meet.** *MCP tool, resource, prompt* (a callable action; a readable address; a
  reusable template) · *token budget* · *`exclude_ids`* · *MRTR* (how 2026-07-28 carries server
  questions to the client) · *`ttlMs` cache hint* · *skill, AGENTS.md, passive context*
  (knowledge the agent sees without deciding to call anything) · *description smell* · *closed
  world (`openWorldHint: false`)*.
- **Explainability in this phase.** `why` and `trace` in structured content on request;
  `degraded` reasons; truncation footers; the promotion dialog shows what is being promoted and
  from where; `audit` shows the channel; `SKILL.md` teaches the agent in text a human can read.
- **Exit criteria.** The golden shows seven annotated tools with output schemas and examples;
  `max_tokens = 500` packs and explains; `exclude_ids` removes results; `read` works for every
  URI kind; resources list and read (template result recorded); the `promote` dialog appears in
  Claude Code and declining leaves trust unchanged; on a client without elicitation the tool
  returns the CLI command; `export --index` of the eval corpus is at most 8 KB; the README
  tool-table test is green.
- **Rollback.** Protocol-only; golden-covered.
- **Tag** `v0.9.0`.
- **Learning artifact.** `articles/designing-tools-for-agents.md` (seven parameters, budgets, why
  structured plus text) plus `SKILL.md` plus a slide "What an agent sees" (the golden as a
  figure).
- **Effort.** 4 days.

### P6 — Ship 1.0 · 2–3 days · `v1.0.0`

> **Status (2026-10-02): built, pending the first tagged run.** CI matrix (ubuntu, macos,
> windows; lint as a separate gate; a snapshot-release job proving the pipeline); nightly
> real-model eval with a cached model directory; `.goreleaser.yaml` (five archives, verified
> locally: `make snapshot` produced darwin/linux × amd64/arm64 and windows/amd64 plus
> `checksums.txt`); `release.yml` publishes on `v*` tags and fills `server.json` with the tag
> and the linux/amd64 checksum before `mcp-publisher login github-oidc && publish`;
> `docs/architecture.md` (the 1.0 contract), `CHANGELOG.md`, README rewrite (install from
> releases, `claude mcp add`, reading list), `ls --json` field names made snake_case. The
> clean-machine scenario ran end to end (`docs/eval/v1.0.0.md`). Not verifiable on this
> machine: the three-OS CI run, `mcp-publisher publish --dry-run` (tool not installed; the
> schema URL and the publisher release URL resolve), the registry's acceptance of the `mcpb`
> package shape. Remaining: commit, push, `git tag -a v1.0.0`, watch the release job, fix
> whatever the registry rejects.


- **Story.** All; a stranger can install it.
- **Goal.** Make the core knowledge base installable on a clean machine, discoverable in the MCP
  registry, and documented so that anyone can read exactly how search decides.
- **Why.** The core (store, hybrid plus exact plus fact search with explanations, the lab,
  provenance/trust/time, the typed surface, CLI and export) is complete and measured. Graph and
  compaction are research layers that are best measured against a shipped baseline and added as
  non-breaking tools (owner decision 7). 1.0 is a contract: tool names and parameters, the URI
  scheme, the explain field names and the export front matter.
- **What gets built.** The CI matrix (ubuntu/macos/windows × Go 1.26.x; gofmt, vet,
  golangci-lint, `-race`, `CGO_ENABLED=0` build; PR CI never downloads a model; nightly
  `realmodel` with a module cache); `.goreleaser.yaml` (darwin/linux × amd64/arm64 plus
  windows/amd64, `-s -w`, version from the tag); `server.json` and `mcp-publisher` through GitHub
  OIDC under the renamed repository (the `mcp-name` marker in the README is the ownership proof);
  `docs/architecture.md` (layers L0–L5, the pipeline, the RRF, cosine, recency and autocut
  formulas, every profile default with its derivation, the explain field reference, the URI
  scheme, current baselines; the successor of the old `RETRIEVAL.md` idea); `CHANGELOG.md`; the
  README rewrite (pitch, tool table generated from the golden, environment table, CLI list,
  privacy and trust caveats, an Obsidian screenshot, install from releases or the registry);
  `.gitignore` gains `/dist/`; `--http` explicitly excluded (OD-11).
- **Concepts you meet.** *CI matrix* · *release artefact and reproducible build* · *MCP registry
  and `server.json`* · *changelog* · *1.0 as a contract*.
- **Explainability in this phase.** `docs/architecture.md` freezes the explain format as part of
  the contract; release notes carry the eval table.
- **Exit criteria.** CI green on three operating systems with zero model downloads in PR jobs;
  `goreleaser release --snapshot` builds five archives; `mcp-publisher publish --dry-run`
  validates; on a clean machine: install, `claude mcp add`, ingest a real docs folder at two
  versions, ask a version-pinned question, read the citation, `forget` it, confirm it is gone,
  `export --md`, grep it; `memo-mcp version` matches the tag; `docs/eval/v1.0.0.md` recorded.
- **Rollback.** The release pipeline is configuration; a bad release is superseded by a patch tag.
- **Tag** `v1.0.0` (milestone M2).
- **Learning artifact.** `articles/shipping-a-pure-go-mcp-server.md` plus `docs/architecture.md`
  plus release notes.
- **Effort.** 2–3 days.

### P7 — Entities and graph, as an index · 6 days · `v1.1.0`

> **Status (2026-10-03): built.** Spike S6 first (`docs/spikes/S6-graph.md`: CSR build 10 ms,
> PPR 42 ms at 10 rounds for 100k nodes / 1M edges, fixed-depth joins beat the recursive CTE).
> Migration 2 (`entities`, `entity_aliases`, `mentions`, `merge_candidates`, `edges`;
> `facts.subject_entity_id` filled). `internal/graph`: rung-1 heuristic extraction, rung-2
> `entities[]`/`relations[]` on ingest and `about[]` on remember, deterministic resolution
> (normalise → exact key → 3-gram MinHash/LSH → Jaccard ≥ 0.8 → entropy gate → number-suffix
> rule → `merge_candidates`), CSR + personalised PageRank. Retrieval: entity arm and graph arm
> (one walk per named entity, product scoring, passages every seed mentions directly left to the
> entity arm), both routed by one rule (two or more known entities, or relational phrasing with
> one) because an always-on entity arm regressed plain lookups ("Postgres"); `mode=graph`;
> `why.graph`, `trace.entities`; ablation profiles `text-only`, `no-graph`. Tool `explore` (eight
> tools), resource `memo://entity/{id}`, `status.graph`; CLI `explore`, `graph merges|merge|reject`.
> Eval: platform multi-hop slice (3 planted chains + 1 version query) and single-hop controls,
> graph-tax column, update-stream test, merge-precision test (2/2 pairs, 0 false). Gate result:
> multi-hop nDCG@10 0.43 text-only → 0.51 entity arm → 0.68 with the graph arm; single-hop slices
> unchanged; so `GraphAuto` is on by default (`docs/eval/v1.1.0.md`). Two side findings fixed
> on the way: the fact arm's weight was missing from the default profile (it voted with weight
> 0), and a one-word keyword overlap could make a fact match (now at least half the content
> words). Not built: rung 3 (GLiNER) and rung 4 (Ollama) extraction, typed edges in ranking
> (stored and shown by `explore`, not scored). Remaining: commit, `git tag -a v1.1.0`.


- **Story.** (a) and (b) for relational and multi-hop questions.
- **Goal.** Recognise the "things" the knowledge talks about (libraries, functions, people,
  concepts), link them to the chunks that mention them, and use those links as one more routed
  search arm, only where the numbers show it helps.
- **Why.** Entity-to-chunk links recover most multi-hop gains at zero LLM cost (D-H, citing
  LazyGraphRAG, KET-RAG and LinearRAG); graph helps multi-hop questions and hurts simple lookup,
  so it is routed, not default (D-E, D-F); extraction is a ladder with no mandatory rung (D-G);
  resolution is deterministic and non-destructive (D-H2); a PPR rerank costs about 15% more
  tokens while community summaries cost a hundred times more (SOTA §2.2). Spike S6 runs first.
- **What gets built.** Migration 2: `entities(id, namespace, canonical, type, summary_page_id)`,
  `entity_aliases(alias, entity_id)`, `mentions(entity_id, chunk_id, weight)`,
  `merge_candidates(a, b, score, reason, state)`, and optional bi-temporal `edges(src, dst, rel,
  weight, valid_from, valid_to, recorded_at, invalidated_at, evidence_chunk_id)`.
  `internal/graph/extract.go`: rung 1 heuristics (capitalised spans, backticked spans,
  dotted/camel/snake identifiers, headings); rung 2 client-supplied `entities[]` and
  `relations[]` on `ingest` and `about[]` on `remember`; rung 3 GLiNER through ONNX only after a
  prototype proves it loads; rung 4 Ollama opt-in. `resolve.go`: normalise → exact match → 3-gram
  MinHash/LSH at Jaccard ≥ 0.9 → an entropy gate → `merge_candidates`; merges keep the canonical
  name plus aliases. `internal/retrieve/arm_entity.go` (match the query against canonical names
  and aliases, expand through `mentions`, feed ranks into RRF; ships first and needs no edges)
  and `arm_graph.go` (a lazily built per-namespace CSR adjacency, a ~20-line personalised
  PageRank seeded by matched entities and the top hybrid chunks, a hub-degree cap, fixed-depth
  joins, no recursive CTEs); `mode = graph`; `auto` adds the graph arm when the query names two
  or more known entities or uses relational phrasing. Tool `explore(entity, hops = 1, as_of?)`
  (tool eight); `memo-mcp explore`; `memo-mcp graph merges`; `memo://entity/{id}`;
  `facts.subject_entity_id` linked. Eval: a multi-hop slice; an update-stream test (index half the
  corpus, add the rest in batches, old queries must not regress); merge precision at least 0.95 on
  planted alias pairs; a "graph tax" cost column. The PPR arm is on by default in `auto` only if
  it beats the entity arm by a pre-registered margin; typed edges ship only if they beat the
  entity arm.
- **Concepts you meet.** *Entity, alias, canonical name* · *mention edge* (an entity-to-chunk
  link with no relation type) · *entity resolution* (deciding two names are one thing) ·
  *MinHash and LSH* (a fast approximate "are these strings nearly the same") · *entropy gate*
  (short or ambiguous names go to a review queue) · *graph-as-index* (structure used to find
  text, not to replace it) · *personalised PageRank* (a random walker who keeps returning to the
  query's entities; where it spends time is relevant) · *CSR* (a compact in-memory graph layout)
  · *hub penalty* · *multi-hop question* · *routing* · *update-stream test*.
- **Explainability in this phase.** `why.graph{seeds[], ppr_score, path, hub_penalised}`;
  `trace.routing_reason` ("graph arm added: 2 known entities"); `explore` returns evidence
  chunks per neighbour; `merge_candidates` records the reason for each decision; the graph tax
  is in the eval report.
- **Exit criteria.** The entity arm improves multi-hop recall without a single-hop regression
  beyond tolerance; the PPR arm is on by default only if it beats the entity arm by the
  pre-registered margin (otherwise documented and off); `explore --as-of` works; the
  update-stream test passes; merge precision is at least 0.95 on the labelled set; the S6 latency
  budget is met; `docs/eval/v1.1.0.md`.
- **Rollback.** Additive tables; `mode = graph` and the `auto` rule are profile flags.
- **Tag** `v1.1.0`.
- **Learning artifact.** `articles/graph-as-an-index-not-an-oracle.md` (where the graph won, what
  it cost) plus the slides Part 2 rewrite plus `docs/eval/v1.1.0.md`.
- **Effort.** 6 days.

### P8 — Compaction and pages · 5 days · `v1.2.0`

> **Status (2026-10-03): built.** Migration 3 (`pages`, `page_sources`, `page_vecs`, `pages_fts`,
> `work_items`). `internal/compact`: generators (page when an entity has ≥ 3 live passages and
> no page; stale; the recurrence trigger re-proposes only after ≥ 2 new passages; conflict =
> two live facts about one subject with overlapping validity and different statements, with the
> trust-then-recency rule stated; merge = open resolver candidates; duplicate = word 3-gram
> Jaccard ≥ 0.75 across documents), `Submit` (page with sources and `is_inference`, conflict
> resolution that invalidates under the trust rule, merge decision; line diff; omission check
> at 75% content-word coverage; corruption check for sentences unsupported by any source),
> `Lint` (five finding kinds with addresses), optional Ollama executor used only by the CLI.
> Staleness on revision and on forget. Tools `compact`, `submit` (ten in total),
> `granularity=page`, `memo://page/{id}`, `status.pages`; CLI `compact | submit | lint | pages
> ls`, export writes `<ns>/pages/`. Eval (`docs/eval/v1.2.0.md`): lint recall 5/5 planted
> kinds, omission and corruption checks flag exactly the planted gaps, conflict rule enforced
> against the tool channel, raw source/document/chunk rows byte-identical across the whole
> loop. Not built: Louvain topic pages (the eval has no global questions, so the rule says no),
> topic and overview page kinds beyond the schema. Not verified: the Ollama executor against a
> live model (none on this machine). Remaining: commit, `git tag -a v1.2.0` (milestone M3).


- **Story.** (b) pages; (a) tidy knowledge.
- **Goal.** Turn piles of chunks into curated pages and resolved conflicts, written by the
  calling agent and never by the server, with every page citing the chunks it came from and
  going stale when they change. This completes the research document's second goal.
- **Why.** Compaction gives the cost and knowledge-update wins of extracted memory (SOTA §1.3)
  while D-A keeps the raw material; derived records must be flagged, versioned and sourced
  (SOTA §1.5 safety rails); omission is the dominant consolidation error, so lint and an omission
  check are built in; recurrence-triggered work bounds cost; the client-LLM pattern keeps the
  server LLM-free (D-C, D-D, D-L).
- **What gets built.** Migration 3: `pages(id, kind entity|topic|overview, subject_id,
  namespace, content, built_at, built_from_rev, stale, is_inference)`, `page_sources(page_id,
  chunk_id)`, `page_vecs`, `work_items(id, job_id, kind page|conflict|merge|stale|lint, scope,
  payload_json, state, result_json)`. `internal/compact/`: work-item generators (near-duplicate
  chunks via MinHash, conflicting facts by trust and overlapping validity, merge candidates, "N
  chunks about X: write or update its page", stale pages after a source revision), a recurrence
  trigger (only clusters that keep growing), a `dry_run` diff, a staleness sweep (a changed
  source hash marks dependent pages `stale = 1`), and `lint` (contradictions, orphan entities,
  missing pages for entities with many mentions, facts past `valid_to`). Tools `compact(scope,
  kind)` and `submit(item_id, result, dry_run?)` (tools nine and ten; results stored with
  `is_inference = 1` and `page_sources`); `search granularity = page`; CLI `compact | submit |
  lint`; an optional `--executor ollama` (`MEMO_OLLAMA_URL`, loopback by default, a non-thinking
  model with constrained JSON) with no code path depending on it; Louvain topics via gonum only
  if the eval gains global questions. Export gains `<ns>/pages/`. Eval: an omission and
  corruption check (planted facts in chunks must appear in the submitted page, checked at string
  or fact level, no LLM judge); lint recall on planted contradictions, orphans and stale pages;
  raw rows checksummed unchanged.
- **Concepts you meet.** *Compaction* · *work item* · *page* (curated markdown with backlinks)
  · *`is_inference`* (derived by a model, not taken from a source) · *staleness* · *lint* ·
  *conflict resolution* (trust and time decide; the loser is invalidated, not deleted) ·
  *dry-run diff* · *recurrence trigger* · *sleep-time compute* (doing derivation work when idle
  and reusing it at query time) · *Ollama* (a local model runner; optional here).
- **Explainability in this phase.** Every page shows its `built_from` chunks and revision;
  results carry `is_inference` and `stale`; `lint` reports with URIs; `compact --dry-run` prints
  the proposed diff; a human can read every page the agent wrote.
- **Exit criteria.** `compact` then `submit` through Claude Code creates a page with
  `page_sources` and `is_inference = 1`; re-ingesting a changed source marks dependent pages
  stale and `lint` lists them; lint finds every planted defect; the omission check passes;
  conflict resolution invalidates the lower-trust fact and records why; raw rows are unchanged
  (checksum test); the Ollama executor works when configured and is absent from every code path
  when not; `docs/eval/v1.2.0.md`.
- **Rollback.** Additive tables; tools nine and ten can be unregistered.
- **Tag** `v1.2.0` (milestone M3: the full research vision).
- **Learning artifact.** `articles/compaction-without-a-server-llm.md` plus a slide "The agent
  writes the wiki" plus `docs/eval/v1.2.0.md`.
- **Effort.** 5 days.

### P9 — A knowledge base you can read (optional) · 3 days · `v1.3.0`

> **Status (2026-10-03): built.** `internal/ui`: `memo-mcp ui [--addr 127.0.0.1:0 --no-model
> --allow-remote]` prints the URL; stdlib `net/http`, `html/template`, `embed.FS`, no
> JavaScript; GET-only routes `/`, `/search`, `/doc/{id}`, `/chunk/{id}`, `/source/{id}`,
> `/facts?as_of=&history=`, `/fact/{id}`, `/entity/{id}?hops=`, `/page/{id}`, `/pages`,
> `/status`, `/log`, `/lint`, `/eval`; a Host-header allowlist (the bound address and its
> loopback spellings) and a refusal to bind non-loopback without `--allow-remote`; a CSP that
> allows only the page's own stylesheet. The search page renders the same `Why`/`Trace`
> structs as an explain table and a trace table; `TestSearchNumbersEqualTheService` asserts
> the UI's addresses and scores equal the service's for the same query, down to the rendered
> digits. Live smoke on the P6 demo file found two product gaps, both fixed: a read-only open
> of a file at an older schema now fails with "run `memo-mcp migrate`" (new command) instead
> of a missing-table error, and `memo-mcp graph rebuild` re-extracts mentions for files
> written before the graph layer. Not built: `export --html` (the optional static site;
> `export --md` plus the UI cover the human face), screenshots in the README (none taken in
> this session; the README describes the pages). Remaining: commit, `git tag -a v1.3.0`.


- **Story.** (c).
- **Goal.** A browser window onto the same store for people who do not live in a terminal:
  search with the explain table, a document and chunk viewer with provenance and revision chain,
  a facts timeline, an entity neighbourhood, pages with their sources, status, the query log if
  enabled, and the eval report. Read-only.
- **Why.** Owner decision 2 (later, optional). The explain breakdown is far more legible as a
  table than as JSON. A web server is the easiest place to make security mistakes, so it comes
  last, over a stabilised read API. It can be pulled forward any time after P5.
- **What gets built.** `internal/ui/`: `memo-mcp ui [--addr 127.0.0.1:0]` prints the URL; the
  standard library's `net/http`, `html/template` and `embed.FS`, no JavaScript build; GET-only
  routes `/search`, `/doc/{id}`, `/chunk/{id}`, `/source/{id}`, `/facts?as_of=`,
  `/entity/{id}`, `/page/{id}`, `/status`, `/log`, `/eval`; a `Host`-header allowlist (a
  DNS-rebinding defence); refuses non-loopback addresses without `--allow-remote`; reuses
  `retrieve.Service` and the `Why`/`Trace` structs unchanged; an optional `export --html` static
  site.
- **Concepts you meet.** *Loopback binding* (listening only on this machine's own address) ·
  *DNS rebinding* (why the Host header is checked) · *server-rendered HTML* · *static export*.
- **Explainability in this phase.** The same explain panel for humans; a test asserts the UI's
  numbers equal the CLI's for the same query.
- **Exit criteria.** Serves on loopback only; no mutating route exists; the explain panel
  renders for a search; provenance, the facts timeline and pages render; the equality test is
  green.
- **Rollback.** A separate package and subcommand; delete it.
- **Tag** `v1.3.0`.
- **Learning artifact.** `articles/a-knowledge-base-you-can-read.md` plus screenshots in the
  README and one slide.
- **Effort.** 3 days.

---

### P10 — Observability · 3 days · `v1.4.0`

> **Status (2026-10-03): built.** `internal/obs` (registry with counters, gauges, labelled
> histograms; Prometheus text 0.0.4 with a golden; `runtime/metrics` subset; loopback-only
> metrics server with Host check; `SetupLogging` from `MEMO_LOG_FORMAT`/`MEMO_LOG_LEVEL`). MCP
> receiving middleware counts every request, times and classifies every tool call, writes one
> log line per call; typed-handler wrapper sees the Go error; elicitation outcomes counted;
> `logging` capability no longer advertised (D-O resolved). Retrieval `finish()` records every
> search (mode, arms, candidates, cutoff, degraded, abstention reason) and logs one line;
> graph-cache hits and builds counted. Store counts every audited write by op and channel, plus
> ingest outcomes, chunks, vectors, embed batches, jobs, mentions, stale pages, work items; a
> Status collector exports the table counts as `memo_kb_*` gauges. Embedders are wrapped by
> `Load` (latency by model and role, downloads, load time). Migration 4 `call_log`; `memo-mcp
> log calls|tail|prune`; UI `/log` with two tables and `/metrics`. `serve --metrics-addr` /
> `MEMO_METRICS_ADDR`; `memo-mcp metrics [--json --since]`. Every stderr print replaced by
> `slog`; a test proves a tool round trip writes nothing to stdout. Retrieval unchanged
> (`docs/eval/v1.4.0.md`). Not built: OpenTelemetry export (names are bridgeable), traces.
> Remaining: commit, `git tag -a v1.4.0`.

- **Story.** (d) the researcher measures the server itself; (a) the agent's operator sees what
  it costs.
- **Goal.** Answer "what is the server doing and how long does it take" from the machine it
  runs on, with nothing sent anywhere: a scrape endpoint on loopback, structured logs on stderr,
  and a per-call log in the file for after-the-fact questions.
- **Why.** The eval measures retrieval quality on fixtures; nothing measured the running system.
  Diagnostics were ad-hoc stderr prints; the server advertised an MCP `logging` capability it
  never used and that the protocol deprecates. The project's promises (pure Go, no telemetry,
  read every line) rule out the usual dependencies, so the registry is small and in-house.
- **What gets built.** `internal/obs`; MCP middleware and typed wrapper; `finish()` in
  retrieval; store counters and the Status collector; the instrumented embedder; migration 4
  `call_log`; `serve --metrics-addr`; UI `/metrics`; `memo-mcp metrics`; `slog` everywhere;
  explicit server capabilities.
- **Concepts you meet.** *counter, gauge, histogram* · *exposition format* (the text a scraper
  reads) · *pull versus push* (why nothing is sent) · *cardinality* (why labels are bounded
  enums) · *structured logging* · *DNS rebinding* (why the Host header is checked).
- **Explainability in this phase.** Every call leaves a row and a line; every search's trace
  becomes numbers a dashboard can chart; the per-process caveat is printed where it matters.
- **Exit criteria.** `make check` green; `curl` on `/metrics` returns HELP/TYPE lines and
  `memo_build_info`; POST gives 405 and a foreign Host 403; `initialize` carries no `logging`;
  a tool round trip writes nothing to stdout; `memo-mcp log calls` shows rows; `memo-mcp
  metrics --json` parses; the golden tool list is unchanged.
- **Rollback.** `--metrics-addr` is off by default; the migration is additive; logs are stderr.
- **Tag** `v1.4.0`.
- **Learning artifact.** `articles/measuring-the-server-itself.md` plus `docs/eval/v1.4.0.md`.
- **Effort.** 3 days.

---

## 8. Sequencing, milestones, and what to cut

```
v0.3.0 (retro tag on 73fb9d4)
  |
P0 reset --> P1 store --> P2 search+explain --> P3 lab --> P4 trust/time --> P5 agent surface --> P6 SHIP v1.0.0
(S1-S4)     (S5, schema      |                              (S7)                                      |
             sign-off)       '-- minimal CI protects everything from P0 onward ----------------------'
                                                                                                      v
                                                        P7 entities+graph (S6) --> P8 compaction --> P9 UI (optional)
                                                             v1.1.0                   v1.2.0           v1.3.0
```

**Hard gates.** S2 and S3 before the P1 schema; `docs/schema.md` sign-off before migration 1; P1
before P2; P2 before P3 (the eval needs search); P3 before P4 (the bake-off changes the baseline);
P4 before P5 (output schemas must describe the final result fields); P5 before P6; S6 before P7's
PPR arm; P7 before P8 (pages are per entity or topic); P9 needs only P5's read API. Every phase
ends with `make eval` showing no regression against the recorded baseline plus at least one new
labelled slice, and every tag is a usable product, so the plan can stop after any phase.

**Parallel work with two people.** P5 (server only) alongside P4 (store and retrieve); P9 any
time after P5.

**Milestones.** M1 `v0.6.0` (P0–P2, about 3 weeks): a fresh knowledge base you can fill, search
and explain. M2 `v1.0.0` (P3–P6, about 3.5 weeks): measured, trusted, agent-ready, shipped. M3
`v1.2.0` (P7–P8, about 2.5 weeks): research layers, each proven in eval. M4 `v1.3.0` (P9,
optional).

**If forced to cut, in this order:** the P9 UI; the Ollama executor; Louvain topics; typed edges
(graph rung 2); the reranker; tree-sitter. Never cut P0–P6; everything there compounds.

---

## 9. Decisions

### 9.1 Taken by the research document (binding; cite, do not re-argue)

| Decision | Reference in `knowledge-base-sota.md` |
|---|---|
| Keep raw sources; every derived record cites raw chunks | D-A, §1.5, §6.1 |
| Bi-temporal validity; invalidate, never delete | D-B |
| No server-side LLM; the client agent is the LLM; local Ollama opt-in only; no sampling | D-C, D-L, §8 decision 3, §4.4 |
| Consolidation is explicit and asynchronous; outputs are marked as inferences | D-D |
| Graph-as-index; the graph is one routed RRF arm, not the default | D-E, D-F |
| Extraction ladder, none mandatory; mention edges first; deterministic resolution with a merge queue | D-G, D-H, D-H2 |
| `models` table; permissively licensed default; EmbeddingGemma opt-in with a licence notice | D-I, §8 decision 4 |
| Reranker only on measured nDCG@10 gain, inside `precise`; weighted RRF default; min-max fusion as a profile; gap-based cutoff | D-J, D-J2 |
| About seven defaulted parameters per tool; numeric knobs in server-side profiles; ten tools at most | D-K, §6.3 |
| Two faces: typed tools plus a file-shaped face; `export --index` of at most 8 KB | D-M, §6.5 |
| Provenance and trust in the first migration; revocation is a hard pre-ranking filter | D-N |
| go-sdk v1.8.0, spec 2026-07-28, stateless; `exclude_ids` and job handles travel as arguments; the deprecated `logging` capability is not advertised, diagnostics go to stderr (resolved in P10) | D-O |
| Observability is local only: a stdlib metrics registry in the Prometheus text format on loopback, `slog` on stderr, an opt-in call log in the file; no OpenTelemetry dependency, names bridgeable (owner, 2026-10-03) | D-P |
| The server never fetches URLs; `ingest` takes content | §8 decision 2 |
| Rename journal to knowledge; the tool surface of §6.3; `MEMO_KB` selects the file | §8 decision 1 |
| `scope.namespaces` defaults to all; every result carries `namespace`; writes target one namespace | §8 decision 5 |
| Trust by channel: tool writes capped at `agent`; `promote` through elicitation or CLI; audited; search does not filter by trust by default | §8 decision 6, §8.1 |
| Pure Go, `CGO_ENABLED=0`, no cloud; labelled recall/nDCG plus cost, never LLM-judge win rates | §7 "what not to build" |

### 9.2 Taken by the owner (2026-10-02; binding)

1. **This file.** `review-roadmap.md` was rewritten in place as `roadmap.md`; the review is
   summarised in §4 and kept in git history.
2. **Human access.** Markdown export and CLI commands early (P1, P2); an optional read-only
   local web UI later (P9).
3. **Build from scratch.** Nothing to migrate or re-index: a fresh database format in this
   repository and binary, designed from the research document's schema and tool surface without
   backward-compatibility constraints; the eval harness, fake embedder, migration scaffold, hugot
   pipeline and SQLite hygiene are reused; phases are ordered for the best end result.
4. **Explainability.** A per-result "why ranked" on request plus a CLI `explain` command from the
   first search phase; a persistent query log is opt-in only (`MEMO_QUERY_LOG=1`), stored in the
   same local file.
5. **Fresh schema and tools, keep the engine.** The six journal tools are retired outright, not
   kept behind a flag (this overrides the research document's `MEMO_LEGACY_TOOLS=1` grace period
   and its "default path moves only if the old one doesn't exist"); there is no importer for old
   journal files, which the new binary refuses to open. New files live under `$MEMO_HOME/kb/`.
6. **Learning artifacts.** Every phase ends with an article in `articles/` and a before/after
   eval table; milestones also update the slides.
7. **1.0 after the agent surface** (P6). Graph (`v1.1.0`) and compaction (`v1.2.0`) are additive
   releases. This is the one deliberate departure from the research document's "ship last" order.
8. **One roadmap file** with a table of contents and overview table; the glossary stays inside.
9. **Repository.** The public repository is `github.com/kKEo/memory-find`; the binary stays
   `memo-mcp`. Module path, import paths, clone commands and the `mcp-name` marker follow (P0).
10. **Documentation hygiene done with this file:** a README truth pass, a `docs/README.md` index
    and an errata header on the measurement article. Content fixes inside the research document
    wait for the schema reconciliation in P1 (§14).

### 9.3 Open decisions

Each has a plain-words question, the options, and a recommendation. The text above assumes the
recommendation; if a different option is chosen, the affected phase is named.

- **OD-1 Where do vectors live?** *Resolved by spike S2 (2026-10-02): plain table; vec0 was only 1.5–1.8× faster.* A vector is a list of numbers per chunk; search finds the
  closest ones. *A:* sqlite-vec's `vec0` virtual table (today's choice): a purpose-built layout,
  but no foreign keys, awkward filters, one fixed dimension per table, and a sharp edge we already
  hit. *B:* an ordinary table compared with `vec_distance_cosine()` in plain SQL: it scans every
  row (so does vec0, whose KNN is exhaustive), so it is a constant factor slower (single-digit
  milliseconds at 10k chunks), but every SQL filter works and any model dimension fits.
  **Recommend B** unless spike S2 shows vec0 more than 3× faster at 50k chunks *and* pushdown
  works. The `Arm` interface leaves room for a vec0 adapter above about 100k chunks. (P1)
- **OD-2 Which index for exact identifiers?** *Resolved by spike S3 (2026-10-02): option A; trigram confirmed available as the fallback.* Stemming mangles symbols, so identifiers need their
  own index. *A:* `unicode61` with `tokenchars '_.:-/'` (keeps `net/http` and `useCallback`
  whole; small). *B:* `trigram` (also finds substrings such as `Callback` inside `useCallback`;
  about 3× the index size; queries need at least three characters). **Recommend A** in P1; adopt B
  only if the P3 exact and version-pinned slices show that substring queries matter.
- **OD-3 How big is a chunk?** Smaller chunks match precisely but lose context; bigger ones blur.
  **Recommend** a target of about 200 estimated tokens, a hard cap of 400 clamped to the active
  model's maximum, a one-sentence (about 40-token) overlap, never splitting a code fence; tune as
  a measured knob in P3.
- **OD-4 Should newer things rank higher?** Recency helps notes and conversations and hurts
  versioned docs. **Recommend** recency on for `note|conversation`, off for `doc|code`, in the
  `default` profile; the factor is always shown in `why.recency_factor`.
- **OD-5 How does an agent ask for the explanation?** *A:* always include it (costs tokens on
  every call). *B:* a ninth `search` parameter (the research document's `search` already has
  eight). *C:* a separate `explain` tool (raises the tool count). *D:* a third `response_format`
  value. **Recommend D: `response_format: concise|detailed|explain`**; the CLI `explain` command
  uses the same path. One word everywhere.
- **OD-6 Which embedding model is the default?** *Resolved by the owner on 2026-10-02 from the P3 bake-off: **granite-small-r2** (Apache-2.0, +0.05 nDCG@10 on the knowledge-base corpus, the only candidate that answers paraphrase queries, 1.6× MiniLM query latency, 2.5× ingestion cost; see `docs/eval/v0.7.0.md`). The rule itself was recalibrated: no ONNX model meets 150 ms on the pure-Go backend, so the latency bar is relative to MiniLM.* It cannot be decided until spike S4 and P3.
  **Recommend a rule, not a name:** Apache or MIT licence, loads under GoMLX, beats MiniLM on eval
  v2 nDCG@10, p50 at most 150 ms for 256 tokens on the development laptop; otherwise MiniLM stays
  and potion is the instant tier. Provisional favourite: granite-embedding-small-english-r2
  (384-d per its model card; the research document flags the figure as assumed). EmbeddingGemma
  opt-in only.
- **OD-7 When does the reranker ship?** *Resolved 2026-10-02: it does not. ms-marco-MiniLM through hugot loses ~0.2 nDCG and costs 0.7–4 s per query because the pipeline cannot pass sentence-pair segment ids (`docs/spikes/S8-reranker.md`). It stays opt-in behind `MEMO_RERANK=1`.* A reranker re-scores the top few candidates with a
  slower, more accurate model. **Recommend** inside `precise` only, promoted only if nDCG@10
  gains at least 0.02 on eval v2 at p50 under 300 ms for a 30-pair rerank under GoMLX; otherwise
  `precise` ships without it and the article says why.
- **OD-8 Where does 1.0 fall?** Resolved by owner decision 7: after the agent surface.
- **OD-9 Default trust for CLI writes?** *A:* `user`, with `--trust curated` explicit. *B:*
  `curated` by default for anything a human runs. **Recommend A**: "curated" should always be a
  deliberate word.
- **OD-10 Import old journal files?** Resolved by owner decision 5: no importer.
- **OD-11 HTTP transport?** It turns a local file into a network service. **Recommend deferring
  past 1.0** (then `Stateless = true`, loopback by default, a separate `MEMO_HTTP_TOKEN`);
  revisit with P9.
- **OD-12 Retro-tag the past?** **Recommend yes:** an annotated `v0.3.0` on `73fb9d4`, so history
  has an anchor and versions match tags from P0 on.
- **OD-13 Package layout?** *A:* one `internal/kb` with many files. *B:* one package per concept
  (`kb`, `chunk`, `embedding`, `retrieve`, `eval`, `server`, `cli`, `export`, `graph`,
  `compact`, `ui`). **Recommend B**: one phase is roughly one package a beginner can read end to
  end.
- **OD-14 CLI framework?** *A:* the standard library's `flag` with about 60 lines of subcommand
  dispatch. *B:* cobra. **Recommend A**: a smaller binary and nothing to learn.
- **OD-15 One vector table or one per kind?** *A:* a polymorphic `vecs(kind, ref_id, model_id,
  embedding)` (one reindex path; no foreign key possible). *B:* `chunk_vecs`, `fact_vecs`,
  `page_vecs`, each with a real foreign key and one shared Go helper. **Recommend B**.
- **OD-16 Expose `profile` to the LLM?** **Recommend no** in 1.0 (fewer parameters, better tool
  use); CLI and eval always; revisit if the eval shows per-request value.
- **OD-17 Tree-sitter for code chunking?** **Recommend no** until spike S5 passes; heading- and
  fence-aware splitting plus `go/parser` for Go until then.
- **OD-18 A `namespaces` table?** A column only, or a column plus a small table with a
  `description`. **Recommend both**: the per-namespace index resource needs a description to
  show.
- **OD-19 Query-log retention?** **Recommend** opt-in only, 10k rows or 30 days, `log prune`;
  stores query, arguments, result URIs, scores and trace, never retrieved chunk text; excluded
  from export.
- **OD-20 What can `as_of` see?** *A:* forgotten records are hidden from `as_of` too; only
  superseded and invalidated facts are visible as history. *B:* `as_of` shows non-redacted
  forgotten content. *C:* redaction always removes content. **Recommend A plus C as the meaning
  of `redact`**: forget means "never serve this", history means "what we believed then".
- **OD-21 Trust transitions.** Which operations on a record above the caller's channel cap need a
  human: `promote` always; `forget` and `supersedes` on `user` or `curated` records
  **recommended yes** (elicitation or the CLI); `demote` is a CLI-only operation. The table is
  written in `docs/schema.md` and tested in P4.

---

## 10. Explainability and transparency, phase by phase

| Phase | What becomes visible | Where |
|---|---|---|
| P0 | The real version and protocol version; each spike's number and decision; the SDK golden diff | `memo-mcp version`, `docs/spikes/`, the pull request |
| P1 | Provenance and trust on every row; what `ingest` did; pending embeddings per model; integrity; every write with its channel | `ingest` output, `status`, `verify`, `audit`, export front matter |
| P2 | Per-result `Why` and per-query `Trace` on request; raw relevance with a band; structured abstention with a reason; `degraded`; the opt-in query log | `search(response_format = explain)`, `memo-mcp explain`, `log tail` |
| P3 | Every tuning constant named, derived and printable; eval tables with cost; why each miss was missed; the active model and licence | `profiles show`, `memo-mcp eval --explain-failures`, `status` |
| P4 | Validity windows, supersession, the revocation filter's effect, trust and origin; tombstones that say why; audited trust changes | `why.time`, `trace.filtered`, `read --history`, `audit` |
| P5 | Truncation footers; the promotion dialog showing what and from where; a human-readable `SKILL.md` | structured content, elicitation, `SKILL.md` |
| P6 | The frozen explain format and every formula and default with its derivation | `docs/architecture.md`, release notes |
| P7 | Graph seeds, PPR scores and paths; the routing reason; merge decisions with reasons; the graph tax | `why.graph`, `trace.routing_reason`, `graph merges`, eval |
| P8 | Which chunks a page was built from; `is_inference` and `stale`; lint reports; dry-run diffs | pages, `lint`, `compact --dry-run` |
| P9 | The same explain panel for humans | `memo-mcp ui` |

Closing rule: CLI, MCP and the UI render the same `Why` and `Trace` Go structs, and tests assert
equality (P2: CLI equals MCP; P9: UI equals CLI). The field shapes are specified in P2; P3, P4,
P7 and P8 only add fields.

---

## 11. The human face: where it lands

| Phase | For a person at a terminal or in an editor |
|---|---|
| P0 | `memo-mcp version`, `memo-mcp status` |
| P1 | `ingest`, `read`, `ls`, `verify`, `export --md` (front-matter provenance; greppable; opens in Obsidian) |
| P2 | `search`, `explain`, `log tail | show | prune` |
| P3 | `eval`, `model ls | smoke | pull | use`, `profiles show`, `reindex` |
| P4 | `remember`, `forget`, `facts ls --as-of`, `trust ls | promote | demote`, `read --history` |
| P5 | `export --index`, `SKILL.md`, `log replay`, the per-namespace index resource |
| P6 | `docs/architecture.md`; a README written for both audiences |
| P7 | `explore`, `graph merges` |
| P8 | `compact --dry-run`, `submit`, `lint`; pages in the export |
| P9 | `memo-mcp ui` |

---

## 12. Verification

**Every phase.** `make check` (fmt, vet, lint, race) clean; `make eval` with no category below
its baseline minus 0.02 and no query dropping more than one rank band (or a deliberate, documented
`--update-baseline`); at least one new labelled slice; the `tools.golden.json` diff reviewed;
`memo-mcp verify` clean on the eval database; a `CGO_ENABLED=0` build; PR CI downloads no model;
a tag. Each phase's exit criteria are phrased as commands or tests above.

**Regression tests prove themselves.** Every test written for a defect is shown failing on the
pre-fix commit (a worktree check recorded in the pull request). Phase 1's tests landed in the same
commit as their fixes, and two of them probably pass on the old code; this rule prevents a repeat.

**End to end at `v1.0.0`.** On a clean machine: install from a release; `claude mcp add`; ingest
a real docs folder at two versions; ask a version-pinned question and read the citation;
`remember` a fact, supersede it, query with `as_of`; `forget` a document and confirm it is gone
from every arm; `export --md` and grep it; open the folder in Obsidian.

**End to end at `v1.2.0`.** Additionally `explore` an entity and run `compact` then `submit`
through Claude Code, then `lint`.

---

## 13. What not to build

One reason each.

- Server-side URL fetching (decision 2: the client fetches; the no-network promise stands).
- Any required server-side LLM, or MCP sampling (D-C, D-L).
- GraphRAG-style community summaries at index time (ten to a thousand times the cost, and they
  update poorly).
- Semantic chunking, late chunking, ColBERT/MUVERA, server-side HyDE or Self-RAG loops (the
  evidence does not justify them at this scale).
- Embedded graph databases (Kuzu was archived; the others need CGo).
- Entity co-occurrence and "precedes" edges from the old plan (D-H: noisy, unsupported).
- A cross-namespace global graph database (`scope.namespaces` covers the need).
- Server-side session state (the protocol is stateless).
- Agent-settable trust (decision 6).
- Automatic re-fetch on TTL expiry (proposals only; the server never fetches).
- LLM-judge win rates as the primary metric (biased by length and position).
- `--http` before 1.1 (OD-11).
- Any write path in the UI.
- The six legacy journal tools behind a flag, or a journal importer (owner decision 5).
- Raw weights (`alpha`, `rrf_k`, half-life) in the LLM-facing schema (D-K).
- MCP prompts (no use case survives the redesign); cobra or a JavaScript framework; telemetry
  (anything that leaves the machine; local metrics are P10).
- `VACUUM INTO` migration backups (nothing to migrate).

---

## 14. Documentation maintenance

| Item | Action | When |
|---|---|---|
| This file | Keep the phase table and §4.5 current; move finished phases' exit evidence into `docs/eval/` | each tag |
| `README.md` | Pitch becomes "measurable local knowledge base for agents"; tool table generated from the golden; environment table (`MEMO_KB`, `MEMO_HOME`, `MEMO_DEFAULT_NAMESPACE`, `MEMO_MODEL`, `MEMO_PROFILE`, `MEMO_QUERY_LOG`, `MEMO_OLLAMA_URL`); CLI list; "How search works" at chunk level; storage section; trust and provenance section with the elicitation-hook caveat; install from releases and the registry; Obsidian screenshot; articles index | P2 interim, P4/P5, P6 final |
| `knowledge-base-sota.md` reconciliation (alongside `docs/schema.md`) | Fix the entity-to-entity edge mislabel (§2, D-H); reconcile the three MemDelta figures (§1.5, D-I, §9); mark the granite-small dimension as assumed inline; unify the origin enum spelling; align the `ingest` signature with the body (`facts[]`, `entities[]`, `relations[]`, `source.library`, `content_hash`); add `merge_candidates`, `is_inference`, `tags`, trust on documents and pages, `namespace` and `origin` in results, and the `memo://` scheme to the schema sketch; settle `mode` versus `strategy` versus `profile` | P1 |
| `slides.md` Part 1 | Title and tagline; "What memo-mcp Does" and "The 6 Thought Categories" become the tool surface and namespaces; "Key Terms" gains chunk, BM25, RRF, provenance, bi-temporal; "Architecture Overview" shows the new packages; "How Search Works" at chunk level; "Token-Based Storage" becomes `MEMO_KB` plus namespaces; the demo transcript uses real output; clone URLs | P2, P4 |
| `slides.md` Part 2 (lines 435–575) | Rewrite as "Going Deeper: measuring, explaining, routing": drop the co-occurrence and "precedes" edges, the `α·vector + β·graph + γ·recency` formula and its "30-day half-life" (really about 20.8 days; the shipped recency is `0.8 + 0.2·0.5^(age/90)`), the global graph and the recursive-CTE slide; replace with the explain table (P2), the bake-off table (P3), two clocks and three trust tiers (P4), graph as one routed arm with measured numbers (P7), compaction (P8) | P3, P4, P7, P8 |
| Code comments citing "Phase N" or "the project roadmap" | `internal/server/server_test.go:287, 292, 295, 300` (P2, when the stubs activate); `internal/eval/corpus.go:29, 87, 99-100, 194, 205` (P2 port); `internal/eval/eval_test.go:33`, `internal/embedding/fake.go:29` (P0); `internal/journal/journal.go:226`, `internal/journal/migrate.go:15` (packages deleted or moved in P1–P2) | P0–P2 |
| `docs/eval/` (new) | `vX.Y.Z.md` per tag from `memo-mcp eval --format md` | P2 onward |
| `docs/spikes/` (new) | `S1..S7.md`: question, number, decision | P0, P1, P4, P7 |
| `docs/schema.md` (new) | The full target schema in plain words; owner sign-off before migration 1 | P1 |
| `docs/architecture.md` (new) | Layers, pipeline, formulas, profile derivations, the explain field reference, the URI scheme, baselines | P6 |
| `SKILL.md`, `CHANGELOG.md`, `server.json`, `.goreleaser.yaml`, `.github/workflows/ci.yml`, `.golangci.yml` (new) | As described in P0, P5, P6 | P0, P5, P6 |
| `articles/` | One article per phase (titles in §7); an index in the README | each phase |
| `docs/README.md` | Keep the live-versus-history index current when documents are added or moved | as needed |

---

## 15. Glossary

One plain sentence each, grouped by where you meet the term.

**Storage and schema**

- **Knowledge base:** one SQLite file holding sources, documents, chunks, facts, entities and
  pages for one or more namespaces.
- **Namespace:** a labelled shelf inside one file (a library, a project, "personal"); searches
  span all shelves by default, writes go to one.
- **Source:** where a document came from: URL or file, version, fetch time, fingerprint, trust and
  declared origin.
- **Document:** the normalised markdown text of one source at one revision, kept and never
  silently rewritten.
- **Revision:** a new copy of a document after its source changed; the old one is marked
  superseded.
- **Chunk:** a passage of a few hundred tokens cut from a document and indexed on its own.
- **Context header:** the "title > section" line prepended to a chunk so it carries its place in
  the document.
- **Token:** the unit a model counts text in, roughly three-quarters of an English word.
- **Content hash:** a fingerprint of the text; identical content gives the same hash, so
  re-ingests are skipped.
- **Migration, `user_version`:** a numbered one-way schema change, and the counter in the file
  header that records how far a file has been brought.
- **Application id:** a header integer saying which program owns the file; lets memo-mcp refuse
  an old journal.
- **UUIDv7:** a time-sortable unique id.
- **External-content FTS table:** a keyword index that reads its text from your real table
  instead of copying it.
- **Trigger:** a small SQL rule that keeps an index in step with its table automatically.
- **Virtual table:** a SQLite table whose storage is provided by an extension (FTS5, vec0).
- **WAL:** SQLite's write-ahead log, which lets readers and a writer work at the same time.
- **Idempotent write:** doing it twice changes nothing.

**Search**

- **Embedding, vector:** text turned into a list of numbers so that similar meanings land near
  each other.
- **Dimension:** how many numbers are in a vector (384, 768).
- **Cosine similarity:** how aligned two vectors are, from −1 to 1; 1 means the same direction.
- **KNN:** the k vectors nearest to the query's vector.
- **FTS5:** SQLite's full-text search engine.
- **BM25:** the classic keyword score that rewards rare, repeated query words.
- **Tokenizer:** in search, the rule that splits text into words for the index; for a model, the
  rule that splits text into the pieces the network reads.
- **Porter stemming:** folding "reviewing" and "review" into one search term.
- **Exact index:** a second keyword index that keeps identifiers such as `ErrNoRows` whole.
- **Trigram index:** an index on every three-letter window so substrings of identifiers match.
- **Hybrid search:** keyword and meaning search run together and merged.
- **Retrieval arm:** one way of ranking (semantic, keyword, exact, fact, entity, graph) whose
  list feeds the fusion.
- **RRF (reciprocal rank fusion):** each arm votes; a result at position r earns
  `weight / (60 + r)`; votes are summed, so no raw score dominates.
- **Fusion weight:** how much each arm's vote counts.
- **Profile:** a named bundle of numeric settings (weights, depth, half-life, rerank) chosen
  server-side and tuned in the lab.
- **Small-to-big:** match on a chunk, return its section or document when reading.
- **Recency decay, half-life:** older items lose a little score; the bonus halves every 90 days
  and never drops below 80%.
- **Autocut (gap cutoff):** stop the result list at the first large score drop instead of a
  fixed threshold.
- **Relevance band:** a strong, moderate or weak label on the raw similarity that an agent can
  act on.
- **Abstention:** returning nothing on purpose, with a reason and a hint, when nothing fits.
- **Degraded:** a result produced without some capability (for example no embedder); always
  flagged.
- **Pre-top-k filter:** narrow by scope first, rank second, so filters never lose results.
- **Routing:** choosing which arms to run from the shape of the query.
- **Multi-query (`queries[]`):** several phrasings sent by the agent and fused.
- **Token budget:** the maximum response size; the server packs results to fit and says what it
  cut.
- **Progressive disclosure:** identifiers and one-liners first, full content on request.
- **Reranker, cross-encoder:** a second, slower model that reads query and passage together to
  reorder the top few.

**Measurement**

- **Eval harness:** labelled questions plus metrics that turn "feels better" into numbers.
- **Recall@k:** the share of relevant items found in the top k.
- **MRR:** how high the first relevant item ranks, averaged over queries.
- **nDCG@10:** a score for the whole top-ten order that rewards relevant items near the top.
- **Baseline and tolerance:** the recorded numbers a change must not fall below, with a small
  allowance for noise.
- **Paired comparison:** judging a change query by query, not only by the averages.
- **Category slice:** a labelled set of queries testing one ability (lookup, exact, knowledge
  update, multi-hop).
- **Cost column:** time, tokens and size next to every quality number.
- **Ablation:** switching one component off to measure what it contributed.
- **Golden test:** a saved copy of exact output, diffed on every change.
- **Hash embedder:** a deterministic fake embedder with real geometry but no synonymy, for tests
  without a model.
- **Spike:** a short throwaway experiment that answers one question with a number.
- **Bake-off:** comparing models on the same corpus and queries.
- **Write loss versus retrieval loss:** information lost when storing versus when searching.
- **Agent-iterating baseline:** "raw chunks plus an agent searching twice", the strong baseline
  derived systems must beat.

**Models**

- **Embedding model (embedder):** the network that turns text into vectors; swapping it changes
  results more than most other choices.
- **Matryoshka (MRL):** a model trained so a truncated vector still works.
- **Quantisation (q8):** smaller model weights at a slight accuracy cost.
- **Static embeddings:** a word-lookup table summed per text; instant but weaker.
- **ONNX:** a portable file format for neural networks.
- **hugot, GoMLX:** the pure-Go runner and backend that execute ONNX models here, about ten times
  slower than ONNX Runtime.
- **Op coverage:** whether the backend implements every operation a model needs; the real
  constraint, not quality.
- **Ollama:** a local model runner; optional here, never required.

**Provenance, trust and time**

- **Provenance:** the recorded chain from any result back to the source and chunks it came from.
- **Trust tier:** `curated`, `user` or `agent`: what the write channel guarantees, assigned by
  channel, not by claim.
- **Origin:** what the writer said the content came from (web, user said, agent derived); a
  label, not a guarantee.
- **Channel:** how a write arrived (tool call, elicitation, CLI, worker).
- **Fact:** one atomic claim in one sentence, with a validity window and an evidence chunk.
- **Bi-temporal:** two clocks per record: when it was true in the world and when the knowledge
  base recorded or retired it.
- **`valid_from`, `valid_to`:** the world-time window. **`recorded_at`, `invalidated_at`:** the
  system-time window.
- **Supersession:** a new record pointing at the old one it replaces, which is retired, not
  deleted.
- **Tombstone:** a record hidden from search but kept so references resolve to "deleted on …
  because …".
- **Redaction:** also removing a tombstoned record's content.
- **Revocation filter:** retired records are excluded before ranking, never merely ranked lower.
- **`as_of`:** asking what the knowledge base believed was true at a given moment.
- **Elicitation:** the server asks the human a question through the client's interface; the
  model cannot answer it.
- **Memory poisoning:** tricking an agent into writing attacker-chosen content into its memory.
- **Audit log:** who changed what, when, through which channel.

**Protocol**

- **MCP:** the Model Context Protocol, which agents use to call tools and read resources.
- **Tool, resource, prompt:** something the agent calls; something it reads by address; a
  reusable template.
- **Stateless (2026-07-28):** the server remembers nothing between calls; anything needed travels
  as an argument.
- **MRTR:** the 2026-07-28 mechanism that carries server questions (elicitation) to the client.
- **Structured output, output schema:** typed JSON results with a published shape, next to a text
  mirror.
- **Tool annotation:** a hint such as read-only, destructive or closed-world.
- **`exclude_ids`:** the client's list of already-seen results.
- **`ttlMs`:** how long a client may cache a response.
- **Skill, AGENTS.md, passive context:** instructions and indexes the agent reads without
  deciding to call a tool.
- **MCP registry:** the public directory of MCP servers.

**Graph**

- **Entity, alias, canonical name:** a named thing documents talk about; another spelling of it;
  the chosen one.
- **Mention edge:** a link saying "this chunk mentions this entity"; no relation type needed.
- **Entity resolution:** deciding two names refer to the same thing.
- **MinHash, LSH:** a fast approximate way to find strings that are almost identical.
- **Entropy gate:** short or ambiguous names go to a review queue instead of auto-merging.
- **Graph-as-index:** using structure to find text, not to replace it.
- **Personalised PageRank (PPR):** random walks that keep returning to the query's entities;
  where they land often is relevant.
- **CSR:** a compact in-memory layout for a graph.
- **Hub penalty:** capping very connected entities so they do not dominate.
- **Multi-hop question:** one that needs two linked facts.
- **Update-stream test:** index half the corpus, add the rest in batches, check old queries do
  not regress.

**Compaction**

- **Compaction:** turning many raw records into a maintained page or a resolved conflict, with
  the raw records kept.
- **Work item:** one self-contained task the server hands to the agent ("write the page for X
  from these chunks").
- **Page:** a curated markdown summary that cites its chunks.
- **`is_inference`:** marks text derived by a model, not taken from a source.
- **Staleness:** a page is stale when a source it was built from changed.
- **Lint:** a pass that looks for contradictions, orphans, stale claims and missing pages.
- **Dry-run diff:** see the change before it is saved.
- **Recurrence trigger:** compaction runs only on clusters that keep growing.
- **Sleep-time compute:** doing derivation work when idle and reusing it at query time.

**Engineering**

- **Semantic version, git tag:** `vMAJOR.MINOR.PATCH` on a named commit; 1.0 freezes a contract.
- **CI:** checks that run automatically on every push.
- **Linter:** a tool that flags unchecked errors and unsafe patterns before tests run.
- **Build tag:** a compile-time switch (`realmodel`, `spike`).
- **`CGO_ENABLED=0`:** pure Go, no C compiler, one static binary.
- **GoReleaser:** the tool that builds release archives per operating system.
- **Query log:** an opt-in table of past searches and their traces, stored in the same file.
- **Explain (`Why`, `Trace`):** the per-result and per-query breakdown of how a ranking was
  reached.
- **Loopback binding:** listening only on this machine's own address.
