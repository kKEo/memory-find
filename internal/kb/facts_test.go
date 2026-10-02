package kb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/embedding"
)

func rememberIn(ns, stmt, channel, trust string) RememberInput {
	return RememberInput{Namespace: ns, Statement: stmt, Origin: OriginUserSaid, Trust: trust, Actor: "t", Channel: channel}
}

func TestRememberSupersedeAsOfAndHistory(t *testing.T) {
	s, db := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	old, err := s.Remember(ctx, rememberIn("grpc", "Connect takes a single addr argument.", ChannelCLI, TrustUser))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(old.URI, "memo://fact/") || count(t, db, `SELECT COUNT(*) FROM fact_vecs`) != 1 || count(t, db, `SELECT COUNT(*) FROM facts_fts WHERE facts_fts MATCH 'addr'`) != 1 {
		t.Fatalf("fact not stored/indexed: %+v", old)
	}
	later := base.Add(30 * 24 * time.Hour)
	s.now = func() time.Time { return later }
	in := rememberIn("grpc", "Connect takes a context and an addr argument (changed in v1.9).", ChannelCLI, TrustUser)
	in.Supersedes = old.URI
	nu, err := s.Remember(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	live, _ := s.ListFacts(ctx, FactFilter{Namespace: "grpc"})
	if len(live) != 1 || live[0].ID != nu.ID {
		t.Fatalf("live facts: %+v", live)
	}
	then := base.Add(24 * time.Hour)
	asOf, _ := s.ListFacts(ctx, FactFilter{Namespace: "grpc", AsOf: &then})
	if len(asOf) != 1 || asOf[0].ID != old.ID {
		t.Fatalf("as_of should show the old fact: %+v", asOf)
	}
	hist, err := s.History(ctx, nu.URI)
	if err != nil || len(hist) != 2 || hist[0].URI != old.URI || hist[0].Live || !hist[1].Live || hist[0].RetiredAt == nil {
		t.Fatalf("history: %+v %v", hist, err)
	}
	if _, err := s.Remember(ctx, in); err == nil {
		t.Fatal("superseding an already superseded fact must fail")
	}
	if n := count(t, db, `SELECT COUNT(*) FROM audit WHERE op IN ('remember','supersede')`); n != 3 {
		t.Fatalf("audit rows: %d", n)
	}
}

func TestToolCannotRetireOrPromoteTrustedRecords(t *testing.T) {
	s, _ := openTestStore(t, nil)
	ctx := context.Background()
	userFact, _ := s.Remember(ctx, rememberIn("ns", "A user-trusted fact.", ChannelCLI, TrustUser))
	agentFact, _ := s.Remember(ctx, rememberIn("ns", "An agent-trusted fact.", ChannelTool, TrustAgent))

	var needs *ErrNeedsHuman
	err := s.Forget(ctx, ForgetInput{URI: userFact.URI, Reason: "wrong", Channel: ChannelTool})
	if !errors.As(err, &needs) || !strings.Contains(needs.Command, "memo-mcp forget") {
		t.Fatalf("tool forgetting a user fact must need a human: %v", err)
	}
	in := rememberIn("ns", "replacement", ChannelTool, TrustAgent)
	in.Supersedes = userFact.URI
	if _, err := s.Remember(ctx, in); !errors.As(err, &needs) {
		t.Fatalf("tool superseding a user fact must need a human: %v", err)
	}
	if err := s.SetTrust(ctx, agentFact.URI, TrustCurated, "t", ChannelTool); !errors.As(err, &needs) {
		t.Fatalf("tool promote must need a human: %v", err)
	}
	// The same operations from the CLI work and are audited with their channel.
	if err := s.Forget(ctx, ForgetInput{URI: userFact.URI, Reason: "wrong", Channel: ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTrust(ctx, agentFact.URI, TrustCurated, "t", ChannelCLI); err != nil {
		t.Fatal(err)
	}
	f, _ := s.ReadFact(ctx, agentFact.ID)
	if f.Trust != TrustCurated {
		t.Fatalf("promote did not apply: %+v", f)
	}
	// A tool call may forget its own agent-trust records... but this one is curated now.
	if err := s.Forget(ctx, ForgetInput{URI: agentFact.URI, Reason: "x", Channel: ChannelTool}); !errors.As(err, &needs) {
		t.Fatalf("curated fact forgotten by a tool: %v", err)
	}
}

func TestForgetDocumentRemovesItFromEveryIndex(t *testing.T) {
	s, db := openTestStore(t, embedding.NewHashEmbedder(32))
	ctx := context.Background()
	res, err := s.Ingest(ctx, docInput("grpc", "https://example.com/i", longDoc()))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Forget(ctx, ForgetInput{URI: res.URI, Reason: "superseded by the v2 guide", Channel: ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	if count(t, db, `SELECT COUNT(*) FROM chunks WHERE document_id = ?`, res.DocumentID) != 0 || count(t, db, `SELECT COUNT(*) FROM chunk_vecs`) != 0 || count(t, db, `SELECT COUNT(*) FROM chunks_fts WHERE chunks_fts MATCH 'interceptor'`) != 0 {
		t.Fatal("forgotten document still indexed")
	}
	_, _, err = s.Read(ctx, res.URI)
	var fg *Forgotten
	if !errors.As(err, &fg) || !strings.Contains(err.Error(), "superseded by the v2 guide") {
		t.Fatalf("read of a tombstone: %v", err)
	}
	var content string
	if err := db.QueryRow(`SELECT content FROM documents WHERE id = ?`, res.DocumentID).Scan(&content); err != nil || !strings.Contains(content, "Interceptors") {
		t.Fatal("content should be kept for audit without redact")
	}
	if err := s.Forget(ctx, ForgetInput{URI: res.URI, Reason: "pii", Redact: true, Channel: ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT content FROM documents WHERE id = ?`, res.DocumentID).Scan(&content); err != nil || content != "[redacted]" {
		t.Fatalf("redact did not clear content: %q", content)
	}
	hist, _ := s.History(ctx, res.URI)
	if len(hist) != 1 || hist[0].Live || hist[0].Forgotten == "" {
		t.Fatalf("history of a forgotten doc: %+v", hist)
	}
	// Live document count and status reflect it.
	st, _ := s.Status(ctx)
	if st.LiveDocuments != 0 {
		t.Fatalf("live documents after forget: %d", st.LiveDocuments)
	}
}

func TestRememberValidatesEvidence(t *testing.T) {
	s, _ := openTestStore(t, nil)
	ctx := context.Background()
	in := rememberIn("ns", "x", ChannelCLI, TrustUser)
	in.EvidenceURI = "memo://chunk/999"
	if _, err := s.Remember(ctx, in); err == nil {
		t.Fatal("missing evidence chunk accepted")
	}
	res, _ := s.Ingest(ctx, docInput("ns", "https://x/d", "# D\n\nSetCacheable sets ttlMs.\n"))
	_ = res
	in.EvidenceURI = "memo://chunk/1"
	f, err := s.Remember(ctx, in)
	if err != nil || f.EvidenceURI != "memo://chunk/1" {
		t.Fatalf("%v %+v", err, f)
	}
}
