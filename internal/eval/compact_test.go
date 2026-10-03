package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/compact"
	"github.com/kKEo/memory-find/internal/kb"
)

// rawChecksum hashes every raw row (sources, documents, chunks, facts) so a
// test can prove compaction touched none of them (research D-A).
func rawChecksum(t *testing.T, store *kb.Store, includeFacts bool) string {
	t.Helper()
	h := sha256.New()
	queries := []string{
		`SELECT id, namespace, uri, content_hash, trust FROM sources ORDER BY id`,
		`SELECT id, source_id, revision, content, deleted_at, superseded_by FROM documents ORDER BY id`,
		`SELECT id, document_id, ord, text FROM chunks ORDER BY id`,
	}
	if includeFacts {
		queries = append(queries, `SELECT id, statement, trust, deleted_at, invalidated_at FROM facts ORDER BY id`)
	}
	for _, q := range queries {
		rows, err := store.DB().QueryContext(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(h, "%v\n", vals)
		}
		rows.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestCompactionSlices is the P8 eval: lint must find every planted defect
// (recall 1.0 on five kinds), the omission check must flag a page that
// leaves a recorded fact out and pass one that covers it, the corruption
// check must flag an invented sentence, conflict resolution must keep the
// trust rule, and the raw rows must be byte-identical before and after.
func TestCompactionSlices(t *testing.T) {
	ctx := context.Background()
	store, _, kbIDs := loadSuite(t)
	// Plant defects in the platform namespace.
	// 1. contradiction: two live user facts about Quorum Replication disagree.
	fa, err := store.Remember(ctx, kb.RememberInput{Namespace: "platform", Statement: "Quorum Replication needs three nodes.", About: []string{"Quorum Replication"}, Origin: kb.OriginAgentDerived, Trust: kb.TrustAgent, Actor: "eval", Channel: kb.ChannelTool})
	if err != nil {
		t.Fatal(err)
	}
	fb, err := store.Remember(ctx, kb.RememberInput{Namespace: "platform", Statement: "Quorum Replication needs five nodes.", About: []string{"Quorum Replication"}, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "eval", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	// 2. expired fact.
	past := time.Now().Add(-48 * time.Hour)
	fe, err := store.Remember(ctx, kb.RememberInput{Namespace: "platform", Statement: "The Mailer is in maintenance mode.", About: []string{"Mailer"}, ValidTo: &past, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "eval", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	// 3. orphan entity: a document whose entities exist only there, then forgotten.
	orph, err := store.Ingest(ctx, kb.IngestInput{Namespace: "platform", Content: "# Orphanage\n\nThe Zeppelin Dispatcher was decommissioned.\n", Source: kb.SourceInput{URI: "https://plat.example/zeppelin", Title: "Orphanage", Kind: kb.KindDoc, Origin: kb.OriginWeb}, Trust: kb.TrustUser, Actor: "eval", Channel: kb.ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Forget(ctx, kb.ForgetInput{URI: orph.URI, Reason: "planted", Actor: "eval", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	// 4. missing page: Billing Service has >= 3 live passages and no page (from the corpus).
	// 5. stale page: write a page for the Mailer from its chunk, then revise the Mailer doc.
	mailerDoc := kbIDs["plat-mailer"]
	var mailerChunk int64
	if err := store.DB().QueryRowContext(ctx, `SELECT id FROM chunks WHERE document_id = ? ORDER BY ord LIMIT 1`, mailerDoc).Scan(&mailerChunk); err != nil {
		t.Fatal(err)
	}
	mailerEnt, err := store.ReadEntity(ctx, "Mailer", "platform")
	if err != nil {
		t.Fatal(err)
	}
	stalePage, err := store.WritePage(ctx, kb.PageInput{Namespace: "platform", Kind: kb.PageKindEntity, SubjectID: mailerEnt.ID, Title: "Mailer", Content: "# Mailer\n\nSends invoice emails (memo://chunk/" + fmt.Sprint(mailerChunk) + ").\n", Sources: []int64{mailerChunk}, Actor: "eval", Channel: kb.ChannelTool})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ingest(ctx, kb.IngestInput{Namespace: "platform", Content: "# Mailer\n\nThe Mailer sends invoice emails for the Billing Service and retries bounces for three days now.\n", Source: kb.SourceInput{URI: "https://plat.example/mailer", Title: "Mailer", Kind: kb.KindDoc, Origin: kb.OriginWeb}, Trust: kb.TrustUser, Actor: "eval", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}

	before, beforeFacts := rawChecksum(t, store, false), rawChecksum(t, store, true)

	// Lint recall on the five planted kinds.
	findings, err := compact.Lint(ctx, store, "platform", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"contradiction": fa.URI, "expired-fact": fe.URI, "orphan-entity": "Zeppelin Dispatcher", "missing-page": "Billing Service", "stale-page": stalePage.URI}
	for kind, needle := range want {
		found := false
		for _, f := range findings {
			if f.Kind == kind && (strings.Contains(f.URI, needle) || strings.Contains(f.Message, needle)) {
				found = true
			}
		}
		if !found {
			t.Errorf("lint missed planted %s (%s); findings: %+v", kind, needle, findings)
		}
	}
	t.Logf("lint: %d findings over 5 planted defect kinds", len(findings))

	// Work items and the omission / corruption checks.
	items, err := compact.Generate(ctx, store, "platform", nil)
	if err != nil {
		t.Fatal(err)
	}
	var pageItem, conflictItem *kb.WorkItem
	for i := range items {
		w := &items[i]
		switch {
		case w.Kind == compact.KindPage && strings.Contains(string(w.Payload), `"canonical":"Billing Service"`):
			pageItem = w
		case w.Kind == compact.KindConflict && strings.Contains(string(w.Payload), fa.ID) && strings.Contains(string(w.Payload), fb.ID):
			conflictItem = w
		}
	}
	if pageItem == nil || conflictItem == nil {
		t.Fatalf("items: page=%v conflict=%v in %d items", pageItem != nil, conflictItem != nil, len(items))
	}
	if _, err := store.Remember(ctx, kb.RememberInput{Namespace: "platform", Statement: "The Billing Service closes the books on the first business day.", About: []string{"Billing Service"}, Origin: kb.OriginUserSaid, Trust: kb.TrustUser, Actor: "eval", Channel: kb.ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	items, _ = compact.Generate(ctx, store, "platform", []string{compact.KindPage}) // refresh payload with the new fact
	for i := range items {
		if strings.Contains(string(items[i].Payload), `"canonical":"Billing Service"`) {
			pageItem = &items[i]
		}
	}
	incomplete := "# Billing Service\n\nIssues invoices once a day and persists invoice lines in the Ledger Store (memo://chunk/1). Pages finance when the run is late.\n"
	rep, err := compact.Submit(ctx, store, pageItem.ID, compact.Result{Content: incomplete}, true, "eval", kb.ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Omitted) != 1 || !strings.Contains(rep.Omitted[0], "closes the books") {
		t.Fatalf("omission check should flag the uncovered fact: %+v", rep)
	}
	invented := incomplete + "\nThe Billing Service is written in Fortran and runs on a mainframe in Reykjavik with twelve redundant generators.\nIt closes the books on the first business day.\n"
	rep, err = compact.Submit(ctx, store, pageItem.ID, compact.Result{Content: invented}, true, "eval", kb.ChannelTool)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Omitted) != 0 || len(rep.Unsupported) != 1 || !strings.Contains(rep.Unsupported[0], "Fortran") {
		t.Fatalf("corruption check should flag the invented sentence only: %+v", rep)
	}
	good := incomplete + "\nIt closes the books on the first business day.\n"
	rep, err = compact.Submit(ctx, store, pageItem.ID, compact.Result{Content: good}, false, "eval", kb.ChannelTool)
	if err != nil || !rep.Applied || len(rep.Omitted) != 0 || len(rep.Unsupported) != 0 {
		t.Fatalf("good page: %+v %v", rep, err)
	}

	// Conflict: the tool may not keep the agent fact over the user fact.
	if _, err := compact.Submit(ctx, store, conflictItem.ID, compact.Result{Keep: fa.URI, Reason: "wrong"}, false, "eval", kb.ChannelTool); err == nil {
		t.Fatal("a tool invalidated a user fact")
	}
	rep, err = compact.Submit(ctx, store, conflictItem.ID, compact.Result{Keep: fb.URI, Reason: "user said five"}, false, "eval", kb.ChannelTool)
	if err != nil || !rep.Applied {
		t.Fatalf("resolve: %+v %v", rep, err)
	}
	loser, _ := store.ReadFact(ctx, fa.ID)
	if loser.InvalidatedAt == nil || loser.DeletedAt != nil {
		t.Fatalf("loser must be invalidated, not deleted: %+v", loser)
	}

	// Raw rows: sources, documents and chunks are byte-identical after a
	// page write, two dry runs and a conflict resolution; the only fact row
	// that changed is the one the agent asked to invalidate.
	if after := rawChecksum(t, store, false); after != before {
		t.Fatal("compaction changed a raw source, document or chunk row")
	}
	if rawChecksum(t, store, true) == beforeFacts {
		t.Fatal("the resolved conflict should have invalidated one fact")
	}
	var changed int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM facts WHERE invalidated_at IS NOT NULL AND superseded_by = ?`, fb.ID).Scan(&changed); err != nil || changed != 1 {
		t.Fatalf("expected exactly one fact invalidated by the decision, got %d (%v)", changed, err)
	}
	var docs, chunks int
	_ = store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`).Scan(&docs)
	_ = store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&chunks)
	t.Logf("raw rows unchanged: %d documents, %d chunks; one fact invalidated by decision", docs, chunks)
}
