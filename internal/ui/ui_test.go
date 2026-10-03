package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

func newUI(t *testing.T) (*Server, *kb.Store, *retrieve.Service) {
	t.Helper()
	ctx := context.Background()
	db, err := kb.Open(ctx, t.TempDir(), "ui", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	for i, body := range []string{
		"# Client.Connect\n\n`Connect(ctx, addr)` dials the Widgets Server; the context controls the dial timeout. On ERR_CONN_RESET the Retry Policy decides.\n",
		"# Retry Policy\n\nThe Retry Policy retries idempotent calls with exponential backoff; ERR_CONN_RESET is retried.\n",
		"# Widgets Server\n\nThe Widgets Server accepts connections from Client.Connect and enforces deadlines.\n",
	} {
		if _, err := store.Ingest(ctx, kb.IngestInput{Namespace: "widgets", Content: body, Source: kb.SourceInput{URI: "https://w.example/" + strconv.Itoa(i), Title: strings.TrimPrefix(strings.SplitN(body, "\n", 2)[0], "# "), Kind: kb.KindDoc, Origin: kb.OriginWeb}, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Remember(ctx, kb.RememberInput{Namespace: "widgets", Statement: "Client.Connect takes its timeout from the context.", About: []string{"Client.Connect"}, EvidenceURI: "memo://chunk/1", Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	ent, _ := store.ReadEntity(ctx, "Client.Connect", "widgets")
	if _, err := store.WritePage(ctx, kb.PageInput{Namespace: "widgets", Kind: kb.PageKindEntity, SubjectID: ent.ID, Title: "Client.Connect", Content: "# Client.Connect\n\nDials the server (memo://chunk/1).\n", Sources: []int64{1}, Actor: "t", Channel: kb.ChannelTool}); err != nil {
		t.Fatal(err)
	}
	svc := retrieve.New(store, retrieve.Default, false)
	s := New(store, svc, "test")
	return s, store, svc
}

func get(t *testing.T, h http.Handler, path, host string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return res.StatusCode, string(body)
}

// The UI's numbers are the CLI's numbers: same service, same request shape.
func TestSearchNumbersEqualTheService(t *testing.T) {
	s, _, svc := newUI(t)
	q := "how does Client.Connect relate to the Retry Policy"
	want, err := svc.Search(context.Background(), retrieve.Request{Query: q, Mode: retrieve.ModeAuto, Granularity: retrieve.GranularityDocument, ResponseFormat: retrieve.FormatExplain, Limit: 10, MaxTokens: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Search(context.Background(), q, "", "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != len(want.Results) || len(got.Results) == 0 {
		t.Fatalf("result counts differ: %d vs %d", len(got.Results), len(want.Results))
	}
	for i := range got.Results {
		if got.Results[i].URI != want.Results[i].URI || got.Results[i].Score != want.Results[i].Score || got.Results[i].Why.Fused != want.Results[i].Why.Fused {
			t.Fatalf("result %d differs: %+v vs %+v", i, got.Results[i], want.Results[i])
		}
	}
	// And the rendered page carries those same numbers.
	_, body := get(t, s.Handler(), "/search?q="+strings.ReplaceAll(q, " ", "+"), "")
	re := regexp.MustCompile(`data-address="([^"]+)" data-final="([0-9.]+)"`)
	m := re.FindAllStringSubmatch(body, -1)
	if len(m) != len(want.Results) {
		t.Fatalf("rendered %d rows, want %d\n%s", len(m), len(want.Results), body)
	}
	for i, row := range m {
		if row[1] != want.Results[i].URI || row[2] != strconv.FormatFloat(want.Results[i].Score, 'f', 5, 64) {
			t.Fatalf("row %d: %v vs %+v", i, row, want.Results[i])
		}
	}
	if !strings.Contains(body, "entity and graph arms added") || !strings.Contains(body, "why rank 1") {
		t.Fatal("the explain panel and trace should render")
	}
}

func TestRoutesRenderAndAreReadOnly(t *testing.T) {
	s, store, _ := newUI(t)
	h := s.Handler()
	for _, path := range []string{"/", "/search", "/doc/" + firstDoc(t, store), "/chunk/1", "/facts", "/facts?as_of=2020-01-01", "/entity/Client.Connect", "/pages", "/status", "/log", "/lint", "/eval"} {
		code, body := get(t, h, path, "")
		if code != http.StatusOK {
			t.Errorf("%s: %d\n%s", path, code, body)
		}
		if !strings.Contains(body, "read-only") {
			t.Errorf("%s: layout missing", path)
		}
	}
	pages, _ := store.ListPages(context.Background(), kb.PageFilter{})
	if code, body := get(t, h, "/page/"+pages[0].ID, ""); code != 200 || !strings.Contains(body, "derived") || !strings.Contains(body, "memo://chunk/1") {
		t.Fatalf("page view: %d %s", code, body)
	}
	if code, _ := get(t, h, "/doc/nope", ""); code != http.StatusNotFound {
		t.Fatalf("missing doc: %d", code)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/search", strings.NewReader("q=x"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s should be refused, got %d", method, rec.Code)
		}
	}
	// Nothing changed.
	st, _ := store.Status(context.Background())
	if st.LiveDocuments != 3 || st.Facts != 1 {
		t.Fatalf("the UI changed the store: %+v", st)
	}
}

func TestHostGuardAndLoopback(t *testing.T) {
	s, _, _ := newUI(t)
	ln, err := s.Listen("127.0.0.1:0", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	h := s.Handler()
	if code, _ := get(t, h, "/", ln.Addr().String()); code != http.StatusOK {
		t.Fatalf("bound address should be allowed: %d", code)
	}
	if code, _ := get(t, h, "/", "evil.example:80"); code != http.StatusForbidden {
		t.Fatalf("foreign Host header should be refused: %d", code)
	}
	if _, err := New(nil, nil, "t").Listen("0.0.0.0:0", false); err == nil {
		t.Fatal("non-loopback bind must be refused without --allow-remote")
	}
}

func firstDoc(t *testing.T, store *kb.Store) string {
	list, err := store.List(context.Background(), kb.ListOptions{})
	if err != nil || len(list) == 0 {
		t.Fatal("no documents")
	}
	return list[0].DocumentID
}

func TestMetricsRoute(t *testing.T) {
	s, _, _ := newUI(t)
	h := s.Handler()
	if code, _ := get(t, h, "/", ""); code != 200 {
		t.Fatal(code)
	}
	code, body := get(t, h, "/metrics", "")
	if code != 200 || !strings.Contains(body, "# TYPE memo_ui_requests_total counter") || !strings.Contains(body, `memo_ui_requests_total{route="GET /{$}",status="200"}`) {
		t.Fatalf("metrics: %d\n%s", code, body)
	}
	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /metrics: %d", rec.Code)
	}
}
