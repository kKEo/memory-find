package compact

import (
	"context"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/kb"
)

func openStore(t *testing.T) *kb.Store {
	t.Helper()
	db, err := kb.Open(context.Background(), t.TempDir(), "c", kb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return kb.NewStore(db, embedding.NewHashEmbedder(64))
}

func ingest(t *testing.T, s *kb.Store, ns, uri, title, body string) *kb.IngestResult {
	t.Helper()
	r, err := s.Ingest(context.Background(), kb.IngestInput{Namespace: ns, Content: "# " + title + "\n\n" + body + "\n",
		Source: kb.SourceInput{URI: uri, Title: title, Kind: kb.KindDoc, Origin: kb.OriginWeb}, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The whole loop: three passages about one entity → a page item → submit
// (dry run shows the diff and the omission) → page stored with sources and
// is_inference → revising a source marks it stale → lint lists it → a stale
// item rebuilds it. Raw rows are never touched.
func TestPageLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	ingest(t, s, "plat", "https://p/billing", "Billing Service", "The Billing Service issues invoices daily and writes to the Ledger Store.")
	ingest(t, s, "plat", "https://p/oncall", "Billing on-call", "The Billing Service pages finance when the invoice run is late.")
	ingest(t, s, "plat", "https://p/mailer", "Mailer", "The Mailer sends invoice emails for the Billing Service.")
	if _, err := s.Remember(ctx, kb.RememberInput{Namespace: "plat", Statement: "The Billing Service runs the invoice job at 02:00 UTC.", About: []string{"Billing Service"}, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	items, err := Generate(ctx, s, "plat", nil)
	if err != nil {
		t.Fatal(err)
	}
	var item *kb.WorkItem
	for i := range items {
		if items[i].Kind == KindPage && strings.Contains(string(items[i].Payload), "Billing Service") {
			item = &items[i]
		}
	}
	if item == nil {
		t.Fatalf("no page item for Billing Service: %+v", items)
	}
	// Dry run: an incomplete page is reported, nothing written.
	rep, err := Submit(ctx, s, item.ID, Result{Content: "# Billing Service\n\nIssues invoices daily; writes to the Ledger Store (memo://chunk/1).\n"}, true, "agent", kb.ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied || len(rep.Omitted) != 1 || !strings.Contains(rep.Diff, "+ # Billing Service") {
		t.Fatalf("dry run: %+v", rep)
	}
	if pages, _ := s.ListPages(ctx, kb.PageFilter{Namespace: "plat"}); len(pages) != 0 {
		t.Fatal("dry run wrote a page")
	}
	// Real submit with the fact covered.
	content := "# Billing Service\n\nIssues invoices daily and writes to the Ledger Store (memo://chunk/1). Pages finance when the run is late (memo://chunk/2). The invoice job runs at 02:00 UTC.\n"
	rep, err = Submit(ctx, s, item.ID, Result{Content: content}, false, "agent", kb.ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Applied || len(rep.Omitted) != 0 || rep.PageURI == "" {
		t.Fatalf("submit: %+v", rep)
	}
	page, err := s.ReadPage(ctx, strings.TrimPrefix(rep.PageURI, "memo://page/"))
	if err != nil {
		t.Fatal(err)
	}
	if !page.IsInference || page.Trust != kb.TrustAgent || len(page.Sources) < 3 || page.Stale {
		t.Fatalf("page: %+v", page)
	}
	if w, _ := s.ReadWorkItem(ctx, item.ID); w.State != "done" {
		t.Fatalf("item state %s", w.State)
	}
	// Nothing proposed again while nothing grew (recurrence trigger).
	again, _ := Generate(ctx, s, "plat", []string{KindPage, KindStale})
	for _, w := range again {
		if strings.Contains(string(w.Payload), "Billing Service") {
			t.Fatalf("page proposed again without growth: %+v", w)
		}
	}
	// Revise a source → stale → lint → stale item → rebuild.
	ingest(t, s, "plat", "https://p/billing", "Billing Service", "The Billing Service issues invoices hourly now and writes to the Ledger Store.")
	page, _ = s.ReadPage(ctx, page.ID)
	if !page.Stale || !strings.Contains(page.StaleReason, "source revised") {
		t.Fatalf("page should be stale after revision: %+v", page)
	}
	findings, err := Lint(ctx, s, "plat", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var sawStale bool
	for _, f := range findings {
		if f.Kind == "stale-page" && f.URI == page.URI {
			sawStale = true
		}
	}
	if !sawStale {
		t.Fatalf("lint missed the stale page: %+v", findings)
	}
	items, _ = Generate(ctx, s, "plat", []string{KindStale})
	if len(items) != 1 || items[0].Kind != KindStale {
		t.Fatalf("stale item: %+v", items)
	}
	rep, err = Submit(ctx, s, items[0].ID, Result{Content: strings.Replace(content, "daily", "hourly", 1)}, false, "agent", kb.ChannelTool)
	if err != nil || !rep.Applied || !strings.Contains(rep.Diff, "- Issues invoices daily") {
		t.Fatalf("rebuild: %+v %v", rep, err)
	}
	page, _ = s.ReadPage(ctx, page.ID)
	if page.Stale || page.BuiltFromRev != 2 {
		t.Fatalf("rebuilt page: %+v", page)
	}
	st, _ := s.Status(ctx)
	if st.Pages.Pages != 1 || st.Pages.StalePages != 0 {
		t.Fatalf("status: %+v", st.Pages)
	}
}

// Conflicts: two live facts about one subject disagree; the rule names the
// winner; submit invalidates the loser (kept as history), never deletes.
func TestConflictResolution(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a, err := s.Remember(ctx, kb.RememberInput{Namespace: "plat", Statement: "Quorum Replication needs three nodes.", About: []string{"Quorum Replication"}, Origin: kb.OriginAgentDerived, Trust: kb.TrustAgent, Actor: "t", Channel: kb.ChannelTool})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Remember(ctx, kb.RememberInput{Namespace: "plat", Statement: "Quorum Replication needs five nodes.", About: []string{"Quorum Replication"}, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "t", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	items, err := Generate(ctx, s, "plat", []string{KindConflict})
	if err != nil || len(items) != 1 {
		t.Fatalf("conflict items: %+v %v", items, err)
	}
	if !strings.Contains(string(items[0].Payload), "keep "+b.URI) {
		t.Fatalf("rule should keep the user fact: %s", items[0].Payload)
	}
	// Against the rule: a tool may not invalidate a user fact.
	if _, err := Submit(ctx, s, items[0].ID, Result{Keep: a.URI, Reason: "prefer the old one"}, false, "agent", kb.ChannelTool); err == nil {
		t.Fatal("a tool invalidated a more-trusted fact")
	}
	rep, err := Submit(ctx, s, items[0].ID, Result{Keep: b.URI, Reason: "user said five"}, false, "agent", kb.ChannelTool)
	if err != nil || !rep.Applied {
		t.Fatalf("resolve: %+v %v", rep, err)
	}
	fa, _ := s.ReadFact(ctx, a.ID)
	if fa.InvalidatedAt == nil || fa.SupersededBy != b.ID {
		t.Fatalf("loser not invalidated: %+v", fa)
	}
	if f, _ := Lint(ctx, s, "plat", time.Now()); len(f) != 0 {
		t.Fatalf("lint after resolution: %+v", f)
	}
}

func TestDuplicatesAndOmissions(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	body := "Retries use exponential backoff with full jitter and a cap of thirty seconds; the policy is configured per method in the service config and applies to idempotent calls only."
	ingest(t, s, "d", "https://d/1", "Retry A", body)
	ingest(t, s, "d", "https://d/2", "Retry B", body+" See also the deadline section.")
	ingest(t, s, "d", "https://d/3", "Other", "Deadlines propagate through the context and are enforced by the server before the handler runs.")
	dups, err := Duplicates(ctx, s, "d")
	if err != nil || len(dups) != 1 || dups[0].Jaccard < DuplicateJaccard {
		t.Fatalf("duplicates: %+v %v", dups, err)
	}
	if om := Omissions("The Billing Service runs its invoice job at 02:00 UTC every day.", []string{"The Billing Service runs the invoice job at 02:00 UTC.", "Unrelated statement about kimchi fermentation."}); len(om) != 1 || !strings.Contains(om[0], "kimchi") {
		t.Fatalf("omissions: %v", om)
	}
	if d := Diff("a\nb\nc", "a\nx\nc"); d != "  a\n- b\n+ x\n  c\n" {
		t.Fatalf("diff:\n%s", d)
	}
}
