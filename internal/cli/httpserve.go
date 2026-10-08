package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/kKEo/memory-find/internal/httpauth"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/retrieve"
	"github.com/kKEo/memory-find/internal/runfile"
	"github.com/kKEo/memory-find/internal/server"
	"github.com/kKEo/memory-find/internal/ui"
)

// httpFlags are serve's --http options.
type httpFlags struct {
	addr        string
	allowRemote bool
	auth        string
	tokenFile   string
	tlsCert     string
	tlsKey      string
	clientCA    string
	publicURL   string
	behindProxy bool
}

func (f *httpFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.addr, "http", os.Getenv("MEMO_HTTP_ADDR"), "serve MCP over HTTP at <addr>/mcp, with the live web UI at <addr>/ (off when empty: stdio)")
	fs.BoolVar(&f.allowRemote, "allow-remote", false, "with --http: allow a non-loopback address (needs TLS or --behind-proxy, and authentication)")
	fs.StringVar(&f.auth, "auth", os.Getenv("MEMO_HTTP_AUTH"), "with --http: token or none (default: token when remote or behind a proxy, none on loopback)")
	fs.StringVar(&f.tokenFile, "token-file", os.Getenv("MEMO_HTTP_TOKEN_FILE"), "bearer token file (default <MEMO_HOME>/http-token, created 0600 when missing)")
	fs.StringVar(&f.tlsCert, "tls-cert", os.Getenv("MEMO_TLS_CERT"), "with --http: serve HTTPS with this PEM certificate (chain)")
	fs.StringVar(&f.tlsKey, "tls-key", os.Getenv("MEMO_TLS_KEY"), "with --http: the certificate's PEM private key")
	fs.StringVar(&f.clientCA, "tls-client-ca", os.Getenv("MEMO_TLS_CLIENT_CA"), "with --tls-cert: require client certificates signed by this PEM CA (mTLS)")
	fs.StringVar(&f.publicURL, "public-url", os.Getenv("MEMO_PUBLIC_URL"), "the URL clients use, e.g. https://box.example:8765 (required when remote or behind a proxy)")
	fs.BoolVar(&f.behindProxy, "behind-proxy", false, "a reverse proxy terminates TLS and forwards to --http; cookies are marked Secure")
}

// httpSetup is a validated, bound HTTP front end.
type httpSetup struct {
	ln          net.Listener
	scheme      string
	publicURL   *url.URL // nil on plain loopback
	guard       *httpauth.Guard
	tokenFile   string
	mtls        bool
	behindProxy bool
	runDir      string // where serveHTTP advertises the server (internal/runfile)
}

func defaultTokenFile(home string) string { return filepath.Join(home, "http-token") }

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// prepareHTTP applies the safety rules, loads TLS material and the token,
// and binds the listener. Every refusal happens here, before the knowledge
// base is opened:
//   - a non-loopback address needs --allow-remote, TLS (or --behind-proxy),
//     --public-url, and authentication (token or mTLS);
//   - behind a proxy the token is on by default too, since the proxy's
//     loopback connection would otherwise look local.
func prepareHTTP(f httpFlags, home string) (*httpSetup, error) {
	host, _, err := net.SplitHostPort(f.addr)
	if err != nil {
		return nil, fmt.Errorf("--http must be host:port: %w", err)
	}
	exposed := !isLoopbackHost(host) || f.behindProxy
	if (f.tlsCert == "") != (f.tlsKey == "") {
		return nil, errors.New("--tls-cert and --tls-key go together")
	}
	if f.clientCA != "" && f.tlsCert == "" {
		return nil, errors.New("--tls-client-ca needs --tls-cert and --tls-key (mTLS is part of TLS)")
	}
	if !isLoopbackHost(host) && f.tlsCert == "" && !f.behindProxy {
		return nil, fmt.Errorf("refusing plain HTTP on %s: the token and everything in the knowledge base would cross the network unencrypted; pass --tls-cert/--tls-key, or --behind-proxy if a proxy terminates TLS", f.addr)
	}
	mode := f.auth
	if mode == "" {
		mode = "none"
		if exposed {
			mode = "token"
		}
	}
	switch mode {
	case "token":
	case "none":
		if exposed && f.clientCA == "" {
			return nil, errors.New("refusing --auth none on a non-loopback or proxied address without mTLS (--tls-client-ca): anyone who reaches it could read and write the knowledge base")
		}
	default:
		return nil, fmt.Errorf("--auth must be token or none, not %q", f.auth)
	}
	s := &httpSetup{scheme: "http", mtls: f.clientCA != "", behindProxy: f.behindProxy, runDir: runfile.Dir(home)}
	if f.publicURL != "" {
		u, err := url.Parse(f.publicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("--public-url must look like https://host:port, not %q", f.publicURL)
		}
		s.publicURL = &url.URL{Scheme: u.Scheme, Host: u.Host}
	} else if exposed {
		return nil, errors.New("--public-url is required when remote or behind a proxy: it is the address clients use, and the only Host the server accepts besides loopback")
	}

	var tlsCfg *tls.Config
	if f.tlsCert != "" {
		cert, err := tls.LoadX509KeyPair(f.tlsCert, f.tlsKey)
		if err != nil {
			return nil, fmt.Errorf("load TLS certificate: %w", err)
		}
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
		if f.clientCA != "" {
			pem, err := os.ReadFile(f.clientCA)
			if err != nil {
				return nil, fmt.Errorf("read client CA: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("no PEM certificates in %s", f.clientCA)
			}
			tlsCfg.ClientCAs, tlsCfg.ClientAuth = pool, tls.RequireAndVerifyClientCert
		}
		s.scheme = "https"
	}
	if mode == "token" {
		s.tokenFile = f.tokenFile
		if s.tokenFile == "" {
			s.tokenFile = defaultTokenFile(home)
		}
		tok, created, err := httpauth.LoadOrCreate(s.tokenFile)
		if err != nil {
			return nil, err
		}
		if created {
			slog.Info("created HTTP bearer token", "file", s.tokenFile)
		}
		s.guard = httpauth.New(tok, tlsCfg != nil || f.behindProxy)
	}

	ln, err := ui.Listen(f.addr, f.allowRemote)
	if err != nil {
		return nil, err
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	s.ln = ln
	return s, nil
}

// base is the URL printed for clients.
func (s *httpSetup) base() string {
	if s.publicURL != nil {
		return s.publicURL.String()
	}
	return s.scheme + "://" + s.ln.Addr().String()
}

// serveHTTP runs one long-lived server for any number of MCP clients: MCP
// at /mcp, the web UI (with live pages) everywhere else, all behind the
// token when one is configured.
func serveHTTP(ctx context.Context, h *httpSetup, srv *server.Server, store *kb.Store, svc *retrieve.Service) error {
	web := ui.New(store, svc, serverVersion, ui.WithLive(srv))
	web.AllowListener(h.ln)
	if h.publicURL != nil {
		web.AllowHost(h.publicURL.Host)
	}
	web.SetEval(evalReportMarkdown)
	mux := http.NewServeMux()
	mux.Handle("/mcp", srv.HTTPHandler(server.HTTPOptions{BehindProxy: h.behindProxy}))
	mux.Handle("/", web.Handler())
	var handler http.Handler = mux
	if h.guard != nil {
		handler = h.guard.Wrap(mux)
	}
	hs := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutdown)
	}()
	base := h.base()
	add := "claude mcp add --transport http memo " + base + "/mcp"
	attrs := []any{"mcp", base + "/mcp", "ui", base + "/", "mtls", h.mtls}
	if h.guard != nil {
		add += ` --header "Authorization: Bearer $(memo-mcp http-token)"`
		attrs = append(attrs, "auth", "token", "token_file", h.tokenFile)
	} else {
		attrs = append(attrs, "auth", "none")
	}
	slog.Info("serving MCP over HTTP", append(attrs, "add_to_claude_code", add)...)
	if h.guard != nil {
		slog.Info("browser login link (single use, 15 minutes)", "url", base+"/login?code="+h.guard.LoginCode())
	}
	if path, err := runfile.Write(h.runDir, h.runInfo(srv.Live())); err != nil {
		slog.Warn("cannot advertise the server to local apps", "dir", h.runDir, "err", err)
	} else {
		defer func() {
			if err := runfile.Remove(path); err != nil {
				slog.Warn("cannot remove run file", "file", path, "err", err)
			}
		}()
	}
	if err := hs.Serve(h.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// runInfo is what a run file says about this server: where and how to
// reach it, never a secret. Paths are absolute, so a reader in another
// working directory finds the same files.
func (s *httpSetup) runInfo(snap live.Snapshot) runfile.Info {
	info := runfile.Info{PID: snap.PID, Instance: snap.Instance, Version: snap.Version, KB: snap.KB, KBPath: absPath(snap.KBPath),
		URL: s.base(), Listen: s.ln.Addr().String(), Scheme: s.scheme, Auth: "none", MTLS: s.mtls, BehindProxy: s.behindProxy, Started: snap.Started}
	if s.guard != nil {
		info.Auth, info.TokenFile = "token", absPath(s.tokenFile)
	}
	return info
}

func absPath(p string) string {
	if p == "" {
		return ""
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// runHTTPToken prints the bearer token for `serve --http`, creating it when
// missing; --rotate replaces it (restart the server afterwards).
func runHTTPToken(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("http-token", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rotate := fs.Bool("rotate", false, "replace the token: every client and browser session must use the new one after the server restarts")
	file := fs.String("file", os.Getenv("MEMO_HTTP_TOKEN_FILE"), "token file (default <MEMO_HOME>/http-token)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := ResolveConfig(os.Getenv)
	if err != nil {
		return err
	}
	path := *file
	if path == "" {
		path = defaultTokenFile(filepath.Dir(cfg.KBDir))
	}
	var tok string
	if *rotate {
		tok, err = httpauth.Rotate(path)
		if err == nil {
			fmt.Fprintf(stderr, "rotated %s; restart `memo-mcp serve --http` and update your clients\n", path)
		}
	} else {
		var created bool
		tok, created, err = httpauth.LoadOrCreate(path)
		if created {
			fmt.Fprintf(stderr, "created %s\n", path)
		}
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, tok)
	return nil
}
