package kb

import (
	"context"
	"testing"
	"time"

	"github.com/kKEo/memors/internal/obs"
)

func mval(reg *obs.Registry, name string, labels ...string) float64 {
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
				return s.Value
			}
		}
	}
	return -1
}

func TestStoreMetricsAndCollector(t *testing.T) {
	s, _ := openTestStore(t, nil)
	reg := obs.NewRegistry()
	s.WithRegistry(reg)
	ctx := context.Background()
	a, err := s.Ingest(ctx, docInput("grpc", "https://example.com/a", "# A\n\nalpha beta\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, docInput("grpc", "https://example.com/a", "# A\n\nalpha beta\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(ctx, docInput("grpc", "https://example.com/a", "# A\n\nalpha gamma\n")); err != nil {
		t.Fatal(err)
	}
	for outcome, want := range map[string]float64{"new": 1, "dedup": 1, "revision": 1} {
		if v := mval(reg, "memors_store_ingests_total", outcome); v != want {
			t.Errorf("ingests %s = %v", outcome, v)
		}
	}
	if v := mval(reg, "memors_store_writes_total", "ingest", ChannelTool); v != 1 {
		t.Errorf("writes ingest = %v", v)
	}
	if v := mval(reg, "memors_store_writes_total", "revise", ChannelTool); v != 1 {
		t.Errorf("writes revise = %v", v)
	}
	if v := mval(reg, "memors_store_chunks_written_total"); v < 2 {
		t.Errorf("chunks written = %v", v)
	}
	// No embedder: vectors are queued as jobs.
	if v := mval(reg, "memors_store_jobs_total", "embed", "queued"); v != 2 {
		t.Errorf("jobs queued = %v", v)
	}
	f, err := s.Remember(ctx, RememberInput{Namespace: "grpc", Statement: "Alpha is beta.", About: []string{"Alpha"}, Origin: OriginUserSaid, Trust: TrustUser, Actor: "t", Channel: ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(ctx, ForgetInput{URI: f.URI, Reason: "test", Actor: "t", Channel: ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	if v := mval(reg, "memors_store_writes_total", "remember", ChannelCLI); v != 1 {
		t.Errorf("writes remember = %v", v)
	}
	if v := mval(reg, "memors_store_writes_total", "forget", ChannelCLI); v != 1 {
		t.Errorf("writes forget = %v", v)
	}
	if v := mval(reg, "memors_store_mentions_linked_total"); v < 1 {
		t.Errorf("mentions = %v", v)
	}
	_ = a
	// The collector mirrors Status and caches within the ttl.
	reg.AddCollector(s.MetricsCollector(time.Hour))
	st, _ := s.Status(ctx)
	if v := mval(reg, "memors_kb_documents_live"); v != float64(st.LiveDocuments) {
		t.Fatalf("collector documents_live %v != %d", v, st.LiveDocuments)
	}
	if v := mval(reg, "memors_kb_namespace_documents", "grpc"); v != 1 {
		t.Fatalf("namespace gauge = %v", v)
	}
	if _, err := s.Ingest(ctx, docInput("grpc", "https://example.com/b", "# B\n\nbeta\n")); err != nil {
		t.Fatal(err)
	}
	if v := mval(reg, "memors_kb_documents_live"); v != float64(st.LiveDocuments) {
		t.Fatalf("collector should be cached within ttl: %v", v)
	}
	if v := mval(reg, "memors_kb_db_size_bytes"); v <= 0 {
		t.Fatalf("db size gauge = %v", v)
	}
}
