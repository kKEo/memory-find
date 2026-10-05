package server

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

func TestTrackerInFlight(t *testing.T) {
	tr := newTracker(time.Now())
	id := tr.begin(&mcp.CallToolRequest{}, "ingest")
	ctx := context.WithValue(context.Background(), callInfoKey{}, &callInfo{tool: "ingest", liveID: id})
	tr.annotate(ctx, "web", "run-1")
	tr.mu.Lock()
	c := *tr.inFlight[id]
	ss := *tr.sessions["client:unknown"]
	tr.mu.Unlock()
	if c.Tool != "ingest" || c.Namespace != "web" || c.RunID != "run-1" || c.Client != "unknown" {
		t.Errorf("call = %+v", c)
	}
	if ss.Calls != 1 || ss.LastCall.IsZero() {
		t.Errorf("session = %+v", ss)
	}
	tr.end(id)
	if len(tr.inFlight) != 0 {
		t.Errorf("in flight after end: %d", len(tr.inFlight))
	}
}

// Over the in-memory transport: one session that made one call, nothing
// left in flight, and the background state is reported.
func TestLiveSnapshot(t *testing.T) {
	db, err := kb.Open(context.Background(), t.TempDir(), "live", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	srv := New(store, retrieve.New(store, retrieve.Default, false), "v9", WithKBPath("/x/live.db"))
	srv.SetBackground("idle")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := srv.mcp.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "snap-client", Version: "0"}, nil).Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	callTool(t, cs, "ingest", map[string]any{"content": "# S\n\nsnapshot\n", "source": map[string]any{"title": "S"}})

	snap := srv.Live()
	if snap.Version != "v9" || snap.KBPath != "/x/live.db" || snap.Background != "idle" || snap.Model == "" || len(snap.InFlight) != 0 {
		t.Errorf("snapshot = %+v", snap)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Client != "snap-client" || snap.Sessions[0].Calls != 1 {
		t.Errorf("sessions = %+v", snap.Sessions)
	}
}

// Without an open SDK session a client stays listed for clientIdle after
// its last call, then drops off.
func TestTrackerKeepsRecentClients(t *testing.T) {
	db, err := kb.Open(context.Background(), t.TempDir(), "idle", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, nil)
	srv := New(store, retrieve.New(store, retrieve.Default, false), "v")
	srv.live.end(srv.live.begin(&mcp.CallToolRequest{}, "search"))
	if snap := srv.Live(); len(snap.Sessions) != 1 {
		t.Fatalf("recent sessionless client dropped: %+v", snap.Sessions)
	}
	srv.live.mu.Lock()
	srv.live.sessions["client:unknown"].LastCall = time.Now().Add(-clientIdle - time.Minute)
	srv.live.mu.Unlock()
	if snap := srv.Live(); len(snap.Sessions) != 0 {
		t.Errorf("idle client kept: %+v", snap.Sessions)
	}
}
