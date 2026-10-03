# S6 — CSR adjacency and personalised PageRank at 100k nodes

**Question.** Can the graph arm run in memory, in pure Go, within the roadmap budget (PPR under
50 ms at 100k nodes and 1M edges; adjacency load under 500 ms)? Should the SQL fallback use
fixed-depth joins or a recursive CTE? Is a Go Louvain available?

**Run.** 2026-10-02, `make spike NAME=s6-graph` (`spikes/s6-graph/main.go`, build tag `spike`).
Synthetic power-law graph by preferential attachment, 100,000 nodes, 1,000,000 undirected
edges, maximum degree 2,183. Apple Silicon laptop.

**Numbers.**

| Measure | Result | Budget |
|---|---|---|
| CSR build from an edge list | 10 ms, 9 MB | < 500 ms |
| PPR, 20 power iterations, 3 seeds, hub cap 200 | 56 ms | < 50 ms |
| PPR, 10 iterations | 42 ms; top-20 identical to 20 iterations | < 50 ms |
| Hub cap on versus off | no measurable cost | — |
| SQL two-hop neighbourhood of a hub (200k edges in SQLite) | fixed-depth joins 40 ms; recursive CTE 46 ms; same 7,961 nodes | — |

**Decision.** In memory, per namespace, lazily built and cached: a knowledge base's namespace
graph is thousands of entities, not 100k, so the arm runs in single-digit milliseconds there and
stays under the budget even at the synthetic scale with 10 iterations (which ranks identically).
Fixed-depth joins for `explore` (hops 1–2); no recursive CTEs. Louvain: `gonum.org/v1/gonum/graph/community`
exists, but it is not added now (roadmap P8 asks for it only if the eval gains global questions);
no dependency added in P7.
