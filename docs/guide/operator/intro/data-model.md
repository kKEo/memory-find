# Data model

One knowledge base is one SQLite file. Inside it, knowledge is stored in layers. Each layer
points down to the one it was derived from, so every answer can be traced to its source.

```
L5  pages       markdown an agent wrote from passages; cites them; marked stale when a source changes
L4  graph       entities, aliases, mentions (entity ↔ passage), merge candidates, typed edges
L3  facts       one-sentence claims with a validity window and an evidence passage
L2  chunks      passages of ~200 estimated tokens, indexed three ways (two FTS5 + vectors)
L1  documents   the text as ingested, one row per revision
L0  sources     where it came from: URI or file, library, version, hash, trust, origin
    bookkeeping namespaces, models, jobs, audit, query_log, call_log
```

| Layer | Rebuildable? | Notes |
|---|---|---|
| L0–L2 | Yes, deterministically from the sources | Chunking and indexing are pure functions of the text |
| L3 | No: facts are additive | Corrections supersede or invalidate; nothing is deleted in place |
| L4 | Yes: `memo-mcp graph rebuild` | Merge decisions made by a human are kept |
| L5 | No: written by agents | Stored as `is_inference`, with the passages each page cites |

## Addresses

| Address | Resolves to |
|---|---|
| `memo://source/<id>` | The latest live document of a source |
| `memo://doc/<id>` | One document revision |
| `memo://chunk/<n>` | One passage (`read` with `section` adds its neighbours) |
| `memo://fact/<id>` | One fact, with its evidence address and its two clocks |
| `memo://entity/<id>` | One entity, its aliases, passages and neighbours |
| `memo://page/<id>` | One curated page with its sources and stale flag |
| `memo://index`, `memo://ns/<namespace>/index` | One line per document and fact, under 8 KB |

Document and fact ids are UUIDv7, which sort by time. Chunk ids are integers. A forgotten
record's address still resolves, to "forgotten on … because …".

## Namespaces and knowledge bases

- A **namespace** is a label on sources inside one file. A search spans all namespaces unless
  the request scopes it. Namespaces organise; they do not isolate.
- A **knowledge base** is a file selected by `MEMO_KB`. Files are the isolation and privacy
  boundary: no command or tool reads across files.

## Trust and origin

Every source, and therefore every passage and fact, carries two labels:

| Label | Values | Set by |
|---|---|---|
| **trust** | `agent` < `user` < `curated` | The **channel** the write came through, never the request. MCP tool writes are `agent`. CLI writes are `user` by default, `curated` only with `--trust curated` |
| **origin** | `web`, `user-said`, `agent-derived` | Declared by the writer: fetched from the web, said by a person, or inferred by an agent |

Only a human can raise trust: `memo-mcp trust promote`, or an elicitation dialog the client
shows for the `promote` tool. Tool calls may forget or supersede only `agent` records. Every
transition is written to the `audit` table. See [Trust administration](../lifecycle/trust.md).

## Two clocks

- **Recorded time**: when the knowledge base learned something.
- **Valid time**: when a fact is true in the world (`valid_from`, `valid_to`).

`as_of` queries ("what did we believe on 1 March?") filter on recorded time, and also on valid
time for facts that have a window. Superseded revisions and replaced facts stay readable under
`as_of`. Forgotten records are excluded in both views. See [Facts and time](../lifecycle/facts.md).

Full column-by-column reference:
[schema.md](https://github.com/kKEo/memory-find/blob/master/docs/schema.md).
