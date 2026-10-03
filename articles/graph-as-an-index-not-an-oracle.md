# Graph as an index, not an oracle

*Phase P7 of the memo-mcp roadmap: entities and graph. Eval report: `docs/eval/v1.1.0.md`.*

Graph retrieval has a reputation problem. The pitch is seductive: extract entities and
relations, build a knowledge graph, let the structure answer questions the text cannot. The
measurements are sobering: on public benchmarks a full graph pipeline barely beats plain keyword
search, costs a hundred times more to build, and hurts the simple questions that make up most
traffic. This phase set out to get the part that works, measure it honestly, and keep the rest
off.

## What "index, not oracle" means

The graph never answers anything. It only points at text. Every entity is linked to the passages
that mention it, and that link, a *mention*, is the only edge type the search uses. There are no
"X depends on Y" triples in the ranking, because extracting them reliably needs a language model
and the research document's reading of the literature is that mention edges alone recover most of
the multi-hop gain at zero model cost. Typed edges exist as a table the client can fill, and
`explore` shows them, but they do not score.

Everything in the graph layer can be deleted and rebuilt from the passages. That is what makes
it an index: it is derived, disposable and auditable.

## Finding the things a passage talks about

Extraction is a ladder with no mandatory rung. The first rung is heuristics that run on every
ingest without any model: backticked spans, dotted and camel-case and snake-case identifiers,
headings, and capitalised spans that are not sentence starts or ordinary words. On the two
evaluation corpora this produced 1,051 entities from 322 documents. The second rung lets the
client that ingests a document declare `entities[]` and `relations[]` it already knows, and lets
`remember` name what a fact is `about`. A small named-entity model is the third rung and a local
language model the fourth; neither is built, because neither has yet been shown to be needed.

## Deciding two names are one thing

"Client.Connect" and "client.connect" are the same thing. "Pipeline Scheduler" and "Pipeline
Schedulers" probably are. "quorum.Publish7" and "quorum.Publish10" are not, and neither are
"Quorum Replication" and "Quorum Replica Count". Resolution is deterministic and asks before it
merges: an exact match on the normalised key merges; a near match on character 3-grams (found
quickly with MinHash and locality-sensitive hashing, confirmed with exact Jaccard above 0.8) goes
into a review queue; names too short or too uniform to compare safely stay separate.

The first version of the queue had 213 entries, one of them correct. Every other pair was two
numbered identifiers from the synthetic library: Publish1 and Publish10 share most of their
trigrams. The fix is a rule a human would state without thinking: names that differ in a number
are different things. The queue then held two pairs, both correct. That is the merge-precision
test in the suite now, and the threshold came down from the roadmap's 0.9 to 0.8 because a
plural suffix on a two-word name scores 0.86.

## Two arms

The *entity arm* is one hop: the query names a known entity, the arm returns the passages that
mention it, scaled down for entities mentioned everywhere. The *graph arm* walks further. For
each entity the query names it runs a personalised PageRank over the namespace's mention graph,
a random walker that keeps returning to its starting entity, and scores each passage by the
product of what every walk leaves on it. The product is the point: a passage reachable from
every named entity (a bridge) beats a passage reachable from one, and passages that every named
entity mentions directly are left to the entity arm, so the graph arm reports only what one hop
cannot see.

The walk runs in memory on a compressed adjacency built per namespace and rebuilt when the
mention count changes. Spike S6 measured 10 ms to build and 42 ms to walk a synthetic graph of
100,000 nodes and a million edges; a real namespace is thousands of nodes, and the measured cost
on the evaluation corpus is a fifth of a millisecond.

## The lookup regression, and why routing is the design

The first wiring ran the entity arm on every query. Three plain lookups in the notes corpus got
worse. "postgres query planner sequential scan index" names an entity, "Postgres", because a
capitalised word in prose is an entity, and the passages that mention Postgres outvoted the
passage that answers the question. This is the finding the research document cites as the reason
graph retrieval must be routed: structure helps relational and multi-entity questions and hurts
simple ones.

So both structural arms join the fusion only when the question names two or more known entities,
or names one and asks a relational question ("relate", "between", "depend", "connect", "differ",
"how … and …"). The trace says when and why. `mode: graph` runs the structural arms alone, as
the ablation.

## What the numbers say

Three chains of services were planted in a new namespace: the Billing Service writes to the
Ledger Store, which is replicated by Quorum Replication, which needs an odd node count. No page
names the whole chain, and a decoy page mentions the first entity only. With the 1.0 arms the
multi-hop slice scores 0.43 nDCG; the entity arm raises it to 0.51; the graph arm to 0.68, with
the single-hop slices unchanged. The pre-registered gate asked for 0.05 and got 0.17, so the
graph arm is on by default for routed queries.

The honest half of the result: when a question names two entities, the pages *about* those
entities also match by words and by meaning, and rank fusion keeps them ahead of the bridge
page the graph found. The gain shows in recall at five, which went from 0.63 to 1.00 on the
slice, and in the single-entity relational question, where the walk is the only arm that reaches
the answer. Getting the bridge to rank one would need the fusion to trust agreement between
walks over agreement between text arms. That experiment is written down as a 1.x item rather
than tuned into the weights on three planted queries.

## Two bugs the graph found

Adding arms meant reading the fusion loop closely, and two things fell out that had nothing to do
with graphs. The fact arm, shipped in 0.8, had no weight in the default profile: the derivation
text said 0.4, the map did not, and facts had been voting with weight zero. With the weight on, a
fact that shared a single common word with a query ("context") could outrank the right passage,
so a keyword-only fact match now has to cover at least half the query's content words. Both are
in the eval report, both have tests, and the notes corpus moved by 0.004 as a result.

## What was not built

No language model extracts anything. No typed edge is scored. No community detection, no
summaries, no global graph across knowledge bases. Each is a line in the roadmap with the
measurement that would justify it, and the evaluation harness now has the slice that could show
it.
