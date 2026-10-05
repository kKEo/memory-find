package httpauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// CookieName is the browser session cookie.
	CookieName = "memo_session"
	cookieAge  = 30 * 24 * time.Hour
	// codeTTL is how long a printed one-time login link stays valid.
	codeTTL = 15 * time.Minute
	// failDelay slows down token guessing through the login form.
	failDelay = 500 * time.Millisecond
)

// Guard checks every request against one token.
type Guard struct {
	token   []byte
	session string // cookie value: HMAC(token), so rotation logs everyone out
	secure  bool   // Secure cookies (TLS here or at a proxy)
	now     func() time.Time

	mu    sync.Mutex
	codes map[string]time.Time // one-time login codes -> expiry
}

// New builds a guard. secure marks cookies Secure; set it whenever the
// browser reaches the server over HTTPS (directly or through a proxy).
func New(token string, secure bool) *Guard {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("memo-mcp ui session v1"))
	return &Guard{
		token:   []byte(token),
		session: base64.RawURLEncoding.EncodeToString(mac.Sum(nil)),
		secure:  secure,
		now:     time.Now,
		codes:   map[string]time.Time{},
	}
}

// LoginCode returns a single-use code for /login?code=..., valid codeTTL.
// The server prints it at startup so a browser can log in without the
// token being pasted or put in a URL.
func (g *Guard) LoginCode() string {
	code := NewToken()
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for c, exp := range g.codes {
		if now.After(exp) {
			delete(g.codes, c)
		}
	}
	g.codes[code] = now.Add(codeTTL)
	return code
}

func (g *Guard) useCode(code string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	exp, ok := g.codes[code]
	if !ok {
		return false
	}
	delete(g.codes, code)
	return !g.now().After(exp)
}

func (g *Guard) tokenOK(s string) bool {
	return subtle.ConstantTimeCompare([]byte(s), g.token) == 1
}

// Authorized reports whether r carries the bearer token or a valid session
// cookie.
func (g *Guard) Authorized(r *http.Request) bool {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, cred, ok := strings.Cut(h, " ")
		return ok && strings.EqualFold(scheme, "Bearer") && g.tokenOK(strings.TrimSpace(cred))
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return subtle.ConstantTimeCompare([]byte(c.Value), []byte(g.session)) == 1
	}
	return false
}

// Wrap guards next. /login and /logout are served here; /style.css stays
// open so the login page is styled. Browsers without a session are sent
// to /login; everything else gets 401 with a Bearer challenge.
func (g *Guard) Wrap(next http.Handler) http.Handler {
	login := http.NewCrossOriginProtection().Handler(http.HandlerFunc(g.login))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			login.ServeHTTP(w, r)
			return
		case "/logout":
			g.setCookie(w, "", -1)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		case "/style.css":
			next.ServeHTTP(w, r)
			return
		}
		if g.Authorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if wantsHTML(r) && r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/mcp") {
			http.Redirect(w, r, "/login?next="+safeNext(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="memo-mcp"`)
		http.Error(w, "unauthorized: send Authorization: Bearer <token> (memo-mcp http-token prints it)", http.StatusUnauthorized)
	})
}

func wantsHTML(r *http.Request) bool {
	return r.Header.Get("Authorization") == "" && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// safeNext keeps a redirect target on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, "/login") {
		return "/"
	}
	return next
}

func (g *Guard) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: g.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (g *Guard) login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	next := safeNext(r.FormValue("next"))
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if code := r.URL.Query().Get("code"); code != "" {
			if g.useCode(code) {
				g.setCookie(w, g.session, int(cookieAge.Seconds()))
				http.Redirect(w, r, next, http.StatusSeeOther)
				return
			}
			time.Sleep(failDelay)
			w.WriteHeader(http.StatusUnauthorized)
			renderLogin(w, next, "That login link has expired or was already used. Paste the token instead, or restart the server for a new link.")
			return
		}
		renderLogin(w, next, "")
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err == nil && g.tokenOK(strings.TrimSpace(r.PostForm.Get("token"))) {
			g.setCookie(w, g.session, int(cookieAge.Seconds()))
			http.Redirect(w, r, safeNext(r.PostForm.Get("next")), http.StatusSeeOther)
			return
		}
		time.Sleep(failDelay)
		w.WriteHeader(http.StatusUnauthorized)
		renderLogin(w, next, "Wrong token.")
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

var loginTmpl = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Log in · memo-mcp</title><link rel="stylesheet" href="/style.css"></head><body>
<header><span class="brand">memo-mcp</span></header><main>
<h1>Log in</h1>
{{if .Error}}<p class="warn">{{.Error}}</p>{{end}}
<p class="muted">Use the login link the server printed at startup, or paste the token: run <code>memo-mcp http-token</code> on the server machine.</p>
<form method="post" action="/login"><input type="hidden" name="next" value="{{.Next}}">
<input type="password" name="token" autocomplete="current-password" placeholder="token" required autofocus> <button>Log in</button></form>
</main></body></html>`))

func renderLogin(w http.ResponseWriter, next, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTmpl.Execute(w, struct{ Next, Error string }{next, msg})
}
