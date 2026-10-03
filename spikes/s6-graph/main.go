//go:build spike

// Spike S6: CSR adjacency and personalised PageRank at 100k nodes / 1M
// edges on a power-law graph. Budget (roadmap §6): PPR under 50 ms,
// adjacency load under 500 ms. Also measures a fixed-depth two-hop
// neighbourhood query in SQLite against a recursive CTE, which is the choice
// the graph arm makes when it falls back to SQL.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	_ "modernc.org/sqlite"
)

type csr struct {
	offsets []int32
	targets []int32
	deg     []int32
}

func buildCSR(n int, edges [][2]int32) csr {
	deg := make([]int32, n)
	for _, e := range edges {
		deg[e[0]]++
		deg[e[1]]++
	}
	off := make([]int32, n+1)
	for i := 0; i < n; i++ {
		off[i+1] = off[i] + deg[i]
	}
	tg := make([]int32, off[n])
	pos := make([]int32, n)
	copy(pos, off[:n])
	for _, e := range edges {
		tg[pos[e[0]]] = e[1]
		pos[e[0]]++
		tg[pos[e[1]]] = e[0]
		pos[e[1]]++
	}
	return csr{offsets: off, targets: tg, deg: deg}
}

// ppr is the ~20-line personalised PageRank: a walker restarts at the seeds
// with probability 1-alpha; hubs above hubCap contribute as if they had
// hubCap neighbours (the hub penalty). Power iteration, fixed rounds.
func ppr(g csr, seeds []int32, alpha float64, rounds int, hubCap int32) []float32 {
	n := len(g.deg)
	rank := make([]float32, n)
	next := make([]float32, n)
	restart := make([]float32, n)
	for _, s := range seeds {
		restart[s] += float32(1.0 / float64(len(seeds)))
	}
	copy(rank, restart)
	for it := 0; it < rounds; it++ {
		for i := range next {
			next[i] = float32(1-alpha) * restart[i]
		}
		for u := 0; u < n; u++ {
			d := g.deg[u]
			if d == 0 || rank[u] == 0 {
				continue
			}
			share := float32(alpha) * rank[u] / float32(d)
			if d > hubCap {
				share *= float32(hubCap) / float32(d)
			}
			for _, v := range g.targets[g.offsets[u]:g.offsets[u+1]] {
				next[v] += share
			}
		}
		rank, next = next, rank
	}
	return rank
}

func main() {
	n, m := 100_000, 1_000_000
	rng := rand.New(rand.NewSource(7))
	// Power-law degrees: preferential attachment via a growing endpoint pool.
	pool := []int32{0, 1}
	edges := make([][2]int32, 0, m)
	for len(edges) < m {
		u := int32(rng.Intn(n))
		v := pool[rng.Intn(len(pool))]
		if u == v {
			continue
		}
		edges = append(edges, [2]int32{u, v})
		pool = append(pool, u, v)
	}
	t0 := time.Now()
	g := buildCSR(n, edges)
	build := time.Since(t0)
	maxDeg := int32(0)
	for _, d := range g.deg {
		if d > maxDeg {
			maxDeg = d
		}
	}
	seeds := []int32{42, 4242, 42424}
	t0 = time.Now()
	r := ppr(g, seeds, 0.85, 20, 200)
	pprT := time.Since(t0)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return r[idx[a]] > r[idx[b]] })
	fmt.Printf("nodes %d edges %d max degree %d\n", n, m, maxDeg)
	fmt.Printf("CSR build: %v (%.0f MB)\n", build, float64(4*(len(g.offsets)+len(g.targets)+len(g.deg)))/1e6)
	fmt.Printf("PPR 20 rounds, 3 seeds, hub cap 200: %v; top5 %v\n", pprT, idx[:5])
	t0 = time.Now()
	_ = ppr(g, seeds, 0.85, 20, math.MaxInt32)
	fmt.Printf("PPR without hub cap: %v\n", time.Since(t0))
	t0 = time.Now()
	r10 := ppr(g, seeds, 0.85, 10, 200)
	idx10 := make([]int, n)
	for i := range idx10 {
		idx10[i] = i
	}
	sort.Slice(idx10, func(a, b int) bool { return r10[idx10[a]] > r10[idx10[b]] })
	same := 0
	for i := 0; i < 20; i++ {
		for j := 0; j < 20; j++ {
			if idx[i] == idx10[j] {
				same++
			}
		}
	}
	fmt.Printf("PPR 10 rounds: %v; top-20 overlap with 20 rounds: %d/20\n", time.Since(t0), same)

	// SQL: two-hop neighbourhood of one entity, fixed-depth joins vs recursive CTE.
	db, err := sql.Open("sqlite", "file:"+os.TempDir()+"/s6.db?_pragma=journal_mode(WAL)&_pragma=synchronous(OFF)")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	ctx := context.Background()
	db.ExecContext(ctx, `DROP TABLE IF EXISTS edges`)
	db.ExecContext(ctx, `CREATE TABLE edges(src INTEGER, dst INTEGER)`)
	tx, _ := db.BeginTx(ctx, nil)
	st, _ := tx.PrepareContext(ctx, `INSERT INTO edges VALUES (?,?)`)
	for _, e := range edges[:200_000] {
		st.ExecContext(ctx, e[0], e[1])
	}
	st.Close()
	tx.Commit()
	db.ExecContext(ctx, `CREATE INDEX edges_src ON edges(src)`)
	db.ExecContext(ctx, `CREATE INDEX edges_dst ON edges(dst)`)
	seed := 1
	t0 = time.Now()
	var fixed int
	db.QueryRowContext(ctx, `WITH h1 AS (SELECT dst AS n FROM edges WHERE src = ?1 UNION SELECT src FROM edges WHERE dst = ?1)
		SELECT COUNT(DISTINCT n) FROM (SELECT e.dst AS n FROM h1 JOIN edges e ON e.src = h1.n UNION ALL SELECT e.src FROM h1 JOIN edges e ON e.dst = h1.n UNION ALL SELECT n FROM h1)`, seed).Scan(&fixed)
	fixedT := time.Since(t0)
	t0 = time.Now()
	var rec int
	db.QueryRowContext(ctx, `WITH RECURSIVE walk(n, depth) AS (SELECT ?1, 0 UNION SELECT CASE WHEN e.src = w.n THEN e.dst ELSE e.src END, w.depth+1 FROM walk w JOIN edges e ON (e.src = w.n OR e.dst = w.n) WHERE w.depth < 2)
		SELECT COUNT(DISTINCT n) FROM walk`, seed).Scan(&rec)
	recT := time.Since(t0)
	fmt.Printf("SQL two-hop of hub node (200k edges): fixed-depth joins %d nodes in %v; recursive CTE %d nodes in %v\n", fixed, fixedT, rec, recT)
}
