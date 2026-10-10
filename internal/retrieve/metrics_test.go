package retrieve

import (
	"context"
	"testing"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/obs"
)

func metric(reg *obs.Registry, name string, labels ...string) (float64, uint64) {
	for _, f := range reg.Snapshot(context.Background()).Families {
		if f.Name != name {
			continue
		}
		for _, s := range f.Series {
			ok := len(s.Labels) == len(labels)
			for i := range labels {
				if ok && s.Labels[i].Value != labels[i] {
					ok = false
				}
			}
			if ok {
				return s.Value, s.Count
			}
		}
	}
	return -1, 0
}

func TestSearchMetrics(t *testing.T) {
	ctx := context.Background()
	db, err := kb.Open(ctx, t.TempDir(), "m", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := kb.NewStore(db, embedding.NewHashEmbedder(64))
	for _, body := range []string{
		"# Client.Connect\n\n`Connect(ctx, addr)` dials the Widgets Server; on ERR_CONN_RESET the Retry Policy decides.\n",
		"# Retry Policy\n\nThe Retry Policy retries idempotent calls; ERR_CONN_RESET is retried.\n",
	} {
		if _, err := store.Ingest(ctx, kb.IngestInput{Namespace: "w", Content: body, Source: kb.SourceInput{URI: "https://w/" + body[:12], Title: "t", Kind: kb.KindDoc, Origin: kb.OriginWeb}, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI}); err != nil {
			t.Fatal(err)
		}
	}
	reg := obs.NewRegistry()
	svc := New(store, Default, false).WithRegistry(reg)
	if _, err := svc.Search(ctx, Request{Query: "ERR_CONN_RESET retry"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := metric(reg, "memors_search_total", "auto", "semantic+keyword+exact+fact", "chunk", "results"); v != 1 {
		t.Fatalf("search_total = %v", v)
	}
	if _, n := metric(reg, "memors_search_arm_duration_seconds", "keyword"); n != 1 {
		t.Fatalf("keyword arm observations = %d", n)
	}
	if _, n := metric(reg, "memors_search_results", "chunk"); n != 1 {
		t.Fatalf("results observations = %d", n)
	}
	// Relational question: structural arms, then the graph cache hits.
	for i := 0; i < 2; i++ {
		if _, err := svc.Search(ctx, Request{Query: "how does Client.Connect relate to the Retry Policy"}); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := metric(reg, "memors_graph_cache_total", "build"); b != 1 {
		t.Fatalf("graph builds = %v", b)
	}
	if h, _ := metric(reg, "memors_graph_cache_total", "hit"); h != 1 {
		t.Fatalf("graph hits = %v", h)
	}
	// Abstention is counted with its reason class.
	resp, err := svc.Search(ctx, Request{Query: "quasar entanglement baritone"})
	if err != nil || len(resp.Results) != 0 {
		t.Fatalf("expected abstention: %+v %v", resp, err)
	}
	if v, _ := metric(reg, "memors_search_abstentions_total", "no_match"); v != 1 {
		t.Fatalf("abstentions = %v", v)
	}
	// Without an embedder the search is degraded and says why.
	store2 := kb.NewStore(db, nil)
	reg2 := obs.NewRegistry()
	if _, err := New(store2, Default, false).WithRegistry(reg2).Search(ctx, Request{Query: "ERR_CONN_RESET"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := metric(reg2, "memors_search_degraded_total", "no_embedder"); v != 1 {
		t.Fatalf("degraded = %v", v)
	}
}
