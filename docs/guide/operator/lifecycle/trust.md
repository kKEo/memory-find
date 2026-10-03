# Trust administration

| From → To | MCP tool call | CLI (human) | Elicitation dialog (human) |
|---|---|---|---|
| create `agent` | yes | — | — |
| create `user` / `curated` | no | yes (`--trust`) | — |
| `agent` → `user` / `curated` | no; `promote` asks | `trust promote` | yes, when accepted |
| lower trust | no | `trust demote` | — |
| forget or supersede `agent` records | yes | yes | — |
| forget or supersede `user` / `curated` records | no | yes | — |

## Commands

```bash
memo-mcp trust ls                                      # everything above agent trust
memo-mcp trust promote memo://doc/0199… --to curated
memo-mcp trust demote  memo://fact/0199… --to agent
```

## The `promote` tool

When an agent calls `promote`:

1. If the client supports elicitation, it shows the human a dialog with the excerpt, the source
   and the target level. Accepting it applies the change through the `elicitation` channel.
   Declining or cancelling changes nothing.
2. If the client does not support elicitation, the tool returns the exact `memo-mcp trust
   promote …` command for a human to run.

Outcomes are counted in
`memo_mcp_elicitations_total{tool="promote",outcome=asked|accept|decline|cancel|unsupported}`.

> **Warning.** Hooks or settings that auto-accept elicitation dialogs defeat this gate. If you
> run such a client, do not rely on trust levels.

## Auditing trust

Every change is an `audit` row, with `op` `promote` or `demote` and the channel it came
through. Inspect it with `sqlite3`:

```bash
sqlite3 ~/.memo-mcp/kb/default.db \
  "SELECT datetime(ts/1000,'unixepoch'), actor, channel, op, target_uri FROM audit WHERE op IN ('promote','demote') ORDER BY ts DESC LIMIT 20"
```
