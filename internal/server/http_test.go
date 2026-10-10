package server

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/live"
	"github.com/kKEo/memors/internal/obs"
	"github.com/kKEo/memors/internal/retrieve"
)

// Over streamable HTTP a go-sdk client probes with server/discover on a
// throwaway session before it initialises. Only the real session may show
// up: once, with the client's name and version, open and streaming while
// connected, and still listed (but neither) after it closes.
func TestHTTPSessionsAreReal(t *testing.T) {
	ctx := context.Background()
	db, err := kb.Open(ctx, t.TempDir(), "http", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	reg := obs.NewRegistry()
	srv := New(store, retrieve.New(store, retrieve.Default, false), "test", WithRegistry(reg))
	ts := httptest.NewServer(srv.HTTPHandler(HTTPOptions{}))
	// The client's event stream never ends on its own: drop connections
	// first, or a failed assertion hangs in Close.
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "0"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callTool(t, cs, "status", map[string]any{})

	var ss live.Session
	eventually(t, "one streaming session", func() bool {
		snap := srv.Live()
		if len(snap.Sessions) != 1 {
			return false
		}
		ss = snap.Sessions[0]
		return ss.Stream
	})
	if ss.Client != "http-client" || ss.ClientVersion != "0" || !ss.Open || ss.Calls != 1 || ss.ID == "" || ss.Key != live.SessionKey(ss.ID) {
		t.Errorf("session = %+v", ss)
	}
	if v := counter(reg, "memors_mcp_sessions_total", "http-client"); v != 1 {
		t.Errorf("sessions_total{http-client} = %v, want 1", v)
	}
	if v := counter(reg, "memors_mcp_sessions_total", "unknown"); v != -1 {
		t.Errorf("the discover probe was counted as a session: %v", v)
	}

	if err := cs.Close(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "session closed and stream gone", func() bool {
		snap := srv.Live()
		return len(snap.Sessions) == 1 && !snap.Sessions[0].Open && !snap.Sessions[0].Stream
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
