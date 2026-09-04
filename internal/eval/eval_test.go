package eval

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
	"github.com/kmaziarz/memo-mcp/internal/search"
)

var updateBaseline = flag.Bool("update-baseline", false, "regenerate testdata/baseline.json from the current measured report")

// baselineTolerance is how far a mean metric is allowed to drift below its
// recorded baseline before the test fails. It exists to absorb
// floating-point noise, not to hide real regressions — 2 percentage
// points on a 0..1 metric.
const baselineTolerance = 0.02

// TestRetrievalEval seeds the fixture corpus, runs every labelled query
// against the real search.Service (hybrid vector+BM25, exactly as
// production uses it), and compares the resulting aggregate metrics
// against a recorded baseline. This is the harness the project roadmap
// calls for: retrieval changes should show up here as a measured
// before/after, not as a vibe.
//
// Run with -update-baseline after a deliberate retrieval change, once
// you've confirmed the new numbers in the test log are an improvement
// (or an accepted, understood tradeoff) rather than a regression.
func TestRetrievalEval(t *testing.T) {
	ctx := context.Background()

	dbPath := filepath.Join(t.TempDir(), "eval.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := journal.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	emb := embedding.NewHashEmbedder(384)
	keyToID, err := Load(ctx, db, emb, Corpus())
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}

	svc := search.NewService(db, emb)
	report, err := Run(ctx, svc, keyToID, Queries())
	if err != nil {
		t.Fatalf("run queries: %v", err)
	}

	logReport(t, report)

	baselinePath := filepath.Join("testdata", "baseline.json")

	if *updateBaseline {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(baselinePath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", baselinePath)
		return
	}

	baselineBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline (run with -update-baseline to create it): %v", err)
	}
	var baseline Report
	if err := json.Unmarshal(baselineBytes, &baseline); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}

	checkNoRegression(t, "RecallAt1", report.Mean.RecallAt1, baseline.Mean.RecallAt1)
	checkNoRegression(t, "RecallAt5", report.Mean.RecallAt5, baseline.Mean.RecallAt5)
	checkNoRegression(t, "RecallAt10", report.Mean.RecallAt10, baseline.Mean.RecallAt10)
	checkNoRegression(t, "MRR", report.Mean.MRR, baseline.Mean.MRR)
	checkNoRegression(t, "NDCG10", report.Mean.NDCG10, baseline.Mean.NDCG10)
}

func checkNoRegression(t *testing.T, name string, got, baseline float64) {
	t.Helper()
	if got < baseline-baselineTolerance {
		t.Errorf("%s regressed: got %.4f, baseline %.4f (tolerance %.2f)", name, got, baseline, baselineTolerance)
	}
}

func logReport(t *testing.T, r *Report) {
	t.Helper()
	t.Logf("%-32s %6s %6s %6s %6s %6s", "query", "R@1", "R@5", "R@10", "MRR", "NDCG10")
	for _, q := range r.PerQuery {
		extra := ""
		if q.NumIrrelevant > 0 {
			extra = fmt.Sprintf("  irrelevant_mean_rank=%.1f", q.IrrelevantMeanRank)
		}
		t.Logf("%-32s %6.2f %6.2f %6.2f %6.2f %6.2f%s",
			q.QueryID, q.RecallAt1, q.RecallAt5, q.RecallAt10, q.MRR, q.NDCG10, extra)
	}
	t.Logf("%-32s %6.2f %6.2f %6.2f %6.2f %6.2f", "MEAN", r.Mean.RecallAt1, r.Mean.RecallAt5, r.Mean.RecallAt10, r.Mean.MRR, r.Mean.NDCG10)
}

// TestCorpusKeysAreUnique guards a fixture-authoring mistake that would
// otherwise silently corrupt every query referencing the duplicated key
// (Load's keyToID map would just keep the last write).
func TestCorpusKeysAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range Corpus() {
		if seen[e.Key] {
			t.Errorf("duplicate fixture key: %q", e.Key)
		}
		seen[e.Key] = true
	}
}

// TestQueriesReferenceRealKeys guards the same class of mistake from the
// query side: a typo'd key in Relevant/Irrelevant would silently vanish
// (keysToIDs drops unknown keys) rather than failing loudly.
func TestQueriesReferenceRealKeys(t *testing.T) {
	valid := make(map[string]bool)
	for _, e := range Corpus() {
		valid[e.Key] = true
	}

	for _, q := range Queries() {
		for _, k := range append(append([]string{}, q.Relevant...), q.Irrelevant...) {
			if !valid[k] {
				t.Errorf("query %q references unknown fixture key %q", q.ID, k)
			}
		}
	}
}

// TestLongFillerExceedsEmbeddingCap is a sanity check on the long-entry
// fixtures' own premise: if longFiller ever got short enough that the
// marker section stopped being truncated away, the long-entry queries
// would silently stop testing what their comments say they test.
func TestLongFillerExceedsEmbeddingCap(t *testing.T) {
	const embeddingCapRunes = 1500 // journal.maxEmbedInputRunes, duplicated here deliberately: this test exists to catch drift between the two.

	filler := longFiller("some topic", 20)
	combined := filler + filler + filler // three sections' worth, as used in the long-* fixtures
	if len([]rune(combined)) <= embeddingCapRunes {
		t.Fatalf("longFiller fixtures no longer exceed the embedding cap (%d runes): got %d runes total; long-entry fixtures need updating", embeddingCapRunes, len([]rune(combined)))
	}
	if !strings.Contains(filler, "some topic") {
		t.Fatalf("longFiller did not include its topic")
	}
}
