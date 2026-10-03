// Package kb is the knowledge-base store: one SQLite file holding sources,
// documents, chunks, facts (and later entities and pages) for one or more
// namespaces. The layout is described in docs/schema.md; this package owns
// opening the file, migrating it, and every write to it.
package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ApplicationID is written into the SQLite file header by migration 1 so
// that a knowledge base can be told apart from any other SQLite file —
// in particular from a v1 memo-mcp journal, which has user_version 1 too
// but no application id.
const ApplicationID = 0x4D454D4F // "MEMO"

// ErrNotKnowledgeBase is returned when the file exists, has tables, and is
// not a knowledge base (for example an old journal). Nothing is migrated:
// owner decision 5 (2026-10-02).
var ErrNotKnowledgeBase = errors.New("this file is not a memo-mcp knowledge base (it looks like a v1 memo-mcp journal or another SQLite database); nothing is migrated")

// ErrNoSuchKB is returned by read-only opens when the file does not exist.
var ErrNoSuchKB = errors.New("knowledge base does not exist")

// namePattern constrains MEMO_KB to a safe filename component.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidateName checks a knowledge-base or namespace name.
func ValidateName(name string) error {
	if !namePattern.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid name %q: must match %s and not be \".\" or \"..\"", name, namePattern.String())
	}
	return nil
}

// Options controls how Open behaves.
type Options struct {
	// ReadOnly opens with mode=ro and never creates or migrates the file.
	// Read-only commands (status, read, ls, search, export) use it so a
	// mistyped name cannot create an empty knowledge base.
	ReadOnly bool
	// NoCreate opens read-write but refuses to create a missing file
	// (maintenance commands such as verify need write access for the FTS
	// integrity check yet must not create an empty knowledge base).
	NoCreate bool
}

// Path returns the database file for a knowledge-base name under dir.
func Path(dir, name string) string { return filepath.Join(dir, name+".db") }

// Open opens (and, unless read-only, creates and migrates) the knowledge
// base named name under dir. The directory is created 0700 and the file is
// restricted to the owner.
func Open(ctx context.Context, dir, name string, opts Options) (*sql.DB, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	path := Path(dir, name)

	if opts.ReadOnly || opts.NoCreate {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrNoSuchKB, path)
		}
	}
	if !opts.ReadOnly {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create storage dir: %w", err)
		}
	}

	// Ownership is checked through a separate read-only connection, before
	// the real DSN switches the file to WAL: refusing a legacy journal must
	// leave its bytes untouched.
	if _, err := os.Stat(path); err == nil {
		if err := probeOwnership(ctx, path); err != nil {
			return nil, err
		}
	}

	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate"
	if opts.ReadOnly {
		dsn += "&mode=ro"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection: writes are serialised in-process; WAL + busy_timeout
	// serialise across processes. Embedding never runs while holding it.
	db.SetMaxOpenConns(1)

	if !opts.ReadOnly {
		if err := Migrate(ctx, db); err != nil {
			db.Close()
			return nil, err
		}
		securePermissions(path)
	} else {
		// A read-only open cannot migrate; say so plainly instead of failing
		// later on a missing table.
		var v int
		if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err == nil && v < len(migrations) {
			db.Close()
			return nil, fmt.Errorf("%w: file is at schema v%d, this binary expects v%d; run `memo-mcp migrate` (or any writing command) once", ErrSchemaBehind, v, len(migrations))
		}
	}
	return db, nil
}

// ErrSchemaBehind is returned by a read-only open of a file that an older
// binary wrote and that newer migrations have not been applied to.
var ErrSchemaBehind = errors.New("knowledge base needs migration")

// probeOwnership refuses a file that has tables but is not ours. A brand-new
// empty file (no tables) is fine: migration 1 will claim it.
func probeOwnership(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("probe database: %w", err)
	}
	defer db.Close()
	var appID int64
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&appID); err != nil {
		return fmt.Errorf("read application_id: %w", err)
	}
	if appID == ApplicationID {
		return nil
	}
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	if tables > 0 {
		return ErrNotKnowledgeBase
	}
	return nil
}

// securePermissions best-effort restricts the file and WAL sidecars to the
// owner. Failures are ignored: on platforms without POSIX bits it is a
// no-op.
func securePermissions(path string) {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		_ = os.Chmod(p, 0o600)
	}
}
