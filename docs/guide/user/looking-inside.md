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

If you run memo-mcp as one shared server (`memo-mcp serve --http 127.0.0.1:8765`, see the
operator guide), the same view is already at `http://127.0.0.1:8765/`, with an extra **Live**
page.

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
| **Live** (only with `memo-mcp serve --http`) | What the running server is doing right now: which clients are connected (name, version, and whether each one is *connected*, has its session *open* but is quiet, or has *left*), calls in flight and for how long, background work, and per-tool calls, errors and response times since it started. Reloads itself every 2 seconds |
| **Ingest** | Every time something was added, from `memo-mcp ingest` or from Claude: whether it is still running, how far it got, and how it went (documents written, unchanged or failed, passages, vectors still pending, size, time, documents per second). Click a run for one row per document. While a run is in progress the page reloads itself every 2 seconds; add `?refresh=off` to stop that |
| **Status** | Size, settings, background work in progress and the last few ingests |
| **Log** | Recent questions and tool use, if you turned the log on |

## When it helps

- **Before you rely on an answer**, open its source and read it in context.
- **When results seem off**, run the same search here and look at the "why" table.
- **To show someone else** what the team's memory knows, without giving them Claude.
- **To check what Claude saved** during a long session.
