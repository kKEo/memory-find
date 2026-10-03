package retrieve

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kKEo/memory-find/internal/graph"
	"github.com/kKEo/memory-find/internal/kb"
)

// The entity arm and the graph arm (roadmap P7). Both use the mention graph
// as an index over the text: the entity arm reads one hop (query names an
// entity → the chunks that mention it), the graph arm walks further with
// personalised PageRank. Neither replaces the text arms; both vote in RRF.

// relationalRe spots questions about how things relate, which is where a
// walk over shared mentions can find what a single lookup cannot.
var relationalRe = regexp.MustCompile(`(?i)\b(between|relate[sd]?|relation|depend[s]?|connect(ed|s|ion)? (to|with)|differ|compare|changed? (between|from|since)|uses?|calls?|interact|how .+ (and|with) )`)

// GraphWhy explains the graph arm's view of a result.
type GraphWhy struct {
	Seeds        []string `json:"seeds"`         // entity canonical names the walk started from
	PPRScore     float64  `json:"ppr_score"`     // product over seeds of the mass each walk leaves on the chunk
	Hops         int      `json:"hops"`          // 1 if a seed mentions the chunk directly, else 2+
	HubPenalised bool     `json:"hub_penalised"` // a seed had degree above the cap
}

// nsGraph is the cached in-memory graph of one namespace.
type nsGraph struct {
	csr      *graph.CSR
	entity   map[string]int32 // entity id → node
	chunk    map[int64]int32  // chunk id → node
	nodeKind []byte           // 'e' or 'c'
	chunkOf  []int64          // node → chunk id (0 for entities)
	edges    int
	builtAt  time.Time
}

type graphCache struct {
	mu sync.Mutex
	m  map[string]*nsGraph
}

const (
	pprAlpha  = 0.85
	pprRounds = 10 // S6: identical top-20 to 20 rounds at 100k nodes
	pprHubCap = 50 // an entity mentioned in more chunks passes on less
)

// matchEntities finds the entities a query names within the scoped
// namespaces (all when the scope names none).
func (s *Service) matchEntities(ctx context.Context, q string, sc Scope) ([]kb.Entity, error) {
	return s.store.FindEntities(ctx, sc.Namespaces, q)
}

// entityArm: chunks mentioning the entities the query names, ranked by the
// mention weight scaled by how selective the entity is (an entity mentioned
// everywhere says little), inside the scope.
func (s *Service) entityArm(ctx context.Context, ents []kb.Entity, where string, args []any, depth int) ([]armRow, error) {
	if len(ents) == 0 {
		return nil, nil
	}
	ids := make([]any, 0, len(ents))
	ph := make([]string, 0, len(ents))
	sel := map[string]float64{}
	name := map[string]string{}
	for _, e := range ents {
		ids = append(ids, e.ID)
		ph = append(ph, "?")
		sel[e.ID] = 1 / math.Log2(2+float64(e.Mentions))
		name[e.ID] = e.Canonical
	}
	q := fmt.Sprintf(`SELECT c.id, c.document_id, m.entity_id, m.weight FROM mentions m JOIN chunks c ON c.id = m.chunk_id
		JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id
		WHERE m.entity_id IN (%s) AND %s`, strings.Join(ph, ","), where)
	rows, err := s.db.QueryContext(ctx, q, append(ids, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		row   armRow
		score float64
	}
	by := map[int64]*acc{}
	for rows.Next() {
		var cid int64
		var did, eid string
		var w float64
		if err := rows.Scan(&cid, &did, &eid, &w); err != nil {
			return nil, err
		}
		a := by[cid]
		if a == nil {
			a = &acc{row: armRow{chunkID: cid, docID: did, rawKind: "mention"}}
			by[cid] = a
		}
		a.score += w * sel[eid]
		a.row.terms = mergeTerms(a.row.terms, []string{name[eid]})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	list := make([]*acc, 0, len(by))
	for _, a := range by {
		a.row.raw = a.score
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].row.chunkID < list[j].row.chunkID
	})
	if len(list) > depth {
		list = list[:depth]
	}
	out := make([]armRow, len(list))
	for i, a := range list {
		out[i] = a.row
	}
	return out, nil
}

// graphFor returns the cached namespace graph, rebuilding it when the
// mention count changed. as_of queries build a throwaway graph.
func (s *Service) graphFor(ctx context.Context, ns string, asOf *time.Time) (*nsGraph, error) {
	if asOf == nil {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mentions m JOIN chunks c ON c.id = m.chunk_id JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id WHERE s.namespace = ? AND d.deleted_at IS NULL AND d.superseded_by IS NULL`, ns).Scan(&n); err != nil {
			return nil, err
		}
		s.graphs.mu.Lock()
		g := s.graphs.m[ns]
		s.graphs.mu.Unlock()
		if g != nil && g.edges == n {
			return g, nil
		}
	}
	edges, err := s.store.MentionGraph(ctx, ns, asOf)
	if err != nil {
		return nil, err
	}
	g := &nsGraph{entity: map[string]int32{}, chunk: map[int64]int32{}, edges: len(edges), builtAt: s.now()}
	var es []graph.Edge
	node := func(kind byte, chunkID int64, entityID string) int32 {
		if kind == 'e' {
			if id, ok := g.entity[entityID]; ok {
				return id
			}
		} else if id, ok := g.chunk[chunkID]; ok {
			return id
		}
		id := int32(len(g.nodeKind))
		g.nodeKind = append(g.nodeKind, kind)
		g.chunkOf = append(g.chunkOf, chunkID)
		if kind == 'e' {
			g.entity[entityID] = id
		} else {
			g.chunk[chunkID] = id
		}
		return id
	}
	for _, e := range edges {
		es = append(es, graph.Edge{A: node('e', 0, e.EntityID), B: node('c', e.ChunkID, ""), W: float32(e.Weight)})
	}
	g.csr = graph.BuildCSR(len(g.nodeKind), es)
	if asOf == nil {
		s.graphs.mu.Lock()
		s.graphs.m[ns] = g
		s.graphs.mu.Unlock()
	}
	return g, nil
}

// graphArm runs a personalised PageRank from each matched entity in each
// scoped namespace and returns the chunks the walks agree on, filtered by
// the scope: the multi-hop finds a one-hop lookup cannot see.
func (s *Service) graphArm(ctx context.Context, ents []kb.Entity, sc Scope, asOf *time.Time, where string, args []any, depth int) ([]armRow, map[int64]*GraphWhy, error) {
	if len(ents) == 0 {
		return nil, nil, nil
	}
	byNS := map[string][]kb.Entity{}
	for _, e := range ents {
		byNS[e.Namespace] = append(byNS[e.Namespace], e)
	}
	type hit struct {
		chunkID int64
		score   float64
		hops    int
		why     *GraphWhy
	}
	var hits []hit
	for ns, seeds := range byNS {
		g, err := s.graphFor(ctx, ns, asOf)
		if err != nil {
			return nil, nil, err
		}
		var seedNodes []int32
		var seedNames []string
		hub := false
		for _, e := range seeds {
			n, ok := g.entity[e.ID]
			if !ok {
				continue
			}
			seedNodes = append(seedNodes, n)
			seedNames = append(seedNames, e.Canonical)
			if g.csr.Deg[n] > pprHubCap {
				hub = true
			}
		}
		if len(seedNodes) == 0 {
			continue
		}
		// One walk per seed; a chunk's score is the product of the mass each
		// seed leaves on it (smoothed), so a passage reachable from every
		// entity in the question (a bridge) beats one reachable from one.
		// Chunks every seed mentions directly are the entity arm's job and
		// are left out here: the graph arm reports what one hop cannot see.
		eps := 1e-6 / float64(g.csr.N+1)
		score := make([]float64, g.csr.N)
		for i := range score {
			score[i] = 1
		}
		directAll := make([]int, g.csr.N)
		for _, sn := range seedNodes {
			rank := g.csr.PPR(map[int32]float32{sn: 1}, pprAlpha, pprRounds, pprHubCap)
			for n := range score {
				score[n] *= float64(rank[n]) + eps
			}
			for _, t := range g.csr.Targets[g.csr.Offsets[sn]:g.csr.Offsets[sn+1]] {
				directAll[t]++
			}
		}
		floor := math.Pow(eps, float64(len(seedNodes)))
		for n := range score {
			if g.nodeKind[n] != 'c' || score[n] <= floor*1.0001 || directAll[n] == len(seedNodes) {
				continue
			}
			hops := 2
			if directAll[n] > 0 {
				hops = 1
			}
			hits = append(hits, hit{chunkID: g.chunkOf[n], score: score[n], hops: hops, why: &GraphWhy{Seeds: seedNames, PPRScore: score[n], Hops: hops, HubPenalised: hub}})
		}
	}
	if len(hits) == 0 {
		return nil, nil, nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].chunkID < hits[j].chunkID
	})
	if len(hits) > depth*2 {
		hits = hits[:depth*2]
	}
	// Scope filter in SQL on the candidate ids (the graph ignores scope).
	ids := make([]any, 0, len(hits))
	ph := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.chunkID)
		ph = append(ph, "?")
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT c.id, c.document_id FROM chunks c JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id WHERE c.id IN (%s) AND %s`, strings.Join(ph, ","), where), append(ids, args...)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	docOf := map[int64]string{}
	for rows.Next() {
		var cid int64
		var did string
		if err := rows.Scan(&cid, &did); err != nil {
			return nil, nil, err
		}
		docOf[cid] = did
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var out []armRow
	why := map[int64]*GraphWhy{}
	for _, h := range hits {
		did, ok := docOf[h.chunkID]
		if !ok {
			continue
		}
		out = append(out, armRow{chunkID: h.chunkID, docID: did, raw: h.score, rawKind: "ppr", terms: h.why.Seeds})
		why[h.chunkID] = h.why
		if len(out) >= depth {
			break
		}
	}
	return out, why, nil
}

// wantsGraph is the auto-routing rule: two or more known entities, or
// relational phrasing with at least one.
func wantsGraph(q string, ents []kb.Entity) (bool, string) {
	if len(ents) >= 2 {
		return true, fmt.Sprintf("entity and graph arms added: %d known entities", len(ents))
	}
	if len(ents) == 1 && relationalRe.MatchString(q) {
		return true, "entity and graph arms added: relational phrasing around a known entity"
	}
	return false, ""
}
