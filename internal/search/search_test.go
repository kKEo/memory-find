package search

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
)

type mockEmbedder struct {
	vec []float32
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, nil
}

var _ embedding.Embedder = (*mockEmbedder)(nil)

func fixedVec(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(i) * 0.001
	}
	return v
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := journal.InitDB(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedEntries(t *testing.T, db *sql.DB, emb embedding.Embedder) {
	t.Helper()
	mgr := journal.NewManager(db, emb)

	entries := []journal.ThoughtInput{
		{Reflections: "I feel frustrated with TypeScript type errors today."},
		{ProjectNotes: "The auth service needs a complete rewrite. Current implementation leaks sessions."},
		{TechnicalInsights: "Dependency injection makes testing much easier in Go."},
	}

	for _, e := range entries {
		if _, err := mgr.WriteThoughts(context.Background(), e); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func TestSearchReturnsResults(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.Search(context.Background(), "TypeScript frustration", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestListRecent(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.ListRecent(context.Background(), 2, 30)
	if err != nil {
		t.Fatalf("list recent: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestReadEntry(t *testing.T) {
	db := testDB(t)
	mgr := journal.NewManager(db, nil)

	id, err := mgr.WriteThoughts(context.Background(), journal.ThoughtInput{
		Reflections: "This is a test entry.",
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := NewService(db, nil)
	content, err := svc.ReadEntry(context.Background(), id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if content == "" {
		t.Fatal("expected non-empty content")
	}
}

func TestReadEntryNotFound(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)

	_, err := svc.ReadEntry(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent entry")
	}
}

func TestReadRecentEntries(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.ReadRecentEntries(context.Background(), 5)
	if err != nil {
		t.Fatalf("read recent: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Content == "" {
			t.Error("expected non-empty content")
		}
	}
}

func TestGenerateExcerpt(t *testing.T) {
	text := "This is a long text about TypeScript frustration and debugging problems in the codebase."

	excerpt := generateExcerpt(text, "TypeScript", 50)
	if excerpt == "" {
		t.Fatal("expected non-empty excerpt")
	}

	excerpt2 := generateExcerpt(text, "", 20)
	if len(excerpt2) > 24 { // 20 + "..."
		t.Errorf("excerpt too long: %q", excerpt2)
	}
}

func TestSectionFilter(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	results, err := svc.Search(context.Background(), "test", SearchOptions{
		Limit:    10,
		Sections: []string{"reflections"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	for _, r := range results {
		found := false
		for _, s := range r.Sections {
			if s == "reflections" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected reflections section in result, got %v", r.Sections)
		}
	}
}

func TestGetStats(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	seedEntries(t, db, emb)

	svc := NewService(db, emb)
	stats, err := svc.GetStats(context.Background())
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if stats.TotalEntries != 3 {
		t.Errorf("expected 3 entries, got %d", stats.TotalEntries)
	}

	if stats.EntriesWithEmbeddings != 3 {
		t.Errorf("expected 3 embeddings, got %d", stats.EntriesWithEmbeddings)
	}

	if len(stats.SectionCounts) == 0 {
		t.Error("expected section counts")
	}

	// Verify specific sections
	if stats.SectionCounts["reflections"] != 1 {
		t.Errorf("expected 1 reflections entry, got %d", stats.SectionCounts["reflections"])
	}
	if stats.SectionCounts["project_notes"] != 1 {
		t.Errorf("expected 1 project_notes entry, got %d", stats.SectionCounts["project_notes"])
	}
	if stats.SectionCounts["technical_insights"] != 1 {
		t.Errorf("expected 1 technical_insights entry, got %d", stats.SectionCounts["technical_insights"])
	}

	// Verify recent activity maps exist
	if _, ok := stats.RecentActivity["7d"]; !ok {
		t.Error("expected 7d recent activity")
	}
	if _, ok := stats.RecentActivity["30d"]; !ok {
		t.Error("expected 30d recent activity")
	}

	// All entries are recent (just created)
	if stats.RecentActivity["7d"] != 3 {
		t.Errorf("expected 3 entries in last 7 days, got %d", stats.RecentActivity["7d"])
	}
	if stats.RecentActivity["30d"] != 3 {
		t.Errorf("expected 3 entries in last 30 days, got %d", stats.RecentActivity["30d"])
	}

	if stats.AvgEntryLength == 0 {
		t.Error("expected non-zero average entry length")
	}

	if stats.EarliestEntry.IsZero() {
		t.Error("expected non-zero earliest entry time")
	}
	if stats.LatestEntry.IsZero() {
		t.Error("expected non-zero latest entry time")
	}
}

func TestGetStatsEmptyJournal(t *testing.T) {
	db := testDB(t)
	svc := NewService(db, nil)

	stats, err := svc.GetStats(context.Background())
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}

	if stats.TotalEntries != 0 {
		t.Errorf("expected 0 entries, got %d", stats.TotalEntries)
	}

	if stats.EntriesWithEmbeddings != 0 {
		t.Errorf("expected 0 embeddings, got %d", stats.EntriesWithEmbeddings)
	}

	if len(stats.SectionCounts) != 0 {
		t.Error("expected no section counts for empty journal")
	}
}

func TestFTS5Escape(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello world", `"hello" "world"`},
		{`say "hi" there`, `"say" "hi" "there"`},
		{"  spaced  ", `"spaced"`},
		{"single", `"single"`},
		{"", ""},
	}
	for _, tc := range tests {
		got := fts5Escape(tc.input)
		if got != tc.want {
			t.Errorf("fts5Escape(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestBM25OnlySearch(t *testing.T) {
	db := testDB(t)
	mgr := journal.NewManager(db, nil)

	inputs := []journal.ThoughtInput{
		{ProjectNotes: "Investigating the UUIDv7 implementation for distributed ID generation."},
		{ProjectNotes: "The weather is nice today, went for a long walk."},
		{TechnicalInsights: "UUIDv7 provides time-ordered unique identifiers unlike UUIDv4."},
	}
	for _, inp := range inputs {
		if _, err := mgr.WriteThoughts(context.Background(), inp); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(db, nil)
	results, err := svc.Search(context.Background(), "UUIDv7", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for UUIDv7, got %d", len(results))
	}
	for _, r := range results {
		if !strings.Contains(r.Content, "UUIDv7") {
			t.Errorf("result should contain UUIDv7: %s", r.Excerpt)
		}
	}
}

func TestHybridSearchMerge(t *testing.T) {
	db := testDB(t)
	emb := &mockEmbedder{vec: fixedVec(384)}
	mgr := journal.NewManager(db, emb)

	inputs := []journal.ThoughtInput{
		{ProjectNotes: "The ONNX runtime integration is complete."},
		{Reflections: "Thinking about machine learning model deployment strategies."},
		{Observations: "Unrelated entry about cooking pasta."},
	}
	for _, inp := range inputs {
		if _, err := mgr.WriteThoughts(context.Background(), inp); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(db, emb)
	results, err := svc.Search(context.Background(), "ONNX runtime", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if !strings.Contains(results[0].Content, "ONNX") {
		t.Errorf("expected ONNX entry ranked first, got: %s", results[0].Excerpt)
	}
}

func TestGenerateExcerptParagraphs(t *testing.T) {
	text := "## reflections\n\nI noticed something interesting about the auth flow.\n\n## project_notes\n\nThe authentication middleware leaks session tokens when under heavy load."

	excerpt := generateExcerpt(text, "authentication session", 200)
	if !strings.Contains(excerpt, "project_notes") {
		t.Errorf("expected section header in excerpt, got: %q", excerpt)
	}
	if !strings.Contains(excerpt, "authentication") {
		t.Errorf("expected matching paragraph in excerpt, got: %q", excerpt)
	}

	short := generateExcerpt(text, "", 30)
	if len(short) > 34 { // 30 + "..."
		t.Errorf("excerpt too long: %q (len=%d)", short, len(short))
	}
}

func TestTruncateAtBoundary(t *testing.T) {
	text := "First sentence. Second sentence. Third sentence is longer."
	got := truncateAtBoundary(text, 35)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ... suffix, got: %q", got)
	}
	if !strings.Contains(got, "Second sentence.") {
		t.Errorf("expected truncation at sentence boundary, got: %q", got)
	}
}

// topicEmbedder simulates real semantic similarity by mapping content keywords
// to vector dimensions. Entries about similar topics get similar vectors.
//
//	dim 0 = frontend (react, component, rendering, dom)
//	dim 1 = database (sql, postgres, migration, query)
//	dim 2 = emotions (frustrated, overwhelmed, stressed, burned, anxious)
//	dim 3 = grpc/rpc (grpc, protobuf, interceptor, rpc)
//	dim 4 = performance (performance, optimization, latency, speed, slow)
type topicEmbedder struct{}

func (e *topicEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.01
	}
	lower := strings.ToLower(text)
	topics := []struct {
		dim      int
		keywords []string
	}{
		{0, []string{"react", "frontend", "component", "rendering", "jsx", "dom"}},
		{1, []string{"database", "sql", "postgres", "migration", "query", "index"}},
		{2, []string{"frustrated", "overwhelmed", "stressed", "burned", "anxious"}},
		{3, []string{"grpc", "protobuf", "interceptor", "rpc"}},
		{4, []string{"performance", "optimization", "latency", "speed", "slow"}},
	}
	for _, topic := range topics {
		for _, kw := range topic.keywords {
			if strings.Contains(lower, kw) {
				vec[topic.dim] = 1.0
				break
			}
		}
	}
	return vec, nil
}

var _ embedding.Embedder = (*topicEmbedder)(nil)

func TestHybridSearchShowcase(t *testing.T) {
	db := testDB(t)
	emb := &topicEmbedder{}
	mgr := journal.NewManager(db, emb)

	entries := []journal.ThoughtInput{
		// Entry 1: frontend only — marker: "virtual DOM"
		{ProjectNotes: "React component re-renders causing unnecessary virtual DOM diffing."},
		// Entry 2: database only — marker: "EXPLAIN ANALYZE"
		{ProjectNotes: "PostgreSQL query planner chose sequential scan instead of index. Added EXPLAIN ANALYZE."},
		// Entry 3: emotions only — marker: "sprint deadline"
		{Reflections: "Feeling overwhelmed by the sprint deadline. Too many PRs to review."},
		// Entry 4: grpc exact keyword — marker: "UnaryInterceptor"
		{TechnicalInsights: "Implemented gRPC interceptors for auth. The UnaryInterceptor chains work well."},
		// Entry 5: frontend + performance — marker: "tree-shaking"
		{ProjectNotes: "Frontend rendering is slow on mobile. Investigating bundle size and tree-shaking."},
		// Entry 6: grpc-adjacent (no "gRPC" keyword) — marker: "binary RPC framework"
		{Observations: "Team discussed switching from REST to a binary RPC framework for internal services."},
		// Entry 7: database only — marker: "ALTER TABLE"
		{ProjectNotes: "Database migration failed in staging. ALTER TABLE locked the users table for 20 minutes."},
		// Entry 8: emotions only — marker: "context switching"
		{Reflections: "Burned out from context switching between three projects all week."},
	}
	for _, inp := range entries {
		if _, err := mgr.WriteThoughts(context.Background(), inp); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	svc := NewService(db, emb)

	containsMarker := func(results []SearchResult, marker string) bool {
		for _, r := range results {
			if strings.Contains(r.Content, marker) {
				return true
			}
		}
		return false
	}

	topN := func(results []SearchResult, n int) []SearchResult {
		if len(results) > n {
			return results[:n]
		}
		return results
	}

	t.Run("BM25_wins_exact_keyword_gRPC_interceptors", func(t *testing.T) {
		results, err := svc.Search(context.Background(), "gRPC interceptors", SearchOptions{Limit: 5})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}

		t.Logf("Results for 'gRPC interceptors':")
		for i, r := range results {
			t.Logf("  #%d [%.3f] %s", i+1, r.Score, r.Excerpt)
		}

		if !strings.Contains(results[0].Content, "UnaryInterceptor") {
			t.Errorf("expected gRPC interceptors entry (#4) ranked first, got: %s", results[0].Excerpt)
		}
		if !containsMarker(topN(results, 3), "binary RPC framework") {
			t.Error("expected RPC framework entry (#6) in top 3 via semantic similarity")
		}
	})

	t.Run("Vector_wins_semantic_match_emotions", func(t *testing.T) {
		results, err := svc.Search(context.Background(), "feeling anxious and burned out from too much work", SearchOptions{Limit: 5})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}

		t.Logf("Results for 'feeling anxious and burned out from too much work':")
		for i, r := range results {
			t.Logf("  #%d [%.3f] %s", i+1, r.Score, r.Excerpt)
		}

		top3 := topN(results, 3)
		if !containsMarker(top3, "sprint deadline") {
			t.Error("expected 'overwhelmed by sprint deadline' entry (#3) in top 3")
		}
		if !containsMarker(top3, "context switching") {
			t.Error("expected 'burned out from context switching' entry (#8) in top 3")
		}
	})

	t.Run("Hybrid_wins_double_signal_rendering_performance", func(t *testing.T) {
		results, err := svc.Search(context.Background(), "slow rendering performance", SearchOptions{Limit: 5})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected results")
		}

		t.Logf("Results for 'slow rendering performance':")
		for i, r := range results {
			t.Logf("  #%d [%.3f] %s", i+1, r.Score, r.Excerpt)
		}

		if !strings.Contains(results[0].Content, "tree-shaking") {
			t.Errorf("expected frontend+performance entry (#5) ranked first (double signal), got: %s", results[0].Excerpt)
		}
		if !containsMarker(topN(results, 3), "virtual DOM") {
			t.Error("expected React re-renders entry (#1) in top 3 via frontend semantic match")
		}
	})
}
