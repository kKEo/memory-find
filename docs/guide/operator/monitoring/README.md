# Part VI: Monitoring and observability

All observability in memo-mcp is **local and pull-based**. Metrics are served on loopback, logs
go to stderr, and the optional call log is a table in your own file. Nothing is pushed
anywhere, and there is no telemetry.

- [Signals overview](overview.md): what exists, where, and the per-process caveat.
- [The metrics endpoint](metrics-endpoint.md): enabling `/metrics` on the server and the UI.
- [Metric catalogue](catalogue.md): every series, its type, labels and meaning.
- [Prometheus: scrape, rules, alerts](prometheus.md): ready-to-adapt configuration.
- [Logs](logs.md): format, level, fields, and where they end up.
- [Call and query logs](call-log.md): per-call history inside the knowledge base.
