package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/httpauth"
	"github.com/kKEo/memory-find/internal/live"
)

const token = "0123456789abcdef0123456789abcdef-tray-test"

// fakeServer answers like memo-mcp serve --http in token mode.
func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /live.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(live.Snapshot{Schema: live.Schema, Instance: "I", Sessions: []live.Session{{Key: "k", Client: "claude-code"}}})
	})
	mux.HandleFunc("POST /login-link", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(httpauth.LoginLink{URL: "/login?code=abc&next=" + r.URL.Query().Get("next"), ExpiresAt: time.Now().Add(time.Minute)})
	})
	mux.HandleFunc("GET /old/live.json", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("GET /moved/live.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
	})
	mux.HandleFunc("GET /future/live.json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"schema":2}`)) })
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func tokenFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "http-token")
	if err := os.WriteFile(p, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLiveAndLoginLink(t *testing.T) {
	ts := fakeServer(t)
	c, ctx := New(), context.Background()
	tgt := Target{URL: ts.URL, Auth: "token", TokenFile: tokenFile(t)}
	snap, err := c.Live(ctx, tgt)
	if err != nil || snap.Instance != "I" || len(snap.Sessions) != 1 {
		t.Fatalf("Live = %+v, %v", snap, err)
	}
	u, err := c.StatsURL(ctx, tgt, "/live")
	if err != nil || u != ts.URL+"/login?code=abc&next=/live" {
		t.Errorf("StatsURL = %q, %v", u, err)
	}
	if u, err := c.StatsURL(ctx, Target{URL: ts.URL, Auth: "none"}, "/live"); err != nil || u != ts.URL+"/live" {
		t.Errorf("StatsURL without auth = %q, %v", u, err)
	}
}

func TestErrors(t *testing.T) {
	ts := fakeServer(t)
	c, ctx := New(), context.Background()
	tf := tokenFile(t)
	wrong := filepath.Join(t.TempDir(), "wrong")
	if err := os.WriteFile(wrong, []byte(strings.Repeat("x", 40)), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	_ = ln.Close()

	for _, tc := range []struct {
		name string
		tgt  Target
		want error
		text string
	}{
		{"wrong token", Target{URL: ts.URL, Auth: "token", TokenFile: wrong}, ErrUnauthorized, ""},
		{"no token sent", Target{URL: ts.URL, Auth: "none"}, ErrUnauthorized, ""},
		{"old server", Target{URL: ts.URL + "/old", Auth: "none"}, ErrNotSupported, ""},
		{"redirect not followed", Target{URL: ts.URL + "/moved", Auth: "none"}, nil, "302"},
		{"newer schema", Target{URL: ts.URL + "/future", Auth: "none"}, nil, "schema 2"},
		{"nothing listening", Target{URL: closed, Auth: "none"}, ErrUnreachable, ""},
		{"token over plain http to another machine", Target{URL: "http://example.com:8765", Auth: "token", TokenFile: tf}, ErrInsecure, ""},
		{"mTLS", Target{URL: ts.URL, MTLS: true}, ErrMTLS, ""},
		{"missing token file", Target{URL: ts.URL, Auth: "token", TokenFile: filepath.Join(t.TempDir(), "none")}, nil, "token file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.Live(ctx, tc.tgt)
			if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) || (tc.text != "" && !strings.Contains(err.Error(), tc.text)) {
				t.Errorf("err = %v, want %v %q", err, tc.want, tc.text)
			}
		})
	}
}

func TestLoopback(t *testing.T) {
	for host, want := range map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "127.1.2.3": true, "example.com": false, "10.0.0.1": false} {
		if Loopback(host) != want {
			t.Errorf("Loopback(%q) = %v", host, !want)
		}
	}
}
