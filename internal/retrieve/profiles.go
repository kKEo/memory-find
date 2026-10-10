package retrieve

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Fusion methods.
const (
	FusionRRF    = "rrf"    // reciprocal rank fusion: weight/(k+rank) per arm, summed
	FusionMinMax = "minmax" // each arm's raw scores min-max scaled to 0..1, weighted sum
)

// Named profiles. Each constant has a derivation in Describe(); none is
// exposed in the LLM-facing schema (roadmap D-K). The eval lab (P3) compares
// them with `memors-mcp eval --profiles a,b`.
var profiles = map[string]Profile{}

func init() {
	register := func(p Profile) { profiles[p.Name] = p }
	register(Default)

	precise := Default
	precise.Name = "precise"
	precise.FetchDepth = 200
	precise.CutoffGap = 0.6
	precise.MinResults = 2
	precise.Rerank = true
	precise.RerankTopN = 30
	precise.Derivation = "Deeper fetch (200 per arm) so rarely-worded answers survive fusion, and a stricter gap cut (0.6) so the list ends sooner once confidence drops. The optional cross-encoder reranker (OD-7) attaches here when it passes its gate."
	register(precise)

	recency := Default
	recency.Name = "recency"
	recency.RecencyKinds = []string{"note", "conversation", "doc", "code"}
	recency.RecencyFloor = 0.6
	recency.HalfLifeDays = 30
	recency.Derivation = "For 'what did I write lately': recency applies to every kind, the floor drops to 0.6 so a month-old item can lose 40%, and the half-life is 30 days. Never the default: versioned documentation does not age."
	register(recency)

	code := Default
	code.Name = "code"
	code.Weights = map[string]float64{ArmSemantic: 0.4, ArmKeyword: 0.4, ArmExact: 0.6, ArmFact: 0.4, ArmEntity: 0.5, ArmGraph: 0.5}
	code.RecencyKinds = nil
	code.Derivation = "For identifier-heavy questions: the exact arm outweighs the others (0.6 vs 0.4/0.4) so a whole-token match on a symbol wins, and recency is off because code and docs do not age by date."
	register(code)

	kw := Default
	kw.Name = "keyword-only"
	kw.Weights = map[string]float64{ArmKeyword: 1}
	kw.Arms = []string{ArmKeyword}
	kw.Derivation = "Ablation: BM25 alone. The baseline every semantic feature has to beat (GraphRAG-Bench: BM25 scored 71.7 against 72.5 for a full graph pipeline)."
	register(kw)

	sem := Default
	sem.Name = "semantic-only"
	sem.Weights = map[string]float64{ArmSemantic: 1}
	sem.Arms = []string{ArmSemantic}
	sem.Derivation = "Ablation: vectors alone, so the eval can show what the keyword and exact arms contribute."
	register(sem)

	ng := Default
	ng.Name = "no-graph"
	ng.Arms = []string{ArmSemantic, ArmKeyword, ArmExact, ArmFact, ArmEntity}
	ng.Derivation = "Ablation for the P7 gate: everything in default except the personalised-PageRank graph arm. The multi-hop slice under default minus this profile is the graph arm's whole contribution; its latency column is the graph tax."
	register(ng)

	text := Default
	text.Name = "text-only"
	text.GraphAuto = false
	text.Arms = []string{ArmSemantic, ArmKeyword, ArmExact, ArmFact}
	text.Derivation = "Ablation for the P7 gate: the 1.0 arms with no entity or graph arm, so the entity arm's own contribution is default minus no-graph minus this."
	register(text)

	mm := Default
	mm.Name = "minmax"
	mm.Fusion = FusionMinMax
	mm.Derivation = "Score fusion instead of rank fusion: each arm's raw scores are min-max scaled to 0..1 and summed with the same weights. OpenSearch measured RRF about 3.9% worse nDCG@10 than score fusion on BEIR (SOTA §3.5); the eval decides whether that holds here."
	register(mm)
}

// Profiles lists the registered profiles by name.
func Profiles() []Profile {
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns a profile by name.
func Lookup(name string) (Profile, error) {
	if name == "" {
		return Default, nil
	}
	p, ok := profiles[name]
	if !ok {
		names := make([]string, 0, len(profiles))
		for n := range profiles {
			names = append(names, n)
		}
		sort.Strings(names)
		return Profile{}, fmt.Errorf("unknown profile %q (have %s)", name, strings.Join(names, ", "))
	}
	return p, nil
}

// Override is the on-disk shape of $MEMORS_HOME/profiles.json: a map from
// profile name to the fields to change. Only the fields present are applied;
// a name that does not exist yet creates a new profile based on Default.
// JSON rather than TOML keeps the binary free of a parsing dependency.
type Override struct {
	Base          string             `json:"base,omitempty"`
	RRFK          *int               `json:"rrf_k,omitempty"`
	Weights       map[string]float64 `json:"weights,omitempty"`
	FetchDepth    *int               `json:"fetch_depth,omitempty"`
	Fusion        *string            `json:"fusion,omitempty"`
	HalfLifeDays  *float64           `json:"half_life_days,omitempty"`
	RecencyFloor  *float64           `json:"recency_floor,omitempty"`
	RecencyKinds  []string           `json:"recency_kinds,omitempty"`
	CutoffGap     *float64           `json:"cutoff_gap,omitempty"`
	MinResults    *int               `json:"min_results,omitempty"`
	SemanticFloor *float64           `json:"semantic_floor,omitempty"`
	Bands         []float64          `json:"bands,omitempty"` // [strong, moderate, weak]
	Derivation    *string            `json:"derivation,omitempty"`
}

// LoadOverrides applies profiles.json if it exists. Missing file: no-op.
func LoadOverrides(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var over map[string]Override
	if err := json.Unmarshal(b, &over); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for name, o := range over {
		base, ok := profiles[name]
		if !ok {
			base = Default
			if o.Base != "" {
				if b2, ok := profiles[o.Base]; ok {
					base = b2
				} else {
					return fmt.Errorf("%s: profile %q: unknown base %q", path, name, o.Base)
				}
			}
		}
		p := base
		p.Name = name
		p.Weights = map[string]float64{}
		for k, v := range base.Weights {
			p.Weights[k] = v
		}
		if o.RRFK != nil {
			p.RRFK = *o.RRFK
		}
		for k, v := range o.Weights {
			p.Weights[k] = v
		}
		if o.FetchDepth != nil {
			p.FetchDepth = *o.FetchDepth
		}
		if o.Fusion != nil {
			if *o.Fusion != FusionRRF && *o.Fusion != FusionMinMax {
				return fmt.Errorf("%s: profile %q: fusion must be rrf or minmax", path, name)
			}
			p.Fusion = *o.Fusion
		}
		if o.HalfLifeDays != nil {
			p.HalfLifeDays = *o.HalfLifeDays
		}
		if o.RecencyFloor != nil {
			p.RecencyFloor = *o.RecencyFloor
		}
		if o.RecencyKinds != nil {
			p.RecencyKinds = o.RecencyKinds
		}
		if o.CutoffGap != nil {
			p.CutoffGap = *o.CutoffGap
		}
		if o.MinResults != nil {
			p.MinResults = *o.MinResults
		}
		if o.SemanticFloor != nil {
			p.SemanticFloor = *o.SemanticFloor
		}
		if len(o.Bands) == 3 {
			p.BandStrong, p.BandModerate, p.BandWeak = o.Bands[0], o.Bands[1], o.Bands[2]
		}
		if o.Derivation != nil {
			p.Derivation = *o.Derivation
		} else if !ok {
			p.Derivation = "user-defined in " + path
		}
		profiles[name] = p
	}
	return nil
}

// Describe prints every constant of a profile with its meaning, for
// `memors-mcp profiles show`: the transparency rule says every knob is
// named, derived and printable.
func Describe(p Profile) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "profile %s\n", p.Name)
	if p.Derivation != "" {
		fmt.Fprintf(&sb, "  %s\n", wrap(p.Derivation, 88, "  "))
	}
	fusion := p.Fusion
	if fusion == "" {
		fusion = FusionRRF
	}
	fmt.Fprintf(&sb, "  fusion          %-8s  how arm lists are combined (rrf: weight/(k+rank) summed; minmax: scaled scores summed)\n", fusion)
	fmt.Fprintf(&sb, "  rrf_k           %-8d  dampens the gap between rank 1 and rank 2 (60 = RRF paper value)\n", p.RRFK)
	arms := make([]string, 0, len(p.Weights))
	for a := range p.Weights {
		arms = append(arms, a)
	}
	sort.Strings(arms)
	for _, a := range arms {
		fmt.Fprintf(&sb, "  weight.%-9s %-8.2f  how much the %s arm's vote counts\n", a, p.Weights[a], a)
	}
	fmt.Fprintf(&sb, "  fetch_depth     %-8d  candidates each arm returns before fusion\n", p.FetchDepth)
	fmt.Fprintf(&sb, "  half_life_days  %-8.0f  the recency bonus halves every this many days\n", p.HalfLifeDays)
	fmt.Fprintf(&sb, "  recency_floor   %-8.2f  oldest items keep this fraction of their score\n", p.RecencyFloor)
	fmt.Fprintf(&sb, "  recency_kinds   %-8s  kinds that age (versioned docs do not)\n", strings.Join(p.RecencyKinds, ","))
	fmt.Fprintf(&sb, "  cutoff_gap      %-8.2f  stop before a result whose score drops by more than this fraction\n", p.CutoffGap)
	fmt.Fprintf(&sb, "  min_results     %-8d  never gap-cut below this many results\n", p.MinResults)
	fmt.Fprintf(&sb, "  semantic_floor  %-8.2f  drop semantic-only candidates below this cosine\n", p.SemanticFloor)
	fmt.Fprintf(&sb, "  bands           %.2f/%.2f/%.2f  strong/moderate/weak thresholds on raw cosine\n", p.BandStrong, p.BandModerate, p.BandWeak)
	fmt.Fprintf(&sb, "  rerank          %-8v  re-score the top %d with a cross-encoder when one is attached\n", p.Rerank, p.RerankTopN)
	fmt.Fprintf(&sb, "  limits          %d default, %d max results\n", p.DefaultLimit, p.MaxLimit)
	return sb.String()
}

func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	var sb strings.Builder
	line := 0
	for i, w := range words {
		if line+len(w)+1 > width && line > 0 {
			sb.WriteString("\n" + indent)
			line = 0
		} else if i > 0 {
			sb.WriteByte(' ')
			line++
		}
		sb.WriteString(w)
		line += len(w)
	}
	return sb.String()
}
