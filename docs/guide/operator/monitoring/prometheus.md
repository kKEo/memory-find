# Prometheus: scrape, rules, alerts

These are starting points written against the [metric catalogue](catalogue.md). Adapt the
thresholds to your usage. The scrape configuration and both rule groups pass
`promtool check config` and `promtool check rules` (Prometheus 3.15). Re-check them after you
edit them.

## Scrape configuration

```yaml
scrape_configs:
  # Long-lived UI sidecar: knowledge-base gauges, always up.
  - job_name: memo-kb
    scrape_interval: 60s
    static_configs:
      - targets: ["127.0.0.1:9470"]
        labels: { kb: my-project }

  # MCP server: live tool and search metrics; only up while a client session runs.
  - job_name: memo-mcp
    scrape_interval: 15s
    static_configs:
      - targets: ["127.0.0.1:9469"]
        labels: { kb: my-project }
```

Prometheus must run on the same machine, because both endpoints are loopback-only. A Grafana
Alloy or OpenTelemetry Collector agent on the machine can scrape them and forward the samples.
That forwarding is your choice; memo-mcp itself never pushes.

> **Do not alert on `up{job="memo-mcp"} == 0`.** The server exists only while a client session
> is open, so being down is normal. Alert on `up{job="memo-kb"}` instead, if you rely on the
> sidecar.

## Recording rules

```yaml
groups:
  - name: memo-recording
    interval: 1m
    rules:
      - record: memo:tool_calls:rate5m
        expr: sum by (kb, tool, outcome) (rate(memo_mcp_tool_calls_total[5m]))
      - record: memo:tool_error_ratio:rate5m
        expr: |
          sum by (kb) (rate(memo_mcp_tool_calls_total{outcome="error"}[5m]))
          /
          sum by (kb) (rate(memo_mcp_tool_calls_total[5m]))
      - record: memo:tool_latency_seconds:p95_5m
        expr: histogram_quantile(0.95, sum by (kb, tool, le) (rate(memo_mcp_tool_call_duration_seconds_bucket[5m])))
      - record: memo:search_latency_seconds:p95_5m
        expr: histogram_quantile(0.95, sum by (kb, le) (rate(memo_search_duration_seconds_bucket[5m])))
      - record: memo:search_abstain_ratio:rate15m
        expr: |
          sum by (kb) (rate(memo_search_total{outcome="abstain"}[15m]))
          /
          sum by (kb) (rate(memo_search_total[15m]))
      - record: memo:arm_latency_seconds:p95_5m
        expr: histogram_quantile(0.95, sum by (kb, arm, le) (rate(memo_search_arm_duration_seconds_bucket[5m])))
```

## Alerts

```yaml
groups:
  - name: memo-alerts
    rules:
      - alert: MemoToolErrorsHigh
        expr: memo:tool_error_ratio:rate5m > 0.05
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "memo-mcp {{ $labels.kb }}: more than 5% of tool calls fail"
          description: "Check memo_mcp_tool_errors_total by class and the server's stderr."

      - alert: MemoSearchSlow
        expr: memo:search_latency_seconds:p95_5m > 2
        for: 15m
        labels: { severity: warning }
        annotations:
          summary: "memo-mcp {{ $labels.kb }}: p95 search latency above 2 s"
          description: "Look at memo:arm_latency_seconds:p95_5m to find the slow arm."

      - alert: MemoSearchDegraded
        expr: sum by (kb, reason) (increase(memo_search_degraded_total[30m])) > 0
        for: 30m
        labels: { severity: warning }
        annotations:
          summary: "memo-mcp {{ $labels.kb }}: searches degraded ({{ $labels.reason }})"
          description: "The embedding model is missing or failing; search is keyword-only."

      - alert: MemoEmbeddingBacklog
        expr: max by (kb, model) (memo_kb_pending_embeddings) > 0
        for: 2h
        labels: { severity: info }
        annotations:
          summary: "{{ $value }} passages lack vectors for {{ $labels.model }}"
          description: "Run memo-mcp backfill, or start a server session to drain the backlog."

      - alert: MemoJobsFailed
        expr: max by (kb) (memo_kb_jobs_failed) > 0
        labels: { severity: warning }
        annotations:
          summary: "memo-mcp {{ $labels.kb }}: background jobs failed"

      - alert: MemoDatabaseGrowth
        expr: delta(memo_kb_db_size_bytes[1d]) > 500e6
        labels: { severity: info }
        annotations:
          summary: "memo-mcp {{ $labels.kb }}: knowledge base grew more than 500 MB in a day"

      - alert: MemoMergeQueueBacklog
        expr: max by (kb) (memo_kb_merge_review_open) > 50
        for: 1d
        labels: { severity: info }
        annotations:
          summary: "{{ $value }} merge candidates waiting for review (memo-mcp graph merges)"

      - alert: MemoKBSidecarDown
        expr: up{job="memo-kb"} == 0
        for: 10m
        labels: { severity: info }
        annotations:
          summary: "memo-mcp UI sidecar for {{ $labels.kb }} is not running"
```

## Dashboard queries

| Panel | PromQL |
|---|---|
| Tool calls by tool | `sum by (tool) (rate(memo_mcp_tool_calls_total[5m]))` |
| Tool errors by class | `sum by (class) (rate(memo_mcp_tool_errors_total[15m]))` |
| p50 and p95 tool latency | `histogram_quantile(0.5, sum by (le) (rate(memo_mcp_tool_call_duration_seconds_bucket[5m])))` (and 0.95) |
| Result size the agent pays for | `histogram_quantile(0.9, sum by (tool, le) (rate(memo_mcp_tool_result_tokens_bucket[15m])))` |
| Search outcomes | `sum by (outcome) (rate(memo_search_total[15m]))` |
| Mode resolution | `sum by (mode_resolved) (rate(memo_search_total[1h]))` |
| Where time goes, per arm | `histogram_quantile(0.95, sum by (arm, le) (rate(memo_search_arm_duration_seconds_bucket[5m])))` |
| Why lists end | `sum by (kind) (rate(memo_search_cutoff_total[1h]))` |
| Budget truncation | `rate(memo_search_truncated_results_total[1h])` |
| Knowledge-base size | `memo_kb_documents_live`, `memo_kb_chunks`, `memo_kb_facts` |
| Per namespace | `memo_kb_namespace_documents` |
| Backlog | `memo_kb_pending_embeddings`, `memo_kb_jobs_queued`, `memo_kb_work_items_open`, `memo_kb_pages_stale` |
| File size | `memo_kb_db_size_bytes` |
| Writes by channel | `sum by (channel) (rate(memo_store_writes_total[1h]))` |
| Human approvals | `sum by (outcome) (increase(memo_mcp_elicitations_total[1d]))` |
| Embedding cost | `histogram_quantile(0.95, sum by (model, role, le) (rate(memo_embed_duration_seconds_bucket[5m])))` |
| Session restarts | `changes(process_start_time_seconds{job="memo-mcp"}[1d])` |
| Version running | `memo_build_info` |
