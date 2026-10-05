package kb

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIngestRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t, nil)
	id, err := s.StartIngestRun(ctx, IngestRunInput{Channel: ChannelCLI, Actor: "cli", Namespace: "grpc", Total: 3})
	if err != nil {
		t.Fatal(err)
	}
	in := IngestInput{Namespace: "grpc", Content: "# A\n\nalpha beta\n", Trust: TrustUser, Actor: "t", Channel: ChannelCLI,
		Source: SourceInput{URI: "https://x/a", Title: "A", Kind: KindDoc, Origin: OriginWeb}}
	res, err := s.Ingest(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordIngestItem(ctx, id, IngestItemFor("a.md", len(in.Content), 5*time.Millisecond, res, nil)); err != nil {
		t.Fatal(err)
	}
	res, err = s.Ingest(ctx, in)
	if err != nil || !res.Dedup {
		t.Fatalf("second ingest: %+v %v", res, err)
	}
	if err := s.RecordIngestItem(ctx, id, IngestItemFor("a.md", len(in.Content), time.Millisecond, res, nil)); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if err := s.RecordIngestItem(ctx, id, IngestItemFor("b.md", 7, 0, nil, boom)); err != nil {
		t.Fatal(err)
	}

	run, items, err := s.IngestRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunRunning || run.Done != 3 || run.Written != 1 || run.Unchanged != 1 || run.Failed != 1 || run.Chunks == 0 || run.Pending == 0 || run.Percent() != 100 {
		t.Errorf("run = %+v", run)
	}
	if len(items) != 3 || items[0].Outcome != OutcomeNew || items[0].URI == "" || items[1].Outcome != OutcomeUnchanged || items[2].Outcome != OutcomeError || items[2].Error != "boom" || items[2].Seq != 3 {
		t.Errorf("items = %+v", items)
	}
	if sum, err := s.IngestSummary(ctx); err != nil || sum.Running != 1 || sum.DocsDay != 1 || sum.ErrorsDay != 1 {
		t.Errorf("summary = %+v %v", sum, err)
	}

	if err := s.FinishIngestRun(ctx, id, boom); err != nil {
		t.Fatal(err)
	}
	runs, err := s.IngestRunsTail(ctx, 10)
	if err != nil || len(runs) != 1 || runs[0].State != RunFailed || runs[0].Error != "boom" || runs[0].FinishedAt.IsZero() {
		t.Fatalf("tail = %+v %v", runs, err)
	}
	if _, _, err := s.IngestRun(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown run: %v", err)
	}
}

func TestIngestRunStalled(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestStore(t, nil)
	id, err := s.StartIngestRun(ctx, IngestRunInput{Channel: ChannelCLI, Total: 2})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Now().Add(StallAfter + time.Minute) }
	run, _, err := s.IngestRun(ctx, id)
	if err != nil || run.State != RunStalled {
		t.Fatalf("state = %q (%v), want stalled", run.State, err)
	}
	if sum, _ := s.IngestSummary(ctx); sum.Running != 0 {
		t.Errorf("stalled run counted as running: %+v", sum)
	}
}

func TestMigrateV4ToV5(t *testing.T) {
	ctx := context.Background()
	_, db := openTestStore(t, nil)
	for _, q := range []string{`DROP TABLE ingest_run_items`, `DROP TABLE ingest_runs`, `PRAGMA user_version = 4`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if v, err := SchemaVersion(ctx, db); err != nil || v != 5 {
		t.Fatalf("version = %d (%v)", v, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('ingest_runs','ingest_run_items')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("tables = %d (%v)", n, err)
	}
}
