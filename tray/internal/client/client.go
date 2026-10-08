// Package client talks to a running memo-mcp HTTP server: its live
// snapshot (GET /live.json) and single-use login links for the stats
// window (POST /login-link).
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kKEo/memory-find/internal/httpauth"
	"github.com/kKEo/memory-find/internal/live"
	"github.com/kKEo/memory-find/internal/runfile"
)

var (
	// ErrUnreachable: nothing answered at the server's address.
	ErrUnreachable = errors.New("not reachable")
	// ErrUnauthorized: the server refused the token (rotated since it started?).
	ErrUnauthorized = errors.New("token refused: restart the server after rotating it")
	// ErrNotSupported: the server predates /live.json.
	ErrNotSupported = errors.New("this memo-mcp is too old for memo-tray: upgrade it")
	// ErrMTLS: the server wants a client certificate, which memo-tray has not got.
	ErrMTLS = errors.New("needs a client certificate (mTLS); open it in a browser that has one")
	// ErrInsecure: the token would cross the network unencrypted.
	ErrInsecure = errors.New("refusing to send the token over plain HTTP to another machine")
)

// Target is how to reach one server.
type Target struct {
	URL       string // base URL, e.g. http://127.0.0.1:8765
	Auth      string // none or token
	TokenFile string
	MTLS      bool
}

// TargetOf reads the target from a run file.
func TargetOf(info runfile.Info) Target {
	return Target{URL: strings.TrimSuffix(info.URL, "/"), Auth: info.Auth, TokenFile: info.TokenFile, MTLS: info.MTLS}
}

// Client is safe for concurrent use.
type Client struct{ hc *http.Client }

// New returns a client that never uses a proxy, never follows redirects
// and gives up quickly: the servers are local and polled every few seconds.
func New() *Client {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: time.Second}).DialContext,
		TLSHandshakeTimeout: 2 * time.Second,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	}
	return &Client{hc: &http.Client{
		Transport:     tr,
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Live fetches the server's live snapshot.
func (c *Client) Live(ctx context.Context, t Target) (live.Snapshot, error) {
	var snap live.Snapshot
	req, err := request(ctx, http.MethodGet, t, "/live.json")
	if err != nil {
		return snap, err
	}
	if err := c.do(req, &snap); err != nil {
		return snap, err
	}
	if snap.Schema != live.Schema {
		return snap, fmt.Errorf("unknown /live.json schema %d: upgrade memo-tray", snap.Schema)
	}
	return snap, nil
}

// StatsURL is the address to open for a server's page at path (such as
// /live): in token mode a single-use login link that lands there, else the
// page itself.
func (c *Client) StatsURL(ctx context.Context, t Target, path string) (string, error) {
	if t.Auth != "token" {
		return t.URL + path, nil
	}
	req, err := request(ctx, http.MethodPost, t, "/login-link?next="+url.QueryEscape(path))
	if err != nil {
		return "", err
	}
	var ll httpauth.LoginLink
	if err := c.do(req, &ll); err != nil {
		return "", err
	}
	if !strings.HasPrefix(ll.URL, "/login?") {
		return "", fmt.Errorf("unexpected login link %q", ll.URL)
	}
	return t.URL + ll.URL, nil
}

func (c *Client) do(req *http.Request, v any) error {
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return ErrNotSupported
	default:
		return fmt.Errorf("server answered %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v); err != nil {
		return fmt.Errorf("bad answer from %s: %w", req.URL.Path, err)
	}
	return nil
}

// request builds a request with the token when the server wants one. The
// token goes only to https or to this machine.
func request(ctx context.Context, method string, t Target, path string) (*http.Request, error) {
	if t.MTLS {
		return nil, ErrMTLS
	}
	u, err := url.Parse(t.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("bad server URL %q", t.URL)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.URL+path, nil)
	if err != nil {
		return nil, err
	}
	if t.Auth == "token" {
		if u.Scheme != "https" && !Loopback(u.Hostname()) {
			return nil, ErrInsecure
		}
		tok, err := httpauth.Load(t.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("token file: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

// Loopback reports whether host names this machine.
func Loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
