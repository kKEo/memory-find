package home

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDirFollowsMemoHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MEMO_HOME", dir)
	if got, err := Dir(); err != nil || got != dir {
		t.Errorf("Dir = %q, %v; want %q", got, err, dir)
	}
	t.Setenv("MEMO_HOME", "")
	if got, err := Dir(); err != nil || filepath.Base(got) != ".memo-mcp" {
		t.Errorf("default Dir = %q, %v", got, err)
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{"default": true, "crportal": true, "my.kb-2_x": true, "": false, ".": false, "..": false, "a/b": false, "a b": false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v", name, !want)
		}
	}
}

func TestKBs(t *testing.T) {
	h := t.TempDir()
	if kbs, err := KBs(h); err != nil || kbs != nil {
		t.Fatalf("no kb dir: %v %v", kbs, err)
	}
	if err := os.MkdirAll(KBDir(h), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"crportal.db", "default.db", "default.db-wal", "default.db-shm", "notes.txt", "bad name.db"} {
		if err := os.WriteFile(filepath.Join(KBDir(h), f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if kbs, err := KBs(h); err != nil || !slices.Equal(kbs, []string{"crportal", "default"}) {
		t.Errorf("KBs = %v, %v", kbs, err)
	}
}
