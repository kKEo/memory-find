package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kKEo/memory-find/internal/retrieve"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestResolveConfigDefaultsToMemoKBUnderHome(t *testing.T) {
	cfg, err := ResolveConfig(env(map[string]string{"MEMO_HOME": "/tmp/home"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "default" || cfg.KBDir != filepath.Join("/tmp/home", "kb") || cfg.LogQueries {
		t.Fatalf("got %+v", cfg)
	}
	if len(cfg.Deprecations) != 0 {
		t.Fatalf("unexpected deprecations: %v", cfg.Deprecations)
	}
}

func TestResolveConfigMemoKBName(t *testing.T) {
	cfg, err := ResolveConfig(env(map[string]string{"MEMO_HOME": "/tmp/home", "MEMO_KB": "grpc-go"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "grpc-go" {
		t.Fatalf("got %+v", cfg)
	}
}

// Legacy variables are accepted as aliases for one release (JOURNAL_TOKEN as
// the name, JOURNAL_PATH as the home) with warnings; old journal files are
// never opened, the knowledge base is a new file under <home>/kb.
func TestResolveConfigLegacyJournalVariables(t *testing.T) {
	cfg, err := ResolveConfig(env(map[string]string{"JOURNAL_TOKEN": "proj", "JOURNAL_PATH": "/data/journals", "MEMO_QUERY_LOG": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "proj" || cfg.KBDir != filepath.Join("/data/journals", "kb") || !cfg.LogQueries {
		t.Fatalf("got %+v", cfg)
	}
	if len(cfg.Deprecations) != 2 {
		t.Fatalf("want 2 deprecation warnings, got %v", cfg.Deprecations)
	}
	// MEMO_KB wins over the legacy name when both are set.
	cfg, _ = ResolveConfig(env(map[string]string{"JOURNAL_TOKEN": "proj", "MEMO_KB": "new"}))
	if cfg.DBName != "new" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main(context.Background(), "v9.9.9-test", []string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"memo-mcp v9.9.9-test", "MCP protocol: 2026-07-28", "Go: go"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("version output missing %q:\n%s", want, out.String())
		}
	}
}

func TestUnknownCommandExits2(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main(context.Background(), "dev", []string{"frobnicate"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr: %s", errOut.String())
	}
}

func TestStatusOnFreshHome(t *testing.T) {
	t.Setenv("MEMO_HOME", t.TempDir())
	t.Setenv("JOURNAL_TOKEN", "")
	var out, errOut bytes.Buffer
	if code := Main(context.Background(), "dev", []string{"status"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "No knowledge base named") {
		t.Errorf("unexpected status output:\n%s", out.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(os.Getenv("MEMO_HOME"), "kb")); len(entries) != 0 {
		t.Errorf("status created files: %v", entries)
	}
}

// The old flag spelling must keep working for one release, with a warning.
func TestLegacyStatsFlagAlias(t *testing.T) {
	t.Setenv("MEMO_HOME", t.TempDir())
	t.Setenv("JOURNAL_TOKEN", "")
	var out, errOut bytes.Buffer
	if code := Main(context.Background(), "dev", []string{"--stats"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "deprecated") || !strings.Contains(out.String(), "No knowledge base named") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

// End to end through the CLI: ingest a file without a model, list it, read
// it, export it, re-import the export (no new revision), verify, status.
func TestKnowledgeBaseCommandsEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEMO_HOME", home)
	t.Setenv("MEMO_KB", "demo")
	t.Setenv("JOURNAL_TOKEN", "")
	run := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := Main(context.Background(), "dev", args, &out, &errOut)
		return out.String(), errOut.String(), code
	}
	doc := filepath.Join(t.TempDir(), "interceptors.md")
	if err := os.WriteFile(doc, []byte("# gRPC Interceptors\n\nInterceptors run in registration order.\n\n## Errors\n\nERR_CONN_RESET maps to codes.Unavailable.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := run("ingest", doc, "--ns", "grpc", "--uri", "https://example.com/interceptors", "--library", "grpc/grpc-go", "--version", "v1.8.0", "--embed=false")
	if code != 0 {
		t.Fatalf("ingest: %s %s", out, errOut)
	}
	if !strings.Contains(out, "revision 1") || !strings.Contains(out, "pending") {
		t.Fatalf("ingest output: %s", out)
	}
	uri := strings.Fields(strings.SplitN(out, "memo://", 2)[1])[0]
	uri = "memo://" + uri

	out, _, code = run("ls", "--ns", "grpc")
	if code != 0 || !strings.Contains(out, "gRPC Interceptors") || !strings.Contains(out, "@v1.8.0") {
		t.Fatalf("ls: %s", out)
	}
	out, _, code = run("read", uri)
	if code != 0 || !strings.Contains(out, "ERR_CONN_RESET") || !strings.Contains(out, "source: https://example.com/interceptors") {
		t.Fatalf("read: %s", out)
	}
	exportDir := filepath.Join(t.TempDir(), "export")
	if out, errOut, code = run("export", "--md", exportDir); code != 0 {
		t.Fatalf("export: %s %s", out, errOut)
	}
	files, _ := filepath.Glob(filepath.Join(exportDir, "grpc", "doc", "*.md"))
	if len(files) != 1 {
		t.Fatalf("exported files: %v", files)
	}
	out, _, code = run("ingest", exportDir, "--embed=false")
	if code != 0 || !strings.Contains(out, "1 unchanged") {
		t.Fatalf("re-import of export should be unchanged: %s", out)
	}
	out, _, code = run("verify")
	if code != 0 || !strings.Contains(out, "ok: no problems found") {
		// No model was ever configured, so no vectors are expected to exist.
		t.Fatalf("verify: %s", out)
	}
	out, _, code = run("status")
	if code != 0 || !strings.Contains(out, "Live documents: 1") || !strings.Contains(out, "grpc") {
		t.Fatalf("status: %s", out)
	}
	if _, errOut, code = run("read", "memo://doc/does-not-exist"); code != 1 || !strings.Contains(errOut, "not found") {
		t.Fatalf("read missing: %d %s", code, errOut)
	}
}

func TestFenceCode(t *testing.T) {
	got := fenceCode("handler.go", "package x\n\nfunc F() {}\n")
	if !strings.HasPrefix(got, "```go\n") || !strings.HasSuffix(got, "\n```\n") {
		t.Fatalf("got %q", got)
	}
	if fenceCode("notes.md", "# hi\n") != "# hi\n" {
		t.Fatal("markdown must not be fenced")
	}
}

// search and explain work from the terminal without a model (keyword-only,
// flagged degraded), and the CLI's numbers are the retrieval service's own
// numbers: the same Why struct, serialised, not a re-computation.
func TestSearchAndExplainCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEMO_HOME", home)
	t.Setenv("MEMO_KB", "demo")
	t.Setenv("JOURNAL_TOKEN", "")
	t.Setenv("MEMO_QUERY_LOG", "1")
	run := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := Main(context.Background(), "dev", args, &out, &errOut)
		return out.String(), errOut.String(), code
	}
	doc := filepath.Join(t.TempDir(), "i.md")
	if err := os.WriteFile(doc, []byte("# gRPC Interceptors\n\nInterceptors run in registration order; put auth before logging.\n\n## Errors\n\nERR_CONN_RESET maps to codes.Unavailable.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := run("ingest", doc, "--ns", "grpc", "--embed=false"); code != 0 {
		t.Fatalf("ingest: %s %s", out, errOut)
	}
	out, errOut, code := run("search", "auth interceptor order", "--no-model")
	if code != 0 || !strings.Contains(out, "gRPC Interceptors") || !strings.Contains(out, "degraded") {
		t.Fatalf("search: %d %s %s", code, out, errOut)
	}
	out, _, code = run("search", "ERR_CONN_RESET", "--no-model", "--format", "json")
	if code != 0 {
		t.Fatalf("search json: %s", out)
	}
	var resp retrieve.Response
	if err := json.Unmarshal([]byte(out), &resp); err != nil || len(resp.Results) == 0 {
		t.Fatalf("json output: %v %s", err, out)
	}
	// explain for one address prints its Why; the fused score must equal the
	// service's own number for that result.
	out, _, code = run("explain", "ERR_CONN_RESET", resp.Results[0].URI, "--no-model")
	if code != 0 {
		t.Fatalf("explain: %s", out)
	}
	var why retrieve.Why
	if err := json.Unmarshal([]byte(out), &why); err != nil {
		t.Fatalf("explain json: %v %s", err, out)
	}
	if why.URI != resp.Results[0].URI || math.Abs(why.Final-resp.Results[0].Score) > 1e-12 {
		t.Fatalf("CLI explain disagrees with search: %+v vs %+v", why, resp.Results[0])
	}
	// The query log recorded the searches (opt-in via MEMO_QUERY_LOG=1).
	out, _, code = run("log", "tail")
	if code != 0 || !strings.Contains(out, "ERR_CONN_RESET") {
		t.Fatalf("log tail: %s", out)
	}
	out, _, code = run("search", "quasar entanglement", "--no-model")
	if code != 0 || !strings.Contains(out, "no results") {
		t.Fatalf("abstention: %s", out)
	}
	// log replay turns the logged searches into unlabelled eval candidates.
	out, _, code = run("log", "replay")
	if code != 0 || !strings.Contains(out, `"query":"ERR_CONN_RESET"`) || !strings.Contains(out, `"category":"unlabelled"`) {
		t.Fatalf("log replay: %s", out)
	}
	// export --index is the AGENTS.md view: one line per document, under the cap.
	out, _, code = run("export", "--index", "--max-bytes", "8192")
	if code != 0 || !strings.Contains(out, "memo://doc/") || !strings.Contains(out, "# memo-mcp knowledge base index") || len(out) > 8192 {
		t.Fatalf("export --index: %d bytes, code %d\n%s", len(out), code, out)
	}
	out, _, code = run("export", "--index", "--max-bytes", "300")
	if code != 0 || len(out) > 300 || !strings.Contains(out, "omitted to fit") {
		t.Fatalf("export --index cap: %d bytes\n%s", len(out), out)
	}
}

// Facts, forgetting and trust from the terminal: the CLI is the human channel,
// so it may do what tool calls cannot, and everything it does is audited.
func TestFactsForgetTrustCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEMO_HOME", home)
	t.Setenv("MEMO_KB", "demo")
	t.Setenv("JOURNAL_TOKEN", "")
	run := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := Main(context.Background(), "dev", args, &out, &errOut)
		return out.String(), errOut.String(), code
	}
	doc := filepath.Join(t.TempDir(), "i.md")
	if err := os.WriteFile(doc, []byte("# gRPC Interceptors\n\nInterceptors run in registration order.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := run("ingest", doc, "--ns", "grpc", "--embed=false"); code != 0 {
		t.Fatalf("ingest: %s %s", out, errOut)
	}
	out, errOut, code := run("remember", "Interceptors run in the order they were registered.", "--ns", "grpc", "--about", "interceptors", "--evidence", "memo://chunk/1", "--no-model")
	if code != 0 || !strings.Contains(out, "recorded memo://fact/") {
		t.Fatalf("remember: %d %s %s", code, out, errOut)
	}
	factURI := "memo://" + strings.Fields(strings.SplitN(out, "memo://", 2)[1])[0]
	out, _, _ = run("facts", "ls", "--ns", "grpc")
	if !strings.Contains(out, "live") || !strings.Contains(out, "evidence memo://chunk/1") {
		t.Fatalf("facts ls: %s", out)
	}
	out, _, code = run("remember", "Interceptors run in reverse order since v2.", "--ns", "grpc", "--supersedes", factURI, "--no-model")
	if code != 0 || !strings.Contains(out, "is now history") {
		t.Fatalf("supersede: %s", out)
	}
	out, _, _ = run("read", factURI, "--history")
	if !strings.Contains(out, "LIVE") || !strings.Contains(out, "history") {
		t.Fatalf("history: %s", out)
	}
	out, _, code = run("trust", "promote", factURI, "--to", "curated")
	if code != 0 || !strings.Contains(out, "trust is now curated") {
		t.Fatalf("promote: %s", out)
	}
	out, _, _ = run("trust", "ls")
	if !strings.Contains(out, "curated") {
		t.Fatalf("trust ls: %s", out)
	}
	out, _, code = run("forget", factURI, "--reason", "wrong library")
	if code != 0 || !strings.Contains(out, "forgot") {
		t.Fatalf("forget: %s", out)
	}
	if _, errOut, code := run("read", factURI); code != 1 || !strings.Contains(errOut, "forgotten on") {
		t.Fatalf("tombstone read: %d %s", code, errOut)
	}
	if _, errOut, code := run("forget", "memo://doc/x", "--reason", ""); code != 1 || !strings.Contains(errOut, "usage") {
		t.Fatalf("forget without reason: %d %s", code, errOut)
	}
}

func TestMetricsCommandAndServeMetricsAddr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEMO_HOME", home)
	t.Setenv("MEMO_KB", "m")
	run := func(args ...string) (string, string, int) {
		var out, errOut bytes.Buffer
		code := Main(context.Background(), "test", args, &out, &errOut)
		return out.String(), errOut.String(), code
	}
	_, errOut, code := run("serve", "--metrics-addr", "0.0.0.0:0")
	if code == 0 || !strings.Contains(errOut, "loopback") {
		t.Fatalf("non-loopback metrics address accepted: %d %s", code, errOut)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nalpha beta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := run("ingest", dir, "--ns", "grpc", "--embed=false"); code != 0 {
		t.Fatalf("ingest: %s", errOut)
	}
	out, errOut, code := run("metrics")
	if code != 0 || !strings.Contains(out, "documents 1 live") || !strings.Contains(out, "per process") {
		t.Fatalf("metrics: %d %s %s", code, out, errOut)
	}
	out, _, code = run("metrics", "--json")
	var v map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &v) != nil || v["kb"] == nil {
		t.Fatalf("metrics --json: %d %s", code, out)
	}
}
