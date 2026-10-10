// Package cli is the command-line front of memors-mcp: subcommand dispatch,
// configuration from the environment, and the small commands that do not
// need an MCP client (version, status, model management). It uses only the
// standard library's flag package (roadmap OD-14).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	// The CLI is what opens databases, so it registers the SQLite driver
	// (with the sqlite-vec extension) itself.
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/obs"
	"github.com/kKEo/memors/internal/retrieve"
	"github.com/kKEo/memors/internal/server"
)

// Config is everything the commands need from the environment.
type Config struct {
	// DBName is the knowledge-base name; it selects the database file.
	// Comes from MEMORS_KB (default "default").
	DBName string
	// KBDir is the directory holding knowledge-base files (<MEMORS_HOME>/kb).
	KBDir string
	// LogQueries is MEMORS_QUERY_LOG=1: keep an opt-in log of searches.
	LogQueries bool
}

// Getenv is the lookup function ResolveConfig uses; tests substitute it.
type Getenv func(string) string

// ResolveConfig maps the environment onto a Config.
//
// MEMORS_KB (default "default") names the knowledge base; its file is
// <MEMORS_HOME or ~/.memors-mcp>/kb/<name>.db.
func ResolveConfig(getenv Getenv) (Config, error) {
	var cfg Config
	home := getenv("MEMORS_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("resolve home directory: %w", err)
		}
		home = filepath.Join(h, ".memors-mcp")
	}
	cfg.KBDir = filepath.Join(home, "kb")
	cfg.LogQueries = getenv("MEMORS_QUERY_LOG") == "1"
	cfg.DBName = getenv("MEMORS_KB")
	if cfg.DBName == "" {
		cfg.DBName = "default"
	}
	return cfg, nil
}

// Main runs the CLI. version is the build's tag (from -ldflags); args are
// the process arguments without the program name. It returns the exit code.
func Main(ctx context.Context, version string, args []string, stdout, stderr io.Writer) int {
	// Legacy flag form: `memors-mcp --stats`, `memors-mcp --redownload-model`.
	// Kept as aliases for one release.
	fs := flag.NewFlagSet("memors-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	statsFlag := fs.Bool("stats", false, "Deprecated alias of `memors-mcp status`")
	redownload := fs.Bool("redownload-model", false, "Deprecated alias of `memors-mcp model redownload`")
	fs.Usage = func() { usage(stderr) }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Structured logs go to stderr (never stdout: in serve mode stdout is the
	// MCP stream). MEMORS_LOG_FORMAT and MEMORS_LOG_LEVEL configure them.
	obs.SetupLogging(stderr, os.Getenv)
	obs.BuildInfo(obs.Default(), version, server.ProtocolVersion)
	obs.Default().AddCollector(obs.RuntimeCollector())
	obs.Default().AddCollector(obs.ProcessCollector())
	rest := fs.Args()
	switch {
	case *statsFlag:
		slog.Warn("--stats is deprecated; use `memors-mcp status`")
		rest = []string{"status"}
	case *redownload:
		slog.Warn("--redownload-model is deprecated; use `memors-mcp model redownload`")
		rest = []string{"model", "redownload"}
	}

	cmd := "serve"
	if len(rest) > 0 {
		cmd, rest = rest[0], rest[1:]
	}

	var err error
	switch cmd {
	case "serve":
		err = runServe(ctx, rest, stderr)
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
	case "search":
		err = runSearch(ctx, rest, stdout, stderr, false)
	case "explain":
		err = runSearch(ctx, rest, stdout, stderr, true)
	case "log":
		err = runLog(ctx, rest, stdout, stderr)
	case "model":
		err = runModel(ctx, rest, stdout, stderr)
	case "reindex":
		err = runReindex(ctx, rest, stdout, stderr)
	case "profiles":
		err = runProfiles(rest, stdout, stderr)
	case "eval":
		err = runEval(ctx, rest, stdout, stderr)
	case "remember":
		err = runRemember(ctx, rest, stdout, stderr)
	case "forget":
		err = runForget(ctx, rest, stdout, stderr)
	case "facts":
		err = runFacts(ctx, rest, stdout, stderr)
	case "trust":
		err = runTrust(ctx, rest, stdout, stderr)
	case "explore":
		err = runExplore(ctx, rest, stdout, stderr)
	case "graph":
		err = runGraph(ctx, rest, stdout, stderr)
	case "compact":
		err = runCompact(ctx, rest, stdout, stderr)
	case "submit":
		err = runSubmit(ctx, rest, stdout, stderr)
	case "lint":
		err = runLint(ctx, rest, stdout, stderr)
	case "pages":
		err = runPages(ctx, rest, stdout, stderr)
	case "ui":
		err = runUI(ctx, rest, stdout, stderr)
	case "http-token":
		err = runHTTPToken(ctx, rest, stdout, stderr)
	case "migrate":
		err = runMigrate(ctx, stdout, stderr)
	case "metrics":
		err = runMetrics(ctx, rest, stdout, stderr)
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
	fmt.Fprint(w, `memors-mcp — a local, measurable knowledge base for agents (MCP server + CLI)

Usage:
  memors-mcp [serve] [--metrics-addr 127.0.0.1:9469]   start the MCP server on stdio (default); the flag exposes /metrics on loopback
  memors-mcp serve --http 127.0.0.1:8765   one long-running MCP server over HTTP (/mcp) with the live web UI (/)
        [--auth token|none] [--token-file F] [--tls-cert F --tls-key F [--tls-client-ca F]]
        [--allow-remote --public-url https://host:port] [--behind-proxy]
  memors-mcp http-token [--rotate]   print (or replace) the bearer token for serve --http
  memors-mcp ingest <file|dir|->     add documents to the knowledge base
      --ns <name> --kind doc|note|code|conversation --uri <u> --title <t>
      --library <l> --version <v> --trust user|curated --context <text> --embed=false
  memors-mcp search "<q>" [--mode --ns --library --version --kind --limit --format table|json|md --explain]
  memors-mcp explain "<q>" [<memo://...>]   ranking table for every hit, or the full why for one
  memors-mcp log tail|calls|show <id>|replay|prune   inspect the opt-in query and call logs
  memors-mcp remember "<fact>" [--ns --about a,b --valid-from --valid-to --supersedes <memo://fact/..> --evidence <memo://chunk/n> --trust user|curated]
  memors-mcp forget <memo://doc/..|memo://fact/..> --reason "<why>" [--redact]
  memors-mcp facts ls [--ns --as-of YYYY-MM-DD --history --json]
  memors-mcp trust ls | promote <uri> --to user|curated | demote <uri> --to agent|user
  memors-mcp explore <name> [--ns --hops 1|2 --as-of --json]   walk the graph index from one entity
  memors-mcp graph merges [--state] | merge <id> | reject <id> | rebuild [--ns]   review near-duplicate names; re-extract mentions
  memors-mcp compact [--ns --kinds page,stale,conflict,merge,duplicate --lint --json] [--executor ollama --apply]
                                   propose compaction work (pages to write, conflicts, merges, duplicates)
  memors-mcp submit <item-id> [--content-file f.md --title t | --keep <memo://fact/..> | --accept|--reject | --skip] [--reason ..] [--dry-run]
  memors-mcp lint [--ns --json]      contradictions, orphan entities, missing or stale pages, expired facts
  memors-mcp pages ls [--ns --stale --json]   list curated pages
  memors-mcp ui [--addr 127.0.0.1:0 --no-model]   read-only web face on loopback (prints the URL)
  memors-mcp read <memo://...> [--history]   print a record with its provenance, or its revision chain
  memors-mcp ls [--ns --kind --since 2026-01-01 --json]   list live documents, newest first
  memors-mcp export --md <dir> [--ns <name>]              write markdown files with front matter
  memors-mcp migrate                 bring an existing file to this binary's schema version
  memors-mcp verify [--repair]       check integrity (chunks, vectors, indexes)
  memors-mcp backfill                embed chunks whose vectors are pending
  memors-mcp status                  print knowledge-base statistics
  memors-mcp metrics [--json --since 24h]   knowledge-base gauges and per-tool call statistics from the opt-in log
  memors-mcp version                 print version, protocol version, Go version, model dir
  memors-mcp model ls|smoke|pull|use|redownload   the embedding-model registry (MEMORS_MODEL picks one)
  memors-mcp reindex [--model <id>]  embed every passage that lacks a vector for the model
  memors-mcp profiles show [<name>]  print every ranking constant with its derivation (MEMORS_PROFILE picks one)
  memors-mcp eval [--models hash,minilm --profiles default,all --corpus notes|kb|all --format table|md|json --explain-failures]

Environment:
  MEMORS_KB        knowledge-base name (default "default"); file is $MEMORS_HOME/kb/<name>.db
  MEMORS_HOME      base directory (default ~/.memors-mcp)
  MEMORS_QUERY_LOG=1               keep an opt-in log of searches in the same file
  MEMORS_MODEL     embedding model id from 'memors-mcp model ls' (default granite-small-r2)
  MEMORS_PROFILE   ranking profile (default "default"); overrides in $MEMORS_HOME/profiles.json
  MEMORS_RERANK=1  attach the cross-encoder reranker (used by the precise profile)
  MEMORS_OLLAMA_URL, MEMORS_OLLAMA_MODEL   optional local model for 'compact --executor ollama' (loopback by default)
  MEMORS_METRICS_ADDR   same as 'serve --metrics-addr' (loopback only)
  MEMORS_HTTP_ADDR      same as 'serve --http' (loopback unless --allow-remote)
  MEMORS_HTTP_AUTH      same as 'serve --auth' (token | none)
  MEMORS_HTTP_TOKEN_FILE  bearer token file (default <MEMORS_HOME>/http-token)
  MEMORS_TLS_CERT, MEMORS_TLS_KEY, MEMORS_TLS_CLIENT_CA   same as 'serve --tls-cert/--tls-key/--tls-client-ca'
  MEMORS_PUBLIC_URL     same as 'serve --public-url'
  MEMORS_LOG_FORMAT     text (default) | json      MEMORS_LOG_LEVEL   debug | info (default) | warn | error
`)
}

func runVersion(version string, w io.Writer) error {
	fmt.Fprintf(w, "memors-mcp %s\n", version)
	fmt.Fprintf(w, "MCP protocol: %s\n", server.ProtocolVersion)
	fmt.Fprintf(w, "Go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(w, "Model dir: %s\n", embedding.DefaultModelDir())
	return nil
}

func runModel(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memors-mcp model ls | smoke [<id>|--all] | pull <id> | use <id> | redownload [<id>]")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		return runModelLs(ctx, stdout, stderr)
	case "smoke":
		return runModelSmoke(ctx, rest, stdout, stderr)
	case "pull":
		if len(rest) != 1 {
			return errors.New("usage: memors-mcp model pull <id>")
		}
		info, err := embedding.LookupModel(rest[0])
		if err != nil {
			return err
		}
		_, cleanup, err := embedding.Load(ctx, info, embedding.DefaultModelDir())
		if err != nil {
			return err
		}
		cleanup()
		fmt.Fprintf(stdout, "%s is downloaded and loads under the pure-Go backend\n", info.ID)
		return nil
	case "use":
		if len(rest) != 1 {
			return errors.New("usage: memors-mcp model use <id>")
		}
		if _, err := embedding.LookupModel(rest[0]); err != nil {
			return err
		}
		store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, nil)
		if err != nil {
			return err
		}
		defer closeFn()
		if err := store.SetDefaultModel(ctx, rest[0], "cli", kb.ChannelCLI); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "default model is now %s; set MEMORS_MODEL=%s in the MCP server config so queries embed with it\n", rest[0], rest[0])
		return nil
	case "redownload":
		info := embedding.MiniLM
		if len(rest) == 1 {
			var err error
			if info, err = embedding.LookupModel(rest[0]); err != nil {
				return err
			}
		}
		modelDir := embedding.DefaultModelDir()
		slog.Info("redownloading embedding model", "repo", info.HFRepo, "dir", modelDir)
		if _, err := embedding.RedownloadModelFor(ctx, info, modelDir); err != nil {
			return fmt.Errorf("redownload model: %w", err)
		}
		slog.Info("model redownloaded", "repo", info.HFRepo)
		return nil
	default:
		return fmt.Errorf("unknown model subcommand %q", sub)
	}
}

func runServe(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	metricsAddr := fs.String("metrics-addr", os.Getenv("MEMORS_METRICS_ADDR"), "expose Prometheus metrics at http://<addr>/metrics (loopback only; off when empty)")
	var hf httpFlags
	hf.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := ResolveConfig(os.Getenv)
	if err != nil {
		return err
	}
	// Bind the metrics listener before the store is opened so that a bad
	// address fails fast without touching the database or starting the
	// background backfill.
	var ms *obs.MetricsServer
	var ln net.Listener
	if *metricsAddr != "" {
		ms = obs.NewMetricsServer(obs.Default())
		ln, err = ms.Listen(*metricsAddr)
		if err != nil {
			return err
		}
	}
	// The HTTP listener (with TLS and the token) is set up early for the
	// same reason.
	var web *httpSetup
	if hf.addr != "" {
		web, err = prepareHTTP(hf, filepath.Dir(cfg.KBDir))
		if err != nil {
			return err
		}
		defer func() { _ = web.ln.Close() }()
	}
	embedder, cleanup, err := buildEmbedder(ctx)
	if err != nil {
		slog.Warn("embedding unavailable; search is keyword-only and new vectors are queued", "err", err)
	}
	defer cleanup()

	if err := retrieve.LoadOverrides(filepath.Join(filepath.Dir(cfg.KBDir), "profiles.json")); err != nil {
		return err
	}
	db, err := kb.Open(ctx, cfg.KBDir, cfg.DBName, kb.Options{})
	if err != nil {
		return err
	}
	defer db.Close()
	store := kb.NewStore(db, embedder)

	profile, err := retrieve.Lookup(os.Getenv("MEMORS_PROFILE"))
	if err != nil {
		return err
	}
	svc := retrieve.New(store, profile, cfg.LogQueries)
	if os.Getenv("MEMORS_RERANK") == "1" {
		rr, closeRR, err := loadReranker(ctx, stderr)
		if err != nil {
			slog.Warn("reranker unavailable; continuing without it", "err", err)
		} else {
			defer closeRR()
			svc.WithReranker(rr)
		}
	}
	obs.Default().AddCollector(store.MetricsCollector(5 * time.Second))
	if ln != nil {
		hs := &http.Server{Handler: ms.Handler(), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = hs.Shutdown(shutdown)
		}()
		go func() {
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Warn("metrics endpoint stopped", "err", err)
			}
		}()
		slog.Info("metrics listening", "url", "http://"+ln.Addr().String()+"/metrics")
	}
	srv := server.New(store, svc, serverVersion, server.WithLogger(slog.Default().With("component", "mcp")), server.WithRegistry(obs.Default()), server.WithCallLog(cfg.LogQueries), server.WithKBPath(kb.Path(cfg.KBDir, cfg.DBName)))

	// In the background: drain queued vectors, then embed any passage that
	// has no vector for THIS model yet (a store built with another model
	// keeps working by keyword meanwhile and reports degraded until done).
	if embedder != nil {
		go func() {
			srv.SetBackground("backfill running")
			n, err := store.Backfill(ctx)
			if err != nil {
				slog.Warn("backfill failed", "err", err)
				srv.SetBackground("backfill failed: " + err.Error())
				return
			} else if n > 0 {
				slog.Info("backfilled chunk vectors", "n", n)
			}
			srv.SetBackground("reindex running")
			m, err := store.Reindex(ctx, nil)
			if err != nil {
				slog.Warn("reindex failed", "model", embedder.Info().ID, "err", err)
				srv.SetBackground("reindex failed: " + err.Error())
				return
			} else if m > 0 {
				slog.Info("embedded passages for the current model", "n", m, "model", embedder.Info().ID)
			}
			srv.SetBackground(fmt.Sprintf("idle (startup backfill %d, reindex %d vectors)", n, m))
		}()
	} else {
		srv.SetBackground("no embedding model: keyword search only, vectors queued")
	}

	if web == nil {
		return srv.Run(ctx)
	}
	return serveHTTP(ctx, web, srv, store, svc)
}

// serverVersion is set by Main before serving so the MCP implementation
// reports the same string as `memors-mcp version`.
var serverVersion = "dev"

// SetVersion records the build version for the MCP server's Implementation.
func SetVersion(v string) { serverVersion = v }

// buildEmbedder constructs the embedding backend for the model MEMORS_MODEL
// names (default: the registry's MiniLM), returning a genuinely nil
// embedding.Embedder interface value on failure — not a non-nil interface
// wrapping a nil pointer. Every caller checks "if embedder != nil", and that
// only works with a true nil interface.
func buildEmbedder(ctx context.Context) (embedding.Embedder, func(), error) {
	info, err := selectedModel()
	if err != nil {
		return nil, func() {}, err
	}
	e, cleanup, err := embedding.Load(ctx, info, embedding.DefaultModelDir())
	if err != nil {
		return nil, func() {}, err
	}
	return e, cleanup, nil
}

// selectedModel resolves MEMORS_MODEL against the registry; unset means the
// registry default (granite-small-r2 since v0.7.0).
func selectedModel() (embedding.ModelInfo, error) {
	id := os.Getenv("MEMORS_MODEL")
	if id == "" {
		return embedding.DefaultModel(), nil
	}
	return embedding.LookupModel(id)
}

func runStatus(ctx context.Context, stdout, stderr io.Writer) error {
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if errors.Is(err, kb.ErrNoSuchKB) {
		cfg, _ := ResolveConfig(os.Getenv)
		fmt.Fprintf(stdout, "No knowledge base named %q yet (%s). Add one with `memors-mcp ingest`.\n", cfg.DBName, kb.Path(cfg.KBDir, cfg.DBName))
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
