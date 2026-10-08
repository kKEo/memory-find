package locate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func fakeMemo(t *testing.T, dir, output string) string {
	t.Helper()
	p := filepath.Join(dir, "memo-mcp")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMemoBinary(t *testing.T) {
	dir := t.TempDir()
	p := fakeMemo(t, dir, "memo-mcp v9.9.9")
	if got, err := MemoBinary(p); err != nil || got != p {
		t.Errorf("configured: %q %v", got, err)
	}
	notExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MemoBinary(notExec); err == nil {
		t.Error("non-executable accepted")
	}
	t.Setenv("PATH", dir)
	if got, err := MemoBinary(""); err != nil || got != p {
		t.Errorf("from PATH: %q %v", got, err)
	}
}

func TestVersion(t *testing.T) {
	dir := t.TempDir()
	if v, err := Version(context.Background(), fakeMemo(t, dir, "memo-mcp v1.5.0-3-gabc")); err != nil || v != "v1.5.0-3-gabc" {
		t.Errorf("Version = %q, %v", v, err)
	}
	other := t.TempDir()
	if _, err := Version(context.Background(), fakeMemo(t, other, "something else")); err == nil {
		t.Error("odd output accepted")
	}
}
