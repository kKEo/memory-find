package obs

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Handler serves the registry in the Prometheus text format. GET and HEAD
// only.
func Handler(reg *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "metrics are read-only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method == http.MethodHead {
			return
		}
		_ = WriteText(w, reg.Snapshot(r.Context()))
	})
}

// MetricsServer is the scrape endpoint of a serving process: loopback only,
// GET /metrics only, Host header checked against the bound address.
type MetricsServer struct {
	reg   *Registry
	hosts map[string]bool
}

// NewMetricsServer wraps a registry.
func NewMetricsServer(reg *Registry) *MetricsServer {
	return &MetricsServer{reg: reg, hosts: map[string]bool{}}
}

// Listen binds addr, refusing anything that is not loopback. There is no
// override: metrics describe everything the knowledge base is doing.
func (m *MetricsServer) Listen(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("metrics address must be host:port: %w", err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("refusing to expose metrics on %s: loopback addresses only", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	for _, h := range []string{ln.Addr().String(), "localhost:" + port, "127.0.0.1:" + port, "[::1]:" + port} {
		m.hosts[strings.ToLower(h)] = true
	}
	return ln, nil
}

// Handler routes GET /metrics behind the Host check; everything else is 404.
func (m *MetricsServer) Handler() http.Handler {
	inner := Handler(m.reg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(m.hosts) > 0 && !m.hosts[strings.ToLower(r.Host)] {
			http.Error(w, "unexpected Host header", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}
