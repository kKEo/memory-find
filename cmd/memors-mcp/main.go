package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/kKEo/memors/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=$(git describe --tags)".
// Without ldflags it falls back to the module version Go recorded in the
// binary, or "dev".
var version = ""

func main() {
	if version == "" {
		version = "dev"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	cli.SetVersion(version)

	// SIGINT/SIGTERM cancel the root context so the database and the model
	// session are closed by their deferred cleanups instead of being killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(cli.Main(ctx, version, os.Args[1:], os.Stdout, os.Stderr))
}
