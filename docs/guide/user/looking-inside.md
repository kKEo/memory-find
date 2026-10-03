# Looking inside

memo-mcp includes a **browser view** of your memory. It shows everything Claude can see, in
the same order Claude would see it. It is read-only: nothing on it can change your memory, so
it is safe to explore.

## Opening it

In a terminal, using the same memory name as in your Claude setup:

```bash
MEMO_KB=my-project memo-mcp ui
```

It prints an address such as `http://127.0.0.1:54750`. Open it in your browser. The page is
only reachable from your own computer. Press `Ctrl-C` in the terminal to stop it.

## What you can do there

| Page | What it shows |
|---|---|
| **Home** | What the memory holds: counts of documents, facts and summary pages, each shelf, and the most recent documents |
| **Search** | The same search Claude uses, with the "why did this rank here" table for every result |
| **Documents and passages** | The full text, where it came from, who saved it, and every earlier version |
| **Facts** | Every fact with its timeline: when it became true, when it was replaced, and by what |
| **Pages** | Summary pages Claude has written, with the passages each one is based on |
| **Names** (follow a link from a page) | One person, system or term the memory mentions, with the passages about it and related names |
| **Lint** | Loose ends: contradicting facts, out-of-date summaries, expired facts |
| **Status** | Size, settings and background work in progress |
| **Log** | Recent questions and tool use, if you turned the log on |

## When it helps

- **Before you rely on an answer**, open its source and read it in context.
- **When results seem off**, run the same search here and look at the "why" table.
- **To show someone else** what the team's memory knows, without giving them Claude.
- **To check what Claude saved** during a long session.
