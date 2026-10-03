package embedding

import (
	"context"
	"errors"
	"testing"

	"github.com/kKEo/memory-find/internal/obs"
)

func TestInstrumentedEmbedder(t *testing.T) {
	if Instrumented(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	e := Instrumented(NewHashEmbedder(16))
	if Instrumented(e) != e {
		t.Fatal("double wrapping")
	}
	if e.Info().ID != NewHashEmbedder(16).Info().ID {
		t.Fatal("Info passthrough")
	}
	before := metricValue(t, "memo_embed_texts_total", e.Info().ID, "query")
	if _, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"}, RoleQuery); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), "d"); err != nil {
		t.Fatal(err)
	}
	if got := metricValue(t, "memo_embed_texts_total", e.Info().ID, "query") - before; got != 3 {
		t.Fatalf("query texts counted %v", got)
	}
	if got := metricValue(t, "memo_embed_texts_total", e.Info().ID, "doc"); got < 1 {
		t.Fatalf("doc texts counted %v", got)
	}
	f := Instrumented(NewFailingEmbedder(errors.New("boom")))
	_, _ = f.EmbedBatch(context.Background(), []string{"x"}, RoleDocument)
	if got := metricValue(t, "memo_embed_errors_total", f.Info().ID, "doc"); got < 1 {
		t.Fatalf("errors counted %v", got)
	}
}

func metricValue(t *testing.T, name string, labels ...string) float64 {
	t.Helper()
	for _, f := range obs.Default().Snapshot(context.Background()).Families {
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
	return 0
}
