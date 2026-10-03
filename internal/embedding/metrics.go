package embedding

import (
	"context"
	"sync"

	"github.com/kKEo/memory-find/internal/obs"
)

// Embedding metrics (roadmap P10): per-call latency and volume through the
// Instrumented wrapper that Load applies to every real model, plus download
// and load events. Recorded in the process-wide registry.
type embedMetrics struct {
	seconds   *obs.HistogramVec
	texts     *obs.CounterVec
	errors    *obs.CounterVec
	batch     *obs.HistogramVec
	downloads *obs.CounterVec
	loadSecs  *obs.GaugeVec
	loaded    *obs.GaugeVec
}

var metricsOnce = sync.OnceValue(func() *embedMetrics {
	r := obs.Default()
	return &embedMetrics{
		seconds:   r.Histogram("memo_embed_duration_seconds", "Embedding call latency, by model and role (query, doc).", obs.LatencyBuckets, "model", "role"),
		texts:     r.Counter("memo_embed_texts_total", "Texts embedded, by model and role.", "model", "role"),
		errors:    r.Counter("memo_embed_errors_total", "Embedding calls that failed, by model and role.", "model", "role"),
		batch:     r.Histogram("memo_embed_batch_size", "Texts per embedding call.", obs.CountBuckets, "model"),
		downloads: r.Counter("memo_embed_model_downloads_total", "Model downloads, by model and outcome (ok, error, corrupt_retry).", "model", "outcome"),
		loadSecs:  r.Gauge("memo_embed_model_load_seconds", "Time the last model load took.", "model"),
		loaded:    r.Gauge("memo_embed_model_loaded", "1 while a model is loaded in this process, by backend (hugot, static).", "model", "backend"),
	}
})

// Instrumented wraps an embedder so every call is timed and counted. nil in,
// nil out, so callers can wrap unconditionally.
func Instrumented(e Embedder) Embedder {
	if e == nil {
		return nil
	}
	if _, ok := e.(*instrumented); ok {
		return e
	}
	return &instrumented{inner: e, model: e.Info().ID}
}

type instrumented struct {
	inner Embedder
	model string
}

func (i *instrumented) Info() ModelInfo { return i.inner.Info() }

func (i *instrumented) Embed(ctx context.Context, text string) ([]float32, error) {
	m := metricsOnce()
	t := obs.Start()
	v, err := i.inner.Embed(ctx, text)
	i.record(m, "doc", 1, t, err)
	return v, err
}

func (i *instrumented) EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error) {
	m := metricsOnce()
	t := obs.Start()
	v, err := i.inner.EmbedBatch(ctx, texts, role)
	i.record(m, roleLabel(role), len(texts), t, err)
	return v, err
}

func (i *instrumented) record(m *embedMetrics, role string, n int, t obs.Timer, err error) {
	m.seconds.With(i.model, role).Observe(t.Seconds())
	m.batch.With(i.model).Observe(float64(n))
	if err != nil {
		m.errors.With(i.model, role).Inc()
		return
	}
	m.texts.With(i.model, role).Add(float64(n))
}

func roleLabel(r Role) string {
	if r == RoleQuery {
		return "query"
	}
	return "doc"
}
