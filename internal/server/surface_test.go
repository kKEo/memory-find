package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/retrieve"
)

// newTestSessionWith is newTestSession with client options, for clients that
// can answer elicitation.
func newTestSessionWith(t *testing.T, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	db, err := kb.Open(context.Background(), t.TempDir(), "test", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	srv := New(store, retrieve.New(store, retrieve.Default, false), "test")
	ct, st := mcp.NewInMemoryTransports()
	if _, err := srv.mcp.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, opts).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// Resources mirror the read tools: a search result's address can be read as
// a resource, and the index resources give an agent a session-start view.
func TestResourcesMirrorReadTools(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	ctx := context.Background()

	tpl, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range tpl.ResourceTemplates {
		names = append(names, r.URITemplate)
	}
	want := []string{"memo://doc/{id}", "memo://chunk/{id}", "memo://source/{id}", "memo://fact/{id}", "memo://entity/{id}", "memo://page/{id}", "memo://ns/{namespace}/index"}
	for _, w := range want {
		if !containsString(names, w) {
			t.Errorf("template %s missing from %v", w, names)
		}
	}
	res, err := cs.ListResources(ctx, nil)
	if err != nil || len(res.Resources) != 1 || res.Resources[0].URI != "memo://index" {
		t.Fatalf("static resources: %+v %v", res, err)
	}

	sr := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "ERR_CONN_RESET", "mode": "exact"}))
	rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: sr.Results[0].ChunkURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(rr.Contents) != 1 || !strings.Contains(rr.Contents[0].Text, "ERR_CONN_RESET") || !strings.Contains(rr.Contents[0].Text, "source: https://example.com/interceptors") {
		t.Fatalf("chunk resource: %+v", rr.Contents)
	}
	idx, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "memo://ns/grpc/index"})
	if err != nil || !strings.Contains(idx.Contents[0].Text, "gRPC Interceptors · memo://doc/") || len(idx.Contents[0].Text) > 8192 {
		t.Fatalf("namespace index: %v %+v", err, idx)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "memo://doc/does-not-exist"}); err == nil {
		t.Fatal("missing document should be a resource error")
	}
}

// promote asks the human through elicitation when the client can show a
// dialog; the model never answers it. Accept applies, decline does not.
func TestPromoteAsksTheHumanWhenTheClientCan(t *testing.T) {
	var seen string
	answer := "accept"
	cs := newTestSessionWith(t, &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		seen = req.Params.Message
		if answer == "accept" {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
		}
		return &mcp.ElicitResult{Action: answer}, nil
	}})
	ingestDoc(t, cs)
	rem := structured[RememberOut](t, callTool(t, cs, "remember", map[string]any{"statement": "Interceptors wrap every RPC.", "namespace": "grpc"}))

	res := callTool(t, cs, "promote", map[string]any{"uri": rem.URI, "to": "user"})
	if res.IsError {
		t.Fatalf("promote errored: %s", resultText(res))
	}
	pr := structured[PromoteOut](t, res)
	if !pr.Applied {
		t.Fatalf("accepted elicitation should apply: %+v", pr)
	}
	for _, must := range []string{rem.URI, "from agent to user", "Interceptors wrap every RPC.", "origin"} {
		if !strings.Contains(seen, must) {
			t.Errorf("dialog lacks %q:\n%s", must, seen)
		}
	}
	answer = "decline"
	pr = structured[PromoteOut](t, callTool(t, cs, "promote", map[string]any{"uri": rem.URI, "to": "curated"}))
	if pr.Applied || !strings.Contains(pr.Command, "memors-mcp trust promote") || !strings.Contains(pr.Reason, "declined") {
		t.Fatalf("declined elicitation: %+v", pr)
	}
	// Audit says who confirmed.
	sr := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "Interceptors wrap every RPC", "granularity": "fact"}))
	if len(sr.Results) != 1 || sr.Results[0].Provenance.Trust != "user" {
		t.Fatalf("trust after accept: %+v", sr.Results)
	}
}

// Without the capability the tool falls back to the command (the P4 path).
func TestPromoteFallsBackToCommandWithoutElicitation(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	rem := structured[RememberOut](t, callTool(t, cs, "remember", map[string]any{"statement": "Interceptors wrap every RPC.", "namespace": "grpc"}))
	pr := structured[PromoteOut](t, callTool(t, cs, "promote", map[string]any{"uri": rem.URI, "to": "user"}))
	if pr.Applied || !strings.Contains(pr.Command, "trust promote") || !strings.Contains(pr.Reason, "cannot ask") {
		t.Fatalf("fallback: %+v", pr)
	}
}

// The token budget footer is in the structured output and the text mirror
// for every format, not only in the explain trace.
func TestBudgetFooterInEveryFormat(t *testing.T) {
	cs, _ := newTestSession(t)
	for i := 0; i < 6; i++ {
		callTool(t, cs, "ingest", map[string]any{"content": "# Retry policy " + string(rune('A'+i)) + "\n\nRetries use exponential backoff with jitter for transient failures; the policy is configured per service in the service config.\n", "namespace": "grpc", "source": map[string]any{"uri": "https://example.com/retry/" + string(rune('a'+i)), "title": "Retry " + string(rune('A'+i)), "kind": "doc", "origin": "web"}})
	}
	res := callTool(t, cs, "search", map[string]any{"query": "exponential backoff jitter retry policy", "max_tokens": 60, "response_format": "concise"})
	out := structured[SearchOut](t, res)
	if out.Truncated == 0 || out.NarrowHint == "" {
		t.Fatalf("expected truncation: %+v", out)
	}
	if txt := resultText(res); !strings.Contains(txt, "did not fit the token budget") {
		t.Fatalf("text mirror lacks footer:\n%s", txt)
	}
}

// detailed shows the passage with its neighbours (small-to-big) so one call
// gives the context.
func TestDetailedReturnsNeighbourPassages(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	sr := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "ERR_CONN_RESET", "mode": "exact", "response_format": "detailed"}))
	if len(sr.Results) == 0 {
		t.Fatal("no results")
	}
	c := sr.Results[0].Content
	if !strings.Contains(c, "[…before:]") && !strings.Contains(c, "[…after:]") {
		t.Fatalf("detailed content has no neighbour markers:\n%s", c)
	}
}

// The README tool table names exactly the tools in the golden, so the two
// cannot drift apart silently.
func TestReadmeToolTableMatchesGolden(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "tools.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| ")
	var inReadme []string
	start := strings.Index(string(readme), "| Tool | Purpose |")
	if start < 0 {
		t.Fatal("README has no tool table")
	}
	section := string(readme)[start:]
	if end := strings.Index(section, "\n\n"); end > 0 {
		section = section[:end]
	}
	for _, m := range re.FindAllStringSubmatch(section, -1) {
		inReadme = append(inReadme, m[1])
	}
	var inGolden []string
	for _, tl := range golden {
		inGolden = append(inGolden, tl.Name)
	}
	sort.Strings(inReadme)
	sort.Strings(inGolden)
	if strings.Join(inReadme, ",") != strings.Join(inGolden, ",") {
		t.Fatalf("README tool table %v != golden %v", inReadme, inGolden)
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// explore walks the graph index (P7): the entity, its passages, and the
// things mentioned alongside it with evidence; the same text is a resource.
func TestExploreAndEntityResource(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	callTool(t, cs, "ingest", map[string]any{"content": "# Auth interceptor\n\nThe auth interceptor validates tokens before logging. On ERR_CONN_RESET it lets the Retry Policy decide.\n", "namespace": "grpc",
		"source": map[string]any{"uri": "https://example.com/auth", "title": "Auth interceptor", "kind": "doc", "origin": "web"}})
	res := callTool(t, cs, "explore", map[string]any{"entity": "ERR_CONN_RESET", "namespace": "grpc"})
	if res.IsError {
		t.Fatalf("explore: %s", resultText(res))
	}
	out := structured[ExploreOut](t, res)
	if out.Entity.Canonical != "ERR_CONN_RESET" || len(out.Chunks) < 2 {
		t.Fatalf("explore entity: %+v", out)
	}
	var sawRetry bool
	for _, n := range out.Neighbours {
		if n.Entity.Canonical == "Retry Policy" && len(n.Evidence) > 0 {
			sawRetry = true
		}
	}
	if !sawRetry {
		t.Fatalf("neighbours lack Retry Policy with evidence: %+v", out.Neighbours)
	}
	rr, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: out.Entity.URI})
	if err != nil || !strings.Contains(rr.Contents[0].Text, "Retry Policy") {
		t.Fatalf("entity resource: %v %+v", err, rr)
	}
	if res := callTool(t, cs, "explore", map[string]any{"entity": "nothing-here-at-all"}); !res.IsError {
		t.Fatal("unknown entity should be a tool error")
	}
	st := structured[StatusOut](t, callTool(t, cs, "status", map[string]any{}))
	if st.Graph.Entities == 0 || st.Graph.Mentions == 0 {
		t.Fatalf("status graph counts: %+v", st.Graph)
	}
	// A relational question routes the structural arms in and explains it.
	sr := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "how does ERR_CONN_RESET relate to the Retry Policy", "response_format": "explain"}))
	if sr.Trace == nil || !strings.Contains(sr.Trace.RoutingReason, "entity and graph arms added") {
		t.Fatalf("routing: %+v", sr.Trace)
	}
}

// compact → submit (dry run, then real) → the page is a resource and a
// search result at granularity=page, marked derived; revising a source
// marks it stale and compact proposes a rebuild.
func TestCompactSubmitRoundTrip(t *testing.T) {
	cs, _ := newTestSession(t)
	for i, body := range []string{
		"The Ledger Store is an append-only table of money movements.",
		"Durability of the Ledger Store comes from Quorum Replication across three regions.",
		"The Billing Service writes every invoice line to the Ledger Store.",
	} {
		callTool(t, cs, "ingest", map[string]any{"content": fmt.Sprintf("# Page %d\n\n%s\n", i, body), "namespace": "platform",
			"source": map[string]any{"uri": fmt.Sprintf("https://plat.example/%d", i), "title": fmt.Sprintf("Page %d", i), "kind": "doc", "origin": "web"}})
	}
	callTool(t, cs, "remember", map[string]any{"statement": "The Ledger Store keeps seven years of history.", "namespace": "platform", "about": []string{"Ledger Store"}})
	out := structured[CompactOut](t, callTool(t, cs, "compact", map[string]any{"namespace": "platform", "kinds": []string{"page"}, "lint": true}))
	var item *WorkItemOut
	for i := range out.Items {
		if raw, _ := json.Marshal(out.Items[i].Payload); strings.Contains(string(raw), "Ledger Store") {
			item = &out.Items[i]
		}
	}
	if item == nil {
		t.Fatalf("no page item: %+v", out)
	}
	var sawMissing bool
	for _, f := range out.Findings {
		if f.Kind == "missing-page" {
			sawMissing = true
		}
	}
	if !sawMissing {
		t.Fatalf("lint should flag the missing page: %+v", out.Findings)
	}
	dry := structured[SubmitOut](t, callTool(t, cs, "submit", map[string]any{"item_id": item.ID, "content": "# Ledger Store\n\nAppend-only table of money movements (memo://chunk/1).\n", "dry_run": true}))
	if dry.Applied || len(dry.Omitted) != 1 || dry.Diff == "" {
		t.Fatalf("dry run: %+v", dry)
	}
	page := "# Ledger Store\n\nAppend-only table of money movements (memo://chunk/1), replicated by Quorum Replication across three regions (memo://chunk/2). The Billing Service writes invoice lines to it (memo://chunk/3). It keeps seven years of history.\n"
	real := structured[SubmitOut](t, callTool(t, cs, "submit", map[string]any{"item_id": item.ID, "content": page}))
	if !real.Applied || real.PageURI == "" || len(real.Omitted) != 0 {
		t.Fatalf("submit: %+v", real)
	}
	rr, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: real.PageURI})
	if err != nil || !strings.Contains(rr.Contents[0].Text, "derived by") || !strings.Contains(rr.Contents[0].Text, "memo://chunk/1") {
		t.Fatalf("page resource: %v %+v", err, rr)
	}
	sr := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "money movements replicated regions", "granularity": "page"}))
	if len(sr.Results) != 1 || sr.Results[0].URI != real.PageURI || !sr.Results[0].Provenance.IsInference || sr.Results[0].Provenance.Stale {
		t.Fatalf("page search: %+v", sr.Results)
	}
	rd := structured[ReadOut](t, callTool(t, cs, "read", map[string]any{"uri": real.PageURI}))
	if rd.Provenance.Kind != "page" || rd.Provenance.Origin != "agent-derived" {
		t.Fatalf("read page: %+v", rd.Provenance)
	}
	// Revise a source: the page goes stale, search says so, compact proposes a rebuild.
	callTool(t, cs, "ingest", map[string]any{"content": "# Page 0\n\nThe Ledger Store is an append-only, partitioned table of money movements.\n", "namespace": "platform",
		"source": map[string]any{"uri": "https://plat.example/0", "title": "Page 0", "kind": "doc", "origin": "web"}})
	sr = structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "money movements", "granularity": "page"}))
	if len(sr.Results) != 1 || !sr.Results[0].Provenance.Stale {
		t.Fatalf("stale not reported: %+v", sr.Results)
	}
	out = structured[CompactOut](t, callTool(t, cs, "compact", map[string]any{"namespace": "platform", "kinds": []string{"stale"}}))
	if len(out.Items) != 1 || out.Items[0].Kind != "stale" {
		t.Fatalf("stale item: %+v", out)
	}
	st := structured[StatusOut](t, callTool(t, cs, "status", map[string]any{}))
	if st.Pages.Pages != 1 || st.Pages.StalePages != 1 || st.Pages.OpenWorkItems < 1 {
		t.Fatalf("status pages: %+v", st.Pages)
	}
}
