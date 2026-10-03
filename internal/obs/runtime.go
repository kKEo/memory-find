package obs

import (
	"context"
	"math"
	"runtime"
	"runtime/metrics"
	"time"
)

var processStart = time.Now()

// RuntimeCollector reports a curated subset of runtime/metrics under the
// names the Prometheus Go collector uses, so existing dashboards apply.
func RuntimeCollector() Collector {
	return CollectorFunc(func(_ context.Context, emit func(Point)) {
		samples := []metrics.Sample{
			{Name: "/sched/goroutines:goroutines"},
			{Name: "/memory/classes/heap/objects:bytes"},
			{Name: "/memory/classes/total:bytes"},
			{Name: "/gc/cycles/total:gc-cycles"},
			{Name: "/gc/pauses:seconds"},
		}
		metrics.Read(samples)
		gauge := func(name, help string, v metrics.Value) {
			switch v.Kind() {
			case metrics.KindUint64:
				emit(Point{Name: name, Help: help, Kind: GaugeKind, Value: float64(v.Uint64())})
			case metrics.KindFloat64:
				emit(Point{Name: name, Help: help, Kind: GaugeKind, Value: v.Float64()})
			}
		}
		gauge("go_goroutines", "Number of goroutines that currently exist.", samples[0].Value)
		gauge("go_memstats_heap_alloc_bytes", "Bytes of allocated heap objects.", samples[1].Value)
		gauge("go_memstats_sys_bytes", "Bytes of memory obtained from the OS.", samples[2].Value)
		if samples[3].Value.Kind() == metrics.KindUint64 {
			emit(Point{Name: "go_gc_cycles_total", Help: "Completed GC cycles.", Kind: CounterKind, Value: float64(samples[3].Value.Uint64())})
		}
		if samples[4].Value.Kind() == metrics.KindFloat64Histogram {
			h := samples[4].Value.Float64Histogram()
			p := Point{Name: "go_gc_pauses_seconds", Help: "Distribution of GC pause durations.", Kind: HistogramKind}
			var cum uint64
			for i, c := range h.Counts {
				cum += c
				le := math.Inf(1)
				if i+1 < len(h.Buckets) {
					le = h.Buckets[i+1]
				}
				p.Buckets = append(p.Buckets, Bucket{Le: le, Count: cum})
			}
			if len(p.Buckets) > 0 && !math.IsInf(p.Buckets[len(p.Buckets)-1].Le, 1) {
				p.Buckets = append(p.Buckets, Bucket{Le: math.Inf(1), Count: cum})
			}
			p.Count = cum
			emit(p)
		}
		emit(Point{Name: "go_info", Help: "Information about the Go environment.", Kind: GaugeKind, Labels: []Label{{Name: "version", Value: runtime.Version()}}, Value: 1})
	})
}

// ProcessCollector reports the process start time.
func ProcessCollector() Collector {
	return CollectorFunc(func(_ context.Context, emit func(Point)) {
		emit(Point{Name: "process_start_time_seconds", Help: "Start time of the process since unix epoch in seconds.", Kind: GaugeKind, Value: float64(processStart.Unix())})
	})
}

// BuildInfo registers the constant build-info gauge.
func BuildInfo(reg *Registry, version, protocol string) {
	reg.Gauge("memo_build_info", "Build information; always 1.", "version", "go_version", "mcp_protocol", "goos", "goarch").
		With(version, runtime.Version(), protocol, runtime.GOOS, runtime.GOARCH).Set(1)
}
