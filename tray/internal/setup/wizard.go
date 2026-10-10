package setup

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/home"
	"github.com/kKEo/memory-find/tray/internal/installer"
	"github.com/kKEo/memory-find/tray/internal/menu"
)

// releaseFor is how long a looked-up release is reused before asking
// GitHub again.
const releaseFor = 10 * time.Minute

// job is one download-and-install, run in the background while the
// progress page reloads itself.
type job struct {
	release installer.Release
	dir     string

	mu       sync.Mutex
	progress installer.Progress
	done     bool
	err      error
	path     string
	version  string
}

type jobView struct {
	Release installer.Release
	Dir     string
	installer.Progress
	Running   bool
	Err       string
	Path      string
	Version   string
	Restarted bool
}

func (j *job) view() jobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := jobView{Release: j.release, Dir: j.dir, Progress: j.progress, Running: !j.done, Path: j.path, Version: j.version}
	if j.err != nil {
		v.Err = j.err.Error()
	}
	return v
}

func (s *Server) currentJob() *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.job
}

type welcomeData struct {
	MemoOK      bool
	MemoStatus  string
	MemoVersion string
	TrayVersion string
}

func (s *Server) wizardWelcome(w http.ResponseWriter, r *http.Request) {
	cfg, _, _ := s.loadConfig()
	bin, version, err := s.memo(r.Context(), cfg)
	s.render(w, "wizard_welcome.html", page{Title: "Set up memo-mcp", Step: 1, Data: welcomeData{
		MemoOK: err == nil, MemoStatus: memoLine(bin, version, err), MemoVersion: version, TrayVersion: s.d.TrayVersion}})
}

type installData struct {
	Release   *installer.Release
	Archive   installer.Asset
	HasAsset  bool
	Err       string
	Installed string // version of the memo-mcp found now
	MemoPath  string
	Current   bool // the installed version is the latest
	Dir       string
}

func (s *Server) wizardInstall(w http.ResponseWriter, r *http.Request) {
	if j := s.currentJob(); j != nil && j.view().Running {
		http.Redirect(w, r, "/wizard/progress", http.StatusSeeOther)
		return
	}
	d := installData{Dir: "~/.local/bin"}
	rel, err := s.release(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		d.Err = err.Error()
	} else {
		d.Release = &rel
		d.Archive, d.HasAsset = rel.Asset(rel.ArchiveName(s.d.GOARCH))
	}
	cfg, _, _ := s.loadConfig()
	if bin, version, err := s.memo(r.Context(), cfg); err == nil {
		d.MemoPath, d.Installed = bin, version
		d.Current = d.Release != nil && strings.TrimPrefix(version, "v") == d.Release.Version()
	}
	s.render(w, "wizard_install.html", page{Title: "Install memo-mcp", Step: 2, Data: d})
}

func (s *Server) startInstall(w http.ResponseWriter, r *http.Request) {
	dir := expandHome(strings.TrimSpace(r.FormValue("dir")))
	if !filepath.IsAbs(dir) {
		http.Redirect(w, r, "/wizard/install", http.StatusSeeOther)
		return
	}
	rel, err := s.release(r.Context(), false)
	if err != nil {
		http.Redirect(w, r, "/wizard/install", http.StatusSeeOther)
		return
	}
	s.mu.Lock()
	if s.job != nil && !s.job.done {
		s.mu.Unlock()
		http.Redirect(w, r, "/wizard/progress", http.StatusSeeOther)
		return
	}
	j := &job{release: rel, dir: dir, progress: installer.Progress{Phase: installer.Downloading}}
	s.job = j
	s.mu.Unlock()
	go s.run(j)
	http.Redirect(w, r, "/wizard/progress", http.StatusSeeOther)
}

// run installs and, on success, points tray.json at the new memo-mcp.
func (s *Server) run(j *job) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	path, err := s.d.Releases.Install(ctx, j.release, s.d.GOARCH, j.dir, func(p installer.Progress) {
		j.mu.Lock()
		j.progress = p
		j.mu.Unlock()
	})
	version := ""
	if err == nil {
		version, _ = s.d.MemoVersion(ctx, path)
		err = s.useMemo(path)
	}
	j.mu.Lock()
	j.done, j.err, j.path, j.version = true, err, path, version
	j.mu.Unlock()
	if err != nil {
		s.d.Logf("install memo-mcp %s: %v", j.release.Tag, err)
		return
	}
	s.d.Logf("installed memo-mcp %s at %s", j.release.Tag, path)
	s.d.Notify(Event{Kind: ConfigSaved})
}

// useMemo makes memo-tray run the memo-mcp at path from now on.
func (s *Server) useMemo(path string) error {
	_, err := config.Update(config.Path(s.d.Home), func(c *config.Config) error {
		c.MemoBinary = path
		return nil
	})
	return err
}

func (s *Server) wizardProgress(w http.ResponseWriter, r *http.Request) {
	j := s.currentJob()
	if j == nil {
		http.Redirect(w, r, "/wizard/install", http.StatusSeeOther)
		return
	}
	v := j.view()
	v.Restarted = r.URL.Query().Get("restarted") == "1"
	refresh := 0
	if v.Running {
		refresh = 1
	}
	s.render(w, "wizard_progress.html", page{Title: "Install memo-mcp", Step: 2, Refresh: refresh, Data: v})
}

func (s *Server) restartServers(w http.ResponseWriter, r *http.Request) {
	s.d.Notify(Event{Kind: RestartServers})
	http.Redirect(w, r, "/wizard/progress?restarted=1", http.StatusSeeOther)
}

type kbData struct {
	Name         string
	KBs          []string
	Models       []Model
	DefaultModel string
	Model        string
	Auth         string
	Autostart    bool
	MemoOK       bool
	Err          string
}

func (s *Server) wizardKB(w http.ResponseWriter, r *http.Request) {
	cfg, _, _ := s.loadConfig()
	d := kbData{Name: "default", Auth: "none", Autostart: true, Model: cfg.Env["MEMO_MODEL"]}
	d.KBs, _ = home.KBs(s.d.Home)
	if len(d.KBs) > 0 {
		d.Name = d.KBs[0]
	}
	if len(cfg.Servers) > 0 {
		d.Name, d.Auth, d.Autostart = cfg.Servers[0].KB, authName(cfg.Servers[0].Auth), cfg.Servers[0].Autostart
	}
	s.fillModels(r.Context(), cfg, &d)
	s.render(w, "wizard_kb.html", page{Title: "Your knowledge base", Step: 3, Data: d})
}

func (s *Server) fillModels(ctx context.Context, cfg *config.Config, d *kbData) {
	bin, _, err := s.memo(ctx, cfg)
	d.MemoOK = err == nil
	if err != nil {
		return
	}
	d.Models, _ = s.d.Models(ctx, bin)
	for _, m := range d.Models {
		if m.Default {
			d.DefaultModel = m.ID
		}
	}
}

func (s *Server) saveKB(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	cfg, _, _ := s.loadConfig()
	fail := func(msg string) {
		d := kbData{Name: name, Auth: r.FormValue("auth"), Autostart: r.FormValue("autostart") == "on", Model: r.FormValue("model"), Err: msg}
		d.KBs, _ = home.KBs(s.d.Home)
		s.fillModels(r.Context(), cfg, &d)
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.render(w, "wizard_kb.html", page{Title: "Your knowledge base", Step: 3, Data: d})
	}
	if !home.ValidName(name) {
		fail("Use letters, digits, '.', '_' or '-' for the name (at most 64).")
		return
	}
	_, err := config.Update(config.Path(s.d.Home), func(c *config.Config) error {
		if c.Find(name) == nil {
			if _, _, err := c.AddrFor(name, s.d.PortFree); err != nil {
				return err
			}
		}
		srv := c.Find(name)
		srv.Auth, srv.Autostart = authOf(r.FormValue("auth")), r.FormValue("autostart") == "on"
		if m := strings.TrimSpace(r.FormValue("model")); m != "" {
			if c.Env == nil {
				c.Env = map[string]string{}
			}
			c.Env["MEMO_MODEL"] = m
		} else {
			delete(c.Env, "MEMO_MODEL")
		}
		return nil
	})
	if err != nil {
		fail(err.Error())
		return
	}
	s.d.Notify(Event{Kind: ConfigSaved})
	s.d.Notify(Event{Kind: StartServer, KB: name})
	http.Redirect(w, r, "/wizard/done?"+url.Values{"kb": {name}}.Encode(), http.StatusSeeOther)
}

type doneData struct {
	KB      string
	URL     string
	Auth    string
	Command string
	Copied  bool
}

func (s *Server) wizardDone(w http.ResponseWriter, r *http.Request) {
	d, ok := s.doneFor(r.URL.Query().Get("kb"))
	if !ok {
		http.Redirect(w, r, "/wizard/kb", http.StatusSeeOther)
		return
	}
	d.Copied = r.URL.Query().Get("copied") == "1"
	s.render(w, "wizard_done.html", page{Title: "Connect your agents", Step: 4, Data: d})
}

func (s *Server) doneFor(kb string) (doneData, bool) {
	cfg, _, _ := s.loadConfig()
	srv := cfg.Find(kb)
	if srv == nil {
		return doneData{}, false
	}
	base := "http://" + srv.Addr
	token := filepath.Join(s.d.Home, "http-token")
	return doneData{KB: kb, URL: base + "/mcp", Auth: authName(srv.Auth),
		Command: menu.ClaudeAddCommand(kb, base, srv.Auth, token, token)}, true
}

func (s *Server) copyCommand(w http.ResponseWriter, r *http.Request) {
	kb := r.FormValue("kb")
	d, ok := s.doneFor(kb)
	if !ok {
		http.Redirect(w, r, "/wizard/kb", http.StatusSeeOther)
		return
	}
	if err := s.d.Copy(d.Command); err != nil {
		s.d.Logf("copy: %v", err)
	}
	http.Redirect(w, r, "/wizard/done?"+url.Values{"kb": {kb}, "copied": {"1"}}.Encode(), http.StatusSeeOther)
}

// release returns the newest release, asking GitHub at most every
// releaseFor unless refresh is set.
func (s *Server) release(ctx context.Context, refresh bool) (installer.Release, error) {
	s.mu.Lock()
	if !refresh && s.latest != nil && time.Since(s.latestAt) < releaseFor {
		rel := *s.latest
		s.mu.Unlock()
		return rel, nil
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rel, err := s.d.Releases.Latest(ctx)
	if err != nil {
		return rel, err
	}
	s.mu.Lock()
	s.latest, s.latestAt = &rel, time.Now()
	s.mu.Unlock()
	return rel, nil
}
