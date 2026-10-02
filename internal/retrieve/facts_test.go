package retrieve

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
)

// A remembered fact votes for its evidence passage, so a query phrased like
// the fact finds the passage even when the passage itself is worded
// differently; and granularity=fact returns the fact with its evidence.
func TestFactArmAndFactGranularity(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	ctx := context.Background()
	// Find the Errors chunk to use as evidence.
	svc := New(s, Default, false)
	r, _ := svc.Search(ctx, Request{Query: "ERR_CONN_RESET", Mode: ModeExact})
	if len(r.Results) == 0 {
		t.Fatal("setup: evidence chunk not found")
	}
	evidence := r.Results[0].ChunkURI
	f, err := s.Remember(ctx, kb.RememberInput{Namespace: "grpc", Statement: "A reset connection surfaces to callers as the Unavailable status code.", About: []string{"codes.Unavailable"}, EvidenceURI: evidence, Origin: kb.OriginAgentDerived, Trust: kb.TrustAgent, Channel: kb.ChannelTool})
	if err != nil {
		t.Fatal(err)
	}
	// The query uses the fact's words, not the passage's.
	resp, err := svc.Search(ctx, Request{Query: "reset connection surfaces Unavailable status", ResponseFormat: FormatExplain})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) == 0 || resp.Results[0].ChunkURI != evidence {
		t.Fatalf("evidence chunk not first: %+v", resp.Results)
	}
	if armByName(resp.Results[0].Why.Arms, ArmFact) == nil {
		t.Fatalf("fact arm not in explain: %+v", resp.Results[0].Why.Arms)
	}
	// granularity=fact returns the fact itself with evidence attached.
	fr, err := svc.Search(ctx, Request{Query: "reset connection Unavailable", Granularity: GranularityFact, ResponseFormat: FormatDetailed})
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Results) != 1 || fr.Results[0].URI != f.URI || !strings.Contains(fr.Results[0].Content, "Evidence:") || fr.Results[0].Provenance.Kind != "fact" {
		t.Fatalf("fact granularity: %+v", fr.Results)
	}
	// Nothing matches → abstention with a fact-specific hint.
	none, _ := svc.Search(ctx, Request{Query: "quasar entanglement", Granularity: GranularityFact})
	if len(none.Results) != 0 || none.Hint == "" {
		t.Fatalf("fact abstention: %+v", none)
	}
}

// as_of shows the revision and the fact that were current at that time;
// forgotten records stay hidden even then.
func TestAsOfAndRevocation(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Ingest at t0, revise at t0+30d.
	s.SetNow(func() time.Time { return t0 })
	first := ingest(t, s, "grpc", "https://x/connect", "Client.Connect", kb.KindDoc, "v1.7.0", "# Client.Connect\n\nConnect takes a single addr argument and dials.\n")
	s.SetNow(func() time.Time { return t0.Add(30 * 24 * time.Hour) })
	ingest(t, s, "grpc", "https://x/connect", "Client.Connect", kb.KindDoc, "v1.8.0", "# Client.Connect\n\nConnect takes a context and an addr argument and dials.\n")
	svc := New(s, Default, false)
	svc.now = func() time.Time { return t0.Add(60 * 24 * time.Hour) }

	// Default: the newest revision.
	now, _ := svc.Search(ctx, Request{Query: "Connect addr argument dials", Granularity: GranularityDocument})
	if len(now.Results) != 1 || now.Results[0].Provenance.Version != "v1.8.0" {
		t.Fatalf("default should return the latest revision: %+v", now.Results)
	}
	// as_of between the two: the old revision.
	then := t0.Add(10 * 24 * time.Hour)
	past, _ := svc.Search(ctx, Request{Query: "Connect addr argument dials", Granularity: GranularityDocument, AsOf: &then, ResponseFormat: FormatExplain})
	if len(past.Results) != 1 || past.Results[0].DocumentURI != first.URI || past.Trace.AsOf == nil {
		t.Fatalf("as_of should return the old revision: %+v", past.Results)
	}
	// Forget the old revision: even as_of cannot see it.
	if err := s.Forget(ctx, kb.ForgetInput{URI: first.URI, Reason: "wrong", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	gone, _ := svc.Search(ctx, Request{Query: "Connect addr argument dials", Granularity: GranularityDocument, AsOf: &then})
	for _, r := range gone.Results {
		if r.DocumentURI == first.URI {
			t.Fatal("forgotten revision served under as_of")
		}
	}
	// Facts: supersede and look back.
	s.SetNow(func() time.Time { return t0 })
	old, _ := s.Remember(ctx, kb.RememberInput{Namespace: "grpc", Statement: "Connect dial timeout is fixed at ten seconds.", Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Channel: kb.ChannelCLI})
	s.SetNow(func() time.Time { return t0.Add(30 * 24 * time.Hour) })
	nu, _ := s.Remember(ctx, kb.RememberInput{Namespace: "grpc", Statement: "Connect dial timeout comes from the context since v1.8.", Supersedes: old.URI, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Channel: kb.ChannelCLI})
	live, _ := svc.Search(ctx, Request{Query: "Connect dial timeout", Granularity: GranularityFact})
	if len(live.Results) != 1 || live.Results[0].URI != nu.URI {
		t.Fatalf("live fact: %+v", live.Results)
	}
	back, _ := svc.Search(ctx, Request{Query: "Connect dial timeout", Granularity: GranularityFact, AsOf: &then, ResponseFormat: FormatExplain})
	if len(back.Results) != 1 || back.Results[0].URI != old.URI || back.Results[0].Why.Time == nil || back.Results[0].Why.Time.AsOfApplied == nil {
		t.Fatalf("as_of fact: %+v", back.Results)
	}
}
