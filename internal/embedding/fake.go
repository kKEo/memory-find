package embedding

import (
	"context"
	"hash/fnv"
	"math"
	"math/rand"
	"strings"
	"unicode"
)

// HashEmbedder is a deterministic, offline Embedder for tests: no network,
// no model, no warm-up, instant.
//
// It is deliberately not a constant-vector stub. Returning the same vector
// for every input (which every mock embedder in this project's test suite
// used to do) makes vector search degenerate — every entry is equally
// "close" to every query, so ranking, thresholding, section/date
// filtering, and recency logic all go untested even though the tests
// pass. HashEmbedder instead gives inputs real lexical geometry: shared
// vocabulary between two texts pulls their vectors together, disjoint
// vocabulary leaves them near-orthogonal, exactly the property production
// code and tests both need to exercise.
//
// Its one honest limitation is that it has no notion of synonymy —
// "car" and "automobile" are unrelated to it, unlike a real embedding
// model. Fixtures built against it must rely on lexical overlap for their
// expected matches; paraphrase-only test cases belong behind a build tag
// that runs against the real model instead (see the project roadmap).
type HashEmbedder struct {
	dim int
}

// NewHashEmbedder returns a HashEmbedder producing dim-dimensional
// vectors. Use 384 to match the dimension the production schema
// (entry_embeddings) is hard-coded to.
func NewHashEmbedder(dim int) *HashEmbedder {
	return &HashEmbedder{dim: dim}
}

func (h *HashEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	return HashEmbed(text, h.dim), nil
}

// EmbedBatch embeds each text independently; roles are ignored because the
// hash embedder has no prefixes.
func (h *HashEmbedder) EmbedBatch(_ context.Context, texts []string, _ Role) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = HashEmbed(t, h.dim)
	}
	return out, nil
}

// Info describes the fake as a model so tests can key vectors by it.
func (h *HashEmbedder) Info() ModelInfo {
	return ModelInfo{ID: "hash", Name: "hash-embedder (test)", HFRepo: "", Dim: h.dim, MaxTokens: 1 << 20, Normalize: true, Licence: "n/a"}
}

var _ Embedder = (*HashEmbedder)(nil)

// HashEmbed computes the same vector HashEmbedder.Embed would, without the
// Embedder interface's ctx/error ceremony — useful when a test wants a
// vector to seed a fixture with directly.
func HashEmbed(text string, dim int) []float32 {
	counts := make(map[string]int)
	for _, tok := range tokenize(text) {
		counts[tok]++
	}

	sum := make([]float64, dim)
	for tok, count := range counts {
		// Sublinear TF weighting: a token mentioned 10 times shouldn't
		// dominate the vector 10x as much as one mentioned once.
		weight := 1 + math.Log(float64(count))
		for i, v := range tokenVector(tok, dim) {
			sum[i] += weight * v
		}
	}

	return toUnitFloat32(sum)
}

func tokenize(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

// tokenVector deterministically maps a token to a pseudo-random unit
// vector, seeded from an FNV-1a hash of the token's bytes so the same
// token always maps to the same vector — in this process, in any other
// process, and across any number of calls.
func tokenVector(tok string, dim int) []float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tok))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	v := make([]float64, dim)
	for i := range v {
		v[i] = rng.NormFloat64()
	}
	return unitFloat64(v)
}

func toUnitFloat32(v []float64) []float32 {
	u := unitFloat64(v)
	out := make([]float32, len(u))
	for i, x := range u {
		out[i] = float32(x)
	}
	return out
}

func unitFloat64(v []float64) []float64 {
	var sumSq float64
	for _, x := range v {
		sumSq += x * x
	}
	norm := math.Sqrt(sumSq)
	if norm == 0 {
		return v
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

// FailingEmbedder always returns Err. Useful for exercising the
// degradation paths that kick in when the real embedder is unavailable
// (a missing model, a corrupt cache, a transient inference error).
type FailingEmbedder struct {
	Err error
}

func NewFailingEmbedder(err error) *FailingEmbedder {
	return &FailingEmbedder{Err: err}
}

func (f *FailingEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, f.Err
}

func (f *FailingEmbedder) EmbedBatch(_ context.Context, _ []string, _ Role) ([][]float32, error) {
	return nil, f.Err
}

func (f *FailingEmbedder) Info() ModelInfo {
	return ModelInfo{ID: "failing", Name: "failing-embedder (test)", Dim: 384, MaxTokens: 512}
}

var _ Embedder = (*FailingEmbedder)(nil)
