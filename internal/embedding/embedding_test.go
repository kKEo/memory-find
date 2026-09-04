package embedding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultModelDir(t *testing.T) {
	dir := DefaultModelDir()
	if dir == "" {
		t.Fatal("expected non-empty model dir")
	}
}

// TestNilHugotEmbedderDoesNotPanic guards against the typed-nil-interface
// trap: constructing an Embedder from a nil *HugotEmbedder must fail
// cleanly, never panic on the mutex dereference inside Embed.
func TestNilHugotEmbedderDoesNotPanic(t *testing.T) {
	var e *HugotEmbedder
	var iface Embedder = e // non-nil interface wrapping a nil pointer, deliberately

	if _, err := iface.Embed(context.Background(), "text"); err == nil {
		t.Fatal("expected error from a nil *HugotEmbedder, got nil")
	}

	e.Destroy() // must also not panic
}

func TestHugotEmbedderWithoutPipelineReturnsError(t *testing.T) {
	e := &HugotEmbedder{}
	if _, err := e.Embed(context.Background(), "text"); err == nil {
		t.Fatal("expected error from an embedder with no pipeline, got nil")
	}
}

func TestSanitizedModelDirName(t *testing.T) {
	got := sanitizedModelDirName("sentence-transformers/all-MiniLM-L6-v2")
	want := "sentence-transformers_all-MiniLM-L6-v2"
	if got != want {
		t.Errorf("sanitizedModelDirName() = %q, want %q", got, want)
	}
}

func TestIsModelReady(t *testing.T) {
	dir := t.TempDir()
	modelDirPath := filepath.Join(dir, "model")
	sentinelPath := modelDirPath + ".ok"

	if isModelReady(modelDirPath, sentinelPath) {
		t.Error("expected not ready: neither model dir nor sentinel exist")
	}

	if err := os.MkdirAll(modelDirPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if isModelReady(modelDirPath, sentinelPath) {
		t.Error("expected not ready: model dir exists but sentinel does not (partial download)")
	}

	if err := os.WriteFile(sentinelPath, []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isModelReady(modelDirPath, sentinelPath) {
		t.Error("expected ready: both model dir and sentinel exist")
	}
}
