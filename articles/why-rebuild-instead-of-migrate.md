# Why Rebuild Instead of Migrate

*Roadmap phase P0, "Reset the map": what we did before building anything, and why the first
decision of the knowledge-base work was to not carry the journal's storage forward.*

## The question

memo-mcp started life as a port of a private-journal server: six tools, one SQLite file per
project, entries that are whole markdown documents with a single embedding each. The plan is to
turn it into something broader, a local knowledge base that an agent fills with library
documentation, notes and facts, and that both agents and people can search, read and audit.

The obvious way to get there is incrementally: add tables beside `entries`, write a migration for
each step, keep the six tools working throughout. That is how the previous plan was written. It is
also how most projects accumulate two overlapping shapes of the same idea, each with its own bugs,
and a migration history that nobody can safely reorder.

So the first question was not "what do we build" but "what do we owe the existing data". The
honest answer, after looking: nothing. The two real journals that existed held one entry each,
both written before search worked. There were no users to migrate. Carrying the journal schema
forward would have been engineering caution with no beneficiary.

## What "build fresh" means here

Not a new repository and not a rewrite of everything. The parts that earned their place stay:

- the **evaluation harness** that turns "search feels better" into recall and nDCG numbers;
- the **hash embedder**, a deterministic fake with real geometry, so tests run without a model;
- the **migration scaffold** (numbered one-way steps, a forward-version guard);
- the **embedding pipeline** with its atomic model download and recovery;
- the **SQLite hygiene**: WAL, `busy_timeout`, `foreign_keys`, `_txlock=immediate`.

What goes is the journal-shaped storage and the six journal tools. The new database layout
(sources, documents, chunks, facts, entities, pages, audit) is designed in writing first, signed
off, and then created as migration 1 of a new file format. Old journal files are refused with a
clear message rather than half-migrated. Nothing has to be backward compatible with a design we
already know is wrong for the job.

## Measure the libraries before designing around them

The second P0 principle: a design should not depend on a library behaviour nobody has checked.
Four short experiments, written as throwaway programs behind a build tag, answered the questions
the schema and tool surface depend on. Each produced a number and a decision, recorded in
`docs/spikes/`.

**S1, the protocol SDK.** Moving the MCP Go SDK from v1.6.0 to v1.8.0 (and the protocol from
2025-11-25 to 2026-07-28) changed nothing visible: the build was clean, every test passed, and the
saved snapshot of the tool list did not change by a byte. A new test now asserts the negotiated
protocol date, so a future bump cannot silently move it.

**S2, where vectors live.** The journal used sqlite-vec's `vec0` virtual table, which is
purpose-built for nearest-neighbour search but has no foreign keys, one fixed dimension per
table, and filter rules we had already tripped over. The alternative is a plain table scanned with
`vec_distance_cosine()`. We set the bar before measuring: use `vec0` only if it is more than three
times faster at 50,000 chunks. It was 1.5 to 1.8 times faster. So the plain table wins, and with
it real foreign keys, arbitrary SQL filters before ranking, and any model dimension per row.

**S3, full-text search.** SQLite's FTS5 does everything the design assumes: a stemmed index where
"review" finds "reviewing", a second exact index where `useCallback` and `ERR_CONN_RESET` stay
whole, triggers that keep both in step with the real table, `highlight()` for showing matched
terms, and the trigram tokenizer in reserve if substring matching ever matters.

**S4, embedding models.** The pure-Go ONNX runtime only runs models whose operations it
implements, so the embedding-model bake-off planned for P3 starts with a load-and-embed smoke test
per candidate. P0 only had to prove the harness works and learn how model repositories are laid
out. It did that, and it also found that the downloader can hang on a stale partial download,
which is exactly the kind of failure the roadmap's download-hardening item exists for.

## Tell the truth about where you are

The rest of P0 was housekeeping that matters more than it looks:

- The project had never been tagged, and the server reported version `2.0.0`. There is now a
  retroactive `v0.3.0` on the commit that finished the measurement foundation, and the binary
  reports the git tag it was built from.
- The repository, module path and clone instructions named three different places. They now name
  one.
- A statistics command crashed whenever the mean entry length was not a whole number. The tests
  had passed by arithmetic coincidence. It is fixed, and the new test was first run against the
  old code to prove it fails there.
- A lint configuration now catches the bug classes this codebase has actually had: unchecked
  errors, unchecked row iteration, unclosed rows.
- A minimal CI runs formatting, vet, lint, the race detector and a pure-Go build on every push,
  and never downloads a model.
- The binary gained subcommands (`serve`, `status`, `version`, `model redownload`) and the new
  `MEMO_KB` / `MEMO_HOME` variables, with the old flags and variables still honoured for one
  release.

None of this is a feature. All of it is the ground the next nine phases stand on.
