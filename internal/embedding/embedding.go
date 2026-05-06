package embedding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
	"github.com/schollz/progressbar/v3"
)

type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type HugotEmbedder struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	mu       sync.Mutex
}

func NewHugotEmbedder(ctx context.Context, modelDir string) (*HugotEmbedder, error) {
	session, err := hugot.NewGoSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("create hugot session: %w", err)
	}

	modelPath, err := downloadModelWithProgress(ctx, modelDir)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("download model: %w", err)
	}

	pipe, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      "journal-embeddings",
		Options:   []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
	})
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("create pipeline: %w", err)
	}

	return &HugotEmbedder{
		session:  session,
		pipeline: pipe,
	}, nil
}

func (e *HugotEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	result, err := e.pipeline.RunPipeline(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("run pipeline: %w", err)
	}
	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("no embeddings returned")
	}
	return result.Embeddings[0], nil
}

func (e *HugotEmbedder) Destroy() {
	if e.session != nil {
		e.session.Destroy()
	}
}

const modelName = "sentence-transformers/all-MiniLM-L6-v2"

func downloadModelWithProgress(ctx context.Context, modelDir string) (string, error) {
	expectedPath := filepath.Join(modelDir, "all-MiniLM-L6-v2")
	if info, err := os.Stat(expectedPath); err == nil && info.IsDir() {
		return expectedPath, nil
	}

	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return "", fmt.Errorf("create model dir: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Downloading embedding model (first run only)...")

	modelPath, err := hugot.DownloadModel(ctx, modelName, modelDir, hugot.NewDownloadOptions())
	if err != nil {
		return "", err
	}

	return modelPath, nil
}

func DefaultModelDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".cache", "memo-mcp", "models")
}

// downloadFileWithProgress downloads a URL to dest showing a progress bar on stderr.
// Not currently used but available for custom download flows.
func downloadFileWithProgress(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	bar := progressbar.NewOptions64(size,
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionSetDescription("Downloading model"),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(40),
		progressbar.OptionOnCompletion(func() { fmt.Fprintln(os.Stderr) }),
	)

	_, err = io.Copy(io.MultiWriter(f, bar), resp.Body)
	return err
}
