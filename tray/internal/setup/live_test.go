package setup

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/tray/internal/config"
	"github.com/kKEo/memory-find/tray/internal/installer"
	"github.com/kKEo/memory-find/tray/internal/locate"
)

// With MEMO_TRAY_LIVE_GITHUB=1, walk the whole assistant the way a window
// does, against the real latest release: install memo-mcp into a temporary
// folder, list its embedding models, save a knowledge base, get the
// command that connects Claude Code.
func TestLiveWizard(t *testing.T) {
	if os.Getenv("MEMO_TRAY_LIVE_GITHUB") != "1" {
		t.Skip("set MEMO_TRAY_LIVE_GITHUB=1 to download the real latest release")
	}
	homeDir := t.TempDir()
	var events []Event
	s, err := New(Deps{
		Home: homeDir, TrayVersion: "test", GOARCH: runtime.GOARCH,
		Releases: installer.New("memo-tray-test"), FindMemo: locate.MemoBinary, MemoVersion: locate.Version,
		Models: ListModels(homeDir), Login: &fakeLogin{}, PortFree: config.PortFree,
		Copy: func(string) error { return nil }, OpenFile: func(string, string) error { return nil },
		Notify: func(e Event) { events = append(events, e) }, Logf: t.Logf,
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Listen(ln)
	defer s.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: time.Minute}
	get := func(u string) string {
		resp, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	post := func(path string, f url.Values) string {
		resp, err := c.PostForm(s.Origin()+path, f)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	if body := get(s.Entry("/wizard/install")); !strings.Contains(body, "memo-mcp_") {
		t.Fatalf("install page:\n%s", body)
	}
	dir := filepath.Join(t.TempDir(), "bin")
	body := post("/wizard/install", url.Values{"dir": {dir}})
	deadline := time.Now().Add(3 * time.Minute)
	for strings.Contains(body, `http-equiv="refresh"`) && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		body = get(s.Origin() + "/wizard/progress")
	}
	if !strings.Contains(body, "is installed") {
		t.Fatalf("install did not finish:\n%s", body)
	}
	body = get(s.Origin() + "/wizard/kb")
	if !strings.Contains(body, `<option value="potion"`) {
		t.Fatalf("models were not listed from the installed memo-mcp:\n%s", body)
	}
	body = post("/wizard/kb", url.Values{"name": {"live"}, "auth": {"none"}, "autostart": {"on"}})
	if !strings.Contains(body, "claude mcp add --transport http memo-live") {
		t.Fatalf("done page:\n%s", body)
	}
	cfg, err := config.Load(config.Path(homeDir))
	if err != nil || cfg.MemoBinary != filepath.Join(dir, "memo-mcp") || cfg.Find("live") == nil {
		t.Fatalf("tray.json = %+v, %v", cfg, err)
	}
	if v, err := locate.Version(context.Background(), cfg.MemoBinary); err != nil {
		t.Fatal(err)
	} else {
		t.Logf("installed memo-mcp %s; events %v", v, events)
	}
}
