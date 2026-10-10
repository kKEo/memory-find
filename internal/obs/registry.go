// Package obs is memors-mcp's observability toolkit: a small metrics registry
// (counters, gauges, histograms with labels) rendered in the Prometheus text
// format, a curated set of Go runtime metrics, an HTTP handler that binds
// loopback only, and structured-logging setup. Standard library only.
//
// Nothing here is telemetry: no value is sent anywhere. Metrics are pulled
// from a loopback address by whoever runs a scraper on the same machine, and
// logs go to this process's stderr.
package obs

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Kind is a metric family's type.
type Kind string

const (
	CounterKind   Kind = "counter"
	GaugeKind     Kind = "gauge"
	HistogramKind Kind = "histogram"
)

// Label is one name=value pair.
type Label struct{ Name, Value string }

// Bucket is one cumulative histogram bucket.
type Bucket struct {
	Le    float64 // upper bound, math.Inf(1) for +Inf
	Count uint64
}

// Point is one series as a collector or snapshot reports it.
type Point struct {
	Name    string
	Help    string
	Kind    Kind
	Labels  []Label
	Value   float64  // counter or gauge value
	Buckets []Bucket // histogram, cumulative, ending with +Inf
	Sum     float64
	Count   uint64
}

// Collector produces points at snapshot time (for values that are cheaper
// to read than to keep current, such as table counts).
type Collector interface {
	Collect(ctx context.Context, emit func(Point))
}

// CollectorFunc adapts a function to Collector.
type CollectorFunc func(ctx context.Context, emit func(Point))

// Collect implements Collector.
func (f CollectorFunc) Collect(ctx context.Context, emit func(Point)) { f(ctx, emit) }

// Registry holds metric families and collectors.
type Registry struct {
	mu         sync.RWMutex
	families   map[string]*family
	collectors []Collector
}

type family struct {
	name, help string
	kind       Kind
	labels     []string
	buckets    []float64
	mu         sync.RWMutex
	series     map[string]*series
}

type series struct {
	values  []string
	bits    atomic.Uint64   // counter/gauge value as float64 bits
	counts  []atomic.Uint64 // histogram bucket counts (non-cumulative), len(buckets)+1
	sumBits atomic.Uint64
	count   atomic.Uint64
}

var defaultRegistry = NewRegistry()

// Default is the process-wide registry.
func Default() *Registry { return defaultRegistry }

// NewRegistry makes an empty registry.
func NewRegistry() *Registry { return &Registry{families: map[string]*family{}} }

func (r *Registry) family(name, help string, kind Kind, labels []string, buckets []float64) *family {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.families[name]; ok {
		if f.kind != kind || strings.Join(f.labels, ",") != strings.Join(labels, ",") {
			panic(fmt.Sprintf("obs: metric %q registered twice with a different kind or label set", name))
		}
		return f
	}
	if !validName(name) {
		panic(fmt.Sprintf("obs: invalid metric name %q", name))
	}
	for _, l := range labels {
		if !validName(l) {
			panic(fmt.Sprintf("obs: invalid label name %q on %s", l, name))
		}
	}
	f := &family{name: name, help: help, kind: kind, labels: append([]string(nil), labels...), buckets: append([]float64(nil), buckets...), series: map[string]*series{}}
	r.families[name] = f
	return f
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r == ':' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

// Counter registers (or returns) a counter family.
func (r *Registry) Counter(name, help string, labels ...string) *CounterVec {
	return &CounterVec{f: r.family(name, help, CounterKind, labels, nil)}
}

// Gauge registers (or returns) a gauge family.
func (r *Registry) Gauge(name, help string, labels ...string) *GaugeVec {
	return &GaugeVec{f: r.family(name, help, GaugeKind, labels, nil)}
}

// Histogram registers (or returns) a histogram family with the given upper
// bounds (ascending; +Inf is added).
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *HistogramVec {
	if !sort.Float64sAreSorted(buckets) {
		panic("obs: histogram buckets must be ascending: " + name)
	}
	return &HistogramVec{f: r.family(name, help, HistogramKind, labels, buckets)}
}

// AddCollector registers a collector run at every Snapshot.
func (r *Registry) AddCollector(c Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors = append(r.collectors, c)
}

func (f *family) get(values []string) *series {
	if len(values) != len(f.labels) {
		panic(fmt.Sprintf("obs: %s wants %d label values, got %d", f.name, len(f.labels), len(values)))
	}
	key := strings.Join(values, "\xff")
	f.mu.RLock()
	s := f.series[key]
	f.mu.RUnlock()
	if s != nil {
		return s
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if s = f.series[key]; s != nil {
		return s
	}
	s = &series{values: append([]string(nil), values...), counts: make([]atomic.Uint64, len(f.buckets)+1)}
	f.series[key] = s
	return s
}

// CounterVec is a counter family.
type CounterVec struct{ f *family }

// Counter is one counter series.
type Counter struct{ s *series }

// With returns the series for the label values.
func (v *CounterVec) With(values ...string) *Counter { return &Counter{s: v.f.get(values)} }

// Inc adds one.
func (c *Counter) Inc() { c.Add(1) }

// Add adds a non-negative amount.
func (c *Counter) Add(d float64) {
	if d < 0 {
		panic("obs: counter decreased")
	}
	addFloat(&c.s.bits, d)
}

// Value reads the counter.
func (c *Counter) Value() float64 { return math.Float64frombits(c.s.bits.Load()) }

// GaugeVec is a gauge family.
type GaugeVec struct{ f *family }

// Gauge is one gauge series.
type Gauge struct{ s *series }

// With returns the series for the label values.
func (v *GaugeVec) With(values ...string) *Gauge { return &Gauge{s: v.f.get(values)} }

// Set stores a value.
func (g *Gauge) Set(x float64) { g.s.bits.Store(math.Float64bits(x)) }

// Inc adds one.
func (g *Gauge) Inc() { addFloat(&g.s.bits, 1) }

// Dec subtracts one.
func (g *Gauge) Dec() { addFloat(&g.s.bits, -1) }

// Value reads the gauge.
func (g *Gauge) Value() float64 { return math.Float64frombits(g.s.bits.Load()) }

// HistogramVec is a histogram family.
type HistogramVec struct{ f *family }

// Histogram is one histogram series.
type Histogram struct {
	s       *series
	buckets []float64
}

// With returns the series for the label values.
func (v *HistogramVec) With(values ...string) *Histogram {
	return &Histogram{s: v.f.get(values), buckets: v.f.buckets}
}

// Observe records one value.
func (h *Histogram) Observe(x float64) {
	i := sort.SearchFloat64s(h.buckets, x) // first bucket with le >= x
	h.s.counts[i].Add(1)
	h.s.count.Add(1)
	addFloat(&h.s.sumBits, x)
}

// Count is the number of observations.
func (h *Histogram) Count() uint64 { return h.s.count.Load() }

// Sum is the total of observations.
func (h *Histogram) Sum() float64 { return math.Float64frombits(h.s.sumBits.Load()) }

func addFloat(bits *atomic.Uint64, d float64) {
	for {
		old := bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + d)
		if bits.CompareAndSwap(old, next) {
			return
		}
	}
}

// Snapshot is a consistent-enough read of every family and collector.
type Snapshot struct{ Families []Family }

// Family is one metric with its series.
type Family struct {
	Name, Help string
	Kind       Kind
	Series     []Series
}

// Series is one labelled value.
type Series struct {
	Labels  []Label
	Value   float64
	Buckets []Bucket
	Sum     float64
	Count   uint64
}

// Snapshot reads all registered families and runs the collectors.
func (r *Registry) Snapshot(ctx context.Context) Snapshot {
	r.mu.RLock()
	fams := make([]*family, 0, len(r.families))
	for _, f := range r.families {
		fams = append(fams, f)
	}
	collectors := append([]Collector(nil), r.collectors...)
	r.mu.RUnlock()

	out := map[string]*Family{}
	for _, f := range fams {
		fam := &Family{Name: f.name, Help: f.help, Kind: f.kind}
		f.mu.RLock()
		for _, s := range f.series {
			ser := Series{Labels: labelsOf(f.labels, s.values)}
			switch f.kind {
			case HistogramKind:
				var cum uint64
				for i, le := range f.buckets {
					cum += s.counts[i].Load()
					ser.Buckets = append(ser.Buckets, Bucket{Le: le, Count: cum})
				}
				cum += s.counts[len(f.buckets)].Load()
				ser.Buckets = append(ser.Buckets, Bucket{Le: math.Inf(1), Count: cum})
				ser.Sum = math.Float64frombits(s.sumBits.Load())
				ser.Count = s.count.Load()
			default:
				ser.Value = math.Float64frombits(s.bits.Load())
			}
			fam.Series = append(fam.Series, ser)
		}
		f.mu.RUnlock()
		out[f.name] = fam
	}
	for _, c := range collectors {
		c.Collect(ctx, func(p Point) {
			fam := out[p.Name]
			if fam == nil {
				fam = &Family{Name: p.Name, Help: p.Help, Kind: p.Kind}
				out[p.Name] = fam
			}
			fam.Series = append(fam.Series, Series{Labels: p.Labels, Value: p.Value, Buckets: p.Buckets, Sum: p.Sum, Count: p.Count})
		})
	}
	snap := Snapshot{}
	for _, fam := range out {
		sort.Slice(fam.Series, func(i, j int) bool { return labelKey(fam.Series[i].Labels) < labelKey(fam.Series[j].Labels) })
		snap.Families = append(snap.Families, *fam)
	}
	sort.Slice(snap.Families, func(i, j int) bool { return snap.Families[i].Name < snap.Families[j].Name })
	return snap
}

func labelsOf(names, values []string) []Label {
	out := make([]Label, len(names))
	for i := range names {
		out[i] = Label{Name: names[i], Value: values[i]}
	}
	return out
}

func labelKey(ls []Label) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.Name + "=" + l.Value
	}
	return strings.Join(parts, ",")
}

// Bucket presets. Latencies are in seconds (millisecond granularity at the
// low end); tokens and counts cover the sizes a search or a tool call returns.
var (
	LatencyBuckets = []float64{.001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
	TokenBuckets   = []float64{50, 100, 200, 500, 1000, 2000, 4000, 8000, 16000}
	CountBuckets   = []float64{0, 1, 2, 5, 10, 20, 50, 100, 200, 500}
	BytesBuckets   = []float64{1 << 10, 4 << 10, 16 << 10, 64 << 10, 256 << 10, 1 << 20, 4 << 20}
)
