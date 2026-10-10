// Package live is the in-memory state a long-running HTTP server exposes to
// the web UI it hosts: connected clients, calls in flight and background
// work. It holds types only, so the server and the UI share them without
// importing each other. A stdio server or a standalone UI has no live
// source; the UI then reads the knowledge-base file alone.
//
// The same types are the /live.json contract for companion apps
// (memors-tray, a separate module that imports this package), so the package
// depends on the standard library only, and the JSON shape changes only by
// adding fields; anything else bumps Schema.
package live

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Schema is the version of the /live.json shape.
const Schema = 1

// Source is implemented by the MCP server when it serves over HTTP.
type Source interface {
	Live() Snapshot
}

// Snapshot is one consistent view of the server's in-memory state.
type Snapshot struct {
	Schema     int       `json:"schema"`   // set by /live.json
	Instance   string    `json:"instance"` // random per server process
	PID        int       `json:"pid"`
	Version    string    `json:"version"`
	KB         string    `json:"kb"` // knowledge-base name (MEMORS_KB)
	KBPath     string    `json:"kb_path"`
	Model      string    `json:"model"` // embedding model id; empty when keyword-only
	Started    time.Time `json:"started"`
	Background string    `json:"background"` // backfill / reindex state, e.g. "running", "done: 12 vectors"
	Sessions   []Session `json:"sessions"`
	InFlight   []Call    `json:"in_flight"`
}

// Session is one connected MCP client.
type Session struct {
	// ID is the raw MCP session id. Whoever knows it can act as that
	// client, so it is never shown: Key is the public handle.
	ID            string    `json:"-"`
	Key           string    `json:"id"`
	Client        string    `json:"client"`
	ClientVersion string    `json:"client_version"`
	Since         time.Time `json:"since"`
	LastSeen      time.Time `json:"last_seen"`          // last request of any kind
	LastCall      time.Time `json:"last_call,omitzero"` // zero before the first tool call
	Calls         int       `json:"calls"`
	Open          bool      `json:"open"`   // the server still holds the session
	Stream        bool      `json:"stream"` // the client holds its event stream (GET /mcp) right now
}

// Call is one tool call being served right now.
type Call struct {
	Tool       string    `json:"tool"`
	Client     string    `json:"client"`
	Session    string    `json:"-"` // raw session id; never shown (see Session.ID)
	SessionKey string    `json:"session"`
	Started    time.Time `json:"started"`
	Namespace  string    `json:"namespace,omitempty"` // ingest only
	RunID      string    `json:"run_id,omitempty"`    // ingest only: the ingest run it is recorded under
}

// SessionKey is the public handle of a raw session id: stable for the
// session's lifetime, useless for taking it over.
func SessionKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:6])
}
