// Package setup serves memors-tray's own pages to its windows: the wizard
// that downloads and installs memors-mcp and sets up a first knowledge base,
// and the settings form. Like memors-mcp's UI they are server-rendered forms
// with no JavaScript, served over HTTP on a loopback port. A window gets
// in through a link carrying a per-process secret (handed to it in
// process, never on a command line) and then holds a SameSite=Strict
// cookie; requests with another Host header, and cross-site form posts,
// are refused.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kKEo/memors/tray/internal/installer"
)

//go:embed templates/*.html style.css
var files embed.FS

// Releases finds and installs memors-mcp releases (*installer.Client).
type Releases interface {
	Latest(ctx context.Context) (installer.Release, error)
	Install(ctx context.Context, rel installer.Release, goarch, dir string, progress func(installer.Progress)) (string, error)
}

// LoginItem switches starting memors-tray at login (loginitem.Agent).
type LoginItem interface {
	Enabled() bool
	Enable() error
	Disable() error
}

// EventKind is something the menu-bar controller has to act on.
type EventKind int

// The events.
const (
	ConfigSaved    EventKind = iota // tray.json changed: reload it, look for memors-mcp again
	StartServer                     // start KB's server (unless it runs)
	RestartServers                  // restart the servers memors-tray started, to run a new memors-mcp
)

// Event is sent to the controller; it must not block.
type Event struct {
	Kind EventKind
	KB   string
}

// Deps is everything the pages reach outside themselves.
type Deps struct {
	Home          string
	TrayVersion   string
	GOARCH        string // the memors-mcp archive to install (arm64, amd64)
	Releases      Releases
	FindMemors    func(configured string) (string, error)
	MemorsVersion func(ctx context.Context, bin string) (string, error)
	Models        func(ctx context.Context, bin string) ([]Model, error)
	Login         LoginItem
	PortFree      func(addr string) bool
	Copy          func(text string) error
	OpenFile      func(path, how string) error
	Notify        func(Event)
	Logf          func(format string, args ...any)
}

// cookieName holds the secret once a window has come in.
const cookieName = "memors_tray"

// Server serves the pages.
type Server struct {
	d      Deps
	secret string
	tmpl   *template.Template

	mu       sync.Mutex
	host     string // the only accepted Host header: the listener's address
	hs       *http.Server
	job      *job
	latest   *installer.Release
	latestAt time.Time
}

// New prepares a server; Serve or Start runs it.
func New(d Deps) (*Server, error) {
	t, err := template.New("").Funcs(funcs).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{d: d, secret: rand.Text(), tmpl: t}, nil
}

// Start listens on a free loopback port and serves until Close.
func Start(d Deps) (*Server, error) {
	s, err := New(d)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.Listen(ln)
	return s, nil
}

// Listen serves on ln in the background until Close. Requests must name
// ln's address as their Host; Origin and Entry are valid on return.
func (s *Server) Listen(ln net.Listener) {
	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	s.host, s.hs = ln.Addr().String(), hs
	s.mu.Unlock()
	go func() {
		if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.d.Logf("setup pages: %v", err)
		}
	}()
}

// Close stops serving.
func (s *Server) Close() error {
	s.mu.Lock()
	hs := s.hs
	s.mu.Unlock()
	if hs == nil {
		return nil
	}
	return hs.Close()
}

// Origin is the base URL the pages are served from.
func (s *Server) Origin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "http://" + s.host
}

// Entry is the link a window opens to land on page (such as /settings).
func (s *Server) Entry(page string) string {
	return s.Origin() + "/enter?" + url.Values{"k": {s.secret}, "next": {page}}.Encode()
}

// Handler is the whole site.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /enter", s.enter)
	mux.HandleFunc("GET /style.css", s.css)

	app := http.NewServeMux()
	app.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/settings", http.StatusSeeOther) })
	app.HandleFunc("GET /settings", s.settingsPage)
	app.HandleFunc("POST /settings", s.saveSettings)
	app.HandleFunc("POST /settings/open", s.openFromSettings)
	app.HandleFunc("GET /wizard", s.wizardWelcome)
	app.HandleFunc("GET /wizard/install", s.wizardInstall)
	app.HandleFunc("POST /wizard/install", s.startInstall)
	app.HandleFunc("GET /wizard/progress", s.wizardProgress)
	app.HandleFunc("POST /wizard/restart", s.restartServers)
	app.HandleFunc("GET /wizard/kb", s.wizardKB)
	app.HandleFunc("POST /wizard/kb", s.saveKB)
	app.HandleFunc("GET /wizard/done", s.wizardDone)
	app.HandleFunc("POST /wizard/copy", s.copyCommand)
	mux.Handle("/", s.authed(http.NewCrossOriginProtection().Handler(app)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		host := s.host
		s.mu.Unlock()
		if host == "" || r.Host != host {
			http.Error(w, "unexpected Host header", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}

// enter trades the secret in the link for the session cookie.
func (s *Server) enter(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("k")), []byte(s.secret)) != 1 {
		http.Error(w, "Open this page from memors-tray's menu.", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.secret, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, safePath(r.URL.Query().Get("next")), http.StatusSeeOther)
}

func (s *Server) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.secret)) != 1 {
			http.Error(w, "Open this page from memors-tray's menu.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) css(w http.ResponseWriter, _ *http.Request) {
	b, _ := files.ReadFile("style.css")
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(b)
}

// page is what every template gets.
type page struct {
	Title   string
	Refresh int // seconds; 0 = no automatic reload
	Step    int // wizard step 1-4; 0 outside the wizard
	Data    any
}

func (s *Server) render(w http.ResponseWriter, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, p); err != nil {
		s.d.Logf("render %s: %v", name, err)
	}
}

// safePath keeps redirects on this site.
func safePath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/settings"
	}
	return p
}

var funcs = template.FuncMap{
	"mb": func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) },
	"pct": func(done, total int64) int64 {
		if total <= 0 {
			return 0
		}
		return done * 100 / total
	},
	"tilde": tilde,
	"hasModel": func(models []Model, id string) bool {
		for _, m := range models {
			if m.ID == id {
				return true
			}
		}
		return false
	},
	"date": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Local().Format("2 January 2006")
	},
}

// tilde shortens paths under the home folder to ~/... for display.
func tilde(s string) string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return strings.ReplaceAll(s, h+"/", "~/")
	}
	return s
}
