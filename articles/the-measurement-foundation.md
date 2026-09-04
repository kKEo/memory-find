# The Measurement Foundation

*Why memo-mcp's next milestone wasn't a feature, and what it took to build it.*

## A search engine that worked, according to its own tests

Before this work started, `memo-mcp`'s test suite was green. Every package passed. And yet, if you'd pointed the real server at a real journal and asked it a real question, `search_journal` would have told you: *"No relevant entries found."* Every time. For every query.

Three bugs compounded into that result. Journal entries longer than a few hundred words silently failed to get an embedding at all — the tokenizer choked, the write path logged a warning nobody was watching, and the entry was saved with no vector. The keyword search required every word in your query to appear in the same entry, which natural-language questions almost never satisfy. And when both signals came up empty, the code did exactly what it was told: it returned nothing.

None of this showed up in testing. Not because the tests were sparse — there were real assertions, real edge cases, a genuinely well-designed showcase test comparing vector search against keyword search against their combination. The problem was one shared design choice sitting underneath all of it: every mock embedder in the suite returned the same vector for every piece of text.

That single choice is quietly catastrophic for testing a *search* system. If every entry is embedded identically, every entry is equally "similar" to every query. Ranking bugs have nothing to disturb. Recency logic has nothing to reorder. A section filter returning zero results looks the same as a section filter returning the right result, because the fixture database was small enough that everything came back anyway. The tests weren't wrong about what they checked — they were checking a system with the one property real search never has: it's easy to satisfy when nothing has to actually rank against anything else.

This is the failure mode the second phase of work — internally called **the measurement foundation** — exists to close off permanently.

## The plan, in one sentence

Fix the bugs first (that was phase one), then make it structurally impossible for the next bug of this shape to hide again. Not "write more tests." Specifically: replace the thing that was hiding the bugs, and add the kind of test that only a hidden bug like this would fail.

That turned into four pieces of work.

## 1. An embedder that actually discriminates

The fix wasn't more tests with the old mock — it was retiring the old mock. `HashEmbedder` is a deterministic, offline replacement for the constant-vector stub: every token in a piece of text maps to a pseudo-random unit vector seeded from its own hash, and the text's embedding is the weighted sum of its tokens' vectors, normalized back to unit length.

The property this buys is the one thing a constant vector can never provide: **real geometry**. Two entries about authentication middleware land close together. An entry about authentication and an entry about a lunchtime walk land close to orthogonal. It has an honest, documented blind spot — it has no notion of synonymy, so "car" and "automobile" are unrelated to it, the same way they'd be unrelated to a bag-of-words model. Fixtures built against it have to rely on shared vocabulary, not paraphrase, for their expected matches. That's a real limitation, and it's better to know about it than to paper over it with a mock that has no limitations because it has no behavior.

Once this existed, it replaced three separate hand-rolled mocks scattered across the `journal`, `search`, and `embedding` packages — including one test whose entire job was verifying that a mock returned what it was told to return. That test is gone now. It never tested the product.

## 2. Testing the protocol, not just the logic

Before this phase, the MCP server layer — the actual thing Claude talks to — had zero tests. Every tool handler, every schema, every error path: unverified. The retrieval logic underneath was tested; the wiring that exposes it to the outside world was not.

That gap wasn't hypothetical. A previous bug had literally been the word `"required,"` leaking verbatim into a tool's description, because of a one-character misunderstanding of a struct tag's contract. Nothing in the test suite could have caught that, because nothing in the test suite looked at what the protocol layer actually produced.

The fix connects a real client to a real server over an in-memory transport and drives it exactly the way Claude would: list the tools, call them, read the results. The centerpiece is a golden-file snapshot of the entire `tools/list` response — names, descriptions, input schemas, all of it — committed to the repo and diffed on every change. If a future edit reintroduces a leaked directive, changes a schema in a way nobody intended, or silently drops a tool, the diff shows up in the same place a code reviewer already looks. That's a stronger guarantee than "we remembered to check," and it costs nothing per review to maintain.

Coverage of the server package went from 0% to 89%.

## 3. A number instead of a feeling

This is the piece that gives the phase its name, and the piece the rest of the project's roadmap depends on.

Testing individual bugs is necessary but not sufficient. It answers "does this specific defect still exist?" It cannot answer "did this change to the ranking formula make search *better*?" — and a project that intends to keep tuning retrieval (which this one does, deliberately, as its whole reason for existing) needs an answer to that second question that isn't a vibe.

The eval harness is a small, purpose-built information-retrieval benchmark: a fixture corpus of 79 journal entries, organized into deliberate scenarios — near-duplicate paraphrases, a topic buried in a rare section surrounded by 35 decoys in a common one, entries long enough to reproduce the embedding-truncation bug on purpose, a documented case where two words share a root but not a token ("review" / "reviewing") to record the current stemming gap honestly rather than hide it. Against that corpus, 29 labelled queries, each with a human judgment of what *should* come back. Four standard IR metrics — recall at 1, 5, and 10, mean reciprocal rank, and normalized discounted cumulative gain — get computed per query and averaged.

The result gets written to a baseline file and checked into the repo. From this point on, a change to the ranking formula, the fusion weights, the recency decay, or the embedding model itself has to clear that baseline within a small tolerance, or the test fails and says exactly which metric moved and by how much. Improving retrieval becomes a before/after number in a pull request description instead of an assertion that it "feels more relevant now."

The first baseline run is itself informative. Mean recall@5 came out to 0.91, recall@1 to 0.72 — good but not perfect, and the per-query breakdown says exactly where the gap is: queries that depend on stemming the harness doesn't have yet, and — most tellingly — three queries built specifically to probe the long-entry truncation bug, all scoring 1.0, because the keyword index still holds the full untruncated text even when the vector index doesn't. That's not a coincidence the harness stumbled into; it's the harness doing its job, making a real architectural trade-off visible as a number instead of leaving it as an assumption.

## 4. Turning "found a bug" into "can't happen again"

The last piece was going back through the defects already fixed and the ones still theoretically possible, and asking: what test would fail if this regressed?

Some of that had already happened as a side effect of fixing bugs earlier — a monotonic-scores test, a recency-ordering test, a section-filter test, each written at the moment its corresponding bug was fixed. What remained were the harder cases: a model-download recovery path that's only interesting when the download has already partially failed, and a concurrency scenario that only manifests when two real database connections fight over the same file.

For the download path, the fetch step got pulled out behind a small interface, so a test can hand it a fake that writes placeholder files with no network call at all — and then simulate an interrupted download, a corrupted cache, a download that "succeeds" but produces a file that fails to load as a real model. Each of those now has a test proving the system recovers instead of failing permanently, including one that deliberately triggers a genuine ONNX parser error to prove the retry logic engages for real, not just in a mocked-up approximation of the failure.

For concurrency, two independent database connections, opened against the same file the way two separate Claude sessions sharing one journal actually would, hammer it with interleaved writes under the race detector. No corruption, no lock contention errors — which is exactly the point of the write-ahead logging and busy-timeout configuration added earlier, now with a test that would fail loudly if a future change quietly broke that guarantee.

And for the storage layer itself, two real SQLite files — not synthetic recreations, but generated to match the two actual on-disk shapes this project's databases have existed in in the wild — got checked into the repository as fixtures. A migration test opens each one, runs the schema migration, and verifies every entry's content survives byte-for-byte while the database ends up at the current schema version. If a future migration mishandles either historical shape, this is where it gets caught, against the real shape, not a description of it.

## What this actually bought

Numbers make a convenient summary, but they're not really the point. The point is what each one represents:

- **Embedding package coverage: 14% → 73%.** The jump isn't from testing more lines — it's from the tests finally exercising the real embedding backend's failure modes instead of a mock's success path.
- **Server package coverage: 0% → 89%.** The protocol surface Claude actually talks to is now verified end to end, with a snapshot that makes any drift visible in a diff.
- **A recorded retrieval baseline** (recall@5 = 0.91, MRR = 0.89) that turns every future retrieval change into a measured comparison instead of an assertion.
- **Every defect fixed so far has a test that fails if it comes back** — including two that reproduce it against a real ONNX parser and two real historical database files, not simplified stand-ins.

None of this is a new feature. A user pointing memo-mcp at their journal today gets the same six tools they'd have gotten before this phase started. What changed is quieter and, for a project whose whole premise is that you should be able to trust and measure what your retrieval system does, more load-bearing: the next time someone changes how search works — and the project's roadmap has several such changes queued up — there's now a way to know, with a number, whether it helped.

That's what "the measurement foundation" means. Not a feature. The thing you build so that the features after it can be trusted.
