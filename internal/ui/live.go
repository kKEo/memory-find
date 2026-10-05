package ui

import (
	"cmp"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/obs"
)

// Option configures New.
type Option func(*Server)

// WithLive attaches the in-memory state of the MCP server hosting this UI
// (memo-mcp serve --http). Without it the UI reads the file alone and has
// no /live page.
func WithLive(src live.Source) Option { return func(s *Server) { s.live = src } }

// WithRegistry sets where live counters are read from (default obs.Default()).
func WithRegistry(r *obs.Registry) Option { return func(s *Server) { s.registry = r } }

// ToolStat is one tool's calls since the server started.
type ToolStat struct {
	Tool        string
	Calls       int
	Errors      int
	P50, P95    float64 // milliseconds; NaN when nothing was timed
	InputNeeded int
}

// IngestStat is ingest throughput since the server started.
type IngestStat struct {
	New, Revision, Dedup, Error int
	Chunks, Vectors             int
	P50, P95                    float64 // milliseconds
}

type liveData struct {
	Snap   live.Snapshot
	Now    time.Time
	Tools  []ToolStat
	Ingest IngestStat
}

func (s *Server) livePage(w http.ResponseWriter, r *http.Request) {
	snap := s.live.Live()
	tools, ingest := liveStats(s.registry.Snapshot(r.Context()))
	s.renderPage(w, "live.html", page{Title: "Live", Refresh: refreshFor(r, true),
		Data: liveData{Snap: snap, Now: time.Now(), Tools: tools, Ingest: ingest}})
}

// liveStats folds the process's counters into per-tool and ingest rows.
func liveStats(snap obs.Snapshot) ([]ToolStat, IngestStat) {
	byTool := map[string]*ToolStat{}
	tool := func(name string) *ToolStat {
		t := byTool[name]
		if t == nil {
			t = &ToolStat{Tool: name, P50: math.NaN(), P95: math.NaN()}
			byTool[name] = t
		}
		return t
	}
	var ing IngestStat
	ing.P50, ing.P95 = math.NaN(), math.NaN()
	for _, f := range snap.Families {
		for _, se := range f.Series {
			switch f.Name {
			case "memo_mcp_tool_calls_total":
				t := tool(label(se, "tool"))
				t.Calls += int(se.Value)
				switch label(se, "outcome") {
				case "error", "tool_error":
					t.Errors += int(se.Value)
				case "input_required":
					t.InputNeeded += int(se.Value)
				}
			case "memo_mcp_tool_call_duration_seconds":
				t := tool(label(se, "tool"))
				t.P50, t.P95 = 1000*quantile(se.Buckets, 0.5), 1000*quantile(se.Buckets, 0.95)
			case "memo_store_ingests_total":
				n := int(se.Value)
				switch label(se, "outcome") {
				case "new":
					ing.New += n
				case "revision":
					ing.Revision += n
				case "dedup":
					ing.Dedup += n
				case "error":
					ing.Error += n
				}
			case "memo_store_chunks_written_total":
				ing.Chunks += int(se.Value)
			case "memo_store_vectors_stored_total":
				ing.Vectors += int(se.Value)
			case "memo_store_ingest_duration_seconds":
				ing.P50, ing.P95 = 1000*quantile(se.Buckets, 0.5), 1000*quantile(se.Buckets, 0.95)
			}
		}
	}
	out := make([]ToolStat, 0, len(byTool))
	for _, t := range byTool {
		if t.Calls > 0 {
			out = append(out, *t)
		}
	}
	slices.SortFunc(out, func(a, b ToolStat) int { return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Tool, b.Tool)) })
	return out, ing
}

func label(se obs.Series, name string) string {
	for _, l := range se.Labels {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// quantile estimates q from cumulative buckets the way Prometheus'
// histogram_quantile does: linear inside the bucket that crosses the rank.
// NaN when the histogram is empty; the last finite bound when the rank
// falls in +Inf.
func quantile(buckets []obs.Bucket, q float64) float64 {
	if len(buckets) == 0 {
		return math.NaN()
	}
	total := buckets[len(buckets)-1].Count
	if total == 0 {
		return math.NaN()
	}
	rank := q * float64(total)
	prevLe, prevCount := 0.0, uint64(0)
	for _, b := range buckets {
		if float64(b.Count) >= rank {
			if math.IsInf(b.Le, 1) {
				return prevLe
			}
			if b.Count == prevCount {
				return b.Le
			}
			return prevLe + (b.Le-prevLe)*(rank-float64(prevCount))/float64(b.Count-prevCount)
		}
		prevLe, prevCount = b.Le, b.Count
	}
	return prevLe
}
