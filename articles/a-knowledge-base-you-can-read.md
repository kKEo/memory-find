# A knowledge base you can read

*Phase P9 of the memo-mcp roadmap, the optional last one. Eval report: `docs/eval/v1.3.0.md`.*

Everything before this phase was built for an agent or for a terminal. The explain output,
the trace, the facts with their two clocks, the pages with their sources: all of it exists as
JSON and as monospace tables. This phase puts a browser window on the same store, for the
person who wants to see what the agent has been told and why it was told that.

## The rule: one ranking, three faces

The temptation with a web UI is to make it its own thing: a friendlier search, a different
ordering, a summary. memo-mcp refuses that. The UI calls the same `retrieve.Service` the MCP
server and the CLI call, with the same request shape (explain format, ten results, document
granularity), and renders the same `Why` and `Trace` structs. A test runs one relational
query through the service and through the UI, compares every address and every score, then
parses the rendered HTML and compares the five-digit numbers in the rows. If a human and an
agent disagree about a result, they are looking at the same arithmetic.

What the UI adds is legibility. The explain breakdown that is a wall of JSON for an agent is,
for a person, a small table per result: arm, rank, raw score, contribution, matched terms, then
the fused score times the recency factor. Under the results sits the trace: which arms ran and
why, how many candidates each produced, what the scope excluded, where the list was cut, how
long each arm took. The routing reason is a sentence, and for a question that names two
entities it says so.

## What is on the pages

The home page is the status: counts, namespaces, recent documents, recent agent-written pages.
A document shows its provenance line, its content, its passages with token estimates, and its
history: the revision chain with what superseded what and when, and whether anything was
forgotten. A passage shows the ones before and after it and the entities it mentions. Facts are
a timeline with a date box: type a date and the page shows what the knowledge base believed
then, superseded facts struck through if history is on. An entity shows its passages, the facts
about it, its page if it has one, and what is mentioned alongside it, with the evidence
addresses. A page says it is derived, who wrote it, which revision of the sources it was built
from, and in red if it has gone stale. Lint, the query log and the eval report have their own
pages.

No JavaScript was written. Server-rendered templates from the standard library, one stylesheet,
forms that submit with GET. A page without scripts has nothing to run, nothing to update, and
nothing to exploit.

## Why it came last, and what it refuses

A web server is the easiest place in a system to make a security mistake, which is why the
roadmap put this phase after the read API had stabilised and made it optional. The server has
no mutating route: POST, PUT and DELETE get a 405 with the allowed methods. It binds to
loopback and refuses any other address unless told `--allow-remote`, because read-only still
means "shows everything in the knowledge base". The Host header is checked against the bound
address and its loopback spellings: a page on another origin that tricks a browser into
resolving its hostname to 127.0.0.1 (DNS rebinding) gets a 403, not the knowledge base. The
content security policy allows the page's own stylesheet and nothing else.

## Two things the browser found

Clicking through the UI against the knowledge base left over from the 1.0 clean-machine run
produced an error on the first page: no such table. That file was written at schema version one,
and a read-only open cannot migrate. The fix is a plain message, "run memo-mcp migrate", and a
`migrate` command that does only that. The second gap followed: documents ingested before the
graph layer existed had no entities, so entity pages were empty. `memo-mcp graph rebuild`
re-extracts mentions for live passages. Neither is a UI change; both are things a terminal user
would have hit eventually and a browser user hit in a minute.

## What was left out

A static HTML export, listed as optional in the roadmap: the markdown export and the UI cover
the two ways a person reads the knowledge base. Screenshots for the README: none were taken in
this session, so the README describes the pages instead. Editing: nothing here writes, by
design, and the roadmap has no phase that changes that.
