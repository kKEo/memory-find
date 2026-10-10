package retrieve

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/obs"
)

// Search metrics (roadmap P10). Every search ends in finish(), which records
// what the Trace already knows, writes one log line, and then the opt-in
// query_log row. Nothing leaves the process.
type searchMetrics struct {
	searches    *obs.CounterVec
	seconds     *obs.HistogramVec
	armSeconds  *obs.HistogramVec
	armCands    *obs.HistogramVec
	results     *obs.HistogramVec
	truncated   *obs.Counter
	cutoff      *obs.CounterVec
	degraded    *obs.CounterVec
	abstentions *obs.CounterVec
	entities    *obs.Histogram
	rerank      *obs.HistogramVec
	graphCache  *obs.CounterVec
	graphBuild  *obs.Histogram
}

func newSearchMetrics(r *obs.Registry) *searchMetrics {
	return &searchMetrics{
		searches:    r.Counter("memors_search_total", "Searches, by requested and resolved mode, granularity and outcome (results, abstain).", "mode_requested", "mode_resolved", "granularity", "outcome"),
		seconds:     r.Histogram("memors_search_duration_seconds", "Search latency end to end.", obs.LatencyBuckets, "granularity"),
		armSeconds:  r.Histogram("memors_search_arm_duration_seconds", "Time spent in each retrieval arm.", obs.LatencyBuckets, "arm"),
		armCands:    r.Histogram("memors_search_arm_candidates", "Candidates each arm returned before fusion.", obs.CountBuckets, "arm"),
		results:     r.Histogram("memors_search_results", "Results returned per search.", obs.CountBuckets, "granularity"),
		truncated:   r.Counter("memors_search_truncated_results_total", "Ranked results left out by the token budget.").With(),
		cutoff:      r.Counter("memors_search_cutoff_total", "Why result lists ended (gap, budget, limit, none).", "kind"),
		degraded:    r.Counter("memors_search_degraded_total", "Searches served without a capability (no_embedder, query_embed_failed, reranker_failed, other).", "reason"),
		abstentions: r.Counter("memors_search_abstentions_total", "Searches that returned nothing on purpose, by reason.", "reason"),
		entities:    r.Histogram("memors_search_entities_matched", "Known entities the query named.", obs.CountBuckets).With(),
		rerank:      r.Histogram("memors_search_rerank_duration_seconds", "Cross-encoder rerank time.", obs.LatencyBuckets, "model"),
		graphCache:  r.Counter("memors_graph_cache_total", "Namespace graph cache events (hit, build, build_asof).", "event"),
		graphBuild:  r.Histogram("memors_graph_build_duration_seconds", "Time to build a namespace's in-memory mention graph.", obs.LatencyBuckets).With(),
	}
}

// WithRegistry records this service's metrics in r (default obs.Default()).
func (s *Service) WithRegistry(r *obs.Registry) *Service {
	s.metrics = newSearchMetrics(r)
	return s
}

// WithLogger sets the logger for the per-search line (default slog.Default()).
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

func (s *Service) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

func degradedClass(reason string) string {
	switch {
	case reason == "":
		return "other"
	case strings.HasPrefix(reason, "no embedding model"):
		return "no_embedder"
	case strings.HasPrefix(reason, "query embedding failed"):
		return "query_embed_failed"
	case strings.HasPrefix(reason, "reranker failed"):
		return "reranker_failed"
	}
	return "other"
}

func abstainClass(reason string) string {
	switch {
	case strings.HasPrefix(reason, "no retrieval arm"):
		return "no_arm"
	case strings.HasPrefix(reason, "no fact"):
		return "no_fact"
	case strings.HasPrefix(reason, "no page"):
		return "no_page"
	case strings.HasPrefix(reason, "no arm matched"), strings.HasPrefix(reason, "nothing matched"):
		return "no_match"
	}
	return "other"
}

// finish records a completed search: metrics, one log line, and the opt-in
// query_log row. It is the single exit point for every granularity.
func (s *Service) finish(_ context.Context, req Request, queries []string, tr *Trace, resp *Response, start time.Time) {
	dur := s.now().Sub(start)
	gran := req.Granularity
	if gran == "" {
		gran = GranularityChunk
	}
	outcome := "results"
	if resp.Reason != "" || len(resp.Results) == 0 {
		outcome = "abstain"
	}
	m := s.metrics
	m.searches.With(req.Mode, tr.ModeResolved, gran, outcome).Inc()
	m.seconds.With(gran).Observe(dur.Seconds())
	m.results.With(gran).Observe(float64(len(resp.Results)))
	for arm, ms := range tr.LatencyMsPerArm {
		m.armSeconds.With(arm).Observe(ms / 1000)
	}
	for arm, n := range tr.CandidatesPerArm {
		m.armCands.With(arm).Observe(float64(n))
	}
	if tr.Budget.TruncatedCount > 0 {
		m.truncated.Add(float64(tr.Budget.TruncatedCount))
	}
	kind := tr.Cutoff.Kind
	if kind == "" {
		kind = "none"
	}
	m.cutoff.With(kind).Inc()
	if tr.Degraded.Flag {
		m.degraded.With(degradedClass(tr.Degraded.Reason)).Inc()
	}
	if outcome == "abstain" {
		m.abstentions.With(abstainClass(resp.Reason)).Inc()
	}
	m.entities.Observe(float64(len(tr.Entities)))
	if tr.Rerank != nil {
		m.rerank.With(tr.Rerank.Model).Observe(tr.Rerank.LatencyMs / 1000)
	}
	attrs := []any{"mode", req.Mode, "resolved", tr.ModeResolved, "granularity", gran, "outcome", outcome, "n_results", len(resp.Results), "truncated", tr.Budget.TruncatedCount, "cutoff", kind, "latency_ms", fmt.Sprintf("%.1f", dur.Seconds()*1000), "profile", tr.Profile}
	if tr.Degraded.Flag {
		attrs = append(attrs, "degraded", tr.Degraded.Reason)
	}
	if len(tr.Entities) > 0 {
		attrs = append(attrs, "entities", strings.Join(tr.Entities, ","))
	}
	s.log().Info("search", attrs...)
	s.log().Debug("search detail", "routing", tr.RoutingReason, "latency_ms_per_arm", tr.LatencyMsPerArm, "candidates_per_arm", tr.CandidatesPerArm, "scope", tr.Filtered.ByScope)
}
