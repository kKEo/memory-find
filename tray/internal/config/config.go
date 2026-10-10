// Package config is memors-tray's settings file, <MEMORS_HOME>/tray.json: the
// servers it starts (knowledge base, address, auth), extra environment for
// them, and where memors-mcp is installed.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/kKEo/memors/tray/internal/home"
)

// Config is tray.json.
type Config struct {
	// MemorsBinary is the memors-mcp executable; empty means search for it.
	MemorsBinary string `json:"memors_binary,omitempty"`
	// Env is extra environment for every server memors-tray starts. Apps
	// started from Finder do not see your shell's variables, so settings
	// such as MEMORS_MODEL go here.
	Env     map[string]string `json:"env,omitempty"`
	Servers []Server          `json:"servers,omitempty"`
	// StopOnQuit stops the servers memors-tray started when it quits
	// (default true).
	StopOnQuit *bool `json:"stop_on_quit,omitempty"`
	// Debug enables the web inspector in stats windows.
	Debug bool `json:"debug,omitempty"`
}

// Server is one knowledge base memors-tray can start.
type Server struct {
	KB        string            `json:"kb"`
	Addr      string            `json:"addr"`           // loopback host:port
	Auth      string            `json:"auth,omitempty"` // none (default) or token
	Autostart bool              `json:"autostart,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// Path is the settings file under MEMORS_HOME.
func Path(homeDir string) string { return filepath.Join(homeDir, "tray.json") }

// Load reads path; a missing file is an empty configuration.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return &c, nil
}

// Save writes c to path atomically, readable by the owner only.
func (c *Config) Save(path string) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tray-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// writeMu serialises read-modify-write cycles on tray.json: the menu (an
// address for a newly started knowledge base) and the settings pages write
// it from different goroutines.
var writeMu sync.Mutex

// Update applies change to the settings in path and saves the result,
// holding writeMu from read to write, so concurrent writers never undo each
// other. An unreadable file is replaced. Nothing is written when change
// leaves the settings as they were, or when the result is invalid.
func Update(path string, change func(c *Config) error) (*Config, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	c, loadErr := Load(path)
	if loadErr != nil {
		c = &Config{}
	}
	before, _ := json.Marshal(c)
	if err := change(c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if after, _ := json.Marshal(c); loadErr == nil && string(after) == string(before) {
		return c, nil
	}
	return c, c.Save(path)
}

// SaveIf saves c in place of the settings in path if they are still at
// revision rev (see Revision); it reports whether it did.
func SaveIf(path string, c *Config, rev string) (bool, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	if Revision(path) != rev {
		return false, nil
	}
	return true, c.Save(path)
}

// Revision changes whenever the file at path does; "none" when there is
// no file.
func Revision(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

// StopsOnQuit reports whether quitting stops the servers memors-tray started.
func (c *Config) StopsOnQuit() bool { return c.StopOnQuit == nil || *c.StopOnQuit }

// Find returns the entry for a knowledge base, or nil.
func (c *Config) Find(kb string) *Server {
	for i := range c.Servers {
		if c.Servers[i].KB == kb {
			return &c.Servers[i]
		}
	}
	return nil
}

// FirstPort and LastPort bound the addresses AddrFor hands out.
const (
	FirstPort = 8765
	LastPort  = 8799
)

// AddrFor returns the address for kb, assigning the first free port from
// FirstPort on when kb has none yet; added reports a new entry, which the
// caller saves, so the knowledge base keeps its MCP URL from then on.
// free reports whether nothing listens on an address.
func (c *Config) AddrFor(kb string, free func(addr string) bool) (addr string, added bool, err error) {
	if s := c.Find(kb); s != nil {
		return s.Addr, false, nil
	}
	used := map[string]bool{}
	for _, s := range c.Servers {
		used[s.Addr] = true
	}
	for port := FirstPort; port <= LastPort; port++ {
		a := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		if !used[a] && free(a) {
			c.Servers = append(c.Servers, Server{KB: kb, Addr: a})
			return a, true, nil
		}
	}
	return "", false, fmt.Errorf("no free port in %d-%d; set an address for %s in tray.json", FirstPort, LastPort, kb)
}

// PortFree reports whether addr can be bound right now.
func PortFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Reserved reports whether memors-tray sets an environment variable itself
// (or removes it) for the servers it starts, so tray.json may not.
func Reserved(name string) bool {
	switch name {
	case "MEMORS_KB", "MEMORS_HOME", "MEMORS_PUBLIC_URL", "MEMORS_METRICS_ADDR":
		return true
	}
	return strings.HasPrefix(name, "MEMORS_HTTP_") || strings.HasPrefix(name, "MEMORS_TLS_")
}

// Validate checks names, addresses and environment.
func (c *Config) Validate() error {
	if err := validEnv(c.Env); err != nil {
		return err
	}
	kbs, addrs := map[string]bool{}, map[string]bool{}
	for _, s := range c.Servers {
		if !home.ValidName(s.KB) {
			return fmt.Errorf("server %q: not a valid knowledge-base name", s.KB)
		}
		if kbs[s.KB] {
			return fmt.Errorf("server %q listed twice", s.KB)
		}
		kbs[s.KB] = true
		host, port, err := net.SplitHostPort(s.Addr)
		if err != nil {
			return fmt.Errorf("server %q: addr %q: %w", s.KB, s.Addr, err)
		}
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("server %q: addr %q: bad port", s.KB, s.Addr)
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("server %q: addr %q: memors-tray starts loopback servers only", s.KB, s.Addr)
		}
		if addrs[s.Addr] {
			return fmt.Errorf("server %q: addr %s used twice", s.KB, s.Addr)
		}
		addrs[s.Addr] = true
		if s.Auth != "" && s.Auth != "none" && s.Auth != "token" {
			return fmt.Errorf("server %q: auth must be none or token, not %q", s.KB, s.Auth)
		}
		if err := validEnv(s.Env); err != nil {
			return fmt.Errorf("server %q: %w", s.KB, err)
		}
	}
	return nil
}

func validEnv(env map[string]string) error {
	for k := range env {
		if !envName.MatchString(k) {
			return fmt.Errorf("env: %q is not a variable name", k)
		}
		if Reserved(k) {
			return fmt.Errorf("env: %s is set by memors-tray itself", k)
		}
	}
	return nil
}
