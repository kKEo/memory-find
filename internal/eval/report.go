package eval

import (
	"fmt"
	"sort"
	"strings"
)

// Suite is the full eval: both corpora loaded into one store, the query sets
// run under one strategy. Baselines compare Suite results.
type Suite struct {
	Name  string     `json:"name"`
	Notes *Report    `json:"notes"`
	KB    *Report    `json:"kb"`
	Load  *LoadStats `json:"load,omitempty"`
}

// Markdown renders one report as a markdown table set.
func (r *Report) Markdown(title string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "### %s\n\n", title)
	fmt.Fprintf(&sb, "| metric | value |\n|---|---|\n| recall@1 | %.3f |\n| recall@5 | %.3f |\n| recall@10 | %.3f |\n| MRR | %.3f |\n| nDCG@10 | %.3f |\n| abstention rate | %.2f (%d queries) |\n| query p50 / p95 | %.1f ms / %.1f ms |\n| tokens returned p50 | %.0f |\n\n",
		r.Mean.RecallAt1, r.Mean.RecallAt5, r.Mean.RecallAt10, r.Mean.MRR, r.Mean.NDCG10, r.AbstentionRate, r.NumAbstention, r.Cost.QueryP50Ms, r.Cost.QueryP95Ms, r.Cost.TokensP50)
	cats := make([]string, 0, len(r.PerCategory))
	for c := range r.PerCategory {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	sb.WriteString("| category | n | recall@1 | recall@5 | recall@10 | MRR | nDCG@10 |\n|---|---|---|---|---|---|---|\n")
	counts := map[string]int{}
	for _, q := range r.PerQuery {
		if !q.Skipped {
			counts[q.Category]++
		}
	}
	for _, c := range cats {
		m := r.PerCategory[c]
		if c == "abstention" {
			fmt.Fprintf(&sb, "| %s | %d | – | – | – | – | abstention rate %.2f |\n", c, counts[c], r.AbstentionRate)
			continue
		}
		fmt.Fprintf(&sb, "| %s | %d | %.2f | %.2f | %.2f | %.2f | %.2f |\n", c, counts[c], m.RecallAt1, m.RecallAt5, m.RecallAt10, m.MRR, m.NDCG10)
	}
	sb.WriteString("\n| query | category | R@1 | R@5 | R@10 | MRR | nDCG | first hit via | note |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, q := range r.PerQuery {
		if q.Skipped {
			fmt.Fprintf(&sb, "| %s | %s | – | – | – | – | – | – | skipped (needs a real model) |\n", q.QueryID, q.Category)
			continue
		}
		note := ""
		switch {
		case q.Abstained != nil && *q.Abstained:
			note = "abstained (correct)"
		case q.Abstained != nil:
			note = fmt.Sprintf("did not abstain: %d results", q.NumResults)
		case q.NumIrrelevant > 0:
			note = fmt.Sprintf("irrelevant mean rank %.1f", q.IrrelevantMeanRank)
		}
		fmt.Fprintf(&sb, "| %s | %s | %.2f | %.2f | %.2f | %.2f | %.2f | %s | %s |\n", q.QueryID, q.Category, q.RecallAt1, q.RecallAt5, q.RecallAt10, q.MRR, q.NDCG10, strings.Join(q.FirstHitArms, "+"), note)
	}
	return sb.String()
}

// CompareMarkdown renders several strategies side by side (the ablation
// table): one row per strategy, quality and cost columns.
func CompareMarkdown(title string, reports []*Report) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "### %s\n\n| strategy | recall@1 | recall@5 | recall@10 | MRR | nDCG@10 | abstention | p50 ms | tokens p50 |\n|---|---|---|---|---|---|---|---|---|\n", title)
	for _, r := range reports {
		fmt.Fprintf(&sb, "| %s | %.3f | %.3f | %.3f | %.3f | %.3f | %.2f | %.1f | %.0f |\n", r.Strategy, r.Mean.RecallAt1, r.Mean.RecallAt5, r.Mean.RecallAt10, r.Mean.MRR, r.Mean.NDCG10, r.AbstentionRate, r.Cost.QueryP50Ms, r.Cost.TokensP50)
	}
	return sb.String()
}

// Regression is one paired difference the gate rejected.
type Regression struct {
	Where  string
	Metric string
	Got    float64
	Was    float64
}

func (r Regression) String() string {
	return fmt.Sprintf("%s: %s %.3f → %.3f", r.Where, r.Metric, r.Was, r.Got)
}

// PairedGate compares a report with its baseline query by query, not only by
// means (audit finding H3d: a mean-only 0.02 gate hid single-query losses).
// A query regresses when its nDCG@10 drops by more than one rank band
// (0.25) or its MRR drops from a hit to a miss; a category or overall mean
// regresses when it drops by more than tolerance; abstention may not fall.
func PairedGate(got, base *Report, tolerance float64) []Regression {
	var out []Regression
	byID := map[string]QueryMetrics{}
	for _, q := range base.PerQuery {
		byID[q.QueryID] = q
	}
	for _, q := range got.PerQuery {
		b, ok := byID[q.QueryID]
		if !ok || q.Skipped || b.Skipped {
			continue
		}
		if q.Abstained != nil && b.Abstained != nil {
			if *b.Abstained && !*q.Abstained {
				out = append(out, Regression{q.QueryID, "abstained", 0, 1})
			}
			continue
		}
		if b.NDCG10-q.NDCG10 > 0.25 {
			out = append(out, Regression{q.QueryID, "nDCG@10", q.NDCG10, b.NDCG10})
		}
		if b.MRR > 0 && q.MRR == 0 {
			out = append(out, Regression{q.QueryID, "MRR", q.MRR, b.MRR})
		}
	}
	check := func(where, metric string, g, b float64) {
		if g < b-tolerance {
			out = append(out, Regression{where, metric, g, b})
		}
	}
	check("mean", "recall@1", got.Mean.RecallAt1, base.Mean.RecallAt1)
	check("mean", "recall@5", got.Mean.RecallAt5, base.Mean.RecallAt5)
	check("mean", "recall@10", got.Mean.RecallAt10, base.Mean.RecallAt10)
	check("mean", "MRR", got.Mean.MRR, base.Mean.MRR)
	check("mean", "nDCG@10", got.Mean.NDCG10, base.Mean.NDCG10)
	check("mean", "abstention rate", got.AbstentionRate, base.AbstentionRate)
	for c, bm := range base.PerCategory {
		if gm, ok := got.PerCategory[c]; ok && c != "abstention" {
			check("category "+c, "nDCG@10", gm.NDCG10, bm.NDCG10)
		}
	}
	return out
}
