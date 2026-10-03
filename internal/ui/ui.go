// Package ui is the optional read-only web face of a knowledge base
// (roadmap P9): server-rendered HTML over the same store and the same
// retrieve.Service the MCP server and the CLI use, so every number a human
// sees here is the number the agent saw. GET only; loopback only unless
// told otherwise; the Host header is checked against the bound address so a
// page on another origin cannot reach it through DNS rebinding.
package ui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kKEo/memory-find/internal/compact"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server serves the read-only UI.
type Server struct {
	store   *kb.Store
	search  *retrieve.Service
	version string
	tmpl    *template.Template
	hosts   map[string]bool // allowed Host header values
	mu      sync.Mutex
	evalFn  func(ctx context.Context) (string, error)
	evalOut string
}

// New builds the handler set. hosts are the Host header values accepted
// (the listen address and its loopback spellings).
func New(store *kb.Store, search *retrieve.Service, version string) *Server {
	funcs := template.FuncMap{
		"f3":   func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) },
		"f5":   func(v float64) string { return strconv.FormatFloat(v, 'f', 5, 64) },
		"pf":   func(v *float64) string { return ptrF(v, 3) },
		"pi":   func(v *int) string { return ptrI(v) },
		"date": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
		"dt":   func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") },
		"pdate": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.UTC().Format("2006-01-02")
		},
		"join": strings.Join,
		"list": func(v ...string) []string { return v },
		"href": uriHref,
		"json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) },
		"ms":   func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) },
		"short": func(s string) string {
			if len(s) > 12 {
				return s[:8]
			}
			return s
		},
	}
	t := template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))
	return &Server{store: store, search: search, version: version, tmpl: t, hosts: map[string]bool{}}
}

// SetEval installs the function that produces the eval report (the CLI
// wires it so the ui package does not depend on the eval package).
func (s *Server) SetEval(fn func(ctx context.Context) (string, error)) { s.evalFn = fn }

// AllowHost adds an accepted Host header value.
func (s *Server) AllowHost(h string) { s.hosts[strings.ToLower(h)] = true }

// Listen binds addr (loopback unless allowRemote) and returns the listener,
// having registered its address as an allowed host.
func (s *Server) Listen(addr string, allowRemote bool) (net.Listener, error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("--addr must be host:port: %w", err)
	}
	if !allowRemote && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("refusing to listen on %s: the UI is read-only but shows everything in the knowledge base; pass --allow-remote if that is intended", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	bound := ln.Addr().String()
	_, port, _ := net.SplitHostPort(bound)
	s.AllowHost(bound)
	s.AllowHost("localhost:" + port)
	s.AllowHost("127.0.0.1:" + port)
	s.AllowHost("[::1]:" + port)
	return ln, nil
}

// Handler returns the http.Handler: GET only, Host checked, no mutation.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /search", s.searchPage)
	mux.HandleFunc("GET /doc/{id}", s.docPage)
	mux.HandleFunc("GET /chunk/{id}", s.chunkPage)
	mux.HandleFunc("GET /source/{id}", s.sourcePage)
	mux.HandleFunc("GET /facts", s.factsPage)
	mux.HandleFunc("GET /fact/{id}", s.factPage)
	mux.HandleFunc("GET /entity/{id}", s.entityPage)
	mux.HandleFunc("GET /page/{id}", s.pagePage)
	mux.HandleFunc("GET /pages", s.pagesPage)
	mux.HandleFunc("GET /status", s.statusPage)
	mux.HandleFunc("GET /log", s.logPage)
	mux.HandleFunc("GET /lint", s.lintPage)
	mux.HandleFunc("GET /eval", s.evalPage)
	mux.HandleFunc("GET /style.css", s.css)
	return s.guard(mux)
}

// guard rejects non-GET methods and unknown Host headers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only: the UI has no mutating routes; use the CLI or the MCP tools", http.StatusMethodNotAllowed)
			return
		}
		if len(s.hosts) > 0 && !s.hosts[strings.ToLower(r.Host)] {
			http.Error(w, "unexpected Host header (DNS rebinding defence); open the UI by the address memo-mcp printed", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type page struct {
	Title   string
	Version string
	Query   string
	Data    any
	Error   string
}

func (s *Server) render(w http.ResponseWriter, name, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, page{Title: title, Version: s.version, Data: data}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var forgotten *kb.Forgotten
	switch {
	case errors.Is(err, kb.ErrNotFound):
		code = http.StatusNotFound
	case errors.As(err, &forgotten):
		code = http.StatusGone
	}
	w.WriteHeader(code)
	s.render(w, "error.html", "Error", err.Error())
}

func (s *Server) css(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write([]byte(styleCSS))
}

// --- pages ---

type homeData struct {
	Status *kb.Status
	Recent []kb.ListEntry
	Pages  []kb.Page
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Status(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	recent, _ := s.store.List(r.Context(), kb.ListOptions{Limit: 15})
	pages, _ := s.store.ListPages(r.Context(), kb.PageFilter{Limit: 10})
	s.render(w, "home.html", "memo-mcp", homeData{Status: st, Recent: recent, Pages: pages})
}

// SearchData is what the search page renders: the same Response the CLI
// and MCP get, with explain on.
type SearchData struct {
	Query       string
	Mode        string
	Granularity string
	Namespace   string
	Library     string
	Version     string
	AsOf        string
	Resp        *retrieve.Response
	Err         string
}

// Search runs the query exactly as the agent would (explain format), so the
// equality test can compare numbers.
func (s *Server) Search(ctx context.Context, q, mode, gran, ns, library, version, asOf string) (*retrieve.Response, error) {
	req := retrieve.Request{Query: q, Mode: mode, Granularity: gran, ResponseFormat: retrieve.FormatExplain, Limit: 10, MaxTokens: 1 << 20}
	if ns != "" {
		req.Scope.Namespaces = []string{ns}
	}
	req.Scope.Library, req.Scope.Version = library, version
	if asOf != "" {
		t, err := time.Parse("2006-01-02", asOf)
		if err != nil {
			return nil, fmt.Errorf("as_of: %w", err)
		}
		req.AsOf = &t
	}
	if req.Mode == "" {
		req.Mode = retrieve.ModeAuto
	}
	if req.Granularity == "" {
		req.Granularity = retrieve.GranularityDocument
	}
	return s.search.Search(ctx, req)
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := SearchData{Query: strings.TrimSpace(q.Get("q")), Mode: q.Get("mode"), Granularity: q.Get("granularity"), Namespace: q.Get("ns"), Library: q.Get("library"), Version: q.Get("version"), AsOf: q.Get("as_of")}
	if d.Query != "" {
		resp, err := s.Search(r.Context(), d.Query, d.Mode, d.Granularity, d.Namespace, d.Library, d.Version, d.AsOf)
		if err != nil {
			d.Err = err.Error()
		} else {
			d.Resp = resp
		}
	}
	s.render(w, "search.html", "Search", d)
}

type docData struct {
	Doc     *kb.Document
	Chunks  []kb.ChunkRead
	History []kb.HistoryEntry
}

func (s *Server) docPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.store.ReadDocument(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	history, _ := s.store.History(r.Context(), d.URI)
	chunks, _ := s.docChunks(r.Context(), id)
	s.render(w, "doc.html", d.Prov.Title, docData{Doc: d, Chunks: chunks, History: history})
}

func (s *Server) docChunks(ctx context.Context, docID string) ([]kb.ChunkRead, error) {
	rows, err := s.store.DB().QueryContext(ctx, `SELECT id FROM chunks WHERE document_id = ? ORDER BY ord`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []kb.ChunkRead
	for _, id := range ids {
		c, err := s.store.ReadChunk(ctx, id)
		if err == nil {
			out = append(out, *c)
		}
	}
	return out, nil
}

type chunkData struct {
	Chunk    *kb.ChunkRead
	Prev     string
	Next     string
	Entities []kb.Entity
	Pages    []kb.Page
}

func (s *Server) chunkPage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, fmt.Errorf("chunk id: %w", kb.ErrNotFound))
		return
	}
	c, err := s.store.ReadChunk(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	prev, next, _ := s.store.Neighbours(r.Context(), c)
	ents, _ := s.chunkEntities(r.Context(), id)
	s.render(w, "chunk.html", fmt.Sprintf("Passage %d", id), chunkData{Chunk: c, Prev: prev, Next: next, Entities: ents})
}

func (s *Server) chunkEntities(ctx context.Context, chunkID int64) ([]kb.Entity, error) {
	rows, err := s.store.DB().QueryContext(ctx, `SELECT e.id, e.namespace, e.canonical, e.type FROM mentions m JOIN entities e ON e.id = m.entity_id WHERE m.chunk_id = ? ORDER BY m.weight DESC, e.canonical`, chunkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []kb.Entity
	for rows.Next() {
		var e kb.Entity
		if err := rows.Scan(&e.ID, &e.Namespace, &e.Canonical, &e.Type); err != nil {
			return nil, err
		}
		e.URI = "memo://entity/" + e.ID
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Server) sourcePage(w http.ResponseWriter, r *http.Request) {
	text, prov, err := s.store.Read(r.Context(), "memo://source/"+r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	history, _ := s.store.History(r.Context(), "memo://source/"+r.PathValue("id"))
	s.render(w, "source.html", prov.Title, struct {
		Prov    kb.Provenance
		Text    string
		History []kb.HistoryEntry
		ID      string
	}{prov, text, history, r.PathValue("id")})
}

type factsData struct {
	Namespace string
	AsOf      string
	History   bool
	Facts     []kb.Fact
}

func (s *Server) factsPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d := factsData{Namespace: q.Get("ns"), AsOf: q.Get("as_of"), History: q.Get("history") == "1"}
	f := kb.FactFilter{Namespace: d.Namespace, IncludeHistory: d.History, Limit: 500}
	if d.AsOf != "" {
		t, err := time.Parse("2006-01-02", d.AsOf)
		if err != nil {
			s.fail(w, err)
			return
		}
		f.AsOf = &t
	}
	facts, err := s.store.ListFacts(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	d.Facts = facts
	s.render(w, "facts.html", "Facts", d)
}

func (s *Server) factPage(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.ReadFact(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	history, _ := s.store.History(r.Context(), f.URI)
	var evidence *kb.ChunkRead
	if f.EvidenceChunkID != nil {
		evidence, _ = s.store.ReadChunk(r.Context(), *f.EvidenceChunkID)
	}
	s.render(w, "fact.html", "Fact", struct {
		Fact     *kb.Fact
		Evidence *kb.ChunkRead
		History  []kb.HistoryEntry
	}{f, evidence, history})
}

func (s *Server) entityPage(w http.ResponseWriter, r *http.Request) {
	hops := 1
	if r.URL.Query().Get("hops") == "2" {
		hops = 2
	}
	ex, err := s.store.Explore(r.Context(), r.PathValue("id"), r.URL.Query().Get("ns"), hops, nil)
	if err != nil {
		s.fail(w, err)
		return
	}
	facts, _ := s.store.FactsAbout(r.Context(), ex.Entity.ID)
	var pg *kb.Page
	pages, _ := s.store.ListPages(r.Context(), kb.PageFilter{Namespace: ex.Entity.Namespace, Kind: kb.PageKindEntity})
	for i := range pages {
		if pages[i].SubjectID == ex.Entity.ID {
			pg = &pages[i]
		}
	}
	s.render(w, "entity.html", ex.Entity.Canonical, struct {
		Ex    *kb.Exploration
		Hops  int
		Facts []kb.Fact
		Page  *kb.Page
	}{ex, hops, facts, pg})
}

func (s *Server) pagePage(w http.ResponseWriter, r *http.Request) {
	p, err := s.store.ReadPage(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "page.html", p.Title, p)
}

func (s *Server) pagesPage(w http.ResponseWriter, r *http.Request) {
	pages, err := s.store.ListPages(r.Context(), kb.PageFilter{Namespace: r.URL.Query().Get("ns"), StaleOnly: r.URL.Query().Get("stale") == "1"})
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, "pages.html", "Pages", pages)
}

func (s *Server) statusPage(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Status(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	models, _ := s.store.InstalledModels(r.Context())
	profiles := retrieve.Profiles()
	s.render(w, "status.html", "Status", struct {
		Status   *kb.Status
		Models   []kb.InstalledModel
		Profiles []retrieve.Profile
		Describe func(retrieve.Profile) string
	}{st, models, profiles, retrieve.Describe})
}

func (s *Server) logPage(w http.ResponseWriter, r *http.Request) {
	entries, err := s.store.QueryLogTail(r.Context(), 100)
	if err != nil {
		s.fail(w, err)
		return
	}
	type row struct {
		kb.QueryLogEntry
		Queries string
	}
	rows := make([]row, 0, len(entries))
	for _, e := range entries {
		var v struct {
			Queries []string `json:"queries"`
		}
		_ = json.Unmarshal([]byte(e.Args), &v)
		rows = append(rows, row{e, strings.Join(v.Queries, " | ")})
	}
	s.render(w, "log.html", "Query log", rows)
}

func (s *Server) lintPage(w http.ResponseWriter, r *http.Request) {
	namespaces := []string{r.URL.Query().Get("ns")}
	if namespaces[0] == "" {
		ns, err := s.store.Namespaces(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		namespaces = ns
	}
	var all []compact.Finding
	for _, ns := range namespaces {
		f, err := compact.Lint(r.Context(), s.store, ns, time.Now())
		if err != nil {
			s.fail(w, err)
			return
		}
		all = append(all, f...)
	}
	items, _ := s.store.ListWorkItems(r.Context(), r.URL.Query().Get("ns"), "", "open", 100)
	s.render(w, "lint.html", "Lint", struct {
		Findings []compact.Finding
		Items    []kb.WorkItem
	}{all, items})
}

func (s *Server) evalPage(w http.ResponseWriter, r *http.Request) {
	if s.evalFn == nil {
		s.render(w, "eval.html", "Eval", "The eval report is not wired in this build.")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evalOut == "" {
		out, err := s.evalFn(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		s.evalOut = out
	}
	s.render(w, "eval.html", "Eval", s.evalOut)
}

// --- helpers ---

func ptrF(v *float64, prec int) string {
	if v == nil {
		return "–"
	}
	return strconv.FormatFloat(*v, 'f', prec, 64)
}

func ptrI(v *int) string {
	if v == nil {
		return "–"
	}
	return strconv.Itoa(*v)
}

// uriHref maps a memo:// address to a UI path.
func uriHref(uri string) string {
	kind, id, err := kb.ParseURI(uri)
	if err != nil {
		return "/"
	}
	switch kind {
	case "doc", "chunk", "source", "fact", "entity", "page":
		return "/" + kind + "/" + id
	}
	return "/"
}

const styleCSS = `
:root { --fg:#1b1b1b; --muted:#666; --line:#ddd; --bg:#fff; --accent:#1a5fb4; --warn:#b4541a; --soft:#f6f6f4; }
* { box-sizing:border-box; }
body { font:15px/1.45 system-ui, -apple-system, "Segoe UI", sans-serif; color:var(--fg); background:var(--bg); margin:0; }
header { border-bottom:1px solid var(--line); padding:.6rem 1rem; display:flex; gap:1rem; align-items:center; flex-wrap:wrap; }
header a { color:var(--fg); text-decoration:none; } header a:hover { text-decoration:underline; }
header .brand { font-weight:700; } header .version { color:var(--muted); font-size:.85em; }
header form { margin-left:auto; display:flex; gap:.4rem; }
main { max-width:1100px; margin:0 auto; padding:1rem; }
h1 { font-size:1.4rem; margin:.4rem 0 .8rem; } h2 { font-size:1.1rem; margin:1.2rem 0 .5rem; }
table { border-collapse:collapse; width:100%; font-size:.93em; } th, td { text-align:left; padding:.35rem .5rem; border-bottom:1px solid var(--line); vertical-align:top; }
th { color:var(--muted); font-weight:600; } td.num, th.num { text-align:right; font-variant-numeric:tabular-nums; }
pre { background:var(--soft); padding:.8rem; overflow:auto; white-space:pre-wrap; border-radius:4px; }
code { background:var(--soft); padding:0 .25em; border-radius:3px; font-size:.92em; }
.muted { color:var(--muted); } .warn { color:var(--warn); } .ok { color:#1a7a3a; }
.badge { display:inline-block; padding:0 .45em; border:1px solid var(--line); border-radius:999px; font-size:.8em; color:var(--muted); }
.badge.strong { border-color:#1a7a3a; color:#1a7a3a; } .badge.moderate { border-color:var(--accent); color:var(--accent); } .badge.weak, .badge.keyword-only { border-color:var(--muted); }
.badge.stale { border-color:var(--warn); color:var(--warn); }
input, select { font:inherit; padding:.3rem .45rem; border:1px solid var(--line); border-radius:4px; }
button { font:inherit; padding:.3rem .7rem; border:1px solid var(--accent); background:var(--accent); color:#fff; border-radius:4px; }
details { margin:.4rem 0; } summary { cursor:pointer; color:var(--accent); }
.explain td { font-size:.88em; } .cards { display:grid; grid-template-columns:repeat(auto-fit, minmax(220px,1fr)); gap:.8rem; }
.card { border:1px solid var(--line); border-radius:6px; padding:.7rem .9rem; } .card .big { font-size:1.6rem; font-weight:700; }
.filters { display:flex; gap:.5rem; flex-wrap:wrap; align-items:end; margin-bottom:1rem; } .filters label { display:flex; flex-direction:column; font-size:.85em; color:var(--muted); }
blockquote { border-left:3px solid var(--line); margin:.4rem 0; padding:.2rem .8rem; color:var(--muted); }
.timeline li { margin:.3rem 0; } .timeline .dead { color:var(--muted); text-decoration:line-through; }
footer { color:var(--muted); font-size:.85em; padding:1rem; text-align:center; border-top:1px solid var(--line); margin-top:2rem; }
`
