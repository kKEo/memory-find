package embedding

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

// MiniLM is the incumbent model: small, Apache-2.0, and the only one so far
// proven to load under the pure-Go backend (spike S4).
var MiniLM = ModelInfo{
	ID:        "minilm",
	Name:      "all-MiniLM-L6-v2",
	HFRepo:    modelName,
	OnnxPath:  "onnx/model.onnx",
	Dim:       384,
	MaxTokens: 512, // the graph's position limit; the model was tuned on 256
	Normalize: true,
	Licence:   "Apache-2.0",
}

type HugotEmbedder struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	info     ModelInfo
	// run executes one batch; tests replace it to exercise the backstop
	// without a model.
	run func(ctx context.Context, texts []string) ([][]float32, error)
	mu  sync.Mutex
}

func NewHugotEmbedder(ctx context.Context, modelDir string) (*HugotEmbedder, error) {
	return NewHugotEmbedderFor(ctx, MiniLM, modelDir)
}

// NewHugotEmbedderFor loads any ONNX model the registry describes: it is
// downloaded (with its external weights file when the repo has one) into
// modelDir on first use and run on hugot's pure-Go backend.
func NewHugotEmbedderFor(ctx context.Context, info ModelInfo, modelDir string) (*HugotEmbedder, error) {
	if info.HFRepo == "" || info.OnnxPath == "" {
		return nil, fmt.Errorf("model %q is not an ONNX model", info.ID)
	}
	session, err := hugot.NewGoSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("create hugot session: %w", err)
	}
	pipe, err := loadPipelineWithRecovery(ctx, session, info, modelDir, fetchFromHuggingFace)
	if err != nil {
		session.Destroy()
		return nil, err
	}
	e := &HugotEmbedder{session: session, pipeline: pipe, info: info}
	e.run = e.runPipeline
	return e, nil
}

func (e *HugotEmbedder) runPipeline(ctx context.Context, texts []string) ([][]float32, error) {
	result, err := e.pipeline.RunPipeline(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("run pipeline: %w", err)
	}
	out := result.Embeddings
	if e.info.Truncate > 0 {
		for i, v := range out {
			out[i] = TruncateNormalize(v, e.info.Truncate)
		}
	}
	return out, nil
}

// TruncateNormalize keeps the first n dimensions of a Matryoshka-trained
// vector and rescales it to unit length.
func TruncateNormalize(v []float32, n int) []float32 {
	if n <= 0 || n >= len(v) {
		return v
	}
	t := make([]float32, n)
	copy(t, v[:n])
	var norm float64
	for _, x := range t {
		norm += float64(x) * float64(x)
	}
	if norm > 0 {
		inv := float32(1 / math.Sqrt(norm))
		for i := range t {
			t[i] *= inv
		}
	}
	return t
}

// Info describes the loaded model.
func (e *HugotEmbedder) Info() ModelInfo {
	if e == nil {
		return MiniLM
	}
	return e.info
}

// EmbedBatch embeds texts in one model pass, with the over-long-input
// backstop. The mutex serialises model access; the database connection is
// never held while this runs.
func (e *HugotEmbedder) EmbedBatch(ctx context.Context, texts []string, role Role) ([][]float32, error) {
	if e == nil || e.run == nil {
		return nil, errEmbedderUnavailable
	}
	if len(texts) == 0 {
		return nil, nil
	}
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = applyPrefix(e.info, role, t)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return batchWithBackstop(ctx, e.run, prefixed)
}

var errEmbedderUnavailable = errors.New("embedder unavailable")

func (e *HugotEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Defensive: a nil *HugotEmbedder must never reach here through the
	// Embedder interface (callers should pass a true nil interface on
	// construction failure instead), but if one does, fail cleanly rather
	// than panicking on e.mu.Lock() below.
	if e == nil || e.run == nil {
		return nil, errEmbedderUnavailable
	}
	out, err := e.EmbedBatch(ctx, []string{text}, RoleDocument)
	if err != nil {
		return nil, err
	}
	return out[0], nil
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
func loadPipelineWithRecovery(ctx context.Context, session *hugot.Session, info ModelInfo, modelDir string, fetch modelFetcher) (*pipelines.FeatureExtractionPipeline, error) {
	const maxAttempts = 2

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		forceRedownload := attempt > 0
		modelPath, err := downloadModel(ctx, modelDir, info, forceRedownload, fetch)
		if err != nil {
			return nil, fmt.Errorf("download model: %w", err)
		}

		opts := []hugot.FeatureExtractionOption{}
		if info.Normalize {
			opts = append(opts, pipelines.WithNormalization())
		}
		pipe, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
			ModelPath:    modelPath,
			Name:         "memo-embeddings-" + info.ID,
			OnnxFilename: filepath.Base(info.OnnxPath),
			Options:      opts,
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

// modelFetcher downloads modelName's files into destDir and returns the
// path to the resulting model directory, mirroring hugot.DownloadModel's
// contract. Production uses fetchFromHuggingFace; tests inject a fake that
// writes a placeholder directory with no network access, so the
// atomic-rename, sentinel, and corruption-recovery logic in downloadModel
// can be exercised in isolation from the real ~90MB download.
type modelFetcher func(ctx context.Context, info ModelInfo, destDir string) (string, error)

func fetchFromHuggingFace(ctx context.Context, info ModelInfo, destDir string) (string, error) {
	opts := hugot.NewDownloadOptions()
	opts.OnnxFilePath = info.OnnxPath
	opts.ExternalDataPath = info.ExternalDataPath
	if info.HFRevision != "" {
		opts.Branch = info.HFRevision
	}
	return hugot.DownloadModel(ctx, info.HFRepo, destDir, opts)
}

// downloadModel ensures the embedding model is present under modelDir and
// returns the path to its directory, fetching it first if necessary.
//
// Readiness is tracked with a sentinel file, not directory existence: an
// interrupted download used to leave a partial model directory behind that
// every subsequent run mistook for "already downloaded", failing forever
// with no way to recover short of deleting the cache by hand. Here the
// download lands in a temporary directory and is atomically renamed into
// place only once it's complete, and the sentinel is written only after
// that rename succeeds. A file lock prevents two concurrent first-run
// processes from downloading the same model at once.
//
// If force is true, any existing cached copy is discarded first.
func downloadModel(ctx context.Context, modelDir string, info ModelInfo, force bool, fetch modelFetcher) (string, error) {
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return "", fmt.Errorf("create model dir: %w", err)
	}

	expectedPath := filepath.Join(modelDir, sanitizedModelDirName(info.HFRepo))
	sentinelPath := expectedPath + ".ok"

	if force {
		_ = os.RemoveAll(expectedPath)
		_ = os.Remove(sentinelPath)
	}

	if isModelReady(expectedPath, sentinelPath) {
		return expectedPath, nil
	}

	// Another process may be downloading right now; wait for it, but not
	// forever (flock's TryLockContext only returns on success or when the
	// context ends, so the bound has to come from the context).
	lockCtx, cancel := context.WithTimeout(ctx, downloadLockTimeout)
	defer cancel()
	lock := flock.New(filepath.Join(modelDir, ".download.lock"))
	locked, err := lock.TryLockContext(lockCtx, 500*time.Millisecond)
	if err != nil || !locked {
		return "", fmt.Errorf("timed out after %s waiting for another process to finish downloading the model (%v)", downloadLockTimeout, err)
	}
	defer lock.Unlock()

	sweepStaleDownloads(modelDir)

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

	fmt.Fprintf(os.Stderr, "Downloading embedding model %s (first run only)...\n", info.HFRepo)
	if info.Licence != "" && info.Licence != "Apache-2.0" && info.Licence != "MIT" {
		fmt.Fprintf(os.Stderr, "note: %s is distributed under the %s licence; check it fits your use.\n", info.Name, info.Licence)
	}

	downloadedPath, err := fetch(ctx, info, tmpDir)
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

// downloadLockTimeout bounds how long a second process waits for a first
// one to finish downloading.
const downloadLockTimeout = 15 * time.Minute

// sweepStaleDownloads removes temporary download directories left behind by
// a process that was killed mid-download. Only the lock holder calls it, so
// a live download's directory is never touched; the age check is extra
// caution against a lock that was somehow bypassed.
func sweepStaleDownloads(modelDir string) {
	entries, err := os.ReadDir(modelDir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() || !strings.HasPrefix(ent.Name(), ".download-") {
			continue
		}
		if info, err := ent.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			_ = os.RemoveAll(filepath.Join(modelDir, ent.Name()))
		}
	}
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
	return downloadModel(ctx, modelDir, MiniLM, true, fetchFromHuggingFace)
}

// RedownloadModelFor forces a fresh download of any registry model.
func RedownloadModelFor(ctx context.Context, info ModelInfo, modelDir string) (string, error) {
	return downloadModel(ctx, modelDir, info, true, fetchFromHuggingFace)
}

func DefaultModelDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".cache", "memo-mcp", "models")
}

// EnsureModelFiles downloads a registry model's files once and returns the
// local directory. Other packages (the reranker) build their own hugot
// pipelines from it, reusing this package's sentinel, lock and recovery.
func EnsureModelFiles(ctx context.Context, info ModelInfo, modelDir string) (string, error) {
	return downloadModel(ctx, modelDir, info, false, fetchFromHuggingFace)
}
