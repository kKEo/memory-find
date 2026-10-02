package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/journal"
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

const (
	// Reciprocal rank fusion parameters combining the vector and keyword
	// (BM25) result lists into a single ranking.
	rrfK      = 60
	alphaVec  = 0.6
	alphaBM25 = 0.4

	defaultSearchLimit = 10
	// maxSearchLimit bounds how many results a single search/list call can
	// return, so a large or accidental "limit" argument can't dump an
	// entire journal into a tool response.
	maxSearchLimit = 200

	fetchMultiplier = 3
	minFetchLimit   = 30
	// maxFetchLimit bounds how wide the candidate pool is allowed to grow
	// when a section/date filter can't be pushed into a single SQL IN
	// clause (see maxSQLINParams) and has to be applied by widening the
	// unfiltered query instead.
	maxFetchLimit = 2000

	// maxSQLINParams caps how many entry IDs are inlined into a single
	// "IN (?, ?, ...)" filter. Below this, filters are pushed directly
	// into the vector and keyword queries, which is both exact and (at
	// personal-journal scale) fast. Above it, the queries run unfiltered
	// against a widened candidate pool and filtering happens in Go instead
	// — still exact, just less precisely targeted.
	maxSQLINParams = 500

	// recencyHalfLifeDays controls how quickly older entries lose ranking
	// weight. This is a mild tie-breaker, not a hard cutoff: decay only
	// ever scales the fused score by a factor in [0.8, 1.0].
	recencyHalfLifeDays = 90.0
)

func clampLimit(limit, fallback int) int {
	if limit <= 0 {
		return fallback
	}
	if limit > maxSearchLimit {
		return maxSearchLimit
	}
	return limit
}

// Search runs a hybrid vector + keyword (BM25) search over journal entries,
// fusing the two ranked lists with reciprocal rank fusion and applying a
// mild recency tie-breaker before returning the top opts.Limit results.
//
// A missing or failing embedder degrades to keyword-only search rather than
// failing the whole request; a failing keyword search analogously degrades
// to vector-only. Only if both signals are entirely unavailable does Search
// return no results.
func (s *Service) Search(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, error) {
	opts.Limit = clampLimit(opts.Limit, defaultSearchLimit)

	fetchLimit := opts.Limit * fetchMultiplier
	if fetchLimit < minFetchLimit {
		fetchLimit = minFetchLimit
	}

	hasFilter := len(opts.Sections) > 0 || opts.DateRange != nil
	var allowed map[string]struct{}
	if hasFilter {
		ids, err := s.filteredEntryIDs(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("resolve filters: %w", err)
		}
		if len(ids) == 0 {
			return nil, nil
		}
		allowed = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			allowed[id] = struct{}{}
		}
	}

	vecRanks := make(map[string]int)
	if s.embedder != nil {
		queryVec, err := s.embedder.Embed(ctx, query)
		if err != nil {
			// Degrade to keyword-only rather than failing the whole
			// search: the write path already tolerates a missing
			// embedder, and a query-time embedding error (a transient
			// model issue, say) shouldn't be any less forgiving.
			fmt.Fprintf(os.Stderr, "warning: query embedding failed, falling back to keyword search: %v\n", err)
		} else if err := s.rankByVector(ctx, queryVec, allowed, fetchLimit, vecRanks); err != nil {
			return nil, fmt.Errorf("vector search: %w", err)
		}
	}

	bm25Ranks := make(map[string]int)
	if ftsQuery := fts5Query(query); ftsQuery != "" {
		if err := s.rankByKeyword(ctx, ftsQuery, allowed, fetchLimit, bm25Ranks); err != nil {
			// Symmetric with the embedder fallback above: a keyword-search
			// failure degrades to vector-only rather than failing outright.
			fmt.Fprintf(os.Stderr, "warning: keyword search failed, falling back to vector search: %v\n", err)
		}
	}

	if len(vecRanks) == 0 && len(bm25Ranks) == 0 {
		return nil, nil
	}

	candidates := fuseRanks(vecRanks, bm25Ranks)
	if len(candidates) > fetchLimit {
		candidates = candidates[:fetchLimit]
	}

	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[i] = c.id
	}
	rows, err := s.fetchEntries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("fetch entries: %w", err)
	}

	nowMs := time.Now().UnixMilli()
	filtered := candidates[:0]
	for _, c := range candidates {
		row, ok := rows[c.id]
		if !ok {
			continue
		}
		c.score *= recencyFactor(nowMs, row.createdAt)
		filtered = append(filtered, c)
	}
	candidates = filtered

	// Recency perturbs the fused rank score, so the list must be re-sorted
	// after applying it — sorting once up front and truncating before
	// decay (as this used to) means decay can reorder within the returned
	// page but the displayed ordering and score never reflect it.
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	results := make([]SearchResult, 0, opts.Limit)
	for _, c := range candidates {
		row := rows[c.id]
		results = append(results, SearchResult{
			ID:        c.id,
			Score:     c.score,
			Content:   row.content,
			Sections:  row.sections,
			CreatedAt: row.createdAt,
			Excerpt:   generateExcerpt(row.content, query, 200),
		})
		if len(results) >= opts.Limit {
			break
		}
	}

	normalizeScores(results)

	return results, nil
}

// filteredEntryIDs returns the IDs of entries matching opts.Sections and
// opts.DateRange, computed directly in SQL so a filtered search sees every
// matching entry rather than only whichever ones happened to survive an
// earlier top-K truncation.
func (s *Service) filteredEntryIDs(ctx context.Context, opts SearchOptions) ([]string, error) {
	q := `SELECT id FROM entries e WHERE 1=1`
	var args []any

	if opts.DateRange != nil {
		if opts.DateRange.Start != nil {
			q += ` AND e.created_at >= ?`
			args = append(args, opts.DateRange.Start.UnixMilli())
		}
		if opts.DateRange.End != nil {
			q += ` AND e.created_at <= ?`
			args = append(args, opts.DateRange.End.UnixMilli())
		}
	}
	if len(opts.Sections) > 0 {
		q += ` AND EXISTS (SELECT 1 FROM json_each(e.sections) WHERE LOWER(json_each.value) IN (` + placeholders(len(opts.Sections)) + `))`
		for _, sec := range opts.Sections {
			args = append(args, strings.ToLower(sec))
		}
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// rankByVector runs the vector KNN leg of the search, writing the 1-based
// rank of each matching entry_id into out.
//
// Unlike rankByKeyword, an entry_id filter is never pushed into this
// query. sqlite-vec's KNN planner has a sharp edge here: SQLite collapses
// a single-value "IN (?)" into a plain equality constraint, and a vec0
// virtual table can't plan a KNN query around an equality constraint on
// its primary key at the same time as a LIMIT/k constraint — it errors
// with "A LIMIT or 'k = ?' constraint is required on vec0 knn queries."
// even though the LIMIT is right there. Multi-value IN clauses don't
// trigger this, but a filtered search's allowed-ID set can easily contain
// just one entry, so the safe rule is: never push it into the vec0 query.
// Instead, when allowed is non-nil, this runs unfiltered against a
// widened candidate pool and membership is checked here in Go.
func (s *Service) rankByVector(ctx context.Context, queryVec []float32, allowed map[string]struct{}, limit int, out map[string]int) error {
	effLimit := limit
	if allowed != nil {
		effLimit = maxFetchLimit
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id, distance FROM entry_embeddings WHERE embedding MATCH ? ORDER BY distance LIMIT ?`,
		journal.Float32ToJSON(queryVec), effLimit,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	rank := 1
	for rows.Next() {
		var entryID string
		var distance float64
		if err := rows.Scan(&entryID, &distance); err != nil {
			return err
		}
		if allowed != nil {
			if _, ok := allowed[entryID]; !ok {
				continue
			}
		}
		out[entryID] = rank
		rank++
	}
	return rows.Err()
}

// rankByKeyword runs the FTS5/BM25 leg of the search. See rankByVector for
// the allowed-set filtering strategy, which is identical here.
func (s *Service) rankByKeyword(ctx context.Context, ftsQuery string, allowed map[string]struct{}, limit int, out map[string]int) error {
	q := `SELECT entry_id, rank FROM entries_fts WHERE content MATCH ?`
	args := []any{ftsQuery}

	pushFilter := allowed != nil && len(allowed) <= maxSQLINParams
	effLimit := limit
	if pushFilter {
		ids := make([]any, 0, len(allowed))
		for id := range allowed {
			ids = append(ids, id)
		}
		q += ` AND entry_id IN (` + placeholders(len(ids)) + `)`
		args = append(args, ids...)
	} else if allowed != nil {
		effLimit = maxFetchLimit
	}

	q += ` ORDER BY rank LIMIT ?`
	args = append(args, effLimit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	rank := 1
	for rows.Next() {
		var entryID string
		var bm25Rank float64
		if err := rows.Scan(&entryID, &bm25Rank); err != nil {
			return err
		}
		if !pushFilter && allowed != nil {
			if _, ok := allowed[entryID]; !ok {
				continue
			}
		}
		out[entryID] = rank
		rank++
	}
	return rows.Err()
}

type scored struct {
	id    string
	score float64
}

// fuseRanks combines the vector and keyword rank lists with weighted
// reciprocal rank fusion, sorted by descending fused score. An entry
// missing from one leg simply doesn't get that leg's term — there's no
// separate "missing" penalty to reason about.
func fuseRanks(vecRanks, bm25Ranks map[string]int) []scored {
	allIDs := make(map[string]struct{}, len(vecRanks)+len(bm25Ranks))
	for id := range vecRanks {
		allIDs[id] = struct{}{}
	}
	for id := range bm25Ranks {
		allIDs[id] = struct{}{}
	}

	candidates := make([]scored, 0, len(allIDs))
	for id := range allIDs {
		var score float64
		if r, ok := vecRanks[id]; ok {
			score += alphaVec / float64(rrfK+r)
		}
		if r, ok := bm25Ranks[id]; ok {
			score += alphaBM25 / float64(rrfK+r)
		}
		candidates = append(candidates, scored{id, score})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	return candidates
}

type entryRow struct {
	content   string
	sections  []string
	createdAt int64
}

// fetchEntries batch-loads entries by ID in a single query, replacing what
// used to be one SELECT per candidate.
func (s *Service) fetchEntries(ctx context.Context, ids []string) (map[string]entryRow, error) {
	out := make(map[string]entryRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	q := `SELECT id, content, sections, created_at FROM entries WHERE id IN (` + placeholders(len(ids)) + `)`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id, content, sectionsJSON string
		var createdAt int64
		if err := rows.Scan(&id, &content, &sectionsJSON, &createdAt); err != nil {
			return nil, err
		}
		var sections []string
		if err := json.Unmarshal([]byte(sectionsJSON), &sections); err != nil {
			return nil, fmt.Errorf("unmarshal sections for %s: %w", id, err)
		}
		out[id] = entryRow{content: content, sections: sections, createdAt: createdAt}
	}
	return out, rows.Err()
}

func recencyFactor(nowMs, createdAt int64) float64 {
	ageDays := float64(nowMs-createdAt) / (24 * 60 * 60 * 1000)
	decay := math.Pow(0.5, ageDays/recencyHalfLifeDays)
	return 0.8 + 0.2*decay
}

// normalizeScores rescales scores so the top result is ~1.0. Requires
// results to already be sorted by descending score — it does not sort
// itself, so this must run after Search's final ordering, not before it.
func normalizeScores(results []SearchResult) {
	if len(results) == 0 || results[0].Score <= 0 {
		return
	}
	max := results[0].Score
	for i := range results {
		results[i].Score /= max
	}
}

func placeholders(n int) string {
	ph := make([]string, n)
	for i := range ph {
		ph[i] = "?"
	}
	return strings.Join(ph, ",")
}

// fts5Query builds an FTS5 MATCH expression that finds entries containing
// ANY of the query's words (OR), not entries containing ALL of them (AND).
// FTS5's implicit operator between bare/quoted terms is AND, which made
// search_journal effectively require every word of a natural-language
// query to appear in the same entry — for anything but a one- or two-word
// query that's close to never true. Returns "" if the query has no usable
// terms, in which case the caller should skip the keyword leg entirely
// rather than issuing "MATCH ”", which FTS5 rejects as a syntax error.
func fts5Query(query string) string {
	var terms []string
	for _, w := range strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_'
	}) {
		if len(w) > 1 {
			terms = append(terms, `"`+w+`"`)
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

func (s *Service) ListRecent(ctx context.Context, limit, days int) ([]SearchResult, error) {
	limit = clampLimit(limit, defaultSearchLimit)
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
		if err := json.Unmarshal([]byte(sectionsJSON), &r.Sections); err != nil {
			return nil, fmt.Errorf("unmarshal sections for %s: %w", r.ID, err)
		}
		r.Score = 1
		r.Excerpt = generateExcerpt(r.Content, "", 150)
		results = append(results, r)
	}

	return results, rows.Err()
}

func (s *Service) ReadRecentEntries(ctx context.Context, limit int) ([]SearchResult, error) {
	limit = clampLimit(limit, 5)

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
		if err := json.Unmarshal([]byte(sectionsJSON), &r.Sections); err != nil {
			return nil, fmt.Errorf("unmarshal sections for %s: %w", r.ID, err)
		}
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sections: %w", err)
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
	// AVG() returns a float even over integer lengths, so scanning into an
	// integer type fails whenever the mean is not whole (e.g. 83.5).
	var avgLength sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `SELECT AVG(LENGTH(content)) FROM entries`).Scan(&avgLength)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("avg length: %w", err)
	}
	if avgLength.Valid {
		stats.AvgEntryLength = int(math.Round(avgLength.Float64))
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

// truncateAtBoundary shortens text to at most maxLength runes, preferring
// to cut at a sentence end or, failing that, whitespace. Operating on
// runes (rather than bytes, as this used to) guarantees the result is
// always valid UTF-8 — a byte-index cut can land in the middle of a
// multi-byte rune for any non-ASCII text.
func truncateAtBoundary(text string, maxLength int) string {
	runes := []rune(text)
	if len(runes) <= maxLength {
		return text
	}

	cut := runes[:maxLength]
	for i := len(cut) - 1; i > maxLength/2; i-- {
		switch cut[i] {
		case '.', '?', '!':
			return string(cut[:i+1]) + "..."
		}
	}
	for i := len(cut) - 1; i > maxLength/2; i-- {
		if cut[i] == ' ' {
			return string(cut[:i]) + "..."
		}
	}
	return string(cut) + "..."
}
