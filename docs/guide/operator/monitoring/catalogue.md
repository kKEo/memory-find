# Metric catalogue

Every series memors-mcp exposes, as of v1.4. Types: **C** counter, **G** gauge, **H** histogram.
A histogram exposes `_bucket{le}`, `_sum` and `_count`. A family with no observations yet
shows only its `# HELP` and `# TYPE` lines.

Bucket sets:

| Set | Boundaries |
|---|---|
| latency (s) | 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10 |
| tokens | 50, 100, 200, 500, 1000, 2000, 4000, 8000, 16000 |
| count | 0, 1, 2, 5, 10, 20, 50, 100, 200, 500 |
| bytes | 1 KiB, 4 KiB, 16 KiB, 64 KiB, 256 KiB, 1 MiB, 4 MiB |

## MCP server

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memors_mcp_requests_total` | C | `method`, `outcome` = `ok`, `error` | Every MCP request received (`tools/call`, `resources/read`, `server/discover`, …) |
| `memors_mcp_requests_in_flight` | G | — | Requests being served now |
| `memors_mcp_tool_calls_total` | C | `tool`, `outcome` = `ok`, `tool_error`, `input_required`, `error` | Tool calls. `input_required` is `promote` waiting for a human |
| `memors_mcp_tool_call_duration_seconds` | H latency | `tool` | Tool call latency |
| `memors_mcp_tool_result_tokens` | H tokens | `tool` | Estimated tokens in each tool result: the cost the agent pays |
| `memors_mcp_tool_errors_total` | C | `tool`, `class` = `not_found`, `forgotten`, `needs_human`, `canceled`, `invalid_args`, `internal` | Why calls failed |
| `memors_mcp_elicitations_total` | C | `tool`, `outcome` = `asked`, `accept`, `decline`, `cancel`, `unsupported` | Human approval dialogs |
| `memors_mcp_resource_reads_total` | C | `kind` = `doc`, `chunk`, `source`, `fact`, `entity`, `page`, `index`, `ns_index`, `unknown`; `outcome` = `ok`, `error` | `memo://` resource reads |
| `memors_mcp_sessions_total` | C | `client` | Sessions started, by client name |

## Search

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memors_search_total` | C | `mode_requested`, `mode_resolved`, `granularity`, `outcome` = `results`, `abstain` | Searches that completed (failed searches show up as tool errors) |
| `memors_search_duration_seconds` | H latency | `granularity` | End-to-end search latency |
| `memors_search_arm_duration_seconds` | H latency | `arm` | Time in each retrieval arm |
| `memors_search_arm_candidates` | H count | `arm` | Candidates each arm returned before fusion |
| `memors_search_results` | H count | `granularity` | Results returned per search |
| `memors_search_truncated_results_total` | C | — | Ranked results left out by the token budget |
| `memors_search_cutoff_total` | C | `kind` = `gap`, `budget`, `limit`, `none` | Why lists ended |
| `memors_search_degraded_total` | C | `reason` = `no_embedder`, `query_embed_failed`, `reranker_failed`, `other` | Searches served without a capability |
| `memors_search_abstentions_total` | C | `reason` | Searches that returned nothing on purpose |
| `memors_search_entities_matched` | H count | — | Known entities named per query |
| `memors_search_rerank_duration_seconds` | H latency | `model` | Cross-encoder time, only with the reranker |
| `memors_graph_cache_total` | C | `event` = `hit`, `build`, `build_asof` | In-memory mention-graph cache |
| `memors_graph_build_duration_seconds` | H latency | — | Time to build a namespace graph |

## Store (write path)

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memors_store_writes_total` | C | `op`, `channel` = `tool`, `cli`, `elicitation`, `worker` | Every audited write |
| `memors_store_ingests_total` | C | `outcome` = `new`, `revision`, `dedup`, `error` | Ingest calls |
| `memors_store_ingest_duration_seconds` | H latency | — | Ingest latency, embedding included |
| `memors_store_chunks_written_total` | C | — | Passages written |
| `memors_store_document_bytes` | H bytes | — | Size of ingested documents |
| `memors_store_vectors_stored_total` | C | `model` | Vectors stored |
| `memors_store_embed_batches_total` | C | `outcome` = `ok`, `error`, `queued` | Embedding batches on the write path |
| `memors_store_jobs_total` | C | `kind`, `event` = `queued`, `done`, `failed` | Background jobs, for example the embed backlog |
| `memors_store_mentions_linked_total` | C | — | Entity mentions linked to passages |
| `memors_store_pages_marked_stale_total` | C | — | Pages marked stale by a source change or forget |
| `memors_store_work_items_total` | C | `kind`, `event` = `created`, `done`, `skipped` | Compaction work items |
| `memors_store_status_scrape_errors_total` | C | — | Failures reading the gauges below |

## Knowledge base (gauges read from the file, cached for 5 s)

| Metric | Labels | Meaning |
|---|---|---|
| `memors_kb_sources` | — | Sources |
| `memors_kb_documents_live` | — | Live documents (latest revision, not forgotten) |
| `memors_kb_revisions` | — | Document revisions stored |
| `memors_kb_chunks` | — | Live passages |
| `memors_kb_facts` | — | Live facts |
| `memors_kb_entities` | — | Entities |
| `memors_kb_mentions` | — | Entity mentions |
| `memors_kb_edges` | — | Typed edges |
| `memors_kb_merge_review_open` | — | Open merge candidates awaiting a human |
| `memors_kb_pages` | — | Curated pages |
| `memors_kb_pages_stale` | — | Stale pages |
| `memors_kb_work_items_open` | — | Open compaction work items |
| `memors_kb_jobs_queued` | — | Jobs queued or running |
| `memors_kb_jobs_failed` | — | Jobs failed |
| `memors_kb_pending_embeddings` | `model` | Passages without a vector for that model |
| `memors_kb_schema_version` | — | Schema version of the file |
| `memors_kb_db_size_bytes` | — | Size of the main SQLite file, excluding the WAL |
| `memors_kb_last_write_timestamp_seconds` | — | Unix time of the last audited write |
| `memors_kb_namespace_documents` | `namespace` | Live documents per namespace |
| `memors_kb_namespace_chunks` | `namespace` | Live passages per namespace |

All are gauges.

## Embedding

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memors_embed_duration_seconds` | H latency | `model`, `role` = `query`, `doc` | Embedding call latency |
| `memors_embed_texts_total` | C | `model`, `role` | Texts embedded |
| `memors_embed_errors_total` | C | `model`, `role` | Embedding calls that failed |
| `memors_embed_batch_size` | H count | `model` | Texts per call |
| `memors_embed_model_downloads_total` | C | `model`, `outcome` = `ok`, `error`, `corrupt_retry` | Model downloads |
| `memors_embed_model_load_seconds` | G | `model` | Time the last model load took |
| `memors_embed_model_loaded` | G | `model`, `backend` = `hugot`, `static` | 1 while a model is loaded in this process |

## Web UI

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memors_ui_requests_total` | C | `route` (for example `GET /search`), `status` | UI requests |
| `memors_ui_request_duration_seconds` | H latency | `route` | UI render time |

## Runtime and build

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `memors_build_info` | G (always 1) | `version`, `go_version`, `mcp_protocol`, `goos`, `goarch` | What is running |
| `go_info` | G | `version` | Go version |
| `go_goroutines` | G | — | Goroutines |
| `go_memstats_heap_alloc_bytes` | G | — | Live heap |
| `go_memstats_sys_bytes` | G | — | Memory obtained from the OS |
| `go_gc_cycles_total` | C | — | Completed GC cycles |
| `go_gc_pauses_seconds` | H | — | GC pause distribution |
| `process_start_time_seconds` | G | — | Process start time; use it to spot session restarts |
