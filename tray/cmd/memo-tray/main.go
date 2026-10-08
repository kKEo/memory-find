//go:build darwin

// Command memo-tray is a macOS menu-bar companion for memo-mcp's HTTP
// servers (`memo-mcp serve --http`): it shows the agents connected to each
// server, opens a server's Live page in a window of its own, and starts and
// stops servers per knowledge base.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"fyne.io/systray"

	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/runfile"
	"github.com/kKEo/memory-find/tray/internal/app"
	"github.com/kKEo/memory-find/tray/internal/client"
	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/discover"
	"github.com/kKEo/memory-find/tray/internal/home"
	"github.com/kKEo/memory-find/tray/internal/icon"
	"github.com/kKEo/memory-find/tray/internal/locate"
	"github.com/kKEo/memory-find/tray/internal/macwin"
	"github.com/kKEo/memory-find/tray/internal/menu"
	"github.com/kKEo/memory-find/tray/internal/supervisor"
	"github.com/kKEo/memory-find/tray/internal/trayui"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = ""

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version", "version":
			fmt.Println("memo-tray", buildVersion())
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: memo-tray [-version]\n\nmemo-tray runs in the menu bar; open memo-tray.app, or run it without arguments.")
			os.Exit(2)
		}
	}
	os.Exit(run())
}

func run() int {
	homeDir, err := home.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memo-tray:", err)
		return 1
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "memo-tray:", err)
		return 1
	}
	unlock, err := lockSingle(filepath.Join(homeDir, "tray.lock"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "memo-tray:", err)
		return 1
	}
	defer unlock()
	logf := openLog(filepath.Join(home.LogDir(homeDir), "tray.log"))
	logf("memo-tray %s starting (MEMO_HOME %s)", buildVersion(), homeDir)

	debug := false
	if cfg, err := config.Load(config.Path(homeDir)); err == nil {
		debug = cfg.Debug
	}
	sup := supervisor.New(homeDir, os.Environ())
	cl := client.New()
	var a *app.App
	ui := trayui.New(func(act menu.Action) { a.Do(act) })
	a = app.New(app.Deps{
		Home:     homeDir,
		Now:      time.Now,
		Scan:     func(dir string) ([]runfile.Entry, error) { return discover.Scan(dir, discover.Inspect) },
		Live:     cl.Live,
		StatsURL: cl.StatsURL,
		StopExternal: func(ctx context.Context, e runfile.Entry) error {
			return supervisor.StopExternal(ctx, e, discover.Inspect, func(ctx context.Context) (live.Snapshot, error) {
				return cl.Live(ctx, client.TargetOf(e.Info))
			})
		},
		Gone:        func(pid int) bool { return !discover.Inspect(pid).Alive },
		Supervisor:  sup,
		FindMemo:    locate.MemoBinary,
		MemoVersion: locate.Version,
		PortFree:    config.PortFree,
		Render:      ui.Render,
		Windows:     macwin.Init(debug), // its setup runs once the run loop starts
		OpenURL:     openURL,
		OpenFile:    openFile,
		Copy:        copyText,
		Quit:        systray.Quit,
		Logf:        logf,
	})

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		a.Do(menu.Action{Kind: menu.Quit})
	}()
	// On darwin systray calls onExit only when the app is terminated
	// (logout, shutdown); after Quit, Run just returns. Clean up either way.
	cleanup := func() {
		cancel()
		a.Shutdown()
	}
	systray.Run(func() {
		ui.Ready(icon.Template(), "memo-tray: memo-mcp servers and their agents")
		go func() {
			for range systray.TrayOpenedCh {
				a.MenuOpened()
			}
		}()
		go a.Run(ctx)
	}, cleanup)
	cleanup()
	logf("memo-tray stopped")
	return 0
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
