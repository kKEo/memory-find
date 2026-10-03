package graph

import (
	"math"
	"testing"
)

func names(ms []Mention) map[string]Mention {
	out := map[string]Mention{}
	for _, m := range ms {
		out[m.Name] = m
	}
	return out
}

func TestExtractFindsIdentifiersHeadingsAndNames(t *testing.T) {
	text := "# Client.Connect\n\n`Connect(ctx context.Context, addr string) (*Client, error)` dials the widgets server at addr. The fixed ten-second timeout from v1.7 is gone. A handler returning ERR_CONN_RESET surfaces as codes.Unavailable. Ask Grace Hopper about the Quorum Store.\n"
	got := names(Extract("Client.Connect", text))
	for _, want := range []string{"Client.Connect", "ERR_CONN_RESET", "codes.Unavailable", "Grace Hopper", "Quorum Store"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	for _, bad := range []string{"The", "A", "1.7", "v1.7"} {
		if _, ok := got[bad]; ok {
			t.Errorf("should not extract %q", bad)
		}
	}
	if got["Client.Connect"].Weight < got["Grace Hopper"].Weight {
		t.Errorf("heading identifier should outweigh a prose name: %+v", got)
	}
}

func TestResolveExactNearAndGate(t *testing.T) {
	ix := NewIndex([]string{Key("Client.Connect"), Key("Personalised PageRank Algorithm"), Key("Grace Hopper"), Key("ab")})
	if d := ix.Resolve("client.connect"); d.Action != "same" {
		t.Fatalf("case-insensitive exact: %+v", d)
	}
	if d := ix.Resolve("Grace  Hopper"); d.Action != "same" {
		t.Fatalf("whitespace-normalised exact: %+v", d)
	}
	if d := ix.Resolve("Personalised PageRank Algorithms"); d.Action != "candidate" || d.MatchKey != Key("Personalised PageRank Algorithm") {
		t.Fatalf("near match should be a candidate, not a merge: %+v", d)
	}
	// A clearly different name is new, not a candidate.
	if d := ix.Resolve("quorum.RotateKeysHandler"); d.Action != "new" {
		t.Fatalf("different identifier must stay separate: %+v", d)
	}
	if d := ix.Resolve("ab"); d.Action != "same" {
		t.Fatalf("short exact still exact: %+v", d)
	}
	if d := ix.Resolve("abc"); d.Action != "new" || d.Reason == "" {
		t.Fatalf("short near name must hit the entropy gate: %+v", d)
	}
	ix.Add(Key("quorum.OpenSession1"))
	if d := ix.Resolve("quorum.OpenSession10"); d.Action != "new" {
		t.Fatalf("names differing in a number are different things: %+v", d)
	}
	if d := ix.Resolve("Pipeline Scheduler"); d.Action != "new" {
		t.Fatalf("unrelated: %+v", d)
	}
}

func TestMinHashEstimatesJaccard(t *testing.T) {
	a, b := Key("Personalised PageRank Algorithm"), Key("Personalised PageRank Algorithms")
	exact := Jaccard(Trigrams(a), Trigrams(b))
	sa, sb := Sign(a), Sign(b)
	same := 0
	for i := range sa {
		if sa[i] == sb[i] {
			same++
		}
	}
	est := float64(same) / float64(len(sa))
	if math.Abs(est-exact) > 0.25 {
		t.Fatalf("minhash estimate %.2f far from exact %.2f", est, exact)
	}
	shared := false
	ba, bb := Bands(sa), Bands(sb)
	for i := range ba {
		if ba[i] == bb[i] {
			shared = true
		}
	}
	if !shared {
		t.Fatal("near-identical names should share an LSH band")
	}
}

func TestPPRPrefersSeedNeighbourhood(t *testing.T) {
	// 0-1-2 chain plus 3 (hub) connected to everything, 4 connected only to hub.
	g := BuildCSR(5, []Edge{{0, 1, 1}, {1, 2, 1}, {3, 0, 1}, {3, 1, 1}, {3, 2, 1}, {3, 4, 1}})
	r := g.PPR(map[int32]float32{0: 1}, 0.85, 20, 2)
	if r[1] <= r[4] || r[0] <= r[4] {
		t.Fatalf("neighbourhood of the seed should outrank the far node: %v", r)
	}
	free := g.PPR(map[int32]float32{0: 1}, 0.85, 20, 1<<30)
	if r[3] >= free[3] {
		t.Fatalf("hub penalty should lower the hub's rank: capped %v vs free %v", r[3], free[3])
	}
	if r[4] >= free[4] {
		t.Fatalf("hub penalty should lower what flows through the hub: %v vs %v", r[4], free[4])
	}
}
