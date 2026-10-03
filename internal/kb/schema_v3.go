package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaV3 is migration 3: layer L5, pages and compaction (roadmap P8,
// docs/schema.md §5.7). A page is curated markdown an agent wrote from
// chunks; it is always derived (is_inference = 1), always cites the chunks
// it was built from (page_sources), and goes stale when one of those chunks'
// documents gets a new revision. work_items is the queue compaction hands to
// the calling agent: the server never writes a page itself.
const schemaV3 = `
CREATE TABLE pages (
    id             TEXT PRIMARY KEY,
    namespace      TEXT NOT NULL REFERENCES namespaces(name),
    kind           TEXT NOT NULL,                 -- entity | topic | overview
    subject_id     TEXT,                          -- entity id for entity pages
    title          TEXT NOT NULL,
    content        TEXT NOT NULL,
    built_at       INTEGER NOT NULL,
    built_from_rev INTEGER NOT NULL DEFAULT 1,    -- bumps on every rebuild
    stale          INTEGER NOT NULL DEFAULT 0,
    stale_reason   TEXT,
    is_inference   INTEGER NOT NULL DEFAULT 1,
    trust          TEXT NOT NULL DEFAULT 'agent',
    actor          TEXT NOT NULL DEFAULT '',
    deleted_at     INTEGER,
    deleted_reason TEXT
);
CREATE INDEX pages_ns ON pages(namespace, kind);
CREATE UNIQUE INDEX pages_subject ON pages(namespace, kind, subject_id) WHERE subject_id IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE page_sources (
    page_id  TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    chunk_id INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    PRIMARY KEY (page_id, chunk_id)
);
CREATE INDEX page_sources_chunk ON page_sources(chunk_id);

CREATE TABLE page_vecs (
    page_id   TEXT NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
    model_id  TEXT NOT NULL REFERENCES models(id),
    embedding BLOB NOT NULL,
    PRIMARY KEY (page_id, model_id)
);

CREATE TABLE work_items (
    id           TEXT PRIMARY KEY,
    namespace    TEXT NOT NULL,
    kind         TEXT NOT NULL,                   -- page | conflict | merge | stale | duplicate
    subject      TEXT NOT NULL,                   -- entity id, fact id pair, page id, chunk id pair
    payload_json TEXT NOT NULL,
    state        TEXT NOT NULL DEFAULT 'open',    -- open | done | skipped
    created_at   INTEGER NOT NULL,
    decided_at   INTEGER,
    result_json  TEXT,
    actor        TEXT
);
CREATE INDEX work_items_state ON work_items(namespace, state, kind);
CREATE UNIQUE INDEX work_items_open ON work_items(kind, subject) WHERE state = 'open';
`

// pagesFTSV3 indexes page text for granularity=page search.
var pagesFTSV3 = []string{
	`CREATE VIRTUAL TABLE pages_fts USING fts5(
	    title, content,
	    content='pages', content_rowid='rowid',
	    tokenize='porter unicode61 remove_diacritics 2')`,
	`CREATE TRIGGER pages_ai AFTER INSERT ON pages BEGIN
	    INSERT INTO pages_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
	END`,
	`CREATE TRIGGER pages_ad AFTER DELETE ON pages BEGIN
	    INSERT INTO pages_fts(pages_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
	END`,
	`CREATE TRIGGER pages_au AFTER UPDATE OF title, content ON pages BEGIN
	    INSERT INTO pages_fts(pages_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
	    INSERT INTO pages_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
	END`,
}

func migrateV3(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, schemaV3); err != nil {
		return fmt.Errorf("create page tables: %w", err)
	}
	for _, stmt := range pagesFTSV3 {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create pages fts: %w\n%s", err, stmt)
		}
	}
	return nil
}
