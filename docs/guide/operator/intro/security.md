# Security model

memo-mcp is designed to run on one person's machine with that person's privileges. Its
security properties come from being local, small and explicit.

## Exposure

| Surface | Exposure | Guard |
|---|---|---|
| MCP server | stdio of the client process (default) | No network listener. **stdout is reserved for JSON-RPC**; all diagnostics go to stderr |
| MCP server, `serve --http` | Loopback by default; non-loopback needs `--allow-remote`, TLS and `--public-url` | Bearer token (constant-time compare) on `/mcp`, UI and `/metrics`, on by default whenever exposed; optional mTLS. Browser sessions: one-time login link (printed at startup, or minted by `POST /login-link` for a caller holding the token; a cookie cannot mint one) or token form, HMAC-derived HttpOnly SameSite=Strict cookie. MCP session ids are never shown, only a hashed handle (`/live`, `/live.json`). Cross-origin requests refused; Host allowlist; SDK DNS-rebinding check. Plain HTTP and unauthenticated exposure are refused |
| Web UI | Loopback (`127.0.0.1:0` by default) | GET only, Host header allowlist against DNS rebinding, CSP, no mutating routes. A non-loopback bind needs `--allow-remote` |
| Metrics endpoint | Off by default; loopback only, with no override | `GET /metrics` only, Host check, 404 for any other path |
| Ollama executor | Off by default; loopback unless `--allow-remote` | CLI only; no MCP code path reaches it |

## Outbound traffic

- **Model download** from Hugging Face on first use of a model, into
  `~/.cache/memo-mcp/models`. Avoidable; see [Air-gapped installs](../install/air-gapped.md).
- Nothing else. The server never fetches URLs. Agents fetch content and pass the text to
  `ingest`. There is no telemetry: metrics are pull-only on loopback, logs go to stderr, and
  the call log is a table in your own file.

## Data at rest

- One SQLite file per knowledge base, in **plaintext**. Directories are created `0700` and
  files `0600`. A pre-existing directory with wider permissions is not tightened; check it.
- Use full-disk encryption if the file may hold sensitive material.
- A running `serve --http` advertises itself in `$MEMO_HOME/run/serve-<pid>.json` (`0600`):
  its URL, auth mode and the path of the token file, never the token itself.
- `forget --redact` erases a record's text. Plain `forget` hides the record but keeps the text
  in history.

## Trust gates

- Trust is set by channel. An agent cannot write a record above `agent`, and cannot forget or
  supersede `user` or `curated` records.
- Raising trust requires a human: `memo-mcp trust promote`, or an MCP elicitation dialog that
  shows the excerpt, the source and the target level. **A client configured to auto-accept
  elicitation removes this protection.** In that case, treat `user` and `curated` as no
  stronger than `agent`.
- Every write and every trust change is recorded in the `audit` table with its actor and
  channel.

## Prompt injection

Ingested content is data, but agents read it. memo-mcp limits the blast radius: content
cannot raise its own trust, cannot delete human records and cannot trigger network calls.
Results carry provenance and trust, so an agent, or a human reviewing its answer, can discount
`agent`-trust web content.

## The MCP host

Everything an agent writes or searches passes through the MCP host as tool input and output.
The knowledge base is as private as that host. memo-mcp does not advertise the MCP `logging`
capability; logs stay on stderr, which the host may display or store.
