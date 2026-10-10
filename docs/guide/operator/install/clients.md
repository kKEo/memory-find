# Connecting MCP clients

memors-mcp speaks MCP over stdio. A client starts the binary, writes JSON-RPC to its stdin and
reads responses from its stdout. Configuration is passed through **environment variables in
the client's server entry**, not the shell. The client starts the process, so your shell
profile does not apply.

## Claude Code

```bash
# In a project directory: this project only, private to you (the default "local" scope)
claude mcp add memors --env MEMORS_KB=my-project -- /absolute/path/to/memors-mcp

# Shared with the team through .mcp.json in the repository
claude mcp add memors --scope project --env MEMORS_KB=my-project -- memors-mcp

# Available in every project
claude mcp add memors --scope user --env MEMORS_KB=personal -- /absolute/path/to/memors-mcp
```

Add more variables with additional `--env` flags, for example
`--env MEMORS_QUERY_LOG=1 --env MEMORS_LOG_LEVEL=warn`. Check the connection with `/mcp` inside
Claude Code. Claude Code shows a stdio server's stderr, so memors-mcp's logs appear there.

## One shared server over HTTP

Instead of one process per session, run a single long-lived server and point every client at
its URL:

```bash
MEMORS_KB=my-project memors-mcp serve --http 127.0.0.1:8765
claude mcp add --transport http memors http://127.0.0.1:8765/mcp
```

The same port serves the web UI at `http://127.0.0.1:8765/`. In this mode the UI also has a
**Live** page with what only the running process knows: active clients, calls in flight,
background backfill, and tool and ingest latency since start. The standalone `memors-mcp ui`
reads the knowledge-base file alone and has no Live page.

Start the server yourself (a terminal, `launchd` or a `systemd --user` unit), or from the macOS
menu-bar app, [memors-tray](tray.md). Clients do not start it, and its logs go to its own
stderr.

### For companion apps

Two routes exist for local tools such as memors-tray rather than for people:

- `GET /live.json`: the Live page's client list as JSON (schema 1): the server's instance id,
  pid, version, knowledge base and model, every client with its name, version, `open` and
  `stream` state, call count and last activity, and the calls in flight. Sessions appear only
  as a short hashed handle; the session id itself grants access to the session, so it is never
  shown. New fields may be added; anything else changes `schema`.
- `POST /login-link?next=/live` (token mode only): returns a single-use login link,
  `{"url": "/login?code=…&next=%2Flive", "expires_at": …}`, for an app that holds the token and
  opens the UI in a window of its own. Only the `Authorization: Bearer` header is accepted, not
  a browser session.

### Authentication

One shared secret, the **bearer token**, protects `/mcp`, the UI and `/metrics`. It lives in
`<MEMORS_HOME>/http-token` (mode 0600, created on first use); `memors-mcp http-token` prints it.

| Setup | Auth by default | Notes |
|---|---|---|
| `--http 127.0.0.1:PORT` | none | Only your machine can connect. Add `--auth token` to keep other OS users on a shared machine out |
| `--behind-proxy` | token | A reverse proxy (Caddy, nginx) terminates TLS and forwards to a loopback `--http`. Needs `--public-url` |
| `--allow-remote` on a non-loopback address | token | Needs TLS (`--tls-cert`/`--tls-key`) and `--public-url`. Plain HTTP is refused |
| `--tls-client-ca` (mTLS) | token, or `--auth none` | Clients must present a certificate signed by that CA; the token is optional on top |

The server refuses any setup that would expose the knowledge base without authentication or
send the token unencrypted over the network.

![Four setups: loopback, the default, with no auth; behind a reverse proxy that terminates TLS, with a token; remote with TLS on the server, with a token; and mutual TLS, with a token or none. Exposing the knowledge base without authentication, or sending the token unencrypted, is refused at start.](../images/http-deployments.svg)

**Claude Code** sends the token as a header:

```bash
claude mcp add --transport http memors https://box.example:8765/mcp \
  --header "Authorization: Bearer $(memors-mcp http-token)"
```

`$(memors-mcp http-token)` is expanded once, when you run the command, and the token is stored in
Claude Code's configuration. After `memors-mcp http-token --rotate`, restart the server and run
`claude mcp remove memors` and the `add` line again.

**Prometheus** uses the same header (`authorization: { credentials_file: … }` in the scrape
config).

**Browsers** cannot send the header. The server prints a **login link** at startup,
`…/login?code=…`, that works once, within 15 minutes. Opening it sets a session cookie
(HttpOnly, SameSite=Strict, Secure over HTTPS, 30 days). Without the link, `/login` asks for
the token. `/logout` ends the session. Rotating the token logs every browser out. The
link appears in the server's log, so treat that log as sensitive while the link is valid.

A full remote example with a certificate:

```bash
memors-mcp serve --http 0.0.0.0:8765 --allow-remote \
  --tls-cert /etc/memors/cert.pem --tls-key /etc/memors/key.pem \
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
    "memors": {
      "command": "/Users/you/.local/bin/memors-mcp",
      "env": {
        "MEMORS_KB": "my-project",
        "MEMORS_QUERY_LOG": "1"
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
| command | Absolute path to `memors-mcp` |
| args | none, or `["serve", "--metrics-addr", "127.0.0.1:9469"]` |
| env | `MEMORS_KB`, and optionally `MEMORS_HOME`, `MEMORS_MODEL`, `MEMORS_PROFILE`, `MEMORS_QUERY_LOG`, `MEMORS_LOG_FORMAT`, `MEMORS_LOG_LEVEL` |

The server implements protocol version `2026-07-28`: stateless, with `server/discover` in
place of `initialize`. It advertises tools and resources. It does not advertise `logging` or
`prompts`. The `promote` tool uses elicitation when the client supports it, and otherwise
returns the CLI command a human can run.

## Teaching the agent

Ship `SKILL.md` from the release archive with the project, for example as a Claude Code
skill, or paste it into the system prompt. It describes the search-then-read loop, scoping,
writing facts and how to treat trust. For a static orientation, add the output of
`memors-mcp export --index` to `CLAUDE.md` or `AGENTS.md`.

## Verifying a connection without a client

```bash
MEMORS_KB=my-project memors-mcp status      # read-only; fails if the file does not exist yet
```

A server that starts and immediately exits usually has an invalid `MEMORS_KB` name or an
unwritable `MEMORS_HOME`. The reason is on stderr. See the
[troubleshooting runbook](../operations/troubleshooting.md).
