package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
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

	fetchLimit := opts.Limit * 3
	if fetchLimit < 30 {
		fetchLimit = 30
	}

	vecRanks := make(map[string]int)
	bm25Ranks := make(map[string]int)

	if s.embedder != nil {
		queryVec, err := s.embedder.Embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("embed query: %w", err)
		}

		vecRows, err := s.db.QueryContext(ctx,
			`SELECT entry_id, distance FROM entry_embeddings WHERE embedding MATCH ? ORDER BY distance LIMIT ?`,
			journal.Float32ToJSON(queryVec), fetchLimit,
		)
		if err != nil {
			return nil, fmt.Errorf("vec search: %w", err)
		}
		rank := 1
		for vecRows.Next() {
			var entryID string
			var distance float64
			if err := vecRows.Scan(&entryID, &distance); err != nil {
				vecRows.Close()
				return nil, fmt.Errorf("scan vec: %w", err)
			}
			vecRanks[entryID] = rank
			rank++
		}
		vecRows.Close()
		if err := vecRows.Err(); err != nil {
			return nil, err
		}
	}

	bm25Rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id, rank FROM entries_fts WHERE content MATCH ? ORDER BY rank LIMIT ?`,
		fts5Escape(query), fetchLimit,
	)
	if err == nil {
		rank := 1
		for bm25Rows.Next() {
			var entryID string
			var bm25rank float64
			if err := bm25Rows.Scan(&entryID, &bm25rank); err != nil {
				break
			}
			bm25Ranks[entryID] = rank
			rank++
		}
		bm25Rows.Close()
	}

	if len(vecRanks) == 0 && len(bm25Ranks) == 0 {
		return nil, nil
	}

	const (
		rrfK           = 60
		alphaVec       = 0.6
		alphaBM25      = 0.4
		missingPenalty = 1000
	)

	allIDs := make(map[string]struct{})
	for id := range vecRanks {
		allIDs[id] = struct{}{}
	}
	for id := range bm25Ranks {
		allIDs[id] = struct{}{}
	}

	type scored struct {
		id    string
		score float64
	}
	candidates := make([]scored, 0, len(allIDs))
	for id := range allIDs {
		vr := missingPenalty
		if r, ok := vecRanks[id]; ok {
			vr = r
		}
		br := missingPenalty
		if r, ok := bm25Ranks[id]; ok {
			br = r
		}
		score := alphaVec/float64(rrfK+vr) + alphaBM25/float64(rrfK+br)
		candidates = append(candidates, scored{id, score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if len(candidates) > fetchLimit {
		candidates = candidates[:fetchLimit]
	}

	nowMs := time.Now().UnixMilli()
	var results []SearchResult
	for _, c := range candidates {
		var content, sectionsJSON string
		var createdAt int64
		err := s.db.QueryRowContext(ctx,
			`SELECT content, sections, created_at FROM entries WHERE id = ?`, c.id,
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

		ageDays := float64(nowMs-createdAt) / (24 * 60 * 60 * 1000)
		decay := math.Pow(0.5, ageDays/90.0)
		score := c.score * (0.8 + 0.2*decay)

		results = append(results, SearchResult{
			ID:        c.id,
			Score:     score,
			Content:   content,
			Sections:  sections,
			CreatedAt: createdAt,
			Excerpt:   generateExcerpt(content, query, 200),
		})

		if len(results) >= opts.Limit {
			break
		}
	}

	if len(results) > 0 {
		maxScore := results[0].Score
		for _, r := range results[1:] {
			if r.Score > maxScore {
				maxScore = r.Score
			}
		}
		if maxScore > 0 {
			for i := range results {
				results[i].Score = results[i].Score / maxScore
			}
		}
	}

	return results, nil
}

func fts5Escape(query string) string {
	words := strings.Fields(query)
	escaped := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ReplaceAll(w, `"`, ``)
		if w != "" {
			escaped = append(escaped, `"`+w+`"`)
		}
	}
	return strings.Join(escaped, " ")
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

type JournalStats struct {
	TotalEntries          int            `json:"total_entries"`
	EntriesWithEmbeddings int            `json:"entries_with_embeddings"`
	EarliestEntry         time.Time      `json:"earliest_entry"`
	LatestEntry           time.Time      `json:"latest_entry"`
	SectionCounts         map[string]int `json:"section_counts"`
	RecentActivity        map[string]int `json:"recent_activity"`
	DatabasePath          string         `json:"database_path"`
	DatabaseSizeMB        float64        `json:"database_size_mb"`
	AvgEntryLength        int            `json:"avg_entry_length"`
}

func (s *Service) GetStats(ctx context.Context) (*JournalStats, error) {
	stats := &JournalStats{
		SectionCounts:  make(map[string]int),
		RecentActivity: make(map[string]int),
	}

	// Query 1: Total entries
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&stats.TotalEntries)
	if err != nil {
		return nil, fmt.Errorf("count entries: %w", err)
	}

	if stats.TotalEntries == 0 {
		return stats, nil
	}

	// Query 2: Entries with embeddings
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entry_embeddings`).Scan(&stats.EntriesWithEmbeddings)
	if err != nil {
		return nil, fmt.Errorf("count embeddings: %w", err)
	}

	// Query 3: Date range
	var earliest, latest int64
	err = s.db.QueryRowContext(ctx, `SELECT MIN(created_at), MAX(created_at) FROM entries`).Scan(&earliest, &latest)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("date range: %w", err)
	}
	if err != sql.ErrNoRows {
		stats.EarliestEntry = time.UnixMilli(earliest)
		stats.LatestEntry = time.UnixMilli(latest)
	}

	// Query 4: Section counts
	rows, err := s.db.QueryContext(ctx, `
		SELECT json_each.value as section, COUNT(*) as count
		FROM entries, json_each(entries.sections)
		GROUP BY json_each.value
		ORDER BY count DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("section counts: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var section string
		var count int
		if err := rows.Scan(&section, &count); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		stats.SectionCounts[section] = count
	}

	// Query 5: Recent activity (7 and 30 days)
	for _, days := range []int{7, 30} {
		cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
		var count int
		err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries WHERE created_at >= ?`, cutoff).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("recent activity %dd: %w", days, err)
		}
		stats.RecentActivity[fmt.Sprintf("%dd", days)] = count
	}

	// Query 6: Average entry length
	var avgLength sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT AVG(LENGTH(content)) FROM entries`).Scan(&avgLength)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("avg length: %w", err)
	}
	if avgLength.Valid {
		stats.AvgEntryLength = int(avgLength.Int64)
	}

	// Query 7: Database path and size
	var dbPath string
	err = s.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list() WHERE name='main'`).Scan(&dbPath)
	if err == nil {
		stats.DatabasePath = dbPath
		if info, err := os.Stat(dbPath); err == nil {
			stats.DatabaseSizeMB = float64(info.Size()) / (1024 * 1024)
		}
	}

	return stats, nil
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
		return truncateAtBoundary(text, maxLength)
	}

	paragraphs := splitParagraphs(text)
	if len(paragraphs) == 0 {
		return truncateAtBoundary(text, maxLength)
	}

	queryWords := strings.Fields(strings.ToLower(query))

	bestIdx := 0
	bestScore := -1
	for i, p := range paragraphs {
		pLower := strings.ToLower(p)
		score := 0
		for _, w := range queryWords {
			score += strings.Count(pLower, w)
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	header := findPrecedingHeader(paragraphs, bestIdx)
	var excerpt string
	if header != "" {
		excerpt = header + "\n\n" + paragraphs[bestIdx]
	} else {
		excerpt = paragraphs[bestIdx]
	}

	if bestIdx+1 < len(paragraphs) && len(excerpt)+len(paragraphs[bestIdx+1])+2 <= maxLength {
		excerpt += "\n\n" + paragraphs[bestIdx+1]
	}

	return truncateAtBoundary(excerpt, maxLength)
}

func splitParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")
	paragraphs := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
	}
	return paragraphs
}

func findPrecedingHeader(paragraphs []string, idx int) string {
	for i := idx; i >= 0; i-- {
		if strings.HasPrefix(paragraphs[i], "## ") {
			if i == idx {
				return ""
			}
			return paragraphs[i]
		}
	}
	return ""
}

func truncateAtBoundary(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	cut := text[:maxLen]
	for i := len(cut) - 1; i > maxLen/2; i-- {
		if cut[i] == '.' || cut[i] == '?' || cut[i] == '!' {
			return cut[:i+1] + "..."
		}
	}
	if idx := strings.LastIndex(cut, " "); idx > maxLen/2 {
		return cut[:idx] + "..."
	}
	return cut + "..."
}
