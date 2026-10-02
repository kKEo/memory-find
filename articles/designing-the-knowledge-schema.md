# Designing the Knowledge Schema

*Roadmap phase P1, "The store": how knowledge gets into memo-mcp now, and why the layout looks
the way it does.*

## Start with the questions, not the tables

A knowledge base is only as good as the questions it can answer later. Before writing a line of
SQL we wrote down the ones that matter here: *Where did this come from, and which version?* *Is
this still the current text, or was it replaced?* *How much should I trust it, and who said so?*
*What did we believe on a given date?* *Which passage, exactly, supports this?*

Each of those became a layer:

```
L3  facts        one-sentence claims with "true from / true until" and an evidence chunk
L2  chunks       passages of ~200 tokens, indexed three ways
L1  documents    the text as ingested, one row per revision
L0  sources      where it came from: URL, library, fetch time, hash, trust, origin
```

Every layer points down. A chunk belongs to a document, a document to a source, a fact to the
chunk that backs it. Nothing above the raw text is ever the only copy of anything: the lower
layers can be rebuilt from the sources, and the upper ones can be invalidated when what they were
built from changes. The full description, column by column in plain words, is
[`docs/schema.md`](../docs/schema.md); it was signed off before the first migration was coded.

## Three indexes, because one is never enough

A chunk is indexed three ways, and each exists because the others fail on something:

- A **stemmed keyword index** (SQLite FTS5 with the Porter stemmer) finds "review" when you
  wrote "reviewing". It also indexes the chunk's *context header*, the title and section trail,
  so a search for a section's name finds its passages even when they use other words.
- An **exact-identifier index** keeps `useCallback`, `net/http` and `ERR_CONN_RESET` whole.
  The stemmer would break them into pieces and match `conn` against everything.
- A **vector index** finds meaning: "middleware ordering" is close to "interceptors run in
  registration order" even though they share no word.

The vectors live in an ordinary table, not a specialised one. Spike S2 measured sqlite-vec's
`vec0` virtual table at 1.5 to 1.8 times faster than a plain scan, under the 3× bar we had set,
so we took the plain table and with it real foreign keys, any SQL filter before ranking, and any
dimension per model. Vectors are keyed by *model* as well as chunk, so switching embedding
models later is a background job, not a migration.

## Versions are revisions

The one place the owner overruled the draft: the same documentation URL at two library versions
is one source with two document revisions, not two sources. It keeps the source table honest
(one row per thing you fetched) and puts `version` where it belongs, on the text that carries
it. The cost is that only the newest revision is "live" by default. A search scoped to a version
therefore reaches the revision for that version even though a newer one superseded it; without
a version scope you get the latest. That rule is written into the live filter every search arm
will use in P2.

## Trust comes from the channel, not from the caller

Any trust level that arrives in a tool call is self-reported, and a poisoned agent would happily
report "curated". So trust is set by the *channel* a write came through: tool calls get
`agent`, the command line gets `user`, and `curated` has to be typed on purpose. What the writer
*said* the content was, web page or user's words or the agent's own inference, is stored
separately as `origin`, a label that helps you read a result without pretending to protect you.

## What ingest actually does

```
normalise → hash → same hash and version?  → no-op (tell the caller)
                 → otherwise                → new revision; the old one is marked superseded
chunk (headings → paragraphs → sentences; code fences never split; ~200 tokens, cap 400)
keyword indexes update themselves (database triggers, not Go code you have to remember)
embed outside the transaction, sixteen chunks at a time
   → vectors stored, or a queued job and a pending count that `status` shows
audit row: who, through which channel, did what, to which address
```

Two details are worth knowing. Embedding never runs while the database connection is held, so a
slow model cannot block readers. And a failed embedding is never silent: the write succeeds, the
vectors are queued, `status` says how many are pending, and `memo-mcp backfill` drains them.

## The human face arrives early

`memo-mcp export --md ~/kb` writes one markdown file per document with its provenance as front
matter, so the knowledge base opens in any editor or in Obsidian. Importing that folder back
produces zero new revisions, because the front matter names each document. `ls`, `read`,
`verify` and `status` work in a terminal without a model or an agent.

## What is deliberately not here

No journal categories (a note is a document of kind `note`; tags do the rest). No importer for
old journal files; the new binary refuses them with a clear message. No scoring weights in the
schema; those are code and an optional profile file. No per-session state; the protocol is
stateless. And no search yet: that is P2, which can now be built on a store whose every row
knows where it came from.
