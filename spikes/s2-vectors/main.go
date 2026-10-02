//go:build spike

// Spike S2: vec0 virtual table vs a plain table scanned with
// vec_distance_cosine(). Answers: latency at several corpus sizes and
// dimensions; whether `rowid IN (subquery)` pushes down into vec0 KNN;
// whether CREATE VIRTUAL TABLE rolls back inside a transaction.
// Run: make spike NAME=s2-vectors
package main

import (
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "spike failed:", err)
		os.Exit(1)
	}
}

func vecJSON(v []float32) string {
	var sb strings.Builder
	sb.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%.6f", x)
	}
	sb.WriteByte(']')
	return sb.String()
}

func randUnit(r *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	var n float64
	for i := range v {
		v[i] = float32(r.NormFloat64())
		n += float64(v[i]) * float64(v[i])
	}
	n = math.Sqrt(n)
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}

func run() error {
	fmt.Println("# S2: vec0 vs plain table (vec_distance_cosine)")
	fmt.Println()
	fmt.Println("| chunks | dim | vec0 KNN p50 | plain scan p50 | ratio |")
	fmt.Println("|---|---|---|---|---|")
	for _, n := range []int{10000, 50000} {
		for _, dim := range []int{384, 768} {
			if err := bench(n, dim); err != nil {
				return err
			}
		}
	}
	fmt.Println()
	return semantics()
}

func bench(n, dim int) error {
	db, err := sql.Open("sqlite", "file:"+os.TempDir()+fmt.Sprintf("/s2-%d-%d.db?_pragma=journal_mode(WAL)&_pragma=synchronous(OFF)", n, dim))
	if err != nil {
		return err
	}
	defer db.Close()
	for _, q := range []string{
		`DROP TABLE IF EXISTS plain`, `DROP TABLE IF EXISTS v0`,
		`CREATE TABLE plain(id INTEGER PRIMARY KEY, embedding BLOB)`,
		fmt.Sprintf(`CREATE VIRTUAL TABLE v0 USING vec0(id INTEGER PRIMARY KEY, embedding float[%d] distance_metric=cosine)`, dim),
	} {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	r := rand.New(rand.NewSource(1))
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	p1, _ := tx.Prepare(`INSERT INTO plain(id, embedding) VALUES (?, vec_f32(?))`)
	p2, _ := tx.Prepare(`INSERT INTO v0(id, embedding) VALUES (?, ?)`)
	for i := 1; i <= n; i++ {
		j := vecJSON(randUnit(r, dim))
		if _, err := p1.Exec(i, j); err != nil {
			return fmt.Errorf("insert plain: %w", err)
		}
		if _, err := p2.Exec(i, j); err != nil {
			return fmt.Errorf("insert vec0: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	q := vecJSON(randUnit(r, dim))
	t1 := median(7, func() error {
		rows, err := db.Query(`SELECT id, distance FROM v0 WHERE embedding MATCH ? AND k = 30 ORDER BY distance`, q)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		return rows.Close()
	})
	t2 := median(7, func() error {
		rows, err := db.Query(`SELECT id, vec_distance_cosine(embedding, vec_f32(?)) AS d FROM plain ORDER BY d LIMIT 30`, q)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		return rows.Close()
	})
	fmt.Printf("| %d | %d | %.1f ms | %.1f ms | %.2fx |\n", n, dim, ms(t1), ms(t2), float64(t2)/float64(t1))
	return nil
}

func ms(d time.Duration) float64 { return float64(d) / 1e6 }

func median(k int, f func() error) time.Duration {
	ds := make([]time.Duration, 0, k)
	for i := 0; i < k; i++ {
		t := time.Now()
		if err := f(); err != nil {
			fmt.Fprintln(os.Stderr, "query error:", err)
			return 0
		}
		ds = append(ds, time.Since(t))
	}
	for i := range ds {
		for j := i + 1; j < len(ds); j++ {
			if ds[j] < ds[i] {
				ds[i], ds[j] = ds[j], ds[i]
			}
		}
	}
	return ds[k/2]
}

func semantics() error {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Println("## Semantics checks")
	// (a) cosine returns 1-cos? identical vectors -> 0.
	var d float64
	if err := db.QueryRow(`SELECT vec_distance_cosine(vec_f32('[1,0,0]'), vec_f32('[1,0,0]'))`).Scan(&d); err != nil {
		return err
	}
	fmt.Printf("- vec_distance_cosine(identical) = %.4f (0 means distance = 1 - cos)\n", d)
	if err := db.QueryRow(`SELECT vec_distance_cosine(vec_f32('[1,0,0]'), vec_f32('[0,1,0]'))`).Scan(&d); err != nil {
		return err
	}
	fmt.Printf("- vec_distance_cosine(orthogonal) = %.4f\n", d)

	// (b) rowid IN (subquery) pushdown on vec0 KNN.
	if _, err := db.Exec(`CREATE VIRTUAL TABLE v USING vec0(id INTEGER PRIMARY KEY, embedding float[3] distance_metric=cosine)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE allowed(id INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	for i := 1; i <= 5; i++ {
		if _, err := db.Exec(`INSERT INTO v(id, embedding) VALUES (?, ?)`, i, fmt.Sprintf("[%d,1,0]", i)); err != nil {
			return err
		}
	}
	db.Exec(`INSERT INTO allowed VALUES (2),(4)`)
	rows, err := db.Query(`SELECT id FROM v WHERE embedding MATCH '[1,1,0]' AND k = 5 AND id IN (SELECT id FROM allowed) ORDER BY distance`)
	if err != nil {
		fmt.Printf("- rowid IN (subquery) on vec0 KNN: ERROR %v\n", err)
	} else {
		var ids []int
		for rows.Next() {
			var id int
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		fmt.Printf("- rowid IN (subquery) on vec0 KNN: returned %v (want [2 4] or [4 2])\n", ids)
	}
	rows, err = db.Query(`SELECT id FROM v WHERE embedding MATCH '[1,1,0]' AND k = 5 AND id IN (2,4) ORDER BY distance`)
	if err != nil {
		fmt.Printf("- rowid IN (literal list) on vec0 KNN: ERROR %v\n", err)
	} else {
		var ids []int
		for rows.Next() {
			var id int
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		fmt.Printf("- rowid IN (literal list) on vec0 KNN: returned %v\n", ids)
	}

	// (c) CREATE VIRTUAL TABLE rollback inside a transaction.
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE rolled USING vec0(id INTEGER PRIMARY KEY, embedding float[3])`); err != nil {
		return err
	}
	tx.Rollback()
	var cnt int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'rolled%'`).Scan(&cnt)
	fmt.Printf("- CREATE VIRTUAL TABLE rolled back cleanly: %v (%d leftover sqlite_master rows)\n", cnt == 0, cnt)
	return nil
}
