# The metrics endpoint

## On the MCP server

```bash
memo-mcp serve --metrics-addr 127.0.0.1:9469
# or, in the client's server entry:  "env": { "MEMO_METRICS_ADDR": "127.0.0.1:9469" }
```

Claude Code example:

```bash
claude mcp add memo --env MEMO_KB=my-project --env MEMO_METRICS_ADDR=127.0.0.1:9469 -- /path/to/memo-mcp
```

At start, the server logs `metrics listening url=http://127.0.0.1:9469/metrics`.

| Property | Behaviour |
|---|---|
| Bind | **Loopback only** (`127.0.0.1`, `::1`, `localhost`). Any other address is refused at start; there is no override |
| Methods | `GET` and `HEAD`; anything else returns `405` |
| Paths | `/metrics` only; anything else returns `404` |
| Host header | Must match the bound address, which defends against DNS rebinding; otherwise `403` |
| Content type | `text/plain; version=0.0.4; charset=utf-8` |
| stdout | Untouched: the MCP stream stays clean |

> **Warning: one port, one process.** The address is bound when the server starts. If a
> second session starts with the same entry while the first is running, for example two
> Claude Code windows on one project, the second fails with
> `fatal: listen tcp 127.0.0.1:9469: bind: address already in use`. Until this is relaxed:
>
> - set `--metrics-addr` on **one** entry you use for a single long session;
> - or give each knowledge base's entry its own port;
> - or leave it off and use the UI's endpoint for file-level gauges plus the call log for
>   per-call data.

## On the UI

`memo-mcp ui` always serves `GET /metrics`, under the same guards as the rest of the UI:
loopback, GET only, and the Host check. Pin the port to scrape it:

```bash
MEMO_KB=my-project memo-mcp ui --addr 127.0.0.1:9470 --no-model
curl -s http://127.0.0.1:9470/metrics | grep memo_kb_documents_live
```

`--no-model` keeps a long-running UI light. Its search is then keyword-only, which is fine for a
metrics sidecar.

## Checking it

```bash
curl -s http://127.0.0.1:9469/metrics | head -20
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:9469/metrics   # 405
curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: example.com' http://127.0.0.1:9469/metrics  # 403
```

`memo_kb_*` gauges are read from the database at scrape time and cached for 5 seconds, so
frequent scrapes do not load the file. Read failures increment
`memo_store_status_scrape_errors_total`.
