//go:build spike

// Spike S4: which candidate embedding models load and embed sanely under
// hugot's pure-Go (GoMLX) backend, and how fast. For each Hugging Face
// repo given on the command line (default: the MiniLM control), download it
// into a temp dir, build a feature-extraction pipeline, embed three
// sentences, check sim(a, a') > sim(a, b), and time 256-token-ish inputs.
// Run: go run -tags spike ./spikes/s4-models <hf-repo>[@<onnx path>] ...
// The path inside the repo defaults to onnx/model.onnx (what production
// uses for MiniLM); repos that ship several .onnx variants need it spelled.
package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

func main() {
	repos := os.Args[1:]
	if len(repos) == 0 {
		repos = []string{"sentence-transformers/all-MiniLM-L6-v2"}
	}
	ctx := context.Background()
	fmt.Println("| model | loads | sane | dim | p50 ms (1 × ~256 tok) | p50 ms (batch 16) | note |")
	fmt.Println("|---|---|---|---|---|---|---|")
	for _, repo := range repos {
		row := try(ctx, repo)
		fmt.Println(row)
	}
}

func try(ctx context.Context, spec string) string {
	repo, onnxPath := spec, "onnx/model.onnx"
	if i := strings.LastIndex(spec, "@"); i > 0 {
		repo, onnxPath = spec[:i], spec[i+1:]
	}
	dir := filepath.Join(os.TempDir(), "s4-models")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Sprintf("| %s | no | - | - | - | - | mkdir: %v |", repo, err)
	}
	opts := hugot.NewDownloadOptions()
	opts.OnnxFilePath = onnxPath
	path, err := hugot.DownloadModel(ctx, repo, dir, opts)
	if err != nil {
		return fmt.Sprintf("| %s | no | - | - | - | - | download: %v |", repo, short(err))
	}
	session, err := hugot.NewGoSession(ctx)
	if err != nil {
		return fmt.Sprintf("| %s | no | - | - | - | - | session: %v |", repo, short(err))
	}
	defer session.Destroy()
	pipe, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
		ModelPath: path, Name: "s4", Options: []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
	})
	if err != nil {
		return fmt.Sprintf("| %s | **no** | - | - | - | - | NewPipeline: %v |", repo, short(err))
	}
	a := "The gRPC interceptor order determines which middleware sees the request first."
	a2 := "Middleware ordering in gRPC interceptors decides who handles the call first."
	b := "I went for a long walk by the river after lunch and watched the ducks."
	res, err := pipe.RunPipeline(ctx, []string{a, a2, b})
	if err != nil {
		return fmt.Sprintf("| %s | yes | **no** | - | - | - | embed: %v |", repo, short(err))
	}
	if len(res.Embeddings) != 3 {
		return fmt.Sprintf("| %s | yes | **no** | - | - | - | got %d embeddings |", repo, len(res.Embeddings))
	}
	dim := len(res.Embeddings[0])
	saa := cos(res.Embeddings[0], res.Embeddings[1])
	sab := cos(res.Embeddings[0], res.Embeddings[2])
	sane := saa > sab
	long := strings.Repeat("Interceptors reorder requests when the auth middleware is registered after logging. ", 14) // ~250 tokens
	p1 := median(5, func() error { _, err := pipe.RunPipeline(ctx, []string{long}); return err })
	batch := make([]string, 16)
	for i := range batch {
		batch[i] = long
	}
	p16 := median(3, func() error { _, err := pipe.RunPipeline(ctx, batch); return err })
	return fmt.Sprintf("| %s | yes | %v (sim(a,a')=%.2f sim(a,b)=%.2f) | %d | %.0f | %.0f | |", repo, sane, saa, sab, dim, ms(p1), ms(p16))
}

func cos(x, y []float32) float64 {
	var d, nx, ny float64
	for i := range x {
		d += float64(x[i]) * float64(y[i])
		nx += float64(x[i]) * float64(x[i])
		ny += float64(y[i]) * float64(y[i])
	}
	return d / (math.Sqrt(nx) * math.Sqrt(ny))
}

func ms(d time.Duration) float64 { return float64(d) / 1e6 }

func median(k int, f func() error) time.Duration {
	ds := make([]time.Duration, 0, k)
	for i := 0; i < k; i++ {
		t := time.Now()
		if err := f(); err != nil {
			return 0
		}
		ds = append(ds, time.Since(t))
	}
	for i := range ds {
		for j := i + 1; j < len(ds); j++ {
			if ds[j] < ds[i] {
				ds[i], ds[j] = ds[j], ds[i]
			}
		}
	}
	return ds[k/2]
}

func short(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return strings.ReplaceAll(s, "\n", " ")
}
