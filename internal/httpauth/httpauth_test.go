package httpauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "http-token")
	tok, created, err := LoadOrCreate(path)
	if err != nil || !created || len(tok) < minTokenLen {
		t.Fatalf("create: %q %v %v", tok, created, err)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("mode %o, want 600", info.Mode().Perm())
		}
	}
	again, created, err := LoadOrCreate(path)
	if err != nil || created || again != tok {
		t.Fatalf("reload: %q %v %v", again, created, err)
	}
	rotated, err := Rotate(path)
	if err != nil || rotated == tok {
		t.Fatalf("rotate: %v", err)
	}
	if got, _ := Load(path); got != rotated {
		t.Errorf("after rotate Load = %q", got)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("world-readable token accepted: %v", err)
		}
	}
	short := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(short, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(short); err == nil {
		t.Error("short token accepted")
	}
}

const testToken = "0123456789abcdef0123456789abcdef-test"

func guarded() (*Guard, http.Handler) {
	g := New(testToken, true)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("inside " + r.URL.Path)) })
	return g, g.Wrap(ok)
}

func do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestBearer(t *testing.T) {
	_, h := guarded()
	for _, c := range []struct {
		header string
		want   int
	}{
		{"Bearer " + testToken, 200},
		{"bearer " + testToken, 200},
		{"Bearer wrong", 401},
		{"Basic " + testToken, 401},
		{"", 401},
	} {
		r := httptest.NewRequest("POST", "/mcp", nil)
		if c.header != "" {
			r.Header.Set("Authorization", c.header)
		}
		rec := do(h, r)
		if rec.Code != c.want {
			t.Errorf("%q: %d, want %d", c.header, rec.Code, c.want)
		}
		if rec.Code == 401 && !strings.Contains(rec.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("%q: no Bearer challenge", c.header)
		}
	}
}

func TestBrowserLoginWithToken(t *testing.T) {
	_, h := guarded()
	r := httptest.NewRequest("GET", "/live?x=1", nil)
	r.Header.Set("Accept", "text/html")
	rec := do(h, r)
	if rec.Code != 303 || rec.Header().Get("Location") != "/login?next=/live?x=1" {
		t.Fatalf("browser not sent to login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(h, httptest.NewRequest("GET", "/style.css", nil)); rec.Code != 200 {
		t.Errorf("style.css guarded: %d", rec.Code)
	}
	if rec := do(h, httptest.NewRequest("GET", "/login", nil)); rec.Code != 200 || !strings.Contains(rec.Body.String(), `type="password"`) || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("login page: %d", rec.Code)
	}

	post := func(token, next string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"token": {token}, "next": {next}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return do(h, r)
	}
	start := time.Now()
	if rec := post("wrong", "/"); rec.Code != 401 || len(rec.Result().Cookies()) != 0 || time.Since(start) < failDelay {
		t.Errorf("wrong token: %d, cookies %v", rec.Code, rec.Result().Cookies())
	}
	rec = post(testToken, "//evil.example/")
	if rec.Code != 303 || rec.Header().Get("Location") != "/" {
		t.Errorf("open redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || strings.Contains(cookies[0].Value, testToken) {
		t.Fatalf("cookie = %+v", cookies)
	}
	r = httptest.NewRequest("GET", "/live", nil)
	r.AddCookie(cookies[0])
	if rec := do(h, r); rec.Code != 200 || rec.Body.String() != "inside /live" {
		t.Errorf("cookie not accepted: %d", rec.Code)
	}
	// A cookie made with another token (after rotation) is refused.
	r = httptest.NewRequest("GET", "/live", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: New("another-token-another-token-another", true).session})
	if rec := do(h, r); rec.Code != 401 {
		t.Errorf("foreign cookie accepted: %d", rec.Code)
	}
	if rec := do(h, httptest.NewRequest("GET", "/logout", nil)); rec.Code != 303 || rec.Result().Cookies()[0].MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie")
	}
}

func TestCrossSiteLoginRefused(t *testing.T) {
	_, h := guarded()
	r := httptest.NewRequest("POST", "/login", strings.NewReader("token="+testToken))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := do(h, r); rec.Code != 403 {
		t.Errorf("cross-site login: %d, want 403", rec.Code)
	}
}

func TestOneTimeLoginCode(t *testing.T) {
	g, h := guarded()
	code := g.LoginCode()
	rec := do(h, httptest.NewRequest("GET", "/login?code="+code+"&next=/ingest", nil))
	if rec.Code != 303 || rec.Header().Get("Location") != "/ingest" || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("code login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(h, httptest.NewRequest("GET", "/login?code="+code, nil)); rec.Code != 401 || len(rec.Result().Cookies()) != 0 {
		t.Errorf("code reused: %d", rec.Code)
	}
	g.now = func() time.Time { return time.Now().Add(-codeTTL - time.Minute) }
	old := g.LoginCode()
	g.now = time.Now
	if rec := do(h, httptest.NewRequest("GET", "/login?code="+old, nil)); rec.Code != 401 {
		t.Errorf("expired code accepted: %d", rec.Code)
	}
}

// An app holding the token mints single-use login links for a browser
// window; a browser session (cookie) cannot, and the token never appears
// in the link.
func TestLoginLink(t *testing.T) {
	g, h := guarded()
	link := func(method, auth, next string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/login-link?next="+url.QueryEscape(next), nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		return do(h, r)
	}
	if rec := link("GET", "Bearer "+testToken, "/live", nil); rec.Code != 405 || rec.Header().Get("Allow") != "POST" {
		t.Errorf("GET: %d, want 405", rec.Code)
	}
	cookie := &http.Cookie{Name: CookieName, Value: g.session}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"no auth":      link("POST", "", "/live", nil),
		"cookie only":  link("POST", "", "/live", cookie),
		"wrong bearer": link("POST", "Bearer wrong", "/live", cookie),
	} {
		if rec.Code != 401 {
			t.Errorf("%s: %d, want 401", name, rec.Code)
		}
	}

	rec := link("POST", "Bearer "+testToken, "/live", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bearer: %d %v", rec.Code, rec.Header())
	}
	var ll LoginLink
	if err := json.Unmarshal(rec.Body.Bytes(), &ll); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(ll.URL)
	if err != nil || u.Path != "/login" || u.Host != "" || u.Query().Get("next") != "/live" || u.Query().Get("code") == "" ||
		strings.Contains(ll.URL, testToken) || time.Until(ll.ExpiresAt) > codeTTL || time.Until(ll.ExpiresAt) < codeTTL-time.Minute {
		t.Fatalf("link = %+v", ll)
	}
	if rec := do(h, httptest.NewRequest("GET", ll.URL, nil)); rec.Code != 303 || rec.Header().Get("Location") != "/live" || len(rec.Result().Cookies()) != 1 {
		t.Fatalf("logging in with the link: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := do(h, httptest.NewRequest("GET", ll.URL, nil)); rec.Code != 401 {
		t.Errorf("link reused: %d", rec.Code)
	}

	rec = link("POST", "Bearer "+testToken, "//evil.example/", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &ll); err != nil {
		t.Fatal(err)
	}
	if u, _ := url.Parse(ll.URL); u.Query().Get("next") != "/" {
		t.Errorf("open redirect through next: %q", ll.URL)
	}

	r := httptest.NewRequest("POST", "/login-link", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := do(h, r); rec.Code != 403 {
		t.Errorf("cross-site: %d, want 403", rec.Code)
	}
}
