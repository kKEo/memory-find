# Prometheus: scrape, rules, alerts

These are starting points written against the [metric catalogue](catalogue.md). Adapt the
thresholds to your usage. The scrape configuration and both rule groups pass
`promtool check config` and `promtool check rules` (Prometheus 3.15). Re-check them after you
edit them.

## Scrape configuration

```yaml
scrape_configs:
  # Long-lived UI sidecar: knowledge-base gauges, always up.
  - job_name: memors-kb
    scrape_interval: 60s
    static_configs:
      - targets: ["127.0.0.1:9470"]
        labels: { kb: my-project }

  # MCP server: live tool and search metrics; only up while a client session runs.
  - job_name: memors-mcp
    scrape_interval: 15s
    static_configs:
      - targets: ["127.0.0.1:9469"]
        labels: { kb: my-project }
```

Prometheus must run on the same machine, because both endpoints are loopback-only. A Grafana
Alloy or OpenTelemetry Collector agent on the machine can scrape them and forward the samples.
That forwarding is your choice; memors-mcp itself never pushes.

> **Do not alert on `up{job="memors-mcp"} == 0`.** The server exists only while a client session
> is open, so being down is normal. Alert on `up{job="memors-kb"}` instead, if you rely on the
> sidecar.

## Recording rules

```yaml
groups:
  - name: memors-recording
    interval: 1m
    rules:
      - record: memors:tool_calls:rate5m
        expr: sum by (kb, tool, outcome) (rate(memors_mcp_tool_calls_total[5m]))
      - record: memors:tool_error_ratio:rate5m
        expr: |
          sum by (kb) (rate(memors_mcp_tool_calls_total{outcome="error"}[5m]))
          /
          sum by (kb) (rate(memors_mcp_tool_calls_total[5m]))
      - record: memors:tool_latency_seconds:p95_5m
        expr: histogram_quantile(0.95, sum by (kb, tool, le) (rate(memors_mcp_tool_call_duration_seconds_bucket[5m])))
      - record: memors:search_latency_seconds:p95_5m
        expr: histogram_quantile(0.95, sum by (kb, le) (rate(memors_search_duration_seconds_bucket[5m])))
      - record: memors:search_abstain_ratio:rate15m
        expr: |
          sum by (kb) (rate(memors_search_total{outcome="abstain"}[15m]))
          /
          sum by (kb) (rate(memors_search_total[15m]))
      - record: memors:arm_latency_seconds:p95_5m
        expr: histogram_quantile(0.95, sum by (kb, arm, le) (rate(memors_search_arm_duration_seconds_bucket[5m])))
```

## Alerts

```yaml
groups:
  - name: memors-alerts
    rules:
      - alert: MemorsToolErrorsHigh
        expr: memors:tool_error_ratio:rate5m > 0.05
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "memors-mcp {{ $labels.kb }}: more than 5% of tool calls fail"
          description: "Check memors_mcp_tool_errors_total by class and the server's stderr."

      - alert: MemorsSearchSlow
        expr: memors:search_latency_seconds:p95_5m > 2
        for: 15m
        labels: { severity: warning }
        annotations:
          summary: "memors-mcp {{ $labels.kb }}: p95 search latency above 2 s"
          description: "Look at memors:arm_latency_seconds:p95_5m to find the slow arm."

      - alert: MemorsSearchDegraded
        expr: sum by (kb, reason) (increase(memors_search_degraded_total[30m])) > 0
        for: 30m
        labels: { severity: warning }
        annotations:
          summary: "memors-mcp {{ $labels.kb }}: searches degraded ({{ $labels.reason }})"
          description: "The embedding model is missing or failing; search is keyword-only."

      - alert: MemorsEmbeddingBacklog
        expr: max by (kb, model) (memors_kb_pending_embeddings) > 0
        for: 2h
        labels: { severity: info }
        annotations:
          summary: "{{ $value }} passages lack vectors for {{ $labels.model }}"
          description: "Run memors-mcp backfill, or start a server session to drain the backlog."

      - alert: MemorsJobsFailed
        expr: max by (kb) (memors_kb_jobs_failed) > 0
        labels: { severity: warning }
        annotations:
          summary: "memors-mcp {{ $labels.kb }}: background jobs failed"

      - alert: MemorsDatabaseGrowth
        expr: delta(memors_kb_db_size_bytes[1d]) > 500e6
        labels: { severity: info }
        annotations:
          summary: "memors-mcp {{ $labels.kb }}: knowledge base grew more than 500 MB in a day"

      - alert: MemorsMergeQueueBacklog
        expr: max by (kb) (memors_kb_merge_review_open) > 50
        for: 1d
        labels: { severity: info }
        annotations:
          summary: "{{ $value }} merge candidates waiting for review (memors-mcp graph merges)"

      - alert: MemorsKBSidecarDown
        expr: up{job="memors-kb"} == 0
        for: 10m
        labels: { severity: info }
        annotations:
          summary: "memors-mcp UI sidecar for {{ $labels.kb }} is not running"
```

## Dashboard queries

| Panel | PromQL |
|---|---|
| Tool calls by tool | `sum by (tool) (rate(memors_mcp_tool_calls_total[5m]))` |
| Tool errors by class | `sum by (class) (rate(memors_mcp_tool_errors_total[15m]))` |
| p50 and p95 tool latency | `histogram_quantile(0.5, sum by (le) (rate(memors_mcp_tool_call_duration_seconds_bucket[5m])))` (and 0.95) |
| Result size the agent pays for | `histogram_quantile(0.9, sum by (tool, le) (rate(memors_mcp_tool_result_tokens_bucket[15m])))` |
| Search outcomes | `sum by (outcome) (rate(memors_search_total[15m]))` |
| Mode resolution | `sum by (mode_resolved) (rate(memors_search_total[1h]))` |
| Where time goes, per arm | `histogram_quantile(0.95, sum by (arm, le) (rate(memors_search_arm_duration_seconds_bucket[5m])))` |
| Why lists end | `sum by (kind) (rate(memors_search_cutoff_total[1h]))` |
| Budget truncation | `rate(memors_search_truncated_results_total[1h])` |
| Knowledge-base size | `memors_kb_documents_live`, `memors_kb_chunks`, `memors_kb_facts` |
| Per namespace | `memors_kb_namespace_documents` |
| Backlog | `memors_kb_pending_embeddings`, `memors_kb_jobs_queued`, `memors_kb_work_items_open`, `memors_kb_pages_stale` |
| File size | `memors_kb_db_size_bytes` |
| Writes by channel | `sum by (channel) (rate(memors_store_writes_total[1h]))` |
| Human approvals | `sum by (outcome) (increase(memors_mcp_elicitations_total[1d]))` |
| Embedding cost | `histogram_quantile(0.95, sum by (model, role, le) (rate(memors_embed_duration_seconds_bucket[5m])))` |
| Session restarts | `changes(process_start_time_seconds{job="memors-mcp"}[1d])` |
| Version running | `memors_build_info` |
