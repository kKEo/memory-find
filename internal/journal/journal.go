package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kKEo/memory-find/internal/embedding"
)

type Manager struct {
	db       *sql.DB
	embedder embedding.Embedder
}

func NewManager(db *sql.DB, embedder embedding.Embedder) *Manager {
	return &Manager{db: db, embedder: embedder}
}

// InitDB brings db up to the schema this binary expects. It is safe to call
// on a fresh database, an existing one at an older schema version, or one
// already at the current version. See migrate.go.
func InitDB(db *sql.DB) error {
	return Migrate(context.Background(), db)
}

// tokenPattern constrains JOURNAL_TOKEN to a safe filename component: it is
// used verbatim as "<token>.db" under the storage directory, so it must not
// contain path separators or otherwise be able to escape that directory.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func validateToken(token string) error {
	if !tokenPattern.MatchString(token) || token == "." || token == ".." {
		return fmt.Errorf("invalid JOURNAL_TOKEN %q: must match %s and not be \".\" or \"..\"", token, tokenPattern.String())
	}
	return nil
}

func OpenDB(token, basePath string) (*sql.DB, error) {
	if err := validateToken(token); err != nil {
		return nil, err
	}

	if basePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		basePath = filepath.Join(home, ".memo-mcp")
	}
	// 0o700: this directory holds a private journal.
	if err := os.MkdirAll(basePath, 0o700); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}

	dbPath := filepath.Join(basePath, token+".db")
	dsn := "file:" + dbPath +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// One connection: writes are serialized in-process, and WAL +
	// busy_timeout (above) handle serialization across processes sharing
	// the same JOURNAL_TOKEN.
	db.SetMaxOpenConns(1)

	if err := Migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, err
	}

	securePermissions(dbPath)

	return db, nil
}

// securePermissions best-effort restricts the database file (and its WAL
// sidecars, if present) to owner-only access. Failures are silently
// ignored: on platforms without POSIX permission bits this is a no-op, and
// a database that already exists with looser permissions from before this
// fix landed is still usable, just not automatically tightened.
func securePermissions(dbPath string) {
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		_ = os.Chmod(p, 0o600)
	}
}

type ThoughtInput struct {
	Reflections       string
	Observations      string
	ProjectNotes      string
	UserContext       string
	TechnicalInsights string
	WorldKnowledge    string
}

func (m *Manager) WriteThoughts(ctx context.Context, input ThoughtInput) (string, error) {
	content, sections := formatMarkdown(input)
	if len(sections) == 0 {
		return "", fmt.Errorf("at least one thought category must be provided")
	}

	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	now := time.Now().UnixMilli()

	sectionsJSON, err := json.Marshal(sections)
	if err != nil {
		return "", fmt.Errorf("marshal sections: %w", err)
	}

	// Embed before opening the write transaction. Inference can take
	// hundreds of milliseconds; holding that inside a transaction on a
	// single-connection database would block every other query for the
	// duration. A failed or unavailable embedder is still non-fatal here —
	// the entry is saved without a vector.
	var vec []float32
	if m.embedder != nil {
		var embedErr error
		vec, embedErr = m.embedder.Embed(ctx, truncateForEmbedding(cleanForEmbedding(content)))
		if embedErr != nil {
			fmt.Fprintf(os.Stderr, "warning: embedding failed (entry saved without embedding): %v\n", embedErr)
			vec = nil
		}
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO entries (id, created_at, content, sections) VALUES (?, ?, ?, ?)`,
		id.String(), now, content, string(sectionsJSON),
	)
	if err != nil {
		return "", fmt.Errorf("insert entry: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO entries_fts(entry_id, content) VALUES (?, ?)`,
		id.String(), content,
	)
	if err != nil {
		return "", fmt.Errorf("insert fts: %w", err)
	}

	if vec != nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO entry_embeddings (entry_id, embedding) VALUES (?, ?)`,
			id.String(), Float32ToJSON(vec),
		); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to store embedding: %v\n", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	return id.String(), nil
}

func formatMarkdown(input ThoughtInput) (string, []string) {
	var parts []string
	var sections []string

	add := func(name, content string) {
		if content == "" {
			return
		}
		sections = append(sections, name)
		parts = append(parts, fmt.Sprintf("## %s\n\n%s", name, content))
	}

	add("reflections", input.Reflections)
	add("observations", input.Observations)
	add("project_notes", input.ProjectNotes)
	add("user_context", input.UserContext)
	add("technical_insights", input.TechnicalInsights)
	add("world_knowledge", input.WorldKnowledge)

	return strings.Join(parts, "\n\n"), sections
}

func cleanForEmbedding(text string) string {
	text = strings.ReplaceAll(text, "##", "")
	text = strings.TrimSpace(text)
	return text
}

// maxEmbedInputRunes is a conservative interim safeguard, not a proper
// token-aware limit. all-MiniLM-L6-v2 has a 512 word-piece position limit,
// but the pure-Go tokenizer path this project builds with
// (CGO_ENABLED=0) does not enforce that limit itself — on the WordPiece
// tokenizer used here, an oversized input reaches the ONNX graph and
// errors there instead of being clamped, and multi-section journal
// entries routinely exceed it. Before this guard existed, that meant
// entries past roughly 512 word-pieces got no embedding at all: Embed
// failed, the write path logged a warning and saved the entry anyway, and
// it became permanently invisible to vector search.
//
// ~1500 runes is a deliberately generous-but-safe budget: English prose
// through this tokenizer averages well above 2.5 characters per
// word-piece, so this should stay under the true limit even for
// code-heavy or symbol-dense text. It is a stopgap, not a fix — it still
// discards the tail of long entries rather than embedding all of it. The
// real fix is chunking each entry into multiple embedded windows (see the
// project roadmap), which removes this cap entirely; until then, this is
// what keeps long entries searchable at all rather than silently invisible.
const maxEmbedInputRunes = 1500

func truncateForEmbedding(text string) string {
	runes := []rune(text)
	if len(runes) <= maxEmbedInputRunes {
		return text
	}
	return string(runes[:maxEmbedInputRunes])
}

func Float32ToJSON(v []float32) string {
	b, _ := json.Marshal(v)
	return string(b)
}
