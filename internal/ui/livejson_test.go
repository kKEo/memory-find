package ui

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/live"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/live.golden.json from the /live.json response")

// /live.json is a contract with companion apps: its shape is pinned by a
// golden file (run with -update to review and accept a deliberate change;
// only additions are allowed without bumping live.Schema).
func TestLiveJSONGolden(t *testing.T) {
	_, store, svc := newUI(t)
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	src := fakeLive{live.Snapshot{Instance: "K2Q7EXAMPLEINSTANCE", PID: 4242, Version: "v1.5.0", KB: "crportal",
		KBPath: "/home/u/.memo-mcp/kb/crportal.db", Model: "granite-small-r2", Started: t0, Background: "idle",
		Sessions: []live.Session{
			{ID: "RAWSESSIONID1", Key: "3fa94c0d12ab", Client: "claude-code", ClientVersion: "2.1.4", Since: t0.Add(time.Minute),
				LastSeen: t0.Add(3 * time.Minute), LastCall: t0.Add(2 * time.Minute), Calls: 14, Open: true, Stream: true},
			{ID: "RAWSESSIONID2", Key: "9d1c22e07f10", Client: "cursor", Since: t0.Add(5 * time.Minute), LastSeen: t0.Add(5 * time.Minute), Open: true},
		},
		InFlight: []live.Call{{Tool: "ingest", Client: "claude-code", Session: "RAWSESSIONID1", SessionKey: "3fa94c0d12ab",
			Started: t0.Add(3 * time.Minute), Namespace: "web", RunID: "run-42"}}}}
	h := New(store, svc, "test", WithLive(src)).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/live.json", nil))
	if rec.Code != 200 {
		t.Fatalf("/live.json: %d\n%s", rec.Code, rec.Body)
	}
	if ct, cc := rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"); ct != "application/json" || cc != "no-store" {
		t.Errorf("headers: Content-Type %q, Cache-Control %q", ct, cc)
	}
	if strings.Contains(rec.Body.String(), "RAWSESSIONID") {
		t.Errorf("/live.json leaks a raw session id:\n%s", rec.Body)
	}
	var got bytes.Buffer
	if err := json.Indent(&got, rec.Body.Bytes(), "", "  "); err != nil {
		t.Fatal(err)
	}
	got.WriteByte('\n')

	goldenPath := filepath.Join("testdata", "live.golden.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("/live.json does not match %s (run with -update to review/accept the diff)\n--- got ---\n%s", goldenPath, got.Bytes())
	}
}

func TestLiveJSONRoutes(t *testing.T) {
	_, store, svc := newUI(t)
	srv := New(store, svc, "test", WithLive(fakeLive{}))
	srv.AllowHost("127.0.0.1:8765")
	h := srv.Handler()

	code, body := get(t, h, "/live.json", "127.0.0.1:8765")
	if code != 200 {
		t.Fatalf("/live.json: %d", code)
	}
	var snap map[string]any
	if err := json.Unmarshal([]byte(body), &snap); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, body)
	}
	if snap["schema"] != float64(live.Schema) || !strings.Contains(body, `"sessions":[]`) || !strings.Contains(body, `"in_flight":[]`) {
		t.Errorf("empty snapshot = %s", body)
	}
	if code, _ = get(t, h, "/live.json", "evil.example:8765"); code != http.StatusForbidden {
		t.Errorf("foreign Host: %d, want 403", code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/live.json", nil)
	req.Host = "127.0.0.1:8765"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", rec.Code)
	}
	if code, _ = get(t, New(store, svc, "test").Handler(), "/live.json", ""); code != 404 {
		t.Errorf("standalone /live.json: %d, want 404", code)
	}
}
