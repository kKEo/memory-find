package embedding

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A fake model that rejects any text longer than limit runes, like the real
// backend does when a text exceeds its position limit.
func limitedRunner(limit int) func(context.Context, []string) ([][]float32, error) {
	return func(_ context.Context, texts []string) ([][]float32, error) {
		out := make([][]float32, len(texts))
		for i, t := range texts {
			if len([]rune(t)) > limit {
				return nil, errors.New("index out of range in position embeddings")
			}
			out[i] = HashEmbed(t, 8)
		}
		return out, nil
	}
}

func TestBackstopHalvesOverlongInput(t *testing.T) {
	e := &HugotEmbedder{info: MiniLM, run: limitedRunner(100)}
	long := strings.Repeat("word ", 50) // 250 runes: fails at 250 and 125, passes at 62
	out, err := e.EmbedBatch(context.Background(), []string{long}, RoleDocument)
	if err != nil {
		t.Fatalf("expected the halved input to succeed, got %v", err)
	}
	if len(out) != 1 || len(out[0]) != 8 {
		t.Fatalf("unexpected output shape")
	}
}

func TestBackstopGivesUpAfterTwoHalvings(t *testing.T) {
	e := &HugotEmbedder{info: MiniLM, run: limitedRunner(10)}
	long := strings.Repeat("word ", 50) // 250 → 125 → 62, still > 10
	_, err := e.EmbedBatch(context.Background(), []string{long}, RoleDocument)
	if !errors.Is(err, ErrInputTooLong) {
		t.Fatalf("err = %v, want ErrInputTooLong", err)
	}
}

func TestBatchFallsBackPerTextWhenOneFails(t *testing.T) {
	e := &HugotEmbedder{info: MiniLM, run: limitedRunner(100)}
	texts := []string{"short one", strings.Repeat("x ", 80), "another short"}
	out, err := e.EmbedBatch(context.Background(), texts, RoleDocument)
	if err != nil {
		t.Fatalf("batch with one over-long text should still succeed: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d vectors", len(out))
	}
}

func TestQueryPrefixApplied(t *testing.T) {
	var seen []string
	e := &HugotEmbedder{
		info: ModelInfo{QueryPrefix: "query: ", DocPrefix: "passage: ", Dim: 8},
		run: func(_ context.Context, texts []string) ([][]float32, error) {
			seen = append(seen, texts...)
			out := make([][]float32, len(texts))
			for i := range texts {
				out[i] = HashEmbed(texts[i], 8)
			}
			return out, nil
		},
	}
	if _, err := e.EmbedBatch(context.Background(), []string{"hello"}, RoleQuery); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EmbedBatch(context.Background(), []string{"hello"}, RoleDocument); err != nil {
		t.Fatal(err)
	}
	if seen[0] != "query: hello" || seen[1] != "passage: hello" {
		t.Fatalf("prefixes not applied: %v", seen)
	}
}

func TestNilEmbedderIsUnavailable(t *testing.T) {
	var e *HugotEmbedder
	if _, err := e.EmbedBatch(context.Background(), []string{"x"}, RoleDocument); err == nil {
		t.Fatal("expected error from nil embedder")
	}
	if e.Info().ID != "minilm" {
		t.Fatal("nil Info should describe the default model")
	}
}

func TestHashEmbedderBatchMatchesSingle(t *testing.T) {
	h := NewHashEmbedder(16)
	single, _ := h.Embed(context.Background(), "alpha beta")
	batch, err := h.EmbedBatch(context.Background(), []string{"alpha beta", "gamma"}, RoleQuery)
	if err != nil || len(batch) != 2 {
		t.Fatal(err)
	}
	for i := range single {
		if single[i] != batch[0][i] {
			t.Fatal("batch differs from single")
		}
	}
	if h.Info().Dim != 16 {
		t.Fatal("Info().Dim")
	}
}
