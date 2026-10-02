package retrieve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/kKEo/memory-find/internal/chunk"
	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/rerank"
)

// Service runs searches over a knowledge base.
type Service struct {
	store      *kb.Store
	db         *sql.DB
	embedder   embedding.Embedder
	profile    Profile
	logQueries bool
	now        func() time.Time
	reranker   rerank.Reranker
}

// WithReranker attaches a cross-encoder; profiles with Rerank=true use it.
func (s *Service) WithReranker(r rerank.Reranker) *Service {
	s.reranker = r
	return s
}

// New builds a Service. embedder may be nil (keyword-only, degraded). When
// the embedder's model declares its own similarity bands, they replace the
// profile's bands and semantic floor: cosine scales differ between models.
func New(store *kb.Store, profile Profile, logQueries bool) *Service {
	if e := store.Embedder(); e != nil {
		if b := e.Info().Bands; b[0] > 0 {
			profile.BandStrong, profile.BandModerate, profile.BandWeak = b[0], b[1], b[2]
			if profile.SemanticFloor > 0 {
				profile.SemanticFloor = b[2]
			}
		}
	}
	return &Service{store: store, db: store.DB(), embedder: store.Embedder(), profile: profile, logQueries: logQueries, now: time.Now}
}

// Scope narrows a search before ranking (docs/schema.md §6).
type Scope struct {
	Namespaces []string   `json:"namespaces,omitempty"`
	Kinds      []string   `json:"kinds,omitempty"`
	Sources    []string   `json:"sources,omitempty"` // source ids or URIs
	Library    string     `json:"library,omitempty"`
	Version    string     `json:"version,omitempty"`
	Tags       []string   `json:"tags,omitempty"`
	DateFrom   *time.Time `json:"date_from,omitempty"`
	DateTo     *time.Time `json:"date_to,omitempty"`
	MinTrust   string     `json:"min_trust,omitempty"`
}

// Request is one search (the research document's eight parameters).
type Request struct {
	Query          string
	Queries        []string
	Mode           string
	Scope          Scope
	Granularity    string
	ResponseFormat string
	MaxTokens      int
	ExcludeIDs     []string
	// Limit is an internal cap (CLI --limit); tools use the budget instead.
	Limit int
}

// Result is one hit.
type Result struct {
	Rank        int      `json:"rank"`
	URI         string   `json:"uri"` // chunk uri at chunk granularity, document uri otherwise
	ChunkURI    string   `json:"chunk_uri"`
	DocumentURI string   `json:"document_uri"`
	Title       string   `json:"title"`
	SectionPath string   `json:"section_path,omitempty"`
	Content     string   `json:"content"` // one-liner (concise) or the passage (detailed/explain)
	Score       float64  `json:"score"`   // the ordering key (fused × recency); not comparable across queries
	Relevance   *float64 `json:"relevance"`
	Band        string   `json:"band"`
	Provenance  ProvRef  `json:"provenance"`
	Why         *Why     `json:"why,omitempty"`
}

// Response is what a search returns.
type Response struct {
	Results  []Result `json:"results"`
	Trace    *Trace   `json:"trace,omitempty"`
	Degraded bool     `json:"degraded"`
	Reason   string   `json:"reason,omitempty"` // set on abstention
	Hint     string   `json:"hint,omitempty"`
}

// candidate accumulates one chunk's evidence across arms.
type candidate struct {
	chunkID int64
	docID   string
	arms    map[string]*ArmHit
	fused   float64
	// loaded metadata
	title, section, text, kind, trust, origin, namespace, sourceURI, version, library string
	ord, estTokens, revision                                                          int
	updatedAt, fetchedAt                                                              int64
	ttl                                                                               sql.NullInt64
	recency, final                                                                    float64
}

var identifierRe = regexp.MustCompile(`(^|\s)([A-Za-z]+[_./:]+[A-Za-z0-9_./:-]*|[a-z]+[A-Z][A-Za-z0-9]*|[A-Z][A-Z0-9]+_[A-Z0-9_]+|[A-Z]{2,}-?\d+)(\s|$)`)

// looksLikeIdentifier reports whether a query contains a code-ish token:
// dotted or slashed paths, camelCase, SCREAMING_SNAKE, error codes.
func looksLikeIdentifier(q string) bool { return identifierRe.MatchString(q) }

// Search runs one request.
func (s *Service) Search(ctx context.Context, req Request) (*Response, error) {
	start := s.now()
	p := s.profile
	queries := req.Queries
	if strings.TrimSpace(req.Query) != "" {
		queries = append([]string{req.Query}, queries...)
	}
	var clean []string
	for _, q := range queries {
		if strings.TrimSpace(q) != "" {
			clean = append(clean, strings.TrimSpace(q))
		}
	}
	queries = clean
	if len(queries) == 0 {
		return nil, errors.New("query is required")
	}
	if req.Mode == "" {
		req.Mode = ModeAuto
	}
	if req.Granularity == "" {
		req.Granularity = GranularityChunk
	}
	if req.ResponseFormat == "" {
		req.ResponseFormat = FormatConcise
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 2000
	}
	limit := req.Limit
	if limit <= 0 {
		limit = p.DefaultLimit
	}
	if limit > p.MaxLimit {
		limit = p.MaxLimit
	}
	for _, v := range [][2]string{{"mode", req.Mode}, {"granularity", req.Granularity}, {"response_format", req.ResponseFormat}} {
		if err := validateEnum(v[0], v[1]); err != nil {
			return nil, err
		}
	}

	tr := &Trace{ModeRequested: req.Mode, Profile: p.Name, CandidatesPerArm: map[string]int{}, LatencyMsPerArm: map[string]float64{}}

	// Resolve arms.
	arms, reason := resolveArms(req.Mode, queries)
	if len(p.Arms) > 0 {
		var kept []string
		for _, a := range arms {
			if contains(p.Arms, a) {
				kept = append(kept, a)
			}
		}
		arms = kept
		reason += "; profile " + p.Name + " restricts arms to " + strings.Join(p.Arms, "+")
	}
	tr.RoutingReason = reason
	if s.embedder == nil && contains(arms, ArmSemantic) {
		arms = remove(arms, ArmSemantic)
		tr.Degraded = Degraded{Flag: true, Reason: "no embedding model available; semantic arm skipped"}
	}
	if len(arms) == 0 {
		tr.ModeResolved = "none"
		return s.abstain(ctx, req, queries, tr, start, "no retrieval arm could run", "configure an embedding model or use mode=keyword")
	}

	// Scope → SQL fragment (pre-top-k), plus counts for the trace.
	where, args, err := scopeSQL(req.Scope)
	if err != nil {
		return nil, err
	}
	tr.Filtered.ByScope = describeScope(req.Scope)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d JOIN sources s ON s.id = d.source_id WHERE `+where, args...).Scan(&tr.Filtered.LiveDocs); err != nil {
		return nil, fmt.Errorf("count scope: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d JOIN sources s ON s.id = d.source_id WHERE (d.deleted_at IS NOT NULL OR d.superseded_by IS NOT NULL) AND `+scopeOnly(where), args...).Scan(&tr.Filtered.ByRevocation); err != nil {
		return nil, fmt.Errorf("count revoked: %w", err)
	}
	if req.Scope.MinTrust != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents d JOIN sources s ON s.id = d.source_id WHERE d.deleted_at IS NULL AND d.superseded_by IS NULL AND s.trust NOT IN (`+trustAtLeast(req.Scope.MinTrust)+`)`).Scan(&tr.Filtered.ByMinTrust); err != nil {
			return nil, err
		}
	}

	// Run arms. Each (arm, query) list is a voter; its weight is the arm's
	// weight divided by the number of queries (RAG-Fusion style).
	cands := map[int64]*candidate{}
	var modelID string
	nq := float64(len(queries))
	semanticDown := false
	for _, arm := range arms {
		t0 := s.now()
		for qi, q := range queries {
			if arm == ArmSemantic && semanticDown {
				continue
			}
			var hits []armRow
			switch arm {
			case ArmKeyword:
				hits, err = s.keywordArm(ctx, "chunks_fts", ftsQueryStemmed(q), where, args, p.FetchDepth)
			case ArmExact:
				hits, err = s.keywordArm(ctx, "chunks_fts_exact", ftsQueryExact(q), where, args, p.FetchDepth)
			case ArmSemantic:
				hits, modelID, err = s.semanticArm(ctx, q, where, args, p.FetchDepth)
				if err != nil && modelID == "" {
					// Embedding failed: degrade rather than fail the search.
					tr.Degraded = Degraded{Flag: true, Reason: "query embedding failed: " + err.Error()}
					arms = remove(arms, ArmSemantic)
					semanticDown = true
					continue
				}
			}
			if err != nil {
				return nil, fmt.Errorf("%s arm: %w", arm, err)
			}
			tr.CandidatesPerArm[arm] += len(hits)
			w := p.Weights[arm] / nq
			for rank, h := range hits {
				c := cands[h.chunkID]
				if c == nil {
					c = &candidate{chunkID: h.chunkID, docID: h.docID, arms: map[string]*ArmHit{}}
					cands[h.chunkID] = c
				}
				contrib := w / float64(p.RRFK+rank+1)
				c.fused += contrib
				ah := c.arms[arm]
				if ah == nil {
					r := rank + 1
					raw := h.raw
					ah = &ArmHit{Arm: arm, Rank: &r, Raw: &raw, RawKind: h.rawKind}
					c.arms[arm] = ah
				} else if rank+1 < *ah.Rank {
					*ah.Rank = rank + 1
					*ah.Raw = h.raw
				}
				ah.Contribution += contrib
				if qi == 0 || len(ah.MatchedTerms) == 0 {
					ah.MatchedTerms = mergeTerms(ah.MatchedTerms, h.terms)
				}
			}
		}
		tr.LatencyMsPerArm[arm] = ms(s.now().Sub(t0))
	}
	tr.ArmsRun = arms
	tr.ModeResolved = strings.Join(arms, "+")
	tr.ModelID = modelID
	if p.Fusion == FusionMinMax {
		minMaxFuse(cands, p)
	}

	// Semantic-only candidates below the floor are noise, not matches.
	if p.SemanticFloor > 0 {
		for id, c := range cands {
			sem := c.arms[ArmSemantic]
			if sem != nil && len(c.arms) == 1 && sem.Raw != nil && *sem.Raw < p.SemanticFloor {
				delete(cands, id)
				tr.Filtered.BySemanticFloor++
			}
		}
	}
	if len(cands) == 0 {
		reason := "no arm matched"
		if tr.Filtered.BySemanticFloor > 0 {
			reason = fmt.Sprintf("nothing matched by keyword and the %d nearest passages are below the similarity floor (%.2f)", tr.Filtered.BySemanticFloor, p.SemanticFloor)
		}
		return s.abstain(ctx, req, queries, tr, start, reason, hintForScope(req.Scope))
	}

	// Load metadata, apply recency, aggregate, sort.
	list := make([]*candidate, 0, len(cands))
	for _, c := range cands {
		list = append(list, c)
	}
	if err := s.loadMeta(ctx, list); err != nil {
		return nil, err
	}
	nowMs := s.now().UnixMilli()
	for _, c := range list {
		c.recency = 1
		if contains(p.RecencyKinds, c.kind) {
			ageDays := math.Max(0, float64(nowMs-c.updatedAt)/86400000)
			c.recency = p.RecencyFloor + (1-p.RecencyFloor)*math.Pow(0.5, ageDays/p.HalfLifeDays)
		}
		c.final = c.fused * c.recency
	}
	if req.Granularity == GranularityDocument {
		best := map[string]*candidate{}
		for _, c := range list {
			if b := best[c.docID]; b == nil || c.final > b.final || (c.final == b.final && c.chunkID < b.chunkID) {
				best[c.docID] = c
			}
		}
		list = list[:0]
		for _, c := range best {
			list = append(list, c)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].final != list[j].final {
			return list[i].final > list[j].final
		}
		return list[i].chunkID < list[j].chunkID
	})

	// Exclusions (already-seen results travel as arguments; MCP is stateless).
	if len(req.ExcludeIDs) > 0 {
		ex := map[string]bool{}
		for _, id := range req.ExcludeIDs {
			ex[id] = true
		}
		kept := list[:0]
		for _, c := range list {
			if !ex[chunkURI(c.chunkID)] && !ex[docURI(c.docID)] {
				kept = append(kept, c)
			}
		}
		list = kept
	}

	// Optional cross-encoder rerank of the top N (precise profile).
	rerankHits := map[int64]*RerankHit{}
	if p.Rerank && s.reranker != nil && len(list) > 1 {
		n := p.RerankTopN
		if n <= 0 || n > len(list) {
			n = len(list)
		}
		t0 := s.now()
		passages := make([]string, n)
		for i := 0; i < n; i++ {
			passages[i] = list[i].text
		}
		scores, err := s.reranker.Score(ctx, queries[0], passages)
		if err != nil {
			tr.Degraded = Degraded{Flag: true, Reason: "reranker failed: " + err.Error()}
		} else {
			for i := 0; i < n; i++ {
				rerankHits[list[i].chunkID] = &RerankHit{Model: s.reranker.Name(), Score: scores[i], BeforeRank: i + 1}
			}
			head := list[:n]
			sort.SliceStable(head, func(i, j int) bool { return rerankHits[head[i].chunkID].Score > rerankHits[head[j].chunkID].Score })
			tr.Rerank = &RerankTrace{Model: s.reranker.Name(), TopN: n, LatencyMs: ms(s.now().Sub(t0))}
		}
	}

	// Cut: limit, then gap (the gap cut is skipped after reranking: the
	// fused scores no longer define the order).
	tr.Cutoff = Cutoff{Kind: "none", Position: len(list)}
	if len(list) > limit {
		list = list[:limit]
		tr.Cutoff = Cutoff{Kind: "limit", Position: limit}
	}
	if p.CutoffGap > 0 && tr.Rerank == nil {
		for i := 1; i < len(list); i++ {
			if i >= p.MinResults && list[i].final < list[i-1].final*(1-p.CutoffGap) {
				tr.Cutoff = Cutoff{Kind: "gap", Position: i, Gap: 1 - list[i].final/list[i-1].final}
				list = list[:i]
				break
			}
		}
	}

	// Pack to the token budget.
	resp := &Response{Degraded: tr.Degraded.Flag}
	tr.Budget.MaxTokens = req.MaxTokens
	used := 0
	for i, c := range list {
		content := c.text
		if req.ResponseFormat == FormatConcise {
			content = oneLiner(c.text)
		}
		cost := chunk.EstimateTokens(content) + 24 // ~24 tokens of metadata per result
		if used+cost > req.MaxTokens && len(resp.Results) > 0 {
			tr.Budget.TruncatedCount = len(list) - i
			tr.Cutoff = Cutoff{Kind: "budget", Position: i}
			tr.Budget.NarrowHint = hintForScope(req.Scope)
			break
		}
		used += cost
		r := s.result(c, len(resp.Results)+1, content, req)
		if h := rerankHits[c.chunkID]; h != nil && r.Why != nil {
			r.Why.Rerank = h
		}
		resp.Results = append(resp.Results, r)
	}
	tr.Budget.Used = used
	if req.ResponseFormat == FormatExplain {
		resp.Trace = tr
	}
	s.logQuery(ctx, req, queries, tr, resp, start)
	return resp, nil
}

func (s *Service) result(c *candidate, rank int, content string, req Request) Result {
	var relevance *float64
	band := "keyword-only"
	if sem := c.arms[ArmSemantic]; sem != nil && sem.Raw != nil {
		v := *sem.Raw
		relevance = &v
		band = s.band(v)
	}
	prov := ProvRef{SourceURI: c.sourceURI, Version: c.version, Library: c.library, Kind: c.kind, FetchedAt: time.UnixMilli(c.fetchedAt).UTC(), Trust: c.trust, Origin: c.origin, Namespace: c.namespace}
	uri := chunkURI(c.chunkID)
	if req.Granularity == GranularityDocument {
		uri = docURI(c.docID)
	}
	section := c.section
	if section == c.title {
		section = ""
	} else if strings.HasPrefix(section, c.title+" > ") {
		section = section[len(c.title)+3:]
	}
	r := Result{Rank: rank, URI: uri, ChunkURI: chunkURI(c.chunkID), DocumentURI: docURI(c.docID), Title: c.title, SectionPath: section, Content: content, Score: c.final, Relevance: relevance, Band: band, Provenance: prov}
	if req.ResponseFormat == FormatExplain {
		arms := make([]ArmHit, 0, len(c.arms))
		for _, a := range []string{ArmSemantic, ArmKeyword, ArmExact} {
			if h := c.arms[a]; h != nil {
				arms = append(arms, *h)
			}
		}
		fresh := Freshness{}
		if c.ttl.Valid && c.fetchedAt+c.ttl.Int64*1000 < s.now().UnixMilli() {
			fresh.TTLExpired = true
		}
		r.Why = &Why{URI: uri, Chunk: ChunkRef{URI: chunkURI(c.chunkID), Ord: c.ord, SectionPath: c.section, EstTokens: c.estTokens},
			Document: DocRef{URI: docURI(c.docID), Title: c.title, Revision: c.revision}, Arms: arms, Fused: c.fused, RecencyFactor: c.recency, Final: c.final, Rank: rank, Relevance: relevance, Band: band, Provenance: prov, Freshness: fresh}
	}
	return r
}

func (s *Service) band(cos float64) string {
	switch {
	case cos >= s.profile.BandStrong:
		return "strong"
	case cos >= s.profile.BandModerate:
		return "moderate"
	case cos >= s.profile.BandWeak:
		return "weak"
	default:
		return "very-weak"
	}
}

func (s *Service) abstain(ctx context.Context, req Request, queries []string, tr *Trace, start time.Time, reason, hint string) (*Response, error) {
	resp := &Response{Results: []Result{}, Degraded: tr.Degraded.Flag, Reason: reason, Hint: hint}
	tr.Cutoff = Cutoff{Kind: "none"}
	tr.Budget.MaxTokens = req.MaxTokens
	if req.ResponseFormat == FormatExplain {
		resp.Trace = tr
	}
	s.logQuery(ctx, req, queries, tr, resp, start)
	return resp, nil
}

// --- arms ---

type armRow struct {
	chunkID int64
	docID   string
	raw     float64
	rawKind string
	terms   []string
}

// keywordArm runs an FTS5 query over the given index with the scope applied
// inside the query, before the LIMIT. Matched terms come from highlight().
func (s *Service) keywordArm(ctx context.Context, table, match, where string, args []any, depth int) ([]armRow, error) {
	if match == "" {
		return nil, nil
	}
	textCol := 1 // chunks_fts has (context_header, text); exact has (text)
	if table == "chunks_fts_exact" {
		textCol = 0
	}
	q := fmt.Sprintf(`SELECT c.id, c.document_id, bm25(%[1]s), highlight(%[1]s, %[2]d, char(1), char(2))
		FROM %[1]s JOIN chunks c ON c.id = %[1]s.rowid
		JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id
		WHERE %[1]s MATCH ? AND %[3]s ORDER BY bm25(%[1]s) LIMIT ?`, table, textCol, where)
	qargs := append([]any{match}, args...)
	qargs = append(qargs, depth)
	rows, err := s.db.QueryContext(ctx, q, qargs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []armRow
	for rows.Next() {
		var r armRow
		var hl string
		if err := rows.Scan(&r.chunkID, &r.docID, &r.raw, &hl); err != nil {
			return nil, err
		}
		r.rawKind = "bm25"
		r.terms = extractMarked(hl)
		out = append(out, r)
	}
	return out, rows.Err()
}

// semanticArm embeds the query and scans the default model's vectors over
// the scoped chunks. Returns the model id used ("" if embedding failed).
func (s *Service) semanticArm(ctx context.Context, q, where string, args []any, depth int) ([]armRow, string, error) {
	// Queries use the vectors of the model that embeds the query; vectors
	// for other models may coexist in the table.
	modelID := s.embedder.Info().ID
	var have int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunk_vecs WHERE model_id = ?`, modelID).Scan(&have); err != nil {
		return nil, "", err
	}
	if have == 0 {
		return nil, "", fmt.Errorf("no vectors stored for model %s yet (run `memo-mcp reindex`)", modelID)
	}
	vecs, err := s.embedder.EmbedBatch(ctx, []string{q}, embedding.RoleQuery)
	if err != nil {
		return nil, "", err
	}
	qv := kb.EncodeVector(vecs[0])
	sqlq := `SELECT c.id, c.document_id, vec_distance_cosine(v.embedding, ?) AS dist
		FROM chunk_vecs v JOIN chunks c ON c.id = v.chunk_id
		JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id
		WHERE v.model_id = ? AND ` + where + ` ORDER BY dist LIMIT ?`
	qargs := append([]any{qv, modelID}, args...)
	qargs = append(qargs, depth)
	rows, err := s.db.QueryContext(ctx, sqlq, qargs...)
	if err != nil {
		return nil, modelID, err
	}
	defer rows.Close()
	var out []armRow
	for rows.Next() {
		var r armRow
		var dist float64
		if err := rows.Scan(&r.chunkID, &r.docID, &dist); err != nil {
			return nil, modelID, err
		}
		r.raw = 1 - dist // cosine similarity
		r.rawKind = "cosine"
		out = append(out, r)
	}
	return out, modelID, rows.Err()
}

// loadMeta fills candidate metadata in one query per batch of ids.
func (s *Service) loadMeta(ctx context.Context, list []*candidate) error {
	byChunk := map[int64]*candidate{}
	ids := make([]any, 0, len(list))
	ph := make([]string, 0, len(list))
	for _, c := range list {
		byChunk[c.chunkID] = c
		ids = append(ids, c.chunkID)
		ph = append(ph, "?")
	}
	const batch = 400
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		if err := s.loadMetaBatch(ctx, byChunk, ph[start:end], ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) loadMetaBatch(ctx context.Context, byChunk map[int64]*candidate, ph []string, ids []any) error {
	q := `SELECT c.id, c.ord, c.section_path, c.text, c.est_tokens, d.revision, d.updated_at, d.version,
		s.title, s.kind, s.trust, s.origin, s.namespace, s.uri, s.library, s.fetched_at, s.ttl_s
		FROM chunks c JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id
		WHERE c.id IN (` + strings.Join(ph, ",") + `)`
	rows, err := s.db.QueryContext(ctx, q, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var version, uri, library sql.NullString
		var c candidate
		if err := rows.Scan(&id, &c.ord, &c.section, &c.text, &c.estTokens, &c.revision, &c.updatedAt, &version, &c.title, &c.kind, &c.trust, &c.origin, &c.namespace, &uri, &library, &c.fetchedAt, &c.ttl); err != nil {
			return err
		}
		if dst := byChunk[id]; dst != nil {
			dst.ord, dst.section, dst.text, dst.estTokens, dst.revision, dst.updatedAt = c.ord, c.section, c.text, c.estTokens, c.revision, c.updatedAt
			dst.version, dst.title, dst.kind, dst.trust, dst.origin, dst.namespace = version.String, c.title, c.kind, c.trust, c.origin, c.namespace
			dst.sourceURI, dst.library, dst.fetchedAt, dst.ttl = uri.String, library.String, c.fetchedAt, c.ttl
		}
	}
	return rows.Err()
}

// --- scope ---

// scopeSQL builds the WHERE fragment applied inside every arm. It always
// includes the live filter; a version scope replaces the "latest revision"
// half of it so the revision for that version is reachable.
func scopeSQL(sc Scope) (string, []any, error) {
	var parts []string
	var args []any
	parts = append(parts, "d.deleted_at IS NULL")
	if sc.Version != "" {
		parts = append(parts, "d.version = ?")
		args = append(args, sc.Version)
	} else {
		parts = append(parts, "d.superseded_by IS NULL")
	}
	in := func(col string, vals []string) {
		if len(vals) == 0 {
			return
		}
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = "?"
			args = append(args, v)
		}
		parts = append(parts, fmt.Sprintf("%s IN (%s)", col, strings.Join(ph, ",")))
	}
	in("s.namespace", sc.Namespaces)
	in("s.kind", sc.Kinds)
	if len(sc.Sources) > 0 {
		ph := make([]string, len(sc.Sources))
		for i, v := range sc.Sources {
			ph[i] = "?"
			args = append(args, v)
		}
		list := strings.Join(ph, ",")
		parts = append(parts, fmt.Sprintf("(s.id IN (%s) OR s.uri IN (%s))", list, list))
		for _, v := range sc.Sources {
			args = append(args, v)
		}
	}
	if sc.Library != "" {
		parts = append(parts, "s.library = ?")
		args = append(args, sc.Library)
	}
	for _, tag := range sc.Tags {
		parts = append(parts, "EXISTS (SELECT 1 FROM json_each(s.tags_json) t WHERE t.value = ?)")
		args = append(args, tag)
	}
	if sc.DateFrom != nil {
		parts = append(parts, "d.updated_at >= ?")
		args = append(args, sc.DateFrom.UnixMilli())
	}
	if sc.DateTo != nil {
		parts = append(parts, "d.updated_at <= ?")
		args = append(args, sc.DateTo.UnixMilli())
	}
	if sc.MinTrust != "" {
		if err := validateTrust(sc.MinTrust); err != nil {
			return "", nil, err
		}
		parts = append(parts, "s.trust IN ("+trustAtLeast(sc.MinTrust)+")")
	}
	return "(" + strings.Join(parts, " AND ") + ")", args, nil
}

// scopeOnly strips the live-filter half so revoked documents in scope can be
// counted for the trace.
func scopeOnly(where string) string {
	w := strings.Replace(where, "d.deleted_at IS NULL AND d.superseded_by IS NULL AND ", "", 1)
	w = strings.Replace(w, "(d.deleted_at IS NULL AND d.superseded_by IS NULL)", "(1=1)", 1)
	w = strings.Replace(w, "d.deleted_at IS NULL AND ", "", 1)
	w = strings.Replace(w, "(d.deleted_at IS NULL)", "(1=1)", 1)
	return w
}

func trustAtLeast(min string) string {
	switch min {
	case kb.TrustCurated:
		return "'curated'"
	case kb.TrustUser:
		return "'curated','user'"
	default:
		return "'curated','user','agent'"
	}
}

func validateTrust(t string) error {
	switch t {
	case kb.TrustAgent, kb.TrustUser, kb.TrustCurated:
		return nil
	}
	return fmt.Errorf("min_trust must be agent|user|curated, got %q", t)
}

func describeScope(sc Scope) string {
	var parts []string
	if len(sc.Namespaces) > 0 {
		parts = append(parts, "namespaces="+strings.Join(sc.Namespaces, ","))
	}
	if len(sc.Kinds) > 0 {
		parts = append(parts, "kinds="+strings.Join(sc.Kinds, ","))
	}
	if len(sc.Sources) > 0 {
		parts = append(parts, fmt.Sprintf("sources=%d", len(sc.Sources)))
	}
	if sc.Library != "" {
		parts = append(parts, "library="+sc.Library)
	}
	if sc.Version != "" {
		parts = append(parts, "version="+sc.Version)
	}
	if len(sc.Tags) > 0 {
		parts = append(parts, "tags="+strings.Join(sc.Tags, ","))
	}
	if sc.DateFrom != nil || sc.DateTo != nil {
		parts = append(parts, "date range")
	}
	if sc.MinTrust != "" {
		parts = append(parts, "min_trust="+sc.MinTrust)
	}
	if len(parts) == 0 {
		return "all namespaces, live documents"
	}
	return strings.Join(parts, "; ")
}

func hintForScope(sc Scope) string {
	switch {
	case len(sc.Namespaces) == 0:
		return "narrow with scope.namespaces or scope.library; or rephrase with the terms the source uses"
	case sc.Version == "":
		return "narrow with scope.version, or rephrase"
	default:
		return "rephrase the query or widen the scope"
	}
}

// --- query text ---

// ftsQueryStemmed OR-joins the words of a natural-language query for the
// stemmed index, dropping FTS operators and one-character tokens.
func ftsQueryStemmed(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' })
	var terms []string
	for _, w := range words {
		if len([]rune(w)) > 1 {
			terms = append(terms, `"`+strings.ReplaceAll(w, `"`, "")+`"`)
		}
	}
	return strings.Join(terms, " OR ")
}

// ftsQueryExact keeps identifier characters inside tokens so the exact index
// is asked for the identifier as a whole.
func ftsQueryExact(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && !strings.ContainsRune("_.:-/", r)
	})
	var terms []string
	for _, w := range words {
		w = strings.Trim(w, ".:-/")
		if len([]rune(w)) > 1 {
			terms = append(terms, `"`+strings.ReplaceAll(w, `"`, "")+`"`)
		}
	}
	return strings.Join(terms, " OR ")
}

// extractMarked pulls the highlighted terms out of highlight() output.
func extractMarked(s string) []string {
	var out []string
	seen := map[string]bool{}
	for {
		i := strings.IndexByte(s, 1)
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i+1:], 2)
		if j < 0 {
			break
		}
		term := strings.ToLower(s[i+1 : i+1+j])
		if !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
		s = s[i+1+j+1:]
	}
	return out
}

func mergeTerms(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append(a, b...) {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func resolveArms(mode string, queries []string) ([]string, string) {
	switch mode {
	case ModeKeyword:
		return []string{ArmKeyword}, "mode=keyword"
	case ModeExact:
		return []string{ArmExact}, "mode=exact"
	case ModeSemantic:
		return []string{ArmSemantic}, "mode=semantic"
	case ModeHybrid:
		return []string{ArmSemantic, ArmKeyword}, "mode=hybrid"
	}
	for _, q := range queries {
		if looksLikeIdentifier(q) {
			return []string{ArmSemantic, ArmKeyword, ArmExact}, "auto: query contains an identifier-like token, exact arm added"
		}
	}
	return []string{ArmSemantic, ArmKeyword}, "auto: natural-language query, hybrid"
}

// oneLiner is the concise form of a passage: its first sentence or ~160 chars.
func oneLiner(text string) string {
	t := strings.Join(strings.Fields(text), " ")
	if i := strings.IndexAny(t, ".!?"); i > 40 && i < 200 {
		return t[:i+1]
	}
	r := []rune(t)
	if len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return t
}

func (s *Service) logQuery(ctx context.Context, req Request, queries []string, tr *Trace, resp *Response, start time.Time) {
	if !s.logQueries {
		return
	}
	args, _ := json.Marshal(map[string]any{"queries": queries, "mode": req.Mode, "scope": req.Scope, "granularity": req.Granularity, "response_format": req.ResponseFormat, "max_tokens": req.MaxTokens, "exclude_ids": req.ExcludeIDs})
	top := make([]map[string]any, 0, len(resp.Results))
	for _, r := range resp.Results {
		top = append(top, map[string]any{"uri": r.URI, "score": r.Score, "relevance": r.Relevance})
	}
	topJSON, _ := json.Marshal(top)
	trJSON, _ := json.Marshal(tr)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO query_log(ts, args_json, mode, profile, model_id, n_results, top_uris_json, latency_ms, trace_json) VALUES (?,?,?,?,?,?,?,?,?)`,
		start.UnixMilli(), string(args), tr.ModeResolved, tr.Profile, tr.ModelID, len(resp.Results), string(topJSON), int64(ms(s.now().Sub(start))), string(trJSON))
}

func validateEnum(field, got string) error {
	allowed := map[string][]string{
		"mode":            {ModeAuto, ModeHybrid, ModeKeyword, ModeExact, ModeSemantic},
		"granularity":     {GranularityChunk, GranularityDocument},
		"response_format": {FormatConcise, FormatDetailed, FormatExplain},
	}[field]
	for _, a := range allowed {
		if a == got {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s, got %q", field, strings.Join(allowed, "|"), got)
}

func chunkURI(id int64) string   { return fmt.Sprintf("memo://chunk/%d", id) }
func docURI(id string) string    { return "memo://doc/" + id }
func ms(d time.Duration) float64 { return float64(d) / 1e6 }

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func remove(list []string, v string) []string {
	out := make([]string, 0, len(list))
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// minMaxFuse replaces the RRF contributions with score fusion: within each
// arm the raw scores of all candidates are scaled to 0..1 (bm25 is negated
// first, lower is better in FTS5), multiplied by the arm's weight and summed.
// Contributions and fused scores are rewritten so the explain block stays
// truthful about what was summed.
func minMaxFuse(cands map[int64]*candidate, p Profile) {
	type span struct{ lo, hi float64 }
	spans := map[string]*span{}
	rawOf := func(h *ArmHit) float64 {
		if h.Raw == nil {
			return 0
		}
		if h.RawKind == "bm25" {
			return -*h.Raw
		}
		return *h.Raw
	}
	for _, c := range cands {
		for arm, h := range c.arms {
			v := rawOf(h)
			sp := spans[arm]
			if sp == nil {
				spans[arm] = &span{v, v}
				continue
			}
			if v < sp.lo {
				sp.lo = v
			}
			if v > sp.hi {
				sp.hi = v
			}
		}
	}
	for _, c := range cands {
		c.fused = 0
		for arm, h := range c.arms {
			sp := spans[arm]
			scaled := 1.0
			if sp.hi > sp.lo {
				scaled = (rawOf(h) - sp.lo) / (sp.hi - sp.lo)
			}
			h.Contribution = p.Weights[arm] * scaled
			c.fused += h.Contribution
		}
	}
}
