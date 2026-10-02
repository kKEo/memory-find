# Designing tools for agents

*Phase P5 of the memo-mcp roadmap: the agent surface. Protocol-only; the eval numbers from
v0.8.0 are unchanged.*

A knowledge base for agents is judged by a reader that cannot ask follow-up questions, never
scrolls, and pays for every token twice: once to read it and once to carry it forward. This
article is about the small set of decisions that follow from that reader.

## Seven tools, each with an example

Tool-selection accuracy drops as the number of tools grows, and rises when the description
shows a call. memo-mcp has seven tools: `ingest`, `search`, `read`, `remember`, `forget`,
`promote`, `status`. Every description now ends with one or two literal calls. The examples
are not decoration: in the vendor evals the research document cites, examples in descriptions
moved selection accuracy from 72% to 90%. They also pin the vocabulary, so an agent writes
`scope.version` rather than inventing `version_filter`.

A test keeps the README's tool table equal to the set of tools in the golden `tools/list`
file, so documentation and protocol cannot drift apart without a failing build.

## The budget footer is part of the answer

`search` packs results to `max_tokens`. Before this phase, the count of results left out lived
only in the explain trace, which most calls do not ask for. Now every response carries
`truncated` and `narrow_hint`, and the plain-text mirror ends with a sentence such as:

```
3 more result(s) did not fit the token budget; narrow with scope.version, or raise max_tokens.
```

An agent that reads only the text still learns that the list was cut and what to do about it.
The rule: anything the agent must act on goes in every format, not only in the diagnostic one.

## Small-to-big, in one call

`response_format: detailed` used to return the matched passage. It now returns the passage
framed by its neighbours, marked `[…before:]` and `[…after:]`. This is the small-to-big idea
from the retrieval literature applied at the protocol level: match on a small unit for
precision, show a larger one for comprehension, without a second round trip.

## Resources are addresses, tools are actions

Every result carries a `memo://` address. Those addresses are now MCP resources as well as
`read` arguments: `memo://doc/{id}`, `memo://chunk/{id}`, `memo://source/{id}`,
`memo://fact/{id}`. A client that supports resources can dereference a citation without a
tool call, and the content is the same text `read` would return, provenance header included.

Two index resources give an agent a shelf map at the start of a session: `memo://index` and
`memo://ns/{namespace}/index`, one line per document and fact, cut to 8 KB with a footer
counting what was left out. The same text comes out of `memo-mcp export --index`, so a human
can paste it into `AGENTS.md` or `CLAUDE.md`. Eight kilobytes is the size that scored 100% in
the AGENTS.md evaluation the research document relies on, against 53% for the same facts as
a skill.

Cache hints travel with the responses: the tool list for an hour, resource reads for a minute.
Long enough that an agent's turn does not re-fetch, short enough that a new revision shows up
soon.

## Asking the human without asking the model

`promote` raises a record's trust from `agent` to `user` or `curated`. A model must not be
able to do that alone, or a poisoned page can promote itself. The P4 design returned a CLI
command for the human to run. P5 wires the question to the client.

The first attempt used the SDK's `Elicit` call from inside the tool handler. Protocol
2026-07-28 refuses that: a server cannot open a request while serving one. The replacement is
the multi round-trip request (SEP-2322). The tool returns no content, only an input request
with the dialog text and a request state; the client shows the dialog, and retries the call
with the answer attached. The handler then sees the answer, checks that it belongs to this
exact promotion, and applies or declines. On a client without the capability the tool skips
the dialog and returns the command, as before. On an older client that lacks the retry
protocol, the SDK's middleware performs the elicitation itself and re-invokes the handler.

What the human sees matters as much as the mechanism. The dialog shows the record's first
lines, its source URI, its declared origin and its current and target trust, and ends with
"Accept only if you vouch for this content yourself." A dialog that says only "Confirm?"
would train people to click yes. One caveat is unavoidable and is in the README: a hook that
auto-accepts elicitation dialogs removes this protection entirely.

## Teaching the agent in text a human can read

`SKILL.md` is a page of instructions for the agent: orient with `status` or `memo://index`,
search concise then read, scope before widening, trust the abstention, pass `exclude_ids` on
the second page, record what you learned, never describe `agent` records as verified. It is
written so a human can check it, because the instructions an agent follows are part of the
system's behaviour and deserve the same review as code.

## Closing the loop with the log

`memo-mcp log replay` prints the opt-in query log as unlabelled eval candidates, one JSON
object per line with the query, scope and the addresses that came back. Real queries are the
best source of new eval fixtures; labelling them is a human task, and the output is shaped so
that task is a few minutes, not an afternoon.

## What is still open

A live check in Claude Code of the dialog rendering and of resource-template resolution has
not been run; spike S7 records the SDK-level results and leaves that line open. Completions for
enum parameters remain an optional item. Prompts are not planned: everything a prompt would
say is in `SKILL.md`, where a human can read it.
