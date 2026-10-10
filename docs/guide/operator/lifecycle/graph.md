# The graph index

The graph layer is an **index**, not a source of truth. It records which entities each passage
mentions. Search uses it to find passages that connect things the query names, and `explore`
uses it to walk from one name.

## Extraction and resolution

- **Extraction** runs on every ingest, using heuristics only: backticked spans, dotted, camel
  and snake identifiers, headings and capitalised names. Clients may also declare `entities[]`
  and `relations[]` when they ingest.
- **Resolution** is deterministic. The same normalised key means the same entity. A near key,
  with 3-gram Jaccard of at least 0.8, not too short and not differing only in a number, becomes
  a **merge candidate** for a human. Anything else is a new entity.

## Commands

```bash
memors-mcp explore "Quorum Replication" --hops 2
memors-mcp graph merges                 # open candidates with their similarity
memors-mcp graph merge 17               # same thing: merge the names
memors-mcp graph reject 18              # different things: keep apart
memors-mcp graph rebuild --ns default   # re-extract mentions
```

Nothing is merged without a decision. Agents can propose decisions through `compact` and
`submit`. Merge precision is gated at 0.95 or better in CI.

## When to rebuild

- Once, after upgrading a file written before 1.1.
- After a release whose notes mention an extraction change.
- If `memors-mcp lint` reports many orphan entities after large deletions.

Rebuild keeps merge decisions. It re-reads every live passage, so on large knowledge bases
run it when the system is quiet.

## Runtime behaviour

The server keeps an in-memory mention graph per namespace. It is built on first use and
reused while the namespace's count of live mentions is unchanged; any ingest, revision or
forget that changes that count triggers a rebuild on the next routed query. `as_of` queries
always build a graph for that point in time and do not cache it.
`memors_graph_cache_total{event=hit|build|build_asof}` and `memors_graph_build_duration_seconds`
show how often this happens and what it costs.
