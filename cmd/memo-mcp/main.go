package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"sort"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
	"github.com/kmaziarz/memo-mcp/internal/search"
	"github.com/kmaziarz/memo-mcp/internal/server"
)

func main() {
	statsFlag := flag.Bool("stats", false, "Display journal statistics and exit")
	flag.Parse()

	if err := run(*statsFlag); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run(showStats bool) error {
	token := os.Getenv("JOURNAL_TOKEN")
	if token == "" {
		return fmt.Errorf("JOURNAL_TOKEN environment variable is required")
	}

	basePath := os.Getenv("JOURNAL_PATH")

	db, err := journal.OpenDB(token, basePath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	ctx := context.Background()

	// If --stats flag, display stats and exit
	if showStats {
		return displayStats(ctx, db)
	}

	// Normal server startup
	modelDir := embedding.DefaultModelDir()
	embedder, err := embedding.NewHugotEmbedder(ctx, modelDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: embedding unavailable, search will not work: %v\n", err)
	}
	if embedder != nil {
		defer embedder.Destroy()
	}

	journalMgr := journal.NewManager(db, embedder)
	searchSvc := search.NewService(db, embedder)
	srv := server.New(journalMgr, searchSvc)

	return srv.Run(ctx)
}

func displayStats(ctx context.Context, db *sql.DB) error {
	searchSvc := search.NewService(db, nil) // No embedder needed for stats

	stats, err := searchSvc.GetStats(ctx)
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	fmt.Println("=== Journal Statistics ===")
	fmt.Println()
	fmt.Printf("Total entries: %d\n", stats.TotalEntries)

	if stats.TotalEntries == 0 {
		fmt.Println("\nJournal is empty.")
		return nil
	}

	fmt.Printf("Date range: %s to %s\n",
		stats.EarliestEntry.Format("2006-01-02"),
		stats.LatestEntry.Format("2006-01-02"))

	coverage := float64(stats.EntriesWithEmbeddings) / float64(stats.TotalEntries) * 100
	fmt.Printf("Entries with embeddings: %d/%d (%.1f%%)\n",
		stats.EntriesWithEmbeddings, stats.TotalEntries, coverage)

	fmt.Println("\nRecent activity:")
	fmt.Printf("  Last 7 days: %d entries\n", stats.RecentActivity["7d"])
	fmt.Printf("  Last 30 days: %d entries\n", stats.RecentActivity["30d"])

	if len(stats.SectionCounts) > 0 {
		fmt.Println("\nSection usage:")
		type sc struct {
			name  string
			count int
		}
		sections := make([]sc, 0, len(stats.SectionCounts))
		for name, count := range stats.SectionCounts {
			sections = append(sections, sc{name, count})
		}
		sort.Slice(sections, func(i, j int) bool {
			return sections[i].count > sections[j].count
		})
		for _, s := range sections {
			fmt.Printf("  %s: %d\n", s.name, s.count)
		}
	}

	fmt.Println("\nStorage:")
	fmt.Printf("  Location: %s\n", stats.DatabasePath)
	fmt.Printf("  Size: %.2f MB\n", stats.DatabaseSizeMB)
	fmt.Printf("  Avg entry length: %d characters\n", stats.AvgEntryLength)

	return nil
}
