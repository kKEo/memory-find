// Package discover finds the memo-mcp HTTP servers running on this machine
// from their run files (<MEMO_HOME>/run/serve-<pid>.json, written by
// memo-mcp itself) and tidies up after servers that died without removing
// theirs.
package discover

import (
	"errors"
	"time"

	"github.com/kKEo/memory-find/internal/runfile"
)

// Process is what the operating system says about a pid.
type Process struct {
	Alive   bool
	Name    string    // command name; empty when unknown
	Started time.Time // zero when unknown
}

// Inspector looks up a pid (Inspect in production).
type Inspector func(pid int) Process

// ErrUntrusted means the run directory could be written by someone else,
// so its files cannot be believed.
var ErrUntrusted = errors.New("run directory is not owned by you or is writable by others; ignoring it")

// Scan returns the run files in dir that belong to a live memo-mcp, plus
// any file that cannot be read (Err set; left alone). A file whose process
// is gone, or whose pid now belongs to another program, is deleted.
func Scan(dir string, inspect Inspector) ([]runfile.Entry, error) {
	if err := trusted(dir); err != nil {
		return nil, err
	}
	entries, err := runfile.List(dir)
	if err != nil {
		return nil, err
	}
	var out []runfile.Entry
	for _, e := range entries {
		if e.Err != nil {
			out = append(out, e)
			continue
		}
		if !Ours(inspect(e.Info.PID), e) {
			_ = runfile.Remove(e.Path)
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Ours reports whether p is the memo-mcp process that wrote e: alive,
// named memo-mcp, and started before the file was written (a pid reused
// later belongs to someone else).
func Ours(p Process, e runfile.Entry) bool {
	switch {
	case !p.Alive:
		return false
	case p.Name != "" && p.Name != "memo-mcp":
		return false
	case !p.Started.IsZero() && !e.ModTime.IsZero() && p.Started.After(e.ModTime.Add(time.Second)):
		return false
	}
	return true
}
