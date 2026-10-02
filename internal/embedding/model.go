package embedding

import (
	"context"
	"errors"
	"fmt"
)

// Role says whether a text is a search query or a document passage. Some
// models want a different prefix for each; MiniLM does not.
type Role int

const (
	RoleDocument Role = iota
	RoleQuery
)

// ModelInfo describes the model an Embedder runs; it is what the `models`
// table records (docs/schema.md §5.1).
type ModelInfo struct {
	ID               string // short handle, e.g. "minilm"
	Name             string
	HFRepo           string
	HFRevision       string
	OnnxPath         string
	ExternalDataPath string
	Dim              int
	MaxTokens        int // hard input limit; chunking clamps to it
	QueryPrefix      string
	DocPrefix        string
	Normalize        bool
	Licence          string
	// Truncate keeps only the first N dimensions (Matryoshka models) and
	// renormalises; 0 = full vector. Dim reports the stored size.
	Truncate int
	// Static marks a model2vec-style lookup-table model run without ONNX.
	Static bool
	// Files lists the extra files a static model needs.
	Files []string
	// Note is shown by `model ls` (size, caveats, opt-in reasons).
	Note string
}

// ErrInputTooLong is returned when a text still exceeds the model's input
// limit after the embedder has halved it twice. Callers should chunk
// smaller rather than retry.
var ErrInputTooLong = errors.New("input too long for the embedding model")

// Embedder turns text into vectors.
//
// Embed is the single-text convenience that the legacy journal code uses;
// EmbedBatch is what the knowledge base uses (hugot runs a batch in one
// pass, so sixteen chunks cost far less than sixteen calls). Info describes
// the model so vectors can be keyed by it.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error)
	Info() ModelInfo
}

// applyPrefix prepends the role's prefix when the model defines one.
func applyPrefix(info ModelInfo, role Role, text string) string {
	switch role {
	case RoleQuery:
		return info.QueryPrefix + text
	default:
		return info.DocPrefix + text
	}
}

// halve returns the first half of text, cut on a rune boundary.
func halve(text string) string {
	r := []rune(text)
	return string(r[:len(r)/2])
}

// batchWithBackstop runs texts through run, retrying any text that the
// model rejects by halving it, at most twice; a text that still fails is
// reported as ErrInputTooLong (wrapping the model's error). The tokenizer
// in the pure-Go backend does not clamp inputs, so the only signal of an
// over-long input is a failed run.
func batchWithBackstop(ctx context.Context, run func(context.Context, []string) ([][]float32, error), texts []string) ([][]float32, error) {
	out, err := run(ctx, texts)
	if err == nil {
		if len(out) != len(texts) {
			return nil, fmt.Errorf("embedder returned %d vectors for %d texts", len(out), len(texts))
		}
		return out, nil
	}
	if len(texts) == 1 {
		// Retry the single text at half, then quarter, length.
		t := texts[0]
		for attempt := 0; attempt < 2; attempt++ {
			t = halve(t)
			if t == "" {
				break
			}
			if out, err2 := run(ctx, []string{t}); err2 == nil && len(out) == 1 {
				return out, nil
			}
		}
		return nil, fmt.Errorf("%w: %v", ErrInputTooLong, err)
	}
	// A batch failed: embed each text alone so one bad input does not
	// sink the others, and so the backstop above can shorten it.
	out = make([][]float32, len(texts))
	for i, t := range texts {
		v, err := batchWithBackstop(ctx, run, []string{t})
		if err != nil {
			return nil, fmt.Errorf("text %d: %w", i, err)
		}
		out[i] = v[0]
	}
	return out, nil
}
