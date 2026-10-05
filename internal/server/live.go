package server

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kKEo/memory-find/internal/live"
)

// tracker keeps the state the hosted web UI shows on /live. Only counts,
// names and timings are kept, never arguments or results.
type tracker struct {
	mu         sync.Mutex
	started    time.Time
	background string
	nextID     uint64
	inFlight   map[uint64]*live.Call
	sessions   map[string]*live.Session
}

func newTracker(now time.Time) *tracker {
	return &tracker{started: now, inFlight: map[uint64]*live.Call{}, sessions: map[string]*live.Session{}}
}

func sessionID(req mcp.Request) string {
	if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss != nil {
		return ss.ID()
	}
	return ""
}

// clientIdle is how long a client with no open session stays on the live
// page after its last call. Under the stateless 2026-07-28 protocol a
// client may hold no SDK session between requests, so recent activity is
// what "connected" means; it matches the HTTP session timeout.
const clientIdle = 30 * time.Minute

// session returns the entry for id, creating it on first sight. A request
// without a session id is keyed by client name. Callers hold mu.
func (t *tracker) session(id, client string, now time.Time) *live.Session {
	key := id
	if key == "" {
		key = "client:" + client
	}
	ss := t.sessions[key]
	if ss == nil {
		ss = &live.Session{ID: id, Client: client, Since: now}
		t.sessions[key] = ss
	}
	if client != "unknown" {
		ss.Client = client
	}
	return ss
}

func (t *tracker) connected(req mcp.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.session(sessionID(req), sessionClient(req), time.Now())
}

// begin records a tool call and returns its id for end and annotate.
func (t *tracker) begin(req mcp.Request, tool string) uint64 {
	now := time.Now()
	id, client := sessionID(req), sessionClient(req)
	t.mu.Lock()
	defer t.mu.Unlock()
	ss := t.session(id, client, now)
	ss.Calls++
	ss.LastCall = now
	t.nextID++
	t.inFlight[t.nextID] = &live.Call{Tool: tool, Client: client, Session: id, Started: now}
	return t.nextID
}

func (t *tracker) end(id uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.inFlight, id)
}

// annotate attaches ingest details to the call in ctx.
func (t *tracker) annotate(ctx context.Context, namespace, runID string) {
	info := infoFrom(ctx)
	if info == nil || info.liveID == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if c := t.inFlight[info.liveID]; c != nil {
		c.Namespace, c.RunID = namespace, runID
	}
}

func (t *tracker) setBackground(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.background = s
}

// Live implements live.Source. A client is listed while the SDK holds its
// session or it made a call within clientIdle; older entries are dropped.
func (s *Server) Live() live.Snapshot {
	open := map[string]bool{}
	for ss := range s.mcp.Sessions() {
		open[ss.ID()] = true
	}
	t := s.live
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := live.Snapshot{Version: s.version, KBPath: s.kbPath, Started: t.started, Background: t.background}
	if e := s.store.Embedder(); e != nil {
		snap.Model = e.Info().ID
	}
	now := time.Now()
	for key, ss := range t.sessions {
		last := ss.LastCall
		if last.IsZero() {
			last = ss.Since
		}
		if !open[ss.ID] && now.Sub(last) > clientIdle {
			delete(t.sessions, key)
			continue
		}
		snap.Sessions = append(snap.Sessions, *ss)
	}
	for _, c := range t.inFlight {
		snap.InFlight = append(snap.InFlight, *c)
	}
	slices.SortFunc(snap.Sessions, func(a, b live.Session) int { return a.Since.Compare(b.Since) })
	slices.SortFunc(snap.InFlight, func(a, b live.Call) int { return cmp.Or(a.Started.Compare(b.Started), cmp.Compare(a.Tool, b.Tool)) })
	return snap
}

// SetBackground reports the state of background work (backfill, reindex)
// on the live page.
func (s *Server) SetBackground(state string) { s.live.setBackground(state) }
