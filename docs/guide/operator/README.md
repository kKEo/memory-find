# memo-mcp operator guide

This guide is for the people who install, configure, tune and run memo-mcp. It is written for
readers comfortable with a terminal, SQLite, environment variables and Prometheus.

memo-mcp is a single, pure-Go binary. It exposes a knowledge base to AI agents over the
[Model Context Protocol](https://modelcontextprotocol.io) (MCP) on stdio. It stores
everything in one SQLite file per knowledge base, and it ships with a CLI and a read-only web
UI over the same store. There is no daemon to deploy, no external database and no network
dependency after a one-time model download.

## What you will find here

| Part | Read it when you need to… |
|---|---|
| [I. How it works](intro/README.md) | Understand the processes, the data model, the search pipeline and the security boundaries |
| [II. Install and deploy](install/README.md) | Install, connect MCP clients, lay out several knowledge bases, upgrade, run offline |
| [III. Configuration reference](config/README.md) | Look up an environment variable, a command, a flag, the overrides file or a path on disk |
| [IV. Retrieval tuning](tuning/README.md) | Change ranking, switch embedding models, try the reranker, and measure whether a change helped |
| [V. Knowledge lifecycle](lifecycle/README.md) | Administer ingest, facts, trust, the graph index, compaction and deletion |
| [VI. Monitoring and observability](monitoring/README.md) | Scrape metrics, write alerts, read logs, use the call log |
| [VII. Operations](operations/README.md) | Back up, verify, size, export, troubleshoot, release |

## Conventions

- Commands are shown as `memo-mcp <command>`. Every command reads the same environment
  (`MEMO_HOME`, `MEMO_KB`, …), so set it the same way the MCP client does.
- "Knowledge base" (KB) means one SQLite file. A "namespace" is a label inside one file.
- Addresses such as `memo://chunk/42` identify records; every CLI command and the UI accept
  them.
- Defaults quoted here are the shipped values for v1.4. `memo-mcp profiles show` and
  `memo-mcp --help` print the live values for your build.

## Deeper references

This guide states what to do and what it costs. The design documents explain why:

- [Architecture](https://github.com/kKEo/memory-find/blob/master/docs/architecture.md): the 1.x contract, formulas and defaults.
- [Schema](https://github.com/kKEo/memory-find/blob/master/docs/schema.md): every table and column, trust transitions, time rules.
- [Eval reports](https://github.com/kKEo/memory-find/blob/master/docs/eval/): measured quality and cost per release.

> **Looking for the non-technical introduction?** See the [user guide](../user/).
