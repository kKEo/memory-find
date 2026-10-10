package runfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteListRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	in := Info{PID: 4242, Instance: "I", Version: "v1", KB: "crportal", KBPath: "/kb/crportal.db", URL: "http://127.0.0.1:8765",
		Listen: "127.0.0.1:8765", Scheme: "http", Auth: "token", TokenFile: "/home/u/.memors-mcp/http-token", Started: time.Unix(1700000000, 0).UTC()}
	path, err := Write(dir, in)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "serve-4242.json" {
		t.Errorf("path = %s", path)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
			t.Errorf("dir mode %o, want 700", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("file mode %o, want 600", fi.Mode().Perm())
		}
	}
	entries, err := List(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	e := entries[0]
	in.Schema = Schema
	if e.Err != nil || e.Info != in || e.Path != path || e.ModTime.IsZero() {
		t.Errorf("entry = %+v", e)
	}
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Remove(path); err != nil {
		t.Errorf("removing a missing file: %v", err)
	}
	if entries, _ := List(dir); len(entries) != 0 {
		t.Errorf("after remove: %+v", entries)
	}
}

func TestListReportsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("serve-7.json", `{"schema":1,"pid":8}`)   // pid does not match the name
	write("serve-9.json", `{"schema":2,"pid":9}`)   // unknown schema
	write("serve-11.json", `not json`)              // garbage
	write(".serve-123.tmp", `{"schema":1,"pid":1}`) // a write in progress
	write("serve-x.json", `{}`)                     // not a pid
	write("notes.txt", `hi`)
	entries, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	for _, e := range entries {
		if e.Err == nil {
			t.Errorf("%s accepted", e.Path)
		} else if filepath.Base(e.Path) == "serve-7.json" && !strings.Contains(e.Err.Error(), "pid 8") {
			t.Errorf("pid mismatch error = %v", e.Err)
		}
	}
	if entries, err := List(filepath.Join(dir, "missing")); err != nil || entries != nil {
		t.Errorf("missing dir: %v %v", entries, err)
	}
}
