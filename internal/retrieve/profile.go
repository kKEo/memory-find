// Package retrieve is the search engine of the knowledge base: several
// retrieval arms (keyword, exact identifier, semantic; later fact, entity
// and graph) run over the same pre-filtered set of live chunks, their ranked
// lists are fused with reciprocal rank fusion, chunks are aggregated to
// documents, a bounded recency factor is applied, the list is cut at the
// first large score gap, and every result can explain itself.
//
// Numeric knobs live in a Profile, never in the tool schema (roadmap D-K).
package retrieve

// Profile is a named bundle of tuning constants. The defaults below carry the
// journal's measured values forward; P3 tunes them in the eval lab and adds
// named alternatives (precise, recency, code).
type Profile struct {
	Name string
	// RRFK is the k in weight/(k+rank). 60 is the value from the original RRF
	// paper and what the journal shipped with.
	RRFK int
	// Weights per arm. Absent arm = 0. The journal shipped 0.6 vector / 0.4
	// keyword; the exact arm gets a modest weight so identifier hits reach
	// the first page without dominating prose queries.
	Weights map[string]float64
	// FetchDepth is how many candidates each arm returns before fusion. Deep
	// enough that a keyword-only hit can reach the first page (the journal's
	// 30 was not).
	FetchDepth int
	// Recency: final = fused * (RecencyFloor + (1-RecencyFloor) * 0.5^(age/HalfLifeDays)),
	// applied only to documents whose source kind is in RecencyKinds.
	HalfLifeDays float64
	RecencyFloor float64
	RecencyKinds []string
	// CutoffGap: the list is cut before the first result whose final score is
	// below (1-CutoffGap) of the previous one, once at least MinResults are
	// kept. 0 disables the gap cut.
	CutoffGap  float64
	MinResults int
	// Relevance bands on raw cosine similarity.
	BandStrong, BandModerate, BandWeak float64
	// SemanticFloor drops a candidate whose ONLY evidence is a semantic
	// similarity below this value. Nearest-neighbour search always returns
	// something, so without a floor a query about nothing in the corpus
	// still gets a full page of noise and abstention never happens. A
	// candidate that also matched a keyword is never dropped by the floor.
	SemanticFloor float64
	// DefaultLimit caps results when the caller gives none.
	DefaultLimit int
	MaxLimit     int
	// Fusion is rrf (default) or minmax; see profiles.go.
	Fusion string
	// GraphAuto lets mode=auto add the structural arms (entity, graph) when
	// the query names two or more known entities or asks a relational
	// question (P7). The pre-registered gate: on by default only if the
	// eval's multi-hop slice gains at least 0.05 nDCG@10 with no single-hop
	// category losing more than the baseline tolerance; the no-graph and
	// text-only profiles are the ablations.
	GraphAuto bool
	// Arms restricts which arms may run (nil = all the mode asks for); the
	// ablation profiles use it.
	Arms []string
	// Rerank re-scores the top RerankTopN fused results with the attached
	// cross-encoder (if one is attached); see rerank package and OD-7.
	Rerank     bool
	RerankTopN int
	// Derivation explains in one paragraph why the constants are what they
	// are; `memo-mcp profiles show` prints it.
	Derivation string
}

// Default is the profile queries use unless the caller names another.
//
// Derivations: RRFK=60 is the value from the RRF paper and what the journal
// shipped. Weights are EQUAL (0.5/0.5), not the journal's 0.6/0.4: with
// unequal weights a keyword-only hit at rank 1 (0.4/61 ≈ 0.0066) scores below
// a vector-only hit down to rank 31 (0.6/91 ≈ 0.0066), so on a corpus with
// thirty semantically similar decoys the one document that contains the
// query's rare term could never reach the first page — the audit's H2
// finding. With equal weights a rank-1 hit in either arm ties a rank-1 hit in
// the other, and the arm that is confident wins ties through the exact arm.
// exact=0.3 is below keyword so an identifier match alone ranks like a strong
// keyword match without dominating prose queries (P3 measures all three).
// FetchDepth=100 keeps keyword-only hits in the fused list instead of cutting
// them before fusion as the journal did.
// Recency floor 0.8 with a 90-day half-life is the shipped factor, now limited
// to notes and conversations because versioned docs do not age (OD-4).
// CutoffGap 0.5 cuts at a halving of the fused score, a large drop in RRF
// terms; MinResults 3 keeps weak-but-plausible neighbours visible.
// SemanticFloor equals the weak band (0.30): below it a semantic-only hit is
// "very weak", and returning it would turn every no-match query into a page
// of noise; P3 calibrates the bands and the floor per model.
var Default = Profile{
	Name:          "default",
	Derivation:    "RRF k=60 (the RRF paper's value). Equal semantic/keyword weights so a keyword-only rank-1 hit ties a vector rank-1 hit and can reach the first page (audit H2); exact=0.3 so an identifier match ranks like a strong keyword match without dominating prose queries. Fetch depth 100 keeps keyword-only hits in the fused list. Recency floor 0.8 with a 90-day half-life, notes and conversations only (OD-4). Gap cut at a halving, never below 3 results. Semantic-only floor 0.30 = the weak band, so no-match queries abstain. fact=0.4: a stored fact that matches votes for its evidence passage almost as strongly as a keyword hit, because a human or agent deliberately recorded it. entity=0.4: a passage that mentions a thing the query names is a strong lead, scaled down for entities mentioned everywhere. graph=0.5: a passage the walks from every named entity agree on ties a keyword hit, because the graph arm only reports what one hop cannot see; it is routed in only for relational or multi-entity questions (P7 gate: multi-hop nDCG@10 0.43 text-only, 0.51 with the entity arm, 0.68 with the graph arm at 0.5; single-hop slices unchanged; docs/eval/v1.1.0.md). All of these are starting points the eval measures, not truths.",
	RRFK:          60,
	Weights:       map[string]float64{ArmSemantic: 0.5, ArmKeyword: 0.5, ArmExact: 0.3, ArmFact: 0.4, ArmEntity: 0.4, ArmGraph: 0.5},
	GraphAuto:     true,
	FetchDepth:    100,
	HalfLifeDays:  90,
	RecencyFloor:  0.8,
	RecencyKinds:  []string{"note", "conversation"},
	CutoffGap:     0.5,
	MinResults:    3,
	BandStrong:    0.60,
	BandModerate:  0.45,
	BandWeak:      0.30,
	SemanticFloor: 0.30,
	DefaultLimit:  10,
	MaxLimit:      100,
}

// Arm names.
const (
	ArmSemantic = "semantic"
	ArmKeyword  = "keyword"
	ArmExact    = "exact"
	// ArmFact matches stored facts (keyword and meaning) and votes for their
	// evidence chunk: "facts as extra keys" (research D-C, §6.1).
	ArmFact = "fact"
	// ArmEntity: chunks mentioning the entities the query names (one hop
	// over the mention graph; P7).
	ArmEntity = "entity"
	// ArmGraph: personalised PageRank from the matched entities over the
	// mention graph; routed in by wantsGraph or mode=graph (P7).
	ArmGraph = "graph"
)

// Modes.
const (
	ModeAuto     = "auto"
	ModeHybrid   = "hybrid"
	ModeKeyword  = "keyword"
	ModeExact    = "exact"
	ModeSemantic = "semantic"
	// ModeGraph runs only the structural arms (entity + graph): the
	// ablation that shows what the graph finds on its own.
	ModeGraph = "graph"
)

// Granularities.
const (
	GranularityChunk    = "chunk"
	GranularityDocument = "document"
	GranularityFact     = "fact"
)

// Response formats.
const (
	FormatConcise  = "concise"
	FormatDetailed = "detailed"
	FormatExplain  = "explain"
)
