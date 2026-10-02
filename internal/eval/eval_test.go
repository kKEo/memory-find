package eval

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memory-find/internal/chunk"
	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

var updateBaseline = flag.Bool("update-baseline", false, "regenerate testdata/baseline.json from the current measured report")

// baselineTolerance absorbs floating-point noise on a 0..1 metric, not real
// regressions. P3 replaces the mean-only gate with a paired per-query one.
const baselineTolerance = 0.02

// TestRetrievalEval ingests the fixture corpus into a real knowledge base,
// runs every labelled query through the real retrieval service (hybrid
// keyword + exact + semantic, exactly as production uses it) with the
// deterministic hash embedder, and compares the metrics with the recorded
// baseline. Run with -update-baseline after a deliberate retrieval change
// once the new numbers are understood.
func TestRetrievalEval(t *testing.T) {
	ctx := context.Background()
	db, err := kb.Open(ctx, t.TempDir(), "eval", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := kb.NewStore(db, embedding.NewHashEmbedder(384))
	keyToID, err := Load(ctx, store, Corpus())
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	svc := retrieve.New(store, retrieve.Default, false)
	report, err := Run(ctx, svc, keyToID, Queries())
	if err != nil {
		t.Fatalf("run queries: %v", err)
	}
	logReport(t, report)

	// The long-document fixtures exist to prove chunking: their markers sit
	// in the last section, past where the journal's 1,500-rune cap ended.
	// Each must now be found by the SEMANTIC arm, not only by keyword.
	for _, q := range report.PerQuery {
		if q.Category == "long-document" && !containsStr(q.FirstHitArms, retrieve.ArmSemantic) {
			t.Errorf("%s: relevant hit did not come through the semantic arm (arms %v); chunking is not reaching the tail", q.QueryID, q.FirstHitArms)
		}
	}

	baselinePath := filepath.Join("testdata", "baseline.json")
	if *updateBaseline {
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
	checkNoRegression(t, "AbstentionRate", report.AbstentionRate, baseline.AbstentionRate)
	for cat, bm := range baseline.PerCategory {
		if gm, ok := report.PerCategory[cat]; ok {
			checkNoRegression(t, cat+"/NDCG10", gm.NDCG10, bm.NDCG10)
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func checkNoRegression(t *testing.T, name string, got, baseline float64) {
	t.Helper()
	if got < baseline-baselineTolerance {
		t.Errorf("%s regressed: got %.4f, baseline %.4f (tolerance %.2f)", name, got, baseline, baselineTolerance)
	}
}

func logReport(t *testing.T, r *Report) {
	t.Helper()
	t.Logf("%-32s %-14s %6s %6s %6s %6s %6s  %s", "query", "category", "R@1", "R@5", "R@10", "MRR", "NDCG10", "first hit via")
	for _, q := range r.PerQuery {
		extra := ""
		if q.NumIrrelevant > 0 {
			extra += fmt.Sprintf("  irrelevant_mean_rank=%.1f", q.IrrelevantMeanRank)
		}
		if q.Abstained != nil {
			extra += fmt.Sprintf("  abstained=%v (%d results)", *q.Abstained, q.NumResults)
		}
		t.Logf("%-32s %-14s %6.2f %6.2f %6.2f %6.2f %6.2f  %s%s", q.QueryID, q.Category, q.RecallAt1, q.RecallAt5, q.RecallAt10, q.MRR, q.NDCG10, strings.Join(q.FirstHitArms, "+"), extra)
	}
	t.Logf("%-32s %-14s %6.2f %6.2f %6.2f %6.2f %6.2f  abstention_rate=%.2f over %d queries", "MEAN", "", r.Mean.RecallAt1, r.Mean.RecallAt5, r.Mean.RecallAt10, r.Mean.MRR, r.Mean.NDCG10, r.AbstentionRate, r.NumAbstention)
	for cat, m := range r.PerCategory {
		t.Logf("  %-30s %-14s %6.2f %6.2f %6.2f %6.2f %6.2f", "category", cat, m.RecallAt1, m.RecallAt5, m.RecallAt10, m.MRR, m.NDCG10)
	}
}

func TestCorpusKeysAreUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, e := range Corpus() {
		if seen[e.Key] {
			t.Errorf("duplicate fixture key: %q", e.Key)
		}
		seen[e.Key] = true
	}
}

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

// The long fixtures must be long enough to need several chunks, or the
// long-document queries stop testing what they claim to test.
func TestLongFixturesNeedSeveralChunks(t *testing.T) {
	for _, e := range Corpus() {
		if !strings.HasPrefix(e.Key, "long-") {
			continue
		}
		md, _ := e.Input.Markdown()
		if n := len(chunk.Split(md, chunk.Options{})); n < 3 {
			t.Errorf("%s splits into only %d chunk(s)", e.Key, n)
		}
	}
}
