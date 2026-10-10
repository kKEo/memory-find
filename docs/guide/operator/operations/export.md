# Exporting

## Markdown with provenance

```bash
memors-mcp export --md ./kb-export            # all namespaces
memors-mcp export --md ./kb-export --ns handbook
```

The layout is `<namespace>/<kind>/<slug>-<shortid>.md`, plus an `_index.md` per namespace. Each
file has YAML front matter:

```yaml
---
memo_uri: memo://doc/01a1017e-ec2c-777a-b64a-4b67d1ac8f97
title: Deploy checklist
kind: doc
namespace: default
revision: 1
content_hash: sha256:cf966c40e2f66a5d595ab316004e2875ff5f70aa51679c31669852fc121d7326
fetched_at: 2026-10-03T11:20:57Z
trust: user
origin: user-said
---
```

Empty fields are left out. `source_uri`, `library`, `version` and `context` appear when they
are set.

The export opens as an Obsidian vault. Re-ingesting it produces **zero** new revisions, because
the content hashes match. Facts, history, graph and logs are not included.

## An index for agents

```bash
memors-mcp export --index > kb-index.md                        # ≤ 8 KB, one line per document
memors-mcp export --index --library grpc/grpc-go@v1.64.0 --max-bytes 4096
```

Each line has the title, address, kind, version and trust, and facts are summarised. Lines that do
not fit the budget are counted in a footer. Paste the output into `CLAUDE.md` or `AGENTS.md` so
an agent knows what exists before it searches. The same content is the MCP resource
`memo://index`.

## Raw data

The file is standard SQLite, so any tool can read it. For example, every live fact as CSV:

```bash
sqlite3 -csv -header ~/.memors-mcp/kb/my-project.db \
  "SELECT id, namespace, statement, trust, valid_from, valid_to FROM facts WHERE invalidated_at IS NULL AND deleted_at IS NULL"
```

Column names: [schema.md](https://github.com/kKEo/memors/blob/master/docs/schema.md).
