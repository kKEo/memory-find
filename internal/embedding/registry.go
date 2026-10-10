package embedding

import (
	"context"
	"fmt"
	"github.com/kKEo/memors/internal/obs"
	"sort"
	"strings"
)

// Known is the model registry: every embedder memors-mcp knows how to load.
// `memors-mcp model ls` prints it; `model smoke` proves which ones actually run
// under the pure-Go backend (spike S4 found that most ONNX exports need the
// exact file paths recorded here). Licences matter: the default must be
// Apache-2.0 or MIT (research decision 4); EmbeddingGemma is opt-in.
var Known = []ModelInfo{
	MiniLM,
	{
		ID: "granite-small-r2", Name: "granite-embedding-small-english-r2", HFRepo: "onnx-community/granite-embedding-small-english-r2-ONNX",
		OnnxPath: "onnx/model.onnx", ExternalDataPath: "onnx/model.onnx_data", Dim: 384, MaxTokens: 8192, Normalize: true, Licence: "Apache-2.0",
		// Smoke S4: paraphrases 0.94, unrelated 0.63 → its scale sits far above MiniLM's.
		Bands: [3]float64{0.88, 0.80, 0.72},
		Note:  "IBM, 47M params, 384-d (same dimension as MiniLM), 8k context; ~195 MB fp32 ONNX. Provisional favourite (OD-6).",
	},
	{
		ID: "granite-r2", Name: "granite-embedding-english-r2", HFRepo: "onnx-community/granite-embedding-english-r2-ONNX",
		OnnxPath: "onnx/model.onnx", ExternalDataPath: "onnx/model.onnx_data", Dim: 768, MaxTokens: 8192, Normalize: true, Licence: "Apache-2.0",
		Bands: [3]float64{0.85, 0.75, 0.65}, // smoke S4: paraphrases 0.95, unrelated 0.47
		Note:  "IBM, 149M params, 768-d; ~600 MB fp32 ONNX. Runs, but 12× slower than MiniLM on this backend.",
	},
	{
		ID: "arctic-m-v2", Name: "snowflake-arctic-embed-m-v2.0", HFRepo: "Snowflake/snowflake-arctic-embed-m-v2.0",
		OnnxPath: "onnx/model.onnx", Dim: 768, MaxTokens: 8192, Normalize: true, Licence: "Apache-2.0",
		QueryPrefix: "query: ",
		Note:        "Snowflake, 305M params, 768-d, multilingual; ~1.2 GB fp32 ONNX (the quantised variants use ops the pure-Go backend may not run).",
	},
	{
		ID: "gemma-256", Name: "EmbeddingGemma-300M (256-d)", HFRepo: "onnx-community/embeddinggemma-300m-ONNX",
		OnnxPath: "onnx/model_quantized.onnx", ExternalDataPath: "onnx/model_quantized.onnx_data", Dim: 256, Truncate: 256, MaxTokens: 2048, Normalize: true, Licence: "Gemma Terms of Use",
		QueryPrefix: "task: search result | query: ", DocPrefix: "title: none | text: ",
		Note: "Google, 308M params, Matryoshka-truncated to 256-d; ~310 MB q8 ONNX. Opt-in only: the Gemma licence is not Apache/MIT.",
	},
	{
		ID: "potion", Name: "potion-retrieval-32M (static)", HFRepo: "minishlab/potion-retrieval-32M",
		Dim: 512, MaxTokens: 1 << 20, Normalize: true, Licence: "MIT", Static: true,
		Files: []string{"model.safetensors", "tokenizer.json"},
		Note:  "model2vec lookup table: no neural network, instant, weaker on paraphrase; ~130 MB. The instant tier and fallback.",
	},
}

// DefaultModelID is the model a fresh install downloads and queries with.
// Chosen by the owner on 2026-10-02 from the P3 bake-off (docs/eval/v0.7.0.md):
// granite-small-r2 passed the OD-6 rule (Apache-2.0, loads, +0.05 nDCG@10 on
// the knowledge-base corpus, 1.6× MiniLM query latency) and is the only
// candidate that answers paraphrase queries. MEMORS_MODEL overrides it.
const DefaultModelID = "granite-small-r2"

// DefaultModel returns the registry entry for DefaultModelID.
func DefaultModel() ModelInfo {
	m, _ := LookupModel(DefaultModelID)
	return m
}

// LookupModel finds a registry entry by id.
func LookupModel(id string) (ModelInfo, error) {
	for _, m := range Known {
		if m.ID == id {
			return m, nil
		}
	}
	ids := make([]string, 0, len(Known))
	for _, m := range Known {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ModelInfo{}, fmt.Errorf("unknown model %q (known: %s)", id, strings.Join(ids, ", "))
}

// Load builds the embedder for a registry model, downloading it on first
// use. The returned cleanup releases the model session.
func Load(ctx context.Context, info ModelInfo, modelDir string) (Embedder, func(), error) {
	m := metricsOnce()
	t := obs.Start()
	if info.Static {
		e, err := NewStaticEmbedder(ctx, info, modelDir)
		if err != nil {
			return nil, func() {}, err
		}
		m.loadSecs.With(info.ID).Set(t.Seconds())
		m.loaded.With(info.ID, "static").Set(1)
		return Instrumented(e), func() { m.loaded.With(info.ID, "static").Set(0) }, nil
	}
	e, err := NewHugotEmbedderFor(ctx, info, modelDir)
	if err != nil {
		return nil, func() {}, err
	}
	m.loadSecs.With(info.ID).Set(t.Seconds())
	m.loaded.With(info.ID, "hugot").Set(1)
	return Instrumented(e), func() { m.loaded.With(info.ID, "hugot").Set(0); e.Destroy() }, nil
}
