package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/httpauth"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/retrieve"
	"github.com/kKEo/memory-find/internal/server"
)

func TestServeHTTPRefusesRemoteAddress(t *testing.T) {
	t.Setenv("MEMO_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	code := Main(context.Background(), "test", []string{"serve", "--http", "0.0.0.0:0"}, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), "TLS") {
		t.Fatalf("plain-HTTP remote --http accepted: %d %s", code, errOut.String())
	}
}

func TestPrepareHTTPRules(t *testing.T) {
	pki := newTestPKI(t)
	home := t.TempDir()
	for _, c := range []struct {
		name string
		f    httpFlags
		want string // substring of the error; "" = accepted
	}{
		{"loopback, no auth", httpFlags{addr: "127.0.0.1:0"}, ""},
		{"loopback, token", httpFlags{addr: "127.0.0.1:0", auth: "token"}, ""},
		{"bad auth mode", httpFlags{addr: "127.0.0.1:0", auth: "basic"}, "token or none"},
		{"remote plain HTTP", httpFlags{addr: "0.0.0.0:0", allowRemote: true, publicURL: "https://box:1"}, "plain HTTP"},
		{"remote TLS, no public URL", httpFlags{addr: "0.0.0.0:0", allowRemote: true, tlsCert: pki.serverCert, tlsKey: pki.serverKey}, "--public-url"},
		{"remote TLS, auth none", httpFlags{addr: "0.0.0.0:0", allowRemote: true, tlsCert: pki.serverCert, tlsKey: pki.serverKey, publicURL: "https://box:1", auth: "none"}, "--auth none"},
		{"remote TLS, without --allow-remote", httpFlags{addr: "0.0.0.0:0", tlsCert: pki.serverCert, tlsKey: pki.serverKey, publicURL: "https://box:1"}, "allow-remote"},
		{"remote TLS + token", httpFlags{addr: "0.0.0.0:0", allowRemote: true, tlsCert: pki.serverCert, tlsKey: pki.serverKey, publicURL: "https://box:1"}, ""},
		{"remote mTLS, auth none", httpFlags{addr: "0.0.0.0:0", allowRemote: true, tlsCert: pki.serverCert, tlsKey: pki.serverKey, clientCA: pki.caCert, publicURL: "https://box:1", auth: "none"}, ""},
		{"cert without key", httpFlags{addr: "127.0.0.1:0", tlsCert: pki.serverCert}, "go together"},
		{"client CA without TLS", httpFlags{addr: "127.0.0.1:0", clientCA: pki.caCert}, "needs --tls-cert"},
		{"proxy without public URL", httpFlags{addr: "127.0.0.1:0", behindProxy: true}, "--public-url"},
		{"proxy, auth none", httpFlags{addr: "127.0.0.1:0", behindProxy: true, publicURL: "https://box", auth: "none"}, "--auth none"},
		{"proxy + token", httpFlags{addr: "127.0.0.1:0", behindProxy: true, publicURL: "https://box"}, ""},
		{"bad public URL", httpFlags{addr: "127.0.0.1:0", publicURL: "box:1"}, "--public-url"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := prepareHTTP(c.f, home)
			if s != nil {
				s.ln.Close()
			}
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if err == nil {
				wantGuard := c.f.auth == "token" || (c.f.auth == "" && (c.f.addr != "127.0.0.1:0" || c.f.behindProxy))
				if (s.guard != nil) != wantGuard {
					t.Errorf("guard = %v, want %v", s.guard != nil, wantGuard)
				}
			}
		})
	}
	if _, err := os.Stat(defaultTokenFile(home)); err != nil {
		t.Errorf("token file not created: %v", err)
	}
}

// startHTTP runs serveHTTP on a fresh knowledge base until the test ends.
func startHTTP(t *testing.T, f httpFlags) (string, *httpSetup) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := kb.Open(ctx, t.TempDir(), "live", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	svc := retrieve.New(store, retrieve.Default, false)
	srv := server.New(store, svc, "test", server.WithKBPath("/tmp/live.db"))
	h, err := prepareHTTP(f, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, h, srv, store, svc) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveHTTP: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("serveHTTP did not stop")
		}
		db.Close()
	})
	return h.scheme + "://" + h.ln.Addr().String(), h
}

func get(t *testing.T, c *http.Client, url string, hdr ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func ingestOver(t *testing.T, endpoint string, hc *http.Client) error {
	t.Helper()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "http-client", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc, MaxRetries: -1}, nil)
	if err != nil {
		return err
	}
	defer cs.Close()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "ingest", Arguments: map[string]any{
		"content": "# Live\n\nServed over HTTP.\n", "namespace": "web", "source": map[string]any{"title": "Live"}}})
	if err == nil && res.IsError {
		t.Fatalf("ingest tool error: %+v", res)
	}
	return err
}

// One loopback HTTP server: an MCP client ingests over /mcp, and the
// hosted UI shows that client and the run on /live and /ingest.
func TestServeHTTPHostsMCPAndLiveUI(t *testing.T) {
	base, _ := startHTTP(t, httpFlags{addr: "127.0.0.1:0"})
	if err := ingestOver(t, base+"/mcp", nil); err != nil {
		t.Fatal(err)
	}
	c := http.DefaultClient
	code, body := get(t, c, base+"/live")
	if code != 200 {
		t.Fatalf("/live: %d", code)
	}
	for _, want := range []string{"http-client", "/tmp/live.db", `<td>ingest</td><td class="num">1</td>`, `href="/live"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/live lacks %q", want)
		}
	}
	if _, body = get(t, c, base+"/ingest"); !strings.Contains(body, `<span class="badge done">done</span>`) || !strings.Contains(body, "http-client") {
		t.Errorf("/ingest does not show the tool run")
	}
	if _, body = get(t, c, base+"/metrics"); !strings.Contains(body, "memo_mcp_tool_calls_total") {
		t.Error("/metrics lacks tool counters")
	}
	code, body = get(t, c, base+"/live.json")
	var snap live.Snapshot
	if err := json.Unmarshal([]byte(body), &snap); code != 200 || err != nil {
		t.Fatalf("/live.json: %d %v\n%s", code, err, body)
	}
	if snap.Schema != live.Schema || snap.KB != "live" || snap.PID != os.Getpid() || snap.Instance == "" {
		t.Errorf("/live.json server fields = %+v", snap)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].Client != "http-client" || snap.Sessions[0].Calls != 1 || snap.Sessions[0].ID != "" {
		t.Errorf("/live.json sessions = %+v", snap.Sessions)
	}
}

type bearer struct {
	token string
	next  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

// HTTPS with the token: MCP needs the bearer header, the UI and /metrics
// too; a browser logs in with the printed one-time link and gets a cookie.
func TestServeHTTPSWithToken(t *testing.T) {
	pki := newTestPKI(t)
	base, h := startHTTP(t, httpFlags{addr: "127.0.0.1:0", auth: "token", tlsCert: pki.serverCert, tlsKey: pki.serverKey})
	tok, err := httpauth.Load(h.tokenFile)
	if err != nil {
		t.Fatal(err)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pki.pool}}
	plain := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	if err := ingestOver(t, base+"/mcp", &http.Client{Transport: tr}); err == nil {
		t.Fatal("MCP without a token succeeded")
	}
	if err := ingestOver(t, base+"/mcp", &http.Client{Transport: bearer{"wrong-wrong-wrong-wrong-wrong-wrong", tr}}); err == nil {
		t.Fatal("MCP with a wrong token succeeded")
	}
	if err := ingestOver(t, base+"/mcp", &http.Client{Transport: bearer{tok, tr}}); err != nil {
		t.Fatalf("MCP with the token: %v", err)
	}
	if code, _ := get(t, plain, base+"/metrics"); code != 401 {
		t.Errorf("/metrics without token: %d", code)
	}
	if code, body := get(t, plain, base+"/metrics", "Authorization", "Bearer "+tok); code != 200 || !strings.Contains(body, "memo_mcp_tool_calls_total") {
		t.Errorf("/metrics with token: %d", code)
	}
	if code, _ := get(t, plain, base+"/live", "Accept", "text/html"); code != 303 {
		t.Errorf("browser without session: %d, want redirect to /login", code)
	}

	if code, _ := get(t, plain, base+"/live.json"); code != 401 {
		t.Errorf("/live.json without token: %d, want 401", code)
	}

	// An app holding the token gets a login link for its own window.
	req, _ := http.NewRequest("POST", base+"/login-link?next=/live", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var link httpauth.LoginLink
	err = json.NewDecoder(resp.Body).Decode(&link)
	resp.Body.Close()
	if resp.StatusCode != 200 || err != nil || !strings.HasPrefix(link.URL, "/login?code=") {
		t.Fatalf("login link: %d %v %+v", resp.StatusCode, err, link)
	}
	req, _ = http.NewRequest("GET", base+link.URL, nil)
	resp, err = plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/live" || len(resp.Cookies()) != 1 {
		t.Fatalf("minted login link: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	req, _ = http.NewRequest("GET", base+"/login?code="+h.guard.LoginCode(), nil)
	resp, err = plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	if resp.StatusCode != 303 || len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("login link: %d %+v", resp.StatusCode, cookies)
	}
	req, _ = http.NewRequest("GET", base+"/live", nil)
	req.AddCookie(cookies[0])
	resp, err = plain.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "http-client") {
		t.Errorf("/live with cookie: %d", resp.StatusCode)
	}
}

// mTLS: without a client certificate the handshake fails; with one, MCP
// works without a token (--auth none is allowed only because of mTLS).
func TestServeHTTPMutualTLS(t *testing.T) {
	pki := newTestPKI(t)
	base, _ := startHTTP(t, httpFlags{addr: "127.0.0.1:0", auth: "none", tlsCert: pki.serverCert, tlsKey: pki.serverKey, clientCA: pki.caCert})
	noCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pki.pool}}}
	if _, err := noCert.Get(base + "/live"); err == nil {
		t.Fatal("TLS handshake without a client certificate succeeded")
	}
	withCert := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pki.pool, Certificates: []tls.Certificate{pki.client}}}}
	if err := ingestOver(t, base+"/mcp", withCert); err != nil {
		t.Fatalf("MCP with a client certificate: %v", err)
	}
}

type testPKI struct {
	caCert, serverCert, serverKey string // PEM file paths
	pool                          *x509.CertPool
	client                        tls.Certificate
}

// newTestPKI writes a CA, a server certificate for 127.0.0.1 and a client
// certificate, all signed by the CA.
func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	dir := t.TempDir()
	key := func() *ecdsa.PrivateKey {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	writePEM := func(name, typ string, der []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	now := time.Now()
	caKey := key()
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "memo test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	issue := func(serial int64, usage x509.ExtKeyUsage, k *ecdsa.PrivateKey) []byte {
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "memo test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &k.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return der
	}
	srvKey, cliKey := key(), key()
	srvKeyDER, _ := x509.MarshalECPrivateKey(srvKey)
	cliKeyDER, _ := x509.MarshalECPrivateKey(cliKey)
	p := testPKI{
		caCert:     writePEM("ca.pem", "CERTIFICATE", caDER),
		serverCert: writePEM("server.pem", "CERTIFICATE", issue(2, x509.ExtKeyUsageServerAuth, srvKey)),
		serverKey:  writePEM("server-key.pem", "EC PRIVATE KEY", srvKeyDER),
		pool:       x509.NewCertPool(),
	}
	p.pool.AddCert(ca)
	cliPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issue(3, x509.ExtKeyUsageClientAuth, cliKey)})
	p.client, err = tls.X509KeyPair(cliPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: cliKeyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
