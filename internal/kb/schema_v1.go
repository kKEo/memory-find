package kb

import (
	"context"
	"database/sql"
	"fmt"
)

// schemaV1 is migration 1: layers L0–L3 plus bookkeeping. Every column is
// explained in docs/schema.md §5. FTS5 and the plain vector tables are
// created separately below because virtual tables and PRAGMAs cannot share
// one Exec with ordinary DDL in every driver configuration.
const schemaV1 = `
CREATE TABLE namespaces (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE TABLE models (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    hf_repo            TEXT NOT NULL,
    hf_revision        TEXT NOT NULL DEFAULT '',
    onnx_path          TEXT NOT NULL DEFAULT '',
    external_data_path TEXT,
    dim                INTEGER NOT NULL,
    max_tokens         INTEGER NOT NULL,
    query_prefix       TEXT NOT NULL DEFAULT '',
    doc_prefix         TEXT NOT NULL DEFAULT '',
    normalize          INTEGER NOT NULL DEFAULT 1,
    licence            TEXT NOT NULL DEFAULT '',
    sha256             TEXT NOT NULL DEFAULT '',
    installed_at       INTEGER NOT NULL,
    is_default         INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE jobs (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('ingest','embed','reindex','compact')),
    scope      TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL CHECK (state IN ('queued','running','done','failed')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    items_json TEXT NOT NULL DEFAULT '[]',
    error      TEXT
);
CREATE INDEX idx_jobs_state ON jobs(state);

CREATE TABLE audit (
    id          INTEGER PRIMARY KEY,
    ts          INTEGER NOT NULL,
    actor       TEXT NOT NULL,
    channel     TEXT NOT NULL CHECK (channel IN ('tool','elicitation','cli','worker')),
    op          TEXT NOT NULL,
    target_uri  TEXT NOT NULL,
    detail_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_ts ON audit(ts);

CREATE TABLE query_log (
    id           INTEGER PRIMARY KEY,
    ts           INTEGER NOT NULL,
    args_json    TEXT NOT NULL,
    mode         TEXT NOT NULL DEFAULT '',
    profile      TEXT NOT NULL DEFAULT '',
    model_id     TEXT NOT NULL DEFAULT '',
    n_results    INTEGER NOT NULL DEFAULT 0,
    top_uris_json TEXT NOT NULL DEFAULT '[]',
    latency_ms   INTEGER NOT NULL DEFAULT 0,
    trace_json   TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_query_log_ts ON query_log(ts);

-- L0
CREATE TABLE sources (
    id           TEXT PRIMARY KEY,
    namespace    TEXT NOT NULL REFERENCES namespaces(name),
    uri          TEXT,
    title        TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL CHECK (kind IN ('doc','note','code','conversation')),
    library      TEXT,
    content_hash TEXT NOT NULL DEFAULT '',
    etag         TEXT,
    fetched_at   INTEGER NOT NULL,
    ttl_s        INTEGER,
    trust        TEXT NOT NULL CHECK (trust IN ('curated','user','agent')),
    origin       TEXT NOT NULL CHECK (origin IN ('web','user-said','agent-derived')),
    tags_json    TEXT NOT NULL DEFAULT '[]',
    created_at   INTEGER NOT NULL
);
CREATE INDEX idx_sources_namespace ON sources(namespace);
CREATE INDEX idx_sources_library ON sources(library);
CREATE UNIQUE INDEX idx_sources_ns_uri ON sources(namespace, uri) WHERE uri IS NOT NULL;

-- L1
CREATE TABLE documents (
    id             TEXT PRIMARY KEY,
    source_id      TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    revision       INTEGER NOT NULL,
    version        TEXT,
    content        TEXT NOT NULL,
    context        TEXT,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    deleted_at     INTEGER,
    deleted_reason TEXT,
    superseded_by  TEXT REFERENCES documents(id),
    UNIQUE (source_id, revision)
);
CREATE INDEX idx_documents_source ON documents(source_id);
CREATE INDEX idx_documents_version ON documents(version);
CREATE INDEX idx_documents_live ON documents(source_id) WHERE deleted_at IS NULL AND superseded_by IS NULL;

-- L2
CREATE TABLE chunks (
    id             INTEGER PRIMARY KEY,
    document_id    TEXT NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    ord            INTEGER NOT NULL,
    section_path   TEXT NOT NULL DEFAULT '',
    text           TEXT NOT NULL,
    context_header TEXT NOT NULL DEFAULT '',
    est_tokens     INTEGER NOT NULL DEFAULT 0,
    lang           TEXT,
    UNIQUE (document_id, ord)
);

CREATE TABLE chunk_vecs (
    chunk_id  INTEGER NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    model_id  TEXT NOT NULL REFERENCES models(id),
    embedding BLOB NOT NULL,
    PRIMARY KEY (chunk_id, model_id)
);
CREATE INDEX idx_chunk_vecs_model ON chunk_vecs(model_id);

-- L3
CREATE TABLE facts (
    id                TEXT PRIMARY KEY,
    namespace         TEXT NOT NULL REFERENCES namespaces(name),
    statement         TEXT NOT NULL,
    subject_entity_id TEXT,
    about_json        TEXT NOT NULL DEFAULT '[]',
    valid_from        INTEGER,
    valid_to          INTEGER,
    recorded_at       INTEGER NOT NULL,
    invalidated_at    INTEGER,
    superseded_by     TEXT REFERENCES facts(id),
    evidence_chunk_id INTEGER REFERENCES chunks(id) ON DELETE SET NULL,
    trust             TEXT NOT NULL CHECK (trust IN ('curated','user','agent')),
    origin            TEXT NOT NULL CHECK (origin IN ('web','user-said','agent-derived')),
    deleted_at        INTEGER,
    deleted_reason    TEXT
);
CREATE INDEX idx_facts_namespace ON facts(namespace);
CREATE INDEX idx_facts_live ON facts(namespace) WHERE deleted_at IS NULL AND invalidated_at IS NULL;

CREATE TABLE fact_vecs (
    fact_id   TEXT NOT NULL REFERENCES facts(id) ON DELETE CASCADE,
    model_id  TEXT NOT NULL REFERENCES models(id),
    embedding BLOB NOT NULL,
    PRIMARY KEY (fact_id, model_id)
);
`

// ftsV1 creates the external-content keyword indexes and the triggers that
// keep them in step with their base tables (spike S3 proved the pattern).
var ftsV1 = []string{
	// Stemmed index over text and context header (owner decision: both).
	`CREATE VIRTUAL TABLE chunks_fts USING fts5(
	    context_header, text,
	    content='chunks', content_rowid='id',
	    tokenize='porter unicode61 remove_diacritics 2')`,
	// Exact-identifier index over text only.
	`CREATE VIRTUAL TABLE chunks_fts_exact USING fts5(
	    text,
	    content='chunks', content_rowid='id',
	    tokenize="unicode61 tokenchars '_.:-/'")`,
	`CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
	    INSERT INTO chunks_fts(rowid, context_header, text) VALUES (new.id, new.context_header, new.text);
	    INSERT INTO chunks_fts_exact(rowid, text) VALUES (new.id, new.text);
	END`,
	`CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
	    INSERT INTO chunks_fts(chunks_fts, rowid, context_header, text) VALUES ('delete', old.id, old.context_header, old.text);
	    INSERT INTO chunks_fts_exact(chunks_fts_exact, rowid, text) VALUES ('delete', old.id, old.text);
	END`,
	`CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
	    INSERT INTO chunks_fts(chunks_fts, rowid, context_header, text) VALUES ('delete', old.id, old.context_header, old.text);
	    INSERT INTO chunks_fts(rowid, context_header, text) VALUES (new.id, new.context_header, new.text);
	    INSERT INTO chunks_fts_exact(chunks_fts_exact, rowid, text) VALUES ('delete', old.id, old.text);
	    INSERT INTO chunks_fts_exact(rowid, text) VALUES (new.id, new.text);
	END`,
	// Facts: stemmed index over the statement.
	`CREATE VIRTUAL TABLE facts_fts USING fts5(
	    statement,
	    content='facts', content_rowid='rowid',
	    tokenize='porter unicode61 remove_diacritics 2')`,
	`CREATE TRIGGER facts_ai AFTER INSERT ON facts BEGIN
	    INSERT INTO facts_fts(rowid, statement) VALUES (new.rowid, new.statement);
	END`,
	`CREATE TRIGGER facts_ad AFTER DELETE ON facts BEGIN
	    INSERT INTO facts_fts(facts_fts, rowid, statement) VALUES ('delete', old.rowid, old.statement);
	END`,
	`CREATE TRIGGER facts_au AFTER UPDATE OF statement ON facts BEGIN
	    INSERT INTO facts_fts(facts_fts, rowid, statement) VALUES ('delete', old.rowid, old.statement);
	    INSERT INTO facts_fts(rowid, statement) VALUES (new.rowid, new.statement);
	END`,
}

func migrateV1(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id = %d`, ApplicationID)); err != nil {
		return fmt.Errorf("set application_id: %w", err)
	}
	if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
		return fmt.Errorf("create tables: %w", err)
	}
	for _, stmt := range ftsV1 {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create fts/triggers: %w\n%s", err, stmt)
		}
	}
	return nil
}
