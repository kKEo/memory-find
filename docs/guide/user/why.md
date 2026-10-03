# Why it matters

## The problem: an assistant that forgets

AI assistants are good at reasoning and poor at remembering. Each new conversation starts from
zero. So people paste the same background again and again: the project layout, the team's
conventions, the decision made last month and the reason for it. Time goes into re-explaining
instead of working. When the assistant guesses instead, it can sound confident and still be
wrong.

## What changes with memo-mcp

**Answers come with receipts.** Every answer from the memory carries its source: which
document, which passage, who saved it and when. You can open the original in one step. You can
also ask *why* that passage was chosen over the others. The memory explains its own ranking in
plain terms.

**You stop repeating yourself.** Save a document, a decision or a single fact once. Claude
looks it up when it is relevant, in this conversation or one months from now.

**Corrections keep their history.** When a fact changes, such as "we moved from Postgres 15 to
16", the new fact replaces the old one. The old one is kept as history. You can always ask what
was believed on a given date.

**You stay in charge of what counts as reliable.** Anything Claude saves on its own is labelled
as Claude's work. Only you can mark something as checked. Claude can ask, but it cannot do it
itself. See [Trust](trust.md).

**It stays on your machine.** The memory is one file on your computer. It works offline after
a one-time download, and it sends nothing anywhere. See [Privacy and safety](privacy.md).

**It costs nothing to run.** It is free and open source under the MIT licence. It needs no
server, no database to install and no paid service.

## Where it pays off

| Situation | Without a memory | With memo-mcp |
|---|---|---|
| Starting a new conversation on an ongoing project | Paste the background again | Claude looks it up |
| "Why did we choose this?" six months later | Search chat history or ask around | The decision is saved with its reason and date |
| Using a specific version of a library | The assistant mixes up versions | Saved docs are tagged with their version, and Claude looks in the right one |
| A new teammate joins | Weeks of tribal knowledge | The same memory is browsable from day one |
| A fact changes | Old and new answers compete | The new fact replaces the old, and the history stays visible |

More examples are in [Use cases](use-cases.md).

## How it compares to Claude's built-in memory

Claude Code and the Claude apps have their own memory features. For many people those are the
right default, and they need no setup.

memo-mcp is for when you want more than that:

- to **see and check** what the memory holds, in a browser or as plain files;
- to know **why** a particular answer came back;
- to keep **separate memories** for separate projects or clients;
- to keep **a full history** of what changed and when;
- to make sure **nothing leaves your computer**.

The two can be used side by side.
