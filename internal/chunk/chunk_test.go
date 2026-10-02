package chunk

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		s        string
		min, max int
	}{
		{"", 0, 0},
		{"hello world", 2, 4},
		{"net/http handler", 3, 7},
		{"日本語のテキスト", 8, 10},
		{`{"id":1,"ok":true}`, 5, 12},
	}
	for _, c := range cases {
		got := EstimateTokens(c.s)
		if got < c.min || got > c.max {
			t.Errorf("EstimateTokens(%q) = %d, want %d..%d", c.s, got, c.min, c.max)
		}
	}
}

func TestContextHeader(t *testing.T) {
	if got := ContextHeader("Guide", "Interceptors > Ordering", ""); got != "Guide > Interceptors > Ordering" {
		t.Errorf("got %q", got)
	}
	if got := ContextHeader("Guide", "", "release notes for v1.8"); got != "Guide — release notes for v1.8" {
		t.Errorf("got %q", got)
	}
	if got := ContextHeader("", "", ""); got != "" {
		t.Errorf("got %q", got)
	}
}

func para(words int, marker string) string {
	var sb strings.Builder
	for i := 0; i < words; i++ {
		if i%12 == 11 {
			sb.WriteString("filler. ")
		} else {
			sb.WriteString("filler ")
		}
	}
	sb.WriteString(marker + ".")
	return sb.String()
}

func TestSplitRespectsCapAndSections(t *testing.T) {
	md := "# Guide\n\nintro text here.\n\n## Interceptors\n\n" + para(300, "ALPHA") + "\n\n" + para(300, "BETA") +
		"\n\n### Ordering\n\n" + para(100, "GAMMA") + "\n\n## Errors\n\n" + para(50, "DELTA") + "\n"
	chunks := Split(md, Options{})
	if len(chunks) < 4 {
		t.Fatalf("expected several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Ord != i {
			t.Errorf("chunk %d has Ord %d", i, c.Ord)
		}
		if c.EstTokens > 400 {
			t.Errorf("chunk %d has %d est tokens > 400", i, c.EstTokens)
		}
	}
	find := func(marker string) Chunk {
		for _, c := range chunks {
			if strings.Contains(c.Text, marker) {
				return c
			}
		}
		t.Fatalf("marker %s not found in any chunk", marker)
		return Chunk{}
	}
	if s := find("GAMMA").SectionPath; s != "Guide > Interceptors > Ordering" {
		t.Errorf("GAMMA section = %q", s)
	}
	if s := find("DELTA").SectionPath; s != "Guide > Errors" {
		t.Errorf("DELTA section = %q", s)
	}
	if s := find("ALPHA").SectionPath; s != "Guide > Interceptors" {
		t.Errorf("ALPHA section = %q", s)
	}
	// Every word of the source survives somewhere (lossless split).
	joined := strings.Join(func() []string {
		var ts []string
		for _, c := range chunks {
			ts = append(ts, c.Text)
		}
		return ts
	}(), " ")
	for _, m := range []string{"ALPHA", "BETA", "GAMMA", "DELTA", "intro text here"} {
		if !strings.Contains(joined, m) {
			t.Errorf("%s lost", m)
		}
	}
}

func TestCodeFenceNeverSplit(t *testing.T) {
	var code strings.Builder
	code.WriteString("```go\n")
	for i := 0; i < 120; i++ {
		code.WriteString("func handler" + strings.Repeat("x", i%7) + "(w http.ResponseWriter, r *http.Request) { return }\n")
	}
	code.WriteString("```")
	md := "# API\n\nSome prose first.\n\n" + code.String() + "\n\nProse after.\n"
	chunks := Split(md, Options{})
	var codeChunks int
	for _, c := range chunks {
		if c.Lang == "go" {
			codeChunks++
			if !strings.HasPrefix(c.Text, "```go") || !strings.HasSuffix(c.Text, "```") {
				t.Errorf("code chunk not whole: %q…", c.Text[:20])
			}
		}
	}
	if codeChunks != 1 {
		t.Fatalf("expected exactly one code chunk, got %d", codeChunks)
	}
}

func TestOverlapCarriesTailSentence(t *testing.T) {
	md := "# T\n\n" + para(200, "FIRST-END") + "\n\n" + para(200, "SECOND-END") + "\n"
	chunks := Split(md, Options{Target: 150, Max: 400, Overlap: 20})
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	// The second chunk should begin with the tail of the first: its last
	// sentence appears in both, before any new material.
	first := sentences(chunks[0].Text)
	last := first[len(first)-1]
	idx := strings.Index(chunks[1].Text, last)
	if idx < 0 {
		t.Fatalf("chunk 2 does not repeat chunk 1's last sentence %q:\n%q", last, chunks[1].Text[:60])
	}
	if strings.Contains(chunks[1].Text[:idx], "SECOND") {
		t.Errorf("overlap is not at the start of chunk 2")
	}
}

func TestOversizedParagraphIsSplitBySentence(t *testing.T) {
	md := para(900, "END")
	chunks := Split(md, Options{Max: 400})
	if len(chunks) < 3 {
		t.Fatalf("expected ≥3 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.EstTokens > 400 {
			t.Errorf("chunk over cap: %d", c.EstTokens)
		}
	}
}

func TestHardSplitWithoutSpaces(t *testing.T) {
	md := strings.Repeat("abcdefghij", 500) // 5000 chars, one "word"
	chunks := Split(md, Options{Max: 100})
	if len(chunks) < 2 {
		t.Fatalf("expected a hard split, got %d chunk(s)", len(chunks))
	}
	total := 0
	for _, c := range chunks {
		total += len(strings.Join(strings.Fields(c.Text), ""))
	}
	if total != 5000 {
		t.Errorf("lost characters: %d", total)
	}
}

func TestEmptyAndWhitespace(t *testing.T) {
	for _, s := range []string{"", "   \n\n", "# only a heading\n"} {
		if got := Split(s, Options{}); len(got) != 0 {
			t.Errorf("Split(%q) = %d chunks, want 0", s, len(got))
		}
	}
}
