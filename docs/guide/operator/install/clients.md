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

## One shared server over HTTP

Instead of one process per session, run a single long-lived server and point every client at
its URL:

```bash
MEMO_KB=my-project memo-mcp serve --http 127.0.0.1:8765
claude mcp add --transport http memo http://127.0.0.1:8765/mcp
```

The same port serves the web UI at `http://127.0.0.1:8765/`. In this mode the UI also has a
**Live** page with what only the running process knows: active clients, calls in flight,
background backfill, and tool and ingest latency since start. The standalone `memo-mcp ui`
reads the knowledge-base file alone and has no Live page.

Start the server yourself (a terminal, `launchd` or a `systemd --user` unit). Clients do not
start it, and its logs go to its own stderr.

### Authentication

One shared secret, the **bearer token**, protects `/mcp`, the UI and `/metrics`. It lives in
`<MEMO_HOME>/http-token` (mode 0600, created on first use); `memo-mcp http-token` prints it.

| Setup | Auth by default | Notes |
|---|---|---|
| `--http 127.0.0.1:PORT` | none | Only your machine can connect. Add `--auth token` to keep other OS users on a shared machine out |
| `--behind-proxy` | token | A reverse proxy (Caddy, nginx) terminates TLS and forwards to a loopback `--http`. Needs `--public-url` |
| `--allow-remote` on a non-loopback address | token | Needs TLS (`--tls-cert`/`--tls-key`) and `--public-url`. Plain HTTP is refused |
| `--tls-client-ca` (mTLS) | token, or `--auth none` | Clients must present a certificate signed by that CA; the token is optional on top |

The server refuses any setup that would expose the knowledge base without authentication or
send the token unencrypted over the network.

**Claude Code** sends the token as a header:

```bash
claude mcp add --transport http memo https://box.example:8765/mcp \
  --header "Authorization: Bearer $(memo-mcp http-token)"
```

`$(memo-mcp http-token)` is expanded once, when you run the command, and the token is stored in
Claude Code's configuration. After `memo-mcp http-token --rotate`, restart the server and run
`claude mcp remove memo` and the `add` line again.

**Prometheus** uses the same header (`authorization: { credentials_file: … }` in the scrape
config).

**Browsers** cannot send the header. The server prints a **login link** at startup,
`…/login?code=…`, that works once, within 15 minutes. Opening it sets a session cookie
(HttpOnly, SameSite=Strict, Secure over HTTPS, 30 days). Without the link, `/login` asks for
the token. `/logout` ends the session. Rotating the token logs every browser out. The
link appears in the server's log, so treat that log as sensitive while the link is valid.

A full remote example with a certificate:

```bash
memo-mcp serve --http 0.0.0.0:8765 --allow-remote \
  --tls-cert /etc/memo/cert.pem --tls-key /etc/memo/key.pem \
  --public-url https://box.example:8765
```

mTLS needs a client certificate in every client. Browsers and Prometheus support that.
Check your MCP client's documentation before relying on mTLS alone; with the token on as
well (the default), a client that cannot present a certificate cannot connect at all.

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
