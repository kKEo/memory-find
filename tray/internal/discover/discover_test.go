package discover

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/runfile"
)

func TestScan(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	for _, pid := range []int{101, 102, 103, 104} {
		if _, err := runfile.Write(dir, runfile.Info{PID: pid, URL: "http://127.0.0.1:1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "serve-105.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	procs := map[int]Process{
		101: {Alive: true, Name: "memo-mcp", Started: past},   // ours
		102: {},                                               // gone
		103: {Alive: true, Name: "Safari", Started: past},     // pid reused by another program
		104: {Alive: true, Name: "memo-mcp", Started: future}, // pid reused by a later memo-mcp
	}
	entries, err := Scan(dir, func(pid int) Process { return procs[pid] })
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Info.PID != 101 || entries[1].Err == nil {
		t.Fatalf("entries = %+v", entries)
	}
	left, _ := runfile.List(dir)
	if len(left) != 2 {
		t.Errorf("stale files not removed: %+v", left)
	}
}

func TestScanRefusesUntrustedDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(dir, func(int) Process { return Process{} }); !errors.Is(err, ErrUntrusted) {
		t.Errorf("world-writable run dir: %v", err)
	}
	if entries, err := Scan(filepath.Join(dir, "missing"), nil); err != nil || entries != nil {
		t.Errorf("missing dir: %v %v", entries, err)
	}
}

func TestInspectSelf(t *testing.T) {
	p := Inspect(os.Getpid())
	if !p.Alive {
		t.Fatal("this process is not alive")
	}
	if p.Name != "" && (p.Started.IsZero() || p.Started.After(time.Now())) {
		t.Errorf("start time = %v", p.Started)
	}
	if Inspect(0).Alive || Inspect(1<<30).Alive {
		t.Error("nonexistent pid reported alive")
	}
}
