// Package cli is the command-line front of memo-mcp: subcommand dispatch,
// configuration from the environment, and the small commands that do not
// need an MCP client (version, status, model management). It uses only the
// standard library's flag package (roadmap OD-14).
package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	// The CLI is what opens databases, so it registers the SQLite driver
	// (with the sqlite-vec extension) itself.
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/journal"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/search"
	"github.com/kKEo/memory-find/internal/server"
)

// Config is everything the commands need from the environment.
type Config struct {
	// DBName is the knowledge-base name; it selects the database file.
	// Comes from MEMO_KB (default "default"), or JOURNAL_TOKEN (deprecated).
	DBName string
	// DBDir is the directory holding the LEGACY journal file the MCP server
	// still serves until roadmap P2 (<MEMO_HOME> or <JOURNAL_PATH>).
	DBDir string
	// KBDir is the directory holding knowledge-base files (<MEMO_HOME>/kb).
	KBDir string
	// Deprecations lists warnings about legacy variables that were honoured.
	Deprecations []string
}

// Getenv is the lookup function ResolveConfig uses; tests substitute it.
type Getenv func(string) string

// ResolveConfig maps the environment onto a Config.
//
// MEMO_KB (default "default") names the knowledge base; its file is
// <MEMO_HOME or ~/.memo-mcp>/kb/<name>.db. The legacy JOURNAL_TOKEN /
// JOURNAL_PATH pair keeps its exact old meaning for the journal the MCP
// server still serves (<JOURNAL_PATH or ~/.memo-mcp>/<token>.db), with a
// deprecation warning.
func ResolveConfig(getenv Getenv) (Config, error) {
	var cfg Config
	home := getenv("MEMO_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("resolve home directory: %w", err)
		}
		home = filepath.Join(h, ".memo-mcp")
	}

	cfg.KBDir = filepath.Join(home, "kb")
	if token := getenv("JOURNAL_TOKEN"); token != "" {
		cfg.DBName = token
		cfg.DBDir = getenv("JOURNAL_PATH")
		if cfg.DBDir == "" {
			cfg.DBDir = home
		}
		cfg.Deprecations = append(cfg.Deprecations,
			"JOURNAL_TOKEN is deprecated; set MEMO_KB=<name> instead (JOURNAL_TOKEN keeps working for one release)")
		if getenv("JOURNAL_PATH") != "" {
			cfg.Deprecations = append(cfg.Deprecations,
				"JOURNAL_PATH is deprecated; set MEMO_HOME=<dir> instead")
		}
		return cfg, nil
	}

	cfg.DBName = getenv("MEMO_KB")
	if cfg.DBName == "" {
		cfg.DBName = "default"
	}
	cfg.DBDir = home
	return cfg, nil
}

// Main runs the CLI. version is the build's tag (from -ldflags); args are
// the process arguments without the program name. It returns the exit code.
func Main(ctx context.Context, version string, args []string, stdout, stderr io.Writer) int {
	// Legacy flag form: `memo-mcp --stats`, `memo-mcp --redownload-model`.
	// Kept as aliases for one release.
	fs := flag.NewFlagSet("memo-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	statsFlag := fs.Bool("stats", false, "Deprecated alias of `memo-mcp status`")
	redownload := fs.Bool("redownload-model", false, "Deprecated alias of `memo-mcp model redownload`")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	switch {
	case *statsFlag:
		fmt.Fprintln(stderr, "warning: --stats is deprecated; use `memo-mcp status`")
		rest = []string{"status"}
	case *redownload:
		fmt.Fprintln(stderr, "warning: --redownload-model is deprecated; use `memo-mcp model redownload`")
		rest = []string{"model", "redownload"}
	}

	cmd := "serve"
	if len(rest) > 0 {
		cmd, rest = rest[0], rest[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(ctx, stderr)
	case "version":
		err = runVersion(version, stdout)
	case "status":
		err = runStatus(ctx, stdout, stderr)
	case "ingest":
		err = runIngest(ctx, rest, stdout, stderr)
	case "read":
		err = runRead(ctx, rest, stdout, stderr)
	case "ls":
		err = runLs(ctx, rest, stdout, stderr)
	case "verify":
		err = runVerify(ctx, rest, stdout, stderr)
	case "export":
		err = runExport(ctx, rest, stdout, stderr)
	case "backfill":
		err = runBackfill(ctx, stdout, stderr)
	case "model":
		err = runModel(ctx, rest, stderr)
	case "help", "-h", "--help":
		usage(stdout)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "fatal: %v\n", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `memo-mcp — a local, measurable knowledge base for agents (MCP server + CLI)

Usage:
  memo-mcp [serve]                 start the MCP server on stdio (default)
  memo-mcp ingest <file|dir|->     add documents to the knowledge base
      --ns <name> --kind doc|note|code|conversation --uri <u> --title <t>
      --library <l> --version <v> --trust user|curated --context <text> --embed=false
  memo-mcp read <memo://...>       print a document, chunk or source with its provenance
  memo-mcp ls [--ns --kind --since 2026-01-01 --json]   list live documents, newest first
  memo-mcp export --md <dir> [--ns <name>]              write markdown files with front matter
  memo-mcp verify [--repair]       check integrity (chunks, vectors, indexes)
  memo-mcp backfill                embed chunks whose vectors are pending
  memo-mcp status                  print knowledge-base statistics
  memo-mcp version                 print version, protocol version, Go version, model dir
  memo-mcp model redownload        fetch a fresh copy of the embedding model

Environment:
  MEMO_KB        knowledge-base name (default "default"); file is $MEMO_HOME/kb/<name>.db
  MEMO_HOME      base directory (default ~/.memo-mcp)
  MEMO_QUERY_LOG=1               keep an opt-in log of searches in the same file
  JOURNAL_TOKEN, JOURNAL_PATH    deprecated; select the legacy journal the server still serves
`)
}

func runVersion(version string, w io.Writer) error {
	fmt.Fprintf(w, "memo-mcp %s\n", version)
	fmt.Fprintf(w, "MCP protocol: %s\n", server.ProtocolVersion)
	fmt.Fprintf(w, "Go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(w, "Model dir: %s\n", embedding.DefaultModelDir())
	return nil
}

func runModel(ctx context.Context, args []string, stderr io.Writer) error {
	if len(args) != 1 || args[0] != "redownload" {
		return errors.New("usage: memo-mcp model redownload")
	}
	modelDir := embedding.DefaultModelDir()
	fmt.Fprintf(stderr, "Redownloading embedding model into %s...\n", modelDir)
	if _, err := embedding.RedownloadModel(ctx, modelDir); err != nil {
		return fmt.Errorf("redownload model: %w", err)
	}
	fmt.Fprintln(stderr, "Done.")
	return nil
}

func openDB(stderr io.Writer) (*sql.DB, error) {
	cfg, err := ResolveConfig(os.Getenv)
	if err != nil {
		return nil, err
	}
	for _, d := range cfg.Deprecations {
		fmt.Fprintln(stderr, "warning:", d)
	}
	return journal.OpenDB(cfg.DBName, cfg.DBDir)
}

func runServe(ctx context.Context, stderr io.Writer) error {
	db, err := openDB(stderr)
	if err != nil {
		return err
	}
	defer db.Close()

	embedder, cleanup, err := buildEmbedder(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "warning: embedding unavailable, semantic search will fall back to keyword search: %v\n", err)
	}
	defer cleanup()

	srv := server.New(journal.NewManager(db, embedder), search.NewService(db, embedder), serverVersion)
	return srv.Run(ctx)
}

// serverVersion is set by Main before serving so the MCP implementation
// reports the same string as `memo-mcp version`.
var serverVersion = "dev"

// SetVersion records the build version for the MCP server's Implementation.
func SetVersion(v string) { serverVersion = v }

// buildEmbedder constructs the embedding backend, returning a genuinely nil
// embedding.Embedder interface value on failure — not a non-nil interface
// wrapping a nil *embedding.HugotEmbedder. Every caller checks
// "if embedder != nil", and that only works with a true nil interface.
func buildEmbedder(ctx context.Context) (embedding.Embedder, func(), error) {
	e, err := embedding.NewHugotEmbedder(ctx, embedding.DefaultModelDir())
	if err != nil {
		return nil, func() {}, err
	}
	return e, e.Destroy, nil
}

func runStatus(ctx context.Context, stdout, stderr io.Writer) error {
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if errors.Is(err, kb.ErrNoSuchKB) {
		cfg, _ := ResolveConfig(os.Getenv)
		fmt.Fprintf(stdout, "No knowledge base named %q yet (%s). Add one with `memo-mcp ingest`.\n", cfg.DBName, kb.Path(cfg.KBDir, cfg.DBName))
		return nil
	}
	if err != nil {
		return err
	}
	defer closeFn()
	st, err := store.Status(ctx)
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	printStatus(stdout, st)
	return nil
}
