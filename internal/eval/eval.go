// Package eval is the retrieval benchmark: a fixture corpus, labelled
// queries, and the standard information-retrieval metrics that turn "search
// feels better" into numbers a pull request can show. It runs the real
// store and the real retrieval service with the deterministic hash
// embedder, so it needs no model and runs in CI.
package eval

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/chunk"
	"github.com/kKEo/memors/internal/kb"
	"github.com/kKEo/memors/internal/retrieve"
)

// ThoughtInput is the fixture authoring shape inherited from the journal
// era: one text per section. Markdown() turns it into a document whose
// sections become headings (and whose section names become tags, so a
// section-scoped query is a tag-scoped query).
type ThoughtInput struct {
	Reflections       string
	Observations      string
	ProjectNotes      string
	UserContext       string
	TechnicalInsights string
	WorldKnowledge    string
}

var sectionOrder = []struct {
	name string
	get  func(ThoughtInput) string
}{
	{"reflections", func(t ThoughtInput) string { return t.Reflections }},
	{"observations", func(t ThoughtInput) string { return t.Observations }},
	{"project_notes", func(t ThoughtInput) string { return t.ProjectNotes }},
	{"user_context", func(t ThoughtInput) string { return t.UserContext }},
	{"technical_insights", func(t ThoughtInput) string { return t.TechnicalInsights }},
	{"world_knowledge", func(t ThoughtInput) string { return t.WorldKnowledge }},
}

// Markdown renders the sections as a document; Sections lists their names.
func (t ThoughtInput) Markdown() (string, []string) {
	var sb strings.Builder
	var names []string
	for _, s := range sectionOrder {
		if text := strings.TrimSpace(s.get(t)); text != "" {
			fmt.Fprintf(&sb, "## %s\n\n%s\n\n", s.name, text)
			names = append(names, s.name)
		}
	}
	return sb.String(), names
}

// FixtureEntry is one document in the eval corpus. Key is a stable,
// human-readable identifier that Query.Relevant/Irrelevant reference; the
// database id is minted fresh on every load.
type FixtureEntry struct {
	Key   string
	Input ThoughtInput
	// AgeDays backdates the document by this many days, for recency-sensitive
	// queries. 0 means "just written".
	AgeDays int
}

// FactFixture is a fact planted into the corpus after the documents: it may
// cite a document's first chunk as evidence, supersede another fixture fact,
// and be backdated. Forgotten facts are retired after creation (revocation
// slice).
type FactFixture struct {
	Key           string
	Namespace     string
	Statement     string
	About         []string
	EvidenceKey   string // document fixture key whose first chunk is the evidence
	SupersedesKey string
	Trust         string
	AgeDays       int
	Forgotten     string // reason; empty = live
}

// Query is one labelled search.
type Query struct {
	ID    string
	Query string
	Scope retrieve.Scope
	// Granularity defaults to document; "fact" runs the fact search.
	Granularity string
	// AsOfDaysAgo > 0 asks what was believed that many days ago.
	AsOfDaysAgo int
	// Category groups queries for per-category means (lookup, exact,
	// abstention, ...). Empty counts as "lookup".
	Category string
	// Relevant lists fixture keys that count as correct matches. An empty
	// Relevant means the right answer is to return nothing (abstention).
	Relevant []string
	// Irrelevant lists tempting false positives whose rank is tracked.
	Irrelevant []string
	// RealModelOnly marks queries the deterministic hash embedder cannot
	// answer (paraphrase); they run only when a real model is configured.
	RealModelOnly bool
}

// ToDocs converts journal-era fixtures (one text per section) into
// knowledge-base fixtures: kind=note in the "eval" namespace, section names
// as tags.
func ToDocs(entries []FixtureEntry) []FixtureDoc {
	out := make([]FixtureDoc, 0, len(entries))
	for _, e := range entries {
		content, sections := e.Input.Markdown()
		out = append(out, FixtureDoc{Key: e.Key, Namespace: "eval", Kind: kb.KindNote, Title: e.Key, Tags: sections, Content: "# " + e.Key + "\n\n" + content, AgeDays: e.AgeDays})
	}
	return out
}

// LoadFacts plants fact fixtures (after Load) and returns fact key → id.
// Forgotten documents are retired here too, via ForgottenDocs.
func LoadFacts(ctx context.Context, store *kb.Store, docIDs map[string]string, facts []FactFixture, forgottenDocs map[string]string) (map[string]string, error) {
	ids := map[string]string{}
	now := time.Now()
	for _, f := range facts {
		in := kb.RememberInput{Namespace: f.Namespace, Statement: f.Statement, About: f.About, Origin: kb.OriginUserSaid, Trust: f.Trust, Actor: "eval", Channel: kb.ChannelCLI}
		if in.Trust == "" {
			in.Trust = kb.TrustUser
		}
		if f.EvidenceKey != "" {
			docID, ok := docIDs[f.EvidenceKey]
			if !ok {
				return nil, fmt.Errorf("fact %q: unknown evidence key %q", f.Key, f.EvidenceKey)
			}
			var chunkID int64
			if err := store.DB().QueryRowContext(ctx, `SELECT id FROM chunks WHERE document_id = ? ORDER BY ord LIMIT 1`, docID).Scan(&chunkID); err != nil {
				return nil, fmt.Errorf("fact %q: evidence chunk: %w", f.Key, err)
			}
			in.EvidenceURI = fmt.Sprintf("memo://chunk/%d", chunkID)
		}
		if f.SupersedesKey != "" {
			old, ok := ids[f.SupersedesKey]
			if !ok {
				return nil, fmt.Errorf("fact %q supersedes unknown %q (order matters)", f.Key, f.SupersedesKey)
			}
			in.Supersedes = "memo://fact/" + old
		}
		if f.AgeDays != 0 {
			at := now.AddDate(0, 0, -f.AgeDays)
			store.SetNow(func() time.Time { return at })
		} else {
			store.SetNow(time.Now)
		}
		fact, err := store.Remember(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("fact %q: %w", f.Key, err)
		}
		ids[f.Key] = fact.ID
		if f.Forgotten != "" {
			if err := store.Forget(ctx, kb.ForgetInput{URI: fact.URI, Reason: f.Forgotten, Actor: "eval", Channel: kb.ChannelCLI}); err != nil {
				return nil, err
			}
		}
	}
	store.SetNow(time.Now)
	for key, reason := range forgottenDocs {
		id, ok := docIDs[key]
		if !ok {
			return nil, fmt.Errorf("forgotten doc %q unknown", key)
		}
		if err := store.Forget(ctx, kb.ForgetInput{URI: "memo://doc/" + id, Reason: reason, Actor: "eval", Channel: kb.ChannelCLI}); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// LoadStats reports the cost of loading a corpus.
type LoadStats struct {
	Docs     int
	Chunks   int
	WriteMs  float64 // total wall time for ingest
	PerDocMs float64
}

// Load ingests fixtures into store (trust=user, CLI channel), returning a map
// from fixture key to document id plus the write cost. Versions of the same
// URI become revisions in order of appearance.
func Load(ctx context.Context, store *kb.Store, docs []FixtureDoc) (map[string]string, *LoadStats, error) {
	keyToID := make(map[string]string, len(docs))
	now := time.Now()
	st := &LoadStats{}
	t0 := time.Now()
	for _, d := range docs {
		if strings.TrimSpace(d.Content) == "" {
			return nil, nil, fmt.Errorf("fixture %q has no content", d.Key)
		}
		origin := kb.OriginUserSaid
		if d.URI != "" {
			origin = kb.OriginWeb
		}
		res, err := store.Ingest(ctx, kb.IngestInput{
			Namespace: d.Namespace, Content: d.Content, Trust: kb.TrustUser, Channel: kb.ChannelCLI, Actor: "eval",
			Source: kb.SourceInput{URI: d.URI, Title: d.Title, Kind: d.Kind, Library: d.Library, Version: d.Version, Origin: origin, Tags: d.Tags},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("seed %q: %w", d.Key, err)
		}
		if res.Pending > 0 {
			return nil, nil, fmt.Errorf("seed %q: %d chunk vectors pending; the eval needs an embedder", d.Key, res.Pending)
		}
		keyToID[d.Key] = res.DocumentID
		st.Docs++
		st.Chunks += res.Chunks
		if d.AgeDays != 0 {
			ts := now.AddDate(0, 0, -d.AgeDays).UnixMilli()
			if _, err := store.DB().ExecContext(ctx, `UPDATE documents SET created_at = ?, updated_at = ? WHERE id = ?`, ts, ts, res.DocumentID); err != nil {
				return nil, nil, fmt.Errorf("backdate %q: %w", d.Key, err)
			}
		}
	}
	st.WriteMs = float64(time.Since(t0)) / 1e6
	if st.Docs > 0 {
		st.PerDocMs = st.WriteMs / float64(st.Docs)
	}
	return keyToID, st, nil
}

// QueryMetrics holds the metrics for one query.
type QueryMetrics struct {
	QueryID            string  `json:"query_id"`
	Category           string  `json:"category"`
	RecallAt1          float64 `json:"recall_at_1"`
	RecallAt5          float64 `json:"recall_at_5"`
	RecallAt10         float64 `json:"recall_at_10"`
	MRR                float64 `json:"mrr"`
	NDCG10             float64 `json:"ndcg_10"`
	IrrelevantMeanRank float64 `json:"irrelevant_mean_rank,omitempty"`
	NumRelevant        int     `json:"num_relevant"`
	NumIrrelevant      int     `json:"num_irrelevant,omitempty"`
	NumResults         int     `json:"num_results"`
	// Abstained is set for zero-relevant queries: true when the system
	// correctly returned nothing.
	Abstained *bool `json:"abstained,omitempty"`
	// FirstHitArms names the arms that returned the first relevant result,
	// so a report can say whether a hit came through keyword or meaning.
	FirstHitArms   []string `json:"first_hit_arms,omitempty"`
	LatencyMs      float64  `json:"latency_ms"`
	GraphMs        float64  `json:"graph_ms,omitempty"` // time spent in the entity and graph arms (the graph tax)
	TokensReturned int      `json:"tokens_returned"`
	Skipped        bool     `json:"skipped,omitempty"` // RealModelOnly query under the hash embedder
}

// Report is the result of a run. Means exclude zero-relevant queries from
// the ranking metrics (they have nothing to rank) and report their
// abstention rate separately.
type Report struct {
	Strategy       string                  `json:"strategy,omitempty"` // profile name or "agent-proxy"
	Model          string                  `json:"model,omitempty"`
	PerQuery       []QueryMetrics          `json:"per_query"`
	Mean           QueryMetrics            `json:"mean"`
	PerCategory    map[string]QueryMetrics `json:"per_category"`
	AbstentionRate float64                 `json:"abstention_rate"`
	NumAbstention  int                     `json:"num_abstention_queries"`
	Cost           Cost                    `json:"cost"`
}

// Cost is what quality costs: time and size, next to every quality number.
type Cost struct {
	QueryP50Ms    float64 `json:"query_p50_ms"`
	QueryP95Ms    float64 `json:"query_p95_ms"`
	TokensP50     float64 `json:"tokens_returned_p50"`
	GraphP50Ms    float64 `json:"graph_p50_ms"` // the graph tax: p50 of time in the structural arms, over the queries that ran them
	WritePerDocMs float64 `json:"write_per_doc_ms"`
	DBSizeMB      float64 `json:"db_size_mb"`
	Docs          int     `json:"docs"`
	Chunks        int     `json:"chunks"`
}

// RunOptions tunes Run.
type RunOptions struct {
	// AgentProxy runs the deterministic two-round "search, then re-query with
	// the top hit's section title appended" stand-in for an agent iterating
	// (labelled a weak proxy in the report).
	AgentProxy bool
	// RealModel reports whether a real embedder is configured, enabling the
	// RealModelOnly queries.
	RealModel bool
}

// Run executes every query at document granularity and computes metrics.
func Run(ctx context.Context, svc *retrieve.Service, keyToID map[string]string, queries []Query, opts RunOptions) (*Report, error) {
	report := &Report{PerQuery: make([]QueryMetrics, 0, len(queries)), PerCategory: map[string]QueryMetrics{}}
	const k = 10
	for _, q := range queries {
		if q.RealModelOnly && !opts.RealModel {
			report.PerQuery = append(report.PerQuery, QueryMetrics{QueryID: q.ID, Category: categoryOf(q), Skipped: true})
			continue
		}
		t0 := time.Now()
		gran := q.Granularity
		if gran == "" {
			gran = retrieve.GranularityDocument
		}
		req := retrieve.Request{Query: q.Query, Scope: q.Scope, Granularity: gran, ResponseFormat: retrieve.FormatExplain, Limit: k, MaxTokens: 1 << 20}
		if q.AsOfDaysAgo > 0 {
			at := time.Now().AddDate(0, 0, -q.AsOfDaysAgo)
			req.AsOf = &at
		}
		resp, err := svc.Search(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("query %q: %w", q.ID, err)
		}
		if opts.AgentProxy && len(resp.Results) > 0 {
			// Round two: an agent that reads the top hit and searches again
			// with what it learned (its section title). Results are unioned,
			// first round first.
			req2 := req
			req2.Query = q.Query + " " + resp.Results[0].Title + " " + resp.Results[0].SectionPath
			if r2, err := svc.Search(ctx, req2); err == nil {
				seen := map[string]bool{}
				for _, r := range resp.Results {
					seen[r.DocumentURI] = true
				}
				for _, r := range r2.Results {
					if !seen[r.DocumentURI] && len(resp.Results) < k {
						resp.Results = append(resp.Results, r)
					}
				}
			}
		}
		resultIDs := make([]string, len(resp.Results))
		for i, r := range resp.Results {
			uri := r.DocumentURI
			if gran == retrieve.GranularityFact {
				uri = r.URI
			}
			_, id, _ := kb.ParseURI(uri)
			resultIDs[i] = id
		}
		relevantIDs := keysToIDs(keyToID, q.Relevant)
		irrelevantIDs := keysToIDs(keyToID, q.Irrelevant)
		m := QueryMetrics{QueryID: q.ID, Category: categoryOf(q), NumRelevant: len(relevantIDs), NumIrrelevant: len(irrelevantIDs), NumResults: len(resultIDs), LatencyMs: float64(time.Since(t0)) / 1e6}
		for _, r := range resp.Results {
			m.TokensReturned += chunk.EstimateTokens(r.Content) + 24
		}
		if resp.Trace != nil {
			m.GraphMs = resp.Trace.LatencyMsPerArm[retrieve.ArmEntity] + resp.Trace.LatencyMsPerArm[retrieve.ArmGraph]
		}
		if len(relevantIDs) == 0 {
			abst := len(resultIDs) == 0
			m.Abstained = &abst
		} else {
			m.RecallAt1 = RecallAtK(resultIDs, relevantIDs, 1)
			m.RecallAt5 = RecallAtK(resultIDs, relevantIDs, 5)
			m.RecallAt10 = RecallAtK(resultIDs, relevantIDs, k)
			m.MRR = ReciprocalRank(resultIDs, relevantIDs)
			m.NDCG10 = NDCGAtK(resultIDs, relevantIDs, k)
			for i, r := range resp.Results {
				if contains(relevantIDs, resultIDs[i]) && r.Why != nil {
					for _, a := range r.Why.Arms {
						m.FirstHitArms = append(m.FirstHitArms, a.Arm)
					}
					break
				}
			}
		}
		if len(irrelevantIDs) > 0 {
			m.IrrelevantMeanRank = MeanRank(resultIDs, irrelevantIDs, k)
		}
		report.PerQuery = append(report.PerQuery, m)
	}
	report.Mean, report.AbstentionRate, report.NumAbstention = meanOf(report.PerQuery)
	report.Cost = costOf(report.PerQuery)
	for _, cat := range categories(report.PerQuery) {
		var subset []QueryMetrics
		for _, m := range report.PerQuery {
			if m.Category == cat {
				subset = append(subset, m)
			}
		}
		cm, _, _ := meanOf(subset)
		cm.QueryID, cm.Category = "MEAN", cat
		report.PerCategory[cat] = cm
	}
	return report, nil
}

func costOf(all []QueryMetrics) Cost {
	var lat, tok, gr []float64
	for _, m := range all {
		if m.Skipped {
			continue
		}
		lat = append(lat, m.LatencyMs)
		tok = append(tok, float64(m.TokensReturned))
		if m.GraphMs > 0 {
			gr = append(gr, m.GraphMs) // only queries where the structural arms ran
		}
	}
	sort.Float64s(lat)
	sort.Float64s(tok)
	sort.Float64s(gr)
	pct := func(v []float64, p float64) float64 {
		if len(v) == 0 {
			return 0
		}
		i := int(p * float64(len(v)-1))
		return v[i]
	}
	return Cost{QueryP50Ms: pct(lat, 0.5), QueryP95Ms: pct(lat, 0.95), TokensP50: pct(tok, 0.5), GraphP50Ms: pct(gr, 0.5)}
}

func categoryOf(q Query) string {
	if q.Category != "" {
		return q.Category
	}
	if len(q.Relevant) == 0 {
		return "abstention"
	}
	return "lookup"
}

func categories(all []QueryMetrics) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range all {
		if !seen[m.Category] {
			seen[m.Category] = true
			out = append(out, m.Category)
		}
	}
	sort.Strings(out)
	return out
}

func keysToIDs(keyToID map[string]string, keys []string) []string {
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		if id, ok := keyToID[k]; ok {
			ids = append(ids, id)
		}
	}
	return ids
}

// meanOf averages ranking metrics over queries that have relevant items and
// returns the abstention rate over those that do not.
func meanOf(all []QueryMetrics) (QueryMetrics, float64, int) {
	m := QueryMetrics{QueryID: "MEAN"}
	var ranked, irrelevantCount, abstQueries, abstained int
	for _, q := range all {
		if q.Skipped {
			continue
		}
		if q.Abstained != nil {
			abstQueries++
			if *q.Abstained {
				abstained++
			}
			continue
		}
		ranked++
		m.RecallAt1 += q.RecallAt1
		m.RecallAt5 += q.RecallAt5
		m.RecallAt10 += q.RecallAt10
		m.MRR += q.MRR
		m.NDCG10 += q.NDCG10
		m.LatencyMs += q.LatencyMs
		if q.NumIrrelevant > 0 {
			m.IrrelevantMeanRank += q.IrrelevantMeanRank
			irrelevantCount++
		}
	}
	if ranked > 0 {
		n := float64(ranked)
		m.RecallAt1 /= n
		m.RecallAt5 /= n
		m.RecallAt10 /= n
		m.MRR /= n
		m.NDCG10 /= n
		m.LatencyMs /= n
	}
	if irrelevantCount > 0 {
		m.IrrelevantMeanRank /= float64(irrelevantCount)
	}
	rate := 0.0
	if abstQueries > 0 {
		rate = float64(abstained) / float64(abstQueries)
	}
	return m, rate, abstQueries
}
