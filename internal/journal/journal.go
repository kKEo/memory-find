package journal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kmaziarz/memo-mcp/internal/embedding"
)

const schema = `
CREATE TABLE IF NOT EXISTS entries (
    id TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    content TEXT NOT NULL,
    sections TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries(created_at);
`

type Manager struct {
	db       *sql.DB
	embedder embedding.Embedder
}

func NewManager(db *sql.DB, embedder embedding.Embedder) *Manager {
	return &Manager{db: db, embedder: embedder}
}

func InitDB(db *sql.DB) error {
	_, err := db.Exec(schema)
	if err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	_, err = db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS entry_embeddings USING vec0(entry_id TEXT PRIMARY KEY, embedding float[384])`)
	if err != nil {
		return fmt.Errorf("create vec table: %w", err)
	}
	return nil
}

func OpenDB(token, basePath string) (*sql.DB, error) {
	if basePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		basePath = filepath.Join(home, ".memo-mcp")
	}
	if err := os.MkdirAll(basePath, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}

	dbPath := filepath.Join(basePath, token+".db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := InitDB(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

type ThoughtInput struct {
	Reflections       string
	Observations      string
	ProjectNotes      string
	UserContext        string
	TechnicalInsights string
	WorldKnowledge    string
}

func (m *Manager) WriteThoughts(ctx context.Context, input ThoughtInput) (string, error) {
	content, sections := formatMarkdown(input)
	if len(sections) == 0 {
		return "", fmt.Errorf("at least one thought category must be provided")
	}

	id := newUUIDv7()
	now := time.Now().UnixMilli()

	sectionsJSON, _ := json.Marshal(sections)

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx,
		`INSERT INTO entries (id, created_at, content, sections) VALUES (?, ?, ?, ?)`,
		id, now, content, string(sectionsJSON),
	)
	if err != nil {
		return "", fmt.Errorf("insert entry: %w", err)
	}

	if m.embedder != nil {
		cleanText := cleanForEmbedding(content)
		vec, embErr := m.embedder.Embed(ctx, cleanText)
		if embErr != nil {
			fmt.Fprintf(os.Stderr, "warning: embedding failed (entry saved without embedding): %v\n", embErr)
		} else {
			_, embErr = tx.ExecContext(ctx,
				`INSERT INTO entry_embeddings (entry_id, embedding) VALUES (?, ?)`,
				id, Float32ToJSON(vec),
			)
			if embErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to store embedding: %v\n", embErr)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}

	return id, nil
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

func Float32ToJSON(v []float32) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newUUIDv7() string {
	now := time.Now().UnixMilli()
	var uuid [16]byte

	uuid[0] = byte(now >> 40)
	uuid[1] = byte(now >> 32)
	uuid[2] = byte(now >> 24)
	uuid[3] = byte(now >> 16)
	uuid[4] = byte(now >> 8)
	uuid[5] = byte(now)

	rand.Read(uuid[6:])

	uuid[6] = (uuid[6] & 0x0f) | 0x70
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}
