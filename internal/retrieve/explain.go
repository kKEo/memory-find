package retrieve

import "time"

// The explain contract. CLI, MCP and (later) the UI render these same
// structs; tests assert the numbers are identical across faces.

// ArmHit is one arm's view of a result.
type ArmHit struct {
	Arm          string   `json:"arm"`
	Rank         *int     `json:"rank"`               // position in this arm's list (1-based), null if the arm did not return it
	Raw          *float64 `json:"raw,omitempty"`      // the arm's own score (cosine or bm25)
	RawKind      string   `json:"raw_kind,omitempty"` // cosine | bm25
	Contribution float64  `json:"contribution"`       // weight / (k + rank), 0 if absent
	MatchedTerms []string `json:"matched_terms,omitempty"`
}

// Why explains one result.
type Why struct {
	URI           string     `json:"uri"`
	Chunk         ChunkRef   `json:"chunk"`
	Document      DocRef     `json:"document"`
	Arms          []ArmHit   `json:"arms"`
	Fused         float64    `json:"fused"`
	RecencyFactor float64    `json:"recency_factor"`
	Final         float64    `json:"final"`
	Rank          int        `json:"rank"`
	Relevance     *float64   `json:"relevance"` // best raw cosine; null when the semantic arm did not run
	Band          string     `json:"band"`      // strong | moderate | weak | keyword-only
	Provenance    ProvRef    `json:"provenance"`
	Freshness     Freshness  `json:"freshness"`
	Time          *TimeInfo  `json:"time,omitempty"`
	Rerank        *RerankHit `json:"rerank,omitempty"`
}

// RerankHit records what the cross-encoder did to one result.
type RerankHit struct {
	Model      string  `json:"model"`
	Score      float64 `json:"score"`
	BeforeRank int     `json:"before_rank"`
}

// ChunkRef locates the winning chunk.
type ChunkRef struct {
	URI         string `json:"uri"`
	Ord         int    `json:"ord"`
	SectionPath string `json:"section_path"`
	EstTokens   int    `json:"est_tokens"`
}

// DocRef locates the document.
type DocRef struct {
	URI      string `json:"uri"`
	Title    string `json:"title"`
	Revision int    `json:"revision"`
}

// ProvRef is the provenance a result carries.
type ProvRef struct {
	SourceURI string    `json:"source_uri,omitempty"`
	Version   string    `json:"version,omitempty"`
	Library   string    `json:"library,omitempty"`
	Kind      string    `json:"kind"`
	FetchedAt time.Time `json:"fetched_at"`
	Trust     string    `json:"trust"`
	Origin    string    `json:"origin"`
	Namespace string    `json:"namespace"`
}

// Freshness flags.
type Freshness struct {
	Stale      bool `json:"stale"`
	TTLExpired bool `json:"ttl_expired"`
}

// Trace explains one query.
type Trace struct {
	ModeRequested    string             `json:"mode_requested"`
	ModeResolved     string             `json:"mode_resolved"`
	ArmsRun          []string           `json:"arms_run"`
	RoutingReason    string             `json:"routing_reason"`
	Profile          string             `json:"profile"`
	ModelID          string             `json:"model_id,omitempty"`
	CandidatesPerArm map[string]int     `json:"candidates_per_arm"`
	Filtered         Filtered           `json:"filtered"`
	Cutoff           Cutoff             `json:"cutoff"`
	Budget           Budget             `json:"budget"`
	LatencyMsPerArm  map[string]float64 `json:"latency_ms_per_arm"`
	Degraded         Degraded           `json:"degraded"`
	AsOf             *time.Time         `json:"as_of,omitempty"`
	Rerank           *RerankTrace       `json:"rerank,omitempty"`
}

// RerankTrace says what the reranker was applied to.
type RerankTrace struct {
	Model     string  `json:"model"`
	TopN      int     `json:"top_n"`
	LatencyMs float64 `json:"latency_ms"`
}

// Filtered counts what the pre-ranking filters removed or constrained.
type Filtered struct {
	ByScope         string `json:"by_scope"` // human summary of the scope applied
	LiveDocs        int    `json:"live_docs_in_scope"`
	ByRevocation    int    `json:"by_revocation"` // superseded/forgotten documents excluded
	ByMinTrust      int    `json:"by_min_trust"`
	BySemanticFloor int    `json:"by_semantic_floor"` // semantic-only candidates below the floor
}

// Cutoff says why the list ended where it did.
type Cutoff struct {
	Kind     string  `json:"kind"` // gap | budget | limit | none
	Position int     `json:"position"`
	Gap      float64 `json:"gap,omitempty"`
}

// Budget reports token packing.
type Budget struct {
	MaxTokens      int    `json:"max_tokens"`
	Used           int    `json:"used"`
	TruncatedCount int    `json:"truncated_count"`
	NarrowHint     string `json:"narrow_hint,omitempty"`
}

// Degraded says whether some capability was missing.
type Degraded struct {
	Flag   bool   `json:"flag"`
	Reason string `json:"reason,omitempty"`
}

// TimeInfo is the two-clock view of a fact result (P4).
type TimeInfo struct {
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidTo       *time.Time `json:"valid_to,omitempty"`
	RecordedAt    time.Time  `json:"recorded_at"`
	InvalidatedAt *time.Time `json:"invalidated_at,omitempty"`
	SupersededBy  string     `json:"superseded_by,omitempty"`
	AsOfApplied   *time.Time `json:"as_of_applied,omitempty"`
}
