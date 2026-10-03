package compact

import (
	"context"
	"fmt"
	"time"

	"github.com/kKEo/memory-find/internal/kb"
)

// Finding is one lint result with the address to look at.
type Finding struct {
	Kind    string `json:"kind"` // contradiction | orphan-entity | missing-page | stale-page | expired-fact
	URI     string `json:"uri"`
	Message string `json:"message"`
}

// Lint reads the knowledge base and reports what a librarian would flag:
// two live facts that disagree, entities no live passage mentions, entities
// with many mentions and no page, stale pages, and facts past their
// valid_to that are still live. It changes nothing.
func Lint(ctx context.Context, store *kb.Store, ns string, now time.Time) ([]Finding, error) {
	var out []Finding
	conflicts, err := Conflicts(ctx, store, ns)
	if err != nil {
		return nil, err
	}
	for _, c := range conflicts {
		out = append(out, Finding{Kind: "contradiction", URI: c.A.URI, Message: fmt.Sprintf("disagrees with %s about %s; rule: %s", c.B.URI, c.Subject.Canonical, c.Rule)})
	}
	orphans, err := store.OrphanEntities(ctx, ns)
	if err != nil {
		return nil, err
	}
	for _, e := range orphans {
		out = append(out, Finding{Kind: "orphan-entity", URI: e.URI, Message: e.Canonical + " has no live passage mentioning it (its sources were forgotten or revised away)"})
	}
	rows, err := store.EntitiesWithMentions(ctx, ns, MinMentionsForPage)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.PageID == "" {
			out = append(out, Finding{Kind: "missing-page", URI: r.Entity.URI, Message: fmt.Sprintf("%s is mentioned in %d live passages and has no page", r.Entity.Canonical, r.Entity.Mentions)})
		}
	}
	stale, err := store.ListPages(ctx, kb.PageFilter{Namespace: ns, StaleOnly: true})
	if err != nil {
		return nil, err
	}
	for _, p := range stale {
		out = append(out, Finding{Kind: "stale-page", URI: p.URI, Message: p.Title + ": " + p.StaleReason})
	}
	facts, err := store.ListFacts(ctx, kb.FactFilter{Namespace: ns, Limit: 1 << 20})
	if err != nil {
		return nil, err
	}
	for _, f := range facts {
		if f.ValidTo != nil && f.ValidTo.Before(now) && f.InvalidatedAt == nil {
			out = append(out, Finding{Kind: "expired-fact", URI: f.URI, Message: fmt.Sprintf("valid_to %s has passed and nothing supersedes it: %s", f.ValidTo.Format("2006-01-02"), f.Statement)})
		}
	}
	return out, nil
}
