package main

import (
	"context"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
	"github.com/kmaziarz/memo-mcp/internal/search"
	"github.com/kmaziarz/memo-mcp/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
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
