package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Ingest run states (migration 5). RunStalled is never stored: readers
// derive it for a run still marked running that has not moved in
// StallAfter, e.g. a CLI batch that was killed.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunFailed  = "failed"
	RunStalled = "stalled"
)

// Ingest item outcomes.
const (
	OutcomeNew       = "new"
	OutcomeRevision  = "revision"
	OutcomeUnchanged = "unchanged"
	OutcomeError     = "error"
)

// StallAfter is how long a running run may go without progress before
// readers report it as stalled.
const StallAfter = 10 * time.Minute

// IngestRunInput opens a run.
type IngestRunInput struct {
	Channel   string
	Actor     string
	Namespace string
	Total     int
}

// IngestRun is one CLI batch or MCP ingest call with its running totals.
type IngestRun struct {
	ID         string
	Channel    string
	Actor      string
	Namespace  string
	State      string
	StartedAt  time.Time
	UpdatedAt  time.Time
	FinishedAt time.Time // zero while running
	Total      int
	Done       int
	Written    int
	Unchanged  int
	Failed     int
	Chunks     int
	Embedded   int
	Pending    int
	Bytes      int64
	Error      string
}

// Elapsed is the run's wall time so far (or in total once finished).
func (r IngestRun) Elapsed() time.Duration {
	end := r.FinishedAt
	if end.IsZero() {
		end = r.UpdatedAt
	}
	return end.Sub(r.StartedAt)
}

// DocsPerSecond is the throughput over Elapsed; 0 when nothing finished.
func (r IngestRun) DocsPerSecond() float64 {
	sec := r.Elapsed().Seconds()
	if sec <= 0 || r.Done == 0 {
		return 0
	}
	return float64(r.Done) / sec
}

// Percent is Done/Total in [0,100].
func (r IngestRun) Percent() int {
	if r.Total <= 0 {
		return 0
	}
	return min(100, r.Done*100/r.Total)
}

// IngestItem is one document a run handled.
type IngestItem struct {
	Seq      int
	Name     string
	URI      string
	Outcome  string
	Revision int
	Chunks   int
	Embedded int
	Pending  int
	Bytes    int64
	Ms       int64
	Error    string
}

// IngestItemFor builds the item row for one Store.Ingest call.
func IngestItemFor(name string, bytes int, took time.Duration, res *IngestResult, err error) IngestItem {
	it := IngestItem{Name: name, Bytes: int64(bytes), Ms: took.Milliseconds()}
	switch {
	case err != nil:
		it.Outcome, it.Error = OutcomeError, err.Error()
	case res.Dedup:
		it.Outcome, it.URI, it.Revision = OutcomeUnchanged, res.URI, res.Revision
	default:
		it.Outcome = OutcomeNew
		if res.Revision > 1 {
			it.Outcome = OutcomeRevision
		}
		it.URI, it.Revision, it.Chunks, it.Embedded, it.Pending = res.URI, res.Revision, res.Chunks, res.Embedded, res.Pending
	}
	return it
}

// StartIngestRun records a new running run and returns its id.
func (s *Store) StartIngestRun(ctx context.Context, in IngestRunInput) (string, error) {
	id := uuid.Must(uuid.NewV7()).String()
	now := s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ingest_runs(id, channel, actor, namespace, state, started_at, updated_at, total) VALUES (?,?,?,?,?,?,?,?)`,
		id, in.Channel, in.Actor, in.Namespace, RunRunning, now, now, in.Total)
	if err != nil {
		return "", fmt.Errorf("start ingest run: %w", err)
	}
	return id, nil
}

// RecordIngestItem appends one item and folds it into the run's counters.
func (s *Store) RecordIngestItem(ctx context.Context, runID string, it IngestItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if it.Seq == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT done + 1 FROM ingest_runs WHERE id = ?`, runID).Scan(&it.Seq); err != nil {
			return fmt.Errorf("ingest run %s: %w", runID, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ingest_run_items(run_id, seq, name, uri, outcome, revision, chunks, embedded, pending, bytes, ms, error) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		runID, it.Seq, it.Name, it.URI, it.Outcome, it.Revision, it.Chunks, it.Embedded, it.Pending, it.Bytes, it.Ms, it.Error); err != nil {
		return fmt.Errorf("record ingest item: %w", err)
	}
	var written, unchanged, failed int
	switch it.Outcome {
	case OutcomeNew, OutcomeRevision:
		written = 1
	case OutcomeUnchanged:
		unchanged = 1
	case OutcomeError:
		failed = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_runs SET done = done + 1, written = written + ?, unchanged = unchanged + ?, failed = failed + ?,
		chunks = chunks + ?, embedded = embedded + ?, pending = pending + ?, bytes = bytes + ?, updated_at = ? WHERE id = ?`,
		written, unchanged, failed, it.Chunks, it.Embedded, it.Pending, it.Bytes, s.now().UnixMilli(), runID); err != nil {
		return fmt.Errorf("update ingest run: %w", err)
	}
	return tx.Commit()
}

// FinishIngestRun closes a run: done when runErr is nil, failed otherwise.
func (s *Store) FinishIngestRun(ctx context.Context, runID string, runErr error) error {
	state, msg := RunDone, ""
	if runErr != nil {
		state, msg = RunFailed, runErr.Error()
	}
	now := s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `UPDATE ingest_runs SET state = ?, error = ?, finished_at = ?, updated_at = ? WHERE id = ?`, state, msg, now, now, runID)
	if err != nil {
		return fmt.Errorf("finish ingest run: %w", err)
	}
	return nil
}

const ingestRunCols = `id, channel, actor, namespace, state, started_at, updated_at, finished_at, total, done, written, unchanged, failed, chunks, embedded, pending, bytes, error`

func (s *Store) scanIngestRun(sc interface{ Scan(...any) error }) (IngestRun, error) {
	var r IngestRun
	var started, updated int64
	var finished sql.NullInt64
	if err := sc.Scan(&r.ID, &r.Channel, &r.Actor, &r.Namespace, &r.State, &started, &updated, &finished,
		&r.Total, &r.Done, &r.Written, &r.Unchanged, &r.Failed, &r.Chunks, &r.Embedded, &r.Pending, &r.Bytes, &r.Error); err != nil {
		return r, err
	}
	r.StartedAt, r.UpdatedAt = time.UnixMilli(started).UTC(), time.UnixMilli(updated).UTC()
	if finished.Valid {
		r.FinishedAt = time.UnixMilli(finished.Int64).UTC()
	}
	if r.State == RunRunning && s.now().Sub(r.UpdatedAt) > StallAfter {
		r.State = RunStalled
	}
	return r, nil
}

// IngestRunsTail returns the newest n runs.
func (s *Store) IngestRunsTail(ctx context.Context, n int) ([]IngestRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+ingestRunCols+` FROM ingest_runs ORDER BY started_at DESC, id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IngestRun
	for rows.Next() {
		r, err := s.scanIngestRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// IngestRun returns one run and its items in order.
func (s *Store) IngestRun(ctx context.Context, id string) (IngestRun, []IngestItem, error) {
	r, err := s.scanIngestRun(s.db.QueryRowContext(ctx, `SELECT `+ingestRunCols+` FROM ingest_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, nil, fmt.Errorf("%w: ingest run %s", ErrNotFound, id)
	}
	if err != nil {
		return r, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT seq, name, uri, outcome, revision, chunks, embedded, pending, bytes, ms, error FROM ingest_run_items WHERE run_id = ? ORDER BY seq`, id)
	if err != nil {
		return r, nil, err
	}
	defer rows.Close()
	var items []IngestItem
	for rows.Next() {
		var it IngestItem
		if err := rows.Scan(&it.Seq, &it.Name, &it.URI, &it.Outcome, &it.Revision, &it.Chunks, &it.Embedded, &it.Pending, &it.Bytes, &it.Ms, &it.Error); err != nil {
			return r, nil, err
		}
		items = append(items, it)
	}
	return r, items, rows.Err()
}

// IngestSummary is the console's header strip.
type IngestSummary struct {
	Running   int
	DocsDay   int
	ErrorsDay int
}

// IngestSummary counts running runs plus documents and errors in the last 24h.
func (s *Store) IngestSummary(ctx context.Context) (IngestSummary, error) {
	var sum IngestSummary
	now := s.now()
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN state = 'running' AND updated_at >= ? THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN started_at >= ? THEN written ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN started_at >= ? THEN failed ELSE 0 END), 0)
		FROM ingest_runs`, now.Add(-StallAfter).UnixMilli(), now.Add(-24*time.Hour).UnixMilli(), now.Add(-24*time.Hour).UnixMilli()).
		Scan(&sum.Running, &sum.DocsDay, &sum.ErrorsDay)
	return sum, err
}
