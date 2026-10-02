package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) Getenv { return func(k string) string { return m[k] } }

func TestResolveConfigDefaultsToMemoKBUnderHome(t *testing.T) {
	cfg, err := ResolveConfig(env(map[string]string{"MEMO_HOME": "/tmp/home"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "default" || cfg.KBDir != filepath.Join("/tmp/home", "kb") || cfg.DBDir != "/tmp/home" {
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

// Legacy variables keep their exact old meaning (old directory, no kb/
// subfolder) so existing journals stay reachable, and warn.
func TestResolveConfigLegacyJournalVariables(t *testing.T) {
	cfg, err := ResolveConfig(env(map[string]string{"MEMO_HOME": "/tmp/home", "JOURNAL_TOKEN": "proj", "JOURNAL_PATH": "/data/journals", "MEMO_KB": "ignored"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "proj" || cfg.DBDir != "/data/journals" || cfg.KBDir != filepath.Join("/tmp/home", "kb") {
		t.Fatalf("got %+v", cfg)
	}
	if len(cfg.Deprecations) != 2 {
		t.Fatalf("want 2 deprecation warnings, got %v", cfg.Deprecations)
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
