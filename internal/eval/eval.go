package eval

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/journal"
	"github.com/kKEo/memory-find/internal/search"
)

// FixtureEntry is one journal entry in the eval corpus. Key is a stable,
// human-readable identifier that Query.Relevant/Irrelevant reference — it
// is not the database ID, which is a fresh UUIDv7 generated every time the
// corpus is loaded.
type FixtureEntry struct {
	Key   string
	Input journal.ThoughtInput
	// AgeDays backdates the entry's created_at by this many days, for
	// exercising recency-sensitive queries. 0 means "just written".
	AgeDays int
}

// Query is one labelled search to run against a loaded corpus.
type Query struct {
	ID    string
	Query string
	Opts  search.SearchOptions

	// Relevant lists fixture keys that count as correct matches,
	// most-relevant first. Drives recall@k, MRR, and nDCG.
	Relevant []string

	// Irrelevant lists fixture keys that are tempting false positives —
	// near-misses that share surface features with the query but should
	// not rank highly. Tracked via mean rank (see MeanRank) rather than a
	// hard pass/fail, since "absent" and "present but ranked low" both
	// count as success here.
	Irrelevant []string
}

// Load seeds db with the fixture corpus via embedder, returning a map from
// each FixtureEntry.Key to the database ID it was actually written under.
func Load(ctx context.Context, db *sql.DB, embedder embedding.Embedder, entries []FixtureEntry) (map[string]string, error) {
	mgr := journal.NewManager(db, embedder)
	keyToID := make(map[string]string, len(entries))

	for _, e := range entries {
		id, err := mgr.WriteThoughts(ctx, e.Input)
		if err != nil {
			return nil, fmt.Errorf("seed %q: %w", e.Key, err)
		}
		keyToID[e.Key] = id

		if e.AgeDays != 0 {
			createdAt := time.Now().AddDate(0, 0, -e.AgeDays).UnixMilli()
			if _, err := db.ExecContext(ctx, `UPDATE entries SET created_at = ? WHERE id = ?`, createdAt, id); err != nil {
				return nil, fmt.Errorf("backdate %q: %w", e.Key, err)
			}
		}
	}

	return keyToID, nil
}

// QueryMetrics holds the computed metrics for a single query.
type QueryMetrics struct {
	QueryID            string  `json:"query_id"`
	RecallAt1          float64 `json:"recall_at_1"`
	RecallAt5          float64 `json:"recall_at_5"`
	RecallAt10         float64 `json:"recall_at_10"`
	MRR                float64 `json:"mrr"`
	NDCG10             float64 `json:"ndcg_10"`
	IrrelevantMeanRank float64 `json:"irrelevant_mean_rank,omitempty"`
	NumRelevant        int     `json:"num_relevant"`
	NumIrrelevant      int     `json:"num_irrelevant,omitempty"`
}

// Report is the full result of running a query set against a corpus.
type Report struct {
	PerQuery []QueryMetrics `json:"per_query"`
	Mean     QueryMetrics   `json:"mean"`
}

// Run executes every query against svc and computes retrieval metrics for
// each, translating fixture keys to database IDs via keyToID (as produced
// by Load).
func Run(ctx context.Context, svc *search.Service, keyToID map[string]string, queries []Query) (*Report, error) {
	report := &Report{PerQuery: make([]QueryMetrics, 0, len(queries))}

	for _, q := range queries {
		opts := q.Opts
		if opts.Limit <= 0 {
			opts.Limit = 10
		}
		results, err := svc.Search(ctx, q.Query, opts)
		if err != nil {
			return nil, fmt.Errorf("query %q: %w", q.ID, err)
		}

		resultIDs := make([]string, len(results))
		for i, r := range results {
			resultIDs[i] = r.ID
		}

		relevantIDs := keysToIDs(keyToID, q.Relevant)
		irrelevantIDs := keysToIDs(keyToID, q.Irrelevant)

		m := QueryMetrics{
			QueryID:       q.ID,
			RecallAt1:     RecallAtK(resultIDs, relevantIDs, 1),
			RecallAt5:     RecallAtK(resultIDs, relevantIDs, 5),
			RecallAt10:    RecallAtK(resultIDs, relevantIDs, 10),
			MRR:           ReciprocalRank(resultIDs, relevantIDs),
			NDCG10:        NDCGAtK(resultIDs, relevantIDs, 10),
			NumRelevant:   len(relevantIDs),
			NumIrrelevant: len(irrelevantIDs),
		}
		if len(irrelevantIDs) > 0 {
			m.IrrelevantMeanRank = MeanRank(resultIDs, irrelevantIDs)
		}
		report.PerQuery = append(report.PerQuery, m)
	}

	report.Mean = meanOf(report.PerQuery)
	return report, nil
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

func meanOf(all []QueryMetrics) QueryMetrics {
	m := QueryMetrics{QueryID: "MEAN"}
	if len(all) == 0 {
		return m
	}

	var irrelevantCount int
	for _, q := range all {
		m.RecallAt1 += q.RecallAt1
		m.RecallAt5 += q.RecallAt5
		m.RecallAt10 += q.RecallAt10
		m.MRR += q.MRR
		m.NDCG10 += q.NDCG10
		if q.NumIrrelevant > 0 {
			m.IrrelevantMeanRank += q.IrrelevantMeanRank
			irrelevantCount++
		}
	}

	n := float64(len(all))
	m.RecallAt1 /= n
	m.RecallAt5 /= n
	m.RecallAt10 /= n
	m.MRR /= n
	m.NDCG10 /= n
	if irrelevantCount > 0 {
		m.IrrelevantMeanRank /= float64(irrelevantCount)
	}
	return m
}
