// Package eval provides a golden-set retrieval evaluation harness: a
// fixture corpus of journal entries, labelled queries over that corpus,
// and the standard information-retrieval metrics needed to tell whether a
// change to search.Service actually improved recall or just moved the
// problem around.
//
// This exists because the project shipped for months with a search engine
// that silently returned nothing for realistic queries (an FTS query
// builder that ANDed every word together, and an embedder that failed
// silently on long entries) while its unit test suite stayed green — the
// tests used constant-vector mock embedders, which make every entry
// equally "similar" to every query and so can never notice a ranking bug.
// A harness that reports recall@k, MRR, and nDCG against a fixed baseline
// is what turns "seems fine" into a number that regresses visibly in CI.
package eval

import "math"

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// RecallAtK returns the fraction of relevantIDs that appear within the
// first k entries of resultIDs. Returns 0 if relevantIDs is empty — there
// is nothing to recall, so no fraction of it was found.
func RecallAtK(resultIDs, relevantIDs []string, k int) float64 {
	if len(relevantIDs) == 0 {
		return 0
	}
	if k > len(resultIDs) {
		k = len(resultIDs)
	}
	top := resultIDs[:k]

	found := 0
	for _, rel := range relevantIDs {
		if contains(top, rel) {
			found++
		}
	}
	return float64(found) / float64(len(relevantIDs))
}

// ReciprocalRank returns 1/rank of the first relevant ID found in
// resultIDs (rank is 1-indexed), or 0 if none of relevantIDs appear at all.
func ReciprocalRank(resultIDs, relevantIDs []string) float64 {
	for i, id := range resultIDs {
		if contains(relevantIDs, id) {
			return 1.0 / float64(i+1)
		}
	}
	return 0
}

// NDCGAtK computes normalized discounted cumulative gain over the first k
// of resultIDs, using binary relevance (1 if a result ID is in
// relevantIDs, 0 otherwise) discounted by log2(rank+1).
func NDCGAtK(resultIDs, relevantIDs []string, k int) float64 {
	if len(relevantIDs) == 0 {
		return 0
	}
	if k > len(resultIDs) {
		k = len(resultIDs)
	}

	var dcg float64
	for i := 0; i < k; i++ {
		if contains(relevantIDs, resultIDs[i]) {
			dcg += 1.0 / math.Log2(float64(i+2)) // i is 0-indexed; rank i+1, discount log2(rank+1)
		}
	}

	idealHits := len(relevantIDs)
	if idealHits > k {
		idealHits = k
	}
	var idcg float64
	for i := 0; i < idealHits; i++ {
		idcg += 1.0 / math.Log2(float64(i+2))
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}

// MeanRank returns the average 1-based rank at which the given ids appear
// in resultIDs. An id that does not appear at all is scored as
// len(resultIDs)+1 ("just past the visible list") rather than excluded, so
// the metric stays meaningful and comparable across queries even when a
// correctly-irrelevant entry doesn't show up in the result set at all.
func MeanRank(resultIDs, ids []string) float64 {
	if len(ids) == 0 {
		return 0
	}
	var total float64
	for _, id := range ids {
		rank := len(resultIDs) + 1
		for i, r := range resultIDs {
			if r == id {
				rank = i + 1
				break
			}
		}
		total += float64(rank)
	}
	return total / float64(len(ids))
}
