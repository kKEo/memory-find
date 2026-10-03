package graph

// CSR is a compressed sparse row adjacency over a namespace's bipartite
// mention graph (entities and chunks are both nodes). It is built lazily per
// namespace and kept in memory; spike S6 measured 10 ms to build and 42 ms
// for PPR at 100k nodes / 1M edges, far above a real namespace's size.
type CSR struct {
	N       int
	Offsets []int32
	Targets []int32
	Weights []float32
	Deg     []int32
}

// Edge is one undirected weighted link between node ids in [0, n).
type Edge struct {
	A, B int32
	W    float32
}

// BuildCSR builds the adjacency from an edge list.
func BuildCSR(n int, edges []Edge) *CSR {
	deg := make([]int32, n)
	for _, e := range edges {
		deg[e.A]++
		deg[e.B]++
	}
	off := make([]int32, n+1)
	for i := 0; i < n; i++ {
		off[i+1] = off[i] + deg[i]
	}
	tg := make([]int32, off[n])
	wt := make([]float32, off[n])
	pos := make([]int32, n)
	copy(pos, off[:n])
	for _, e := range edges {
		tg[pos[e.A]], wt[pos[e.A]] = e.B, e.W
		pos[e.A]++
		tg[pos[e.B]], wt[pos[e.B]] = e.A, e.W
		pos[e.B]++
	}
	return &CSR{N: n, Offsets: off, Targets: tg, Weights: wt, Deg: deg}
}

// PPR is personalised PageRank by power iteration: a walker follows mention
// links and, with probability 1-alpha, jumps back to one of the seeds. Where
// it spends time is what the graph considers relevant to the seeds. Hubs with
// degree above hubCap pass on proportionally less (the hub penalty), so an
// entity mentioned everywhere does not flood the ranking.
func (g *CSR) PPR(seeds map[int32]float32, alpha float64, rounds int, hubCap int32) []float32 {
	rank := make([]float32, g.N)
	next := make([]float32, g.N)
	restart := make([]float32, g.N)
	total := float32(0)
	for _, w := range seeds {
		total += w
	}
	if total == 0 {
		return rank
	}
	for s, w := range seeds {
		restart[s] = w / total
	}
	copy(rank, restart)
	for it := 0; it < rounds; it++ {
		for i := range next {
			next[i] = float32(1-alpha) * restart[i]
		}
		for u := 0; u < g.N; u++ {
			d := g.Deg[u]
			if d == 0 || rank[u] == 0 {
				continue
			}
			// Weighted share: split by edge weight over the node's total.
			wsum := float32(0)
			for _, w := range g.Weights[g.Offsets[u]:g.Offsets[u+1]] {
				wsum += w
			}
			if wsum == 0 {
				continue
			}
			share := float32(alpha) * rank[u] / wsum
			if d > hubCap {
				share *= float32(hubCap) / float32(d)
			}
			for i := g.Offsets[u]; i < g.Offsets[u+1]; i++ {
				next[g.Targets[i]] += share * g.Weights[i]
			}
		}
		rank, next = next, rank
	}
	return rank
}
