# Documentation index

What is current, what is history, and where to start. Dates are the last substantive revision.

## Live documents

| Document | What it is | Updated |
|---|---|---|
| [`roadmap.md`](roadmap.md) | The plan: ten phases from "reset the map" to an explainable local knowledge base, with decisions, spikes, verification and a glossary. Start here. | 2026-10-02 |
| [`knowledge-base-sota.md`](knowledge-base-sota.md) | The research behind the roadmap: agent memory systems, graph RAG, retrieval, MCP, security, with the decision drivers (D-A … D-O) the roadmap cites. Long and technical; the roadmap's glossary defines its terms. | 2026-10-02 (links) |

## Planned documents

These are created by the roadmap phases named: `schema.md` (P1), `spikes/` (P0 onward),
`eval/` (P2 onward), `architecture.md` (P6).

## History (superseded, kept for the record)

| Document | Why it is here |
|---|---|
| [`history/go-implementation-plan.md`](history/go-implementation-plan.md) | The original port plan, written before the project was renamed. Stale paths, tool count and search design; kept for the design rationale. |
| [`history/graphrag-evolution-plan.md`](history/graphrag-evolution-plan.md) | The 2026-09 GraphRAG plan. Replaced by the research document's graph-as-index approach and roadmap phases P7–P8. |
| The 2026-09-04 review (`git show 375f24c:docs/review-roadmap.md`) | Found that search returned nothing on real data, listed defects D1–D17; its Phases 0–1 shipped the same day. Summarised in `roadmap.md` §4. |

## Elsewhere in the repository

- [`../README.md`](../README.md): what the binary does today.
- [`../articles/`](../articles/): one article per finished phase; the first is the measurement foundation.
- [`../slides.md`](../slides.md): the teaching deck (Part 2 still shows the superseded GraphRAG plan; its rewrite is scheduled in `roadmap.md` §14).
