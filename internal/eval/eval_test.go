package eval

import (
	"context"
	"encoding/json"
	"flag"
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

// baselineTolerance applies to means and category means; single queries are
// gated by PairedGate's rank-band rule.
const baselineTolerance = 0.02

// loadSuite ingests both corpora into one fresh knowledge base with the
// hash embedder and returns the store and the key→id maps.
func loadSuite(t *testing.T) (*kb.Store, map[string]string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	db, err := kb.Open(ctx, t.TempDir(), "eval", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := kb.NewStore(db, embedding.NewHashEmbedder(384))
	notes, _, err := Load(ctx, store, ToDocs(Corpus()))
	if err != nil {
		t.Fatalf("load notes corpus: %v", err)
	}
	kbIDs, _, err := Load(ctx, store, CorpusKB())
	if err != nil {
		t.Fatalf("load kb corpus: %v", err)
	}
	factIDs, err := LoadFacts(ctx, store, kbIDs, FactsKB(), ForgottenDocsKB())
	if err != nil {
		t.Fatalf("load facts: %v", err)
	}
	for k, v := range factIDs {
		kbIDs[k] = v
	}
	return store, notes, kbIDs
}

// TestRetrievalEval is the gate: both corpora, the default profile, the hash
// embedder, compared query by query with the recorded baseline. Run with
// -update-baseline after a deliberate retrieval change once the new numbers
// are understood; the article for the phase must say what moved and why.
func TestRetrievalEval(t *testing.T) {
	ctx := context.Background()
	store, notesIDs, kbIDs := loadSuite(t)
	svc := retrieve.New(store, retrieve.Default, false)
	notes, err := Run(ctx, svc, notesIDs, Queries(), RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kbRep, err := Run(ctx, svc, kbIDs, append(QueriesKB(), QueriesFactsKB()...), RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	notes.Strategy, kbRep.Strategy = "default", "default"
	suite := Suite{Name: "hash/default", Notes: notes, KB: kbRep}
	t.Log("\n" + notes.Markdown("notes corpus (79 docs, 29 queries)"))
	t.Log("\n" + kbRep.Markdown("knowledge-base corpus (315 docs + 6 facts, 27 queries)"))

	for _, q := range append(notes.PerQuery, kbRep.PerQuery...) {
		if q.Category == "long-document" && !q.Skipped && !containsStr(q.FirstHitArms, retrieve.ArmSemantic) {
			t.Errorf("%s: relevant hit did not come through the semantic arm (arms %v)", q.QueryID, q.FirstHitArms)
		}
	}

	baselinePath := filepath.Join("testdata", "baseline.json")
	if *updateBaseline {
		b, _ := json.MarshalIndent(suite, "", "  ")
		if err := os.WriteFile(baselinePath, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", baselinePath)
		return
	}
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatalf("read baseline (run with -update-baseline to create it): %v", err)
	}
	var base Suite
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	for _, r := range PairedGate(notes, base.Notes, baselineTolerance) {
		t.Errorf("notes corpus regressed: %s", r)
	}
	for _, r := range PairedGate(kbRep, base.KB, baselineTolerance) {
		t.Errorf("kb corpus regressed: %s", r)
	}
}

// TestAblations prints the strategy comparison every run so the numbers are
// always one `go test -v` away; it gates only that each strategy runs.
func TestAblations(t *testing.T) {
	ctx := context.Background()
	store, _, kbIDs := loadSuite(t)
	var reports []*Report
	for _, name := range []string{"keyword-only", "semantic-only", "default", "minmax", "code"} {
		p, err := retrieve.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		r, err := Run(ctx, retrieve.New(store, p, false), kbIDs, QueriesKB(), RunOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		r.Strategy = name
		reports = append(reports, r)
	}
	r, err := Run(ctx, retrieve.New(store, retrieve.Default, false), kbIDs, QueriesKB(), RunOptions{AgentProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	r.Strategy = "agent-proxy (2 rounds, weak proxy)"
	reports = append(reports, r)
	t.Log("\n" + CompareMarkdown("knowledge-base corpus, hash embedder", reports))
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestCorpusKeysAreUnique(t *testing.T) {
	for _, docs := range [][]FixtureDoc{ToDocs(Corpus()), CorpusKB()} {
		seen := map[string]bool{}
		for _, d := range docs {
			if seen[d.Key] {
				t.Errorf("duplicate fixture key: %q", d.Key)
			}
			seen[d.Key] = true
		}
	}
}

func TestQueriesReferenceRealKeys(t *testing.T) {
	check := func(docs []FixtureDoc, queries []Query) {
		valid := map[string]bool{}
		for _, d := range docs {
			valid[d.Key] = true
		}
		for _, q := range queries {
			for _, k := range append(append([]string{}, q.Relevant...), q.Irrelevant...) {
				if !valid[k] {
					t.Errorf("query %q references unknown fixture key %q", q.ID, k)
				}
			}
		}
	}
	check(ToDocs(Corpus()), Queries())
	docs := CorpusKB()
	for _, f := range FactsKB() {
		docs = append(docs, FixtureDoc{Key: f.Key})
	}
	check(docs, append(QueriesKB(), QueriesFactsKB()...))
}

func TestLongFixturesNeedSeveralChunks(t *testing.T) {
	for _, d := range append(ToDocs(Corpus()), CorpusKB()...) {
		if !strings.HasPrefix(d.Key, "long-") {
			continue
		}
		if n := len(chunk.Split(d.Content, chunk.Options{})); n < 3 {
			t.Errorf("%s splits into only %d chunk(s)", d.Key, n)
		}
	}
}

func TestPairedGateCatchesSingleQueryLoss(t *testing.T) {
	base := &Report{PerQuery: []QueryMetrics{{QueryID: "a", MRR: 1, NDCG10: 1}, {QueryID: "b", MRR: 1, NDCG10: 1}}, Mean: QueryMetrics{MRR: 1, NDCG10: 1}, PerCategory: map[string]QueryMetrics{}}
	got := &Report{PerQuery: []QueryMetrics{{QueryID: "a", MRR: 1, NDCG10: 1}, {QueryID: "b", MRR: 0, NDCG10: 0}}, Mean: QueryMetrics{MRR: 0.5, NDCG10: 0.5}, PerCategory: map[string]QueryMetrics{}}
	regs := PairedGate(got, base, 0.02)
	if len(regs) < 2 {
		t.Fatalf("expected the single-query loss to be reported, got %v", regs)
	}
	if regs := PairedGate(base, base, 0.02); len(regs) != 0 {
		t.Fatalf("identical reports regressed: %v", regs)
	}
}
