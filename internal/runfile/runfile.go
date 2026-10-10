// Package runfile advertises running `memors-mcp serve --http` servers to
// local companion apps (memors-tray). While it serves, each server keeps
// <MEMORS_HOME>/run/serve-<pid>.json, which says where and how to reach it,
// and removes the file when it stops. A run file never holds a secret:
// in token mode it names the token file, which the reader opens with its
// own permissions.
//
// memors-tray, a separate module, imports this package, so it depends on the
// standard library only.
package runfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Schema is the version of the run-file shape. New fields may be added
// without changing it.
const Schema = 1

// Info describes one running server.
type Info struct {
	Schema      int       `json:"schema"`
	PID         int       `json:"pid"`
	Instance    string    `json:"instance"` // the same id /live.json reports
	Version     string    `json:"version"`
	KB          string    `json:"kb"`
	KBPath      string    `json:"kb_path"`
	URL         string    `json:"url"`    // what clients use: --public-url, else scheme://listen
	Listen      string    `json:"listen"` // the bound address
	Scheme      string    `json:"scheme"` // http or https
	Auth        string    `json:"auth"`   // none or token
	TokenFile   string    `json:"token_file,omitempty"`
	MTLS        bool      `json:"mtls"`
	BehindProxy bool      `json:"behind_proxy"`
	Started     time.Time `json:"started"`
}

// Dir is the run-file directory under MEMORS_HOME.
func Dir(home string) string { return filepath.Join(home, "run") }

// Name is the file name for a server process.
func Name(pid int) string { return "serve-" + strconv.Itoa(pid) + ".json" }

// Write stores info as dir/serve-<pid>.json and returns its path. The
// directory is created 0700 and the file 0600; readers never see a partial
// file.
func Write(dir string, info Info) (string, error) {
	if info.Schema == 0 {
		info.Schema = Schema
	}
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".serve-*.tmp") // created 0600
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op after the rename
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, Name(info.PID))
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// Remove deletes a run file; a missing file is not an error.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Entry is one run file as found on disk.
type Entry struct {
	Path    string
	ModTime time.Time
	Info    Info
	// Err is set when the file cannot be used: unreadable, not JSON, an
	// unknown schema, or a pid that does not match the file name.
	Err error
}

// List reads every run file in dir, ordered by file name. A missing
// directory means no servers.
func List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, de := range des {
		pid, ok := pidOf(de.Name())
		if !ok || !de.Type().IsRegular() {
			continue
		}
		e := Entry{Path: filepath.Join(dir, de.Name())}
		if fi, err := de.Info(); err == nil {
			e.ModTime = fi.ModTime()
		}
		e.Err = read(e.Path, pid, &e.Info)
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func read(path string, pid int, info *Info) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, info); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if info.Schema != Schema {
		return fmt.Errorf("%s: schema %d, this reader knows %d", filepath.Base(path), info.Schema, Schema)
	}
	if info.PID != pid {
		return fmt.Errorf("%s: names pid %d inside", filepath.Base(path), info.PID)
	}
	return nil
}

// pidOf parses serve-<pid>.json.
func pidOf(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, "serve-")
	if !ok {
		return 0, false
	}
	digits, ok = strings.CutSuffix(digits, ".json")
	if !ok || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	return pid, err == nil && pid > 0
}
