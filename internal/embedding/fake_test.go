package embedding

import (
	"context"
	"errors"
	"math"
	"testing"
)

func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func TestHashEmbedderDeterministic(t *testing.T) {
	h := NewHashEmbedder(384)
	v1, err := h.Embed(context.Background(), "the quick brown fox")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := h.Embed(context.Background(), "the quick brown fox")
	if err != nil {
		t.Fatal(err)
	}
	if len(v1) != len(v2) {
		t.Fatalf("length mismatch: %d vs %d", len(v1), len(v2))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("non-deterministic at index %d: %v vs %v", i, v1[i], v2[i])
		}
	}
}

func TestHashEmbedderDimension(t *testing.T) {
	h := NewHashEmbedder(384)
	v, err := h.Embed(context.Background(), "some text")
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 384 {
		t.Errorf("expected 384 dimensions, got %d", len(v))
	}
}

func TestHashEmbedderIsUnitNorm(t *testing.T) {
	h := NewHashEmbedder(384)
	v, err := h.Embed(context.Background(), "any non-empty text at all")
	if err != nil {
		t.Fatal(err)
	}
	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if math.Abs(norm-1.0) > 1e-4 {
		t.Errorf("expected unit norm, got %v", norm)
	}
}

func TestHashEmbedderSharedVocabularyIsCloser(t *testing.T) {
	h := NewHashEmbedder(384)
	ctx := context.Background()

	a, _ := h.Embed(ctx, "the authentication middleware leaks session tokens")
	b, _ := h.Embed(ctx, "session tokens leaked by the authentication middleware")
	c, _ := h.Embed(ctx, "went for a long walk in the park today")

	simAB := cosine(a, b)
	simAC := cosine(a, c)

	if simAB <= simAC {
		t.Errorf("expected shared-vocabulary texts to be closer: sim(a,b)=%.4f, sim(a,c)=%.4f", simAB, simAC)
	}
	if simAB < 0.5 {
		t.Errorf("expected high similarity for near-identical vocabulary, got %.4f", simAB)
	}
}

func TestHashEmbedderDisjointVocabularyIsNearOrthogonal(t *testing.T) {
	h := NewHashEmbedder(384)
	ctx := context.Background()

	a, _ := h.Embed(ctx, "database migration postgres index query")
	b, _ := h.Embed(ctx, "walked outside enjoyed sunshine weather calm")

	sim := cosine(a, b)
	if math.Abs(sim) > 0.3 {
		t.Errorf("expected near-orthogonal vectors for disjoint vocabulary, got cosine=%.4f", sim)
	}
}

func TestHashEmbedderEmptyTextDoesNotProduceNaN(t *testing.T) {
	h := NewHashEmbedder(384)
	v, err := h.Embed(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for i, x := range v {
		if math.IsNaN(float64(x)) {
			t.Fatalf("NaN at index %d", i)
		}
	}
}

func TestFailingEmbedder(t *testing.T) {
	wantErr := errors.New("boom")
	f := NewFailingEmbedder(wantErr)
	_, err := f.Embed(context.Background(), "anything")
	if err != wantErr {
		t.Errorf("expected %v, got %v", wantErr, err)
	}
}
