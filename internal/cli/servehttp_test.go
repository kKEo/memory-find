package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
	"github.com/kKEo/memory-find/internal/server"
	"github.com/kKEo/memory-find/internal/ui"
)

func TestServeHTTPRefusesRemoteAddress(t *testing.T) {
	t.Setenv("MEMO_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code := Main(context.Background(), "test", []string{"serve", "--http", "0.0.0.0:0"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "allow-remote") {
		t.Fatalf("non-loopback --http accepted: %d %s", code, errOut.String())
	}
}

// One HTTP server: an MCP client ingests over /mcp, and the hosted UI
// shows that client and the run on /live and /ingest.
func TestServeHTTPHostsMCPAndLiveUI(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := kb.Open(ctx, t.TempDir(), "live", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	svc := retrieve.New(store, retrieve.Default, false)
	srv := server.New(store, svc, "test", server.WithKBPath("/tmp/live.db"))
	ln, err := ui.Listen("127.0.0.1:0", false)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, ln, srv, store, svc) }()
	base := "http://" + ln.Addr().String()

	client := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "ingest", Arguments: map[string]any{
		"content": "# Live\n\nServed over HTTP.\n", "namespace": "web", "source": map[string]any{"title": "Live"}}})
	if err != nil || res.IsError {
		t.Fatalf("ingest: %v %+v", err, res)
	}

	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s: %d\n%s", path, resp.StatusCode, b)
		}
		return string(b)
	}
	body := get("/live")
	for _, want := range []string{"http-client", "/tmp/live.db", `<td>ingest</td><td class="num">1</td>`, `href="/live"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/live lacks %q", want)
		}
	}
	if body = get("/ingest"); !strings.Contains(body, `<span class="badge done">done</span>`) || !strings.Contains(body, "http-client") {
		t.Errorf("/ingest does not show the tool run:\n%s", body)
	}
	if body = get("/metrics"); !strings.Contains(body, "memo_mcp_tool_calls_total") {
		t.Error("/metrics lacks tool counters")
	}

	cs.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP did not stop")
	}
}
