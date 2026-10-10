// Package compact turns piles of chunks into work for the calling agent:
// pages to write, conflicts to resolve, near-duplicates and name merges to
// decide, stale pages to rebuild. The server proposes and records; it never
// writes a page or picks a winner itself (research D-C, D-D; roadmap P8).
package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kKEo/memors/internal/graph"
	"github.com/kKEo/memors/internal/kb"
)

// Thresholds. Starting points the eval measures, printed by `compact --explain`.
const (
	// MinMentionsForPage: an entity mentioned in this many live chunks
	// deserves a page (the "N chunks about X" trigger).
	MinMentionsForPage = 3
	// RecurrenceGrowth: a page is proposed again only when the entity has
	// gained this many mentions since the page was built (RecMem: only
	// clusters that keep growing cost work).
	RecurrenceGrowth = 2
	// DuplicateJaccard on word 3-grams (heading line included) marks two
	// chunks from different documents as near-duplicates: 0.75 catches a
	// copied paragraph with a different title and one added sentence.
	DuplicateJaccard = 0.75
)

// Kinds of work item.
const (
	KindPage      = "page"
	KindStale     = "stale"
	KindConflict  = "conflict"
	KindMerge     = "merge"
	KindDuplicate = "duplicate"
)

// PagePayload is what the agent gets for a page or stale item: everything
// needed to write the page without another call.
type PagePayload struct {
	Entity    kb.Entity       `json:"entity"`
	PageID    string          `json:"page_id,omitempty"` // existing page to rebuild
	Reason    string          `json:"reason"`
	Chunks    []PayloadChunk  `json:"chunks"`
	Facts     []kb.Fact       `json:"facts,omitempty"`
	MustCover []string        `json:"must_cover"` // fact statements the page must not omit (the omission check)
	Previous  string          `json:"previous,omitempty"`
	Hint      json.RawMessage `json:"-"`
}

// PayloadChunk is one source passage.
type PayloadChunk struct {
	URI   string `json:"uri"`
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// ConflictPayload is two live facts about one subject that disagree.
type ConflictPayload struct {
	Subject kb.Entity `json:"subject"`
	A       kb.Fact   `json:"a"`
	B       kb.Fact   `json:"b"`
	Reason  string    `json:"reason"`
	Rule    string    `json:"rule"` // what the server would decide: trust first, then recency
}

// DuplicatePayload is two near-identical chunks from different documents.
type DuplicatePayload struct {
	A       PayloadChunk `json:"a"`
	B       PayloadChunk `json:"b"`
	Jaccard float64      `json:"jaccard"`
}

// MergePayload wraps an open merge candidate.
type MergePayload struct {
	Candidate kb.MergeCandidate `json:"candidate"`
}

// Generate scans a namespace and records the open work items. It is
// idempotent: an item already open for the same subject is refreshed, not
// duplicated. Returns the items created or refreshed, newest kinds first.
func Generate(ctx context.Context, store *kb.Store, ns string, kinds []string) ([]kb.WorkItem, error) {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	all := len(kinds) == 0
	var out []kb.WorkItem
	add := func(kind, subject string, payload any) error {
		w, _, err := store.UpsertWorkItem(ctx, ns, kind, subject, payload)
		if err != nil {
			return err
		}
		out = append(out, *w)
		return nil
	}
	if all || want[KindPage] || want[KindStale] {
		rows, err := store.EntitiesWithMentions(ctx, ns, MinMentionsForPage)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			kind := ""
			reason := ""
			switch {
			case r.PageID == "":
				kind, reason = KindPage, fmt.Sprintf("%d live passages mention %s and it has no page", r.Entity.Mentions, r.Entity.Canonical)
			case r.PageStale:
				kind, reason = KindStale, "the page is stale: a source it was built from changed"
			case r.Entity.Mentions >= r.PageSources+RecurrenceGrowth:
				kind, reason = KindPage, fmt.Sprintf("%s gained %d passages since its page was built", r.Entity.Canonical, r.Entity.Mentions-r.PageSources)
			default:
				continue
			}
			if !all && !want[kind] {
				continue
			}
			payload, err := pagePayload(ctx, store, r, reason)
			if err != nil {
				return nil, err
			}
			if err := add(kind, r.Entity.ID, payload); err != nil {
				return nil, err
			}
		}
	}
	if all || want[KindConflict] {
		conflicts, err := Conflicts(ctx, store, ns)
		if err != nil {
			return nil, err
		}
		for _, c := range conflicts {
			if err := add(KindConflict, c.A.ID+"|"+c.B.ID, c); err != nil {
				return nil, err
			}
		}
	}
	if all || want[KindMerge] {
		cands, err := store.MergeCandidates(ctx, "open")
		if err != nil {
			return nil, err
		}
		for _, c := range cands {
			if c.A.Namespace != ns {
				continue
			}
			if err := add(KindMerge, fmt.Sprintf("merge:%d", c.ID), MergePayload{Candidate: c}); err != nil {
				return nil, err
			}
		}
	}
	if all || want[KindDuplicate] {
		dups, err := Duplicates(ctx, store, ns)
		if err != nil {
			return nil, err
		}
		for _, d := range dups {
			if err := add(KindDuplicate, fmt.Sprintf("%d|%d", d.A.ID, d.B.ID), d); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

func pagePayload(ctx context.Context, store *kb.Store, r kb.EntityPageRow, reason string) (*PagePayload, error) {
	ex, err := store.Explore(ctx, r.Entity.ID, r.Entity.Namespace, 1, nil)
	if err != nil {
		return nil, err
	}
	p := &PagePayload{Entity: ex.Entity, PageID: r.PageID, Reason: reason, MustCover: []string{}}
	for _, uri := range ex.Chunks {
		var id int64
		if _, err := fmt.Sscanf(uri, "memo://chunk/%d", &id); err != nil {
			continue
		}
		c, err := store.ReadChunk(ctx, id)
		if err != nil {
			continue
		}
		p.Chunks = append(p.Chunks, PayloadChunk{URI: uri, ID: id, Title: c.Prov.Title, Text: c.Text})
	}
	facts, err := store.FactsAbout(ctx, r.Entity.ID)
	if err != nil {
		return nil, err
	}
	p.Facts = facts
	for _, f := range facts {
		p.MustCover = append(p.MustCover, f.Statement)
	}
	if r.PageID != "" {
		if prev, err := store.ReadPage(ctx, r.PageID); err == nil {
			p.Previous = prev.Content
		}
	}
	return p, nil
}

// Conflicts finds pairs of live facts about the same subject whose validity
// windows overlap and whose statements differ: two current answers to one
// question. The rule the server would apply (and the agent may confirm) is
// trust first, then the more recently recorded fact.
func Conflicts(ctx context.Context, store *kb.Store, ns string) ([]ConflictPayload, error) {
	facts, err := store.ListFacts(ctx, kb.FactFilter{Namespace: ns, Limit: 1 << 20})
	if err != nil {
		return nil, err
	}
	bySubject := map[string][]kb.Fact{}
	for _, f := range facts {
		for _, a := range f.About {
			bySubject[graph.Key(a)] = append(bySubject[graph.Key(a)], f)
		}
	}
	var out []ConflictPayload
	seen := map[string]bool{}
	for key, list := range bySubject {
		if len(list) < 2 {
			continue
		}
		sort.Slice(list, func(i, j int) bool { return list[i].RecordedAt.Before(list[j].RecordedAt) })
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				a, b := list[i], list[j]
				if a.ID == b.ID || !overlap(a, b) || graph.Key(a.Statement) == graph.Key(b.Statement) {
					continue
				}
				pair := a.ID + "|" + b.ID
				if seen[pair] {
					continue
				}
				seen[pair] = true
				subj := kb.Entity{Canonical: key}
				if e, err := store.ReadEntity(ctx, key, ns); err == nil {
					subj = *e
				}
				out = append(out, ConflictPayload{Subject: subj, A: a, B: b,
					Reason: "two live facts about " + subj.Canonical + " with overlapping validity disagree",
					Rule:   ruleFor(a, b)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].A.ID+out[i].B.ID < out[j].A.ID+out[j].B.ID })
	return out, nil
}

func overlap(a, b kb.Fact) bool {
	start := func(f kb.Fact) time.Time {
		if f.ValidFrom != nil {
			return *f.ValidFrom
		}
		return time.Time{}
	}
	end := func(f kb.Fact) time.Time {
		if f.ValidTo != nil {
			return *f.ValidTo
		}
		return time.Unix(1<<40, 0)
	}
	return start(a).Before(end(b)) && start(b).Before(end(a))
}

var trustRank = map[string]int{kb.TrustAgent: 0, kb.TrustUser: 1, kb.TrustCurated: 2}

// ruleFor says which fact the deterministic rule keeps: higher trust, then
// the more recently recorded.
func ruleFor(a, b kb.Fact) string {
	switch {
	case trustRank[a.Trust] > trustRank[b.Trust]:
		return "keep " + a.URI + " (more trusted: " + a.Trust + " over " + b.Trust + ")"
	case trustRank[b.Trust] > trustRank[a.Trust]:
		return "keep " + b.URI + " (more trusted: " + b.Trust + " over " + a.Trust + ")"
	case b.RecordedAt.After(a.RecordedAt):
		return "keep " + b.URI + " (same trust; recorded later)"
	default:
		return "keep " + a.URI + " (same trust; recorded later)"
	}
}

// Duplicates finds near-identical chunks across different documents using
// MinHash bands over word 3-grams, confirmed by exact Jaccard.
func Duplicates(ctx context.Context, store *kb.Store, ns string) ([]DuplicatePayload, error) {
	chunks, err := store.LiveChunks(ctx, ns)
	if err != nil {
		return nil, err
	}
	type sig struct {
		idx   int
		grams map[string]bool
	}
	bands := map[string][]int{}
	sigs := make([]sig, len(chunks))
	for i, c := range chunks {
		words := strings.Fields(strings.ToLower(c.Text))
		if len(words) < 8 {
			continue
		}
		grams := map[string]bool{}
		for k := 0; k+3 <= len(words); k++ {
			grams[strings.Join(words[k:k+3], " ")] = true
		}
		sigs[i] = sig{idx: i, grams: grams}
		for _, b := range graph.Bands(signWords(grams)) {
			bands[b] = append(bands[b], i)
		}
	}
	seen := map[[2]int]bool{}
	var out []DuplicatePayload
	for _, members := range bands {
		for x := 0; x < len(members); x++ {
			for y := x + 1; y < len(members); y++ {
				i, j := members[x], members[y]
				if i > j {
					i, j = j, i
				}
				if seen[[2]int{i, j}] || chunks[i].DocID == chunks[j].DocID {
					continue
				}
				seen[[2]int{i, j}] = true
				if jac := graph.Jaccard(sigs[i].grams, sigs[j].grams); jac >= DuplicateJaccard {
					out = append(out, DuplicatePayload{
						A:       PayloadChunk{URI: fmt.Sprintf("memo://chunk/%d", chunks[i].ID), ID: chunks[i].ID, Title: chunks[i].Title, Text: chunks[i].Text},
						B:       PayloadChunk{URI: fmt.Sprintf("memo://chunk/%d", chunks[j].ID), ID: chunks[j].ID, Title: chunks[j].Title, Text: chunks[j].Text},
						Jaccard: jac})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].A.ID < out[j].A.ID || (out[i].A.ID == out[j].A.ID && out[i].B.ID < out[j].B.ID)
	})
	return out, nil
}

// signWords is a MinHash over an arbitrary gram set (graph.Sign works on a
// key string; this reuses its signature type and bands).
func signWords(grams map[string]bool) graph.Signature {
	var sig graph.Signature
	for i := range sig {
		sig[i] = ^uint32(0)
	}
	for g := range grams {
		h := fnv32(g)
		for i := range sig {
			v := h*uint32(2*i+1) + uint32(i)*0x9e3779b9
			if v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

func fnv32(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
