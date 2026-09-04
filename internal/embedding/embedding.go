package embedding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
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

	pipe, err := loadPipelineWithRecovery(ctx, session, modelDir)
	if err != nil {
		session.Destroy()
		return nil, err
	}

	return &HugotEmbedder{
		session:  session,
		pipeline: pipe,
	}, nil
}

var errEmbedderUnavailable = errors.New("embedder unavailable")

func (e *HugotEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Defensive: a nil *HugotEmbedder must never reach here through the
	// Embedder interface (callers should pass a true nil interface on
	// construction failure instead), but if one does, fail cleanly rather
	// than panicking on e.mu.Lock() below.
	if e == nil || e.pipeline == nil {
		return nil, errEmbedderUnavailable
	}

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
	if e != nil && e.session != nil {
		e.session.Destroy()
	}
}

const modelName = "sentence-transformers/all-MiniLM-L6-v2"

// loadPipelineWithRecovery downloads the model (if needed) and builds the
// feature-extraction pipeline. If the pipeline fails to load from what
// looks like a ready model cache, the cache is treated as corrupt: it is
// wiped and the download is retried exactly once before giving up.
func loadPipelineWithRecovery(ctx context.Context, session *hugot.Session, modelDir string) (*pipelines.FeatureExtractionPipeline, error) {
	const maxAttempts = 2

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		forceRedownload := attempt > 0
		modelPath, err := downloadModelWithProgress(ctx, modelDir, forceRedownload)
		if err != nil {
			return nil, fmt.Errorf("download model: %w", err)
		}

		pipe, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
			ModelPath:    modelPath,
			Name:         "journal-embeddings",
			OnnxFilename: "model.onnx",
			Options:      []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
		})
		if err == nil {
			return pipe, nil
		}

		lastErr = err
		if attempt < maxAttempts-1 {
			fmt.Fprintf(os.Stderr, "warning: model cache appears corrupt (%v); re-downloading...\n", err)
		}
	}

	return nil, fmt.Errorf("create pipeline: %w", lastErr)
}

func sanitizedModelDirName(name string) string {
	return strings.ReplaceAll(name, "/", "_")
}

// downloadModelWithProgress ensures the embedding model is present under
// modelDir and returns the path to its directory, downloading it first if
// necessary.
//
// Readiness is tracked with a sentinel file, not directory existence: an
// interrupted download used to leave a partial model directory behind that
// every subsequent run mistook for "already downloaded", failing forever
// with no way to recover short of deleting the cache by hand. Here the
// download lands in a temporary directory and is atomically renamed into
// place only once it's complete, and the sentinel is written only after
// that rename succeeds. A file lock prevents two concurrent first-run
// processes from downloading the same ~90MB model at once.
//
// If force is true, any existing cached copy is discarded first.
func downloadModelWithProgress(ctx context.Context, modelDir string, force bool) (string, error) {
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return "", fmt.Errorf("create model dir: %w", err)
	}

	expectedPath := filepath.Join(modelDir, sanitizedModelDirName(modelName))
	sentinelPath := expectedPath + ".ok"

	if force {
		_ = os.RemoveAll(expectedPath)
		_ = os.Remove(sentinelPath)
	}

	if isModelReady(expectedPath, sentinelPath) {
		return expectedPath, nil
	}

	lock := flock.New(filepath.Join(modelDir, ".download.lock"))
	locked, err := lock.TryLockContext(ctx, 500*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("acquire model download lock: %w", err)
	}
	if !locked {
		return "", fmt.Errorf("timed out waiting for another process to finish downloading the model")
	}
	defer lock.Unlock()

	// Another process may have finished downloading while we waited for
	// the lock.
	if isModelReady(expectedPath, sentinelPath) {
		return expectedPath, nil
	}

	// Clear out any partial directory left by a prior interrupted attempt
	// (it has no sentinel, or we wouldn't be here).
	_ = os.RemoveAll(expectedPath)

	tmpDir, err := os.MkdirTemp(modelDir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("create temp download dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	fmt.Fprintln(os.Stderr, "Downloading embedding model (first run only)...")

	opts := hugot.NewDownloadOptions()
	opts.OnnxFilePath = "onnx/model.onnx"
	downloadedPath, err := hugot.DownloadModel(ctx, modelName, tmpDir, opts)
	if err != nil {
		return "", err
	}

	if err := os.Rename(downloadedPath, expectedPath); err != nil {
		return "", fmt.Errorf("move downloaded model into place: %w", err)
	}
	if err := os.WriteFile(sentinelPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write model sentinel: %w", err)
	}

	return expectedPath, nil
}

func isModelReady(modelDirPath, sentinelPath string) bool {
	if _, err := os.Stat(sentinelPath); err != nil {
		return false
	}
	info, err := os.Stat(modelDirPath)
	return err == nil && info.IsDir()
}

// RedownloadModel forces a fresh download of the embedding model into
// modelDir, discarding any cached copy first. Exposed for the
// --redownload-model CLI flag, for cases the automatic corruption recovery
// in loadPipelineWithRecovery doesn't catch (e.g. a model that loads but
// produces bad output after a partial write).
func RedownloadModel(ctx context.Context, modelDir string) (string, error) {
	return downloadModelWithProgress(ctx, modelDir, true)
}

func DefaultModelDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".cache", "memo-mcp", "models")
}
