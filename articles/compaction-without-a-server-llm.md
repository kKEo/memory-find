# Compaction without a server LLM

*Phase P8 of the memo-mcp roadmap: compaction and pages. Eval report: `docs/eval/v1.2.0.md`.*

Every memory system eventually faces the pile. Hundreds of passages about one thing, three
versions of one fact, two spellings of one name. The usual answer is to let a language model
inside the server consolidate: summarise, dedupe, resolve. memo-mcp has no model in the server
and no network, by decision. This phase is about getting the benefits of consolidation anyway,
and about keeping the raw material untouched while doing it.

## The agent writes, the server checks

The division of labour is simple. The server knows the data: which entity has many passages and
no page, which page's sources changed, which two facts disagree, which two names are nearly the
same. The calling agent has the language model. So the server produces *work items* with
everything attached, and the agent produces text or decisions. `compact` is the scan;
`submit` is the hand-back.

A page item for the Ledger Store carries the three passages that mention it, each with its
address, the one recorded fact about it, the previous page if there is one, and a sentence
saying why the item exists. The agent writes markdown, cites passages as `memo://chunk/<n>`,
and submits. The server stores the page with `is_inference = 1`, records every cited chunk as a
source, and bumps the build number. That is the whole protocol.

## Honesty checks without a judge

The dominant failure of consolidation, in the literature the research document cites, is
omission: the summary quietly drops a fact. The second is invention. Both are usually caught, if
at all, by asking another model. memo-mcp cannot do that, so it does the string-level version
and says so.

The *omission check* takes the recorded facts about the page's subject and asks whether most of
each fact's content words appear in the page (75%, on five-letter stems). The *corruption check*
takes each page sentence of eight or more content words and asks whether at least half of them
appear in some single source passage or fact. Neither understands meaning. Both catch the cases
that matter in practice: in the eval, a page that left out "closes the books on the first
business day" was flagged on exactly that statement, and a page with an added sentence about
Fortran and Reykjavik was flagged on exactly that sentence. `submit` with `dry_run` returns both
lists and a line diff before anything is written, and the agent's skill file tells it to read
them.

## Stale by construction

A page cites the chunks it came from, and that citation is a dependency. When a document gets a
new revision, every page built from its chunks is marked stale with the reason; when a document
is forgotten, the same. Search at `granularity: page` returns the flag, `read` shows it, `lint`
lists it, and `compact` turns it into a rebuild item that carries the new passages and the old
page. Nothing expires on a timer and nothing is recomputed in the background; staleness is a
fact about the data, recorded when the data changes.

## Conflicts: the rule is stated, the human can disagree

Two live facts about one subject whose validity windows overlap and whose statements differ are a
contradiction. The conflict item carries both facts and the rule the server would apply: higher
trust wins, then the more recently recorded. The agent may pick either, but the trust rule is
enforced on the way back: a tool-channel submission cannot invalidate a fact more trusted than
its cap, or more trusted than the winner. The loser is invalidated with `superseded_by`, never
deleted; `as_of` still shows it. In the eval, the attempt to keep the agent's "three nodes" over
the user's "five nodes" was refused; the other way round applied.

## What never changes

The research document's first decision is to keep the raw material. The eval makes that a
checksum: a SHA-256 over every source, document and chunk row before the loop (a page write, two
dry runs, a conflict resolution) and after. They are identical. Of the fact rows, exactly one
changed, and it is the invalidation the agent asked for. Near-duplicate passages are reported as
work items and nothing more; if a human wants one gone, `forget` with a reason is the way.

## The recurrence trigger

Compaction has to be bounded or it eats the agent's budget. The trigger is the one the
research document borrows from RecMem: only clusters that keep growing deserve work. An entity
gets a page item at three live passages and a new one only after two more arrive. A page that is
up to date and whose subject is quiet is never re-proposed.

## The optional local model

`memo-mcp compact --executor ollama` writes page items with a model running on the machine,
loopback only unless told otherwise, with constrained JSON output and a non-thinking prompt. It
goes through the same `submit`, the same checks and a dry run by default. It is a convenience
for a human at the terminal; no other code path knows it exists, and it was not exercised
against a live model in this phase because none was installed.

## What was left out

Louvain community pages: the rule in the roadmap was "only if the eval gains global questions",
and it has not. Topic and overview page kinds exist in the schema and nowhere else yet. Page
quality itself is not measured: the checks bound it from below, which is what a server without a
model can honestly do.
