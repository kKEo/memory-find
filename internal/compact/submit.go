package compact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kKEo/memors/internal/kb"
)

// Result is what the agent submits for a work item.
type Result struct {
	// page | stale: the page text.
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
	// conflict: which fact to keep; merge: accept or reject; duplicate:
	// which chunk's document to keep (informational) or "keep both".
	Keep   string `json:"keep,omitempty"`
	Accept *bool  `json:"accept,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Skip records that the agent looked and chose to do nothing.
	Skip bool `json:"skip,omitempty"`
}

// Report is what submit returns: what would or did change, and what the
// omission check found.
type Report struct {
	ItemID  string   `json:"item_id"`
	Kind    string   `json:"kind"`
	Applied bool     `json:"applied"`
	DryRun  bool     `json:"dry_run"`
	PageURI string   `json:"page_uri,omitempty"`
	Diff    string   `json:"diff,omitempty"`
	Omitted []string `json:"omitted,omitempty"` // must_cover statements not reflected in the page
	// Unsupported lists page sentences that share little with any source
	// passage or recorded fact: the corruption check, at string level.
	Unsupported []string `json:"unsupported,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// Submit applies (or previews) the agent's result for an item. Pages are
// stored with is_inference = 1 and their sources; conflicts invalidate the
// loser under the trust rule; merges go through the resolver's decision;
// duplicates are recorded only (raw rows are never touched).
func Submit(ctx context.Context, store *kb.Store, itemID string, res Result, dryRun bool, actor, channel string) (*Report, error) {
	w, err := store.ReadWorkItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if w.State != "open" {
		return nil, fmt.Errorf("work item %s is already %s", itemID, w.State)
	}
	rep := &Report{ItemID: w.ID, Kind: w.Kind, DryRun: dryRun}
	if res.Skip {
		rep.Note = "skipped by the agent"
		if !dryRun {
			if err := store.CloseWorkItem(ctx, w.ID, "skipped", res, actor); err != nil {
				return nil, err
			}
			rep.Applied = true
		}
		return rep, nil
	}
	switch w.Kind {
	case KindPage, KindStale:
		var p PagePayload
		if err := json.Unmarshal(w.Payload, &p); err != nil {
			return nil, err
		}
		if strings.TrimSpace(res.Content) == "" {
			return nil, errors.New("a page item needs content")
		}
		title := res.Title
		if title == "" {
			title = p.Entity.Canonical
		}
		rep.Omitted = Omissions(res.Content, p.MustCover)
		var sourceTexts []string
		for _, c := range p.Chunks {
			sourceTexts = append(sourceTexts, c.Text)
		}
		rep.Unsupported = Unsupported(res.Content, append(sourceTexts, p.MustCover...))
		if len(rep.Unsupported) > 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d sentence(s) are not backed by any source passage or fact", len(rep.Unsupported)))
		}
		rep.Diff = Diff(p.Previous, res.Content)
		if cited := citedChunks(res.Content); len(cited) == 0 {
			rep.Warnings = append(rep.Warnings, "the page cites no memo://chunk address; sources are recorded from the work item")
		}
		if len(rep.Omitted) > 0 {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%d recorded fact(s) about %s are not reflected in the page", len(rep.Omitted), p.Entity.Canonical))
		}
		if dryRun {
			rep.Note = "dry run: nothing written"
			return rep, nil
		}
		var sources []int64
		for _, c := range p.Chunks {
			sources = append(sources, c.ID)
		}
		sources = append(sources, citedChunks(res.Content)...)
		page, err := store.WritePage(ctx, kb.PageInput{Namespace: w.Namespace, Kind: kb.PageKindEntity, SubjectID: p.Entity.ID, Title: title, Content: res.Content, Sources: dedupe(sources), Actor: actor, Channel: channel})
		if err != nil {
			return nil, err
		}
		rep.PageURI, rep.Applied = page.URI, true
		if err := store.CloseWorkItem(ctx, w.ID, "done", map[string]any{"page": page.URI, "omitted": rep.Omitted}, actor); err != nil {
			return nil, err
		}
	case KindConflict:
		var c ConflictPayload
		if err := json.Unmarshal(w.Payload, &c); err != nil {
			return nil, err
		}
		keep := strings.TrimPrefix(res.Keep, "memo://fact/")
		var winner, loser kb.Fact
		switch keep {
		case c.A.ID:
			winner, loser = c.A, c.B
		case c.B.ID:
			winner, loser = c.B, c.A
		default:
			return nil, fmt.Errorf("keep must name one of %s or %s", c.A.URI, c.B.URI)
		}
		rep.Diff = fmt.Sprintf("- %s  (%s)\n+ %s  (%s)\n", loser.Statement, loser.Trust, winner.Statement, winner.Trust)
		if !strings.Contains(c.Rule, winner.URI) {
			rep.Warnings = append(rep.Warnings, "this choice goes against the trust-then-recency rule: "+c.Rule)
		}
		if dryRun {
			rep.Note = "dry run: nothing invalidated"
			return rep, nil
		}
		if err := store.InvalidateFact(ctx, loser.ID, winner.ID, res.Reason, actor, channel); err != nil {
			return nil, err
		}
		rep.Applied = true
		if err := store.CloseWorkItem(ctx, w.ID, "done", map[string]any{"kept": winner.URI, "invalidated": loser.URI, "reason": res.Reason}, actor); err != nil {
			return nil, err
		}
	case KindMerge:
		var m MergePayload
		if err := json.Unmarshal(w.Payload, &m); err != nil {
			return nil, err
		}
		if res.Accept == nil {
			return nil, errors.New("a merge item needs accept: true or false")
		}
		if *res.Accept {
			rep.Diff = fmt.Sprintf("- %s\n+ %s (alias: %s)\n", m.Candidate.B.Canonical, m.Candidate.A.Canonical, m.Candidate.B.Canonical)
		} else {
			rep.Diff = "(kept apart)\n"
		}
		if dryRun {
			rep.Note = "dry run: no merge"
			return rep, nil
		}
		if err := store.DecideMerge(ctx, m.Candidate.ID, *res.Accept, actor, channel); err != nil {
			return nil, err
		}
		rep.Applied = true
		if err := store.CloseWorkItem(ctx, w.ID, "done", map[string]any{"accepted": *res.Accept, "reason": res.Reason}, actor); err != nil {
			return nil, err
		}
	case KindDuplicate:
		rep.Note = "duplicates are recorded, not deleted: raw rows are never touched (D-A); forget the redundant document deliberately if you want it gone"
		if dryRun {
			return rep, nil
		}
		rep.Applied = true
		if err := store.CloseWorkItem(ctx, w.ID, "done", res, actor); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown work item kind %q", w.Kind)
	}
	return rep, nil
}

// Omissions returns the must-cover statements whose content words are
// mostly absent from the page: the omission check, at string level, no
// judge model. A statement counts as covered when at least 75% of its
// content words (4+ letters, lowercased, stem-ish prefix of 5) appear.
func Omissions(page string, mustCover []string) []string {
	pageWords := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(page)) {
		pageWords[stem(w)] = true
	}
	var out []string
	for _, s := range mustCover {
		var total, hit int
		for _, w := range strings.Fields(strings.ToLower(s)) {
			w = strings.Trim(w, ".,;:()\"'")
			if len(w) < 4 {
				continue
			}
			total++
			if pageWords[stem(w)] {
				hit++
			}
		}
		if total > 0 && float64(hit)/float64(total) < 0.75 {
			out = append(out, s)
		}
	}
	return out
}

// Unsupported returns page sentences (8+ content words) of which fewer than
// half the content words appear in any single source text. It flags
// invented detail; it cannot judge meaning.
func Unsupported(page string, sources []string) []string {
	var srcWords []map[string]bool
	for _, s := range sources {
		m := map[string]bool{}
		for _, w := range strings.Fields(strings.ToLower(s)) {
			m[stem(w)] = true
		}
		srcWords = append(srcWords, m)
	}
	var out []string
	for _, sent := range splitSentences(page) {
		var words []string
		for _, w := range strings.Fields(strings.ToLower(sent)) {
			w = stem(w)
			if len(w) >= 4 && !strings.HasPrefix(w, "memo:") {
				words = append(words, w)
			}
		}
		if len(words) < 8 {
			continue
		}
		best := 0.0
		for _, m := range srcWords {
			hit := 0
			for _, w := range words {
				if m[w] {
					hit++
				}
			}
			if r := float64(hit) / float64(len(words)); r > best {
				best = r
			}
		}
		if best < 0.5 {
			out = append(out, strings.TrimSpace(sent))
		}
	}
	return out
}

func splitSentences(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' || r == '\n' {
			if seg := strings.TrimSpace(s[start:i]); seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	if seg := strings.TrimSpace(s[start:]); seg != "" {
		out = append(out, seg)
	}
	return out
}

func stem(w string) string {
	w = strings.Trim(w, ".,;:()\"'`*_-")
	if len(w) > 5 {
		return w[:5]
	}
	return w
}

// citedChunks finds memo://chunk/<n> addresses in a page.
func citedChunks(content string) []int64 {
	var out []int64
	for _, f := range strings.FieldsFunc(content, func(r rune) bool {
		return r == ' ' || r == '\n' || r == ')' || r == '(' || r == ']' || r == '[' || r == ','
	}) {
		var id int64
		if _, err := fmt.Sscanf(f, "memo://chunk/%d", &id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func dedupe(ids []int64) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Diff is a small line diff (LCS) in unified style: enough for a human to
// see what a rebuilt page changes before it lands.
func Diff(oldText, newText string) string {
	if oldText == "" {
		var sb strings.Builder
		for _, l := range strings.Split(strings.TrimRight(newText, "\n"), "\n") {
			sb.WriteString("+ " + l + "\n")
		}
		return sb.String()
	}
	a := strings.Split(strings.TrimRight(oldText, "\n"), "\n")
	b := strings.Split(strings.TrimRight(newText, "\n"), "\n")
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var sb strings.Builder
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			sb.WriteString("  " + a[i] + "\n")
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			sb.WriteString("- " + a[i] + "\n")
			i++
		default:
			sb.WriteString("+ " + b[j] + "\n")
			j++
		}
	}
	for ; i < n; i++ {
		sb.WriteString("- " + a[i] + "\n")
	}
	for ; j < m; j++ {
		sb.WriteString("+ " + b[j] + "\n")
	}
	return sb.String()
}
