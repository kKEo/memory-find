package retrieve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/kb"
)

func TestProfilesHaveDerivations(t *testing.T) {
	ps := Profiles()
	if len(ps) < 6 {
		t.Fatalf("expected at least 6 profiles, got %d", len(ps))
	}
	for _, p := range ps {
		if p.Derivation == "" {
			t.Errorf("profile %s has no derivation", p.Name)
		}
		if !strings.Contains(Describe(p), "rrf_k") {
			t.Errorf("Describe(%s) incomplete", p.Name)
		}
	}
	if _, err := Lookup("nope"); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestLoadOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := LoadOverrides(path); err != nil {
		t.Fatalf("missing file must be a no-op: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"default": {"rrf_k": 30}, "mine": {"base": "code", "weights": {"exact": 0.9}, "fusion": "minmax"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadOverrides(path); err != nil {
		t.Fatal(err)
	}
	d, _ := Lookup("default")
	if d.RRFK != 30 {
		t.Errorf("override not applied: %+v", d)
	}
	m, err := Lookup("mine")
	if err != nil || m.Weights[ArmExact] != 0.9 || m.Weights[ArmKeyword] != 0.4 || m.Fusion != FusionMinMax || m.RecencyKinds != nil {
		t.Fatalf("derived profile: %+v %v", m, err)
	}
	// restore
	profiles["default"] = Default
	delete(profiles, "mine")
	if err := os.WriteFile(path, []byte(`{"x": {"fusion": "magic"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadOverrides(path); err == nil {
		t.Fatal("bad fusion accepted")
	}
}

func TestAblationProfilesRestrictArms(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	kw, _ := Lookup("keyword-only")
	resp, err := New(s, kw, false).Search(context.Background(), Request{Query: "interceptor order", ResponseFormat: FormatExplain})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Trace.ModeResolved != "keyword" || len(resp.Results) == 0 || resp.Results[0].Relevance != nil {
		t.Fatalf("keyword-only: %+v", resp.Trace)
	}
}

func TestMinMaxFusionExplainsTruthfully(t *testing.T) {
	s := newStore(t, embedding.NewHashEmbedder(64))
	seed(t, s)
	mm, _ := Lookup("minmax")
	resp, err := New(s, mm, false).Search(context.Background(), Request{Query: "auth interceptor order", ResponseFormat: FormatExplain})
	if err != nil || len(resp.Results) == 0 {
		t.Fatal(err)
	}
	top := resp.Results[0]
	var sum float64
	for _, a := range top.Why.Arms {
		sum += a.Contribution
		if a.Contribution < 0 || a.Contribution > 0.5+1e-9 {
			t.Errorf("minmax contribution out of range: %+v", a)
		}
	}
	if d := sum - top.Why.Fused; d > 1e-9 || d < -1e-9 {
		t.Errorf("contributions %.4f != fused %.4f", sum, top.Why.Fused)
	}
	if _, _, err := kb.ParseURI(top.URI); err != nil {
		t.Error("bad uri")
	}
}
