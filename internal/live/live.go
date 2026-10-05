// Package live is the in-memory state a long-running HTTP server exposes to
// the web UI it hosts: connected clients, calls in flight and background
// work. It holds types only, so the server and the UI share them without
// importing each other. A stdio server or a standalone UI has no live
// source; the UI then reads the knowledge-base file alone.
package live

import "time"

// Source is implemented by the MCP server when it serves over HTTP.
type Source interface {
	Live() Snapshot
}

// Snapshot is one consistent view of the server's in-memory state.
type Snapshot struct {
	Version    string
	KBPath     string
	Model      string // embedding model id; empty when keyword-only
	Started    time.Time
	Background string // backfill / reindex state, e.g. "running", "done: 12 vectors"
	Sessions   []Session
	InFlight   []Call
}

// Session is one connected MCP client.
type Session struct {
	ID       string
	Client   string
	Since    time.Time
	LastCall time.Time // zero before the first tool call
	Calls    int
}

// Call is one tool call being served right now.
type Call struct {
	Tool      string
	Client    string
	Session   string
	Started   time.Time
	Namespace string // ingest only
	RunID     string // ingest only: the ingest run it is recorded under
}
