package retrieve

import (
	"context"
	"strings"
	"testing"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/rerank"
)

// A fake cross-encoder that prefers passages mentioning "ducks" moves the
// lunch-walk note to the top of a query it would otherwise lose, and the
// explain block records the move.
func TestPreciseProfileReranksTopN(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	precise, _ := Lookup("precise")
	svc := New(s, precise, false).WithReranker(rerank.Func{ID: "fake-ce", Fn: func(q string, ps []string) []float64 {
		out := make([]float64, len(ps))
		for i, p := range ps {
			if strings.Contains(p, "ducks") {
				out[i] = 0.99
			} else {
				out[i] = 0.1
			}
		}
		return out
	}})
	resp, err := svc.Search(context.Background(), Request{Query: "interceptor order walk", ResponseFormat: FormatExplain, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) < 2 || !strings.Contains(resp.Results[0].Title, "Lunch walk") {
		t.Fatalf("reranker did not promote the ducks note: %+v", resp.Results)
	}
	top := resp.Results[0]
	if top.Why.Rerank == nil || top.Why.Rerank.Model != "fake-ce" || top.Why.Rerank.BeforeRank == 1 {
		t.Fatalf("rerank explain missing or unmoved: %+v", top.Why.Rerank)
	}
	if resp.Trace.Rerank == nil || resp.Trace.Rerank.TopN < 2 {
		t.Fatalf("trace lacks rerank: %+v", resp.Trace)
	}
	// Default profile ignores the reranker even when attached.
	plain := New(s, Default, false).WithReranker(rerank.Func{ID: "fake-ce", Fn: func(q string, ps []string) []float64 { return make([]float64, len(ps)) }})
	resp, _ = plain.Search(context.Background(), Request{Query: "interceptor order walk", ResponseFormat: FormatExplain})
	if resp.Trace.Rerank != nil {
		t.Fatal("default profile must not rerank")
	}
}
