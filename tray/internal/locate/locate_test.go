package locate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func fakeMemors(t *testing.T, dir, output string) string {
	t.Helper()
	p := filepath.Join(dir, "memors-mcp")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho '"+output+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMemorsBinary(t *testing.T) {
	dir := t.TempDir()
	p := fakeMemors(t, dir, "memors-mcp v9.9.9")
	if got, err := MemorsBinary(p); err != nil || got != p {
		t.Errorf("configured: %q %v", got, err)
	}
	notExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MemorsBinary(notExec); err == nil {
		t.Error("non-executable accepted")
	}
	t.Setenv("PATH", dir)
	if got, err := MemorsBinary(""); err != nil || got != p {
		t.Errorf("from PATH: %q %v", got, err)
	}
}

func TestVersion(t *testing.T) {
	dir := t.TempDir()
	if v, err := Version(context.Background(), fakeMemors(t, dir, "memors-mcp v1.5.0-3-gabc")); err != nil || v != "v1.5.0-3-gabc" {
		t.Errorf("Version = %q, %v", v, err)
	}
	other := t.TempDir()
	if _, err := Version(context.Background(), fakeMemors(t, other, "something else")); err == nil {
		t.Error("odd output accepted")
	}
}
