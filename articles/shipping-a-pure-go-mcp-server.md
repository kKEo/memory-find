# Shipping a pure-Go MCP server

*Phase P6 of the memo-mcp roadmap: 1.0. The eval numbers are unchanged from v0.9.0; this phase
is about the contract and the pipeline.*

A 1.0 is a promise about what will not change. For memo-mcp that promise covers four things:
the seven tool names and their parameters, the `memo://` address scheme, the names of the fields
in `why` and `trace`, and the front matter of the markdown export. Everything else, including
the ranking constants, may move, and the eval reports say when it does.

## Why pure Go made this easy

The binary has no C in it. SQLite is `modernc.org/sqlite`, a translation to Go; the FTS5 and
sqlite-vec extensions ride along; the embedding models run through hugot's GoMLX backend, which
executes ONNX graphs in Go. The practical consequence is that `CGO_ENABLED=0 go build` produces
a working binary for any platform Go supports, from any platform, in one step. The release
pipeline is therefore small: GoReleaser cross-compiles five targets (macOS and Linux on amd64 and
arm64, Windows on amd64), strips them, stamps the version from the tag, archives them with the
licence, the README and `SKILL.md`, and writes a checksum file. `make snapshot` runs the same
build locally without a tag; it took a few seconds once the build cache was warm.

The cost of pure Go shows up elsewhere: embedding is slower than a native runtime (about two
seconds per passage with the default model on a laptop), and not every ONNX operator is
implemented, which is why the model bake-off in P3 tested loading before it tested quality.
Those costs were measured and accepted in P3; they do not change how the binary ships.

## CI that never downloads a model

Pull-request CI runs on three operating systems and does three things: format and vet, the
race-enabled test suite, and a `CGO_ENABLED=0` build followed by `memo-mcp version`. Every test
uses a deterministic hash embedder, so no job downloads a model and no job depends on Hugging
Face being up. Lint runs as its own job on Linux, so a style finding does not hide behind a
Windows path problem. A fourth job runs the release pipeline in snapshot mode and counts the
archives, so a broken `.goreleaser.yaml` fails the pull request that broke it, not the release.

The real model is measured nightly instead. That workflow caches the model directory between
runs, runs `memo-mcp eval` with both the hash embedder and the default model, and uploads the
markdown report as an artifact. It is informational: the hash-embedder gate, compared query by
query against the recorded baseline, is what blocks a merge.

## The registry

The MCP registry lists servers by a name whose ownership is proved by a marker in the README
(`mcp-name: io.github.kKEo/memory-find`) and by logging in from the repository's own GitHub
Actions with an OIDC token, so no secret is stored. The release workflow fills `server.json`
with the tag and the checksum of the Linux archive, then publishes. The manifest describes the
binary as an `mcpb` package with a stdio transport and documents the three environment
variables a client sets: `MEMO_KB`, `MEMO_HOME`, `MEMO_MODEL`. The first tagged run is the real
test of this; the roadmap status note says so.

## The contract, written down

`docs/architecture.md` is the file a reader opens to learn how search decides without reading
Go. It has the worked RRF example (`0.5 / 61 + 0.5 / 62 = 0.016262`), the recency formula with
its floor and half-life, the cutoff rule, the abstention rule, the per-model relevance bands,
and a table for every field of `why` and `trace`. It also lists what 1.0 deliberately does not
include: HTTP transport, a server-side language model, prompts, importing the old journal files.
Writing it surfaced one small inconsistency, which was fixed before tagging: the `ls --json`
output used Go field names while every other JSON surface used snake_case.

## The clean-machine scenario

The exit criterion that matters most is the one a stranger would run. With a fresh home
directory: ingest a docs folder at `v1.0`, ingest the changed folder at `v2.0`, ask a question
pinned to each version, read the citation, forget the newer document, confirm the pinned search
now returns nothing and the address explains why, export to markdown, grep for the passage. It
ran end to end on 2026-10-02; the transcript is summarised in `docs/eval/v1.0.0.md`. The one
hiccup was in the test script, not the product.

## What a patch release looks like

Nothing in this pipeline is special to 1.0. A fix is a commit, a tag `v1.0.1`, and the same
workflow: tests, five archives, checksums, a registry entry with the new version. The eval
report for the tag is written before the tag. If a release is bad, the next tag supersedes it;
nothing is edited in place.
