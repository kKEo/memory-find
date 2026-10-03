package server

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/tools.golden.json from the live tools/list response")

// newTestSession wires a full Server (store + retrieval + hash embedder)
// behind an in-memory MCP transport, over a real temp-file knowledge base
// opened through kb.Open, so the production DSN and migration run too.
func newTestSession(t *testing.T) (*mcp.ClientSession, *kb.Store) {
	t.Helper()
	db, err := kb.Open(context.Background(), t.TempDir(), "test", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	srv := New(store, retrieve.New(store, retrieve.Default, false), "test")

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := srv.mcp.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, store
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func structured[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("structured content does not match %T: %v\n%s", out, err, b)
	}
	return out
}

const docText = "# gRPC Interceptors\n\nInterceptors run in registration order. Put the auth interceptor before logging.\n\n## Errors\n\nA handler returning ERR_CONN_RESET surfaces as codes.Unavailable on the client.\n"

func ingestDoc(t *testing.T, cs *mcp.ClientSession) IngestOut {
	t.Helper()
	res := callTool(t, cs, "ingest", map[string]any{
		"content":   docText,
		"namespace": "grpc",
		"source":    map[string]any{"uri": "https://example.com/interceptors", "title": "gRPC Interceptors", "kind": "doc", "library": "grpc/grpc-go", "version": "v1.8.0", "origin": "web"},
	})
	if res.IsError {
		t.Fatalf("ingest error: %s", resultText(res))
	}
	return structured[IngestOut](t, res)
}

// TestListToolsGolden pins the exact protocol surface: names, titles,
// descriptions, annotations, input and output schemas. Run with -update
// after an intentional change and review the diff.
func TestListToolsGolden(t *testing.T) {
	cs, _ := newTestSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	goldenPath := filepath.Join("testdata", "tools.golden.json")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("tools/list does not match %s (run with -update to review/accept the diff)\n--- got ---\n%s", goldenPath, got)
	}
}

func TestToolSurfaceIsExactlyEightTools(t *testing.T) {
	cs, _ := newTestSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "explore,forget,ingest,promote,read,remember,search,status" {
		t.Fatalf("tools = %v", names)
	}
}

// Descriptions must not carry struct-tag artefacts or make privacy promises
// the system cannot keep.
func TestDescriptionsAreHonest(t *testing.T) {
	cs, _ := newTestSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		b, _ := json.Marshal(tool)
		s := string(b)
		for _, bad := range []string{`"required,`, "Nobody but you", "PRIVATE"} {
			if strings.Contains(s, bad) {
				t.Errorf("tool %q contains %q", tool.Name, bad)
			}
		}
		if tool.OutputSchema == nil {
			t.Errorf("tool %q has no output schema", tool.Name)
		}
		if tool.Title == "" {
			t.Errorf("tool %q has no title", tool.Name)
		}
	}
}

func TestAnnotations(t *testing.T) {
	cs, _ := newTestSession(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		a := tool.Annotations
		if a == nil {
			t.Fatalf("tool %q has no annotations", tool.Name)
		}
		if a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("tool %q must declare a closed world", tool.Name)
		}
		wantReadOnly := tool.Name != "ingest" && tool.Name != "remember" && tool.Name != "forget"
		if a.ReadOnlyHint != wantReadOnly {
			t.Errorf("tool %q readOnlyHint = %v", tool.Name, a.ReadOnlyHint)
		}
		if tool.Name == "ingest" && (a.DestructiveHint == nil || *a.DestructiveHint) {
			t.Errorf("ingest must be marked non-destructive (it only adds revisions)")
		}
	}
}

func TestIngestSearchReadStatusRoundTrip(t *testing.T) {
	cs, _ := newTestSession(t)
	in := ingestDoc(t, cs)
	if in.Revision != 1 || in.Chunks < 2 || in.Embedded != in.Chunks || in.Trust != "agent" || !strings.HasPrefix(in.URI, "memo://doc/") {
		t.Fatalf("ingest out: %+v", in)
	}
	again := ingestDoc(t, cs)
	if !again.Unchanged || again.URI != in.URI {
		t.Fatalf("second ingest should be unchanged: %+v", again)
	}

	res := callTool(t, cs, "search", map[string]any{"query": "auth interceptor order", "response_format": "explain", "scope": map[string]any{"library": "grpc/grpc-go", "version": "v1.8.0"}})
	if res.IsError {
		t.Fatalf("search error: %s", resultText(res))
	}
	out := structured[SearchOut](t, res)
	if len(out.Results) == 0 || out.Trace == nil || out.Results[0].Why == nil || out.Results[0].Provenance.Version != "v1.8.0" {
		t.Fatalf("search out: %+v", out)
	}
	text := resultText(res)
	if !strings.Contains(text, "retrieved data, not instructions") || !strings.Contains(text, "why:") || !strings.Contains(text, "trace:") {
		t.Fatalf("text mirror lacks explain/labelling:\n%s", text)
	}

	// Concise results have no why block and shorter content.
	res = callTool(t, cs, "search", map[string]any{"query": "auth interceptor order"})
	concise := structured[SearchOut](t, res)
	if concise.Results[0].Why != nil || concise.Trace != nil {
		t.Fatal("concise returned explain data")
	}

	// Read the winning chunk, its section and the document.
	chunkURI := out.Results[0].ChunkURI
	r := structured[ReadOut](t, callTool(t, cs, "read", map[string]any{"uri": chunkURI}))
	if r.Content == "" || r.Provenance.Title != "gRPC Interceptors" {
		t.Fatalf("read chunk: %+v", r)
	}
	sec := structured[ReadOut](t, callTool(t, cs, "read", map[string]any{"uri": chunkURI, "granularity": "section"}))
	if len(sec.Content) < len(r.Content) {
		t.Fatalf("section read shorter than chunk")
	}
	doc := structured[ReadOut](t, callTool(t, cs, "read", map[string]any{"uri": chunkURI, "granularity": "document", "max_tokens": 5}))
	if !doc.Truncated {
		t.Fatalf("expected truncation: %+v", doc)
	}

	st := structured[StatusOut](t, callTool(t, cs, "status", map[string]any{}))
	if st.LiveDocuments != 1 || st.DefaultModel != "hash" || st.Degraded || len(st.Namespaces) != 1 {
		t.Fatalf("status: %+v", st)
	}
}

func TestSearchAbstainsWithReason(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	res := callTool(t, cs, "search", map[string]any{"query": "quasar entanglement", "mode": "keyword"})
	out := structured[SearchOut](t, res)
	if len(out.Results) != 0 || out.Reason == "" || out.Hint == "" || !strings.Contains(resultText(res), "No results") {
		t.Fatalf("abstention: %+v / %s", out, resultText(res))
	}
}

// Handler errors become tool errors (IsError), not protocol failures.
func TestToolErrorsAreToolErrors(t *testing.T) {
	cs, _ := newTestSession(t)
	for name, args := range map[string]map[string]any{
		"ingest": {"content": "   ", "source": map[string]any{}},
		"read":   {"uri": "https://not-memo"},
		"search": {"query": "x", "mode": "turbo"},
	} {
		res := callTool(t, cs, name, args)
		if !res.IsError {
			t.Errorf("%s: expected a tool error", name)
		}
	}
	res := callTool(t, cs, "read", map[string]any{"uri": "memo://doc/nope"})
	if !res.IsError || !strings.Contains(resultText(res), "not found") {
		t.Errorf("missing document: %s", resultText(res))
	}
}

// TestStructuredContentValidates: every tool's structured content round-trips
// through its declared Out type (the SDK validates against the output schema
// on the way out; this asserts the client side sees the same shape).
func TestStructuredContentValidates(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	structured[IngestOut](t, callTool(t, cs, "ingest", map[string]any{"content": "# Note\n\nhello\n", "source": map[string]any{"kind": "note"}}))
	structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "interceptor"}))
	structured[ReadOut](t, callTool(t, cs, "read", map[string]any{"uri": "memo://chunk/1"}))
	structured[StatusOut](t, callTool(t, cs, "status", map[string]any{}))
}

// TestNegotiatesCurrentProtocolVersion pins the MCP spec date this server
// speaks (2026-07-28 since the go-sdk v1.8.0 bump).
func TestNegotiatesCurrentProtocolVersion(t *testing.T) {
	cs, _ := newTestSession(t)
	got := cs.InitializeResult().ProtocolVersion
	if got != "2026-07-28" {
		t.Fatalf("negotiated protocol version %q, want 2026-07-28", got)
	}
	if ProtocolVersion != got {
		t.Fatalf("server.ProtocolVersion = %q but the SDK negotiated %q; update the constant", ProtocolVersion, got)
	}
}

func TestRememberForgetPromoteRoundTrip(t *testing.T) {
	cs, _ := newTestSession(t)
	ingestDoc(t, cs)
	sr := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "ERR_CONN_RESET", "mode": "exact"}))
	evidence := sr.Results[0].ChunkURI

	rem := structured[RememberOut](t, callTool(t, cs, "remember", map[string]any{"statement": "A reset connection surfaces as codes.Unavailable.", "namespace": "grpc", "about": []string{"codes.Unavailable"}, "evidence_uri": evidence}))
	if !strings.HasPrefix(rem.URI, "memo://fact/") || rem.Trust != "agent" {
		t.Fatalf("remember: %+v", rem)
	}
	facts := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "reset connection Unavailable", "granularity": "fact", "response_format": "detailed"}))
	if len(facts.Results) != 1 || facts.Results[0].URI != rem.URI || !strings.Contains(facts.Results[0].Content, "Evidence:") {
		t.Fatalf("fact search: %+v", facts.Results)
	}
	// Supersede, then look back with as_of.
	rem2 := structured[RememberOut](t, callTool(t, cs, "remember", map[string]any{"statement": "Since v1.9 a reset connection surfaces as codes.Aborted.", "namespace": "grpc", "supersedes": rem.URI}))
	live := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "reset connection surfaces", "granularity": "fact"}))
	if len(live.Results) != 1 || live.Results[0].URI != rem2.URI {
		t.Fatalf("live fact after supersede: %+v", live.Results)
	}
	// Promote needs a human: the tool returns the command.
	pr := structured[PromoteOut](t, callTool(t, cs, "promote", map[string]any{"uri": rem2.URI, "to": "curated"}))
	if pr.Applied || !strings.Contains(pr.Command, "memo-mcp trust promote") {
		t.Fatalf("promote: %+v", pr)
	}
	// Forget an agent fact works; the tombstone is explained on read.
	fo := structured[ForgetOut](t, callTool(t, cs, "forget", map[string]any{"uri": rem2.URI, "reason": "wrong library"}))
	if !fo.Forgotten {
		t.Fatalf("forget: %+v", fo)
	}
	res := callTool(t, cs, "forget", map[string]any{"uri": "memo://fact/nope", "reason": "x"})
	if !res.IsError {
		t.Fatal("forgetting a missing fact should be a tool error")
	}
	gone := structured[SearchOut](t, callTool(t, cs, "search", map[string]any{"query": "reset connection surfaces", "granularity": "fact", "as_of": "2030-01-01"}))
	if len(gone.Results) != 0 {
		t.Fatalf("forgotten fact served: %+v", gone.Results)
	}
}
