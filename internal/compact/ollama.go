package compact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Ollama is the optional local executor for page items: it asks a model
// running on the machine to write the page from the payload. Nothing else
// depends on it; the server never calls it. Constrained JSON output, a
// non-thinking model, loopback by default (MEMO_OLLAMA_URL).
type Ollama struct {
	URL   string // http://127.0.0.1:11434
	Model string // e.g. qwen2.5:7b-instruct
	HTTP  *http.Client
}

// NewOllama validates the endpoint (loopback unless allowRemote).
func NewOllama(rawURL, model string, allowRemote bool) (*Ollama, error) {
	if rawURL == "" {
		rawURL = "http://127.0.0.1:11434"
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	host := u.Hostname()
	if !allowRemote && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, fmt.Errorf("ollama url %s is not loopback; pass --allow-remote to use it", rawURL)
	}
	if model == "" {
		return nil, errors.New("an ollama model name is required (MEMO_OLLAMA_MODEL)")
	}
	return &Ollama{URL: strings.TrimRight(rawURL, "/"), Model: model, HTTP: &http.Client{Timeout: 5 * time.Minute}}, nil
}

type ollamaOut struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

var pageSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"title": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
	"required":   []string{"title", "content"},
}

// WritePage asks the model for a page from the payload. The prompt gives
// the passages with their addresses and the facts that must be covered; the
// result is validated by the same omission check as a human submission.
func (o *Ollama) WritePage(ctx context.Context, p PagePayload) (Result, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Write a concise markdown page titled %q from ONLY the passages below. Cite each passage you use by its address in parentheses, e.g. (memo://chunk/12). Cover every fact listed under FACTS. Do not add anything the passages do not say.\n\nPASSAGES\n", p.Entity.Canonical)
	for _, c := range p.Chunks {
		fmt.Fprintf(&sb, "\n[%s] %s\n%s\n", c.URI, c.Title, c.Text)
	}
	sb.WriteString("\nFACTS\n")
	for _, f := range p.MustCover {
		sb.WriteString("- " + f + "\n")
	}
	if p.Previous != "" {
		sb.WriteString("\nPREVIOUS PAGE (update it)\n" + p.Previous + "\n")
	}
	body, _ := json.Marshal(map[string]any{
		"model":    o.Model,
		"stream":   false,
		"think":    false,
		"format":   pageSchema,
		"options":  map[string]any{"temperature": 0},
		"messages": []map[string]string{{"role": "user", "content": sb.String()}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.URL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Result{}, fmt.Errorf("ollama: bad envelope: %w", err)
	}
	var out ollamaOut
	if err := json.Unmarshal([]byte(env.Message.Content), &out); err != nil {
		return Result{}, fmt.Errorf("ollama: model did not return the requested JSON: %w", err)
	}
	if strings.TrimSpace(out.Content) == "" {
		return Result{}, errors.New("ollama: empty page")
	}
	return Result{Title: out.Title, Content: out.Content, Reason: "written by ollama/" + o.Model}, nil
}
