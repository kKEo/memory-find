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
// relevantIDs, 0 otherwise) discounted by log2(rank+1). The ideal ranking
// is min(k, |relevant|) hits at the top: it is NOT clamped to the number of
// results returned, so a short list that misses relevant items is penalised
// (the journal-era harness inflated short lists; audit finding H3b).
func NDCGAtK(resultIDs, relevantIDs []string, k int) float64 {
	if len(relevantIDs) == 0 {
		return 0
	}
	var dcg float64
	for i := 0; i < k && i < len(resultIDs); i++ {
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
// in resultIDs. An id that does not appear is scored as k+1 ("just past the
// page"), where k is the evaluation depth, so the metric is comparable
// across queries regardless of how many results came back. (The journal-era
// harness used len(results)+1, which scored an absent irrelevant as rank 1
// on an empty list and so punished correct abstention; audit finding H3a.)
func MeanRank(resultIDs, ids []string, k int) float64 {
	if len(ids) == 0 {
		return 0
	}
	if k < len(resultIDs) {
		k = len(resultIDs)
	}
	var total float64
	for _, id := range ids {
		rank := k + 1
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
