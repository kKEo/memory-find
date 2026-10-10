package retrieve

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/chunk"
	"github.com/kKEo/memors/internal/kb"
)

// searchPages is granularity=page: keyword (pages_fts) and semantic
// (page_vecs) ranks fused with the profile's weights. Pages are derived
// records, so every result says is_inference and whether it is stale.
func (s *Service) searchPages(ctx context.Context, req Request, queries []string, limit int, tr *Trace, start time.Time) (*Response, error) {
	p := s.profile
	tr.ModeResolved = "page"
	tr.RoutingReason = "granularity=page: keyword and semantic match over curated pages"
	tr.ArmsRun = []string{ArmKeyword, ArmSemantic}
	tr.Filtered.ByScope = describeScope(req.Scope)
	type agg struct {
		id    string
		fused float64
		arms  []ArmHit
		cos   float64
		has   bool
	}
	aggs := map[string]*agg{}
	get := func(id string) *agg {
		a := aggs[id]
		if a == nil {
			a = &agg{id: id}
			aggs[id] = a
		}
		return a
	}
	nsWhere, nsArgs := "", []any{}
	if len(req.Scope.Namespaces) > 0 {
		nsWhere = ` AND p.namespace IN (` + strings.TrimSuffix(strings.Repeat("?,", len(req.Scope.Namespaces)), ",") + `)`
		for _, n := range req.Scope.Namespaces {
			nsArgs = append(nsArgs, n)
		}
	}
	nq := float64(len(queries))
	for _, q := range queries {
		if match := ftsQueryStemmed(q); match != "" {
			hits, err := s.pageKeywordHits(ctx, match, nsWhere, nsArgs, p.FetchDepth)
			if err != nil {
				return nil, fmt.Errorf("page search: %w", err)
			}
			for rank, h := range hits {
				c := (p.Weights[ArmKeyword] / nq) / float64(p.RRFK+rank+1)
				a := get(h.id)
				a.fused += c
				r, raw := rank+1, h.raw
				a.arms = append(a.arms, ArmHit{Arm: ArmKeyword, Rank: &r, Raw: &raw, RawKind: "bm25", Contribution: c, MatchedTerms: h.terms})
			}
			tr.CandidatesPerArm[ArmKeyword] += len(hits)
		}
		if s.embedder != nil {
			if qv, err := s.embedder.Embed(ctx, q); err == nil {
				hits, err := s.pageSemanticHits(ctx, kb.EncodeVector(qv), nsWhere, nsArgs, p.FetchDepth)
				if err != nil {
					return nil, fmt.Errorf("page vectors: %w", err)
				}
				for rank, h := range hits {
					cos := h.raw
					a := get(h.id)
					if cos < p.SemanticFloor && len(a.arms) == 0 {
						tr.Filtered.BySemanticFloor++
						delete(aggs, h.id)
						continue
					}
					c := (p.Weights[ArmSemantic] / nq) / float64(p.RRFK+rank+1)
					a.fused += c
					a.cos, a.has = cos, true
					r, raw := rank+1, cos
					a.arms = append(a.arms, ArmHit{Arm: ArmSemantic, Rank: &r, Raw: &raw, RawKind: "cosine", Contribution: c})
				}
				tr.CandidatesPerArm[ArmSemantic] += len(hits)
			}
		}
	}
	if len(aggs) == 0 {
		return s.abstain(ctx, req, queries, tr, start, "no page matched", "pages are written by agents through compact/submit; try granularity=chunk")
	}
	list := make([]*agg, 0, len(aggs))
	for _, a := range aggs {
		list = append(list, a)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].fused != list[j].fused {
			return list[i].fused > list[j].fused
		}
		return list[i].id < list[j].id
	})
	tr.Cutoff = Cutoff{Kind: "none", Position: len(list)}
	if len(list) > limit {
		list = list[:limit]
		tr.Cutoff = Cutoff{Kind: "limit", Position: limit}
	}
	resp := &Response{}
	tr.Budget.MaxTokens = req.MaxTokens
	used := 0
	for i, a := range list {
		page, err := s.store.ReadPage(ctx, a.id)
		if err != nil {
			continue
		}
		content := page.Content
		if req.ResponseFormat == FormatConcise {
			content = oneLiner(page.Content)
		}
		cost := chunk.EstimateTokens(content) + 24
		if used+cost > req.MaxTokens && len(resp.Results) > 0 {
			tr.Budget.TruncatedCount = len(list) - i
			tr.Cutoff = Cutoff{Kind: "budget", Position: i}
			break
		}
		used += cost
		var relevance *float64
		band := "keyword-only"
		if a.has {
			v := a.cos
			relevance = &v
			band = s.band(v)
		}
		prov := ProvRef{Kind: "page", Trust: page.Trust, Origin: kb.OriginAgentDerived, Namespace: page.Namespace, FetchedAt: page.BuiltAt, IsInference: true, Stale: page.Stale, StaleReason: page.StaleReason}
		r := Result{Rank: len(resp.Results) + 1, URI: page.URI, Title: page.Title, Content: content, Score: a.fused, Relevance: relevance, Band: band, Provenance: prov}
		if len(page.Sources) > 0 {
			r.ChunkURI = page.Sources[0]
		}
		if req.ResponseFormat == FormatExplain {
			r.Why = &Why{URI: page.URI, Document: DocRef{URI: page.URI, Title: page.Title, Revision: page.BuiltFromRev}, Arms: a.arms, Fused: a.fused, RecencyFactor: 1, Final: a.fused, Rank: r.Rank, Relevance: relevance, Band: band, Provenance: prov, Freshness: Freshness{Stale: page.Stale}}
		}
		resp.Results = append(resp.Results, r)
	}
	tr.Budget.Used = used
	resp.Truncated, resp.NarrowHint = tr.Budget.TruncatedCount, "scope.namespaces"
	if resp.Truncated == 0 {
		resp.NarrowHint = ""
	}
	if req.ResponseFormat == FormatExplain {
		resp.Trace = tr
	}
	s.logQuery(ctx, req, queries, tr, resp, start)
	return resp, nil
}

type pageHit struct {
	id    string
	raw   float64
	terms []string
}

func (s *Service) pageKeywordHits(ctx context.Context, match, nsWhere string, nsArgs []any, depth int) ([]pageHit, error) {
	args := append([]any{match}, nsArgs...)
	args = append(args, depth)
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, bm25(pages_fts), highlight(pages_fts, 1, char(1), char(2)) FROM pages_fts JOIN pages p ON p.rowid = pages_fts.rowid WHERE pages_fts MATCH ? AND p.deleted_at IS NULL`+nsWhere+` ORDER BY bm25(pages_fts) LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pageHit
	for rows.Next() {
		var h pageHit
		var hl string
		if err := rows.Scan(&h.id, &h.raw, &hl); err != nil {
			return nil, err
		}
		h.terms = extractMarked(hl)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Service) pageSemanticHits(ctx context.Context, qv []byte, nsWhere string, nsArgs []any, depth int) ([]pageHit, error) {
	args := append([]any{qv, s.embedder.Info().ID}, nsArgs...)
	args = append(args, depth)
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, vec_distance_cosine(v.embedding, ?) AS dist FROM page_vecs v JOIN pages p ON p.id = v.page_id WHERE v.model_id = ? AND p.deleted_at IS NULL`+nsWhere+` ORDER BY dist LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pageHit
	for rows.Next() {
		var h pageHit
		var dist float64
		if err := rows.Scan(&h.id, &dist); err != nil {
			return nil, err
		}
		h.raw = 1 - dist
		out = append(out, h)
	}
	return out, rows.Err()
}
