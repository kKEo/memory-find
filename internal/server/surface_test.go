package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
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
	want := []string{"memo://doc/{id}", "memo://chunk/{id}", "memo://source/{id}", "memo://fact/{id}", "memo://ns/{namespace}/index"}
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
	if pr.Applied || !strings.Contains(pr.Command, "memo-mcp trust promote") || !strings.Contains(pr.Reason, "declined") {
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
