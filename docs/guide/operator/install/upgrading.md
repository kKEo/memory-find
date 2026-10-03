# Upgrading and migrations

## Replacing the binary

Stop or let finish any running `memo-mcp` processes, replace the binary, and start the client
again. Each MCP session starts its own process, so a new binary is picked up on the next session.

## Schema versions

The schema version is SQLite's `user_version`. Migrations are additive and run forward only.

| Version | Release | Adds |
|---|---|---|
| 1 | 0.4 to 1.0 | Sources, documents, chunks and their indexes, facts, bookkeeping |
| 2 | 1.1 | Graph layer: entities, aliases, mentions, merge candidates, edges |
| 3 | 1.2 | Pages layer: pages, page sources, page vectors, work items |
| 4 | 1.4 | `call_log` table |

`memo-mcp status` prints the version in its first line, and the metric
`memo_kb_schema_version` exposes it.

## What migrates and when

| Situation | Behaviour |
|---|---|
| A **writing** process opens an older file: `serve`, `ingest`, `remember`, `forget`, `search`, `verify`, `backfill`, `migrate`, … | Migrates forward automatically, one transaction per migration |
| A **read-only** command opens an older file: `status`, `ui`, `ls`, `read`, `export`, `metrics`, `facts`, `explore`, `lint`, `pages`, `trust ls`, `graph merges` | Refuses with `file is at schema vN, this binary expects vM; run memo-mcp migrate`. Read-only opens never write |
| Any process opens a **newer** file than it knows | Refuses with `database schema is at version N, but this binary only supports up to version M; upgrade memo-mcp` |

To migrate explicitly before anything else touches the file:

```bash
MEMO_KB=my-project memo-mcp migrate
```

**Downgrades are not supported.** Back up the file before upgrading across a schema version
if you may need to roll back. See [Backup and restore](../operations/backup.md).

## After upgrading to 1.1 or later from 1.0

Files written before the graph layer have no entity mentions. Populate them once:

```bash
memo-mcp graph rebuild            # all namespaces; or --ns <name>
```

Run it again after any release whose notes mention an extraction change.

## After changing the embedding model

A model change is not a schema change. Vectors are stored per model, so the server embeds the
missing ones in the background at start, and search reports `degraded` until it finishes. See
[Embedding models](../tuning/models.md).

## Release notes

Read the [changelog](https://github.com/kKEo/memory-find/blob/master/CHANGELOG.md) and the
eval report for the tag, `docs/eval/vX.Y.Z.md`, before upgrading. The 1.x contract keeps tool
names and parameters, `memo://` addresses, explain field names and the export format stable.
