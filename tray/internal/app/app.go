// Package app is memors-tray's controller. One goroutine owns all state: it
// rescans and polls the servers every couple of seconds (and whenever the
// menu opens), rebuilds the menu model, and carries out what the user
// picks. Everything it touches outside itself (the menu bar, windows,
// processes, the clipboard) is a dependency, so it is tested with fakes.
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
	"time"

	"github.com/kKEo/memors/internal/live"
	"github.com/kKEo/memors/internal/runfile"
	"github.com/kKEo/memors/tray/internal/client"
	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/home"
	"github.com/kKEo/memors/tray/internal/menu"
	"github.com/kKEo/memors/tray/internal/supervisor"
)

// Supervisor is the part of *supervisor.Supervisor the controller uses.
type Supervisor interface {
	Start(bin string, spec supervisor.Spec) error
	Stop(kb string, grace time.Duration)
	StopAll(grace time.Duration)
	Snapshot() []supervisor.Child
	Running() int
	LogPath(kb string) string
}

// Windows shows server pages in windows of memors-tray's own.
type Windows interface {
	// Open shows url in the window for key (a server's base URL), creating
	// it or bringing it to the front. origin is where it may navigate.
	Open(key, title, url, origin string)
	// ShowMessage replaces a window's content with a note (the server went away).
	ShowMessage(key, text string)
	// Has reports whether a window for key is open.
	Has(key string) bool
}

// Deps is everything the controller reaches outside itself.
type Deps struct {
	Home          string
	Now           func() time.Time
	Scan          func(runDir string) ([]runfile.Entry, error)
	Live          func(ctx context.Context, t client.Target) (live.Snapshot, error)
	StatsURL      func(ctx context.Context, t client.Target, path string) (string, error)
	StopExternal  func(ctx context.Context, e runfile.Entry) error
	Gone          func(pid int) bool // the process has exited
	Supervisor    Supervisor
	FindMemors    func(configured string) (string, error)
	MemorsVersion func(ctx context.Context, bin string) (string, error)
	PortFree      func(addr string) bool
	Render        func(items []menu.Item, title string)
	Windows       Windows
	OpenURL       func(url string) error              // default browser
	OpenFile      func(path string, how string) error // how: "log", "text", "folder"
	Copy          func(text string) error
	Quit          func() // ends the menu-bar loop
	Logf          func(format string, args ...any)
	Setup         Pages // memors-tray's own pages: settings and the setup assistant
}

// Pages are memors-tray's own pages (*setup.Server).
type Pages interface {
	Entry(page string) string // the link a window opens to land on page
	Origin() string
}

// Window keys of memors-tray's own pages; servers' windows use their URL.
const (
	settingsWindow = "tray:settings"
	setupWindow    = "tray:setup"
)

const (
	tickEvery   = 2 * time.Second
	failsBefore = 3 // failed polls before a server shows as unreachable
	stopGrace   = 10 * time.Second
	noteFor     = 15 * time.Second // how long a transient problem stays in the menu
	runFileWait = 15 * time.Second // a started server without a run file after this is suspicious
)

// App is the controller.
type App struct {
	d       Deps
	actions chan menu.Action
	opened  chan struct{}
	kick    chan struct{}

	// Owned by the Run goroutine.
	cfg        *config.Config
	cfgErr     error
	cfgMod     time.Time
	memorsBin  string
	memorsVer  string
	memorsErr  error
	memorsAt   time.Time
	polls      map[string]*poll // by base URL
	views      map[string]view  // last menu rows, by key
	instances  map[string]string
	note       string
	noteUntil  time.Time
	autostart  bool
	firstRun   bool // offer the setup assistant once, if memors-mcp is missing
	quitting   bool
	busyMu     sync.Mutex
	busy       map[string]bool // knowledge bases with a start/stop in progress
	shutdownMu sync.Once
}

type poll struct {
	snap  *live.Snapshot
	fails int
	err   error
}

// view is a menu row plus what actions need.
type view struct {
	menu.ServerView
	entry *runfile.Entry
}

// New returns a controller; call Run on its own goroutine.
func New(d Deps) *App {
	return &App{d: d, actions: make(chan menu.Action, 16), opened: make(chan struct{}, 1), kick: make(chan struct{}, 1),
		polls: map[string]*poll{}, views: map[string]view{}, instances: map[string]string{}, busy: map[string]bool{}, autostart: true, firstRun: true}
}

// Do queues a menu action; it never blocks the caller (the menu thread).
func (a *App) Do(act menu.Action) {
	select {
	case a.actions <- act:
	default:
	}
}

// MenuOpened asks for an immediate refresh.
// ConfigChanged makes the controller reread tray.json and look for
// memors-mcp again now (the settings pages saved them).
func (a *App) ConfigChanged() { a.Do(menu.Action{Kind: reloadAction}) }

// StartIfStopped starts a knowledge base's server unless one runs.
func (a *App) StartIfStopped(kb string) { a.Do(menu.Action{Kind: startIfStopped, KB: kb}) }

// RestartManaged restarts the servers memors-tray started (to run a new
// memors-mcp).
func (a *App) RestartManaged() { a.Do(menu.Action{Kind: restartManaged}) }

func (a *App) MenuOpened() {
	select {
	case a.opened <- struct{}{}:
	default:
	}
}

func (a *App) refreshSoon() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// Run serves until ctx ends or the user quits.
func (a *App) Run(ctx context.Context) {
	t := time.NewTicker(tickEvery)
	defer t.Stop()
	a.tick(ctx)
	for !a.quitting {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.tick(ctx)
		case <-a.opened:
			a.tick(ctx)
		case <-a.kick:
			a.tick(ctx)
		case act := <-a.actions:
			a.handle(ctx, act)
			if !a.quitting {
				a.tick(ctx)
			}
		}
	}
}

// Shutdown stops the servers memors-tray started, when tray.json says so.
// It is safe to call more than once and from any goroutine, and it never
// touches the menu bar.
func (a *App) Shutdown() {
	a.shutdownMu.Do(func() {
		cfg, err := config.Load(config.Path(a.d.Home))
		if err != nil || cfg.StopsOnQuit() {
			a.d.Supervisor.StopAll(stopGrace)
		}
	})
}

func (a *App) tick(ctx context.Context) {
	now := a.d.Now()
	a.loadConfig()
	a.findMemors(ctx, now)
	if a.firstRun {
		a.firstRun = false
		if _, err := os.Stat(config.Path(a.d.Home)); a.memorsErr != nil && errors.Is(err, os.ErrNotExist) && a.d.Setup != nil {
			a.openPage(setupWindow, "Set up memors-mcp", "/wizard") // first launch, nothing installed yet
		}
	}
	entries, scanErr := a.d.Scan(runfile.Dir(a.d.Home))
	a.pollAll(ctx, entries)
	views, problem := a.merge(now, entries)
	if scanErr != nil {
		problem = scanErr.Error()
	}
	if a.autostart {
		a.autostart = false
		a.startAutostart(views)
	}
	a.views = map[string]view{}
	var rows []menu.ServerView
	for _, v := range views {
		a.views[v.Key] = v
		rows = append(rows, v.ServerView)
	}
	a.followRestarts(ctx, views)
	kbs, _ := home.KBs(a.d.Home)
	for _, s := range a.cfg.Servers {
		if !slices.Contains(kbs, s.KB) {
			kbs = append(kbs, s.KB)
		}
	}
	slices.Sort(kbs)
	if a.note != "" && now.Before(a.noteUntil) {
		problem = a.note
	}
	stops := 0
	if a.cfg.StopsOnQuit() {
		stops = a.d.Supervisor.Running()
	}
	in := menu.Input{Now: now, Servers: rows, KBs: kbs, Memors: a.memorsLine(), MemorsOK: a.memorsErr == nil, Problem: problem,
		StopsOnQuit: stops, DefaultTokenFile: filepath.Join(a.d.Home, "http-token")}
	a.d.Render(menu.Build(in), menu.Title(in))
}

func (a *App) loadConfig() {
	path := config.Path(a.d.Home)
	fi, err := os.Stat(path)
	var mod time.Time
	if err == nil {
		mod = fi.ModTime()
	}
	if a.cfg != nil && mod.Equal(a.cfgMod) {
		return
	}
	a.cfgMod = mod
	cfg, err := config.Load(path)
	a.cfgErr = err
	if err != nil {
		if a.cfg == nil {
			a.cfg = &config.Config{}
		}
		return // keep the last good settings
	}
	a.cfg = cfg
	a.memorsAt = time.Time{} // memors_binary may have changed
}

// findMemors locates memors-mcp at most once a minute.
func (a *App) findMemors(ctx context.Context, now time.Time) {
	if !a.memorsAt.IsZero() && now.Sub(a.memorsAt) < time.Minute {
		return
	}
	a.memorsAt = now
	a.memorsBin, a.memorsErr = a.d.FindMemors(a.cfg.MemorsBinary)
	a.memorsVer = ""
	if a.memorsErr == nil {
		a.memorsVer, _ = a.d.MemorsVersion(ctx, a.memorsBin)
	}
}

func (a *App) memorsLine() string {
	if a.memorsErr != nil {
		return a.memorsErr.Error()
	}
	v := a.memorsVer
	if v == "" {
		v = "(version unknown)"
	}
	return "memors-mcp " + v + " at " + a.memorsBin
}

// pollAll fetches /live.json from every server at once.
func (a *App) pollAll(ctx context.Context, entries []runfile.Entry) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	type result struct {
		url  string
		snap live.Snapshot
		err  error
	}
	var wg sync.WaitGroup
	results := make(chan result, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Err != nil || seen[e.Info.URL] {
			continue
		}
		seen[e.Info.URL] = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, err := a.d.Live(ctx, client.TargetOf(e.Info))
			results <- result{e.Info.URL, snap, err}
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		p := a.polls[r.url]
		if p == nil {
			p = &poll{}
			a.polls[r.url] = p
		}
		if r.err == nil {
			snap := r.snap
			p.snap, p.fails, p.err = &snap, 0, nil
		} else {
			p.fails++
			p.err = r.err
		}
	}
	for url := range a.polls {
		if !seen[url] {
			delete(a.polls, url)
		}
	}
}

// merge joins run files, the servers memors-tray started and tray.json into
// menu rows.
func (a *App) merge(now time.Time, entries []runfile.Entry) ([]view, string) {
	children := a.d.Supervisor.Snapshot()
	childByPID := map[int]supervisor.Child{}
	for _, c := range children {
		childByPID[c.PID] = c
	}
	var views []view
	shown := map[string]bool{} // knowledge bases with a running row
	problem := ""
	if a.cfgErr != nil {
		problem = a.cfgErr.Error()
	}
	for i := range entries {
		e := entries[i]
		if e.Err != nil {
			if problem == "" {
				problem = "unreadable run file: " + e.Err.Error()
			}
			continue
		}
		info := e.Info
		v := view{entry: &entries[i], ServerView: menu.ServerView{Key: info.URL, KB: info.KB, URL: info.URL, Auth: info.Auth,
			State: menu.Running, MTLS: info.MTLS, TokenFile: info.TokenFile, Configured: a.cfg.Find(info.KB) != nil}}
		if c, ok := childByPID[info.PID]; ok && (c.State == supervisor.Running || c.State == supervisor.Stopping) {
			v.Managed = true
			if c.State == supervisor.Stopping {
				v.State = menu.Stopping
			}
		}
		v.HasLog = exists(a.d.Supervisor.LogPath(info.KB))
		if a.isBusy(info.KB) && v.State == menu.Running {
			v.State = menu.Stopping
		}
		if p := a.polls[info.URL]; p != nil {
			v.Snapshot = p.snap
			if p.err != nil {
				v.Detail = describe(p.err)
				if p.fails >= failsBefore || errors.Is(p.err, client.ErrNotSupported) || errors.Is(p.err, client.ErrUnauthorized) || errors.Is(p.err, client.ErrMTLS) {
					if v.State == menu.Running {
						v.State = menu.Unreachable
					}
				}
			}
		}
		views = append(views, v)
		shown[info.KB] = true
	}
	for _, c := range children {
		if shown[c.Spec.KB] {
			continue
		}
		v := view{ServerView: menu.ServerView{Key: "kb:" + c.Spec.KB, KB: c.Spec.KB, Managed: true,
			Configured: a.cfg.Find(c.Spec.KB) != nil, HasLog: exists(c.Log)}}
		switch c.State {
		case supervisor.Running:
			v.State = menu.Starting
			if now.Sub(c.Started) > runFileWait {
				v.Detail = "no run file yet: is memors-mcp older than memors-tray?"
			}
		case supervisor.Stopping:
			v.State = menu.Stopping
		case supervisor.Crashed:
			v.State, v.Detail = menu.Crashed, c.Exit
			if c.LastLog != "" {
				v.Detail += ": " + shorten(c.LastLog, 60)
			}
		default:
			v.State = menu.Stopped
		}
		views = append(views, v)
		shown[c.Spec.KB] = true
	}
	for _, s := range a.cfg.Servers {
		if !shown[s.KB] {
			views = append(views, view{ServerView: menu.ServerView{Key: "kb:" + s.KB, KB: s.KB, State: menu.Stopped,
				Configured: true, HasLog: exists(a.d.Supervisor.LogPath(s.KB))}})
		}
	}
	return views, problem
}

// followRestarts re-logs-in open windows of servers that came back as a
// new process (restart), and tells windows of servers that went away.
func (a *App) followRestarts(ctx context.Context, views []view) {
	current := map[string]bool{}
	for _, v := range views {
		if v.URL == "" || v.Snapshot == nil {
			continue
		}
		current[v.URL] = true
		prev, known := a.instances[v.URL]
		a.instances[v.URL] = v.Snapshot.Instance
		if known && prev != v.Snapshot.Instance && a.d.Windows.Has(v.URL) {
			a.openStats(ctx, v)
		}
	}
	for url := range a.instances {
		if !current[url] {
			delete(a.instances, url)
			if a.d.Windows.Has(url) {
				a.d.Windows.ShowMessage(url, "This memors-mcp server has stopped. The window updates when it is back.")
			}
		}
	}
}

func (a *App) startAutostart(views []view) {
	running := map[string]bool{}
	for _, v := range views {
		if v.State != menu.Stopped && v.State != menu.Crashed {
			running[v.KB] = true
		}
	}
	for _, s := range a.cfg.Servers {
		if s.Autostart && !running[s.KB] {
			a.start(s.KB)
		}
	}
}

func (a *App) handle(ctx context.Context, act menu.Action) {
	if a.cfg == nil { // before the first tick
		a.loadConfig()
		a.findMemors(ctx, a.d.Now())
	}
	v, hasView := a.views[act.Server]
	switch act.Kind {
	case menu.OpenStats:
		if hasView {
			a.openStats(ctx, v)
		}
	case menu.OpenBrowser:
		if hasView && v.entry != nil {
			u, err := a.d.StatsURL(ctx, client.TargetOf(v.entry.Info), "/live")
			if errors.Is(err, client.ErrNotSupported) {
				u, err = v.URL+"/login?next=/live", nil
			}
			if err == nil {
				err = a.d.OpenURL(u)
			}
			a.report("open in browser", err)
		}
	case menu.CopyURL:
		if hasView && v.URL != "" {
			a.report("copy", a.d.Copy(v.URL+"/mcp"))
		}
	case menu.CopyAdd:
		if hasView && v.URL != "" {
			a.report("copy", a.d.Copy(menu.ClaudeAddCommand(v.KB, v.URL, v.Auth, v.TokenFile, filepath.Join(a.d.Home, "http-token"))))
		}
	case menu.ShowLog:
		a.report("show log", a.d.OpenFile(a.d.Supervisor.LogPath(act.KB), "log"))
	case menu.Start:
		a.start(act.KB)
	case menu.Stop:
		if hasView {
			a.stop(ctx, v, false)
		}
	case menu.Restart:
		if hasView {
			a.stop(ctx, v, true)
		}
	case menu.EditConfig:
		a.editConfig()
	case menu.OpenLogs:
		dir := home.LogDir(a.d.Home)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			a.report("open logs", err)
			return
		}
		a.report("open logs", a.d.OpenFile(dir, "folder"))
	case menu.Quit:
		a.quitting = true
		a.d.Quit()
	case menu.OpenSettings:
		a.openPage(settingsWindow, "memors-tray Settings", "/settings")
	case menu.OpenSetup:
		a.openPage(setupWindow, "Set up memors-mcp", "/wizard")
	case reloadAction:
		a.cfgMod, a.memorsAt = time.Time{}, time.Time{}
	case startIfStopped:
		a.cfgMod, a.memorsAt = time.Time{}, time.Time{}
		a.loadConfig()
		a.findMemors(ctx, a.d.Now())
		for _, v := range a.views {
			if v.KB == act.KB && v.State != menu.Stopped && v.State != menu.Crashed {
				return
			}
		}
		a.start(act.KB)
	case restartManaged:
		a.memorsAt = time.Time{}
		for _, v := range a.views {
			if v.Managed && v.State != menu.Stopped && v.State != menu.Crashed {
				a.stop(ctx, v, true)
			}
		}
	case noteAction:
		a.note, a.noteUntil = act.KB, a.d.Now().Add(noteFor)
	}
}

// openStats shows the server's Live page in a window, logged in.
func (a *App) openStats(ctx context.Context, v view) {
	if v.entry == nil {
		return
	}
	u, err := a.d.StatsURL(ctx, client.TargetOf(v.entry.Info), "/live")
	if errors.Is(err, client.ErrNotSupported) {
		u, err = v.URL+"/login?next=/live", nil // an older server: log in with the token
	}
	if err != nil {
		a.report("open stats window", err)
		return
	}
	a.d.Windows.Open(v.URL, "memors-mcp · "+v.KB, u, v.URL)
}

// start runs a knowledge base's server from tray.json, giving it an
// address first if it has none.
func (a *App) start(kb string) {
	if a.memorsErr != nil {
		a.report("start "+kb, a.memorsErr)
		return
	}
	if !home.ValidName(kb) {
		a.report("start", fmt.Errorf("invalid knowledge-base name %q", kb))
		return
	}
	var addr string
	added := false
	cfg, err := config.Update(config.Path(a.d.Home), func(c *config.Config) error {
		var err error
		addr, added, err = c.AddrFor(kb, a.d.PortFree)
		return err
	})
	if err != nil {
		a.report("start "+kb, err)
		return
	}
	a.cfg = cfg
	if fi, err := os.Stat(config.Path(a.d.Home)); err == nil {
		a.cfgMod = fi.ModTime()
	}
	if !added && !a.d.PortFree(addr) {
		a.report("start "+kb, fmt.Errorf("%s is in use by another program", addr))
		return
	}
	s := a.cfg.Find(kb)
	env := map[string]string{}
	for k, v := range a.cfg.Env {
		env[k] = v
	}
	for k, v := range s.Env {
		env[k] = v
	}
	if err := a.d.Supervisor.Start(a.memorsBin, supervisor.Spec{KB: kb, Addr: addr, Auth: s.Auth, Env: env}); err != nil {
		a.report("start "+kb, err)
		return
	}
	a.d.Logf("started %s on %s", kb, addr)
}

// stop stops a server (and starts it again for a restart) off the
// controller goroutine, so the menu keeps updating meanwhile.
func (a *App) stop(ctx context.Context, v view, restart bool) {
	if !a.setBusy(v.KB, true) {
		return
	}
	entry := v.entry
	go func() {
		defer a.refreshSoon()
		defer a.setBusy(v.KB, false)
		if v.Managed {
			a.d.Supervisor.Stop(v.KB, stopGrace)
		} else if entry != nil {
			if err := a.d.StopExternal(ctx, *entry); err != nil {
				a.queueNote("stop "+v.KB, err)
				return
			}
			deadline := time.Now().Add(stopGrace)
			for !a.d.Gone(entry.Info.PID) && time.Now().Before(deadline) {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if restart {
			a.Do(menu.Action{Kind: menu.Start, KB: v.KB})
		}
	}()
}

func (a *App) editConfig() {
	path := config.Path(a.d.Home)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := (&config.Config{}).Save(path); err != nil {
			a.report("create tray.json", err)
			return
		}
	}
	a.report("edit config", a.d.OpenFile(path, "text"))
}

func (a *App) setBusy(kb string, busy bool) bool {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	if busy && a.busy[kb] {
		return false
	}
	if busy {
		a.busy[kb] = true
	} else {
		delete(a.busy, kb)
	}
	return true
}

func (a *App) isBusy(kb string) bool {
	a.busyMu.Lock()
	defer a.busyMu.Unlock()
	return a.busy[kb]
}

// report shows a failed action at the top of the menu for a while.
func (a *App) report(what string, err error) {
	if err == nil {
		return
	}
	a.d.Logf("%s: %v", what, err)
	a.note, a.noteUntil = what+": "+describe(err), a.d.Now().Add(noteFor)
}

// queueNote reports from another goroutine, through the controller.
func (a *App) queueNote(what string, err error) {
	a.d.Logf("%s: %v", what, err)
	a.Do(menu.Action{Kind: noteAction, KB: what + ": " + describe(err)})
}

// noteAction carries a message from a worker goroutine to the controller.
const noteAction menu.ActionKind = -1

// Actions the setup pages ask for (through ConfigChanged and friends).
const (
	reloadAction menu.ActionKind = -2 - iota
	startIfStopped
	restartManaged
)

// openPage shows one of memors-tray's own pages in its window.
func (a *App) openPage(key, title, page string) {
	if a.d.Setup == nil {
		return
	}
	a.d.Windows.Open(key, title, a.d.Setup.Entry(page), a.d.Setup.Origin())
}

func describe(err error) string {
	for _, known := range []error{client.ErrUnauthorized, client.ErrNotSupported, client.ErrMTLS, client.ErrInsecure} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if errors.Is(err, client.ErrUnreachable) {
		return "not answering"
	}
	return shorten(err.Error(), 80)
}

func shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
