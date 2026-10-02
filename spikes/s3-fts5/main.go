//go:build spike

// Spike S3: FTS5 behaviours the P1 schema depends on. Answers: is the
// trigram tokenizer compiled in; does an external-content table with
// insert/delete/update triggers round-trip; what does `porter unicode61`
// do to identifiers; do highlight(), bm25() and integrity-check work.
// Run: make spike NAME=s3-fts5
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "spike failed:", err)
		os.Exit(1)
	}
}

func run() error {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Println("# S3: FTS5 behaviours")
	fmt.Println()

	// trigram tokenizer present?
	_, err = db.Exec(`CREATE VIRTUAL TABLE tri USING fts5(t, tokenize='trigram')`)
	fmt.Printf("- trigram tokenizer available: %v", err == nil)
	if err != nil {
		fmt.Printf(" (%v)", err)
	}
	fmt.Println()
	if err == nil {
		db.Exec(`INSERT INTO tri(t) VALUES ('call useCallback inside render')`)
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM tri WHERE t MATCH 'Callback'`).Scan(&n)
		fmt.Printf("- trigram substring match 'Callback' in 'useCallback': %d row(s)\n", n)
	}

	// external-content table + triggers
	stmts := []string{
		`CREATE TABLE chunks(id INTEGER PRIMARY KEY, text TEXT NOT NULL)`,
		`CREATE VIRTUAL TABLE chunks_fts USING fts5(text, content='chunks', content_rowid='id', tokenize='porter unicode61 remove_diacritics 2')`,
		`CREATE VIRTUAL TABLE chunks_exact USING fts5(text, content='chunks', content_rowid='id', tokenize="unicode61 tokenchars '_.:-/'")`,
		`CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
		   INSERT INTO chunks_fts(rowid, text) VALUES (new.id, new.text);
		   INSERT INTO chunks_exact(rowid, text) VALUES (new.id, new.text); END`,
		`CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
		   INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.id, old.text);
		   INSERT INTO chunks_exact(chunks_exact, rowid, text) VALUES ('delete', old.id, old.text); END`,
		`CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
		   INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES ('delete', old.id, old.text);
		   INSERT INTO chunks_fts(rowid, text) VALUES (new.id, new.text);
		   INSERT INTO chunks_exact(chunks_exact, rowid, text) VALUES ('delete', old.id, old.text);
		   INSERT INTO chunks_exact(rowid, text) VALUES (new.id, new.text); END`,
		`INSERT INTO chunks(text) VALUES ('Reviewing the net/http handler: useCallback fires ERR_CONN_RESET when interceptors reorder')`,
		`INSERT INTO chunks(text) VALUES ('Second chunk about authentication reviews')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	count := func(q string, args ...any) int {
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			fmt.Printf("  (query error: %v)\n", err)
			return -1
		}
		return n
	}
	fmt.Println("- porter unicode61 (stemmed index):")
	for _, term := range []string{"review", "reviewing", "http", "net/http", "usecallback", "err_conn_reset", "ERR_CONN_RESET"} {
		fmt.Printf("    %-16s -> %d row(s)\n", term, count(`SELECT COUNT(*) FROM chunks_fts WHERE text MATCH ?`, `"`+term+`"`))
	}
	fmt.Println("- unicode61 tokenchars '_.:-/' (exact index):")
	for _, term := range []string{"review", "reviewing", "http", "net/http", "useCallback", "ERR_CONN_RESET", "conn"} {
		fmt.Printf("    %-16s -> %d row(s)\n", term, count(`SELECT COUNT(*) FROM chunks_exact WHERE text MATCH ?`, `"`+term+`"`))
	}

	// update + delete through triggers
	db.Exec(`UPDATE chunks SET text = 'Totally different words now' WHERE id = 1`)
	fmt.Printf("- after UPDATE: old term 'interceptors' rows = %d, new term 'different' rows = %d\n",
		count(`SELECT COUNT(*) FROM chunks_fts WHERE text MATCH 'interceptors'`), count(`SELECT COUNT(*) FROM chunks_fts WHERE text MATCH 'different'`))
	db.Exec(`DELETE FROM chunks WHERE id = 1`)
	fmt.Printf("- after DELETE: 'different' rows = %d\n", count(`SELECT COUNT(*) FROM chunks_fts WHERE text MATCH 'different'`))

	// highlight, bm25, integrity-check
	var hl string
	var score float64
	err = db.QueryRow(`SELECT highlight(chunks_fts, 0, '[', ']'), bm25(chunks_fts) FROM chunks_fts WHERE text MATCH 'authentication' `).Scan(&hl, &score)
	fmt.Printf("- highlight()/bm25(): err=%v highlight=%q bm25=%.3f\n", err, hl, score)
	_, err = db.Exec(`INSERT INTO chunks_fts(chunks_fts) VALUES ('integrity-check')`)
	fmt.Printf("- integrity-check on external-content table: err=%v\n", err)
	return nil
}
