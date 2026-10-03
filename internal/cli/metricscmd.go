package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kKEo/memory-find/internal/kb"
)

// runMetrics prints a database-backed snapshot: the knowledge-base gauges
// (the same numbers the /metrics collector exports) and, when the opt-in log
// has data, per-tool call statistics and search aggregates over a window.
// Live counters are per process and are scraped from the serving process.
func runMetrics(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	since := fs.Duration("since", 24*time.Hour, "window for the call and search statistics")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	st, err := store.Status(ctx)
	if err != nil {
		return err
	}
	from := time.Now().Add(-*since)
	calls, err := store.CallLogStats(ctx, from)
	if err != nil {
		return err
	}
	searches, err := store.QueryLogStats(ctx, from)
	if err != nil {
		return err
	}
	var size int64
	if fi, err := os.Stat(st.Path); err == nil {
		size = fi.Size()
	}
	note := "live counters are per process: scrape the serving process (memo-mcp serve --metrics-addr 127.0.0.1:9469, then GET /metrics) or the UI's /metrics; this snapshot comes from the knowledge-base file"
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"kb": map[string]any{"path": st.Path, "schema_version": st.SchemaVersion, "db_size_bytes": size, "sources": st.Sources, "documents_live": st.LiveDocuments, "revisions": st.Revisions, "chunks": st.Chunks, "facts": st.Facts,
				"entities": st.Graph.Entities, "mentions": st.Graph.Mentions, "edges": st.Graph.Edges, "merge_review_open": st.Graph.OpenMergeReview, "pages": st.Pages.Pages, "pages_stale": st.Pages.StalePages, "work_items_open": st.Pages.OpenWorkItems,
				"jobs_queued": st.JobsQueued, "jobs_failed": st.JobsFailed, "pending_embeddings": st.PendingEmbeddings, "last_write": st.LastWrite},
			"since": from, "calls": calls, "searches": searches, "note": note,
		})
	}
	fmt.Fprintf(stdout, "knowledge base  %s  (schema v%d, %.2f MB)\n", st.Path, st.SchemaVersion, float64(size)/1e6)
	fmt.Fprintf(stdout, "  documents %d live / %d revisions   chunks %d   facts %d   entities %d   mentions %d   pages %d (%d stale)   work items %d open\n",
		st.LiveDocuments, st.Revisions, st.Chunks, st.Facts, st.Graph.Entities, st.Graph.Mentions, st.Pages.Pages, st.Pages.StalePages, st.Pages.OpenWorkItems)
	fmt.Fprintf(stdout, "  jobs %d queued / %d failed", st.JobsQueued, st.JobsFailed)
	for m, n := range st.PendingEmbeddings {
		fmt.Fprintf(stdout, "   pending vectors[%s] %d", m, n)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "\nlast %s (opt-in log, MEMO_QUERY_LOG=1):\n", since.String())
	if searches.Searches == 0 && len(calls) == 0 {
		fmt.Fprintln(stdout, "  no logged calls or searches")
	} else {
		fmt.Fprintf(stdout, "  searches %d   abstentions %d   mean %.1f ms   mean results %.1f\n", searches.Searches, searches.Abstentions, searches.MeanMs, searches.MeanResults)
		if len(calls) > 0 {
			fmt.Fprintf(stdout, "  %-10s %6s %6s %7s %7s %7s %8s %8s\n", "tool", "calls", "errors", "p50 ms", "p95 ms", "max ms", "results", "tokens")
			for _, c := range calls {
				fmt.Fprintf(stdout, "  %-10s %6d %6d %7d %7d %7d %8.1f %8.0f\n", c.Tool, c.Calls, c.Errors, c.P50Ms, c.P95Ms, c.MaxMs, c.MeanResults, c.MeanTokens)
			}
		}
	}
	fmt.Fprintln(stdout, "\n"+note)
	return nil
}
