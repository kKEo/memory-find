package embedding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/knights-analytics/hugot"
)

// fakeFetcher returns a modelFetcher that writes a placeholder model
// directory (no real network access, no real model files) and counts how
// many times it was invoked, so tests can assert exactly when a
// (re)download actually happened versus when the sentinel let it skip.
func fakeFetcher(calls *int) modelFetcher {
	return func(_ context.Context, info ModelInfo, destDir string) (string, error) {
		*calls++
		modelPath := filepath.Join(destDir, sanitizedModelDirName(MiniLM.HFRepo))
		if err := os.MkdirAll(modelPath, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(modelPath, "model.onnx"), []byte("fake-onnx-bytes"), 0o644); err != nil {
			return "", err
		}
		return modelPath, nil
	}
}

func TestDownloadModelFetchesOnce(t *testing.T) {
	dir := t.TempDir()
	var calls int

	path, err := downloadModel(context.Background(), dir, MiniLM, false, fakeFetcher(&calls))
	if err != nil {
		t.Fatalf("downloadModel: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected fetch to be called once, got %d", calls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected model directory to exist at %s: %v", path, err)
	}
	if _, err := os.Stat(path + ".ok"); err != nil {
		t.Errorf("expected sentinel file to exist: %v", err)
	}
}

func TestDownloadModelSkipsWhenAlreadyReady(t *testing.T) {
	dir := t.TempDir()
	var calls int
	fetch := fakeFetcher(&calls)

	if _, err := downloadModel(context.Background(), dir, MiniLM, false, fetch); err != nil {
		t.Fatalf("first downloadModel: %v", err)
	}
	if _, err := downloadModel(context.Background(), dir, MiniLM, false, fetch); err != nil {
		t.Fatalf("second downloadModel: %v", err)
	}

	if calls != 1 {
		t.Errorf("expected fetch to be called exactly once across both calls, got %d", calls)
	}
}

// TestDownloadModelRecoversFromPartialDownload is the direct regression
// test for D8: readiness used to be tracked by directory existence alone,
// so an interrupted first download left a partial directory that every
// subsequent run mistook for "already downloaded" and failed on forever.
// A partial directory (present, but with no sentinel) must instead be
// wiped and retried.
func TestDownloadModelRecoversFromPartialDownload(t *testing.T) {
	dir := t.TempDir()
	var calls int

	// Simulate an interrupted prior download: the model directory exists
	// (with junk inside, unlike a real partial download, but the point is
	// it's present) and has no sentinel.
	partial := filepath.Join(dir, sanitizedModelDirName(MiniLM.HFRepo))
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, "truncated.onnx"), []byte("incomplete"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := downloadModel(context.Background(), dir, MiniLM, false, fakeFetcher(&calls))
	if err != nil {
		t.Fatalf("downloadModel: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected the partial download to be retried exactly once, got %d fetch calls", calls)
	}
	if _, err := os.Stat(filepath.Join(path, "truncated.onnx")); err == nil {
		t.Error("expected the partial download's leftover file to be gone after recovery")
	}
	if _, err := os.Stat(filepath.Join(path, "model.onnx")); err != nil {
		t.Errorf("expected the freshly (re)downloaded model file to exist: %v", err)
	}
}

func TestDownloadModelForceRedownloads(t *testing.T) {
	dir := t.TempDir()
	var calls int
	fetch := fakeFetcher(&calls)

	if _, err := downloadModel(context.Background(), dir, MiniLM, false, fetch); err != nil {
		t.Fatalf("first downloadModel: %v", err)
	}
	if _, err := downloadModel(context.Background(), dir, MiniLM, true, fetch); err != nil {
		t.Fatalf("forced downloadModel: %v", err)
	}

	if calls != 2 {
		t.Errorf("expected force=true to trigger a second fetch, got %d total calls", calls)
	}
}

func TestDownloadModelFetchFailureLeavesNoSentinel(t *testing.T) {
	dir := t.TempDir()
	fetch := func(context.Context, ModelInfo, string) (string, error) {
		return "", errors.New("simulated network failure")
	}

	if _, err := downloadModel(context.Background(), dir, MiniLM, false, fetch); err == nil {
		t.Fatal("expected an error from a failing fetch")
	}

	expectedPath := filepath.Join(dir, sanitizedModelDirName(MiniLM.HFRepo))
	if _, err := os.Stat(expectedPath + ".ok"); err == nil {
		t.Error("expected no sentinel file after a failed fetch")
	}
}

// TestLoadPipelineWithRecoveryRetriesDownloadOnPipelineFailure exercises
// the layer above downloadModel: even a "successfully downloaded" (per the
// fetcher) model can fail to load as a pipeline — e.g. bytes corrupted
// after the fact, or genuinely incompatible files. That should also
// trigger exactly one force-redownload retry, not an infinite loop or an
// immediate give-up. hugot.NewGoSession needs no network access (verified
// separately); only the model fetch is faked here.
func TestLoadPipelineWithRecoveryRetriesDownloadOnPipelineFailure(t *testing.T) {
	dir := t.TempDir()
	var fetchCalls int

	// A fetcher whose files will always fail hugot.NewPipeline (they're
	// not real ONNX files), so this test's assertion is specifically about
	// the retry *count*, not about ever reaching a working pipeline.
	fetch := fakeFetcher(&fetchCalls)

	session, err := hugot.NewGoSession(context.Background())
	if err != nil {
		t.Fatalf("create hugot session: %v", err)
	}
	defer session.Destroy()

	_, pipelineErr := loadPipelineWithRecovery(context.Background(), session, MiniLM, dir, fetch)
	if pipelineErr == nil {
		t.Fatal("expected pipeline construction to fail against placeholder (non-ONNX) files")
	}
	if fetchCalls != 2 {
		t.Errorf("expected exactly 2 fetch attempts (initial + one forced retry), got %d", fetchCalls)
	}
}
