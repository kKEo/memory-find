package cli

import (
	"bytes"
	"context"
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
	if cfg.DBName != "default" || cfg.DBDir != filepath.Join("/tmp/home", "kb") {
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
	cfg, err := ResolveConfig(env(map[string]string{"JOURNAL_TOKEN": "proj", "JOURNAL_PATH": "/data/journals", "MEMO_KB": "ignored"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DBName != "proj" || cfg.DBDir != "/data/journals" {
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
	if !strings.Contains(out.String(), "Journal is empty.") {
		t.Errorf("unexpected status output:\n%s", out.String())
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
	if !strings.Contains(errOut.String(), "deprecated") || !strings.Contains(out.String(), "Total entries: 0") {
		t.Errorf("stdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}
