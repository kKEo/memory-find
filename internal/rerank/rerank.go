// Package rerank re-scores the top candidates of a search with a
// cross-encoder: a model that reads the query and a passage together and
// outputs one relevance score. It is slower than embedding (one forward pass
// per pair) and only ever runs on the top few dozen results, inside the
// `precise` profile, and only ships by default if the eval shows it earns
// its latency (roadmap OD-7).
package rerank

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"

	"github.com/kKEo/memory-find/internal/embedding"
)

// Reranker scores (query, passage) pairs; higher is more relevant.
type Reranker interface {
	Score(ctx context.Context, query string, passages []string) ([]float64, error)
	Name() string
}

// Known lists the rerankers memo-mcp can load. ms-marco-MiniLM shares its
// architecture with the MiniLM embedder, so it is the one known to run under
// the pure-Go backend; the Ettin models are candidates for the bake-off.
var Known = []embedding.ModelInfo{
	{ID: "ms-marco-minilm", Name: "cross-encoder/ms-marco-MiniLM-L6-v2", HFRepo: "cross-encoder/ms-marco-MiniLM-L6-v2", OnnxPath: "onnx/model.onnx", MaxTokens: 512, Licence: "Apache-2.0",
		Note: "22M params; the reference small reranker (MTEB-R nDCG@10 0.508 over top-100 in the Ettin benchmark). ~91 MB."},
}

// Lookup finds a reranker by id.
func Lookup(id string) (embedding.ModelInfo, error) {
	for _, m := range Known {
		if m.ID == id {
			return m, nil
		}
	}
	return embedding.ModelInfo{}, fmt.Errorf("unknown reranker %q", id)
}

// CrossEncoder runs a sequence-classification model as a reranker.
type CrossEncoder struct {
	info     embedding.ModelInfo
	session  *hugot.Session
	pipeline *pipelines.TextClassificationPipeline
	mu       sync.Mutex
}

// Load downloads (once) and loads a cross-encoder.
func Load(ctx context.Context, info embedding.ModelInfo, modelDir string) (*CrossEncoder, error) {
	path, err := embedding.EnsureModelFiles(ctx, info, modelDir)
	if err != nil {
		return nil, err
	}
	session, err := hugot.NewGoSession(ctx)
	if err != nil {
		return nil, err
	}
	// Sigmoid of the single logit is monotonic in the logit, so ranking by
	// it is ranking by the model's relevance score.
	pipe, err := hugot.NewPipeline(session, hugot.TextClassificationConfig{
		ModelPath:    path,
		Name:         "memo-rerank-" + info.ID,
		OnnxFilename: filepath.Base(info.OnnxPath),
		Options:      []hugot.TextClassificationOption{pipelines.WithSigmoid(), pipelines.WithSingleLabel()},
	})
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("load reranker %s: %w", info.ID, err)
	}
	return &CrossEncoder{info: info, session: session, pipeline: pipe}, nil
}

func (c *CrossEncoder) Name() string { return c.info.ID }

// Close releases the model session.
func (c *CrossEncoder) Close() {
	if c != nil && c.session != nil {
		c.session.Destroy()
	}
}

// Score encodes each pair as "query [SEP] passage" (the BERT pair format the
// tokenizer understands) and returns the model's score per passage.
func (c *CrossEncoder) Score(ctx context.Context, query string, passages []string) ([]float64, error) {
	if len(passages) == 0 {
		return nil, nil
	}
	inputs := make([]string, len(passages))
	for i, p := range passages {
		inputs[i] = query + " [SEP] " + p
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out, err := c.pipeline.RunPipeline(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("rerank: %w", err)
	}
	scores := make([]float64, len(passages))
	for i, co := range out.ClassificationOutputs {
		if len(co) > 0 {
			scores[i] = float64(co[0].Score)
		}
	}
	return scores, nil
}

// Func adapts a scoring function to the Reranker interface (tests).
type Func struct {
	ID string
	Fn func(query string, passages []string) []float64
}

func (f Func) Name() string { return f.ID }
func (f Func) Score(_ context.Context, query string, passages []string) ([]float64, error) {
	return f.Fn(query, passages), nil
}
