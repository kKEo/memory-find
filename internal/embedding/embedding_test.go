package embedding

import (
	"context"
	"testing"
)

type mockEmbedder struct {
	vec []float32
	err error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, m.err
}

func newFixedVector(dim int, val float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = val
	}
	return v
}

func TestMockEmbedder(t *testing.T) {
	emb := &mockEmbedder{vec: newFixedVector(384, 0.1)}
	vec, err := emb.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 384 {
		t.Fatalf("expected 384 dimensions, got %d", len(vec))
	}
}

func TestDefaultModelDir(t *testing.T) {
	dir := DefaultModelDir()
	if dir == "" {
		t.Fatal("expected non-empty model dir")
	}
}
