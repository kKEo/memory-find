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
	"sort"

	// The CLI is what opens databases, so it registers the SQLite driver
	// (with the sqlite-vec extension) itself.
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/journal"
	"github.com/kKEo/memory-find/internal/search"
	"github.com/kKEo/memory-find/internal/server"
)

// Config is everything the commands need from the environment.
type Config struct {
	// DBName is the knowledge-base (today: journal) name; it selects the
	// database file. Comes from MEMO_KB, or JOURNAL_TOKEN (deprecated).
	DBName string
	// DBDir is the directory holding the database file.
	DBDir string
	// Deprecations lists warnings about legacy variables that were honoured.
	Deprecations []string
}

// Getenv is the lookup function ResolveConfig uses; tests substitute it.
type Getenv func(string) string

// ResolveConfig maps the environment onto a Config.
//
// Precedence: the legacy JOURNAL_TOKEN / JOURNAL_PATH pair keeps its exact
// old meaning (<JOURNAL_PATH or ~/.memo-mcp>/<token>.db) so existing
// installs keep working, with a deprecation warning. Otherwise MEMO_KB
// (default "default") selects <MEMO_HOME or ~/.memo-mcp>/kb/<name>.db, the
// layout the knowledge-base format will use (roadmap P1).
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
	cfg.DBDir = filepath.Join(home, "kb")
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
  memo-mcp [serve]            start the MCP server on stdio (default)
  memo-mcp status             print database statistics
  memo-mcp version            print version, protocol version, Go version, model dir
  memo-mcp model redownload   fetch a fresh copy of the embedding model

Environment:
  MEMO_KB        database name (default "default"); file is $MEMO_HOME/kb/<name>.db
  MEMO_HOME      base directory (default ~/.memo-mcp)
  JOURNAL_TOKEN, JOURNAL_PATH   deprecated; still honoured with a warning
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
	db, err := openDB(stderr)
	if err != nil {
		return err
	}
	defer db.Close()

	stats, err := search.NewService(db, nil).GetStats(ctx) // no embedder needed
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}
	printStats(stdout, stats)
	return nil
}

func printStats(w io.Writer, stats *search.JournalStats) {
	fmt.Fprintln(w, "=== Journal Statistics ===")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Total entries: %d\n", stats.TotalEntries)
	if stats.TotalEntries == 0 {
		fmt.Fprintln(w, "\nJournal is empty.")
		return
	}
	fmt.Fprintf(w, "Date range: %s to %s\n",
		stats.EarliestEntry.Format("2006-01-02"), stats.LatestEntry.Format("2006-01-02"))
	coverage := float64(stats.EntriesWithEmbeddings) / float64(stats.TotalEntries) * 100
	fmt.Fprintf(w, "Entries with embeddings: %d/%d (%.1f%%)\n", stats.EntriesWithEmbeddings, stats.TotalEntries, coverage)
	fmt.Fprintln(w, "\nRecent activity:")
	fmt.Fprintf(w, "  Last 7 days: %d entries\n", stats.RecentActivity["7d"])
	fmt.Fprintf(w, "  Last 30 days: %d entries\n", stats.RecentActivity["30d"])
	if len(stats.SectionCounts) > 0 {
		fmt.Fprintln(w, "\nSection usage:")
		type sc struct {
			name  string
			count int
		}
		sections := make([]sc, 0, len(stats.SectionCounts))
		for name, count := range stats.SectionCounts {
			sections = append(sections, sc{name, count})
		}
		sort.Slice(sections, func(i, j int) bool { return sections[i].count > sections[j].count })
		for _, s := range sections {
			fmt.Fprintf(w, "  %s: %d\n", s.name, s.count)
		}
	}
	fmt.Fprintln(w, "\nStorage:")
	fmt.Fprintf(w, "  Location: %s\n", stats.DatabasePath)
	fmt.Fprintf(w, "  Size: %.2f MB\n", stats.DatabaseSizeMB)
	fmt.Fprintf(w, "  Avg entry length: %d characters\n", stats.AvgEntryLength)
}
