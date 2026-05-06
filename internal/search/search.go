package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
)

type Service struct {
	db       *sql.DB
	embedder embedding.Embedder
}

func NewService(db *sql.DB, embedder embedding.Embedder) *Service {
	return &Service{db: db, embedder: embedder}
}

type SearchResult struct {
	ID        string   `json:"id"`
	Score     float64  `json:"score"`
	Content   string   `json:"content"`
	Sections  []string `json:"sections"`
	CreatedAt int64    `json:"created_at"`
	Excerpt   string   `json:"excerpt"`
}

type SearchOptions struct {
	Limit     int
	Sections  []string
	DateRange *DateRange
}

type DateRange struct {
	Start *time.Time
	End   *time.Time
}

func (s *Service) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	if s.embedder == nil {
		return nil, fmt.Errorf("no embedder available for search")
	}

	queryVec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// sqlite-vec KNN: query vec table, then join with entries
	// Fetch more than limit to allow post-filtering by section/date
	fetchLimit := opts.Limit * 3
	if fetchLimit < 30 {
		fetchLimit = 30
	}

	vecRows, err := s.db.QueryContext(ctx,
		`SELECT entry_id, distance FROM entry_embeddings WHERE embedding MATCH ? ORDER BY distance LIMIT ?`,
		journal.Float32ToJSON(queryVec), fetchLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("vec search: %w", err)
	}
	defer vecRows.Close()

	type vecHit struct {
		entryID  string
		distance float64
	}
	var hits []vecHit
	for vecRows.Next() {
		var h vecHit
		if err := vecRows.Scan(&h.entryID, &h.distance); err != nil {
			return nil, fmt.Errorf("scan vec: %w", err)
		}
		hits = append(hits, h)
	}
	if err := vecRows.Err(); err != nil {
		return nil, err
	}

	if len(hits) == 0 {
		return nil, nil
	}

	var results []SearchResult
	for _, h := range hits {
		var content, sectionsJSON string
		var createdAt int64
		err := s.db.QueryRowContext(ctx,
			`SELECT content, sections, created_at FROM entries WHERE id = ?`, h.entryID,
		).Scan(&content, &sectionsJSON, &createdAt)
		if err != nil {
			continue
		}

		var sections []string
		json.Unmarshal([]byte(sectionsJSON), &sections)

		if opts.DateRange != nil {
			t := time.UnixMilli(createdAt)
			if opts.DateRange.Start != nil && t.Before(*opts.DateRange.Start) {
				continue
			}
			if opts.DateRange.End != nil && t.After(*opts.DateRange.End) {
				continue
			}
		}

		if len(opts.Sections) > 0 && !hasMatchingSection(sections, opts.Sections) {
			continue
		}

		results = append(results, SearchResult{
			ID:        h.entryID,
			Score:     1.0 - h.distance,
			Content:   content,
			Sections:  sections,
			CreatedAt: createdAt,
			Excerpt:   generateExcerpt(content, query, 200),
		})

		if len(results) >= opts.Limit {
			break
		}
	}

	return results, nil
}

func (s *Service) ListRecent(ctx context.Context, limit, days int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}
	if days <= 0 {
		days = 30
	}

	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, content, sections, created_at FROM entries WHERE created_at >= ? ORDER BY created_at DESC LIMIT ?`,
		cutoff, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list recent: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var sectionsJSON string
		if err := rows.Scan(&r.ID, &r.Content, &sectionsJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		json.Unmarshal([]byte(sectionsJSON), &r.Sections)
		r.Score = 1
		r.Excerpt = generateExcerpt(r.Content, "", 150)
		results = append(results, r)
	}

	return results, rows.Err()
}

func (s *Service) ReadRecentEntries(ctx context.Context, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, content, sections, created_at FROM entries ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("read recent: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var sectionsJSON string
		if err := rows.Scan(&r.ID, &r.Content, &sectionsJSON, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		json.Unmarshal([]byte(sectionsJSON), &r.Sections)
		r.Score = 1
		results = append(results, r)
	}

	return results, rows.Err()
}

func (s *Service) ReadEntry(ctx context.Context, id string) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx,
		`SELECT content FROM entries WHERE id = ?`, id,
	).Scan(&content)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("entry not found: %s", id)
	}
	if err != nil {
		return "", fmt.Errorf("read entry: %w", err)
	}
	return content, nil
}

func hasMatchingSection(entrySections, filterSections []string) bool {
	for _, fs := range filterSections {
		for _, es := range entrySections {
			if strings.EqualFold(es, fs) {
				return true
			}
		}
	}
	return false
}

func generateExcerpt(text, query string, maxLength int) string {
	if query == "" || strings.TrimSpace(query) == "" {
		if len(text) <= maxLength {
			return text
		}
		return text[:maxLength] + "..."
	}

	queryWords := strings.Fields(strings.ToLower(query))
	textLower := strings.ToLower(text)

	bestPos := 0
	bestScore := 0

	for i := 0; i <= len(text)-maxLength; i += 20 {
		end := i + maxLength
		if end > len(text) {
			end = len(text)
		}
		window := textLower[i:end]
		score := 0
		for _, word := range queryWords {
			if strings.Contains(window, word) {
				score++
			}
		}
		if score > bestScore {
			bestScore = score
			bestPos = i
		}
	}

	end := bestPos + maxLength
	if end > len(text) {
		end = len(text)
	}
	excerpt := text[bestPos:end]
	if bestPos > 0 {
		excerpt = "..." + excerpt
	}
	if end < len(text) {
		excerpt += "..."
	}
	return excerpt
}
