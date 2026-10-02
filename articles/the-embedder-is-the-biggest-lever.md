# The Embedder Is the Biggest Lever

*Roadmap phase P3, "The lab": how memo-mcp measures itself, what the first bake-off showed, and
why the default model changed.*

## Why build a lab before building features

Every retrieval change so far has been judged by one number set: a fixture corpus of 79 notes,
29 labelled questions, and a recorded baseline. That was enough to prove the journal's search
returned nothing and to prove the rebuild fixed it. It is not enough to choose between two
embedding models, or to decide whether a reranker earns its latency, because the notes are
lexical by construction and the hash embedder that makes CI fast has no notion of meaning.

P3 adds the second half of the instrument. A second corpus: 300 generated API pages for a
fictional library that differ only in an identifier and an error code, a five-note namespace
about cooking that must not drown among them, one documentation page in two versions at the
same URL, three long handbooks whose only distinctive sentence sits in the appendix, four notes
on one topic written over two years, and questions phrased in other words than their answers.
Categories (exact, version-pinned, cross-namespace, long-document, recency, abstention,
paraphrase, multi-hop) so a regression says *where* it happened. Cost columns next to every
quality column. A gate that compares query by query, because the old mean-only gate could hide a
single question going from found to lost. And `memo-mcp eval`, which loads it all into a
throwaway knowledge base with any model and any profile and prints the table.

## Every constant now has a sentence

Ranking has knobs: the fusion constant, the arm weights, how deep each arm fetches, the recency
half-life, the gap at which the result list ends, the similarity below which a semantic-only hit
is noise. The journal had them as unexplained literals. They are now *profiles*, and
`memo-mcp profiles show` prints each constant with its meaning and a paragraph on why it has
the value it has. There are seven: `default`, `precise`, `recency`, `code`, `minmax` (score
fusion instead of rank fusion) and the two ablations `keyword-only` and `semantic-only`, which
exist so the eval can show what each arm contributes. A JSON file can override any of them.
None of this is exposed to the language model; the tool schema has eight parameters and no
weights, because tool-selection accuracy falls as surfaces grow.

## Which models even run

Before measuring quality we measured something humbler: which ONNX exports load at all on
hugot's pure-Go backend, and how fast. Six candidates. Two failed outright: EmbeddingGemma's
quantised export uses a `DequantizeLinear` form the backend does not implement, and
snowflake-arctic's 1.2 GB graph does not parse. Four ran. MiniLM, the incumbent: 593 ms per
passage. granite-small-r2: about 2 s. granite-r2: 7 s. And potion, a model2vec static model that
is a lookup table rather than a network, read straight from its safetensors file by about 200
lines of Go: 0 ms.

The original rule for a new default said "150 ms per passage". No ONNX model on this backend
comes close, including the one we already shipped. Rules written before measurement get
rewritten after it: the bar became *no slower than twice MiniLM at query time, and at least 0.02
better nDCG*.

## What the bake-off showed

On the notes corpus every model ties (nDCG 0.97–0.98). Words carry it; the embedder barely
matters. On the knowledge-base corpus the picture splits. MiniLM and potion score zero on both
paraphrase questions ("keeping a bread culture alive" → the sourdough note). granite-small-r2
finds both, and lifts recall@5 from 0.88 to 1.00. The hybrid beats either arm alone with every
real model: semantic-only loses 2–20 points, keyword-only never finds a paraphrase. Score fusion
(`minmax`) edges out rank fusion by 0.03 on the knowledge-base corpus and is a wash on notes:
suggestive, not decisive, so RRF stays the default and the alternative is a flag.

Two findings were not about ranking at all. Granite's similarity scale is different: unrelated
text scores about 0.6 where MiniLM scores 0.04, so with MiniLM's floor granite never abstained
and every no-match query returned a page of noise. Bands now belong to the model, and with its
own bands granite abstains on both no-match queries at no cost. And the cross-encoder reranker,
the second lever the research promised, lost about 0.2 nDCG everywhere while costing 0.7 to 4
seconds per query. A probe shows it does discriminate, but all its scores sit near zero: the
pipeline cannot pass the sentence-pair segment ids a cross-encoder was trained with. It fails its
gate and stays opt-in.

## The decision

granite-small-r2 passed the recalibrated rule: Apache-2.0, loads, +0.05 nDCG on the knowledge
base, 1.6× MiniLM's query latency. It costs a 195 MB first download instead of 87 MB and
ingestion 2.5× slower. The owner chose it as the default. A knowledge base built with MiniLM
keeps working by keyword, reports itself degraded, and is re-embedded in the background the next
time the server starts; `MEMO_MODEL=minilm` or `potion` is one line in a config for anyone who
prefers speed.

That is what "the lab" means in practice: the numbers are in `docs/eval/v0.7.0.md`, the method
is `memo-mcp eval`, and the next model that wants to be the default has to beat this one on the
same table.
