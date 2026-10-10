# Part V: Knowledge lifecycle

How records enter, change, gain trust, get connected and summarised, and leave. Agents do most
of this through MCP tools; an operator does the parts that need a human through the CLI.

- [Ingest and revisions](ingest.md)
- [Facts and time](facts.md)
- [Trust administration](trust.md)
- [The graph index](graph.md)
- [Compaction and pages](compaction.md)
- [Forget and redact](forget.md)

Every write in every chapter lands in the `audit` table: who (actor), through which channel
(`tool`, `cli`, `elicitation`, `worker`), what operation, on which record. The same events
increment `memors_store_writes_total{op,channel}`.
