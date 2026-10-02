package eval

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	return d < eps && d > -eps
}

func TestRecallAtK(t *testing.T) {
	results := []string{"a", "b", "c", "d", "e"}

	if got := RecallAtK(results, nil, 5); got != 0 {
		t.Errorf("empty relevant set: got %v, want 0", got)
	}
	if got := RecallAtK(results, []string{"a"}, 1); !almostEqual(got, 1) {
		t.Errorf("single relevant at rank 1: got %v, want 1", got)
	}
	if got := RecallAtK(results, []string{"e"}, 1); !almostEqual(got, 0) {
		t.Errorf("relevant outside top-1: got %v, want 0", got)
	}
	if got := RecallAtK(results, []string{"a", "c"}, 5); !almostEqual(got, 1) {
		t.Errorf("both relevant found in top-5: got %v, want 1", got)
	}
	if got := RecallAtK(results, []string{"a", "z"}, 5); !almostEqual(got, 0.5) {
		t.Errorf("half of relevant found: got %v, want 0.5", got)
	}
	// k larger than the result list must not panic or overcount.
	if got := RecallAtK(results, []string{"a"}, 100); !almostEqual(got, 1) {
		t.Errorf("k > len(results): got %v, want 1", got)
	}
}

func TestReciprocalRank(t *testing.T) {
	results := []string{"a", "b", "c"}

	if got := ReciprocalRank(results, []string{"a"}); !almostEqual(got, 1.0) {
		t.Errorf("rank 1: got %v, want 1.0", got)
	}
	if got := ReciprocalRank(results, []string{"b"}); !almostEqual(got, 0.5) {
		t.Errorf("rank 2: got %v, want 0.5", got)
	}
	if got := ReciprocalRank(results, []string{"c"}); !almostEqual(got, 1.0/3.0) {
		t.Errorf("rank 3: got %v, want 1/3", got)
	}
	if got := ReciprocalRank(results, []string{"z"}); got != 0 {
		t.Errorf("not found: got %v, want 0", got)
	}
	// When multiple relevant IDs are present, the earliest rank wins.
	if got := ReciprocalRank(results, []string{"c", "b"}); !almostEqual(got, 0.5) {
		t.Errorf("earliest of multiple matches: got %v, want 0.5", got)
	}
}

func TestNDCGAtK(t *testing.T) {
	// Perfect ranking: the only relevant result is first -> NDCG = 1.
	if got := NDCGAtK([]string{"a", "b", "c"}, []string{"a"}, 3); !almostEqual(got, 1.0) {
		t.Errorf("perfect ranking: got %v, want 1.0", got)
	}
	// Worst case within k: relevant result last -> NDCG < 1.
	got := NDCGAtK([]string{"b", "c", "a"}, []string{"a"}, 3)
	if got >= 1.0 || got <= 0 {
		t.Errorf("expected 0 < NDCG < 1 for a late hit, got %v", got)
	}
	// No relevant results in top k at all -> NDCG = 0.
	if got := NDCGAtK([]string{"b", "c", "d"}, []string{"a"}, 3); got != 0 {
		t.Errorf("no hits: got %v, want 0", got)
	}
	// Empty relevant set -> 0, not division by zero / NaN.
	if got := NDCGAtK([]string{"a", "b"}, nil, 2); got != 0 {
		t.Errorf("empty relevant set: got %v, want 0", got)
	}
	// Two relevant results, both found, ideal order -> NDCG = 1.
	if got := NDCGAtK([]string{"a", "b", "c"}, []string{"a", "b"}, 3); !almostEqual(got, 1.0) {
		t.Errorf("two hits in ideal order: got %v, want 1.0", got)
	}
}

func TestMeanRank(t *testing.T) {
	results := []string{"a", "b", "c"}

	if got := MeanRank(results, nil, 3); got != 0 {
		t.Errorf("empty id set: got %v, want 0", got)
	}
	if got := MeanRank(results, []string{"a"}, 3); !almostEqual(got, 1) {
		t.Errorf("rank of a: got %v, want 1", got)
	}
	if got := MeanRank(results, []string{"a", "c"}, 3); !almostEqual(got, 2) {
		t.Errorf("mean rank of a,c: got %v, want 2", got)
	}
	// An id absent from the results scores as len(results)+1, not
	// excluded — it should look at least as "unranked" as the worst
	// visible rank, not better.
	if got := MeanRank(results, []string{"z"}, 3); !almostEqual(got, 4) {
		t.Errorf("absent id: got %v, want 4 (len+1)", got)
	}
}

// Audit finding H3a: an absent irrelevant on an EMPTY result list must score
// "past the page" (k+1), not rank 1, or correct abstention looks worst.
func TestMeanRankOnEmptyListUsesDepth(t *testing.T) {
	if got := MeanRank(nil, []string{"z"}, 10); !almostEqual(got, 11) {
		t.Fatalf("MeanRank(empty) = %v, want 11", got)
	}
}

// Audit finding H3b: nDCG's ideal must not shrink to the number of results
// returned; a one-result list that found one of two relevant items is not
// perfect.
func TestNDCGShortListIsPenalised(t *testing.T) {
	got := NDCGAtK([]string{"a"}, []string{"a", "b"}, 10)
	want := 1.0 / (1.0 + 1.0/math.Log2(3))
	if !almostEqual(got, want) {
		t.Fatalf("NDCG short list = %.4f, want %.4f", got, want)
	}
}
