# S5 — Pure-Go tree-sitter for code chunking?

**Question.** Should code files be split by syntax (functions, classes) using a pure-Go
tree-sitter port, or is the heading- and fence-aware splitter enough for now?

**Run.** 2026-10-02, P1. Not executed as a benchmark: the two candidate ports
(`odvcencio/gotreesitter`, `malivvan/tree-sitter` on wazero) were not pulled into the module,
because nothing in P1 or P2 needs syntax-aware chunking and each port is a large dependency
whose maturity the research document flags as unverified.

**What P1 does instead.** `internal/chunk` never splits a fenced code block, keeps the fence's
language tag on the chunk (`lang`), and splits prose by headings, paragraphs and sentences. A
file ingested with `kind=code` is treated as one fenced block per file in P1 (the CLI wraps it),
so a function is never cut in half, and an over-long file falls back to the embedder's halving
backstop rather than to a syntax split.

**Decision.** **Defer (OD-17: no tree-sitter yet).** Revisit when the P3 eval gains its
`version-pinned` and `exact` code slices: if recall on code identifiers is the weak category, run
the real spike (parse Go/TypeScript/Python samples with both ports, measure binary growth against
the 10 MB bar) and add `go/parser`-based splitting for Go first, since it needs no new
dependency. Until a number says otherwise, the simpler splitter stands.
