> **Historical document — predates the project rename.** This plan was
> written against a working name of `private-journal-mcp-go`, with paths
> under `~/.private-journal/` and `~/.cache/private-journal-mcp/models/`.
> The project shipped as **memo-mcp**, storing data under `~/.memo-mcp/`
> and caching the model under `~/.cache/memo-mcp/models/`. Several other
> details here are also stale versus what shipped: it describes 5 MCP
> tools (a 6th, `journal_stats`, was added), a pure-vector search design
> (the shipped version is hybrid vector + BM25 with reciprocal rank
> fusion), and dependency versions/binary-size estimates that no longer
> match `go.mod`. Kept for the design rationale, not as a description of
> the current system — see the top-level `README.md` for that.

# Go Implementation Plan: Private Journal MCP Server (v2)

## Context

A from-scratch Go implementation of the private journal MCP server. 

The goals are:
- Single-binary MCP server, zero CGo, zero system dependencies
- All data for a given token stored in one SQLite file
- Vector similarity search + SQL filtering in one database
- Token-based identity: an env var (`JOURNAL_TOKEN`) determines which database to use

## Architecture

### Project Structure

```
private-journal-mcp-go/
├── cmd/
│   └── private-journal-mcp/
│       └── main.go                 # Entry point: read token, open DB, start server
├── internal/
│   ├── server/
│   │   └── server.go               # MCP server, tool registration, handlers
│   ├── journal/
│   │   └── journal.go              # Write entries, format markdown, split by category
│   ├── search/
│   │   └── search.go               # Vector search, text search, filtering, excerpts
│   └── embedding/
│       └── embedding.go            # Embedding generation (hugot or clems4ever)
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

### Core Dependencies

```go
require (
    // MCP Protocol — official SDK, v1.0+ stability guarantee
    github.com/modelcontextprotocol/go-sdk v1.4.0

    // SQLite — pure Go, no CGo
    modernc.org/sqlite v1.50.0

    // Vector search — built into modernc.org/sqlite, just blank-import
    // modernc.org/sqlite/vec (no separate dependency needed)

    // Embeddings — hugot with pure Go backend, no CGo
    github.com/knights-analytics/hugot v0.x.x

    // Progress bar for model download
    github.com/schollz/progressbar/v3 v3.14.0

    // Testing
    github.com/stretchr/testify v1.9.0
)
```

**Usage pattern for SQLite + vec:**
```go
import (
    "database/sql"
    _ "modernc.org/sqlite"
    _ "modernc.org/sqlite/vec"  // Auto-registers sqlite-vec extension
)

db, _ := sql.Open("sqlite", filepath.Join(dbDir, token+".db"))
db.Exec("CREATE VIRTUAL TABLE entry_embeddings USING vec0(entry_id TEXT PRIMARY KEY, embedding float[384])")
```

**Key decisions:**
- `modelcontextprotocol/go-sdk` over mark3labs/mcp-go (official, stable API)
- `modernc.org/sqlite` v1.50+ with built-in `sqlite/vec` — pure Go, no CGo, no WASM, no ncruces needed
- `hugot` with pure Go backend (`hugot.NewGoSession`) — zero CGo, downloads model on first run
- `schollz/progressbar` for model download UX — shows progress during ~90MB first-run download
- No `golang.org/x/sync` — sequential writes are fine for this workload

### Embedding: hugot Pure Go Backend

```go
import (
    "github.com/knights-analytics/hugot"
    "github.com/knights-analytics/hugot/pipelines"
)

// Pure Go session — no C deps, no ONNX Runtime
session, _ := hugot.NewGoSession(ctx)
defer session.Destroy()

// Download model on first run (~90MB, with progress bar)
modelPath, _ := hugot.DownloadModel(ctx,
    "sentence-transformers/all-MiniLM-L6-v2",
    modelDir,
    hugot.NewDownloadOptions(),
)

// Create pipeline with L2 normalization (matches TS version's normalize: true)
pipe, _ := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
    ModelPath: modelPath,
    Name:      "journal-embeddings",
    Options:   []hugot.FeatureExtractionOption{pipelines.WithNormalization()},
})

// Generate embeddings → [][]float32
result, _ := pipe.RunPipeline(ctx, []string{"some text"})
embedding := result.Embeddings[0] // []float32, 384 dimensions
```

**First-run model download:** Wraps `hugot.DownloadModel` with `schollz/progressbar` to show download progress on stderr. Model cached at `~/.cache/private-journal-mcp/models/`.

## Persistence Model

### Token-Based Storage

```
~/.private-journal/
└── {token}.db          # One SQLite file per token
```

- `JOURNAL_TOKEN` env var provides the token (required)
- Token is a short identifier (e.g., project name, user alias, "default")
- All entries, embeddings, and metadata live in one `.db` file
- No CWD detection, no path resolution fallback chain, no dual-write

**If `JOURNAL_TOKEN` is not set:** server refuses to start with a clear error message. No guessing.

**Storage location:** `~/.private-journal/` (home directory). Override with `JOURNAL_PATH` env var if needed.

### SQLite Schema

```sql
-- Journal entries
CREATE TABLE entries (
    id TEXT PRIMARY KEY,                    -- UUID v7 (time-ordered)
    created_at INTEGER NOT NULL,            -- Unix milliseconds
    content TEXT NOT NULL,                   -- Full markdown content
    sections TEXT NOT NULL DEFAULT '[]'      -- JSON array of section names present
);

CREATE INDEX idx_entries_created_at ON entries(created_at);

-- Vector embeddings (sqlite-vec virtual table)
CREATE VIRTUAL TABLE entry_embeddings USING vec0(
    entry_id TEXT PRIMARY KEY,
    embedding float[384]
);

-- Optional: FTS5 for keyword search fallback
CREATE VIRTUAL TABLE entries_fts USING fts5(
    content,
    content=entries,
    content_rowid=rowid
);
```

**Why this schema:**
- `id` uses UUID v7 — time-ordered, globally unique, no collision risk (replaces microsecond filename hack)
- `sections` as JSON array in a TEXT column — simple, queryable with `json_each()`, avoids a join table for 6 possible values
- `entry_embeddings` as a vec0 virtual table — sqlite-vec handles vector storage and similarity natively
- `entries_fts` optional — provides keyword search when embedding generation fails or as a complement

### Write Path

```
process_thoughts(reflections: "...", project_notes: "...")
    │
    ├── Format markdown (sections as ## headers, no YAML frontmatter needed)
    ├── Generate UUID v7
    ├── INSERT INTO entries (id, created_at, content, sections)
    ├── Generate embedding from cleaned text
    ├── INSERT INTO entry_embeddings (entry_id, embedding)
    └── Return success (embedding failure does NOT block write)
```

No dual-write. No file system operations. One transaction per entry.

### Search Path

```
search_journal(query: "frustrating TypeScript bugs")
    │
    ├── Generate query embedding
    ├── SELECT with vec0 KNN + optional WHERE filters:
    │     SELECT e.id, e.content, e.sections, e.created_at,
    │            vec_distance_cosine(v.embedding, ?) as distance
    │     FROM entry_embeddings v
    │     JOIN entries e ON e.id = v.entry_id
    │     WHERE (section filter) AND (date range filter)
    │     ORDER BY distance
    │     LIMIT ?
    ├── Generate excerpts for results
    └── Return ranked results
```

Vector search + SQL filtering in one query. No loading all embeddings into memory, no manual cosine similarity loops.

### Read Path

```
read_journal_entry(id: "...")  →  SELECT content FROM entries WHERE id = ?
list_recent_entries(limit, days)  →  SELECT ... ORDER BY created_at DESC LIMIT ?
read_recent_entries(limit)  →  same but returns full content
```

All reads are single SQL queries. No directory scanning.

## MCP Tools

Same 5 tools as the TypeScript version, same schemas, same descriptions. The tool interface IS the compatibility surface — callers (Claude) see identical tools regardless of backend.

| Tool | Changes from TS version |
|------|------------------------|
| `process_thoughts` | Same 6 categories. No dual-write split — all go to one DB. |
| `search_journal` | Same params. `type` param (`project`/`user`/`both`) removed — single store. |
| `read_journal_entry` | Takes `id` (UUID) instead of file path. |
| `list_recent_entries` | Same params minus `type`. |
| `read_recent_entries` | Same params minus `type`. |

**Breaking change:** `read_journal_entry` takes an ID, not a path. Search results return IDs. This is cleaner and doesn't leak filesystem internals.

## What We Drop

| TypeScript feature | Replacement |
|---|---|
| Dual project/user directories | Single DB per token |
| CWD-based path resolution | `JOURNAL_TOKEN` + `JOURNAL_PATH` env vars |
| Markdown files + `.embedding` sidecars | SQLite rows |
| YAML frontmatter | SQL columns (created_at, sections) |
| Microsecond filename timestamps | UUID v7 |
| Loading all embeddings into memory for search | sqlite-vec KNN query |
| Cosine similarity in application code | `vec_distance_cosine()` in SQL |
| `generateMissingEmbeddings()` on startup | Entries without embeddings queryable via `LEFT JOIN ... WHERE embedding IS NULL` |

## What We Keep

- All 5 MCP tool names and their input schemas (minus `type` param)
- 6 thought categories with identical descriptions
- Privacy-first: all processing local, no external API calls
- Semantic search with all-MiniLM-L6-v2 embeddings
- Excerpt generation (sliding window with query word matching)
- Non-blocking embedding failures (writes succeed even if embedding generation fails)

## Implementation Plan

### Phase 1: Project Setup + Embedding (Days 1-2)

- [ ] `go mod init`, set up 4-package structure
- [ ] Implement `embedding.go`: hugot wrapper behind `Embedder` interface
  - `hugot.NewGoSession` → `hugot.DownloadModel` → `hugot.NewPipeline` with `pipelines.WithNormalization()`
  - Model cache at `~/.cache/private-journal-mcp/models/`
  - Wrap download with `schollz/progressbar/v3` writing to stderr
- [ ] Verify: `CGO_ENABLED=0 go build` succeeds
- [ ] Unit test: embedding produces 384-dim normalized vector

### Phase 2: Persistence + Journal (Days 3-5)

- [ ] Implement DB init: `modernc.org/sqlite` + `modernc.org/sqlite/vec` (blank import), run schema creation
- [ ] Implement token-based DB path: `JOURNAL_TOKEN` → `~/.private-journal/{token}.db`
- [ ] Implement `journal.go`: format markdown, UUID v7, write entry + embedding in one transaction
- [ ] Implement `search.go`: vector search via `vec_distance_cosine`, recent entries, read entry, excerpts
- [ ] Unit tests for each package (mock `Embedder` interface)

### Phase 3: MCP Server (Days 6-7)

- [ ] Implement `server.go`: tool registration with `modelcontextprotocol/go-sdk`, handlers
- [ ] Wire up DI: `main.go` reads token, opens DB, creates embedder + services + server
- [ ] Integration test: write entries → search → read back
- [ ] Test with Claude Desktop / Claude Code

### Phase 4: Polish & Ship (Days 8-10)

- [ ] Error handling audit (graceful embedding failures, missing token, DB corruption)
- [ ] Cross-platform build (`CGO_ENABLED=0`, GitHub Actions, GoReleaser)
- [ ] Homebrew formula for macOS
- [ ] README with setup instructions
- [ ] Release v1.0.0

**Total: ~2 weeks.**

## Key Design Decisions

### Why SQLite Over Files

| Concern | Files (TS approach) | SQLite (new approach) |
|---------|--------------------|-----------------------|
| Search | Load ALL embeddings into memory, compute cosine similarity in a loop | `vec_distance_cosine()` in SQL, handled by sqlite-vec |
| Filtering | Scan directories, parse dates from filenames, filter in memory | `WHERE created_at BETWEEN ? AND ?` |
| Atomic writes | Write file + write sidecar = 2 operations, partial failure possible | Single transaction |
| Startup cost | Scan all directories, find missing embeddings, backfill | Open DB file, done |
| Data integrity | Orphaned `.embedding` files, missing entries, name collisions | Foreign keys, constraints |
| Backup | Copy directory tree | Copy one `.db` file |

### Why Token Instead of CWD Detection

The TypeScript version uses CWD to determine which project's journal to write to. This is fragile:
- Docker containers have synthetic CWDs
- IDEs launch MCP servers from inconsistent directories
- Multiple terminals in the same project get different CWDs

A token is explicit. The MCP client config sets `JOURNAL_TOKEN=my-project` and that's the identity. No guessing, no path resolution fallback chains, no "system directory" blocklist.

### Why No Dual Write

The TypeScript version splits entries: `project_notes` → project directory, everything else → user directory. This creates complexity (two write paths, two search paths, a `type` filter on every query) for a use case that's better served by a single store with section filtering.

If a user wants separate journals per project, they use different tokens. If they want one combined journal, they use one token. The token IS the namespace.

### Why Dependency Injection Over Singleton

```go
// Embedder interface — swap in mocks for tests
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
}

// Constructor injection — explicit, testable
func NewJournalManager(db *sql.DB, embedder Embedder) *JournalManager { ... }
func NewSearchService(db *sql.DB, embedder Embedder) *SearchService { ... }
func NewServer(journal *JournalManager, search *SearchService) *Server { ... }
```

Tests create a `MockEmbedder` that returns fixed vectors. No `sync.Once`, no global state, no test pollution.

## Distribution

**Primary: Native binary** (Homebrew, GitHub Releases)
- `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build` — trivial cross-compilation
- GoReleaser for automated multi-platform builds
- Homebrew tap for macOS: `brew install yourname/tap/private-journal-mcp`

**Secondary: Docker** (for server/CI use cases only)
- Minimal Dockerfile: `FROM scratch` + binary + ca-certificates
- Mount `~/.private-journal/` and model cache for persistence

**Binary size:** ~15-20MB (model downloaded separately on first run, ~90MB, cached at `~/.cache/private-journal-mcp/models/`).

## Verification

1. **Functional:** All 5 tools work in Claude Desktop with a test token
2. **Search quality:** Vector search returns relevant results for natural language queries
3. **Performance:** Cold start < 1s, embedding < 500ms, search < 50ms (SQLite indexed)
4. **Reliability:** Embedding failure doesn't block journal writes
5. **Cross-platform:** macOS (arm64, amd64), Linux (amd64)

## Resolved Decisions

1. **Embeddings: hugot** with pure Go backend (`hugot.NewGoSession`). Zero CGo. Model downloaded on first run.
2. **SQLite: modernc.org/sqlite v1.50+** with built-in `modernc.org/sqlite/vec`. No ncruces, no WASM — just a blank import. Pure Go throughout.
3. **Model download UX: progress bar** via `schollz/progressbar/v3` on stderr during first-run ~90MB model download.

**Result: The entire binary is `CGO_ENABLED=0` compatible.** Single binary, zero system dependencies, cross-compiles trivially.
