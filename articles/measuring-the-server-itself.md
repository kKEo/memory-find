# Measuring the server itself

*Phase P10 of the memo-mcp roadmap: observability. Eval report: `docs/eval/v1.4.0.md`.*

For nine phases the project measured retrieval quality: recall, MRR, nDCG on labelled corpora,
a paired gate on every change. Nothing measured the running server. Diagnostics were a dozen
`fmt.Fprintf(stderr, …)` lines, the only timing that reached anyone was the opt-in query log,
and the server advertised an MCP `logging` capability it had never used. This phase adds
metrics, structured logs and a per-call log, under a constraint that shaped every decision:
nothing leaves the machine.

## Not telemetry

The README has said "no telemetry" since the first commit, and the roadmap lists telemetry
under what not to build. Observability does not contradict that; the two words are easy to
confuse, so the project now says what it means. Telemetry is data sent somewhere. What this
phase adds is pulled, from a loopback address, by whoever runs a scraper on the same machine;
logs go to the process's own stderr; the call log is a table in the user's own SQLite file. The
privacy sentence gained a clause instead of losing one.

## Why a registry in 300 lines

The usual choice for a Go program is `prometheus/client_golang`, or the OpenTelemetry SDK with a
Prometheus exporter. Both would be the largest dependencies in the binary after the embedding
runtime, and the project's promise that a reader can read every line it runs would end at a
vendored protobuf package. So `internal/obs` is a registry of counters, gauges and histograms
with labels, rendered in the Prometheus text format, in a few hundred lines of standard library.
Counters are atomic floats; histogram buckets are atomic counters; label vectors are maps under a
read-write lock; the text writer sorts families and series so the output is stable enough for a
golden test. Go's own `runtime/metrics` supplies goroutines, heap, GC cycles and pauses under
the names the standard Go collector uses, so existing dashboards work. A small parser in the test
suite checks that every sample line is well formed and that every histogram's `+Inf` bucket
equals its count.

Naming follows the Prometheus conventions, `memo_` prefix, `_total` for counters, seconds and
bytes as units, and maps one to one onto OpenTelemetry names by swapping underscores for dots.
If a bridge is ever wanted, nothing is renamed.

## Three processes, one caveat

The MCP server, the web UI and the CLI are separate processes, and a counter lives in the process
that incremented it. That is the fact the design had to respect. The serving process is the
scrape target: `memo-mcp serve --metrics-addr 127.0.0.1:9469` starts a loopback-only listener
that answers `GET /metrics` and nothing else, refuses any non-loopback address with no override,
and checks the Host header so a page on another origin cannot reach it through DNS rebinding.
The UI serves its own `/metrics` for its own requests. `memo-mcp metrics` is honest about what a
fresh process can know: it prints the table-count gauges, which it reads from the file, and the
per-tool statistics from the opt-in call log, and then says in plain words where the live
counters are.

## What is counted

One receiving middleware sees every MCP request. For a tool call it times the call, reads the
outcome off the wire result (ok, tool error, input required for an elicitation, transport
error), estimates the tokens in the returned text, and writes one log line. A thin wrapper
around each typed handler sees the Go error before the SDK folds it into an `IsError` result, and
classifies it: not found, forgotten, needs a human, cancelled, invalid arguments, internal. The
search service already computed everything a dashboard would want in its `Trace`; a single
`finish()` at the end of every search turns it into metrics (mode requested and resolved, latency
per arm, candidates per arm, cutoff kind, truncation, abstention reason, degraded reason) and one
log line. The store counts every audited write by operation and channel in the one function all
writes pass through, and a collector exports the table counts as gauges, read at scrape time and
cached for five seconds so a scraper cannot turn into a load. Every real embedder comes out of
`Load` wrapped, so latency by model and role, batch sizes and download events are counted without
touching any caller.

## Logs that say one thing per line

The stderr prints became `log/slog` with a text or JSON handler and a level from the
environment. One Info line per tool call and per search carries the fields an operator asks for
first: which tool, which client, how long, what came back, what went wrong. Degraded modes,
backfills, model downloads and licence notes are warnings. The SDK's own chatter is kept at warn
and above. A test swaps `os.Stdout` for a pipe, runs a tool round trip with debug logging, and
asserts that zero bytes arrived: in serve mode stdout is the protocol stream, and a stray print
there would corrupt it.

## The capability nobody used

The go-sdk advertises `logging` when a server does not set its capabilities. memo-mcp never sent
a log message over MCP, protocol 2026-07-28 deprecates the mechanism, and the research document's
decision D-O says not to use it. The owner was asked whether to forward logs to the client
through the deprecated path anyway; the answer was no. The server now sets its capabilities
explicitly, the test suite asserts that `logging` is absent, and the roadmap records D-O as
resolved.

## The call log

`MEMO_QUERY_LOG=1` already recorded every search. Migration 4 adds a sibling table for every
tool call: client, tool, latency, outcome, error class, result count, tokens out, and a summary
of the arguments built from an allowlist per tool. The summary carries queries, scopes,
addresses and sizes; it never carries the content of an ingest, the statement of a fact or the
reason for a forget. `memo-mcp log calls` lists it, `log tail` shows searches and calls side by
side, the UI `/log` page renders both tables, and pruning covers both at ten thousand rows or
thirty days.

## What was not built

OpenTelemetry export and traces. The metric names are ready for a bridge; the bridge is not
worth its dependencies until someone needs it. Screenshots of a dashboard: none was set up in
this session, and the README shows the `curl` instead.
