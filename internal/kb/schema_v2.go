package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaV2 is migration 2: layer L4, the graph as an index (roadmap P7,
// docs/schema.md §5.6). Entities are the things the knowledge talks about;
// mentions link them to the chunks that talk about them (an edge with no
// relation type). Typed edges are optional and bi-temporal. merge_candidates
// is the review queue of the deterministic resolver: nothing is merged
// without a decision recorded here. facts.subject_entity_id already exists
// in migration 1 and is filled from here on.
const schemaV2 = `
CREATE TABLE entities (
    id              TEXT PRIMARY KEY,
    namespace       TEXT NOT NULL REFERENCES namespaces(name),
    canonical       TEXT NOT NULL,
    key             TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT 'name',
    summary_page_id TEXT,
    created_at      INTEGER NOT NULL,
    UNIQUE (namespace, key)
);

CREATE TABLE entity_aliases (
    alias     TEXT NOT NULL,
    key       TEXT NOT NULL,
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    PRIMARY KEY (entity_id, key)
);
CREATE INDEX entity_aliases_key ON entity_aliases(key);

CREATE TABLE mentions (
    entity_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    chunk_id  INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    weight    REAL NOT NULL DEFAULT 1.0,
    PRIMARY KEY (entity_id, chunk_id)
);
CREATE INDEX mentions_chunk ON mentions(chunk_id);

CREATE TABLE merge_candidates (
    id         INTEGER PRIMARY KEY,
    a          TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    b          TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    score      REAL NOT NULL,
    reason     TEXT NOT NULL,
    state      TEXT NOT NULL DEFAULT 'open',   -- open | merged | rejected
    created_at INTEGER NOT NULL,
    decided_at INTEGER,
    UNIQUE (a, b)
);

CREATE TABLE edges (
    id                INTEGER PRIMARY KEY,
    src               TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    dst               TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    rel               TEXT NOT NULL,
    weight            REAL NOT NULL DEFAULT 1.0,
    valid_from        INTEGER,
    valid_to          INTEGER,
    recorded_at       INTEGER NOT NULL,
    invalidated_at    INTEGER,
    evidence_chunk_id INTEGER REFERENCES chunks(id) ON DELETE SET NULL,
    UNIQUE (src, dst, rel, recorded_at)
);
CREATE INDEX edges_src ON edges(src);
CREATE INDEX edges_dst ON edges(dst);
`

func migrateV2(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, schemaV2); err != nil {
		return fmt.Errorf("create graph tables: %w", err)
	}
	return nil
}
