//go:build darwin

// Command memors-tray is a macOS menu-bar companion for memors-mcp's HTTP
// servers (`memors-mcp serve --http`): it shows the agents connected to each
// server, opens a server's Live page in a window of its own, and starts and
// stops servers per knowledge base.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"fyne.io/systray"

	"github.com/kKEo/memors/internal/live"
	"github.com/kKEo/memors/internal/runfile"
	"github.com/kKEo/memors/tray/internal/app"
	"github.com/kKEo/memors/tray/internal/client"
	"github.com/kKEo/memors/tray/internal/config"
	"github.com/kKEo/memors/tray/internal/discover"
	"github.com/kKEo/memors/tray/internal/home"
	"github.com/kKEo/memors/tray/internal/icon"
	"github.com/kKEo/memors/tray/internal/installer"
	"github.com/kKEo/memors/tray/internal/locate"
	"github.com/kKEo/memors/tray/internal/loginitem"
	"github.com/kKEo/memors/tray/internal/macwin"
	"github.com/kKEo/memors/tray/internal/menu"
	"github.com/kKEo/memors/tray/internal/setup"
	"github.com/kKEo/memors/tray/internal/supervisor"
	"github.com/kKEo/memors/tray/internal/trayui"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = ""

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-version", "--version", "version":
			fmt.Println("memors-tray", buildVersion())
			return
		default:
			fmt.Fprintln(os.Stderr, "usage: memors-tray [-version]\n\nmemors-tray runs in the menu bar; open memors-tray.app, or run it without arguments.")
			os.Exit(2)
		}
	}
	os.Exit(run())
}

func run() int {
	homeDir, err := home.Dir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "memors-tray:", err)
		return 1
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "memors-tray:", err)
		return 1
	}
	unlock, err := lockSingle(filepath.Join(homeDir, "tray.lock"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "memors-tray:", err)
		return 1
	}
	defer unlock()
	logf := openLog(filepath.Join(home.LogDir(homeDir), "tray.log"))
	logf("memors-tray %s starting (MEMORS_HOME %s)", buildVersion(), homeDir)

	debug := false
	if cfg, err := config.Load(config.Path(homeDir)); err == nil {
		debug = cfg.Debug
	}
	sup := supervisor.New(homeDir, os.Environ())
	cl := client.New()
	var a *app.App
	ui := trayui.New(func(act menu.Action) { a.Do(act) })
	login := loginAgent()
	if err := login.AdoptLegacy(); err != nil {
		logf("login item: %v", err)
	}
	if err := login.Repair(); err != nil {
		logf("login item: %v", err)
	}
	pages, err := setup.Start(setup.Deps{
		Home:          homeDir,
		TrayVersion:   buildVersion(),
		GOARCH:        runtime.GOARCH,
		Releases:      installer.New("memors-tray/" + buildVersion()),
		FindMemors:    locate.MemorsBinary,
		MemorsVersion: locate.Version,
		Models:        setup.ListModels(homeDir),
		Login:         login,
		PortFree:      config.PortFree,
		Copy:          copyText,
		OpenFile:      openFile,
		Notify: func(e setup.Event) {
			switch e.Kind {
			case setup.ConfigSaved:
				a.ConfigChanged()
			case setup.StartServer:
				a.StartIfStopped(e.KB)
			case setup.RestartServers:
				a.RestartManaged()
			}
		},
		Logf: logf,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "memors-tray:", err)
		return 1
	}
	defer pages.Close()
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
		Gone:          func(pid int) bool { return !discover.Inspect(pid).Alive },
		Supervisor:    sup,
		FindMemors:    locate.MemorsBinary,
		MemorsVersion: locate.Version,
		PortFree:      config.PortFree,
		Render:        ui.Render,
		Windows:       macwin.Init(debug), // its setup runs once the run loop starts
		OpenURL:       openURL,
		OpenFile:      openFile,
		Copy:          copyText,
		Quit:          systray.Quit,
		Logf:          logf,
		Setup:         pages,
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
		ui.Ready(icon.Template(), "memors-tray: memors-mcp servers and their agents")
		go func() {
			for range systray.TrayOpenedCh {
				a.MenuOpened()
			}
		}()
		go a.Run(ctx)
	}, cleanup)
	cleanup()
	logf("memors-tray stopped")
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

// loginAgent is the LaunchAgent that opens this memors-tray at login.
func loginAgent() loginitem.Agent {
	userHome, _ := os.UserHomeDir()
	exe, err := os.Executable()
	if err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
	}
	return loginitem.Agent{Home: userHome, Executable: exe}
}
