# Connecting MCP clients

memo-mcp speaks MCP over stdio. A client starts the binary, writes JSON-RPC to its stdin and
reads responses from its stdout. Configuration is passed through **environment variables in
the client's server entry**, not the shell. The client starts the process, so your shell
profile does not apply.

## Claude Code

```bash
# In a project directory: this project only, private to you (the default "local" scope)
claude mcp add memo --env MEMO_KB=my-project -- /absolute/path/to/memo-mcp

# Shared with the team through .mcp.json in the repository
claude mcp add memo --scope project --env MEMO_KB=my-project -- memo-mcp

# Available in every project
claude mcp add memo --scope user --env MEMO_KB=personal -- /absolute/path/to/memo-mcp
```

Add more variables with additional `--env` flags, for example
`--env MEMO_QUERY_LOG=1 --env MEMO_LOG_LEVEL=warn`. Check the connection with `/mcp` inside
Claude Code. Claude Code shows a stdio server's stderr, so memo-mcp's logs appear there.

## Claude Desktop

Edit `claude_desktop_config.json`. On macOS it is in `~/Library/Application Support/Claude/`;
on Windows, in `%APPDATA%\Claude\`. Use absolute paths; `~` is not expanded.

```json
{
  "mcpServers": {
    "memo": {
      "command": "/Users/you/.local/bin/memo-mcp",
      "env": {
        "MEMO_KB": "my-project",
        "MEMO_QUERY_LOG": "1"
      }
    }
  }
}
```

Restart Claude Desktop completely after editing.

## Any other stdio client

The generic shape is the same everywhere: a command, optional args and an environment map.

| Field | Value |
|---|---|
| command | Absolute path to `memo-mcp` |
| args | none, or `["serve", "--metrics-addr", "127.0.0.1:9469"]` |
| env | `MEMO_KB`, and optionally `MEMO_HOME`, `MEMO_MODEL`, `MEMO_PROFILE`, `MEMO_QUERY_LOG`, `MEMO_LOG_FORMAT`, `MEMO_LOG_LEVEL` |

The server implements protocol version `2026-07-28`: stateless, with `server/discover` in
place of `initialize`. It advertises tools and resources. It does not advertise `logging` or
`prompts`. The `promote` tool uses elicitation when the client supports it, and otherwise
returns the CLI command a human can run.

## Teaching the agent

Ship `SKILL.md` from the release archive with the project, for example as a Claude Code
skill, or paste it into the system prompt. It describes the search-then-read loop, scoping,
writing facts and how to treat trust. For a static orientation, add the output of
`memo-mcp export --index` to `CLAUDE.md` or `AGENTS.md`.

## Verifying a connection without a client

```bash
MEMO_KB=my-project memo-mcp status      # read-only; fails if the file does not exist yet
```

A server that starts and immediately exits usually has an invalid `MEMO_KB` name or an
unwritable `MEMO_HOME`. The reason is on stderr. See the
[troubleshooting runbook](../operations/troubleshooting.md).
