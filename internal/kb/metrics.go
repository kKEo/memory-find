package kb

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/kKEo/memory-find/internal/obs"
)

// Store metrics (roadmap P10). Every audited write counts once by op and
// channel; a few hot paths add their own counters; the table counts are a
// collector read at scrape time, cached briefly.
type storeMetrics struct {
	writes       *obs.CounterVec
	ingests      *obs.CounterVec
	ingestSecs   *obs.Histogram
	chunks       *obs.Counter
	docBytes     *obs.Histogram
	vectors      *obs.CounterVec
	embedBatches *obs.CounterVec
	jobs         *obs.CounterVec
	mentions     *obs.Counter
	pagesStale   *obs.Counter
	workItems    *obs.CounterVec
	scrapeErrors *obs.Counter
}

func newStoreMetrics(r *obs.Registry) *storeMetrics {
	return &storeMetrics{
		writes:       r.Counter("memo_store_writes_total", "Audited writes, by operation and channel.", "op", "channel"),
		ingests:      r.Counter("memo_store_ingests_total", "Ingest calls, by outcome (new, revision, dedup, error).", "outcome"),
		ingestSecs:   r.Histogram("memo_store_ingest_duration_seconds", "Ingest latency, embedding included.", obs.LatencyBuckets).With(),
		chunks:       r.Counter("memo_store_chunks_written_total", "Passages written.").With(),
		docBytes:     r.Histogram("memo_store_document_bytes", "Size of ingested documents.", obs.BytesBuckets).With(),
		vectors:      r.Counter("memo_store_vectors_stored_total", "Vectors stored, by model.", "model"),
		embedBatches: r.Counter("memo_store_embed_batches_total", "Embedding batches, by outcome (ok, error, queued).", "outcome"),
		jobs:         r.Counter("memo_store_jobs_total", "Background jobs, by kind and event (queued, done, failed).", "kind", "event"),
		mentions:     r.Counter("memo_store_mentions_linked_total", "Entity mentions linked to passages.").With(),
		pagesStale:   r.Counter("memo_store_pages_marked_stale_total", "Pages marked stale because a source changed or was forgotten.").With(),
		workItems:    r.Counter("memo_store_work_items_total", "Compaction work items, by kind and event (created, done, skipped).", "kind", "event"),
		scrapeErrors: r.Counter("memo_store_status_scrape_errors_total", "Failures reading Status for the metrics collector.").With(),
	}
}

// WithRegistry records this store's metrics in r (default obs.Default()).
func (s *Store) WithRegistry(r *obs.Registry) *Store {
	s.metrics = newStoreMetrics(r)
	return s
}

// MetricsCollector exports the knowledge base's table counts as gauges,
// re-reading Status at most once per ttl.
func (s *Store) MetricsCollector(ttl time.Duration) obs.Collector {
	var mu sync.Mutex
	var last time.Time
	var cached *Status
	return obs.CollectorFunc(func(ctx context.Context, emit func(obs.Point)) {
		mu.Lock()
		defer mu.Unlock()
		if cached == nil || time.Since(last) > ttl {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			st, err := s.Status(cctx)
			cancel()
			if err != nil {
				s.metrics.scrapeErrors.Inc()
				if cached == nil {
					return
				}
			} else {
				cached, last = st, time.Now()
			}
		}
		st := cached
		g := func(name, help string, v float64, labels ...obs.Label) {
			emit(obs.Point{Name: name, Help: help, Kind: obs.GaugeKind, Value: v, Labels: labels})
		}
		g("memo_kb_sources", "Sources in the knowledge base.", float64(st.Sources))
		g("memo_kb_documents_live", "Live documents (latest revision, not forgotten).", float64(st.LiveDocuments))
		g("memo_kb_revisions", "Document revisions stored.", float64(st.Revisions))
		g("memo_kb_chunks", "Live passages.", float64(st.Chunks))
		g("memo_kb_facts", "Live facts.", float64(st.Facts))
		g("memo_kb_entities", "Entities.", float64(st.Graph.Entities))
		g("memo_kb_mentions", "Entity mentions.", float64(st.Graph.Mentions))
		g("memo_kb_edges", "Typed edges.", float64(st.Graph.Edges))
		g("memo_kb_merge_review_open", "Open merge candidates.", float64(st.Graph.OpenMergeReview))
		g("memo_kb_pages", "Curated pages.", float64(st.Pages.Pages))
		g("memo_kb_pages_stale", "Stale pages.", float64(st.Pages.StalePages))
		g("memo_kb_work_items_open", "Open compaction work items.", float64(st.Pages.OpenWorkItems))
		g("memo_kb_jobs_queued", "Jobs queued or running.", float64(st.JobsQueued))
		g("memo_kb_jobs_failed", "Jobs failed.", float64(st.JobsFailed))
		g("memo_kb_schema_version", "Schema version of the file.", float64(st.SchemaVersion))
		if fi, err := os.Stat(st.Path); err == nil {
			g("memo_kb_db_size_bytes", "Size of the SQLite file.", float64(fi.Size()))
		}
		if !st.LastWrite.IsZero() {
			g("memo_kb_last_write_timestamp_seconds", "Unix time of the last audited write.", float64(st.LastWrite.Unix()))
		}
		for model, n := range st.PendingEmbeddings {
			g("memo_kb_pending_embeddings", "Passages without a vector, by model.", float64(n), obs.Label{Name: "model", Value: model})
		}
		for _, ns := range st.Namespaces {
			g("memo_kb_namespace_documents", "Live documents per namespace.", float64(ns.Documents), obs.Label{Name: "namespace", Value: ns.Name})
			g("memo_kb_namespace_chunks", "Live passages per namespace.", float64(ns.Chunks), obs.Label{Name: "namespace", Value: ns.Name})
		}
	})
}
