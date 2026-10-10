package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/home"
)

// serverRow is one knowledge base in the settings table.
type serverRow struct {
	KB, Addr, Auth string
	Autostart      bool
}

type settingsData struct {
	Rev          string
	Saved        bool
	Notice       string
	Errors       []string
	Login        bool
	StopOnQuit   bool
	MemoMode     string // auto or path
	MemoPath     string
	MemoStatus   string
	MemoOK       bool
	Model        string
	Models       []Model
	DefaultModel string
	Servers      []serverRow
	NewKB        string
	NewAddr      string
	NewAuth      string
	NewAutostart bool
	KBs          []string
	Env          string
	Debug        bool
	ConfigPath   string
	LogsDir      string
}

// maxServers bounds the rows read back from a posted form.
const maxServers = 100

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	cfg, rev, err := s.loadConfig()
	d := s.settingsFrom(r.Context(), cfg)
	d.Rev = rev
	d.Saved = r.URL.Query().Get("saved") == "1"
	if err != nil {
		d.Errors = append(d.Errors, err.Error()+" (saving this form replaces it)")
	}
	s.render(w, "settings.html", page{Title: "Settings", Data: d})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	cur, rev, _ := s.loadConfig()
	if r.PostForm.Get("rev") != rev {
		d := s.settingsFrom(r.Context(), cur)
		d.Rev = rev
		d.Notice = "tray.json changed while this form was open (memo-tray adds a server when you start a new knowledge base). Nothing was saved: check the values below and save again."
		w.WriteHeader(http.StatusConflict)
		s.render(w, "settings.html", page{Title: "Settings", Data: d})
		return
	}
	next, errs := s.fromForm(r.PostForm, cur)
	if want := r.PostForm.Get("login") == "on"; want != s.d.Login.Enabled() {
		var err error
		if want {
			err = s.d.Login.Enable()
		} else {
			err = s.d.Login.Disable()
		}
		if err != nil {
			errs = append(errs, "Launch at login: "+err.Error())
		}
	}
	if len(errs) > 0 {
		d := s.settingsFrom(r.Context(), next)
		d.Rev, d.Errors = rev, errs
		d.MemoMode, d.MemoPath = r.PostForm.Get("memo_mode"), r.PostForm.Get("memo_path")
		d.Env = r.PostForm.Get("env")
		d.NewKB, d.NewAddr, d.NewAuth = r.PostForm.Get("new_kb"), r.PostForm.Get("new_addr"), r.PostForm.Get("new_auth")
		d.NewAutostart = r.PostForm.Get("new_autostart") == "on"
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, "settings.html", page{Title: "Settings", Data: d})
		return
	}
	saved, err := config.SaveIf(config.Path(s.d.Home), next, rev)
	if err != nil {
		http.Error(w, "saving tray.json: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !saved { // changed between the check above and now
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	s.d.Notify(Event{Kind: ConfigSaved})
	http.Redirect(w, r, "/settings?saved=1", http.StatusSeeOther)
}

// fromForm builds the configuration a posted settings form describes.
// Per-server environment, which the form does not show, is kept.
func (s *Server) fromForm(f url.Values, cur *config.Config) (*config.Config, []string) {
	var errs []string
	next := &config.Config{Debug: f.Get("debug") == "on"}
	if f.Get("stop_on_quit") != "on" {
		no := false
		next.StopOnQuit = &no
	}
	if f.Get("memo_mode") == "path" {
		p := expandHome(strings.TrimSpace(f.Get("memo_path")))
		if !filepath.IsAbs(p) {
			errs = append(errs, "memo-mcp location: give the full path to the memo-mcp file, or choose Find automatically")
		} else if _, err := s.d.FindMemo(p); err != nil {
			errs = append(errs, "memo-mcp location: "+err.Error())
		}
		next.MemoBinary = p
	}

	env, envErrs := parseEnv(f.Get("env"))
	errs = append(errs, envErrs...)
	if m := strings.TrimSpace(f.Get("model")); m != "" {
		env["MEMO_MODEL"] = m
	}
	if len(env) > 0 {
		next.Env = env
	}

	n, _ := strconv.Atoi(f.Get("servers"))
	for i := range min(n, maxServers) {
		kb := f.Get(fmt.Sprintf("kb_%d", i))
		if kb == "" || f.Get(fmt.Sprintf("remove_%d", i)) == "on" {
			continue
		}
		srv := config.Server{KB: kb, Addr: strings.TrimSpace(f.Get(fmt.Sprintf("addr_%d", i))),
			Auth: authOf(f.Get(fmt.Sprintf("auth_%d", i))), Autostart: f.Get(fmt.Sprintf("autostart_%d", i)) == "on"}
		if old := cur.Find(kb); old != nil {
			srv.Env = old.Env
		}
		next.Servers = append(next.Servers, srv)
	}
	if kb := strings.TrimSpace(f.Get("new_kb")); kb != "" {
		switch {
		case !home.ValidName(kb):
			errs = append(errs, fmt.Sprintf("New server: %q is not a valid knowledge-base name (letters, digits, '.', '_', '-')", kb))
		case next.Find(kb) != nil:
			errs = append(errs, fmt.Sprintf("New server: %s is already listed", kb))
		default:
			if addr := strings.TrimSpace(f.Get("new_addr")); addr != "" {
				next.Servers = append(next.Servers, config.Server{KB: kb, Addr: addr})
			} else if _, _, err := next.AddrFor(kb, s.d.PortFree); err != nil {
				errs = append(errs, "New server: "+err.Error())
			}
			if srv := next.Find(kb); srv != nil {
				srv.Auth, srv.Autostart = authOf(f.Get("new_auth")), f.Get("new_autostart") == "on"
			}
		}
	}
	if err := next.Validate(); err != nil {
		errs = append(errs, err.Error())
	}
	return next, errs
}

// settingsFrom fills the form from a configuration.
func (s *Server) settingsFrom(ctx context.Context, cfg *config.Config) settingsData {
	d := settingsData{
		Login:        s.d.Login.Enabled(),
		StopOnQuit:   cfg.StopsOnQuit(),
		MemoMode:     "auto",
		MemoPath:     cfg.MemoBinary,
		Model:        cfg.Env["MEMO_MODEL"],
		Debug:        cfg.Debug,
		NewAuth:      "none",
		NewAutostart: true,
		ConfigPath:   config.Path(s.d.Home),
		LogsDir:      home.LogDir(s.d.Home),
	}
	if cfg.MemoBinary != "" {
		d.MemoMode = "path"
	}
	bin, version, err := s.memo(ctx, cfg)
	d.MemoOK = err == nil
	d.MemoStatus = memoLine(bin, version, err)
	if d.MemoOK {
		d.Models, _ = s.d.Models(ctx, bin)
		for _, m := range d.Models {
			if m.Default {
				d.DefaultModel = m.ID
			}
		}
	}
	for _, srv := range cfg.Servers {
		d.Servers = append(d.Servers, serverRow{KB: srv.KB, Addr: srv.Addr, Auth: authName(srv.Auth), Autostart: srv.Autostart})
	}
	d.KBs, _ = home.KBs(s.d.Home)
	var lines []string
	for k, v := range cfg.Env {
		if k != "MEMO_MODEL" {
			lines = append(lines, k+"="+v)
		}
	}
	slices.Sort(lines)
	d.Env = strings.Join(lines, "\n")
	return d
}

func (s *Server) openFromSettings(w http.ResponseWriter, r *http.Request) {
	var err error
	switch r.FormValue("what") {
	case "config":
		path := config.Path(s.d.Home)
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			err = (&config.Config{}).Save(path)
		}
		if err == nil {
			err = s.d.OpenFile(path, "text")
		}
	case "logs":
		dir := home.LogDir(s.d.Home)
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = s.d.OpenFile(dir, "folder")
		}
	}
	if err != nil {
		s.d.Logf("settings: open %s: %v", r.FormValue("what"), err)
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// loadConfig reads tray.json and a revision string that changes whenever
// the file does. A broken file comes back as an empty configuration plus
// its error.
func (s *Server) loadConfig() (*config.Config, string, error) {
	path := config.Path(s.d.Home)
	rev := config.Revision(path)
	cfg, err := config.Load(path)
	if err != nil {
		return &config.Config{}, rev, err
	}
	return cfg, rev, nil
}

// memo finds memo-mcp the way the menu does, and asks its version.
func (s *Server) memo(ctx context.Context, cfg *config.Config) (bin, version string, err error) {
	bin, err = s.d.FindMemo(cfg.MemoBinary)
	if err == nil {
		version, _ = s.d.MemoVersion(ctx, bin)
	}
	return bin, version, err
}

func memoLine(bin, version string, err error) string {
	if err != nil {
		return err.Error()
	}
	if version == "" {
		version = "(version unknown)"
	}
	return "memo-mcp " + version + " at " + bin
}

// parseEnv reads KEY=value lines; blank lines and # comments are skipped.
func parseEnv(text string) (map[string]string, []string) {
	env := map[string]string{}
	var errs []string
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		switch {
		case !ok || k == "":
			errs = append(errs, fmt.Sprintf("Environment, line %d: write NAME=value", i+1))
		case k == "MEMO_MODEL":
			errs = append(errs, "Environment: choose the embedding model with the field above, not MEMO_MODEL here")
		default:
			env[k] = strings.TrimSpace(v)
		}
	}
	return env, errs
}

func authOf(v string) string {
	if v == "token" {
		return "token"
	}
	return ""
}

func authName(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

// expandHome turns a leading ~/ into the home directory.
func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, rest)
		}
	}
	return p
}
