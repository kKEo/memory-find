//go:build spike

// Spike S8: does the cross-encoder discriminate at all through hugot's
// text-classification pipeline with "query [SEP] passage" as one string?
// The P3 eval showed the reranker hurting every corpus; this isolates
// whether the scores are meaningful before blaming the model.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kKEo/memors/internal/embedding"
	"github.com/kKEo/memors/internal/rerank"
)

func main() {
	ctx := context.Background()
	info, _ := rerank.Lookup("ms-marco-minilm")
	ce, err := rerank.Load(ctx, info, embedding.DefaultModelDir())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ce.Close()
	q := "how do I feed a sourdough starter"
	passages := []string{
		"Feed the sourdough starter with equal parts flour and water every twelve hours until it doubles in volume.",
		"Simmer pork bones for twelve hours; skim the fat; season the broth with tare just before serving the ramen.",
		"`quorum.RotateKeys3` is part of the quorum client API. It takes a context and the client options.",
		"Interceptors run in registration order. Put the auth interceptor before logging.",
	}
	scores, err := ce.Score(ctx, q, passages)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("query:", q)
	for i, p := range passages {
		fmt.Printf("  %.4f  %s\n", scores[i], p[:60])
	}
	// Same passages, a query about ramen: the order must flip.
	scores, _ = ce.Score(ctx, "how long to simmer ramen broth", passages)
	fmt.Println("query: how long to simmer ramen broth")
	for i, p := range passages {
		fmt.Printf("  %.4f  %s\n", scores[i], p[:60])
	}
}
