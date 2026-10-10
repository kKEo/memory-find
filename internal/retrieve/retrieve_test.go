package retrieve

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
)

func newStore(t *testing.T, emb embedding.Embedder) *kb.Store {
	t.Helper()
	db, err := kb.Open(context.Background(), t.TempDir(), "t", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return kb.NewStore(db, emb)
}

func ingest(t *testing.T, s *kb.Store, ns, uri, title, kind, version, content string) *kb.IngestResult {
	t.Helper()
	origin := kb.OriginWeb
	if uri == "" {
		origin = kb.OriginUserSaid
	}
	res, err := s.Ingest(context.Background(), kb.IngestInput{Namespace: ns, Content: content, Trust: kb.TrustAgent, Channel: kb.ChannelTool,
		Source: kb.SourceInput{URI: uri, Title: title, Kind: kind, Version: version, Origin: origin, Library: "grpc/grpc-go"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func seed(t *testing.T, s *kb.Store) {
	ingest(t, s, "grpc", "https://example.com/interceptors", "gRPC Interceptors", kb.KindDoc, "v1.8.0",
		"# gRPC Interceptors\n\nInterceptors run in registration order. Put the auth interceptor before logging.\n\n## Errors\n\nA handler returning ERR_CONN_RESET surfaces as codes.Unavailable on the client.\n")
	ingest(t, s, "grpc", "https://example.com/streaming", "gRPC Streaming", kb.KindDoc, "v1.8.0",
		"# Streaming\n\nServer streaming sends many messages for one request. Use context cancellation to stop a stream.\n")
	ingest(t, s, "personal", "", "Lunch walk", kb.KindNote, "",
		"Went for a long walk by the river after lunch and watched the ducks.\n")
	ingest(t, s, "personal", "", "Deploy checklist", kb.KindNote, "",
		"Before deploying: run make check, bump the version, write the changelog.\n")
}

func TestHybridSearchFindsAndExplains(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	svc := New(s, Default, false)
	resp, err := svc.Search(context.Background(), Request{Query: "auth interceptor order", ResponseFormat: FormatExplain})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || !strings.Contains(resp.Results[0].Title, "Interceptors") {
		t.Fatalf("top result: %+v", resp.Results)
	}
	top := resp.Results[0]
	if top.Why == nil || top.Relevance == nil || top.Band == "" || top.Provenance.Namespace != "grpc" || top.Provenance.Version != "v1.8.0" {
		t.Fatalf("explain missing: %+v", top)
	}
	var sum float64
	for _, a := range top.Why.Arms {
		sum += a.Contribution
		if a.Rank == nil || *a.Rank < 1 {
			t.Errorf("arm %s has no rank", a.Arm)
		}
	}
	if math.Abs(sum-top.Why.Fused) > 1e-9 {
		t.Errorf("contributions %.6f != fused %.6f", sum, top.Why.Fused)
	}
	if top.Why.Final != top.Why.Fused*top.Why.RecencyFactor || top.Score != top.Why.Final {
		t.Errorf("final/score mismatch: %+v", top.Why)
	}
	if resp.Trace == nil || resp.Trace.ModeResolved != "semantic+keyword+fact" || resp.Trace.Filtered.LiveDocs != 4 || resp.Trace.Degraded.Flag {
		t.Fatalf("trace: %+v", resp.Trace)
	}
	// Keyword arm reports the words it matched.
	kw := armByName(top.Why.Arms, ArmKeyword)
	if kw == nil || len(kw.MatchedTerms) == 0 {
		t.Errorf("keyword arm has no matched terms: %+v", kw)
	}
	// A doc has recency 1.0; a note would not.
	if top.Why.RecencyFactor != 1 {
		t.Errorf("doc recency factor = %v", top.Why.RecencyFactor)
	}
}

func armByName(arms []ArmHit, name string) *ArmHit {
	for i := range arms {
		if arms[i].Arm == name {
			return &arms[i]
		}
	}
	return nil
}

func TestAutoAddsExactArmForIdentifiers(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	svc := New(s, Default, false)
	resp, err := svc.Search(context.Background(), Request{Query: "ERR_CONN_RESET", ResponseFormat: FormatExplain})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.Trace.ModeResolved, "semantic+keyword+exact+fact") {
		t.Fatalf("mode resolved %q (%s)", resp.Trace.ModeResolved, resp.Trace.RoutingReason)
	}
	if len(resp.Results) == 0 || !strings.Contains(resp.Results[0].Content, "ERR_CONN_RESET") && !strings.Contains(resp.Results[0].Title, "Interceptors") {
		t.Fatalf("identifier not found: %+v", resp.Results)
	}
	if ex := armByName(resp.Results[0].Why.Arms, ArmExact); ex == nil || !contains(ex.MatchedTerms, "err_conn_reset") {
		t.Errorf("exact arm did not report the identifier: %+v", ex)
	}
	// mode=exact alone works too.
	resp, _ = svc.Search(context.Background(), Request{Query: "codes.Unavailable", Mode: ModeExact})
	if len(resp.Results) != 1 {
		t.Fatalf("exact mode: %+v", resp.Results)
	}
}

// synonymEmbedder is a hash embedder that understands a few synonyms, so a
// test can build documents that are semantically close to a query without
// sharing a single keyword with it (vector-only candidates).
type synonymEmbedder struct {
	*embedding.HashEmbedder
	syn map[string]string
}

func (e *synonymEmbedder) norm(text string) string {
	words := strings.Fields(strings.ToLower(text))
	for i, w := range words {
		if r, ok := e.syn[strings.Trim(w, ".,;:")]; ok {
			words[i] = r
		}
	}
	return strings.Join(words, " ")
}

func (e *synonymEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.HashEmbedder.Embed(ctx, e.norm(text))
}

func (e *synonymEmbedder) EmbedBatch(ctx context.Context, texts []string, role embedding.Role) ([][]float32, error) {
	out := make([]string, len(texts))
	for i, t := range texts {
		out[i] = e.norm(t)
	}
	return e.HashEmbedder.EmbedBatch(ctx, out, role)
}

// The keyword arm must be able to ADD a result the semantic arm did not
// return, not only reorder what it returned (the audit's H2 finding on the
// journal). Forty decoys are semantically close to the query but share no
// keyword with it; the target shares the query's rare identifier and nothing
// else. The target must be on the first page.
func TestKeywordOnlyHitReachesFirstPage(t *testing.T) {
	emb := &synonymEmbedder{HashEmbedder: embedding.NewHashEmbedder(64), syn: map[string]string{
		"shipping": "deployment", "shipped": "deployment", "conveyor": "pipeline", "launch": "rollout", "launches": "rollout"}}
	s := newStore(t, emb)
	for i := 0; i < 40; i++ {
		ingest(t, s, "ns", fmt.Sprintf("https://x/decoy-%d", i), fmt.Sprintf("Decoy %d", i), kb.KindDoc, "",
			strings.Repeat("shipping conveyor launch shipped conveyor launches ", 6)+"\n")
	}
	ingest(t, s, "ns", "https://x/target", "Incident", kb.KindDoc, "",
		"Postmortem of the outage: the root cause was ZEBRA7734, a misconfigured certificate chain on the edge.\n")
	svc := New(s, Default, false)
	for _, mode := range []string{ModeHybrid, ModeAuto} {
		resp, err := svc.Search(context.Background(), Request{Query: "deployment pipeline rollout ZEBRA7734", Mode: mode, ResponseFormat: FormatExplain, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		found := -1
		for i, r := range resp.Results {
			if r.Title == "Incident" {
				found = i
				// The scenario is only meaningful if semantic search alone
				// would have left the target off the first page.
				if sem := armByName(r.Why.Arms, ArmSemantic); sem != nil && sem.Rank != nil && *sem.Rank <= 10 {
					t.Fatalf("test setup: semantic arm ranked the target %d, decoys are not dominant enough", *sem.Rank)
				}
			}
		}
		if found < 0 {
			t.Fatalf("mode %s: keyword-only target not on the first page (%d results)", mode, len(resp.Results))
		}
	}
}

func TestDegradedWithoutEmbedder(t *testing.T) {
	s := newStore(t, nil)
	seed(t, s)
	svc := New(s, Default, false)
	resp, err := svc.Search(context.Background(), Request{Query: "interceptor order", ResponseFormat: FormatExplain})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Degraded || !resp.Trace.Degraded.Flag || resp.Trace.ModeResolved != "keyword+fact" {
		t.Fatalf("expected degraded keyword-only search: %+v", resp.Trace)
	}
	if len(resp.Results) == 0 || resp.Results[0].Relevance != nil || resp.Results[0].Band != "keyword-only" {
		t.Fatalf("keyword-only result shape: %+v", resp.Results)
	}
	// Semantic-only mode with no embedder cannot run at all → abstention.
	resp, err = svc.Search(context.Background(), Request{Query: "interceptor", Mode: ModeSemantic})
	if err != nil || len(resp.Results) != 0 || resp.Reason == "" {
		t.Fatalf("expected abstention: %+v %v", resp, err)
	}
}

func TestScopeNamespaceVersionAndTrust(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	// A newer version of the interceptors page supersedes v1.8.0.
	ingest(t, s, "grpc", "https://example.com/interceptors", "gRPC Interceptors", kb.KindDoc, "v1.9.0",
		"# gRPC Interceptors\n\nInterceptors now run in reverse registration order (changed in v1.9.0).\n")
	svc := New(s, Default, false)
	ctx := context.Background()

	// Default: only the latest revision is live.
	resp, _ := svc.Search(ctx, Request{Query: "interceptors registration order", Granularity: GranularityDocument})
	for _, r := range resp.Results {
		if r.Provenance.Version == "v1.8.0" && strings.Contains(r.Title, "Interceptors") {
			t.Fatalf("superseded revision returned without a version scope")
		}
	}
	// Version scope reaches the superseded revision.
	resp, _ = svc.Search(ctx, Request{Query: "interceptors registration order", Scope: Scope{Version: "v1.8.0"}, Granularity: GranularityDocument, ResponseFormat: FormatExplain})
	if len(resp.Results) == 0 || resp.Results[0].Provenance.Version != "v1.8.0" {
		t.Fatalf("version scope: %+v", resp.Results)
	}
	// Namespace scope excludes the other shelf and counts it as filtered.
	resp, _ = svc.Search(ctx, Request{Query: "walk river ducks", Scope: Scope{Namespaces: []string{"grpc"}}, ResponseFormat: FormatExplain})
	for _, r := range resp.Results {
		if r.Provenance.Namespace != "grpc" {
			t.Fatalf("namespace scope leaked: %+v", r)
		}
	}
	if resp.Trace != nil && resp.Trace.Filtered.LiveDocs != 2 {
		t.Errorf("live docs in grpc scope = %d", resp.Trace.Filtered.LiveDocs)
	}
	// min_trust=user excludes agent-written content entirely → abstention.
	resp, _ = svc.Search(ctx, Request{Query: "interceptors", Scope: Scope{MinTrust: kb.TrustUser}, ResponseFormat: FormatExplain})
	if len(resp.Results) != 0 || resp.Trace.Filtered.ByMinTrust == 0 {
		t.Fatalf("min_trust: %+v", resp)
	}
}

func TestBudgetFormatsExcludeAndLog(t *testing.T) {
	db, err := kb.Open(context.Background(), t.TempDir(), "t", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := kb.NewStore(db, embedding.NewHashEmbedder(64))
	seed(t, s)
	ctx := context.Background()

	// Concise is a one-liner, detailed is the passage.
	svc := New(s, Default, false)
	concise, _ := svc.Search(ctx, Request{Query: "interceptor logging auth", ResponseFormat: FormatConcise})
	detailed, _ := svc.Search(ctx, Request{Query: "interceptor logging auth", ResponseFormat: FormatDetailed})
	if len(concise.Results) == 0 || len(concise.Results[0].Content) > len(detailed.Results[0].Content) || concise.Results[0].Why != nil {
		t.Fatalf("formats: %q vs %q", concise.Results[0].Content, detailed.Results[0].Content)
	}

	// A tiny budget truncates and explains how to narrow.
	small, _ := svc.Search(ctx, Request{Query: "interceptor stream deploy walk", ResponseFormat: FormatExplain, MaxTokens: 40, Limit: 10})
	if len(small.Results) != 1 || small.Trace.Cutoff.Kind != "budget" || small.Trace.Budget.TruncatedCount == 0 || small.Trace.Budget.NarrowHint == "" {
		t.Fatalf("budget: %d results, cutoff %+v, budget %+v", len(small.Results), small.Trace.Cutoff, small.Trace.Budget)
	}

	// exclude_ids removes already-seen results.
	all, _ := svc.Search(ctx, Request{Query: "interceptor stream deploy walk", Limit: 10})
	excl, _ := svc.Search(ctx, Request{Query: "interceptor stream deploy walk", Limit: 10, ExcludeIDs: []string{all.Results[0].URI}})
	for _, r := range excl.Results {
		if r.URI == all.Results[0].URI {
			t.Fatal("excluded id returned")
		}
	}

	// Query log: off by default, on when asked, never stores chunk text.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM query_log`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("query_log rows without logging: %d %v", n, err)
	}
	logged := New(s, Default, true)
	if _, err := logged.Search(ctx, Request{Query: "interceptor"}); err != nil {
		t.Fatal(err)
	}
	var argsJSON, top string
	if err := db.QueryRow(`SELECT args_json, top_uris_json FROM query_log`).Scan(&argsJSON, &top); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(argsJSON, "interceptor") || !strings.Contains(top, "memo://") || strings.Contains(top, "registration order") {
		t.Fatalf("query log content: %s %s", argsJSON, top)
	}
}

func TestDocumentGranularityDedupes(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	var sb strings.Builder
	sb.WriteString("# Long\n\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&sb, "## Part %d\n\n%s\n\n", i, strings.Repeat("interceptor ordering matters for auth and logging. ", 25))
	}
	ingest(t, s, "ns", "https://x/long", "Long", kb.KindDoc, "", sb.String())
	svc := New(s, Default, false)
	chunks, _ := svc.Search(context.Background(), Request{Query: "interceptor ordering auth", Granularity: GranularityChunk, Limit: 20})
	docs, _ := svc.Search(context.Background(), Request{Query: "interceptor ordering auth", Granularity: GranularityDocument, Limit: 20})
	if len(chunks.Results) < 2 || len(docs.Results) != 1 || !strings.HasPrefix(docs.Results[0].URI, "memo://doc/") {
		t.Fatalf("chunks=%d docs=%d", len(chunks.Results), len(docs.Results))
	}
}

func TestAbstentionAndValidation(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	svc := New(s, Default, false)
	resp, err := svc.Search(context.Background(), Request{Query: "quasar entanglement", Mode: ModeKeyword})
	if err != nil || len(resp.Results) != 0 || resp.Reason != "no arm matched" || resp.Hint == "" {
		t.Fatalf("abstention: %+v %v", resp, err)
	}
	if _, err := svc.Search(context.Background(), Request{Query: "x", Mode: "turbo"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	if _, err := svc.Search(context.Background(), Request{Query: "   "}); err == nil {
		t.Fatal("empty query accepted")
	}
}

func TestRecencyOnlyForNotesAndClamped(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	svc := New(s, Default, false)
	svc.now = func() time.Time { return time.Now().Add(-365 * 24 * time.Hour) } // "now" a year before the writes: future-dated content
	resp, _ := svc.Search(context.Background(), Request{Query: "deploy checklist changelog", ResponseFormat: FormatExplain})
	for _, r := range resp.Results {
		if r.Provenance.Kind == kb.KindNote && r.Why.RecencyFactor != 1 {
			t.Fatalf("future-dated note should clamp to factor 1, got %v", r.Why.RecencyFactor)
		}
	}
}

func TestHelpers(t *testing.T) {
	if ftsQueryStemmed(`foo* -bar (baz) "q"`) != `"foo" OR "bar" OR "baz"` {
		t.Error(ftsQueryStemmed(`foo* -bar (baz) "q"`))
	}
	if ftsQueryStemmed("the order of the interceptors") != `"order" OR "interceptors"` {
		t.Error(ftsQueryStemmed("the order of the interceptors"))
	}
	if ftsQueryStemmed("to be or not") == "" {
		t.Error("an all-stopword query must keep its words")
	}
	if ftsQueryExact("call net/http and useCallback, then ERR_CONN_RESET.") != `"call" OR "net/http" OR "useCallback" OR "ERR_CONN_RESET"` {
		t.Error(ftsQueryExact("call net/http and useCallback, then ERR_CONN_RESET."))
	}
	for q, want := range map[string]bool{"how do interceptors work": false, "useCallback fires twice": true, "ERR_CONN_RESET": true, "net/http handler": true, "codes.Unavailable meaning": true, "release v1.8": false} {
		if looksLikeIdentifier(q) != want {
			t.Errorf("looksLikeIdentifier(%q) = %v", q, !want)
		}
	}
	if got := extractMarked("a \x01foo\x02 b \x01Bar\x02 \x01foo\x02"); len(got) != 2 || got[1] != "bar" {
		t.Error(got)
	}
	if l := oneLiner(strings.Repeat("word ", 100)); len([]rune(l)) > 165 {
		t.Error("one-liner too long")
	}
}
