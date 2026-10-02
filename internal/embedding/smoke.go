package embedding

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Smoke downloads and loads one registry model, checks that it embeds
// sanely (two paraphrases closer than an unrelated sentence) and times a
// ~250-token input alone and in a batch of 16. It returns one markdown table
// row. Failures are rows too: the point is to learn which models run.
func Smoke(ctx context.Context, info ModelInfo, modelDir string) string {
	t0 := time.Now()
	e, cleanup, err := Load(ctx, info, modelDir)
	if err != nil {
		return fmt.Sprintf("| %s | **no** | - | - | - | - | load: %s |", info.ID, short(err))
	}
	defer cleanup()
	loadMs := time.Since(t0).Milliseconds()
	a := "The gRPC interceptor order determines which middleware sees the request first."
	a2 := "Middleware ordering in gRPC interceptors decides who handles the call first."
	b := "I went for a long walk by the river after lunch and watched the ducks."
	vs, err := e.EmbedBatch(ctx, []string{a, a2, b}, RoleDocument)
	if err != nil || len(vs) != 3 {
		return fmt.Sprintf("| %s | yes | **no** | - | - | - | embed: %v |", info.ID, err)
	}
	saa, sab := cosineSim(vs[0], vs[1]), cosineSim(vs[0], vs[2])
	long := strings.Repeat("Interceptors reorder requests when the auth middleware is registered after logging. ", 14)
	p1 := median(5, func() error { _, err := e.EmbedBatch(ctx, []string{long}, RoleDocument); return err })
	batch := make([]string, 16)
	for i := range batch {
		batch[i] = long
	}
	p16 := median(3, func() error { _, err := e.EmbedBatch(ctx, batch, RoleDocument); return err })
	return fmt.Sprintf("| %s | yes (%d ms) | %v (sim(a,a′)=%.2f sim(a,b)=%.2f) | %d | %.0f | %.0f | %s |", info.ID, loadMs, saa > sab, saa, sab, len(vs[0]), p1, p16, info.Licence)
}

func cosineSim(x, y []float32) float64 {
	var d, nx, ny float64
	for i := range x {
		d += float64(x[i]) * float64(y[i])
		nx += float64(x[i]) * float64(x[i])
		ny += float64(y[i]) * float64(y[i])
	}
	if nx == 0 || ny == 0 {
		return 0
	}
	return d / (math.Sqrt(nx) * math.Sqrt(ny))
}

func median(k int, f func() error) float64 {
	ds := make([]float64, 0, k)
	for i := 0; i < k; i++ {
		t := time.Now()
		if err := f(); err != nil {
			return math.NaN()
		}
		ds = append(ds, float64(time.Since(t))/1e6)
	}
	sort.Float64s(ds)
	return ds[k/2]
}

func short(err error) string {
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
