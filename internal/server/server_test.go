package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
	"github.com/kmaziarz/memo-mcp/internal/journal"
	"github.com/kmaziarz/memo-mcp/internal/search"
)

var updateGolden = flag.Bool("update", false, "update golden files")

// newTestSession wires up a full Server (journal + search + embedder)
// behind an in-memory MCP transport, so tests exercise the real tool
// registration, schema inference, and handler wiring end to end rather
// than calling the Manager/Service methods directly. It uses a real
// temp-file database (not :memory:) so the WAL pragmas and migration path
// from journal.OpenDB are exercised too.
func newTestSession(t *testing.T) (*mcp.ClientSession, *sql.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	emb := embedding.NewHashEmbedder(384)
	srv := New(journal.NewManager(db, emb), search.NewService(db, emb))

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	// The server must be connected before the client: the client
	// initializes the MCP session as part of connecting.
	if _, err := srv.mcp.Connect(context.Background(), serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	cs, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })

	return cs, db
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// TestListToolsGolden pins the exact protocol surface the server exposes:
// tool names, descriptions, and inferred JSON schemas. It exists to catch
// exactly the kind of thing that's easy to miss in review otherwise — the
// jsonschema:"required,..." tag bug (see git history) leaked the literal
// string "required," into a tool description, and nothing but a snapshot
// of the actual generated schema would have caught that.
//
// Run with -update to regenerate testdata/tools.golden.json after an
// intentional change to the tool surface.
func TestListToolsGolden(t *testing.T) {
	cs, _ := newTestSession(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "tools.golden.json")

	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("tools/list output does not match %s (run with -update to review/accept the diff)\n--- got ---\n%s", goldenPath, got)
	}
}

// TestNoToolDescriptionLeaksRequiredDirective is a narrower, human-readable
// companion to the golden test above: whatever the schema looks like, no
// field description should ever start with "required," — that string is a
// jsonschema struct-tag artifact, not something the model should read.
func TestNoToolDescriptionLeaksRequiredDirective(t *testing.T) {
	cs, _ := newTestSession(t)

	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	for _, tool := range res.Tools {
		b, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), `"required,`) {
			t.Errorf("tool %q input schema contains a leaked 'required,' description: %s", tool.Name, b)
		}
	}
}

func TestEachToolRoundTrip(t *testing.T) {
	cs, _ := newTestSession(t)

	// process_thoughts first, since every other tool needs at least one
	// entry to operate on.
	writeRes := callTool(t, cs, "process_thoughts", map[string]any{
		"reflections":   "I noticed the retry logic was fragile under load.",
		"project_notes": "The auth service needs a circuit breaker around the token endpoint.",
	})
	if writeRes.IsError {
		t.Fatalf("process_thoughts returned an error result: %s", resultText(writeRes))
	}
	if !strings.Contains(resultText(writeRes), "Entry ID:") {
		t.Errorf("expected an entry ID in the response, got: %s", resultText(writeRes))
	}

	tests := []struct {
		name           string
		args           map[string]any
		wantErr        bool
		wantSubstrings []string
	}{
		{
			name:           "search_journal",
			args:           map[string]any{"query": "retry logic under load"},
			wantSubstrings: []string{"retry logic"},
		},
		{
			name:    "search_journal missing query",
			args:    map[string]any{"query": ""},
			wantErr: true,
		},
		{
			name:           "list_recent_entries",
			args:           map[string]any{},
			wantSubstrings: []string{"Recent entries"},
		},
		{
			name:           "read_recent_entries",
			args:           map[string]any{"limit": 1},
			wantSubstrings: []string{"retry logic"},
		},
		{
			name:           "journal_stats",
			args:           map[string]any{},
			wantSubstrings: []string{"Total entries: 1"},
		},
		{
			name:    "read_journal_entry missing id",
			args:    map[string]any{"id": "does-not-exist"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolName, _, _ := strings.Cut(tc.name, " ")
			res := callTool(t, cs, toolName, tc.args)

			if tc.wantErr {
				if !res.IsError {
					t.Errorf("expected a tool error, got: %s", resultText(res))
				}
				return
			}
			if res.IsError {
				t.Fatalf("unexpected tool error: %s", resultText(res))
			}
			text := resultText(res)
			for _, want := range tc.wantSubstrings {
				if !strings.Contains(text, want) {
					t.Errorf("expected result to contain %q, got: %s", want, text)
				}
			}
		})
	}
}

// TestReadJournalEntryByID exercises the ID round trip specifically:
// write, search to get an ID, then read that exact entry back.
func TestReadJournalEntryByID(t *testing.T) {
	cs, _ := newTestSession(t)

	writeRes := callTool(t, cs, "process_thoughts", map[string]any{
		"world_knowledge": "The mitochondria is the powerhouse of the cell.",
	})
	text := resultText(writeRes)
	_, idPart, ok := strings.Cut(text, "Entry ID: ")
	if !ok {
		t.Fatalf("could not find entry ID in response: %s", text)
	}
	id := strings.TrimSpace(idPart)

	readRes := callTool(t, cs, "read_journal_entry", map[string]any{"id": id})
	if readRes.IsError {
		t.Fatalf("read_journal_entry returned an error: %s", resultText(readRes))
	}
	if !strings.Contains(resultText(readRes), "mitochondria") {
		t.Errorf("expected the written content back, got: %s", resultText(readRes))
	}
}

// TestToolErrorsAreProtocolSuccesses locks in a subtlety of the MCP SDK
// that's easy to get backwards: a tool-level failure (bad input, not
// found, etc.) must surface as CallToolResult.IsError, with CallTool
// itself returning a nil error — not as a transport/protocol-level error.
// Getting this backwards means the model never sees why a call failed.
func TestToolErrorsAreProtocolSuccesses(t *testing.T) {
	cs, _ := newTestSession(t)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "read_journal_entry",
		Arguments: map[string]any{"id": "nonexistent-id"},
	})
	if err != nil {
		t.Fatalf("expected CallTool to succeed at the protocol level, got error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for a nonexistent entry")
	}
	if resultText(res) == "" {
		t.Error("expected a non-empty error message in Content")
	}
}

func TestProcessThoughtsRequiresAtLeastOneField(t *testing.T) {
	cs, _ := newTestSession(t)

	res := callTool(t, cs, "process_thoughts", map[string]any{})
	if !res.IsError {
		t.Error("expected an error result when no thought categories are provided")
	}
}

// TestStructuredContentValidates activates once handlers return typed Out
// values (Phase 4 of the project roadmap: output schemas + StructuredContent
// instead of prose-only responses) instead of `any`. At that point this
// should assert CallToolResult.StructuredContent is non-nil and conforms to
// the tool's advertised output schema for at least search_journal.
func TestStructuredContentValidates(t *testing.T) {
	t.Skip("pending Phase 4: handlers don't return structured output yet")
}

// TestAnnotations activates once tools carry mcp.ToolAnnotations (Phase 4).
// At that point this should assert ReadOnlyHint is true on the five
// read-only tools and false on process_thoughts, and OpenWorldHint is false
// throughout (a private journal is a closed world).
func TestAnnotations(t *testing.T) {
	t.Skip("pending Phase 4: tools don't carry annotations yet")
}
