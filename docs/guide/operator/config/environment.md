# Environment variables

Set these in the MCP client's server entry for the server, and in your shell for CLI commands.
Both must agree on `MEMORS_HOME` and `MEMORS_KB` to see the same file.

## Storage and selection

| Variable | Default | Effect |
|---|---|---|
| `MEMORS_HOME` | `~/.memors-mcp` | Base directory. Knowledge bases live in `$MEMORS_HOME/kb/`; overrides in `$MEMORS_HOME/profiles.json` |
| `MEMORS_KB` | `default` | Knowledge-base name; the file is `$MEMORS_HOME/kb/<name>.db`. Must match `^[A-Za-z0-9._-]{1,64}$` |

## Retrieval

| Variable | Default | Effect |
|---|---|---|
| `MEMORS_MODEL` | `granite-small-r2` | Embedding model id from `memors-mcp model ls`. Queries use this model's vectors; missing vectors are embedded in the background at server start |
| `MEMORS_PROFILE` | `default` | Ranking profile for the server and the UI (`memors-mcp profiles show` lists them). The CLI `search --profile` flag defaults to it |
| `MEMORS_RERANK` | unset | `1` loads the cross-encoder reranker. Only profiles with rerank on (`precise`) use it. Measured slower and worse than the default; experiments only |
| `MEMORS_RERANKER` | `ms-marco-minilm` | Reranker id to load when `MEMORS_RERANK=1` |

## Logging and observability

| Variable | Default | Effect |
|---|---|---|
| `MEMORS_LOG_FORMAT` | `text` | `text` or `json`. Logs always go to **stderr** |
| `MEMORS_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Unknown values fall back to the default with one warning |
| `MEMORS_HTTP_ADDR` | empty (stdio) | Same as `serve --http`: serve MCP over HTTP at `http://<addr>/mcp` and the live web UI at `http://<addr>/`. Loopback addresses only, unless `serve --allow-remote` |
| `MEMORS_HTTP_AUTH` | `token` when remote or behind a proxy, else `none` | Same as `serve --auth`. `none` is refused on an exposed address unless mTLS is on |
| `MEMORS_HTTP_TOKEN_FILE` | `<MEMORS_HOME>/http-token` | Bearer token file for `serve --http`. Created with mode 0600 when missing; refused if other users can read it |
| `MEMORS_TLS_CERT` / `MEMORS_TLS_KEY` | empty | Same as `serve --tls-cert` / `--tls-key`: PEM certificate (chain) and key; the server speaks HTTPS |
| `MEMORS_TLS_CLIENT_CA` | empty | Same as `serve --tls-client-ca`: require client certificates signed by this PEM CA (mTLS) |
| `MEMORS_PUBLIC_URL` | empty | Same as `serve --public-url`: the URL clients use. Required when remote or behind a proxy |
| `MEMORS_METRICS_ADDR` | empty (off) | Same as `serve --metrics-addr`: serve Prometheus metrics at `http://<addr>/metrics`. Loopback addresses only |
| `MEMORS_QUERY_LOG` | unset | `1` records every search in `query_log` and every tool call in `call_log`, in the same file. See [Call and query logs](../monitoring/call-log.md) |

## Compaction executor (CLI only)

| Variable | Default | Effect |
|---|---|---|
| `MEMORS_OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama endpoint for `memors-mcp compact --executor ollama`. Loopback only unless `--allow-remote` |
| `MEMORS_OLLAMA_MODEL` | none (required) | Ollama model name for the executor. Use a non-thinking instruction model |

## Renamed

Before the rename to memors-mcp every variable here was spelled `MEMO_*` (for example
`MEMO_KB`). The old names are not read any more: rename them in client configs and service
files, as described in [Upgrading from memo-mcp](../install/upgrading.md#upgrading-from-memo-mcp).
The `JOURNAL_TOKEN` and `JOURNAL_PATH` aliases are gone as well.

## Not configurable

- The model cache, `~/.cache/memors-mcp/models`, is fixed per user.
- SQLite pragmas are fixed: WAL, `synchronous=NORMAL`, `busy_timeout=5000`, foreign keys on.
