package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/kKEo/memory-find/internal/chunk"
	"github.com/kKEo/memory-find/internal/graph"
)

// Layer L4: entities, mentions, merge candidates, edges (roadmap P7). The
// graph is an index over the text: every entity row can be rebuilt from the
// chunks, nothing here is the only copy of anything.

// Entity is one recognised thing.
type Entity struct {
	ID        string   `json:"id"`
	URI       string   `json:"uri"`
	Namespace string   `json:"namespace"`
	Canonical string   `json:"canonical"`
	Type      string   `json:"type"`
	Aliases   []string `json:"aliases,omitempty"`
	Mentions  int      `json:"mentions"`
}

// resolveIndex loads the resolution index for a namespace: every entity key
// and alias key.
func (s *Store) resolveIndex(ctx context.Context, q queryer, ns string) (*graph.Index, map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT key, id FROM entities WHERE namespace = ? UNION ALL SELECT a.key, a.entity_id FROM entity_aliases a JOIN entities e ON e.id = a.entity_id WHERE e.namespace = ?`, ns, ns)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	byKey := map[string]string{}
	var keys []string
	for rows.Next() {
		var k, id string
		if err := rows.Scan(&k, &id); err != nil {
			return nil, nil, err
		}
		byKey[k] = id
		keys = append(keys, k)
	}
	return graph.NewIndex(keys), byKey, rows.Err()
}

type queryer interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

// ensureEntity resolves a name inside a transaction: an exact key match
// returns the existing id; a near match creates a new entity AND a merge
// candidate (never an automatic merge); otherwise a new entity.
func (s *Store) ensureEntity(ctx context.Context, tx queryer, ix *graph.Index, byKey map[string]string, ns, name, typ string, nowMs int64) (string, error) {
	d := ix.Resolve(name)
	key := graph.Key(name)
	if d.Action == "same" {
		return byKey[d.MatchKey], nil
	}
	id := uuid.Must(uuid.NewV7()).String()
	if typ == "" {
		typ = "name"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO entities(id, namespace, canonical, key, type, created_at) VALUES (?,?,?,?,?,?)`, id, ns, graph.Normalize(name), key, typ, nowMs); err != nil {
		return "", fmt.Errorf("insert entity %q: %w", name, err)
	}
	ix.Add(key)
	byKey[key] = id
	if d.Action == "candidate" {
		other := byKey[d.MatchKey]
		a, b := other, id
		if a > b {
			a, b = b, a
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO merge_candidates(a, b, score, reason, state, created_at) VALUES (?,?,?,?,'open',?)`, a, b, d.Score, d.Reason, nowMs); err != nil {
			return "", err
		}
	}
	return id, nil
}

// linkMentions writes the mention edges for a document's new chunks: rung-1
// heuristics per chunk plus the client's rung-2 entities (linked to every
// chunk of the document at a low weight, and to chunks whose text contains
// them at full weight) and relations (typed edges with the first chunk as
// evidence). Returns the number of distinct entities linked.
func (s *Store) linkMentions(ctx context.Context, tx *sql.Tx, ns, title string, ids []int64, chunks []chunk.Chunk, extra []EntityInput, rels []RelationInput, nowMs int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ix, byKey, err := s.resolveIndex(ctx, tx, ns)
	if err != nil {
		return 0, err
	}
	linked := map[string]bool{}
	add := func(entityID string, chunkID int64, w float64) error {
		linked[entityID] = true
		_, err := tx.ExecContext(ctx, `INSERT INTO mentions(entity_id, chunk_id, weight) VALUES (?,?,?) ON CONFLICT(entity_id, chunk_id) DO UPDATE SET weight = MAX(weight, excluded.weight)`, entityID, chunkID, w)
		return err
	}
	for i, c := range chunks {
		for _, m := range graph.Extract(title, c.Text) {
			id, err := s.ensureEntity(ctx, tx, ix, byKey, ns, m.Name, m.Type, nowMs)
			if err != nil {
				return 0, err
			}
			if err := add(id, ids[i], m.Weight); err != nil {
				return 0, err
			}
		}
	}
	named := map[string]string{} // rung-2 name → entity id
	for _, e := range extra {
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		id, err := s.ensureEntity(ctx, tx, ix, byKey, ns, e.Name, e.Type, nowMs)
		if err != nil {
			return 0, err
		}
		named[e.Name] = id
		lower := strings.ToLower(e.Name)
		for i, c := range chunks {
			w := 0.3
			if strings.Contains(strings.ToLower(c.Text), lower) {
				w = 1.0
			}
			if err := add(id, ids[i], w); err != nil {
				return 0, err
			}
		}
	}
	for _, r := range rels {
		if r.From == "" || r.To == "" || r.Rel == "" {
			continue
		}
		var src, dst string
		var err error
		if src = named[r.From]; src == "" {
			if src, err = s.ensureEntity(ctx, tx, ix, byKey, ns, r.From, "", nowMs); err != nil {
				return 0, err
			}
		}
		if dst = named[r.To]; dst == "" {
			if dst, err = s.ensureEntity(ctx, tx, ix, byKey, ns, r.To, "", nowMs); err != nil {
				return 0, err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO edges(src, dst, rel, weight, recorded_at, evidence_chunk_id) VALUES (?,?,?,1.0,?,?)`, src, dst, strings.ToLower(strings.TrimSpace(r.Rel)), nowMs, ids[0]); err != nil {
			return 0, err
		}
		linked[src], linked[dst] = true, true
	}
	return len(linked), nil
}

// linkFactSubject ties a fact to the entities its `about` names and records
// the first as the subject.
func (s *Store) linkFactSubject(ctx context.Context, tx *sql.Tx, ns, factID string, about []string, nowMs int64) error {
	if len(about) == 0 {
		return nil
	}
	ix, byKey, err := s.resolveIndex(ctx, tx, ns)
	if err != nil {
		return err
	}
	subject := ""
	for _, name := range about {
		if strings.TrimSpace(name) == "" {
			continue
		}
		id, err := s.ensureEntity(ctx, tx, ix, byKey, ns, name, "", nowMs)
		if err != nil {
			return err
		}
		if subject == "" {
			subject = id
		}
	}
	if subject == "" {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE facts SET subject_entity_id = ? WHERE id = ?`, subject, factID)
	return err
}

// FindEntities returns the entities whose canonical name or alias appears in
// the query text (longest names first, each matched once), for the entity
// and graph arms. Matching is on the normalised key over the query's words
// and word pairs/triples, plus whole-identifier tokens.
func (s *Store) FindEntities(ctx context.Context, ns []string, query string) ([]Entity, error) {
	keys := candidateKeys(query)
	if len(keys) == 0 {
		return nil, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys)+len(ns))
	for _, k := range keys {
		args = append(args, k)
	}
	where := ""
	if len(ns) > 0 {
		where = ` AND e.namespace IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ns)), ",") + `)`
		for _, n := range ns {
			args = append(args, n)
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT e.id, e.namespace, e.canonical, e.type, (SELECT COUNT(*) FROM mentions m WHERE m.entity_id = e.id)
		FROM entities e LEFT JOIN entity_aliases a ON a.entity_id = e.id
		WHERE (e.key IN (`+ph+`) OR a.key IN (`+ph+`))`+where, append(append([]any{}, args[:len(keys)]...), args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entity
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.Namespace, &e.Canonical, &e.Type, &e.Mentions); err != nil {
			return nil, err
		}
		e.URI = "memo://entity/" + e.ID
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i].Canonical) > len(out[j].Canonical) })
	return out, rows.Err()
}

// candidateKeys lists the keys a query could name: every identifier-like
// token and every run of one to three words.
func candidateKeys(q string) []string {
	words := strings.Fields(q)
	seen := map[string]bool{}
	var keys []string
	add := func(s string) {
		k := graph.Key(s)
		if len(k) >= 2 && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for i := range words {
		for n := 1; n <= 3 && i+n <= len(words); n++ {
			add(strings.Join(words[i:i+n], " "))
		}
	}
	return keys
}

// MentionEdge is one entity↔chunk link, for building the in-memory graph.
type MentionEdge struct {
	EntityID string
	ChunkID  int64
	Weight   float64
}

// MentionGraph returns every live mention edge in a namespace (chunks of
// forgotten or superseded documents excluded, or as of a date).
func (s *Store) MentionGraph(ctx context.Context, ns string, asOf *time.Time) ([]MentionEdge, error) {
	live := `d.deleted_at IS NULL AND d.superseded_by IS NULL`
	args := []any{ns}
	if asOf != nil {
		live = `d.deleted_at IS NULL AND d.created_at <= ? AND (d.superseded_by IS NULL OR d.updated_at > ?)`
		args = append(args, asOf.UnixMilli(), asOf.UnixMilli())
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.entity_id, m.chunk_id, m.weight FROM mentions m JOIN chunks c ON c.id = m.chunk_id JOIN documents d ON d.id = c.document_id JOIN sources s ON s.id = d.source_id WHERE s.namespace = ? AND `+live, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MentionEdge
	for rows.Next() {
		var e MentionEdge
		if err := rows.Scan(&e.EntityID, &e.ChunkID, &e.Weight); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Neighbour is one hop of an exploration.
type Neighbour struct {
	Entity   Entity   `json:"entity"`
	Shared   int      `json:"shared_chunks"` // chunks mentioning both
	Rel      string   `json:"rel,omitempty"` // typed edge, when one exists
	Evidence []string `json:"evidence"`      // chunk addresses, strongest first (max 3)
	Hop      int      `json:"hop"`
}

// Exploration is what explore returns.
type Exploration struct {
	Entity     Entity      `json:"entity"`
	Chunks     []string    `json:"chunks"` // addresses mentioning the entity, by weight
	Neighbours []Neighbour `json:"neighbours"`
	AsOf       *time.Time  `json:"as_of,omitempty"`
}

// ReadEntity loads one entity by id or by name within a namespace.
func (s *Store) ReadEntity(ctx context.Context, idOrName, ns string) (*Entity, error) {
	var e Entity
	err := s.db.QueryRowContext(ctx, `SELECT id, namespace, canonical, type, (SELECT COUNT(*) FROM mentions m WHERE m.entity_id = entities.id) FROM entities WHERE id = ?`, idOrName).Scan(&e.ID, &e.Namespace, &e.Canonical, &e.Type, &e.Mentions)
	if errors.Is(err, sql.ErrNoRows) {
		key := graph.Key(idOrName)
		q := `SELECT e.id, e.namespace, e.canonical, e.type, (SELECT COUNT(*) FROM mentions m WHERE m.entity_id = e.id) FROM entities e LEFT JOIN entity_aliases a ON a.entity_id = e.id WHERE (e.key = ? OR a.key = ?)`
		args := []any{key, key}
		if ns != "" {
			q += ` AND e.namespace = ?`
			args = append(args, ns)
		}
		err = s.db.QueryRowContext(ctx, q+` ORDER BY e.created_at LIMIT 1`, args...).Scan(&e.ID, &e.Namespace, &e.Canonical, &e.Type, &e.Mentions)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("entity %q: %w", idOrName, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	e.URI = "memo://entity/" + e.ID
	rows, err := s.db.QueryContext(ctx, `SELECT alias FROM entity_aliases WHERE entity_id = ? ORDER BY alias`, e.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		e.Aliases = append(e.Aliases, a)
	}
	return &e, rows.Err()
}

// Explore returns an entity's chunks and the entities that share chunks with
// it (one hop), optionally their neighbours (two hops), with evidence
// addresses per neighbour. Fixed-depth joins, no recursion (S6).
func (s *Store) Explore(ctx context.Context, idOrName, ns string, hops int, asOf *time.Time) (*Exploration, error) {
	e, err := s.ReadEntity(ctx, idOrName, ns)
	if err != nil {
		return nil, err
	}
	if hops < 1 {
		hops = 1
	}
	if hops > 2 {
		hops = 2
	}
	live := `d.deleted_at IS NULL AND d.superseded_by IS NULL`
	var liveArgs []any
	if asOf != nil {
		live = `d.deleted_at IS NULL AND d.created_at <= ? AND (d.superseded_by IS NULL OR d.updated_at > ?)`
		liveArgs = []any{asOf.UnixMilli(), asOf.UnixMilli()}
	}
	ex := &Exploration{Entity: *e, AsOf: asOf}
	chunks, err := s.entityChunks(ctx, e.ID, live, liveArgs)
	if err != nil {
		return nil, err
	}
	ex.Chunks = chunks
	frontier := []string{e.ID}
	seen := map[string]bool{e.ID: true}
	for hop := 1; hop <= hops; hop++ {
		var next []string
		for _, from := range frontier {
			ns, err := s.neighbours(ctx, from, live, liveArgs, hop)
			if err != nil {
				return nil, err
			}
			for _, n := range ns {
				if seen[n.Entity.ID] {
					continue
				}
				seen[n.Entity.ID] = true
				ex.Neighbours = append(ex.Neighbours, n)
				next = append(next, n.Entity.ID)
			}
		}
		frontier = next
		if len(ex.Neighbours) > 60 {
			break
		}
	}
	return ex, nil
}

func (s *Store) entityChunks(ctx context.Context, entityID, live string, liveArgs []any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.chunk_id FROM mentions m JOIN chunks c ON c.id = m.chunk_id JOIN documents d ON d.id = c.document_id WHERE m.entity_id = ? AND `+live+` ORDER BY m.weight DESC, m.chunk_id LIMIT 20`, append([]any{entityID}, liveArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("memo://chunk/%d", id))
	}
	return out, rows.Err()
}

func (s *Store) neighbours(ctx context.Context, entityID, live string, liveArgs []any, hop int) ([]Neighbour, error) {
	args := append([]any{entityID}, liveArgs...)
	args = append(args, entityID)
	rows, err := s.db.QueryContext(ctx, `SELECT e.id, e.namespace, e.canonical, e.type, COUNT(*) AS shared, GROUP_CONCAT(m2.chunk_id)
		FROM mentions m1 JOIN mentions m2 ON m2.chunk_id = m1.chunk_id AND m2.entity_id != m1.entity_id
		JOIN chunks c ON c.id = m1.chunk_id JOIN documents d ON d.id = c.document_id
		JOIN entities e ON e.id = m2.entity_id
		WHERE m1.entity_id = ? AND `+live+`
		GROUP BY e.id HAVING e.id != ? ORDER BY shared DESC, e.canonical LIMIT 15`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Neighbour
	for rows.Next() {
		var n Neighbour
		var chunkList string
		if err := rows.Scan(&n.Entity.ID, &n.Entity.Namespace, &n.Entity.Canonical, &n.Entity.Type, &n.Shared, &chunkList); err != nil {
			return nil, err
		}
		n.Entity.URI = "memo://entity/" + n.Entity.ID
		n.Hop = hop
		for i, id := range strings.Split(chunkList, ",") {
			if i >= 3 {
				break
			}
			n.Evidence = append(n.Evidence, "memo://chunk/"+id)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		var rel sql.NullString
		_ = s.db.QueryRowContext(ctx, `SELECT rel FROM edges WHERE ((src = ? AND dst = ?) OR (src = ? AND dst = ?)) AND invalidated_at IS NULL ORDER BY recorded_at DESC LIMIT 1`, entityID, out[i].Entity.ID, out[i].Entity.ID, entityID).Scan(&rel)
		out[i].Rel = rel.String
	}
	return out, nil
}

// MergeCandidate is one row of the resolver's review queue.
type MergeCandidate struct {
	ID     int64   `json:"id"`
	A      Entity  `json:"a"`
	B      Entity  `json:"b"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
	State  string  `json:"state"`
}

// MergeCandidates lists the queue (open first).
func (s *Store) MergeCandidates(ctx context.Context, state string) ([]MergeCandidate, error) {
	q := `SELECT mc.id, mc.score, mc.reason, mc.state, a.id, a.namespace, a.canonical, a.type, b.id, b.namespace, b.canonical, b.type FROM merge_candidates mc JOIN entities a ON a.id = mc.a JOIN entities b ON b.id = mc.b`
	var args []any
	if state != "" {
		q += ` WHERE mc.state = ?`
		args = append(args, state)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY mc.state = 'open' DESC, mc.score DESC, mc.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MergeCandidate
	for rows.Next() {
		var m MergeCandidate
		if err := rows.Scan(&m.ID, &m.Score, &m.Reason, &m.State, &m.A.ID, &m.A.Namespace, &m.A.Canonical, &m.A.Type, &m.B.ID, &m.B.Namespace, &m.B.Canonical, &m.B.Type); err != nil {
			return nil, err
		}
		m.A.URI, m.B.URI = "memo://entity/"+m.A.ID, "memo://entity/"+m.B.ID
		out = append(out, m)
	}
	return out, rows.Err()
}

// DecideMerge accepts or rejects a candidate. Accepting keeps A as the
// canonical entity, turns B's canonical name into an alias of A, moves B's
// mentions, edges and fact subjects to A, and deletes B. Audited.
func (s *Store) DecideMerge(ctx context.Context, id int64, accept bool, actor, channel string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var a, b, state string
	if err := tx.QueryRowContext(ctx, `SELECT a, b, state FROM merge_candidates WHERE id = ?`, id).Scan(&a, &b, &state); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("merge candidate %d: %w", id, ErrNotFound)
	} else if err != nil {
		return err
	}
	if state != "open" {
		return fmt.Errorf("merge candidate %d is already %s", id, state)
	}
	nowMs := s.now().UnixMilli()
	newState := "rejected"
	if accept {
		newState = "merged"
		var canonical, key string
		if err := tx.QueryRowContext(ctx, `SELECT canonical, key FROM entities WHERE id = ?`, b).Scan(&canonical, &key); err != nil {
			return err
		}
		stmts := []struct {
			q    string
			args []any
		}{
			{`INSERT OR IGNORE INTO entity_aliases(alias, key, entity_id) VALUES (?,?,?)`, []any{canonical, key, a}},
			{`UPDATE OR IGNORE entity_aliases SET entity_id = ? WHERE entity_id = ?`, []any{a, b}},
			{`INSERT OR IGNORE INTO mentions(entity_id, chunk_id, weight) SELECT ?, chunk_id, weight FROM mentions WHERE entity_id = ?`, []any{a, b}},
			{`UPDATE OR IGNORE edges SET src = ? WHERE src = ?`, []any{a, b}},
			{`UPDATE OR IGNORE edges SET dst = ? WHERE dst = ?`, []any{a, b}},
			{`UPDATE facts SET subject_entity_id = ? WHERE subject_entity_id = ?`, []any{a, b}},
			{`DELETE FROM entities WHERE id = ?`, []any{b}},
		}
		for _, st := range stmts {
			if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
				return fmt.Errorf("merge: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE merge_candidates SET state = ?, decided_at = ? WHERE id = ?`, newState, nowMs, id); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, nowMs, actor, channel, "merge_"+newState, "memo://entity/"+a, map[string]any{"other": "memo://entity/" + b, "candidate": id}); err != nil {
		return err
	}
	return tx.Commit()
}

// GraphStats counts the layer for status.
type GraphStats struct {
	Entities        int `json:"entities"`
	Mentions        int `json:"mentions"`
	Edges           int `json:"edges"`
	OpenMergeReview int `json:"open_merge_candidates"`
}

// GraphStats returns the counts.
func (s *Store) GraphStats(ctx context.Context) (GraphStats, error) {
	var g GraphStats
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM entities), (SELECT COUNT(*) FROM mentions), (SELECT COUNT(*) FROM edges WHERE invalidated_at IS NULL), (SELECT COUNT(*) FROM merge_candidates WHERE state = 'open')`).Scan(&g.Entities, &g.Mentions, &g.Edges, &g.OpenMergeReview)
	return g, err
}
