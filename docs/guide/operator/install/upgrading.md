# Upgrading and migrations

## Replacing the binary

Stop or let finish any running `memors-mcp` processes, replace the binary, and start the client
again. Each MCP session starts its own process, so a new binary is picked up on the next session.

## Upgrading from memo-mcp

memo-mcp was renamed to memors-mcp, and memo-tray to memors-tray. Knowledge-base files,
`memo://` addresses, tool names and the export format are unchanged. The names around them
changed, and the old ones are not read any more:

| Was | Now |
|---|---|
| `memo-mcp` binary, `memo-mcp_<version>_<os>_<arch>` archives | `memors-mcp`, `memors-mcp_<version>_<os>_<arch>` |
| `~/.memo-mcp` (knowledge bases, token, profiles, run files, `tray.json`) | `~/.memors-mcp` |
| `~/.cache/memo-mcp/models` | `~/.cache/memors-mcp/models` |
| `MEMO_*` variables | `MEMORS_*`, same suffixes |
| `memo_*` metrics | `memors_*`, same suffixes |
| memo-tray.app, bundle ID and LaunchAgent `io.github.kkeo.memo-tray` | memors-tray.app, `io.github.kkeo.memors-tray` |
| `tray.json` key `memo_binary` | `memors_binary` |
| `github.com/kKEo/memory-find` | `github.com/kKEo/memors` |

1. Quit memo-tray and stop every `memo-mcp serve` process.
2. Move the data: `mv ~/.memo-mcp ~/.memors-mcp`. If you set `MEMO_HOME`, keep that directory
   and set `MEMORS_HOME` instead. Optionally `mv ~/.cache/memo-mcp ~/.cache/memors-mcp` keeps the
   downloaded models; otherwise they download again.
3. Install memors-mcp ([From a release](release.md)) and delete the old `memo-mcp` binary.
4. In every client config, point the command at `memors-mcp` and rename `MEMO_*` variables to
   `MEMORS_*`. The server key (`memo`) may stay; these guides now use `memors`
   ([Connecting clients](clients.md)).
5. If `tray.json` sets `memo_binary`, rename the key to `memors_binary`, or delete it and let
   memors-tray find `memors-mcp` by itself.
6. Install memors-tray by hand ([memors-tray](tray.md)): memo-tray cannot update itself to the
   renamed release archives. Delete memo-tray.app. "Open at login" carries over on the first start.
7. Rename `memo_` to `memors_` in Prometheus recording rules, alerts and dashboards
   ([Prometheus](../monitoring/prometheus.md)).
8. Browsers log in to the web UI once more: the session cookie was renamed.

`memors-mcp status` then shows the same knowledge base as before.

## Schema versions

The schema version is SQLite's `user_version`. Migrations are additive and run forward only.

| Version | Release | Adds |
|---|---|---|
| 1 | 0.4 to 1.0 | Sources, documents, chunks and their indexes, facts, bookkeeping |
| 2 | 1.1 | Graph layer: entities, aliases, mentions, merge candidates, edges |
| 3 | 1.2 | Pages layer: pages, page sources, page vectors, work items |
| 4 | 1.4 | `call_log` table |

`memors-mcp status` prints the version in its first line, and the metric
`memors_kb_schema_version` exposes it.

## What migrates and when

| Situation | Behaviour |
|---|---|
| A **writing** process opens an older file: `serve`, `ingest`, `remember`, `forget`, `search`, `verify`, `backfill`, `migrate`, … | Migrates forward automatically, one transaction per migration |
| A **read-only** command opens an older file: `status`, `ui`, `ls`, `read`, `export`, `metrics`, `facts`, `explore`, `lint`, `pages`, `trust ls`, `graph merges` | Refuses with `file is at schema vN, this binary expects vM; run memors-mcp migrate`. Read-only opens never write |
| Any process opens a **newer** file than it knows | Refuses with `database schema is at version N, but this binary only supports up to version M; upgrade memors-mcp` |

To migrate explicitly before anything else touches the file:

```bash
MEMORS_KB=my-project memors-mcp migrate
```

**Downgrades are not supported.** Back up the file before upgrading across a schema version
if you may need to roll back. See [Backup and restore](../operations/backup.md).

## After upgrading to 1.1 or later from 1.0

Files written before the graph layer have no entity mentions. Populate them once:

```bash
memors-mcp graph rebuild            # all namespaces; or --ns <name>
```

Run it again after any release whose notes mention an extraction change.

## After changing the embedding model

A model change is not a schema change. Vectors are stored per model, so the server embeds the
missing ones in the background at start, and search reports `degraded` until it finishes. See
[Embedding models](../tuning/models.md).

## Release notes

Read the [changelog](https://github.com/kKEo/memors/blob/master/CHANGELOG.md) and the
eval report for the tag, `docs/eval/vX.Y.Z.md`, before upgrading. The 1.x contract keeps tool
names and parameters, `memo://` addresses, explain field names and the export format stable.
