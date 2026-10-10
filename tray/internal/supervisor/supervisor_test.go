package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	ossignal "os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kKEo/memors/internal/live"
	"github.com/kKEo/memors/internal/runfile"
	"github.com/kKEo/memors/tray/internal/client"
	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/discover"
)

// The test binary doubles as a fake memors-mcp: with TRAY_FAKE_MEMORS set it
// behaves like `memors-mcp serve --http <addr>` in the way chosen by
// TRAY_FAKE_MODE, instead of running tests.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TRAY_FAKE_MEMORS"); mode != "" {
		os.Exit(fakeMemors(mode))
	}
	os.Exit(m.Run())
}

func fakeMemors(mode string) int {
	fmt.Printf("args=%s kb=%s home=%s auth_env=%q\n", strings.Join(os.Args[1:], " "), os.Getenv("MEMORS_KB"), os.Getenv("MEMORS_HOME"), os.Getenv("MEMORS_HTTP_AUTH"))
	switch mode {
	case "crash":
		fmt.Fprintln(os.Stderr, "fatal: the knowledge base is locked")
		return 3
	case "stubborn":
		ossignal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
		return 0
	}
	if len(os.Args) < 4 || os.Args[1] != "serve" || os.Args[2] != "--http" {
		return 2
	}
	addr := os.Args[3]
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	instance := fmt.Sprint("fake-", os.Getpid())
	path, err := runfile.Write(runfile.Dir(os.Getenv("MEMORS_HOME")), runfile.Info{PID: os.Getpid(), Instance: instance, KB: os.Getenv("MEMORS_KB"),
		URL: "http://" + ln.Addr().String(), Listen: ln.Addr().String(), Scheme: "http", Auth: "none", Started: time.Now()})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	go func() {
		_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(live.Snapshot{Schema: live.Schema, PID: os.Getpid(), Instance: instance})
		}))
	}()
	stop := make(chan os.Signal, 1)
	ossignal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	<-stop
	_ = runfile.Remove(path)
	return 0
}

func newSupervisor(t *testing.T, mode string) (*Supervisor, string) {
	t.Helper()
	h := t.TempDir()
	env := append(os.Environ(), "TRAY_FAKE_MEMORS="+mode, "MEMORS_HTTP_AUTH=none", "MEMORS_KB=wrong")
	return New(h, env), h
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func childOf(t *testing.T, s *Supervisor, kb string) Child {
	t.Helper()
	for _, c := range s.Snapshot() {
		if c.Spec.KB == kb {
			return c
		}
	}
	t.Fatalf("no child for %s", kb)
	return Child{}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartAndStop(t *testing.T) {
	s, h := newSupervisor(t, "serve")
	bin, _ := os.Executable()
	if err := s.Start(bin, Spec{KB: "crportal", Addr: freeAddr(t), Env: map[string]string{"MEMORS_MODEL": "potion"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.StopAll(time.Second) })
	if err := s.Start(bin, Spec{KB: "crportal", Addr: freeAddr(t)}); err == nil {
		t.Error("a second server for the same knowledge base started")
	}
	pid := childOf(t, s, "crportal").PID
	var entries []runfile.Entry
	eventually(t, "the run file", func() bool {
		entries, _ = runfile.List(runfile.Dir(h))
		return len(entries) == 1 && entries[0].Info.PID == pid
	})
	if s.Running() != 1 || childOf(t, s, "crportal").State != Running {
		t.Errorf("children = %+v", s.Snapshot())
	}

	s.Stop("crportal", 5*time.Second)
	c := childOf(t, s, "crportal")
	if c.State != Stopped || c.Exit != "exited" || s.Running() != 0 {
		t.Errorf("after stop: %+v", c)
	}
	if entries, _ := runfile.List(runfile.Dir(h)); len(entries) != 0 {
		t.Errorf("run file left: %+v", entries)
	}
	log, _ := os.ReadFile(c.Log)
	for _, want := range []string{"--- memors-tray", "args=serve --http", "kb=crportal home=" + h, `auth_env=""`} {
		if !strings.Contains(string(log), want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if fi, _ := os.Stat(c.Log); fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode %o", fi.Mode().Perm())
	}
	if err := s.Start(bin, Spec{KB: "crportal", Addr: freeAddr(t)}); err != nil {
		t.Errorf("restart after stop: %v", err)
	}
}

func TestCrash(t *testing.T) {
	s, _ := newSupervisor(t, "crash")
	bin, _ := os.Executable()
	if err := s.Start(bin, Spec{KB: "a", Addr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the crash", func() bool { return childOf(t, s, "a").State == Crashed })
	c := childOf(t, s, "a")
	if c.Exit != "exit status 3" || c.LastLog != "fatal: the knowledge base is locked" {
		t.Errorf("crash = %+v", c)
	}
}

func TestStopKillsAStubbornServer(t *testing.T) {
	s, _ := newSupervisor(t, "stubborn")
	bin, _ := os.Executable()
	if err := s.Start(bin, Spec{KB: "a", Addr: "127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // let it ignore SIGTERM first
	start := time.Now()
	s.Stop("a", 300*time.Millisecond)
	if c := childOf(t, s, "a"); c.State != Stopped || c.Exit != "signal: killed" || time.Since(start) < 300*time.Millisecond {
		t.Errorf("after stop: %+v (%v)", c, time.Since(start))
	}
}

func TestChildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "MEMORS_KB=x", "MEMORS_HOME=/old", "MEMORS_HTTP_ADDR=0.0.0.0:1", "MEMORS_TLS_CERT=c", "MEMORS_PUBLIC_URL=u",
		"MEMORS_METRICS_ADDR=m", "MEMORS_MODEL=granite", "MEMORS_QUERY_LOG=1"}
	got := childEnv(base, "/h", Spec{KB: "kb1", Env: map[string]string{"MEMORS_MODEL": "potion", "MEMORS_HTTP_AUTH": "none"}})
	want := []string{"PATH=/bin", "MEMORS_QUERY_LOG=1", "MEMORS_HOME=/h", "MEMORS_KB=kb1", "MEMORS_MODEL=potion"}
	if !slices.Equal(got, want) {
		t.Errorf("childEnv =\n%q\nwant\n%q", got, want)
	}
	if !config.Reserved("MEMORS_HTTP_AUTH") {
		t.Error("MEMORS_HTTP_AUTH must be reserved")
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "serve-a.log")
	if err := os.WriteFile(p, make([]byte, maxLog+1), 0o600); err != nil {
		t.Fatal(err)
	}
	rotate(p)
	if _, err := os.Stat(p + ".1"); err != nil {
		t.Errorf("not rotated: %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old log still there: %v", err)
	}
}

// A server started elsewhere is stopped only once memors-tray has made sure
// the pid still is that server.
func TestStopExternal(t *testing.T) {
	s, h := newSupervisor(t, "serve")
	bin, _ := os.Executable()
	if err := s.Start(bin, Spec{KB: "ext", Addr: freeAddr(t)}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.StopAll(time.Second) })
	var e runfile.Entry
	eventually(t, "the run file", func() bool {
		entries, _ := discover.Scan(runfile.Dir(h), func(int) discover.Process { return discover.Process{Alive: true} })
		if len(entries) == 1 {
			e = entries[0]
		}
		return len(entries) == 1
	})
	ctx := context.Background()
	alive := func(int) discover.Process {
		return discover.Process{Alive: true, Name: "memors-mcp", Started: time.Now().Add(-time.Hour)}
	}
	liveOf := func(ctx context.Context) (live.Snapshot, error) {
		return client.New().Live(ctx, client.TargetOf(e.Info))
	}

	wrongInstance := func(context.Context) (live.Snapshot, error) {
		return live.Snapshot{PID: e.Info.PID, Instance: "someone-else"}, nil
	}
	if err := StopExternal(ctx, e, alive, wrongInstance); err == nil {
		t.Error("stopped although another server answers")
	}
	if err := StopExternal(ctx, e, func(int) discover.Process { return discover.Process{Alive: true, Name: "Safari"} }, liveOf); err == nil {
		t.Error("stopped a process that is not memors-mcp")
	}
	unreachable := func(context.Context) (live.Snapshot, error) { return live.Snapshot{}, client.ErrUnreachable }
	if err := StopExternal(ctx, e, func(int) discover.Process { return discover.Process{Alive: true} }, unreachable); err == nil {
		t.Error("stopped an unconfirmed process")
	}
	if err := StopExternal(ctx, e, alive, liveOf); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the server to exit", func() bool { return childOf(t, s, "ext").State == Stopped })
}
