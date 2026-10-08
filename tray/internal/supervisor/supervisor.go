// Package supervisor runs the memo-mcp servers memo-tray starts: one
// `memo-mcp serve --http` child per knowledge base, its output in a log
// file, its exit noticed and explained. It can also ask a server it did not
// start to stop, once it has made sure the pid still is that server.
package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/runfile"
	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/discover"
	"github.com/kKEo/memory-find/tray/internal/home"
)

// State is a child's state.
type State string

// The states.
const (
	Running  State = "running"
	Stopping State = "stopping"
	Stopped  State = "stopped"
	Crashed  State = "crashed"
)

// Spec is a server to start.
type Spec struct {
	KB   string
	Addr string
	Auth string            // none (default) or token
	Env  map[string]string // from tray.json: global env, then the server's own
}

// Child is a server memo-tray started, as last seen.
type Child struct {
	Spec    Spec
	PID     int
	State   State
	Started time.Time
	Exit    string // how it ended: "exit status 3", "signal: killed"
	LastLog string // the last line it logged, for a crash
	Log     string // log file
}

// maxLog is the size at which a server's log is rotated to <log>.1.
const maxLog = 5 << 20

// Supervisor is safe for concurrent use.
type Supervisor struct {
	home    string
	baseEnv []string

	mu       sync.Mutex
	children map[string]*child // by knowledge base
}

type child struct {
	Child
	proc     *os.Process
	done     chan struct{} // closed once the process is reaped
	stopping bool
}

// New returns a supervisor for servers under MEMO_HOME home; env is the
// environment children inherit (os.Environ()), minus what memo-tray sets.
func New(homeDir string, env []string) *Supervisor {
	return &Supervisor{home: homeDir, baseEnv: env, children: map[string]*child{}}
}

// LogPath is the log file of a knowledge base's server.
func (s *Supervisor) LogPath(kb string) string {
	return filepath.Join(home.LogDir(s.home), "serve-"+kb+".log")
}

// Start runs `bin serve --http <addr>` for spec.KB.
func (s *Supervisor) Start(bin string, spec Spec) error {
	if !home.ValidName(spec.KB) {
		return fmt.Errorf("invalid knowledge-base name %q", spec.KB)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.children[spec.KB]; c != nil && !c.reaped() {
		return fmt.Errorf("a server for %s is already running", spec.KB)
	}
	logPath := s.LogPath(spec.KB)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	rotate(logPath)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	args := []string{"serve", "--http", spec.Addr}
	if spec.Auth == "token" {
		args = append(args, "--auth", "token")
	}
	fmt.Fprintf(f, "--- memo-tray %s: %s %s (MEMO_KB=%s)\n", time.Now().Format(time.RFC3339), bin, strings.Join(args, " "), spec.KB)
	cmd := exec.Command(bin, args...)
	cmd.Env = childEnv(s.baseEnv, s.home, spec)
	cmd.Stdout, cmd.Stderr = f, f // stdin stays /dev/null
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		f.Close()
		return err
	}
	c := &child{Child: Child{Spec: spec, PID: cmd.Process.Pid, State: Running, Started: time.Now(), Log: logPath},
		proc: cmd.Process, done: make(chan struct{})}
	s.children[spec.KB] = c
	go func() {
		err := cmd.Wait()
		f.Close()
		s.exited(c, err)
	}()
	return nil
}

func (s *Supervisor) exited(c *child, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer close(c.done)
	c.Exit = "exited"
	if err != nil {
		c.Exit = err.Error()
	}
	if c.stopping || err == nil {
		c.State = Stopped
		return
	}
	c.State = Crashed
	c.LastLog = lastLine(c.Log)
}

func (c *child) reaped() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// Stop asks the server for kb to stop (SIGTERM) and kills it if it is
// still there after grace. It returns once the process
// is gone; stopping a server that is not running is not an error.
func (s *Supervisor) Stop(kb string, grace time.Duration) {
	s.mu.Lock()
	c := s.children[kb]
	if c == nil || c.reaped() {
		s.mu.Unlock()
		return
	}
	c.stopping, c.State = true, Stopping
	s.mu.Unlock()
	signal(c.proc, syscall.SIGTERM)
	select {
	case <-c.done:
	case <-time.After(grace):
		signal(c.proc, syscall.SIGKILL)
		<-c.done
	}
}

// StopAll stops every running child, in parallel.
func (s *Supervisor) StopAll(grace time.Duration) {
	var wg sync.WaitGroup
	for _, c := range s.Snapshot() {
		if c.State == Running || c.State == Stopping {
			wg.Add(1)
			go func() { defer wg.Done(); s.Stop(c.Spec.KB, grace) }()
		}
	}
	wg.Wait()
}

// Running counts the children still running.
func (s *Supervisor) Running() int {
	n := 0
	for _, c := range s.Snapshot() {
		if c.State == Running || c.State == Stopping {
			n++
		}
	}
	return n
}

// Snapshot returns every child memo-tray started, by knowledge base.
func (s *Supervisor) Snapshot() []Child {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Child, 0, len(s.children))
	for _, c := range s.children {
		out = append(out, c.Child)
	}
	slices.SortFunc(out, func(a, b Child) int { return strings.Compare(a.Spec.KB, b.Spec.KB) })
	return out
}

// signal goes through os.Process, which refuses once the child has been
// reaped, so a reused pid is never hit.
func signal(p *os.Process, sig syscall.Signal) { _ = p.Signal(sig) }

// StopExternal asks a server memo-tray did not start to stop. It sends
// SIGTERM only, and only once the pid is shown to still be that server:
// a memo-mcp that started before its run file was written, and, when the
// server answers, one that reports the same pid and instance.
func StopExternal(ctx context.Context, e runfile.Entry, inspect discover.Inspector, liveOf func(context.Context) (live.Snapshot, error)) error {
	p := inspect(e.Info.PID)
	if !discover.Ours(p, e) {
		return errors.New("that process is no longer the server; not stopping it")
	}
	snap, err := liveOf(ctx)
	switch {
	case err == nil && (snap.PID != e.Info.PID || snap.Instance != e.Info.Instance):
		return errors.New("another server answers at that address; not stopping it")
	case err != nil && p.Name != "memo-mcp":
		return fmt.Errorf("cannot confirm the process is the server (%v); not stopping it", err)
	}
	if err := syscall.Kill(e.Info.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop pid %d: %w", e.Info.PID, err)
	}
	return nil
}

// childEnv is the child's environment: the inherited one without anything
// that would change how or where it serves, then MEMO_HOME, MEMO_KB and
// tray.json's settings.
func childEnv(base []string, homeDir string, spec Spec) []string {
	var env []string
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if !config.Reserved(k) {
			if _, override := spec.Env[k]; !override {
				env = append(env, kv)
			}
		}
	}
	env = append(env, "MEMO_HOME="+homeDir, "MEMO_KB="+spec.KB)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		if !config.Reserved(k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		env = append(env, k+"="+spec.Env[k])
	}
	return env
}

// rotate moves a log over maxLog aside to <log>.1.
func rotate(path string) {
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLog {
		_ = os.Rename(path, path+".1")
	}
}

// lastLine is the last non-empty line of a log, shortened for a menu.
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 4096 {
		_, _ = f.Seek(-4096, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	lines := bytes.Split(bytes.TrimSpace(b), []byte("\n"))
	line := strings.TrimSpace(string(lines[len(lines)-1]))
	if r := []rune(line); len(r) > 120 {
		line = string(r[:119]) + "…"
	}
	return line
}
