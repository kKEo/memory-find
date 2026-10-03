package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestCounterGaugeHistogram(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("memo_test_total", "help", "tool")
	c.With("search").Inc()
	c.With("search").Add(2)
	if v := c.With("search").Value(); v != 3 {
		t.Fatalf("counter %v", v)
	}
	g := r.Gauge("memo_test_gauge", "help")
	g.With().Set(5)
	g.With().Dec()
	if v := g.With().Value(); v != 4 {
		t.Fatalf("gauge %v", v)
	}
	h := r.Histogram("memo_test_seconds", "help", []float64{0.1, 1}, "arm")
	for _, x := range []float64{0.05, 0.5, 5} {
		h.With("keyword").Observe(x)
	}
	if h.With("keyword").Count() != 3 || math.Abs(h.With("keyword").Sum()-5.55) > 1e-9 {
		t.Fatalf("histogram %d %v", h.With("keyword").Count(), h.With("keyword").Sum())
	}
	snap := r.Snapshot(context.Background())
	var hist *Family
	for i := range snap.Families {
		if snap.Families[i].Name == "memo_test_seconds" {
			hist = &snap.Families[i]
		}
	}
	if hist == nil || len(hist.Series) != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	b := hist.Series[0].Buckets
	if len(b) != 3 || b[0].Count != 1 || b[1].Count != 2 || !math.IsInf(b[2].Le, 1) || b[2].Count != 3 {
		t.Fatalf("cumulative buckets: %+v", b)
	}
	// Re-registration is idempotent; a different label set panics.
	if r.Counter("memo_test_total", "help", "tool").With("search").Value() != 3 {
		t.Fatal("re-registration lost the series")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on label mismatch")
			}
		}()
		r.Counter("memo_test_total", "help", "other")
	}()
	// Concurrent increments are exact.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.With("ingest").Inc()
				h.With("semantic").Observe(0.01)
			}
		}()
	}
	wg.Wait()
	if c.With("ingest").Value() != 5000 || h.With("semantic").Count() != 5000 {
		t.Fatalf("lost updates: %v %d", c.With("ingest").Value(), h.With("semantic").Count())
	}
}

var updateGolden = os.Getenv("UPDATE_GOLDEN") == "1"

func TestPromTextGolden(t *testing.T) {
	r := NewRegistry()
	r.Counter("memo_calls_total", "Tool calls.", "tool", "outcome").With("search", "ok").Add(3)
	r.Counter("memo_calls_total", "Tool calls.", "tool", "outcome").With("read", "error").Inc()
	r.Gauge("memo_odd_gauge", "Label value needing escapes.", "name").With("a\"b\\c\nd").Set(1.5)
	h := r.Histogram("memo_latency_seconds", "Latency.", []float64{0.1, 1}, "arm")
	h.With("keyword").Observe(0.05)
	h.With("keyword").Observe(2)
	r.AddCollector(CollectorFunc(func(_ context.Context, emit func(Point)) {
		emit(Point{Name: "memo_collected", Help: "From a collector.", Kind: GaugeKind, Value: 42})
	}))
	var buf bytes.Buffer
	if err := WriteText(&buf, r.Snapshot(context.Background())); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "metrics.golden.txt")
	if updateGolden {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (UPDATE_GOLDEN=1 to create): %v", err)
	}
	if buf.String() != string(want) {
		t.Fatalf("text format differs from golden:\n%s\n--- want ---\n%s", buf.String(), want)
	}
}

// A small checker of the exposition format: every sample line parses, every
// histogram's buckets are cumulative and end at +Inf with the _count value.
func TestPromTextParses(t *testing.T) {
	r := NewRegistry()
	r.AddCollector(RuntimeCollector())
	r.AddCollector(ProcessCollector())
	BuildInfo(r, "v1.4.0-test", "2026-07-28")
	h := r.Histogram("memo_x_seconds", "x", LatencyBuckets, "k")
	h.With("a").Observe(0.3)
	h.With("a").Observe(30)
	var buf bytes.Buffer
	if err := WriteText(&buf, r.Snapshot(context.Background())); err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{[^}]*\})? (\S+)$`)
	buckets := map[string][]float64{}
	counts := map[string]float64{}
	for _, l := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		m := line.FindStringSubmatch(l)
		if m == nil {
			t.Fatalf("bad sample line %q", l)
		}
		if _, err := strconv.ParseFloat(strings.Replace(m[3], "+Inf", "Inf", 1), 64); err != nil {
			t.Fatalf("bad value in %q", l)
		}
		switch {
		case strings.HasSuffix(m[1], "_bucket"):
			v, _ := strconv.ParseFloat(strings.Replace(m[3], "+Inf", "Inf", 1), 64)
			key := strings.TrimSuffix(m[1], "_bucket") + stripLe(m[2])
			buckets[key] = append(buckets[key], v)
		case strings.HasSuffix(m[1], "_count"):
			v, _ := strconv.ParseFloat(m[3], 64)
			counts[strings.TrimSuffix(m[1], "_count")+m[2]] = v
		}
	}
	if len(buckets) == 0 {
		t.Fatal("no histogram found")
	}
	for key, b := range buckets {
		for i := 1; i < len(b); i++ {
			if b[i] < b[i-1] {
				t.Fatalf("%s buckets not cumulative: %v", key, b)
			}
		}
		if c, ok := counts[key]; !ok || c != b[len(b)-1] {
			t.Fatalf("%s: +Inf bucket %v != count %v", key, b[len(b)-1], c)
		}
	}
	for _, must := range []string{"go_goroutines ", "process_start_time_seconds ", `memo_build_info{version="v1.4.0-test"`, "go_info{"} {
		if !strings.Contains(buf.String(), must) {
			t.Errorf("missing %q", must)
		}
	}
}

func stripLe(labels string) string {
	if labels == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(labels, "{}"), ",")
	var kept []string
	for _, p := range parts {
		if !strings.HasPrefix(p, "le=") {
			kept = append(kept, p)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "{" + strings.Join(kept, ",") + "}"
}

func TestHandlerAndMetricsServer(t *testing.T) {
	r := NewRegistry()
	r.Counter("memo_a_total", "a").With().Inc()
	h := Handler(r)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" || !strings.Contains(rec.Body.String(), "memo_a_total 1") {
		t.Fatalf("GET: %d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST: %d", rec.Code)
	}
	ms := NewMetricsServer(r)
	if _, err := ms.Listen("0.0.0.0:0"); err == nil {
		t.Fatal("non-loopback must be refused")
	}
	ln, err := ms.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Host = ln.Addr().String()
	rec = httptest.NewRecorder()
	ms.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("bound host: %d", rec.Code)
	}
	req.Host = "evil.example"
	rec = httptest.NewRecorder()
	ms.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", rec.Code)
	}
	req.Host = ln.Addr().String()
	req.URL.Path = "/"
	rec = httptest.NewRecorder()
	ms.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other path: %d", rec.Code)
	}
}

func TestSetupLogging(t *testing.T) {
	var buf bytes.Buffer
	env := map[string]string{"MEMO_LOG_FORMAT": "json", "MEMO_LOG_LEVEL": "warn"}
	l := SetupLogging(&buf, func(k string) string { return env[k] })
	l.Info("dropped")
	l.Warn("kept", "k", 1)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one line, got %q", buf.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil || rec["level"] != "WARN" || rec["msg"] != "kept" || rec["k"] != float64(1) {
		t.Fatalf("json line: %v %q", err, lines[0])
	}
	buf.Reset()
	env = map[string]string{"MEMO_LOG_LEVEL": "loud", "MEMO_LOG_FORMAT": "xml"}
	l = SetupLogging(&buf, func(k string) string { return env[k] })
	l.Info("info shows at the default level")
	if !strings.Contains(buf.String(), "unknown MEMO_LOG_LEVEL") || !strings.Contains(buf.String(), "unknown MEMO_LOG_FORMAT") || !strings.Contains(buf.String(), "info shows") {
		t.Fatalf("fallback: %q", buf.String())
	}
	tm := Start()
	h := NewRegistry().Histogram("memo_t_seconds", "t", LatencyBuckets)
	tm.ObserveTo(h.With())
	if h.With().Count() != 1 {
		t.Fatal("timer did not observe")
	}
}
