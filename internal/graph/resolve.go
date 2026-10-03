package graph

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
)

// Resolution decides whether a new name is an existing entity. It is
// deterministic and non-destructive (research D-H2): exact matches on the
// normalised key merge; near matches above the Jaccard threshold on
// character 3-grams become merge candidates for a human, unless the name is
// too short or too ambiguous to decide (the entropy gate), in which case it
// stays a separate entity.

// Key is the matching form of a name: lowercase, spaces and dashes removed
// around separators, so "Client.Connect" and "client.connect" collide while
// "Client Connect" and "ClientConnect" do too.
func Key(name string) string {
	name = strings.ToLower(Normalize(name))
	name = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(name)
	return name
}

// Trigrams returns the character 3-gram set of a key.
func Trigrams(key string) map[string]bool {
	out := map[string]bool{}
	r := []rune("  " + key + " ")
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])] = true
	}
	return out
}

// Jaccard is |A∩B| / |A∪B| over two trigram sets.
func Jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for g := range a {
		if b[g] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// MinHash is a fixed-size signature whose per-slot equality rate estimates
// Jaccard similarity; two signatures that share any LSH band are candidates
// for the exact Jaccard check, so a new name is compared with a handful of
// entities instead of all of them.
const (
	minhashSize = 32
	lshBands    = 8 // 8 bands × 4 rows: pairs at Jaccard 0.9 collide with p ≈ 1-(1-0.9^4)^8 ≈ 0.96
)

type Signature [minhashSize]uint32

// Sign computes the MinHash signature of a key's trigrams.
func Sign(key string) Signature {
	var sig Signature
	for i := range sig {
		sig[i] = math.MaxUint32
	}
	for g := range Trigrams(key) {
		h := fnv.New32a()
		h.Write([]byte(g))
		base := h.Sum32()
		for i := 0; i < minhashSize; i++ {
			// Universal-ish hashing: mix the base hash with a per-slot odd multiplier.
			v := base*uint32(2*i+1) + uint32(i)*0x9e3779b9
			if v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

// Bands returns the LSH band keys of a signature; two signatures sharing a
// band key are candidates.
func Bands(sig Signature) []string {
	rows := minhashSize / lshBands
	out := make([]string, lshBands)
	for b := 0; b < lshBands; b++ {
		h := fnv.New64a()
		for r := 0; r < rows; r++ {
			v := sig[b*rows+r]
			h.Write([]byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)})
		}
		out[b] = string(rune('a'+b)) + ":" + strings.ToLower(strings.TrimLeft(fmtHex(h.Sum64()), "0"))
	}
	return out
}

func fmtHex(v uint64) string {
	const digits = "0123456789abcdef"
	var b [16]byte
	for i := 15; i >= 0; i-- {
		b[i] = digits[v&0xf]
		v >>= 4
	}
	return string(b[:])
}

// NearThreshold is the trigram Jaccard above which two names are queued as
// a merge candidate. The roadmap sketched 0.9; on real names that misses a
// plural or a one-letter suffix ("Pipeline Scheduler" vs "Pipeline
// Schedulers" is 0.86), so 0.8 is used and the eval's planted alias pairs
// and non-pairs measure the precision of what gets queued. A candidate is a
// question for a human, never an automatic merge.
const NearThreshold = 0.8

// Ambiguous is the entropy gate: names too short or too uniform to resolve
// safely go to the review queue (or stay separate) instead of merging. Short
// keys have few trigrams, so one shared gram swings the Jaccard; very low
// character entropy (e.g. "aaa", "x1x1") means the trigram set carries no
// identity.
func Ambiguous(key string) bool {
	if len([]rune(key)) < 5 {
		return true
	}
	counts := map[rune]int{}
	n := 0
	for _, r := range key {
		counts[r]++
		n++
	}
	h := 0.0
	for _, c := range counts {
		p := float64(c) / float64(n)
		h -= p * math.Log2(p)
	}
	return h < 1.5
}

// Decision is what Resolve concluded for one new name.
type Decision struct {
	Action   string  // same | candidate | new
	MatchKey string  // the existing key matched, if any
	Score    float64 // Jaccard for candidate decisions
	Reason   string
}

// Index is the in-memory resolution index over a namespace's entity keys.
type Index struct {
	keys  map[string]bool
	bands map[string][]string // band key → entity keys
	sigs  map[string]Signature
}

// NewIndex builds the index from existing keys.
func NewIndex(keys []string) *Index {
	ix := &Index{keys: map[string]bool{}, bands: map[string][]string{}, sigs: map[string]Signature{}}
	for _, k := range keys {
		ix.Add(k)
	}
	return ix
}

// Add registers a key.
func (ix *Index) Add(key string) {
	if ix.keys[key] {
		return
	}
	ix.keys[key] = true
	sig := Sign(key)
	ix.sigs[key] = sig
	for _, b := range Bands(sig) {
		ix.bands[b] = append(ix.bands[b], key)
	}
}

// Resolve decides for one name.
func (ix *Index) Resolve(name string) Decision {
	key := Key(name)
	if key == "" {
		return Decision{Action: "new", Reason: "empty key"}
	}
	if ix.keys[key] {
		return Decision{Action: "same", MatchKey: key, Score: 1, Reason: "exact key match"}
	}
	if Ambiguous(key) {
		return Decision{Action: "new", Reason: "entropy gate: name too short or uniform to compare"}
	}
	sig := Sign(key)
	seen := map[string]bool{}
	var cands []string
	for _, b := range Bands(sig) {
		for _, k := range ix.bands[b] {
			if !seen[k] {
				seen[k] = true
				cands = append(cands, k)
			}
		}
	}
	sort.Strings(cands)
	mine := Trigrams(key)
	best, bestScore := "", 0.0
	for _, k := range cands {
		if Ambiguous(k) {
			continue
		}
		// Names that differ in a number are enumerated things (Publish7 vs
		// Publish10, handbook 1 vs handbook 2), not spellings of one thing.
		if digits(k) != digits(key) {
			continue
		}
		if j := Jaccard(mine, Trigrams(k)); j > bestScore {
			best, bestScore = k, j
		}
	}
	if best != "" && bestScore >= NearThreshold {
		return Decision{Action: "candidate", MatchKey: best, Score: bestScore, Reason: "3-gram Jaccard above threshold; queued for review, not merged"}
	}
	return Decision{Action: "new", Reason: "no near match"}
}

// digits returns the digit characters of a key in order.
func digits(key string) string {
	var b strings.Builder
	for _, r := range key {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
