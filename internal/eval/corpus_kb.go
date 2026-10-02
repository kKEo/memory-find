package eval

import (
	"fmt"
	"strings"

	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/retrieve"
)

// FixtureDoc is a knowledge-base fixture: a document with its provenance.
// The journal-era FixtureEntry converts into one (see Load).
type FixtureDoc struct {
	Key       string
	Namespace string
	Kind      string
	Title     string
	URI       string
	Library   string
	Version   string
	Tags      []string
	Content   string
	AgeDays   int
}

// quorumFunctions are the fictional library's API pages: each has a unique
// identifier and error code, and heavily shared template prose, so the big
// namespace can drown a small one unless ranking is right.
var quorumFunctions = []string{"RotateKeys", "OpenSession", "CloseSession", "Subscribe", "Publish", "Acknowledge", "Retry", "Backoff", "Checkpoint", "Restore",
	"Snapshot", "Compact", "Verify", "Sign", "Encrypt", "Decrypt", "Hash", "Compare", "Merge", "Split"}

// CorpusKB returns the knowledge-base fixture set (eval v2): a 300-document
// library namespace, a 5-document unrelated namespace, a mini library in two
// versions, identifier and error-code pages, long documents whose unique
// vocabulary sits only in the tail, aged notes, and no-match decoys.
func CorpusKB() []FixtureDoc {
	var docs []FixtureDoc
	// --- biglib: 300 generated API pages about "quorum" ---
	for i := 0; i < 300; i++ {
		fn := quorumFunctions[i%len(quorumFunctions)]
		variant := i / len(quorumFunctions) // 0..14
		name := fmt.Sprintf("quorum.%s%d", fn, variant)
		code := fmt.Sprintf("QRM-%04d", 1000+i)
		extra := ""
		if i == 0 {
			extra = "\n## Hardware backend\n\nOnly this call supports the hardware security module backend; keys never leave the HSM.\n"
		}
		docs = append(docs, FixtureDoc{
			Key: fmt.Sprintf("biglib-%03d", i), Namespace: "quorum", Kind: kb.KindDoc, Title: name,
			URI: fmt.Sprintf("https://quorum.example/api/%s", strings.ToLower(name)), Library: "example/quorum", Version: "v2.3.0",
			Content: fmt.Sprintf("# %s\n\n`%s` is part of the quorum client API. It takes a context and the client options and returns an error when the cluster is unavailable.\n\n## Errors\n\nReturns %s when the operation times out. Retry with exponential backoff and check the cluster health endpoint.\n\n## Example\n\n```go\nif err := c.%s%d(ctx, opts); err != nil {\n    return fmt.Errorf(\"%s: %%w\", err)\n}\n```\n%s", name, name, code, fn, variant, code, extra),
		})
	}
	// --- tiny namespace: 5 recipes, nothing to do with quorum ---
	recipes := []struct{ key, title, body string }{
		{"tiny-sourdough", "Sourdough starter", "Feed the sourdough starter with equal parts flour and water every twelve hours until it doubles in volume."},
		{"tiny-ramen", "Ramen broth", "Simmer pork bones for twelve hours; skim the fat; season the broth with tare just before serving the ramen."},
		{"tiny-kimchi", "Kimchi fermentation", "Salt the napa cabbage, rinse, then ferment with gochugaru paste at room temperature for three days."},
		{"tiny-espresso", "Espresso dialing", "Dial in espresso by adjusting grind size until eighteen grams yield thirty-six grams in about twenty-eight seconds."},
		{"tiny-pickles", "Quick pickles", "Quick pickles: cucumbers in hot brine of vinegar, water, salt and dill; refrigerate overnight."},
	}
	for _, r := range recipes {
		docs = append(docs, FixtureDoc{Key: r.key, Namespace: "kitchen", Kind: kb.KindNote, Title: r.title, Content: "# " + r.title + "\n\n" + r.body + "\n"})
	}
	// --- widgets: one page, two versions (same URI → revisions) ---
	docs = append(docs,
		FixtureDoc{Key: "widgets-connect-v17", Namespace: "widgets", Kind: kb.KindDoc, Title: "Client.Connect", URI: "https://widgets.example/docs/client-connect", Library: "example/widgets", Version: "v1.7.0",
			Content: "# Client.Connect\n\n`Connect(addr string) (*Client, error)` dials the widgets server at addr and returns a connected client. Connection timeouts are fixed at ten seconds in v1.7.\n"},
		FixtureDoc{Key: "widgets-connect-v18", Namespace: "widgets", Kind: kb.KindDoc, Title: "Client.Connect", URI: "https://widgets.example/docs/client-connect", Library: "example/widgets", Version: "v1.8.0",
			Content: "# Client.Connect\n\n`Connect(ctx context.Context, addr string) (*Client, error)` dials the widgets server at addr; the context controls the dial timeout. The fixed ten-second timeout from v1.7 is gone.\n"},
	)
	// --- long documents: generic filler, unique marker only in the tail ---
	for i, marker := range []string{"The pager escalation policy pages the secondary after nine minutes of silence.", "Nightly certificate rotation renews the signing key before the token expiry window.", "The archival job compacts cold partitions into parquet files every Sunday."} {
		var sb strings.Builder
		fmt.Fprintf(&sb, "# Operations handbook %d\n\n", i+1)
		for p := 0; p < 18; p++ {
			fmt.Fprintf(&sb, "## Chapter %d\n\n%s\n\n", p+1, strings.Repeat("General notes about meetings, participants, timelines and follow-up actions that were discussed at length during the review. ", 4))
		}
		fmt.Fprintf(&sb, "## Appendix\n\n%s\n", marker)
		docs = append(docs, FixtureDoc{Key: fmt.Sprintf("long-tail-%d", i+1), Namespace: "ops", Kind: kb.KindDoc, Title: fmt.Sprintf("Operations handbook %d", i+1), URI: fmt.Sprintf("https://ops.example/handbook/%d", i+1), Content: sb.String()})
	}
	// --- aged notes on one topic: recency should order them ---
	for i, age := range []int{0, 30, 365, 730} {
		docs = append(docs, FixtureDoc{Key: fmt.Sprintf("standup-note-%d", i), Namespace: "personal", Kind: kb.KindNote, Title: fmt.Sprintf("Standup note %d", i), AgeDays: age,
			Content: fmt.Sprintf("# Standup note %d\n\nStandup today: the release branch is frozen, the rollout plan is unchanged, and the on-call handover happens at ten.\n", i)})
	}
	return docs
}

// QueriesKB returns the eval v2 labelled queries over CorpusKB.
func QueriesKB() []Query {
	return []Query{
		// cross-namespace: a tiny namespace must not drown in the big one
		{ID: "xns-sourdough", Category: "cross-namespace", Query: "how do I feed a sourdough starter", Relevant: []string{"tiny-sourdough"}, Irrelevant: []string{"biglib-000"}},
		{ID: "xns-espresso", Category: "cross-namespace", Query: "dialing in espresso grind size yield", Relevant: []string{"tiny-espresso"}},
		{ID: "xns-kimchi", Category: "cross-namespace", Query: "ferment cabbage with gochugaru", Relevant: []string{"tiny-kimchi"}},
		// exact identifiers and error codes inside 300 near-identical pages
		{ID: "exact-rotatekeys3", Category: "exact", Query: "quorum.RotateKeys3", Relevant: []string{"biglib-060"}},
		{ID: "exact-qrm-1234", Category: "exact", Query: "QRM-1234", Relevant: []string{"biglib-234"}},
		{ID: "exact-publish7", Category: "exact", Query: "what does quorum.Publish7 return on timeout", Relevant: []string{"biglib-144"}},
		{ID: "exact-qrm-1005-scoped", Category: "exact", Query: "QRM-1005 timeout", Scope: retrieve.Scope{Library: "example/quorum"}, Relevant: []string{"biglib-005"}},
		// version-pinned: same URI, two revisions
		{ID: "version-connect-v17", Category: "version-pinned", Query: "Client.Connect timeout", Scope: retrieve.Scope{Version: "v1.7.0"}, Relevant: []string{"widgets-connect-v17"}, Irrelevant: []string{"widgets-connect-v18"}},
		{ID: "version-connect-latest", Category: "version-pinned", Query: "Client.Connect timeout", Scope: retrieve.Scope{Library: "example/widgets"}, Relevant: []string{"widgets-connect-v18"}, Irrelevant: []string{"widgets-connect-v17"}},
		// long documents: the answer is only in the tail
		{ID: "longtail-pager", Category: "long-document", Query: "pager escalation secondary minutes of silence", Relevant: []string{"long-tail-1"}},
		{ID: "longtail-certificate", Category: "long-document", Query: "certificate rotation signing key token expiry", Relevant: []string{"long-tail-2"}},
		{ID: "longtail-parquet", Category: "long-document", Query: "archival job parquet cold partitions", Relevant: []string{"long-tail-3"}},
		// recency: newest note first
		{ID: "recency-standup", Category: "recency", Query: "standup release branch frozen rollout handover", Relevant: []string{"standup-note-0"}, Irrelevant: []string{"standup-note-3"}},
		// long-tail prose: words that appear in exactly one of 300 near-identical pages
		{ID: "longtail-hsm-backend", Category: "long-tail", Query: "hardware security module backend keys", Relevant: []string{"biglib-000"}, Irrelevant: []string{"biglib-001"}},
		// abstention
		{ID: "abstain-astronomy", Category: "abstention", Query: "exoplanet atmospheric spectroscopy telescope", Irrelevant: []string{"biglib-000", "tiny-ramen"}},
		{ID: "abstain-opera", Category: "abstention", Query: "baritone aria libretto overture", Irrelevant: []string{"long-tail-1"}},
		// multi-hop (planted now, honest until P7): both versions of Connect
		{ID: "multihop-connect-change", Category: "multi-hop", Query: "how did Client.Connect change between v1.7 and v1.8", Scope: retrieve.Scope{Library: "example/widgets", Version: ""}, Relevant: []string{"widgets-connect-v18"}},
		// paraphrase: real model only (the hash embedder has no synonymy)
		{ID: "para-starter-feeding", Category: "paraphrase", RealModelOnly: true, Query: "keeping a bread culture alive", Relevant: []string{"tiny-sourdough"}},
		{ID: "para-coffee", Category: "paraphrase", RealModelOnly: true, Query: "getting a good shot of coffee", Relevant: []string{"tiny-espresso"}},
	}
}
