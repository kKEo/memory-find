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
	streams    map[string]int // raw session id -> event streams (GET /mcp) open now
}

func newTracker(now time.Time) *tracker {
	return &tracker{started: now, inFlight: map[uint64]*live.Call{}, sessions: map[string]*live.Session{}, streams: map[string]int{}}
}

func sessionID(req mcp.Request) string {
	if ss, ok := req.GetSession().(*mcp.ServerSession); ok && ss != nil {
		return ss.ID()
	}
	return ""
}

// clientIdle is how long a client with no open session stays on the live
// page after it was last seen. Under the stateless 2026-07-28 protocol a
// client may hold no SDK session between requests, so recent activity is
// what "connected" means; it matches the HTTP session timeout.
const clientIdle = 30 * time.Minute

// trackerKey is the map key of a session: its id, or the client name for a
// request without a session id.
func trackerKey(id, client string) string {
	if id == "" {
		return "client:" + client
	}
	return id
}

// session returns the entry for id, creating it on first sight. Callers
// hold mu.
func (t *tracker) session(id, client, version string, now time.Time) *live.Session {
	k := trackerKey(id, client)
	ss := t.sessions[k]
	if ss == nil {
		ss = &live.Session{ID: id, Key: live.SessionKey(k), Client: client, Since: now}
		t.sessions[k] = ss
	}
	if client != "unknown" {
		ss.Client = client
		if version != "" {
			ss.ClientVersion = version
		}
	}
	ss.LastSeen = now
	return ss
}

func (t *tracker) connected(req mcp.Request) {
	name, version := clientOf(req)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.session(sessionID(req), name, version, time.Now())
}

// seen marks activity on a session already tracked. It never creates one,
// so a probe that does not establish a session leaves no trace.
func (t *tracker) seen(req mcp.Request) {
	name, _ := clientOf(req)
	k := trackerKey(sessionID(req), name)
	t.mu.Lock()
	defer t.mu.Unlock()
	if ss := t.sessions[k]; ss != nil {
		ss.LastSeen = time.Now()
	}
}

// streamOpened and streamClosed bracket a client's event stream. The
// stream ends the moment the client goes away, unlike the session, which
// the server keeps until it times out.
func (t *tracker) streamOpened(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.streams[id]++
}

func (t *tracker) streamClosed(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams[id]--; t.streams[id] <= 0 {
		delete(t.streams, id)
	}
	if ss := t.sessions[id]; ss != nil {
		ss.LastSeen = time.Now()
	}
}

// begin records a tool call and returns its id for end and annotate.
func (t *tracker) begin(req mcp.Request, tool string) uint64 {
	now := time.Now()
	id := sessionID(req)
	client, version := clientOf(req)
	t.mu.Lock()
	defer t.mu.Unlock()
	ss := t.session(id, client, version, now)
	ss.Calls++
	ss.LastCall = now
	t.nextID++
	t.inFlight[t.nextID] = &live.Call{Tool: tool, Client: client, Session: id, SessionKey: ss.Key, Started: now}
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
// session, its event stream is open, or it was seen within clientIdle;
// older entries are dropped.
func (s *Server) Live() live.Snapshot {
	open := map[string]bool{}
	for ss := range s.mcp.Sessions() {
		open[ss.ID()] = true
	}
	t := s.live
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := live.Snapshot{Version: s.version, KBPath: s.kbPath, Started: t.started, Background: t.background,
		Sessions: []live.Session{}, InFlight: []live.Call{}}
	if e := s.store.Embedder(); e != nil {
		snap.Model = e.Info().ID
	}
	now := time.Now()
	for k, ss := range t.sessions {
		ss.Open = open[ss.ID]
		ss.Stream = ss.ID != "" && t.streams[ss.ID] > 0
		if !ss.Open && !ss.Stream && now.Sub(ss.LastSeen) > clientIdle {
			delete(t.sessions, k)
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
