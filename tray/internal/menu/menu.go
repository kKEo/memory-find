// Package menu turns what memors-tray knows (running servers, their agents,
// the knowledge bases on disk) into the menu-bar menu as plain data. The
// GUI layer renders it and sends the chosen Action back; nothing here
// touches AppKit, so the whole menu is unit-tested.
package menu

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/kKEo/memors/internal/live"
)

// ActionKind is what a menu item does.
type ActionKind int

// The actions.
const (
	None ActionKind = iota
	OpenStats
	OpenBrowser
	CopyURL
	CopyAdd
	ShowLog
	Start
	Restart
	Stop
	EditConfig
	OpenLogs
	Quit
	OpenSettings // memors-tray's settings window
	OpenSetup    // the assistant that installs memors-mcp and sets up a knowledge base
)

// Action is a click: what to do, to which server or knowledge base.
type Action struct {
	Kind   ActionKind
	Server string // ServerView.Key
	KB     string
}

// Item is one menu entry. Key identifies it across rebuilds; Children make
// it a submenu.
type Item struct {
	Key       string
	Title     string
	Tooltip   string
	Disabled  bool
	Separator bool
	Children  []Item
	Action    Action
}

// State is a server's state as memors-tray sees it.
type State string

// The states.
const (
	Starting    State = "starting"
	Running     State = "running"
	Stopping    State = "stopping"
	Stopped     State = "stopped"
	Crashed     State = "crashed"
	Unreachable State = "unreachable"
)

// up reports whether a server process is (still) there.
func (s State) up() bool { return s == Starting || s == Running || s == Stopping || s == Unreachable }

// ServerView is one server row.
type ServerView struct {
	Key        string // stable: the server URL, or "kb:<name>" when not running
	KB         string
	URL        string // base URL; empty when not running
	Auth       string
	State      State
	Detail     string // why, for crashed or unreachable servers and old versions
	Managed    bool   // started by this memors-tray
	Configured bool   // listed in tray.json, so it can be (re)started
	HasLog     bool
	MTLS       bool
	TokenFile  string
	Snapshot   *live.Snapshot // nil until the first successful poll
}

// Input is everything the menu shows.
type Input struct {
	Now              time.Time
	Servers          []ServerView
	KBs              []string // knowledge bases on disk
	Memors           string   // "memors-mcp <version> at <path>", or why it is missing
	MemorsOK         bool
	Problem          string // a configuration or discovery problem
	StopsOnQuit      int    // servers Quit will stop
	DefaultTokenFile string // <MEMORS_HOME>/http-token
}

// activeWindow is how recently a client without an event stream must have
// been seen to count as active.
const activeWindow = 2 * time.Minute

// Build returns the menu.
func Build(in Input) []Item {
	var items []Item
	if !in.MemorsOK {
		items = append(items, Item{Key: "install", Title: "Install memors-mcp…", Tooltip: in.Memors, Action: Action{Kind: OpenSetup}}, sep("install"))
	}
	if in.Problem != "" {
		items = append(items, Item{Key: "problem", Title: "⚠ " + in.Problem, Disabled: true}, sep("problem"))
	}
	servers := slices.Clone(in.Servers)
	slices.SortFunc(servers, func(a, b ServerView) int {
		if c := strings.Compare(a.KB, b.KB); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
	for _, s := range servers {
		items = append(items, header(s, in))
		items = append(items, agents(in.Now, s)...)
	}
	if len(servers) == 0 {
		items = append(items, Item{Key: "none", Title: "No memors-mcp servers running", Disabled: true})
	}
	items = append(items, sep("start"), startMenu(in, servers),
		Item{Key: "settings", Title: "Settings…", Action: Action{Kind: OpenSettings}},
		Item{Key: "setup", Title: "Install or Update memors-mcp…", Tooltip: in.Memors, Action: Action{Kind: OpenSetup}},
		sep("quit"))
	quit := "Quit memors-tray"
	if in.StopsOnQuit > 0 {
		quit += fmt.Sprintf(" (stops %d server%s)", in.StopsOnQuit, plural(in.StopsOnQuit))
	}
	return append(items, Item{Key: "quit", Title: quit, Action: Action{Kind: Quit}})
}

// Title is the text next to the menu-bar icon: the number of active
// agents, or nothing.
func Title(in Input) string {
	n := 0
	for _, s := range in.Servers {
		if s.Snapshot == nil || !s.State.up() {
			continue
		}
		for _, ss := range s.Snapshot.Sessions {
			if active, _ := activity(in.Now, ss, s.Snapshot.InFlight); active && (ss.Open || ss.Stream) {
				n++
			}
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

// SameShape reports whether b can be shown by updating a's items in place:
// the same keys, separators and submenus in the same order.
func SameShape(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Separator != b[i].Separator || !SameShape(a[i].Children, b[i].Children) {
			return false
		}
	}
	return true
}

func header(s ServerView, in Input) Item {
	title := s.KB
	if host := hostOf(s.URL); host != "" {
		title += " · " + host
	}
	title += " · " + string(s.State)
	if s.Detail != "" {
		title += " (" + s.Detail + ")"
	}
	k := "srv:" + s.Key
	act := func(name, title string, kind ActionKind, enabled bool) Item {
		return Item{Key: k + ":" + name, Title: title, Disabled: !enabled, Action: Action{Kind: kind, Server: s.Key, KB: s.KB}}
	}
	var children []Item
	if s.State.up() {
		reachable := s.URL != "" && s.State != Stopping
		children = append(children,
			act("stats", "Open Stats Window", OpenStats, reachable && !s.MTLS),
			act("browser", "Open in Browser", OpenBrowser, reachable),
			act("url", "Copy MCP URL", CopyURL, s.URL != ""),
			act("add", "Copy “claude mcp add” Command", CopyAdd, s.URL != ""),
			sep(k+":sep"),
		)
		if s.HasLog {
			children = append(children, act("log", "Show Log", ShowLog, true))
		}
		if s.Configured {
			children = append(children, act("restart", "Restart", Restart, s.State != Stopping))
		}
		children = append(children, act("stop", "Stop", Stop, s.State != Stopping))
	} else {
		if s.Configured {
			children = append(children, act("start", "Start", Start, in.MemorsOK))
		}
		if s.HasLog {
			children = append(children, act("log", "Show Log", ShowLog, true))
		}
	}
	tip := ""
	if s.Snapshot != nil {
		tip = fmt.Sprintf("memors-mcp %s · %s · model %s · up %s", s.Snapshot.Version, s.Snapshot.KBPath, orNone(s.Snapshot.Model), ago(in.Now.Sub(s.Snapshot.Started)))
	}
	return Item{Key: k, Title: title, Tooltip: tip, Children: children}
}

// agents lists the clients with an open session, newest activity first in
// meaning: an in-flight call, then recent activity, then quiet sessions.
func agents(now time.Time, s ServerView) []Item {
	if s.Snapshot == nil || !s.State.up() {
		return nil
	}
	var items []Item
	for _, ss := range s.Snapshot.Sessions {
		if !ss.Open && !ss.Stream {
			continue // left; still on the Live page, not here
		}
		active, call := activity(now, ss, s.Snapshot.InFlight)
		name := Clean(ss.Client)
		if v := Clean(ss.ClientVersion); v != "" {
			name += " " + v
		}
		var what string
		switch {
		case call != nil:
			what = fmt.Sprintf("%s (%s)", Clean(call.Tool), ago(now.Sub(call.Started)))
		case active && ss.Calls == 0:
			what = "connected, no calls yet"
		case active:
			what = fmt.Sprintf("%d call%s · last %s ago", ss.Calls, plural(ss.Calls), ago(now.Sub(ss.LastCall)))
		default:
			what = "idle " + ago(now.Sub(ss.LastSeen))
		}
		marker := "○"
		if active {
			marker = "●"
		}
		items = append(items, Item{
			Key:     "agent:" + s.Key + ":" + ss.Key,
			Title:   "    " + marker + " " + name + " — " + what,
			Tooltip: fmt.Sprintf("session %s · connected %s ago", ss.Key, ago(now.Sub(ss.Since))),
			Action:  Action{Kind: OpenStats, Server: s.Key, KB: s.KB},
		})
	}
	if len(items) == 0 {
		items = append(items, Item{Key: "agent:" + s.Key + ":none", Title: "    no agents connected", Disabled: true})
	}
	return items
}

// activity: a client is active while a call of its is running, while it
// holds its event stream, or for a while after it was last seen.
func activity(now time.Time, ss live.Session, calls []live.Call) (bool, *live.Call) {
	for i := range calls {
		if calls[i].SessionKey == ss.Key {
			return true, &calls[i]
		}
	}
	return ss.Stream || now.Sub(ss.LastSeen) < activeWindow, nil
}

func startMenu(in Input, servers []ServerView) Item {
	running := map[string]bool{}
	for _, s := range servers {
		if s.State.up() {
			running[s.KB] = true
		}
	}
	var children []Item
	for _, kb := range in.KBs {
		if running[kb] {
			continue
		}
		children = append(children, Item{Key: "start:" + kb, Title: kb, Disabled: !in.MemorsOK, Action: Action{Kind: Start, KB: kb}})
	}
	if len(children) == 0 {
		title := "Every knowledge base is running"
		if len(in.KBs) == 0 {
			title = "No knowledge bases yet (memors-mcp ingest creates one)"
		}
		children = append(children, Item{Key: "start:none", Title: title, Disabled: true})
	}
	return Item{Key: "start", Title: "Start Server", Children: children}
}

// ClaudeAddCommand is the line that adds a server to Claude Code. It names
// the token through `memors-mcp http-token`, never the token itself.
func ClaudeAddCommand(kb, serverURL, auth, tokenFile, defaultTokenFile string) string {
	name := "memors"
	if kb != "default" {
		name = "memors-" + strings.Map(func(r rune) rune {
			if r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return '-'
		}, kb)
	}
	cmd := "claude mcp add --transport http " + name + " " + shellQuote(serverURL+"/mcp")
	if auth == "token" {
		tok := "memors-mcp http-token"
		if tokenFile != "" && tokenFile != defaultTokenFile {
			tok += " --file " + shellQuote(tokenFile)
		}
		cmd += ` --header "Authorization: Bearer $(` + tok + `)"`
	}
	return cmd
}

// Clean makes a client-supplied string safe to show: no control or
// formatting characters (bidi overrides), at most 40 characters.
func Clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 40 {
		s = string(r[:39]) + "…"
	}
	return s
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return ""
}

func ago(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func sep(key string) Item { return Item{Key: "sep:" + key, Separator: true} }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
