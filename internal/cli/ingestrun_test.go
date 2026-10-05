package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kKEo/memory-find/internal/kb"
)

// A directory ingest is recorded as one cli run with an item per file, so
// the web UI's ingest console can show it.
func TestIngestRecordsRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MEMO_HOME", home)
	t.Setenv("MEMO_KB", "demo")
	t.Setenv("JOURNAL_TOKEN", "")
	dir := t.TempDir()
	for name, body := range map[string]string{"a.md": "# A\n\nalpha beta\n", "b.md": "# B\n\ngamma delta\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errOut bytes.Buffer
	if code := Main(context.Background(), "test", []string{"ingest", dir, "--ns", "grpc", "--embed=false"}, &out, &errOut); code != 0 {
		t.Fatalf("ingest: %s", errOut.String())
	}

	ctx := context.Background()
	db, err := kb.Open(ctx, filepath.Join(home, "kb"), "demo", kb.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	runs, err := kb.NewStore(db, nil).IngestRunsTail(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	r := runs[0]
	if r.Channel != kb.ChannelCLI || r.State != kb.RunDone || r.Total != 2 || r.Done != 2 || r.Written != 2 || r.Namespace != "grpc" || r.Bytes == 0 {
		t.Errorf("run = %+v", r)
	}
}
