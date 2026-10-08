package supervisor

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/runfile"
	"github.com/kKEo/memory-find/tray/internal/client"
	"github.com/kKEo/memory-find/tray/internal/discover"
)

// With MEMO_TRAY_E2E_BIN set to a real memo-mcp (make build), memo-tray
// starts it on a fresh MEMO_HOME, finds its run file, reads /live.json and
// stops it cleanly. MEMO_MODEL names no model, so the server runs keyword
// only and downloads nothing.
func TestE2ERealServer(t *testing.T) {
	bin := os.Getenv("MEMO_TRAY_E2E_BIN")
	if bin == "" {
		t.Skip("set MEMO_TRAY_E2E_BIN to a memo-mcp binary to run")
	}
	h := t.TempDir()
	s := New(h, os.Environ())
	addr := freeAddr(t)
	if err := s.Start(bin, Spec{KB: "e2e", Addr: addr, Env: map[string]string{"MEMO_MODEL": "not-a-model"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.StopAll(5 * time.Second) })
	pid := childOf(t, s, "e2e").PID

	var e runfile.Entry
	eventually(t, "the server's run file", func() bool {
		if c := childOf(t, s, "e2e"); c.State == Crashed {
			t.Fatalf("server crashed: %s (%s)", c.Exit, c.LastLog)
		}
		entries, _ := discover.Scan(runfile.Dir(h), discover.Inspect)
		for _, x := range entries {
			if x.Err == nil && x.Info.PID == pid {
				e = x
				return true
			}
		}
		return false
	})
	if e.Info.URL != "http://"+addr || e.Info.KB != "e2e" || e.Info.Auth != "none" {
		t.Errorf("run file = %+v", e.Info)
	}
	snap, err := client.New().Live(context.Background(), client.TargetOf(e.Info))
	if err != nil || snap.Schema != live.Schema || snap.KB != "e2e" || snap.PID != pid || snap.Instance != e.Info.Instance {
		t.Fatalf("/live.json = %+v, %v", snap, err)
	}

	s.Stop("e2e", 10*time.Second)
	if c := childOf(t, s, "e2e"); c.State != Stopped || c.Exit != "exited" {
		t.Errorf("after stop: %+v", c)
	}
	if entries, _ := runfile.List(runfile.Dir(h)); len(entries) != 0 {
		t.Errorf("run file left behind: %+v", entries)
	}
}
