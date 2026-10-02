# The knowledge-base schema, in plain words

*Roadmap P1 review checkpoint. Written 2026-10-02 for owner sign-off before migration 1 is coded.
This document describes the whole target layout (layers L0–L5); migration 1 creates L0–L3 plus
bookkeeping, migration 2 (P7) adds the graph, migration 3 (P8) adds pages.*

**Status: approved by the owner on 2026-10-02** with these answers to §11: the stemmed index
covers text and context header; **one source per URL, versions are document revisions** (so
`version` lives on `documents`, a source is unique per `(namespace, uri)`, and a search scoped
to a version reaches that revision even when a newer one has superseded it); no default `ttl_s`;
query-log retention as proposed; audit ops as listed. Decisions this document assumes: a fresh file format with
nothing migrated (owner decision 5), vectors in a plain table (spike S2), two keyword indexes
(spike S3), per-kind vector tables (OD-15), a `namespaces` table (OD-18), forgotten records hidden
even under `as_of` (OD-20), trust transitions needing a human (OD-21).

---

## 1. One picture

```
L5  pages        curated markdown an agent wrote: entity pages, topic pages    (P8)
L4  graph        entities, aliases, which chunk mentions which entity, edges   (P7)
L3  facts        one-sentence claims with a validity window and evidence       (P1 tables, P4 tools)
L2  chunks       passages of ~200 tokens, indexed three ways                   (P1)
L1  documents    the text as ingested, one row per revision                    (P1)
L0  sources      where it came from: URL/file, version, hash, trust, origin    (P1)
     bookkeeping namespaces, models, jobs, audit, query_log                    (P1)
```

Every layer points down. A page lists the chunks it was built from; a fact points at its evidence
chunk; a chunk belongs to a document; a document belongs to a source. Nothing above L2 is ever the
only copy of anything: L0–L2 are deterministic and can be rebuilt from the sources, L3–L5 are
additive and can be invalidated when what they were built from changes.

## 2. Vocabulary (one sentence each)

- **Source**: where a piece of knowledge came from and how much we trust it.
- **Document**: the normalised markdown text of one source at one point in time (a *revision*).
- **Chunk**: a passage cut from a document, small enough to match precisely; the unit search ranks.
- **Fact**: a single claim in one sentence, with "true from / true until" dates and a chunk that
  backs it up.
- **Entity**: a named thing documents talk about (a library, a function, a person, a concept).
- **Page**: a curated summary written by the calling agent, citing its chunks.
- **Namespace**: a labelled shelf inside one file (a library, a project, "personal"). Searches
  span all shelves by default; writes go to exactly one.
- **Trust**: what the *channel* a record arrived through guarantees (`agent`, `user`, `curated`).
- **Origin**: what the *writer* said the content came from (`web`, `user-said`,
  `agent-derived`). A label, not a guarantee.
- **Two clocks**: *world time* (when something was true) and *system time* (when we learned it or
  retired it). Both are kept so "what did we believe on date X" is answerable.
- **Tombstone**: a record hidden from search but kept, so a reference to it resolves to "deleted
  on … because …" instead of "not found".

## 3. Identity and addresses

Every row that an agent or a person can refer to has a stable address:

| Address | Points at | Id type |
|---|---|---|
| `memo://source/<uuid>` | a source | UUIDv7 (time-sortable) |
| `memo://doc/<uuid>` | a document revision | UUIDv7 |
| `memo://chunk/<int>` | a chunk | integer (chunks are numerous and never referenced outside the file) |
| `memo://fact/<uuid>` | a fact | UUIDv7 |
| `memo://entity/<uuid>` (P7) | an entity | UUIDv7 |
| `memo://page/<uuid>` (P8) | a page | UUIDv7 |
| `memo://ns/<name>/index` (P5) | the per-namespace index resource | namespace name |

Ids are unique across the whole file, so a result does not need its namespace to be addressed;
the namespace travels as a field on every result instead.

**Notes have no URL.** A note (something an agent or person wrote, not fetched) gets a source
row with `uri = NULL` and is addressed by its document id. Updating a note is an explicit new
revision of the same document, not a hash comparison.

## 4. The file

One SQLite file per knowledge base: `$MEMO_HOME/kb/<MEMO_KB>.db` (default
`~/.memo-mcp/kb/default.db`). The file header carries `PRAGMA application_id = 0x4D454D4F`
("MEMO") and `PRAGMA user_version = <migration number>`. On open, a file that has tables but a
different application id is refused: *"this is a v1 memo-mcp journal, not a knowledge base;
nothing is migrated."* Read-only commands (`status`, `read`, `ls`, `search`, `export`) open with
`mode=ro` and never create a file; only writes create one.

Connection settings (unchanged from today): WAL journal, `busy_timeout 5000`, `synchronous
NORMAL`, `foreign_keys ON`, `_txlock=immediate`, one connection for writes.

## 5. Tables, column by column

Types are SQLite's. `ts` means Unix milliseconds stored as INTEGER. `uuid` means a UUIDv7 stored
as TEXT. Columns marked **(index)** have an index.

### 5.1 Bookkeeping

**`namespaces`** — the shelves.

| Column | Meaning |
|---|---|
| `name` TEXT PK | the label (`[A-Za-z0-9._-]{1,64}`) |
| `description` TEXT | one line a human or the index resource can show |
| `created_at` ts | |

**`models`** — every embedding model whose vectors exist in this file.

| Column | Meaning |
|---|---|
| `id` TEXT PK | short handle (`minilm`, `granite-small-r2`) |
| `name` TEXT | display name |
| `hf_repo` TEXT | Hugging Face repository |
| `hf_revision` TEXT | pinned commit, so a re-download fetches the same bytes |
| `onnx_path` TEXT | path of the `.onnx` file inside the repo (spike S4: required) |
| `external_data_path` TEXT NULL | path of the external weights file, if any (spike S4: required for onnx-community exports) |
| `dim` INTEGER | vector length |
| `max_tokens` INTEGER | the model's input limit; chunking clamps to it |
| `query_prefix`, `doc_prefix` TEXT | strings some models want prepended to queries / documents |
| `normalize` INTEGER | 1 if vectors are unit length (needed for cosine = 1 − distance) |
| `licence` TEXT | shown at download time |
| `sha256` TEXT | of the model file actually loaded |
| `installed_at` ts | |
| `is_default` INTEGER | exactly one row is 1: the model queries use |

**`jobs`** — background or long work, visible in `status`.

| Column | Meaning |
|---|---|
| `id` uuid PK | the handle an agent or the CLI can poll |
| `kind` TEXT | `ingest`, `embed`, `reindex`, `compact` |
| `scope` TEXT | namespace, model id or document id the job is about |
| `state` TEXT | `queued`, `running`, `done`, `failed` |
| `created_at`, `updated_at` ts | |
| `items_json` TEXT | the work list (chunk ids to embed, documents to re-chunk) |
| `error` TEXT NULL | why it failed |

**`audit`** — who changed what, when, through which channel. Append-only.

| Column | Meaning |
|---|---|
| `ts` ts **(index)** | |
| `actor` TEXT | client name from the MCP handshake, or `cli`, or `worker` |
| `channel` TEXT | `tool`, `elicitation`, `cli`, `worker` |
| `op` TEXT | `ingest`, `revise`, `remember`, `forget`, `redact`, `supersede`, `promote`, `demote`, `merge`, `submit` |
| `target_uri` TEXT | the record touched |
| `detail_json` TEXT | the arguments that matter (old and new trust, reason, hash) |

**`query_log`** — written only when `MEMO_QUERY_LOG=1` (owner decision 4). Never stores chunk text; excluded from export; pruned at 10k rows or 30 days.

| Column | Meaning |
|---|---|
| `id` INTEGER PK | |
| `ts` ts **(index)** | |
| `args_json` TEXT | the search arguments as received |
| `mode`, `profile`, `model_id` TEXT | what actually ran |
| `n_results` INTEGER | |
| `top_uris_json` TEXT | result addresses and scores |
| `latency_ms` INTEGER | |
| `trace_json` TEXT | the per-query `Trace` (routing, filters, cutoff, budget) |

### 5.2 L0 — sources

| Column | Meaning |
|---|---|
| `id` uuid PK | |
| `namespace` TEXT **(index)** → `namespaces.name` | the shelf |
| `uri` TEXT NULL | URL or file path the client fetched; NULL for notes |
| `title` TEXT | |
| `kind` TEXT | `doc`, `note`, `code`, `conversation` |
| `library` TEXT NULL **(index)** | e.g. `grpc/grpc-go`; lets a coding agent scope by library |
| `content_hash` TEXT | fingerprint of the latest revision's normalised content |
| `etag` TEXT NULL | as reported by the client, for its own re-fetch decisions |
| `fetched_at` ts | when the client fetched it |
| `ttl_s` INTEGER NULL | after this many seconds the source is *proposed* for re-fetch (never fetched automatically) |
| `trust` TEXT | `curated`, `user`, `agent` — assigned by channel (§7) |
| `origin` TEXT | `web`, `user-said`, `agent-derived` — as declared by the writer |
| `tags_json` TEXT | free labels, filterable |
| `created_at` ts | |

Uniqueness: `(namespace, uri)` when `uri` is not NULL. Re-ingesting the same source with an
identical `content_hash` is a no-op; a different hash **or a different `version`** creates a new
document revision.

### 5.3 L1 — documents

| Column | Meaning |
|---|---|
| `id` uuid PK | |
| `source_id` uuid **(index)** → `sources.id` | |
| `revision` INTEGER | 1, 2, 3… per source |
| `version` TEXT NULL **(index)** | semver, git tag or commit this revision's content belongs to (owner decision: versions are revisions) |
| `content` TEXT | normalised markdown, kept verbatim |
| `context` TEXT NULL | an optional client-written sentence about the document, prepended to every chunk's context header |
| `created_at`, `updated_at` ts | |
| `deleted_at` ts NULL | set by `forget` (tombstone) |
| `deleted_reason` TEXT NULL | shown when a tombstone is read |
| `superseded_by` uuid NULL → `documents.id` | the newer revision |

"Live" means `deleted_at IS NULL AND superseded_by IS NULL`. Every search arm applies that
filter *inside* its SQL, before ranking. **Exception for versions:** when a search is scoped to a
`version`, the filter becomes `deleted_at IS NULL AND version = ?`, so the revision for that
version is found even though a newer version has superseded it. Without a version scope, only the
latest revision of each source is live.

### 5.4 L2 — chunks and their three indexes

**`chunks`**

| Column | Meaning |
|---|---|
| `id` INTEGER PK | |
| `document_id` uuid **(index)** → `documents.id` ON DELETE CASCADE | |
| `ord` INTEGER | position within the document |
| `section_path` TEXT | heading trail, e.g. `Interceptors > Ordering` |
| `text` TEXT | the passage, without the context header |
| `context_header` TEXT | `title > section path` (plus `documents.context`), prepended when embedding and indexing |
| `est_tokens` INTEGER | model-free estimate used by the splitter |
| `lang` TEXT NULL | `go`, `ts`, … for code chunks |

Chunking: headings → paragraphs → sentences → hard split; target about 200 estimated tokens,
hard cap 400 clamped to the default model's `max_tokens`, about 40 tokens of overlap, fenced code
blocks never split (OD-3).

**`chunks_fts`** — FTS5, external content on `chunks(context_header, text)`, tokenizer
`porter unicode61 remove_diacritics 2`, kept in step by insert/delete/update triggers. Finds
words and their stems ("review" finds "reviewing").

**`chunks_fts_exact`** — FTS5, external content on `chunks(text)`, tokenizer
`unicode61 tokenchars '_.:-/'`. Finds identifiers whole (`useCallback`, `net/http`,
`ERR_CONN_RESET`) and never stems (OD-2, spike S3).

**`chunk_vecs`** — a plain table (spike S2, OD-1).

| Column | Meaning |
|---|---|
| `chunk_id` INTEGER → `chunks.id` ON DELETE CASCADE | |
| `model_id` TEXT → `models.id` | vectors for several models may coexist; queries use the default model's rows |
| `embedding` BLOB | `float32[dim]` as sqlite-vec expects (`vec_f32`) |
| PRIMARY KEY `(chunk_id, model_id)` | |

Searched with `vec_distance_cosine(embedding, ?)` over the rows that pass the scope and live
filters. A chunk with no row for the default model is *pending*; `status` counts them and the
backfill worker fills them.

### 5.5 L3 — facts

| Column | Meaning |
|---|---|
| `id` uuid PK | |
| `namespace` TEXT **(index)** → `namespaces.name` | |
| `statement` TEXT | one sentence |
| `subject_entity_id` uuid NULL → `entities.id` (P7) | the main thing the fact is about |
| `about_json` TEXT | names the writer attached (`["SetCacheable"]`), used for matching before entities exist |
| `valid_from`, `valid_to` ts NULL | world time: when it was / stopped being true |
| `recorded_at` ts | system time: when we learned it |
| `invalidated_at` ts NULL | system time: when we retired it |
| `superseded_by` uuid NULL → `facts.id` | the fact that replaced it |
| `evidence_chunk_id` INTEGER NULL → `chunks.id` | the passage that backs it |
| `trust`, `origin` TEXT | as for sources |
| `deleted_at` ts NULL, `deleted_reason` TEXT NULL | tombstone, as for documents |

Facts are add-only: `remember(..., supersedes=<id>)` writes a new row and sets `invalidated_at`
and `superseded_by` on the old one. `facts_fts` (porter) and `fact_vecs(fact_id, model_id,
embedding)` index them for the fact arm (P4).

### 5.6 L4 — graph (migration 2, P7)

`entities(id, namespace, canonical, type, summary_page_id NULL)`,
`entity_aliases(alias, entity_id)`, `mentions(entity_id, chunk_id, weight)`,
`merge_candidates(a, b, score, reason, state)`, optional `edges(src, dst, rel, weight,
valid_from, valid_to, recorded_at, invalidated_at, evidence_chunk_id)`. Mention edges come
first; typed edges only if they beat the entity arm in eval.

### 5.7 L5 — pages (migration 3, P8)

`pages(id, kind entity|topic|overview, subject_id, namespace, content, built_at,
built_from_rev, stale, is_inference, trust)`, `page_sources(page_id, chunk_id)`,
`page_vecs(page_id, model_id, embedding)`, `work_items(id, job_id, kind, scope, payload_json,
state, result_json)`.

## 6. What is indexed where (P1 and P2)

| Question | Answered by |
|---|---|
| "find chunks about these words" | `chunks_fts` (BM25, stemmed) |
| "find this exact identifier" | `chunks_fts_exact` |
| "find chunks that mean this" | `chunk_vecs` + `vec_distance_cosine` |
| "only this library/version/kind/namespace/date/trust" | plain columns on `sources` and `documents`, joined before ranking |
| "only live records" | `deleted_at IS NULL AND superseded_by IS NULL` inside every arm |
| "what was true on date X" | `facts.valid_*` and `recorded_at`/`invalidated_at` (P4) |

## 7. Trust and origin, and who may change what

`trust` is set by the channel the write came through and cannot be claimed in a tool call:

| Channel | Trust the record gets |
|---|---|
| MCP tool call (`ingest`, `remember`) | `agent` |
| CLI (`memo-mcp ingest`, `remember`) | `user` by default; `--trust curated` must be typed (OD-9) |
| `promote` after the human accepts an elicitation (P5), or `memo-mcp trust promote` | the requested level |

`origin` is what the writer declared and is kept separately, so results can still say "this came
from the web" without that label conferring trust.

Operations on a record whose trust is **above** the caller's channel cap need a human (OD-21):

| Operation | On `agent` record | On `user` record | On `curated` record |
|---|---|---|---|
| `forget`, `supersedes`, `redact` from a tool call | allowed | elicitation or CLI | elicitation or CLI |
| the same from the CLI | allowed | allowed | allowed (audited) |
| `promote` | elicitation or CLI | elicitation or CLI | – |
| `demote` | CLI only | CLI only | CLI only |

Every row of this table is written to `audit` with its channel. `search` does not filter by trust
by default (recall first); it labels every result and offers `scope.min_trust`.

## 8. The two clocks and what `as_of` can see

- Superseded documents and invalidated facts are **history**: hidden by default, visible with
  `as_of=T` (`recorded_at <= T AND (invalidated_at IS NULL OR invalidated_at > T)`).
- Forgotten (tombstoned) records are **never served**, not even under `as_of`; `read` on their
  address returns "forgotten on … because …". `redact` additionally clears the content (OD-20).

## 9. Migrations

| # | Creates | Phase |
|---|---|---|
| 1 | application id; `namespaces`, `models`, `jobs`, `audit`, `query_log`; `sources`, `documents`, `chunks`, `chunks_fts`, `chunks_fts_exact`, `chunk_vecs`; `facts`, `facts_fts`, `fact_vecs`; all triggers and indexes | P1 |
| 2 | `entities`, `entity_aliases`, `mentions`, `merge_candidates`, `edges` | P7 |
| 3 | `pages`, `page_sources`, `page_vecs`, `work_items` | P8 |

Migrations run inside `BEGIN IMMEDIATE`, read `user_version` inside the transaction, and refuse
negative or future versions.

## 10. Deliberately not in the schema

- Any table for the journal's six categories (they become `kind=note` plus tags).
- Scoring weights or profiles (they are code and an optional TOML file, never rows).
- Per-session state (MCP is stateless; `exclude_ids` travels as an argument).
- A cross-namespace "global graph" database (namespaces in one file cover it).

## 11. Questions for sign-off

Answered 2026-10-02 (see the status line at the top): 1. both columns; 2. one source, versions as
revisions; 3. no default; 4. as proposed; 5. nothing added.
