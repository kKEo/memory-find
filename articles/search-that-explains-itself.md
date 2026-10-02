# Search That Explains Itself

*Roadmap phase P2: three ways to find a passage, one way to combine them, and a result that can
say why it ranked where it did.*

## Three arms, one list

Ask the knowledge base "how do I set a TTL on a resource" and three different searches run over
the same pre-filtered set of passages:

- the **keyword arm** asks SQLite's full-text index for the stems of your words ("set", "ttl",
  "resourc") and scores matches with BM25, the classic formula that rewards rare words;
- the **exact arm** asks a second index that keeps identifiers whole, so `SetCacheable` or
  `net/http` match as one token and are never stemmed into fragments;
- the **semantic arm** turns the question into a vector and finds the passages whose vectors
  point the same way, which is how "middleware ordering" finds "interceptors run in registration
  order" without sharing a word.

Each arm returns a ranked list. They are combined with *reciprocal rank fusion*: a passage at
position r in an arm's list earns `weight / (60 + r)` from that arm, and the votes are summed.
Position, not raw score, is what counts, so a BM25 score of −4.2 and a cosine of 0.61 never have
to be compared directly.

## The number that could never reach the first page

The journal weighted the vector arm 0.6 and the keyword arm 0.4. That looks harmless until you
do the arithmetic the audit did: a keyword-only hit at rank 1 earns 0.4/61 ≈ 0.0066, and a
vector-only hit keeps earning more than that down to rank 31 (0.6/91 ≈ 0.0066). On a corpus with
thirty semantically similar decoys, the one document that contains your rare identifier could
not reach the first page by keyword alone. BM25 could reorder results; it could not add one.

The fix is not clever: equal weights. A rank-1 hit in either arm now ties a rank-1 hit in the
other, the exact arm breaks ties for identifiers, and each arm fetches a hundred candidates
instead of thirty so a keyword-only hit is still in the list when fusion happens. A test with
forty vector-only decoys and one keyword-only target guards it.

## When to say nothing

Nearest-neighbour search always returns something. Ask about sourdough in a corpus about gRPC
and the semantic arm will still hand back the ten least-unrelated passages, each with a
similarity near zero. The journal did exactly that and printed `[Score: 1.000]` on the first one.

Now a candidate whose only evidence is a semantic similarity below the "weak" band (0.30) is
dropped before fusion, and if nothing survives the response is a structured *abstention*: zero
results, a reason ("nothing matched by keyword and the 10 nearest passages are below the
similarity floor"), and a hint about how to narrow or rephrase. Both no-match queries in the
eval now abstain, and the eval reports an abstention rate instead of pretending the zeros were
recall.

## What "why" looks like

Every result carries its address, the passage, provenance (source URL, version, trust, origin,
namespace) and a *relevance band* computed from the raw cosine, never from a normalised score.
Ask for `response_format: explain` and each result also carries a `Why` block and the response a
`Trace`:

```json
{"arms":[{"arm":"semantic","rank":2,"raw":0.61,"raw_kind":"cosine","contribution":0.00806},
         {"arm":"keyword","rank":1,"raw":-4.2,"raw_kind":"bm25","contribution":0.00820,
          "matched_terms":["interceptor","auth"]}],
 "fused":0.01626,"recency_factor":1.0,"final":0.01626,"rank":1,"relevance":0.61,"band":"strong",
 "chunk":{"uri":"memo://chunk/812","section_path":"Interceptors > Ordering","ord":7,"est_tokens":188},
 "provenance":{"source_uri":"https://…","version":"v1.8.0","trust":"agent","origin":"web","namespace":"grpc-go"}}
```

The trace says which arms ran and why ("auto: query contains an identifier-like token, exact
arm added"), how many live documents the scope covered and how many superseded or forgotten ones
it excluded, where the list was cut (limit, score gap, or token budget) and how many results the
budget left out, and whether the search ran degraded because no embedding model was available.

The same two structs are what the terminal prints. `memo-mcp explain "<query>" memo://chunk/812`
emits the `Why` block as JSON, and a test asserts the number it shows is the number the search
produced, not a recomputation. One explain contract, two faces today; the web page in P9 will be
the third.

## Honest about what changed

The old eval baseline is archived, not compared against. The new harness fixes two metric
defects the audit found (an absent item on an empty list scored as rank 1; nDCG's ideal shrank to
the number of results returned), excludes queries with no right answer from the ranking means,
and reports the arms that produced each first hit. On the fixture corpus the knowledge base
scores recall@10 = 1.00 and MRR = 1.00, all three long-document queries are found through the
semantic arm (the journal could not embed their tails at all), and both no-match queries abstain.
The numbers and the caveats are in `docs/eval/v0.6.0.md`.

What is not in P2: the embedding model is still the one the journal shipped, the fusion weights
are a derivation rather than a measurement, and the similarity floor is a single constant. P3
builds the lab that turns all three into numbers.
