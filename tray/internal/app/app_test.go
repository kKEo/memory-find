package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/runfile"
	"github.com/kKEo/memory-find/tray/internal/client"
	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/menu"
	"github.com/kKEo/memory-find/tray/internal/supervisor"
)

type fakeSup struct {
	mu       sync.Mutex
	children map[string]supervisor.Child
	starts   []supervisor.Spec
	stops    []string
	stopAll  int
	home     string
}

func (f *fakeSup) Start(bin string, spec supervisor.Spec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, spec)
	f.children[spec.KB] = supervisor.Child{Spec: spec, PID: 1000 + len(f.starts), State: supervisor.Running, Started: time.Now()}
	return nil
}

func (f *fakeSup) Stop(kb string, _ time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, kb)
	c := f.children[kb]
	c.State = supervisor.Stopped
	f.children[kb] = c
}

func (f *fakeSup) StopAll(time.Duration) { f.mu.Lock(); f.stopAll++; f.mu.Unlock() }

func (f *fakeSup) Snapshot() []supervisor.Child {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []supervisor.Child
	for _, c := range f.children {
		out = append(out, c)
	}
	return out
}

func (f *fakeSup) Running() int {
	n := 0
	for _, c := range f.Snapshot() {
		if c.State == supervisor.Running {
			n++
		}
	}
	return n
}

func (f *fakeSup) LogPath(kb string) string { return filepath.Join(f.home, "logs", "serve-"+kb+".log") }

type fakeWindows struct {
	opens    []string // key|url|origin
	messages []string
	open     map[string]bool
}

func (w *fakeWindows) Open(key, title, url, origin string) {
	w.opens = append(w.opens, key+"|"+url+"|"+origin)
	w.open[key] = true
}
func (w *fakeWindows) ShowMessage(key, text string) { w.messages = append(w.messages, key) }
func (w *fakeWindows) Has(key string) bool          { return w.open[key] }

type harness struct {
	t        *testing.T
	a        *App
	sup      *fakeSup
	win      *fakeWindows
	entries  []runfile.Entry
	snaps    map[string]live.Snapshot
	liveErr  map[string]error
	statsErr map[string]error
	items    []menu.Item
	title    string
	copied   []string
	opened   []string
	stoppedX []int
	now      time.Time
	quit     bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, snaps: map[string]live.Snapshot{}, liveErr: map[string]error{}, statsErr: map[string]error{}, now: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	home := t.TempDir()
	h.sup = &fakeSup{children: map[string]supervisor.Child{}, home: home}
	h.win = &fakeWindows{open: map[string]bool{}}
	h.a = New(Deps{
		Home: home,
		Now:  func() time.Time { return h.now },
		Scan: func(string) ([]runfile.Entry, error) { return h.entries, nil },
		Live: func(_ context.Context, t client.Target) (live.Snapshot, error) {
			if err := h.liveErr[t.URL]; err != nil {
				return live.Snapshot{}, err
			}
			return h.snaps[t.URL], nil
		},
		StatsURL: func(_ context.Context, t client.Target, path string) (string, error) {
			if err := h.statsErr[t.URL]; err != nil {
				return "", err
			}
			if t.Auth == "token" {
				return t.URL + "/login?code=c&next=" + path, nil
			}
			return t.URL + path, nil
		},
		StopExternal: func(_ context.Context, e runfile.Entry) error {
			h.stoppedX = append(h.stoppedX, e.Info.PID)
			return nil
		},
		Gone:        func(int) bool { return true },
		Supervisor:  h.sup,
		FindMemo:    func(string) (string, error) { return "/bin/memo-mcp", nil },
		MemoVersion: func(context.Context, string) (string, error) { return "v9", nil },
		PortFree:    func(string) bool { return true },
		Render:      func(items []menu.Item, title string) { h.items, h.title = items, title },
		Windows:     h.win,
		OpenURL:     func(u string) error { h.opened = append(h.opened, u); return nil },
		OpenFile:    func(string, string) error { return nil },
		Copy:        func(s string) error { h.copied = append(h.copied, s); return nil },
		Quit:        func() { h.quit = true },
		Logf:        func(string, ...any) {},
	})
	return h
}

func (h *harness) server(pid int, kb, url, auth string) {
	h.entries = append(h.entries, runfile.Entry{Path: fmt.Sprintf("/run/serve-%d.json", pid), ModTime: h.now,
		Info: runfile.Info{Schema: 1, PID: pid, Instance: "i1", KB: kb, URL: url, Auth: auth, TokenFile: filepath.Join(h.a.d.Home, "http-token")}})
}

func (h *harness) tick() { h.t.Helper(); h.a.tick(context.Background()) }

func (h *harness) menuText() string {
	var b strings.Builder
	var walk func([]menu.Item, string)
	walk = func(items []menu.Item, ind string) {
		for _, it := range items {
			if !it.Separator {
				b.WriteString(ind + it.Title + "\n")
				walk(it.Children, ind+"  ")
			}
		}
	}
	walk(h.items, "")
	return b.String()
}

func TestAgentsAppearAndLeave(t *testing.T) {
	h := newHarness(t)
	h.server(41, "crportal", "http://127.0.0.1:8765", "none")
	h.snaps["http://127.0.0.1:8765"] = live.Snapshot{Schema: 1, Instance: "i1", Sessions: []live.Session{{Key: "k", Client: "claude-code", ClientVersion: "2.1", Open: true, Stream: true, LastSeen: h.now}}}
	h.tick()
	if h.title != "1" || !strings.Contains(h.menuText(), "● claude-code 2.1") || !strings.Contains(h.menuText(), "crportal · 127.0.0.1:8765 · running") {
		t.Fatalf("title %q, menu:\n%s", h.title, h.menuText())
	}
	h.snaps["http://127.0.0.1:8765"] = live.Snapshot{Schema: 1, Instance: "i1"}
	h.tick()
	if h.title != "" || !strings.Contains(h.menuText(), "no agents connected") {
		t.Errorf("after the agent left: title %q, menu:\n%s", h.title, h.menuText())
	}
}

func TestUnreachableAfterRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	h.server(41, "crportal", "http://127.0.0.1:8765", "none")
	h.liveErr["http://127.0.0.1:8765"] = fmt.Errorf("%w: refused", client.ErrUnreachable)
	for i := range failsBefore {
		h.tick()
		unreachable := strings.Contains(h.menuText(), "unreachable (not answering)")
		if want := i == failsBefore-1; unreachable != want {
			t.Fatalf("after %d failures unreachable=%v:\n%s", i+1, unreachable, h.menuText())
		}
	}
	h.liveErr["http://127.0.0.1:8765"] = client.ErrNotSupported
	h.a.polls = map[string]*poll{}
	h.tick()
	if !strings.Contains(h.menuText(), "too old for memo-tray") {
		t.Errorf("old server not flagged at once:\n%s", h.menuText())
	}
}

func TestActions(t *testing.T) {
	h := newHarness(t)
	h.server(41, "crportal", "http://127.0.0.1:8765", "token")
	h.snaps["http://127.0.0.1:8765"] = live.Snapshot{Schema: 1, Instance: "i1"}
	h.tick()
	key := "http://127.0.0.1:8765"
	ctx := context.Background()
	h.a.handle(ctx, menu.Action{Kind: menu.CopyURL, Server: key})
	h.a.handle(ctx, menu.Action{Kind: menu.CopyAdd, Server: key, KB: "crportal"})
	if len(h.copied) != 2 || h.copied[0] != key+"/mcp" || !strings.Contains(h.copied[1], `memo-crportal 'http://127.0.0.1:8765/mcp' --header "Authorization: Bearer $(memo-mcp http-token)"`) {
		t.Errorf("copied = %q", h.copied)
	}
	h.a.handle(ctx, menu.Action{Kind: menu.OpenStats, Server: key})
	if len(h.win.opens) != 1 || h.win.opens[0] != key+"|"+key+"/login?code=c&next=/live|"+key {
		t.Errorf("window opens = %q", h.win.opens)
	}
	h.a.handle(ctx, menu.Action{Kind: menu.OpenBrowser, Server: key})
	if len(h.opened) != 1 || !strings.HasPrefix(h.opened[0], key+"/login?code=") {
		t.Errorf("browser opens = %q", h.opened)
	}

	// A server that comes back as a new process gets its window logged in again.
	h.snaps[key] = live.Snapshot{Schema: 1, Instance: "i2"}
	h.tick()
	if len(h.win.opens) != 2 {
		t.Errorf("window not re-opened after a restart: %q", h.win.opens)
	}
	// One that goes away gets a note in its window.
	h.entries = nil
	h.tick()
	if len(h.win.messages) != 1 {
		t.Errorf("window not told the server stopped: %q", h.win.messages)
	}
	h.a.handle(ctx, menu.Action{Kind: menu.Quit})
	if !h.quit || !h.a.quitting {
		t.Error("quit did not end the menu bar")
	}
}

func TestStartAssignsAndSavesAnAddress(t *testing.T) {
	h := newHarness(t)
	h.tick()
	h.a.handle(context.Background(), menu.Action{Kind: menu.Start, KB: "crportal"})
	if len(h.sup.starts) != 1 || h.sup.starts[0].Addr != "127.0.0.1:8765" || h.sup.starts[0].KB != "crportal" {
		t.Fatalf("starts = %+v", h.sup.starts)
	}
	cfg, err := config.Load(config.Path(h.a.d.Home))
	if err != nil || cfg.Find("crportal") == nil || cfg.Find("crportal").Addr != "127.0.0.1:8765" {
		t.Errorf("tray.json = %+v, %v", cfg, err)
	}
	h.tick()
	if !strings.Contains(h.menuText(), "crportal · starting") || !strings.Contains(h.menuText(), "Quit memo-tray (stops 1 server)") {
		t.Errorf("menu:\n%s", h.menuText())
	}
	// Its run file appears: the same row, now running and managed.
	h.server(1001, "crportal", "http://127.0.0.1:8765", "none")
	h.snaps["http://127.0.0.1:8765"] = live.Snapshot{Schema: 1, Instance: "i1"}
	h.tick()
	if v := h.a.views["http://127.0.0.1:8765"]; !v.Managed || v.State != menu.Running {
		t.Errorf("view = %+v", v.ServerView)
	}
}

func TestStopAndRestart(t *testing.T) {
	h := newHarness(t)
	h.server(41, "ext", "http://127.0.0.1:8770", "none") // started elsewhere
	h.snaps["http://127.0.0.1:8770"] = live.Snapshot{Schema: 1, Instance: "i1"}
	h.tick()
	h.a.handle(context.Background(), menu.Action{Kind: menu.Stop, Server: "http://127.0.0.1:8770"})
	waitFor(t, func() bool { return !h.a.isBusy("ext") })
	if !slices.Equal(h.stoppedX, []int{41}) {
		t.Errorf("external stops = %v", h.stoppedX)
	}

	h.entries = nil
	h.a.handle(context.Background(), menu.Action{Kind: menu.Start, KB: "mine"})
	h.server(1001, "mine", "http://127.0.0.1:8765", "none")
	h.snaps["http://127.0.0.1:8765"] = live.Snapshot{Schema: 1, Instance: "i1"}
	h.tick()
	h.a.handle(context.Background(), menu.Action{Kind: menu.Restart, Server: "http://127.0.0.1:8765"})
	waitFor(t, func() bool { return !h.a.isBusy("mine") })
	select {
	case act := <-h.a.actions:
		if act.Kind != menu.Start || act.KB != "mine" {
			t.Errorf("restart queued %+v", act)
		}
	case <-time.After(time.Second):
		t.Fatal("restart did not queue a start")
	}
	if !slices.Equal(h.sup.stops, []string{"mine"}) {
		t.Errorf("managed stops = %v", h.sup.stops)
	}
}

func TestAutostart(t *testing.T) {
	h := newHarness(t)
	cfg := &config.Config{Servers: []config.Server{{KB: "a", Addr: "127.0.0.1:8765", Autostart: true}, {KB: "b", Addr: "127.0.0.1:8766", Autostart: true}, {KB: "c", Addr: "127.0.0.1:8767"}}}
	if err := cfg.Save(config.Path(h.a.d.Home)); err != nil {
		t.Fatal(err)
	}
	h.server(41, "b", "http://127.0.0.1:8766", "none") // already running
	h.tick()
	h.tick()
	if len(h.sup.starts) != 1 || h.sup.starts[0].KB != "a" {
		t.Errorf("autostarted %+v", h.sup.starts)
	}
	if !strings.Contains(h.menuText(), "c · stopped") {
		t.Errorf("configured server not listed:\n%s", h.menuText())
	}
}

func TestShutdown(t *testing.T) {
	h := newHarness(t)
	h.a.Shutdown()
	h.a.Shutdown()
	if h.sup.stopAll != 1 {
		t.Errorf("StopAll called %d times", h.sup.stopAll)
	}
	h2 := newHarness(t)
	no := false
	if err := (&config.Config{StopOnQuit: &no}).Save(config.Path(h2.a.d.Home)); err != nil {
		t.Fatal(err)
	}
	h2.a.Shutdown()
	if h2.sup.stopAll != 0 {
		t.Error("stop_on_quit false still stopped servers")
	}
}

func TestBadConfigIsShown(t *testing.T) {
	h := newHarness(t)
	if err := os.WriteFile(config.Path(h.a.d.Home), []byte(`{"servers":[{"kb":"x","addr":"8.8.8.8:1"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.tick()
	if !strings.Contains(h.menuText(), "⚠ tray.json") {
		t.Errorf("config error not shown:\n%s", h.menuText())
	}
	h.a.report("start x", errors.New("boom"))
	h.tick()
	if !strings.Contains(h.menuText(), "⚠ start x: boom") {
		t.Errorf("action error not shown:\n%s", h.menuText())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A server too old for login links still opens: on its login page.
func TestOpenStatsOnAnOlderServer(t *testing.T) {
	h := newHarness(t)
	key := "http://127.0.0.1:8765"
	h.server(41, "old", key, "token")
	h.snaps[key] = live.Snapshot{Schema: 1, Instance: "i1"}
	h.statsErr[key] = client.ErrNotSupported
	h.tick()
	h.a.handle(context.Background(), menu.Action{Kind: menu.OpenStats, Server: key})
	if len(h.win.opens) != 1 || h.win.opens[0] != key+"|"+key+"/login?next=/live|"+key {
		t.Errorf("window opens = %q", h.win.opens)
	}
	h.statsErr[key] = client.ErrUnauthorized
	h.a.handle(context.Background(), menu.Action{Kind: menu.OpenStats, Server: key})
	h.tick()
	if len(h.win.opens) != 1 || !strings.Contains(h.menuText(), "⚠ open stats window: token refused") {
		t.Errorf("refused token not reported:\n%s", h.menuText())
	}
}
