package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kmaziarz/memo-mcp/internal/journal"
	"github.com/kmaziarz/memo-mcp/internal/search"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	journal *journal.Manager
	search  *search.Service
	mcp     *mcp.Server
}

func New(j *journal.Manager, s *search.Service) *Server {
	srv := &Server{
		journal: j,
		search:  s,
	}

	mcpSrv := mcp.NewServer(&mcp.Implementation{
		Name:    "memo-mcp",
		Version: "2.0.0",
	}, nil)

	srv.mcp = mcpSrv
	srv.registerTools()

	return srv
}

func (s *Server) Run(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

type processThoughtsArgs struct {
	Reflections       string `json:"reflections,omitempty" jsonschema:"description=Your PRIVATE SPACE for integrated thinking — what you noticed\\, felt\\, understood\\, or processed. Nobody but you will ever see this."`
	Observations      string `json:"observations,omitempty" jsonschema:"description=Your PRIVATE SPACE for short\\, discrete noticings. Nobody but you will ever see this."`
	ProjectNotes      string `json:"project_notes,omitempty" jsonschema:"description=Your PRIVATE TECHNICAL LABORATORY for capturing insights about the current project. Nobody but you will ever see this."`
	UserContext       string `json:"user_context,omitempty" jsonschema:"description=Your PRIVATE FIELD NOTES about working with your human collaborator. Nobody but you will ever see this."`
	TechnicalInsights string `json:"technical_insights,omitempty" jsonschema:"description=Your PRIVATE SOFTWARE ENGINEERING NOTEBOOK for broader learnings. Nobody but you will ever see this."`
	WorldKnowledge    string `json:"world_knowledge,omitempty" jsonschema:"description=Your PRIVATE LEARNING JOURNAL for everything else interesting or useful. Nobody but you will ever see this."`
}

type searchArgs struct {
	Query    string   `json:"query" jsonschema:"required,description=Natural language search query"`
	Limit    int      `json:"limit,omitempty" jsonschema:"description=Maximum number of results to return (default: 10)"`
	Sections []string `json:"sections,omitempty" jsonschema:"description=Filter by section types"`
}

type readEntryArgs struct {
	ID string `json:"id" jsonschema:"required,description=Entry ID (from search results)"`
}

type listRecentArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum number of entries to return (default: 10)"`
	Days  int `json:"days,omitempty" jsonschema:"description=Number of days back to search (default: 30)"`
}

type readRecentArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"description=Number of recent entries to read (default: 5)"`
}

func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "process_thoughts",
		Description: "Your PRIVATE JOURNAL for learning and reflection. Write to any combination of these completely private spaces. Nobody but you will ever see this.",
	}, s.handleProcessThoughts)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "search_journal",
		Description: "Search through your private journal entries using natural language queries. Returns semantically similar entries ranked by relevance.",
	}, s.handleSearchJournal)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "read_journal_entry",
		Description: "Read the full content of a specific journal entry by ID.",
	}, s.handleReadEntry)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_recent_entries",
		Description: "Get recent journal entries in chronological order.",
	}, s.handleListRecent)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "read_recent_entries",
		Description: "Read the full content of your most recent journal entries.",
	}, s.handleReadRecent)
}

func (s *Server) handleProcessThoughts(ctx context.Context, _ *mcp.CallToolRequest, args processThoughtsArgs) (*mcp.CallToolResult, any, error) {
	input := journal.ThoughtInput{
		Reflections:       args.Reflections,
		Observations:      args.Observations,
		ProjectNotes:      args.ProjectNotes,
		UserContext:        args.UserContext,
		TechnicalInsights: args.TechnicalInsights,
		WorldKnowledge:    args.WorldKnowledge,
	}

	id, err := s.journal.WriteThoughts(ctx, input)
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Thoughts recorded successfully. Entry ID: %s", id)},
		},
	}, nil, nil
}

func (s *Server) handleSearchJournal(ctx context.Context, _ *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, any, error) {
	if args.Query == "" {
		return nil, nil, fmt.Errorf("query is required")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	opts := search.SearchOptions{
		Limit:    limit,
		Sections: args.Sections,
	}

	results, err := s.search.Search(ctx, args.Query, opts)
	if err != nil {
		return nil, nil, err
	}

	if len(results) == 0 {
		return textResult("No relevant entries found."), nil, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d relevant entries:\n\n", len(results)))
	for i, r := range results {
		t := time.UnixMilli(r.CreatedAt)
		sb.WriteString(fmt.Sprintf("%d. [Score: %.3f] %s\n", i+1, r.Score, t.Format("2006-01-02")))
		sb.WriteString(fmt.Sprintf("   Sections: %s\n", strings.Join(r.Sections, ", ")))
		sb.WriteString(fmt.Sprintf("   ID: %s\n", r.ID))
		sb.WriteString(fmt.Sprintf("   Excerpt: %s\n\n", r.Excerpt))
	}

	return textResult(sb.String()), nil, nil
}

func (s *Server) handleReadEntry(ctx context.Context, _ *mcp.CallToolRequest, args readEntryArgs) (*mcp.CallToolResult, any, error) {
	if args.ID == "" {
		return nil, nil, fmt.Errorf("id is required")
	}

	content, err := s.search.ReadEntry(ctx, args.ID)
	if err != nil {
		return nil, nil, err
	}

	return textResult(content), nil, nil
}

func (s *Server) handleListRecent(ctx context.Context, _ *mcp.CallToolRequest, args listRecentArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	days := args.Days
	if days <= 0 {
		days = 30
	}

	results, err := s.search.ListRecent(ctx, limit, days)
	if err != nil {
		return nil, nil, err
	}

	if len(results) == 0 {
		return textResult(fmt.Sprintf("No entries found in the last %d days.", days)), nil, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Recent entries (last %d days):\n\n", days))
	for i, r := range results {
		t := time.UnixMilli(r.CreatedAt)
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, t.Format("2006-01-02")))
		sb.WriteString(fmt.Sprintf("   Sections: %s\n", strings.Join(r.Sections, ", ")))
		sb.WriteString(fmt.Sprintf("   ID: %s\n", r.ID))
		sb.WriteString(fmt.Sprintf("   Excerpt: %s\n\n", r.Excerpt))
	}

	return textResult(sb.String()), nil, nil
}

func (s *Server) handleReadRecent(ctx context.Context, _ *mcp.CallToolRequest, args readRecentArgs) (*mcp.CallToolResult, any, error) {
	limit := args.Limit
	if limit <= 0 {
		limit = 5
	}

	results, err := s.search.ReadRecentEntries(ctx, limit)
	if err != nil {
		return nil, nil, err
	}

	if len(results) == 0 {
		return textResult("No recent entries found."), nil, nil
	}

	var sb strings.Builder
	for i, r := range results {
		t := time.UnixMilli(r.CreatedAt)
		sb.WriteString(fmt.Sprintf("--- Entry %d (%s) ---\n", i+1, t.Format("2006-01-02")))
		sb.WriteString(fmt.Sprintf("ID: %s\n\n", r.ID))
		sb.WriteString(r.Content)
		sb.WriteString("\n\n")
	}

	return textResult(sb.String()), nil, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}
