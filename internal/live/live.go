// Package live is the in-memory state a long-running HTTP server exposes to
// the web UI it hosts: connected clients, calls in flight and background
// work. It holds types only, so the server and the UI share them without
// importing each other. A stdio server or a standalone UI has no live
// source; the UI then reads the knowledge-base file alone.
package live

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

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
	// ID is the raw MCP session id. Whoever knows it can act as that
	// client, so it is never shown: Key is the public handle.
	ID            string
	Key           string
	Client        string
	ClientVersion string
	Since         time.Time
	LastSeen      time.Time // last request of any kind
	LastCall      time.Time // zero before the first tool call
	Calls         int
	Open          bool // the server still holds the session
	Stream        bool // the client holds its event stream (GET /mcp) right now
}

// Call is one tool call being served right now.
type Call struct {
	Tool       string
	Client     string
	Session    string // raw session id; never shown (see Session.ID)
	SessionKey string
	Started    time.Time
	Namespace  string // ingest only
	RunID      string // ingest only: the ingest run it is recorded under
}

// SessionKey is the public handle of a raw session id: stable for the
// session's lifetime, useless for taking it over.
func SessionKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:6])
}
