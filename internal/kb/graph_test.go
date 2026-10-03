package kb

import (
	"context"
	"strings"
	"testing"
)

// Ingest links rung-1 mentions; a second document sharing an identifier
// resolves to the same entity; explore walks the shared chunks; a near name
// becomes a merge candidate, never an automatic merge; forgetting hides the
// mentions.
func TestGraphLinksResolvesAndExplores(t *testing.T) {
	s, _ := openTestStore(t, nil)
	ctx := context.Background()
	a, err := s.Ingest(ctx, IngestInput{Namespace: "widgets", Content: "# Client.Connect\n\n`Connect(ctx, addr)` dials the Widgets Server. It is used by the Pipeline Scheduler.\n",
		Source: SourceInput{URI: "https://w.example/connect", Title: "Client.Connect", Kind: KindDoc, Origin: OriginWeb}, Trust: TrustUser, Actor: "t", Channel: ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	if a.Entities < 3 {
		t.Fatalf("expected entities linked, got %d", a.Entities)
	}
	b, err := s.Ingest(ctx, IngestInput{Namespace: "widgets", Content: "# Pipeline Scheduler\n\nThe Pipeline Scheduler retries client.connect on ERR_CONN_RESET and reports to the Quorum Store.\n",
		Source:   SourceInput{URI: "https://w.example/scheduler", Title: "Pipeline Scheduler", Kind: KindDoc, Origin: OriginWeb},
		Entities: []EntityInput{{Name: "Quorum Store", Type: "service"}}, Relations: []RelationInput{{From: "Pipeline Scheduler", To: "Quorum Store", Rel: "reports_to"}},
		Trust: TrustUser, Actor: "t", Channel: ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	_ = b
	ents, err := s.FindEntities(ctx, []string{"widgets"}, "how does Client.Connect relate to the Pipeline Scheduler")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int{}
	for _, e := range ents {
		names[e.Canonical] = e.Mentions
	}
	if names["Client.Connect"] < 2 || names["Pipeline Scheduler"] < 2 {
		t.Fatalf("entities should be shared across both documents: %+v", names)
	}
	ex, err := s.Explore(ctx, "Pipeline Scheduler", "widgets", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawConnect, sawQuorum bool
	for _, n := range ex.Neighbours {
		if n.Entity.Canonical == "Client.Connect" && len(n.Evidence) > 0 {
			sawConnect = true
		}
		if n.Entity.Canonical == "Quorum Store" && n.Rel == "reports_to" {
			sawQuorum = true
		}
	}
	if !sawConnect || !sawQuorum {
		t.Fatalf("explore neighbours: %+v", ex.Neighbours)
	}
	st, err := s.Status(ctx)
	if err != nil || st.Graph.Entities == 0 || st.Graph.Mentions == 0 || st.Graph.Edges != 1 {
		t.Fatalf("graph stats: %+v %v", st.Graph, err)
	}
	// Near name → candidate, not merge; accepting makes it an alias.
	if _, err := s.Ingest(ctx, IngestInput{Namespace: "widgets", Content: "# Notes\n\nThe Pipeline Schedulers team owns rollout.\n", Source: SourceInput{Title: "Notes", Kind: KindNote, Origin: OriginUserSaid}, Trust: TrustUser, Actor: "t", Channel: ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	cands, err := s.MergeCandidates(ctx, "open")
	if err != nil || len(cands) == 0 {
		t.Fatalf("expected an open merge candidate: %v %v", cands, err)
	}
	if err := s.DecideMerge(ctx, cands[0].ID, true, "t", ChannelCLI); err != nil {
		t.Fatal(err)
	}
	e, err := s.ReadEntity(ctx, "Pipeline Schedulers", "widgets")
	if err != nil || e.Canonical == "Pipeline Schedulers" || len(e.Aliases) == 0 {
		t.Fatalf("after merge the alias should resolve to the canonical entity: %+v %v", e, err)
	}
	// Forgetting a document removes its chunks from the live mention graph.
	if err := s.Forget(ctx, ForgetInput{URI: a.URI, Reason: "test", Actor: "t", Channel: ChannelCLI}); err != nil {
		t.Fatal(err)
	}
	edges, err := s.MentionGraph(ctx, "widgets", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if e.ChunkID == 1 {
			t.Fatal("forgotten document's chunk still in the mention graph")
		}
	}
	// Fact subjects link to entities.
	f, err := s.Remember(ctx, RememberInput{Namespace: "widgets", Statement: "The Pipeline Scheduler runs nightly.", About: []string{"Pipeline Scheduler"}, Origin: OriginUserSaid, Trust: TrustUser, Actor: "t", Channel: ChannelCLI})
	if err != nil {
		t.Fatal(err)
	}
	var subj string
	if err := s.db.QueryRowContext(ctx, `SELECT subject_entity_id FROM facts WHERE id = ?`, f.ID).Scan(&subj); err != nil || !strings.HasPrefix(subj, "01") {
		t.Fatalf("fact subject not linked: %q %v", subj, err)
	}
}
