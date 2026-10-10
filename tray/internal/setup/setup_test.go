package setup

import (
	"context"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/installer"
)

type fakeReleases struct {
	latestErr  error
	installErr error
	gate       chan struct{} // when set, Install waits for it to close
	dirs       []string
}

func (f *fakeReleases) Latest(context.Context) (installer.Release, error) {
	if f.latestErr != nil {
		return installer.Release{}, f.latestErr
	}
	return installer.Release{Tag: "v9.9.9", PageURL: "https://github.com/kKEo/memory-find/releases/tag/v9.9.9", Published: time.Now(),
		Assets: []installer.Asset{{Name: "memo-mcp_9.9.9_darwin_arm64.tar.gz", URL: "https://example.invalid/a", Size: 9 << 20}}}, nil
}

func (f *fakeReleases) Install(_ context.Context, rel installer.Release, goarch, dir string, progress func(installer.Progress)) (string, error) {
	f.dirs = append(f.dirs, dir)
	progress(installer.Progress{Phase: installer.Downloading, Done: 3 << 20, Total: 9 << 20})
	if f.gate != nil {
		<-f.gate
	}
	if f.installErr != nil {
		return "", f.installErr
	}
	path := filepath.Join(dir, "memo-mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, []byte("#!/bin/sh\necho 'memo-mcp 9.9.9'\n"), 0o755)
}

type fakeLogin struct {
	on  bool
	err error
}

func (f *fakeLogin) Enabled() bool { return f.on }
func (f *fakeLogin) Enable() error {
	if f.err != nil {
		return f.err
	}
	f.on = true
	return nil
}
func (f *fakeLogin) Disable() error { f.on = false; return nil }

type harness struct {
	t      *testing.T
	s      *Server
	c      *http.Client
	home   string
	rel    *fakeReleases
	login  *fakeLogin
	memoOK bool
	mu     sync.Mutex
	events []Event
	copied []string
	opened []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, home: t.TempDir(), rel: &fakeReleases{}, login: &fakeLogin{}, memoOK: true}
	s, err := New(Deps{
		Home:        h.home,
		TrayVersion: "test",
		GOARCH:      "arm64",
		Releases:    h.rel,
		FindMemo: func(configured string) (string, error) {
			if configured != "" {
				if _, err := os.Stat(configured); err != nil {
					return "", errors.New("memo_binary in tray.json is not an executable: " + configured)
				}
				return configured, nil
			}
			if !h.memoOK {
				return "", errors.New("memo-mcp not found: install it, or set memo_binary in tray.json")
			}
			return "/opt/fake/memo-mcp", nil
		},
		MemoVersion: func(context.Context, string) (string, error) { return "1.4.0", nil },
		Models: func(context.Context, string) ([]Model, error) {
			return []Model{{ID: "granite-small-r2", Note: "IBM; 47M params", Default: true}, {ID: "potion", Note: "instant; weaker"}}, nil
		},
		Login:    h.login,
		PortFree: func(string) bool { return true },
		Copy:     func(s string) error { h.copied = append(h.copied, s); return nil },
		OpenFile: func(p, how string) error { h.opened = append(h.opened, how+":"+p); return nil },
		Notify:   func(e Event) { h.mu.Lock(); h.events = append(h.events, e); h.mu.Unlock() },
		Logf:     func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Listen(ln)
	t.Cleanup(func() { _ = s.Close() })
	jar, _ := cookiejar.New(nil)
	h.s, h.c = s, &http.Client{Jar: jar, Timeout: 5 * time.Second}
	return h
}

func (h *harness) enter(page string) string {
	h.t.Helper()
	code, body := h.get(h.s.Entry(page))
	if code != 200 {
		h.t.Fatalf("entering %s: %d\n%s", page, code, body)
	}
	return body
}

func (h *harness) get(u string) (int, string) {
	h.t.Helper()
	resp, err := h.c.Get(u)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (h *harness) post(path string, form url.Values) (int, string) {
	h.t.Helper()
	resp, err := h.c.PostForm(h.s.Origin()+path, form)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (h *harness) cfg() *config.Config {
	h.t.Helper()
	c, err := config.Load(config.Path(h.home))
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

func (h *harness) kinds() []EventKind {
	h.mu.Lock()
	defer h.mu.Unlock()
	var k []EventKind
	for _, e := range h.events {
		k = append(k, e.Kind)
	}
	return k
}

var revField = regexp.MustCompile(`name="rev" value="([^"]+)"`)

func rev(t *testing.T, body string) string {
	t.Helper()
	m := revField.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no rev in the form:\n%s", body)
	}
	return m[1]
}

func TestAccessControl(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.get(h.s.Origin() + "/settings"); code != http.StatusForbidden {
		t.Errorf("without the cookie: %d", code)
	}
	if code, _ := h.get(h.s.Origin() + "/enter?k=wrong&next=/settings"); code != http.StatusForbidden {
		t.Errorf("wrong secret: %d", code)
	}
	body := h.enter("/settings")
	if !strings.Contains(body, "<h1>Settings</h1>") {
		t.Fatalf("settings page:\n%s", body)
	}
	if code, _ := h.get(h.s.Origin() + "/settings"); code != 200 {
		t.Errorf("with the cookie: %d", code)
	}

	req, _ := http.NewRequest("GET", h.s.Origin()+"/settings", nil)
	req.Host = "evil.example"
	resp, err := h.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: %d", resp.StatusCode)
	}

	req, _ = http.NewRequest("POST", h.s.Origin()+"/settings", strings.NewReader("rev=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err = h.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site post: %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	if got := h.s.Entry("//evil.example"); !strings.Contains(got, "next=%2F%2Fevil.example") {
		t.Errorf("entry link = %s", got)
	}
	if safePath("//evil.example") != "/settings" || safePath("/wizard") != "/wizard" {
		t.Error("safePath lets an off-site redirect through")
	}
}

func TestSettingsSave(t *testing.T) {
	h := newHarness(t)
	old := &config.Config{Servers: []config.Server{{KB: "crportal", Addr: "127.0.0.1:8765", Env: map[string]string{"MEMO_PROFILE": "precise"}}}}
	if err := old.Save(config.Path(h.home)); err != nil {
		t.Fatal(err)
	}
	body := h.enter("/settings")
	for _, want := range []string{`value="crportal"`, `Default (granite-small-r2)`, `potion — instant`, "memo-mcp 1.4.0 at /opt/fake/memo-mcp"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page lacks %q", want)
		}
	}
	bin := filepath.Join(t.TempDir(), "memo-mcp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, body := h.post("/settings", url.Values{
		"rev": {rev(t, body)}, "login": {"on"}, "memo_mode": {"path"}, "memo_path": {bin}, "model": {"potion"},
		"servers": {"1"}, "kb_0": {"crportal"}, "addr_0": {"127.0.0.1:8770"}, "auth_0": {"token"}, "autostart_0": {"on"},
		"new_kb": {"work"}, "new_auth": {"none"}, "new_autostart": {"on"}, "env": {"MEMO_QUERY_LOG=1\n# comment\n"}, "debug": {"on"},
	})
	if code != 200 || !strings.Contains(body, "Settings saved.") {
		t.Fatalf("save: %d\n%s", code, body)
	}
	c := h.cfg()
	if c.MemoBinary != bin || c.Env["MEMO_MODEL"] != "potion" || c.Env["MEMO_QUERY_LOG"] != "1" || c.StopsOnQuit() || !c.Debug {
		t.Errorf("saved config = %+v", c)
	}
	cr, work := c.Find("crportal"), c.Find("work")
	if cr == nil || cr.Addr != "127.0.0.1:8770" || cr.Auth != "token" || !cr.Autostart || cr.Env["MEMO_PROFILE"] != "precise" {
		t.Errorf("crportal = %+v", cr)
	}
	if work == nil || work.Addr != "127.0.0.1:8765" || work.Auth != "" || !work.Autostart {
		t.Errorf("work = %+v", work)
	}
	if !h.login.on || !slices.Contains(h.kinds(), ConfigSaved) {
		t.Errorf("login %v, events %v", h.login.on, h.kinds())
	}

	// Removing a server and going back to automatic discovery.
	_, body = h.get(h.s.Origin() + "/settings")
	h.post("/settings", url.Values{"rev": {rev(t, body)}, "memo_mode": {"auto"}, "stop_on_quit": {"on"},
		"servers": {"2"}, "kb_0": {"crportal"}, "addr_0": {"127.0.0.1:8770"}, "remove_0": {"on"}, "kb_1": {"work"}, "addr_1": {"127.0.0.1:8765"}})
	c = h.cfg()
	if c.MemoBinary != "" || len(c.Servers) != 1 || c.Servers[0].KB != "work" || !c.StopsOnQuit() || c.Env != nil || h.login.on {
		t.Errorf("after the second save: %+v (login %v)", c, h.login.on)
	}
}

func TestSettingsRefusesBadInput(t *testing.T) {
	h := newHarness(t)
	body := h.enter("/settings")
	before, _ := os.ReadFile(config.Path(h.home))
	for _, c := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"new_kb": {"a b"}}, "not a valid knowledge-base name"},
		{url.Values{"env": {"MEMO_HTTP_AUTH=none"}}, "set by memo-tray"},
		{url.Values{"env": {"MEMO_MODEL=potion"}}, "embedding model with the field above"},
		{url.Values{"env": {"no equals sign"}}, "NAME=value"},
		{url.Values{"memo_mode": {"path"}, "memo_path": {"relative/memo-mcp"}}, "full path"},
		{url.Values{"memo_mode": {"path"}, "memo_path": {"/does/not/exist"}}, "not an executable"},
		{url.Values{"new_kb": {"x"}, "new_addr": {"10.0.0.1:1"}}, "loopback"},
	} {
		c.form.Set("rev", rev(t, body))
		code, got := h.post("/settings", c.form)
		if code != http.StatusUnprocessableEntity || !strings.Contains(got, c.want) || !strings.Contains(got, "Nothing was saved") {
			t.Errorf("%v: %d, want an error containing %q", c.form, code, c.want)
		}
	}
	if after, _ := os.ReadFile(config.Path(h.home)); string(after) != string(before) {
		t.Error("an invalid form changed tray.json")
	}
	h.login.err = errors.New("no LaunchAgents folder")
	code, got := h.post("/settings", url.Values{"rev": {rev(t, body)}, "login": {"on"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(got, "Launch at login: no LaunchAgents folder") {
		t.Errorf("login failure not shown: %d", code)
	}
}

func TestSettingsConflict(t *testing.T) {
	h := newHarness(t)
	body := h.enter("/settings")
	time.Sleep(10 * time.Millisecond)
	meanwhile := &config.Config{Servers: []config.Server{{KB: "started", Addr: "127.0.0.1:8765"}}}
	if err := meanwhile.Save(config.Path(h.home)); err != nil {
		t.Fatal(err)
	}
	code, got := h.post("/settings", url.Values{"rev": {rev(t, body)}, "new_kb": {"other"}})
	if code != http.StatusConflict || !strings.Contains(got, "changed while this form was open") {
		t.Fatalf("stale form: %d", code)
	}
	if c := h.cfg(); len(c.Servers) != 1 || c.Servers[0].KB != "started" {
		t.Errorf("a stale form overwrote tray.json: %+v", c)
	}
	if !strings.Contains(got, `value="started"`) {
		t.Error("the conflict page does not show the current settings")
	}
}

func TestWizardInstall(t *testing.T) {
	h := newHarness(t)
	h.memoOK = false
	body := h.enter("/wizard")
	if !strings.Contains(body, "Install memo-mcp") || !strings.Contains(body, "memo-mcp not found") {
		t.Fatalf("welcome:\n%s", body)
	}
	_, body = h.get(h.s.Origin() + "/wizard/install")
	for _, want := range []string{"v9.9.9", "memo-mcp_9.9.9_darwin_arm64.tar.gz", "9.0 MB", "not installed", "Download and install", `value="~/.local/bin"`} {
		if !strings.Contains(body, want) {
			t.Errorf("install page lacks %q", want)
		}
	}
	h.rel.gate = make(chan struct{})
	dir := filepath.Join(t.TempDir(), "bin")
	code, body := h.post("/wizard/install", url.Values{"dir": {dir}})
	if code != 200 || !strings.Contains(body, "Installing memo-mcp 9.9.9") || !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "3.0 MB of 9.0 MB") {
		t.Fatalf("progress while running: %d\n%s", code, body)
	}
	close(h.rel.gate)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(body, "is installed") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		_, body = h.get(h.s.Origin() + "/wizard/progress")
	}
	if !strings.Contains(body, "memo-mcp 1.4.0 is installed") || strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatalf("progress when done:\n%s", body)
	}
	if c := h.cfg(); c.MemoBinary != filepath.Join(dir, "memo-mcp") {
		t.Errorf("tray.json memo_binary = %q", c.MemoBinary)
	}
	h.post("/wizard/restart", nil)
	if k := h.kinds(); !slices.Equal(k, []EventKind{ConfigSaved, RestartServers}) {
		t.Errorf("events = %v", k)
	}
	if len(h.rel.dirs) != 1 || h.rel.dirs[0] != dir {
		t.Errorf("installed into %v", h.rel.dirs)
	}
}

func TestWizardInstallFailures(t *testing.T) {
	h := newHarness(t)
	h.rel.latestErr = errors.New("offline")
	body := h.enter("/wizard/install")
	if !strings.Contains(body, "Could not look up the latest release: offline") {
		t.Errorf("offline install page:\n%s", body)
	}
	h.rel.latestErr = nil
	h.rel.installErr = installer.ErrChecksum
	_, body = h.post("/wizard/install", url.Values{"dir": {t.TempDir()}})
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(body, "did not finish") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		_, body = h.get(h.s.Origin() + "/wizard/progress")
	}
	if !strings.Contains(body, "does not match the release") {
		t.Errorf("failure page:\n%s", body)
	}
	if _, err := os.Stat(config.Path(h.home)); err == nil {
		t.Error("a failed install wrote tray.json")
	}
}

func TestWizardKnowledgeBase(t *testing.T) {
	h := newHarness(t)
	body := h.enter("/wizard/kb")
	if !strings.Contains(body, `value="default"`) || !strings.Contains(body, "Save and start") {
		t.Fatalf("kb page:\n%s", body)
	}
	code, body := h.post("/wizard/kb", url.Values{"name": {"a/b"}})
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "letters, digits") {
		t.Errorf("bad name: %d", code)
	}
	code, body = h.post("/wizard/kb", url.Values{"name": {"work"}, "auth": {"token"}, "autostart": {"on"}, "model": {"potion"}})
	if code != 200 || !strings.Contains(body, "Connect your agents") {
		t.Fatalf("save: %d\n%s", code, body)
	}
	c := h.cfg()
	if s := c.Find("work"); s == nil || s.Addr != "127.0.0.1:8765" || s.Auth != "token" || !s.Autostart || c.Env["MEMO_MODEL"] != "potion" {
		t.Errorf("config = %+v", c)
	}
	h.mu.Lock()
	events := slices.Clone(h.events)
	h.mu.Unlock()
	if !slices.Equal(events, []Event{{Kind: ConfigSaved}, {Kind: StartServer, KB: "work"}}) {
		t.Errorf("events = %v", events)
	}
	want := `claude mcp add --transport http memo-work 'http://127.0.0.1:8765/mcp' --header "Authorization: Bearer $(memo-mcp http-token)"`
	if !strings.Contains(body, html.EscapeString(want)) || !strings.Contains(body, "http://127.0.0.1:8765/mcp") {
		t.Errorf("done page lacks the command:\n%s", body)
	}
	_, body = h.post("/wizard/copy", url.Values{"kb": {"work"}})
	if len(h.copied) != 1 || h.copied[0] != want || !strings.Contains(body, "Copied ✓") {
		t.Errorf("copied %q", h.copied)
	}
}

func TestParseModels(t *testing.T) {
	out := `id                 dim   licence              vectors    note
minilm             384   Apache-2.0           -          22M params; the journal's model and the latency floor.
granite-small-r2   384   Apache-2.0           0          IBM, 47M params, 384-d. Provisional favourite (OD-6).* (kb default)
gemma-256          256   Gemma Terms of Use   -          Google, 308M params; opt-in only.
potion             512   MIT                  -          model2vec lookup table: instant; weaker on paraphrase.

* = selected by MEMO_MODEL (or the registry default).`
	models := ParseModels(out)
	if len(models) != 4 {
		t.Fatalf("models = %+v", models)
	}
	g := models[1]
	if g.ID != "granite-small-r2" || !g.Default || strings.Contains(g.Note, "*") || strings.Contains(g.Note, "kb default") || models[0].Default {
		t.Errorf("granite = %+v", g)
	}
	if models[2].Licence != "Gemma Terms of Use" {
		t.Errorf("gemma licence = %q", models[2].Licence)
	}
	if l := models[0].Label(); l != "minilm — 22M params" {
		t.Errorf("label = %q", l)
	}
	if ParseModels("no table here") != nil {
		t.Error("garbage parsed as models")
	}
}

// An install finishing while the knowledge-base step is saved keeps both
// changes in tray.json.
func TestInstallAndKnowledgeBaseTogether(t *testing.T) {
	h := newHarness(t)
	h.enter("/wizard")
	h.rel.gate = make(chan struct{})
	dir := t.TempDir()
	h.post("/wizard/install", url.Values{"dir": {dir}})
	if code, body := h.post("/wizard/kb", url.Values{"name": {"work"}, "autostart": {"on"}}); code != 200 || !strings.Contains(body, "Connect your agents") {
		t.Fatalf("kb step: %d", code)
	}
	close(h.rel.gate)
	deadline := time.Now().Add(5 * time.Second)
	for h.s.currentJob().view().Running && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	c := h.cfg()
	if c.MemoBinary != filepath.Join(dir, "memo-mcp") || c.Find("work") == nil {
		t.Errorf("a write was lost: %+v", c)
	}
}
