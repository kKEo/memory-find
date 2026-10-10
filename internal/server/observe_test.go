package server

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/obs"
	"github.com/kKEo/memors/internal/retrieve"
)

// newObservedSession is newTestSession with a private registry so counters
// can be asserted exactly.
func newObservedSession(t *testing.T, opts ...Option) (*mcp.ClientSession, *obs.Registry, *kb.Store) {
	t.Helper()
	db, err := kb.Open(context.Background(), t.TempDir(), "obs", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	reg := obs.NewRegistry()
	srv := New(store, retrieve.New(store, retrieve.Default, false), "test", append([]Option{WithRegistry(reg)}, opts...)...)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.mcp.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, reg, store
}

func counter(reg *obs.Registry, name string, labels ...string) float64 {
	snap := reg.Snapshot(context.Background())
	for _, f := range snap.Families {
		if f.Name != name {
			continue
		}
		for _, s := range f.Series {
			match := len(s.Labels) == len(labels)
			for i := range labels {
				if i < len(s.Labels) && s.Labels[i].Value != labels[i] {
					match = false
				}
			}
			if match {
				return s.Value
			}
		}
	}
	return -1
}

func histCount(reg *obs.Registry, name string, labels ...string) uint64 {
	snap := reg.Snapshot(context.Background())
	for _, f := range snap.Families {
		if f.Name != name {
			continue
		}
		for _, s := range f.Series {
			match := true
			for i := range labels {
				if i >= len(s.Labels) || s.Labels[i].Value != labels[i] {
					match = false
				}
			}
			if match {
				return s.Count
			}
		}
	}
	return 0
}

func TestMiddlewareCountsToolCallsAndErrors(t *testing.T) {
	cs, reg, _ := newObservedSession(t)
	ingestDoc(t, cs)
	callTool(t, cs, "search", map[string]any{"query": "ERR_CONN_RESET"})
	callTool(t, cs, "search", map[string]any{"query": "interceptor order"})
	res := callTool(t, cs, "read", map[string]any{"uri": "memo://doc/does-not-exist"})
	if !res.IsError {
		t.Fatal("expected a tool error")
	}
	if v := counter(reg, "memors_mcp_tool_calls_total", "search", "ok"); v != 2 {
		t.Fatalf("search ok = %v", v)
	}
	if v := counter(reg, "memors_mcp_tool_calls_total", "read", "tool_error"); v != 1 {
		t.Fatalf("read tool_error = %v", v)
	}
	if v := counter(reg, "memors_mcp_tool_errors_total", "read", "not_found"); v != 1 {
		t.Fatalf("read not_found = %v", v)
	}
	if n := histCount(reg, "memors_mcp_tool_call_duration_seconds", "search"); n != 2 {
		t.Fatalf("search latency observations = %d", n)
	}
	if v := counter(reg, "memors_mcp_requests_in_flight"); v != 0 {
		t.Fatalf("in flight after calls = %v", v)
	}
	if v := counter(reg, "memors_mcp_requests_total", "tools/call", "ok"); v != 4 {
		t.Fatalf("requests tools/call ok = %v", v)
	}
	if v := counter(reg, "memors_mcp_sessions_total", "test-client"); v != 1 {
		t.Fatalf("sessions = %v", v)
	}
	if _, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "memo://index"}); err != nil {
		t.Fatal(err)
	}
	if v := counter(reg, "memors_mcp_resource_reads_total", "index", "ok"); v != 1 {
		t.Fatalf("resource reads = %v", v)
	}
}

// The server does not advertise the deprecated MCP logging capability; tools
// and resources are still there.
func TestServerDoesNotAdvertiseLogging(t *testing.T) {
	cs, _ := newTestSession(t)
	caps := cs.InitializeResult().Capabilities
	raw, _ := json.Marshal(caps)
	if caps == nil || strings.Contains(string(raw), `"logging"`) {
		t.Fatalf("logging capability advertised: %s", raw)
	}
	if caps.Tools == nil || caps.Resources == nil {
		t.Fatalf("tools/resources capabilities missing: %+v", caps)
	}
}

// Serve mode's stdout is the MCP stream: a tool round trip with logging on
// must write nothing to os.Stdout.
func TestToolCallsWriteNothingToStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	var logBuf strings.Builder
	obs.SetupLogging(&logBuf, func(k string) string { return map[string]string{"MEMORS_LOG_LEVEL": "debug"}[k] })
	cs, _, _ := newObservedSession(t)
	ingestDoc(t, cs)
	callTool(t, cs, "search", map[string]any{"query": "ERR_CONN_RESET", "response_format": "explain"})
	callTool(t, cs, "status", map[string]any{})
	_ = w.Close()
	os.Stdout = old
	got, _ := io.ReadAll(r)
	if len(got) != 0 {
		t.Fatalf("stdout received %d bytes: %q", len(got), got)
	}
	if !strings.Contains(logBuf.String(), "tool call") || !strings.Contains(logBuf.String(), "tool=search") {
		t.Fatalf("expected tool call log lines on the logger: %q", logBuf.String())
	}
}

func TestSummarizeArgsDropsContent(t *testing.T) {
	out := summarizeArgs("ingest", []byte(`{"content":"secret body","namespace":"n","source":{"uri":"https://x","title":"t"}}`))
	if strings.Contains(string(out), "secret") || !strings.Contains(string(out), `"content_len"`) || !strings.Contains(string(out), `"namespace"`) {
		t.Fatalf("ingest summary: %s", out)
	}
	out = summarizeArgs("remember", []byte(`{"statement":"private","namespace":"n","about":["x"]}`))
	if strings.Contains(string(out), "private") || !strings.Contains(string(out), `"about"`) {
		t.Fatalf("remember summary: %s", out)
	}
	if string(summarizeArgs("status", []byte(`not json`))) != "{}" {
		t.Fatal("bad json should give {}")
	}
}

// With the opt-in log on, every tool call leaves a row with timing, outcome
// and an argument summary that never contains written content.
func TestCallLogRoundTripViaMCP(t *testing.T) {
	cs, _, store := newObservedSession(t, WithCallLog(true))
	ingestDoc(t, cs)
	callTool(t, cs, "search", map[string]any{"query": "ERR_CONN_RESET"})
	callTool(t, cs, "read", map[string]any{"uri": "memo://doc/nope"})
	rows, err := store.CallLogTail(context.Background(), 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("call rows: %d %v", len(rows), err)
	}
	byTool := map[string]kb.CallLogEntry{}
	for _, r := range rows {
		byTool[r.Tool] = r
	}
	if r := byTool["ingest"]; !r.OK || r.Client != "test-client" || strings.Contains(string(r.Args), "Interceptors run") || !strings.Contains(string(r.Args), "content_len") {
		t.Fatalf("ingest row: %+v %s", r, r.Args)
	}
	if r := byTool["search"]; !r.OK || r.NResults < 1 || r.TokensOut == 0 || !strings.Contains(string(r.Args), "ERR_CONN_RESET") {
		t.Fatalf("search row: %+v %s", r, r.Args)
	}
	if r := byTool["read"]; r.OK || r.ErrorClass != "not_found" {
		t.Fatalf("read row: %+v", r)
	}
	stats, err := store.CallLogStats(context.Background(), time.Time{})
	if err != nil || len(stats) != 3 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
	for _, st := range stats {
		if st.Tool == "read" && st.Errors != 1 {
			t.Fatalf("read errors: %+v", st)
		}
	}
	// Without the option nothing is written.
	cs2, _, store2 := newObservedSession(t)
	ingestDoc(t, cs2)
	if rows, _ := store2.CallLogTail(context.Background(), 10); len(rows) != 0 {
		t.Fatalf("call log written without opt-in: %d", len(rows))
	}
}
