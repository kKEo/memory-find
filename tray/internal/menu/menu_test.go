package menu

import (
	"strings"
	"testing"
	"time"

	"github.com/kKEo/memory-find/internal/live"
)

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// titles flattens a menu to indented titles, so a test reads like the menu.
func titles(items []Item, indent string) string {
	var b strings.Builder
	for _, it := range items {
		switch {
		case it.Separator:
			b.WriteString(indent + "---\n")
		default:
			b.WriteString(indent + it.Title)
			if it.Disabled {
				b.WriteString(" [off]")
			}
			b.WriteString("\n")
			b.WriteString(titles(it.Children, indent+"  > "))
		}
	}
	return b.String()
}

func running(snap *live.Snapshot) ServerView {
	return ServerView{Key: "http://127.0.0.1:8765", KB: "crportal", URL: "http://127.0.0.1:8765", Auth: "none", State: Running,
		Managed: true, Configured: true, HasLog: true, Snapshot: snap}
}

func TestEmpty(t *testing.T) {
	got := titles(Build(Input{Now: now, Memo: "memo-mcp v1 at /bin/memo-mcp", MemoOK: true}), "")
	want := `No memo-mcp servers running [off]
---
Start Server
  > No knowledge bases yet (memo-mcp ingest creates one) [off]
Settings…
Install or Update memo-mcp…
---
Quit memo-tray
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestServerWithAgents(t *testing.T) {
	snap := &live.Snapshot{Version: "v1.5.0", KBPath: "/kb/crportal.db", Started: now.Add(-time.Hour), Sessions: []live.Session{
		{Key: "k1", Client: "claude-code", ClientVersion: "2.1.4", Since: now.Add(-10 * time.Minute), LastSeen: now.Add(-time.Second), LastCall: now.Add(-3 * time.Second), Calls: 14, Open: true, Stream: true},
		{Key: "k2", Client: "cursor", ClientVersion: "1.2", Since: now.Add(-time.Hour), LastSeen: now.Add(-12 * time.Minute), Open: true},
		{Key: "k3", Client: "gone", Since: now.Add(-time.Hour), LastSeen: now.Add(-time.Minute)},
		{Key: "k4", Client: "new", Since: now, LastSeen: now, Open: true, Stream: true},
	}, InFlight: []live.Call{{Tool: "search", SessionKey: "k2", Started: now.Add(-3 * time.Second)}}}
	in := Input{Now: now, Servers: []ServerView{running(snap)}, KBs: []string{"crportal", "default"}, Memo: "m", MemoOK: true, StopsOnQuit: 1}
	got := titles(Build(in), "")
	want := `crportal · 127.0.0.1:8765 · running
  > Open Stats Window
  > Open in Browser
  > Copy MCP URL
  > Copy “claude mcp add” Command
  > ---
  > Show Log
  > Restart
  > Stop
    ● claude-code 2.1.4 — 14 calls · last 3s ago
    ● cursor 1.2 — search (3s)
    ● new — connected, no calls yet
---
Start Server
  > default
Settings…
Install or Update memo-mcp…
---
Quit memo-tray (stops 1 server)
`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if title := Title(in); title != "3" {
		t.Errorf("Title = %q, want 3 active agents", title)
	}
	snap.InFlight = nil
	if got := titles(Build(in), ""); !strings.Contains(got, "○ cursor 1.2 — idle 12m") {
		t.Errorf("quiet session not marked idle:\n%s", got)
	}
	if title := Title(in); title != "2" {
		t.Errorf("Title = %q, want 2", title)
	}
}

func TestStoppedCrashedAndProblems(t *testing.T) {
	in := Input{Now: now, MemoOK: false, Memo: "memo-mcp not found", Problem: "tray.json: bad", KBs: []string{"a", "b"}, Servers: []ServerView{
		{Key: "kb:a", KB: "a", State: Crashed, Detail: "exit status 3", Configured: true, HasLog: true},
		{Key: "kb:b", KB: "b", State: Stopped, Configured: true},
		{Key: "https://box:9", KB: "c", URL: "https://box:9", Auth: "token", State: Unreachable, Detail: "token refused", MTLS: true},
	}}
	got := titles(Build(in), "")
	if items := Build(in); items[0].Title != "Install memo-mcp…" || items[0].Action.Kind != OpenSetup || items[0].Tooltip != "memo-mcp not found" {
		t.Errorf("first item when memo-mcp is missing = %+v", items[0])
	}
	for _, want := range []string{
		"⚠ tray.json: bad [off]",
		"a · crashed (exit status 3)\n  > Start [off]\n  > Show Log\n",
		"b · stopped\n  > Start [off]\n",
		"c · box:9 · unreachable (token refused)\n  > Open Stats Window [off]\n  > Open in Browser\n",
		"Start Server\n  > a [off]\n  > b [off]\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("menu lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "agents") {
		t.Errorf("agent rows for a server without a snapshot:\n%s", got)
	}
}

func TestSameShape(t *testing.T) {
	snap := &live.Snapshot{Sessions: []live.Session{{Key: "k1", Client: "c", LastSeen: now, LastCall: now, Calls: 2, Open: true}}}
	in := Input{Now: now, Servers: []ServerView{running(snap)}, MemoOK: true}
	a := Build(in)
	in.Now = now.Add(time.Minute) // titles change, shape does not
	b := Build(in)
	if !SameShape(a, b) || titles(a, "") == titles(b, "") {
		t.Error("a ticking clock should update titles in place")
	}
	snap.Sessions = append(snap.Sessions, live.Session{Key: "k2", Client: "d", LastSeen: now, Open: true})
	if SameShape(a, Build(in)) {
		t.Error("a new agent changes the shape")
	}
}

func TestClaudeAddCommand(t *testing.T) {
	for _, c := range []struct {
		kb, url, auth, file, want string
	}{
		{"default", "http://127.0.0.1:8765", "none", "", `claude mcp add --transport http memo 'http://127.0.0.1:8765/mcp'`},
		{"crportal", "http://127.0.0.1:8766", "token", "/h/http-token", `claude mcp add --transport http memo-crportal 'http://127.0.0.1:8766/mcp' --header "Authorization: Bearer $(memo-mcp http-token)"`},
		{"my.kb", "http://127.0.0.1:1", "token", "/etc/it's", `claude mcp add --transport http memo-my-kb 'http://127.0.0.1:1/mcp' --header "Authorization: Bearer $(memo-mcp http-token --file '/etc/it'\''s')"`},
	} {
		if got := ClaudeAddCommand(c.kb, c.url, c.auth, c.file, "/h/http-token"); got != c.want {
			t.Errorf("ClaudeAddCommand(%s) =\n%s\nwant\n%s", c.kb, got, c.want)
		}
	}
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"claude-code":           "claude-code",
		"evil\u202Eedoc\n\x00":  "eviledoc",
		strings.Repeat("x", 50): strings.Repeat("x", 39) + "…",
		"  spaced  ":            "spaced",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}
