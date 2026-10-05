# Environment variables

Set these in the MCP client's server entry for the server, and in your shell for CLI commands.
Both must agree on `MEMO_HOME` and `MEMO_KB` to see the same file.

## Storage and selection

| Variable | Default | Effect |
|---|---|---|
| `MEMO_HOME` | `~/.memo-mcp` | Base directory. Knowledge bases live in `$MEMO_HOME/kb/`; overrides in `$MEMO_HOME/profiles.json` |
| `MEMO_KB` | `default` | Knowledge-base name; the file is `$MEMO_HOME/kb/<name>.db`. Must match `^[A-Za-z0-9._-]{1,64}$` |

## Retrieval

| Variable | Default | Effect |
|---|---|---|
| `MEMO_MODEL` | `granite-small-r2` | Embedding model id from `memo-mcp model ls`. Queries use this model's vectors; missing vectors are embedded in the background at server start |
| `MEMO_PROFILE` | `default` | Ranking profile for the server and the UI (`memo-mcp profiles show` lists them). The CLI `search --profile` flag defaults to it |
| `MEMO_RERANK` | unset | `1` loads the cross-encoder reranker. Only profiles with rerank on (`precise`) use it. Measured slower and worse than the default; experiments only |
| `MEMO_RERANKER` | `ms-marco-minilm` | Reranker id to load when `MEMO_RERANK=1` |

## Logging and observability

| Variable | Default | Effect |
|---|---|---|
| `MEMO_LOG_FORMAT` | `text` | `text` or `json`. Logs always go to **stderr** |
| `MEMO_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Unknown values fall back to the default with one warning |
| `MEMO_HTTP_ADDR` | empty (stdio) | Same as `serve --http`: serve MCP over HTTP at `http://<addr>/mcp` and the live web UI at `http://<addr>/`. Loopback addresses only, unless `serve --allow-remote` |
| `MEMO_HTTP_AUTH` | `token` when remote or behind a proxy, else `none` | Same as `serve --auth`. `none` is refused on an exposed address unless mTLS is on |
| `MEMO_HTTP_TOKEN_FILE` | `<MEMO_HOME>/http-token` | Bearer token file for `serve --http`. Created with mode 0600 when missing; refused if other users can read it |
| `MEMO_TLS_CERT` / `MEMO_TLS_KEY` | empty | Same as `serve --tls-cert` / `--tls-key`: PEM certificate (chain) and key; the server speaks HTTPS |
| `MEMO_TLS_CLIENT_CA` | empty | Same as `serve --tls-client-ca`: require client certificates signed by this PEM CA (mTLS) |
| `MEMO_PUBLIC_URL` | empty | Same as `serve --public-url`: the URL clients use. Required when remote or behind a proxy |
| `MEMO_METRICS_ADDR` | empty (off) | Same as `serve --metrics-addr`: serve Prometheus metrics at `http://<addr>/metrics`. Loopback addresses only |
| `MEMO_QUERY_LOG` | unset | `1` records every search in `query_log` and every tool call in `call_log`, in the same file. See [Call and query logs](../monitoring/call-log.md) |

## Compaction executor (CLI only)

| Variable | Default | Effect |
|---|---|---|
| `MEMO_OLLAMA_URL` | `http://127.0.0.1:11434` | Ollama endpoint for `memo-mcp compact --executor ollama`. Loopback only unless `--allow-remote` |
| `MEMO_OLLAMA_MODEL` | none (required) | Ollama model name for the executor. Use a non-thinking instruction model |

## Deprecated

| Variable | Replacement | Behaviour |
|---|---|---|
| `JOURNAL_TOKEN` | `MEMO_KB` | Used as the name when `MEMO_KB` is unset, with a warning. Old journal files are not opened |
| `JOURNAL_PATH` | `MEMO_HOME` | Used as the base directory when `MEMO_HOME` is unset, with a warning |

## Not configurable

- The model cache, `~/.cache/memo-mcp/models`, is fixed per user.
- SQLite pragmas are fixed: WAL, `synchronous=NORMAL`, `busy_timeout=5000`, foreign keys on.
