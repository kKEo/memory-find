# Keeping it tidy

A memory that grows for months collects loose ends:

- facts that contradict each other;
- two names for the same thing, such as "Postgres" and "PostgreSQL";
- near-duplicate notes;
- topics mentioned in many places but never summarised.

memors-mcp finds these. It does not fix them on its own. Claude does the writing, and you decide
anything that matters.

## Asking Claude to tidy up

> Tidy up the memors knowledge base.

Claude asks memors-mcp for a to-do list. Each item comes with the material needed to handle it:

- **Write a summary page** for a topic mentioned in many places. Claude writes it from the
  saved passages. Each sentence points back to its source.
- **Refresh an out-of-date page.** A summary is marked *stale* when a document it was based on
  changes.
- **Resolve a contradiction** between two facts by keeping the right one.
- **Merge two names** that mean the same thing.

## Checks on every summary

When Claude hands a summary back, memors-mcp checks it before keeping it:

- **Did it leave anything out?** Important facts about the topic that the page does not
  mention are reported.
- **Did it make anything up?** Sentences that the sources do not support are flagged.
- **What changed?** For a refreshed page, you see the difference from the previous version.

Summary pages are always labelled as written by Claude, with their sources listed, so they
never pass for original material.

## Decisions that stay with you

Merging two names and settling contradictions affect what the memory treats as true. Claude can
propose them, and you can confirm them in the conversation or from the terminal. Nothing is
merged silently.

> **Tip.** Once a month, ask "Is there anything in memors that needs my decision?" Claude will
> list the open items.
