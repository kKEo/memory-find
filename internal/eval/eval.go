// Package eval is the retrieval benchmark: a fixture corpus, labelled
// queries, and the standard information-retrieval metrics that turn "search
// feels better" into numbers a pull request can show. It runs the real
// store and the real retrieval service with the deterministic hash
// embedder, so it needs no model and runs in CI.
package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

// ThoughtInput is the fixture authoring shape inherited from the journal
// era: one text per section. Markdown() turns it into a document whose
// sections become headings (and whose section names become tags, so a
// section-scoped query is a tag-scoped query).
type ThoughtInput struct {
	Reflections       string
	Observations      string
	ProjectNotes      string
	UserContext       string
	TechnicalInsights string
	WorldKnowledge    string
}

var sectionOrder = []struct {
	name string
	get  func(ThoughtInput) string
}{
	{"reflections", func(t ThoughtInput) string { return t.Reflections }},
	{"observations", func(t ThoughtInput) string { return t.Observations }},
	{"project_notes", func(t ThoughtInput) string { return t.ProjectNotes }},
	{"user_context", func(t ThoughtInput) string { return t.UserContext }},
	{"technical_insights", func(t ThoughtInput) string { return t.TechnicalInsights }},
	{"world_knowledge", func(t ThoughtInput) string { return t.WorldKnowledge }},
}

// Markdown renders the sections as a document; Sections lists their names.
func (t ThoughtInput) Markdown() (string, []string) {
	var sb strings.Builder
	var names []string
	for _, s := range sectionOrder {
		if text := strings.TrimSpace(s.get(t)); text != "" {
			fmt.Fprintf(&sb, "## %s\n\n%s\n\n", s.name, text)
			names = append(names, s.name)
		}
	}
	return sb.String(), names
}

// FixtureEntry is one document in the eval corpus. Key is a stable,
// human-readable identifier that Query.Relevant/Irrelevant reference; the
// database id is minted fresh on every load.
type FixtureEntry struct {
	Key   string
	Input ThoughtInput
	// AgeDays backdates the document by this many days, for recency-sensitive
	// queries. 0 means "just written".
	AgeDays int
}

// Query is one labelled search.
type Query struct {
	ID    string
	Query string
	Scope retrieve.Scope
	// Category groups queries for per-category means (lookup, exact,
	// abstention, ...). Empty counts as "lookup".
	Category string
	// Relevant lists fixture keys that count as correct matches. An empty
	// Relevant means the right answer is to return nothing (abstention).
	Relevant []string
	// Irrelevant lists tempting false positives whose rank is tracked.
	Irrelevant []string
}

// Load ingests the corpus into store (kind=note, trust=user, one namespace),
// returning a map from fixture key to document id. Section names become tags.
func Load(ctx context.Context, store *kb.Store, entries []FixtureEntry) (map[string]string, error) {
	keyToID := make(map[string]string, len(entries))
	now := time.Now()
	for _, e := range entries {
		content, sections := e.Input.Markdown()
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("fixture %q has no content", e.Key)
		}
		res, err := store.Ingest(ctx, kb.IngestInput{
			Namespace: "eval", Content: "# " + e.Key + "\n\n" + content, Trust: kb.TrustUser, Channel: kb.ChannelCLI, Actor: "eval",
			Source: kb.SourceInput{Title: e.Key, Kind: kb.KindNote, Origin: kb.OriginUserSaid, Tags: sections},
		})
		if err != nil {
			return nil, fmt.Errorf("seed %q: %w", e.Key, err)
		}
		if res.Pending > 0 {
			return nil, fmt.Errorf("seed %q: %d chunk vectors pending; the eval needs an embedder", e.Key, res.Pending)
		}
		keyToID[e.Key] = res.DocumentID
		if e.AgeDays != 0 {
			ts := now.AddDate(0, 0, -e.AgeDays).UnixMilli()
			if _, err := store.DB().ExecContext(ctx, `UPDATE documents SET created_at = ?, updated_at = ? WHERE id = ?`, ts, ts, res.DocumentID); err != nil {
				return nil, fmt.Errorf("backdate %q: %w", e.Key, err)
			}
		}
	}
	return keyToID, nil
}

// QueryMetrics holds the metrics for one query.
type QueryMetrics struct {
	QueryID            string  `json:"query_id"`
	Category           string  `json:"category"`
	RecallAt1          float64 `json:"recall_at_1"`
	RecallAt5          float64 `json:"recall_at_5"`
	RecallAt10         float64 `json:"recall_at_10"`
	MRR                float64 `json:"mrr"`
	NDCG10             float64 `json:"ndcg_10"`
	IrrelevantMeanRank float64 `json:"irrelevant_mean_rank,omitempty"`
	NumRelevant        int     `json:"num_relevant"`
	NumIrrelevant      int     `json:"num_irrelevant,omitempty"`
	NumResults         int     `json:"num_results"`
	// Abstained is set for zero-relevant queries: true when the system
	// correctly returned nothing.
	Abstained *bool `json:"abstained,omitempty"`
	// FirstHitArms names the arms that returned the first relevant result,
	// so a report can say whether a hit came through keyword or meaning.
	FirstHitArms []string `json:"first_hit_arms,omitempty"`
	LatencyMs    float64  `json:"latency_ms"`
}

// Report is the result of a run. Means exclude zero-relevant queries from
// the ranking metrics (they have nothing to rank) and report their
// abstention rate separately.
type Report struct {
	PerQuery       []QueryMetrics          `json:"per_query"`
	Mean           QueryMetrics            `json:"mean"`
	PerCategory    map[string]QueryMetrics `json:"per_category"`
	AbstentionRate float64                 `json:"abstention_rate"`
	NumAbstention  int                     `json:"num_abstention_queries"`
}

// Run executes every query at document granularity and computes metrics.
func Run(ctx context.Context, svc *retrieve.Service, keyToID map[string]string, queries []Query) (*Report, error) {
	report := &Report{PerQuery: make([]QueryMetrics, 0, len(queries)), PerCategory: map[string]QueryMetrics{}}
	const k = 10
	for _, q := range queries {
		t0 := time.Now()
		resp, err := svc.Search(ctx, retrieve.Request{Query: q.Query, Scope: q.Scope, Granularity: retrieve.GranularityDocument, ResponseFormat: retrieve.FormatExplain, Limit: k, MaxTokens: 1 << 20})
		if err != nil {
			return nil, fmt.Errorf("query %q: %w", q.ID, err)
		}
		resultIDs := make([]string, len(resp.Results))
		for i, r := range resp.Results {
			_, id, _ := kb.ParseURI(r.DocumentURI)
			resultIDs[i] = id
		}
		relevantIDs := keysToIDs(keyToID, q.Relevant)
		irrelevantIDs := keysToIDs(keyToID, q.Irrelevant)
		m := QueryMetrics{QueryID: q.ID, Category: categoryOf(q), NumRelevant: len(relevantIDs), NumIrrelevant: len(irrelevantIDs), NumResults: len(resultIDs), LatencyMs: float64(time.Since(t0)) / 1e6}
		if len(relevantIDs) == 0 {
			abst := len(resultIDs) == 0
			m.Abstained = &abst
		} else {
			m.RecallAt1 = RecallAtK(resultIDs, relevantIDs, 1)
			m.RecallAt5 = RecallAtK(resultIDs, relevantIDs, 5)
			m.RecallAt10 = RecallAtK(resultIDs, relevantIDs, k)
			m.MRR = ReciprocalRank(resultIDs, relevantIDs)
			m.NDCG10 = NDCGAtK(resultIDs, relevantIDs, k)
			for i, r := range resp.Results {
				if contains(relevantIDs, resultIDs[i]) && r.Why != nil {
					for _, a := range r.Why.Arms {
						m.FirstHitArms = append(m.FirstHitArms, a.Arm)
					}
					break
				}
			}
		}
		if len(irrelevantIDs) > 0 {
			m.IrrelevantMeanRank = MeanRank(resultIDs, irrelevantIDs, k)
		}
		report.PerQuery = append(report.PerQuery, m)
	}
	report.Mean, report.AbstentionRate, report.NumAbstention = meanOf(report.PerQuery)
	for _, cat := range categories(report.PerQuery) {
		var subset []QueryMetrics
		for _, m := range report.PerQuery {
			if m.Category == cat {
				subset = append(subset, m)
			}
		}
		cm, _, _ := meanOf(subset)
		cm.QueryID, cm.Category = "MEAN", cat
		report.PerCategory[cat] = cm
	}
	return report, nil
}

func categoryOf(q Query) string {
	if q.Category != "" {
		return q.Category
	}
	if len(q.Relevant) == 0 {
		return "abstention"
	}
	return "lookup"
}

func categories(all []QueryMetrics) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range all {
		if !seen[m.Category] {
			seen[m.Category] = true
			out = append(out, m.Category)
		}
	}
	sort.Strings(out)
	return out
}

func keysToIDs(keyToID map[string]string, keys []string) []string {
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		if id, ok := keyToID[k]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// meanOf averages ranking metrics over queries that have relevant items and
// returns the abstention rate over those that do not.
func meanOf(all []QueryMetrics) (QueryMetrics, float64, int) {
	m := QueryMetrics{QueryID: "MEAN"}
	var ranked, irrelevantCount, abstQueries, abstained int
	for _, q := range all {
		if q.Abstained != nil {
			abstQueries++
			if *q.Abstained {
				abstained++
			}
			continue
		}
		ranked++
		m.RecallAt1 += q.RecallAt1
		m.RecallAt5 += q.RecallAt5
		m.RecallAt10 += q.RecallAt10
		m.MRR += q.MRR
		m.NDCG10 += q.NDCG10
		m.LatencyMs += q.LatencyMs
		if q.NumIrrelevant > 0 {
			m.IrrelevantMeanRank += q.IrrelevantMeanRank
			irrelevantCount++
		}
	}
	if ranked > 0 {
		n := float64(ranked)
		m.RecallAt1 /= n
		m.RecallAt5 /= n
		m.RecallAt10 /= n
		m.MRR /= n
		m.NDCG10 /= n
		m.LatencyMs /= n
	}
	if irrelevantCount > 0 {
		m.IrrelevantMeanRank /= float64(irrelevantCount)
	}
	rate := 0.0
	if abstQueries > 0 {
		rate = float64(abstained) / float64(abstQueries)
	}
	return m, rate, abstQueries
}
