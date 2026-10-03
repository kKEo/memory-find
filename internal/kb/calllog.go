package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (s *Store) logCall(ctx context.Context, e CallLogEntry) error {
	args := string(e.Args)
	if args == "" {
		args = "{}"
	}
	at := e.At
	if at.IsZero() {
		at = s.now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO call_log(ts, client, method, tool, latency_ms, ok, error_class, n_results, tokens_out, args_summary_json) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		at.UnixMilli(), e.Client, e.Method, e.Tool, e.LatencyMs, boolInt(e.OK), e.ErrorClass, e.NResults, e.TokensOut, args)
	return err
}

// CallLogTail returns the newest n call rows.
func (s *Store) CallLogTail(ctx context.Context, n int) ([]CallLogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ts, client, method, tool, latency_ms, ok, error_class, n_results, tokens_out, args_summary_json FROM call_log ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CallLogEntry
	for rows.Next() {
		var e CallLogEntry
		var ts int64
		var ok int
		var args string
		if err := rows.Scan(&e.ID, &ts, &e.Client, &e.Method, &e.Tool, &e.LatencyMs, &ok, &e.ErrorClass, &e.NResults, &e.TokensOut, &args); err != nil {
			return nil, err
		}
		e.At, e.OK, e.Args = time.UnixMilli(ts).UTC(), ok == 1, []byte(args)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CallLogPrune keeps at most maxRows rows and nothing older than maxAge.
func (s *Store) CallLogPrune(ctx context.Context, maxRows int, maxAge time.Duration) (int64, error) {
	cutoff := s.now().Add(-maxAge).UnixMilli()
	res, err := s.db.ExecContext(ctx, `DELETE FROM call_log WHERE ts < ? OR id NOT IN (SELECT id FROM call_log ORDER BY id DESC LIMIT ?)`, cutoff, maxRows)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CallLogStat summarises one tool's calls over a window.
type CallLogStat struct {
	Tool        string  `json:"tool"`
	Calls       int     `json:"calls"`
	Errors      int     `json:"errors"`
	P50Ms       int64   `json:"p50_ms"`
	P95Ms       int64   `json:"p95_ms"`
	MaxMs       int64   `json:"max_ms"`
	MeanResults float64 `json:"mean_results"`
	MeanTokens  float64 `json:"mean_tokens"`
}

// CallLogStats aggregates the call log since a time, per tool.
func (s *Store) CallLogStats(ctx context.Context, since time.Time) ([]CallLogStat, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tool, latency_ms, ok, n_results, tokens_out FROM call_log WHERE ts >= ? ORDER BY tool`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		lat             []int64
		errs            int
		results, tokens int
	}
	by := map[string]*acc{}
	for rows.Next() {
		var tool string
		var lat int64
		var ok, n, tok int
		if err := rows.Scan(&tool, &lat, &ok, &n, &tok); err != nil {
			return nil, err
		}
		a := by[tool]
		if a == nil {
			a = &acc{}
			by[tool] = a
		}
		a.lat = append(a.lat, lat)
		if ok == 0 {
			a.errs++
		}
		a.results += n
		a.tokens += tok
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []CallLogStat
	for tool, a := range by {
		sort.Slice(a.lat, func(i, j int) bool { return a.lat[i] < a.lat[j] })
		n := len(a.lat)
		pct := func(p float64) int64 { return a.lat[int(p*float64(n-1))] }
		out = append(out, CallLogStat{Tool: tool, Calls: n, Errors: a.errs, P50Ms: pct(0.5), P95Ms: pct(0.95), MaxMs: a.lat[n-1], MeanResults: float64(a.results) / float64(n), MeanTokens: float64(a.tokens) / float64(n)})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Calls > out[j].Calls || (out[i].Calls == out[j].Calls && out[i].Tool < out[j].Tool)
	})
	return out, nil
}

// QueryLogStats aggregates the search log since a time.
type QueryLogStats struct {
	Searches    int     `json:"searches"`
	Abstentions int     `json:"abstentions"`
	MeanMs      float64 `json:"mean_ms"`
	MeanResults float64 `json:"mean_results"`
}

// QueryLogStats reads the per-search log over a window.
func (s *Store) QueryLogStats(ctx context.Context, since time.Time) (QueryLogStats, error) {
	var st QueryLogStats
	var meanMs, meanRes sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN n_results = 0 THEN 1 ELSE 0 END), 0), AVG(latency_ms), AVG(n_results) FROM query_log WHERE ts >= ?`, since.UnixMilli()).Scan(&st.Searches, &st.Abstentions, &meanMs, &meanRes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, fmt.Errorf("query log stats: %w", err)
	}
	st.MeanMs, st.MeanResults = meanMs.Float64, meanRes.Float64
	return st, nil
}
