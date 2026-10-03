# Documentation index

What is current, what is history, and where to start. Dates are the last substantive revision.

## Live documents

| Document | What it is | Updated |
|---|---|---|
| [`roadmap.md`](roadmap.md) | The plan: ten phases from "reset the map" to an explainable local knowledge base, with decisions, spikes, verification and a glossary. Start here. | 2026-10-02 |
| [`knowledge-base-sota.md`](knowledge-base-sota.md) | The research behind the roadmap: agent memory systems, graph RAG, retrieval, MCP, security, with the decision drivers (D-A … D-O) the roadmap cites. Long and technical; the roadmap's glossary defines its terms. | 2026-10-02 (links) |
| [`architecture.md`](architecture.md) | The 1.0 contract: layers, the write and read paths, every formula and default, the explain field reference, the address scheme, the export front matter. | 2026-10-02 |
| [`schema.md`](schema.md) | The knowledge-base schema in plain words: layers L0–L5, every column, the address scheme, trust transitions, `as_of` rules, the migration plan. Signed off before migration 1 was coded. | 2026-10-02 |
| [`eval/`](eval/) | One report per tag (`v0.6.0` to `v1.2.0`) from `memo-mcp eval`: quality next to cost, what moved and why. | 2026-10-02 |
| [`spikes/`](spikes/) | One note per spike (S1 go-sdk bump, S2 vector storage, S3 FTS5, S4 embedding models, S5 tree-sitter, S6 graph, S7 elicitation, S8 reranker): the question, the number, the decision. | 2026-10-02 |

## History (superseded, kept for the record)

| Document | Why it is here |
|---|---|
| [`history/go-implementation-plan.md`](history/go-implementation-plan.md) | The original port plan, written before the project was renamed. Stale paths, tool count and search design; kept for the design rationale. |
| [`history/graphrag-evolution-plan.md`](history/graphrag-evolution-plan.md) | The 2026-09 GraphRAG plan. Replaced by the research document's graph-as-index approach and roadmap phases P7–P8. |
| The 2026-09-04 review (`git show 375f24c:docs/review-roadmap.md`) | Found that search returned nothing on real data, listed defects D1–D17; its Phases 0–1 shipped the same day. Summarised in `roadmap.md` §4. |

## Elsewhere in the repository

- [`../README.md`](../README.md): what the binary does today; [`../CHANGELOG.md`](../CHANGELOG.md): what each tag changed.
- [`../articles/`](../articles/): one article per finished phase, from the measurement foundation to designing tools for agents.
- [`../SKILL.md`](../SKILL.md): how an agent should use the knowledge base (the search-then-read loop, scoping, trust).
- [`../slides.md`](../slides.md): the teaching deck (Part 2 rewritten in P7 around the graph-as-index results).
