package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kKEo/memory-find/internal/embedding"
	"github.com/kKEo/memory-find/internal/eval"
	"github.com/kKEo/memory-find/internal/kb"
	"github.com/kKEo/memory-find/internal/rerank"
	"github.com/kKEo/memory-find/internal/retrieve"
)

// openStore resolves the configuration and opens the knowledge base. embedder
// may be nil (keyword-only writes; vectors are queued).
func openStore(ctx context.Context, stderr io.Writer, opts kb.Options, embedder embedding.Embedder) (*kb.Store, func(), error) {
	cfg, err := ResolveConfig(os.Getenv)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range cfg.Deprecations {
		fmt.Fprintln(stderr, "warning:", d)
	}
	db, err := kb.Open(ctx, cfg.KBDir, cfg.DBName, opts)
	if err != nil {
		return nil, nil, err
	}
	return kb.NewStore(db, embedder), func() { db.Close() }, nil
}

// loadEmbedder builds the real embedder unless the caller asked to skip
// embedding; a failure degrades to "queue the vectors" with a warning.
func loadEmbedder(ctx context.Context, want bool, stderr io.Writer) (embedding.Embedder, func()) {
	if !want {
		return nil, func() {}
	}
	e, cleanup, err := buildEmbedder(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "warning: embedding unavailable (%v); vectors will be queued for `memo-mcp backfill`\n", err)
		return nil, func() {}
	}
	return e, cleanup
}

func runIngest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ns := fs.String("ns", "default", "namespace (shelf) to write to")
	kind := fs.String("kind", kb.KindDoc, "doc|note|code|conversation")
	uri := fs.String("uri", "", "where the content came from (URL); empty for notes")
	title := fs.String("title", "", "title (default: first heading or file name)")
	library := fs.String("library", "", "library the docs belong to, e.g. grpc/grpc-go")
	version := fs.String("version", "", "version, tag or commit the content belongs to")
	trust := fs.String("trust", kb.TrustUser, "user|curated (CLI writes default to user)")
	origin := fs.String("origin", "", "web|user-said|agent-derived (default: web when --uri is set, else user-said)")
	docCtx := fs.String("context", "", "one sentence of context prepended to every chunk")
	embed := fs.Bool("embed", true, "embed chunks now (false: queue vectors for `memo-mcp backfill`)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: memo-mcp ingest <file|dir|-> [flags]")
	}
	if *trust != kb.TrustUser && *trust != kb.TrustCurated {
		return fmt.Errorf("--trust must be user or curated (agent is reserved for tool calls)")
	}
	if *origin == "" {
		*origin = kb.OriginUserSaid
		if *uri != "" {
			*origin = kb.OriginWeb
		}
	}

	embedder, cleanup := loadEmbedder(ctx, *embed, stderr)
	defer cleanup()
	store, closeFn, err := openStore(ctx, stderr, kb.Options{}, embedder)
	if err != nil {
		return err
	}
	defer closeFn()

	type item struct {
		name    string
		content string
	}
	var items []item
	target := positional[0]
	switch target {
	case "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		items = append(items, item{name: "stdin", content: string(b)})
	default:
		info, err := os.Stat(target)
		if err != nil {
			return err
		}
		if info.IsDir() {
			err = filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || d.Name() == "_index.md" || strings.HasPrefix(d.Name(), ".") {
					return nil
				}
				if *kind != kb.KindCode && !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
					return nil
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				items = append(items, item{name: p, content: string(b)})
				return nil
			})
			if err != nil {
				return err
			}
		} else {
			b, err := os.ReadFile(target)
			if err != nil {
				return err
			}
			items = append(items, item{name: target, content: string(b)})
		}
	}
	if len(items) == 0 {
		return errors.New("nothing to ingest (no .md files found)")
	}

	var written, dedup, pending int
	for _, it := range items {
		fm, body := kb.ParseExported(it.content)
		if firstNonEmpty(fm.Kind, *kind) == kb.KindCode && fm.MemoURI == "" {
			body = fenceCode(it.name, body)
		}
		in := kb.IngestInput{
			Namespace: firstNonEmpty(fm.Namespace, *ns),
			Content:   body,
			Context:   firstNonEmpty(fm.Context, *docCtx),
			Trust:     *trust,
			Actor:     "cli",
			Channel:   kb.ChannelCLI,
			Source: kb.SourceInput{
				URI:     firstNonEmpty(fm.SourceURI, *uri),
				Title:   firstNonEmpty(fm.Title, *title, firstHeading(body), filepath.Base(it.name)),
				Kind:    firstNonEmpty(fm.Kind, *kind),
				Library: firstNonEmpty(fm.Library, *library),
				Version: firstNonEmpty(fm.Version, *version),
				Origin:  firstNonEmpty(fm.Origin, *origin),
			},
		}
		if fm.MemoURI != "" {
			if _, id, err := kb.ParseURI(fm.MemoURI); err == nil {
				in.DocumentID = id
			}
		}
		res, err := store.Ingest(ctx, in)
		if err != nil {
			return fmt.Errorf("%s: %w", it.name, err)
		}
		switch {
		case res.Dedup:
			dedup++
			fmt.Fprintf(stdout, "unchanged  %s  %s\n", res.URI, it.name)
		default:
			written++
			pending += res.Pending
			fmt.Fprintf(stdout, "revision %d  %s  %s  (%d chunks, %d embedded, %d pending)\n", res.Revision, res.URI, it.name, res.Chunks, res.Embedded, res.Pending)
		}
	}
	fmt.Fprintf(stdout, "%d written, %d unchanged", written, dedup)
	if pending > 0 {
		fmt.Fprintf(stdout, ", %d chunk vectors pending (run `memo-mcp backfill`)", pending)
	}
	fmt.Fprintln(stdout)
	return nil
}

func runRead(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(stderr)
	history := fs.Bool("history", false, "print the revision or supersession chain instead of the text")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: memo-mcp read <memo://doc/...|memo://chunk/...|memo://source/...|memo://fact/...> [--history]")
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	if *history {
		chain, err := store.History(ctx, positional[0])
		if err != nil {
			return err
		}
		for _, e := range chain {
			state := "history"
			if e.Live {
				state = "LIVE"
			}
			if e.Forgotten != "" {
				state = e.Forgotten
			}
			rev := ""
			if e.Revision > 0 {
				rev = fmt.Sprintf(" r%d", e.Revision)
			}
			if e.Version != "" {
				rev += " @" + e.Version
			}
			fmt.Fprintf(stdout, "%s%s  %-10s %s\n    %s\n", e.At.Format("2006-01-02"), rev, state, e.Summary, e.URI)
		}
		return nil
	}
	if kind, id, perr := kb.ParseURI(positional[0]); perr == nil && kind == "fact" {
		f, err := store.ReadFact(ctx, id)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(f)
	}
	text, prov, err := store.Read(ctx, positional[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "# %s\n", orUntitled(prov.Title))
	fmt.Fprintf(stdout, "namespace: %s  kind: %s  trust: %s  origin: %s  revision: %d\n", prov.Namespace, prov.Kind, prov.Trust, prov.Origin, prov.Revision)
	if prov.SourceURI != "" {
		fmt.Fprintf(stdout, "source: %s", prov.SourceURI)
		if prov.Version != "" {
			fmt.Fprintf(stdout, "  version: %s", prov.Version)
		}
		fmt.Fprintf(stdout, "  fetched: %s\n", prov.FetchedAt.Format("2006-01-02"))
	}
	fmt.Fprintln(stdout, strings.Repeat("-", 40))
	fmt.Fprintln(stdout, strings.TrimRight(text, "\n"))
	return nil
}

func runLs(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ns := fs.String("ns", "", "namespace filter")
	kind := fs.String("kind", "", "kind filter")
	since := fs.String("since", "", "only documents updated on/after this date (YYYY-MM-DD)")
	limit := fs.Int("limit", 50, "maximum rows")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	opts := kb.ListOptions{Namespace: *ns, Kind: *kind, Limit: *limit}
	if *since != "" {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		opts.Since = t
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	entries, err := store.List(ctx, opts)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "(no documents)")
		return nil
	}
	for _, e := range entries {
		v := ""
		if e.Version != "" {
			v = " @" + e.Version
		}
		fmt.Fprintf(stdout, "%s  %-12s %-5s %-7s r%d %3d chunks  %s%s\n", e.UpdatedAt.Format("2006-01-02"), e.Namespace, e.Kind, e.Trust, e.Revision, e.Chunks, orUntitled(e.Title), v)
		fmt.Fprintf(stdout, "            %s\n", e.URI)
	}
	return nil
}

func runVerify(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repair := fs.Bool("repair", false, "queue missing vectors and rebuild broken indexes")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	rep, err := store.Verify(ctx, *repair)
	if err != nil {
		return err
	}
	if rep.Clean() {
		fmt.Fprintln(stdout, "ok: no problems found")
		return nil
	}
	for _, p := range rep.Problems {
		fmt.Fprintln(stdout, "problem:", p)
	}
	for _, r := range rep.Repaired {
		fmt.Fprintln(stdout, "repaired:", r)
	}
	if !*repair {
		fmt.Fprintln(stdout, "run `memo-mcp verify --repair` to fix what can be fixed")
	}
	return nil
}

func runExport(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	md := fs.String("md", "", "directory to write markdown files into")
	ns := fs.String("ns", "", "only this namespace")
	index := fs.Bool("index", false, "print a compact index for AGENTS.md/CLAUDE.md instead of writing files")
	library := fs.String("library", "", "with --index: only this library, as name or name@version")
	maxBytes := fs.Int("max-bytes", 8192, "with --index: size cap")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *md == "" && !*index {
		return errors.New("usage: memo-mcp export --md <dir> [--ns <name>] | export --index [--ns <name>] [--library x@v] [--max-bytes 8192]")
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	if *index {
		opts := kb.IndexOptions{Namespace: *ns, MaxBytes: *maxBytes}
		opts.Library, opts.Version, _ = strings.Cut(*library, "@")
		text, err := store.ExportIndex(ctx, opts)
		if err != nil {
			return err
		}
		_, err = io.WriteString(stdout, text)
		return err
	}
	res, err := store.Export(ctx, *md, kb.ExportOptions{Namespace: *ns})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %d file(s) in %d namespace(s) to %s\n", res.Files, len(res.Namespaces), *md)
	return nil
}

func runBackfill(ctx context.Context, stdout, stderr io.Writer) error {
	embedder, cleanup, err := buildEmbedder(ctx)
	if err != nil {
		return fmt.Errorf("backfill needs the embedding model: %w", err)
	}
	defer cleanup()
	store, closeFn, err := openStore(ctx, stderr, kb.Options{}, embedder)
	if err != nil {
		return err
	}
	defer closeFn()
	n, err := store.Backfill(ctx)
	fmt.Fprintf(stdout, "embedded %d chunk(s)\n", n)
	return err
}

func printStatus(w io.Writer, st *kb.Status) {
	fmt.Fprintf(w, "Knowledge base: %s (%.2f MB, schema v%d)\n", st.Path, st.SizeMB, st.SchemaVersion)
	fmt.Fprintf(w, "Sources: %d   Live documents: %d (%d revisions)   Chunks: %d   Facts: %d\n", st.Sources, st.LiveDocuments, st.Revisions, st.Chunks, st.Facts)
	if st.DefaultModel == "" {
		fmt.Fprintln(w, "Embedding model: none yet (no vectors stored)")
	} else {
		fmt.Fprintf(w, "Embedding model: %s", st.DefaultModel)
		if p := st.PendingEmbeddings[st.DefaultModel]; p > 0 {
			fmt.Fprintf(w, "   pending vectors: %d (run `memo-mcp backfill`)", p)
		}
		fmt.Fprintln(w)
	}
	if st.JobsQueued+st.JobsFailed > 0 {
		fmt.Fprintf(w, "Jobs: %d queued, %d failed\n", st.JobsQueued, st.JobsFailed)
	}
	if !st.LastWrite.IsZero() {
		fmt.Fprintf(w, "Last write: %s\n", st.LastWrite.Format(time.RFC3339))
	}
	if len(st.Namespaces) > 0 {
		fmt.Fprintln(w, "Namespaces:")
		sort.Slice(st.Namespaces, func(i, j int) bool { return st.Namespaces[i].Name < st.Namespaces[j].Name })
		for _, n := range st.Namespaces {
			desc := ""
			if n.Description != "" {
				desc = "  — " + n.Description
			}
			fmt.Fprintf(w, "  %-16s %4d documents %5d chunks%s\n", n.Name, n.Documents, n.Chunks, desc)
		}
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstHeading(md string) string {
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return ""
}

func orUntitled(t string) string {
	if t == "" {
		return "(untitled)"
	}
	return t
}

// parseInterspersed parses flags that may appear before or after positional
// arguments (`memo-mcp ingest file.md --ns grpc`), which the standard flag
// package does not do on its own. It returns the positional arguments.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// fenceCode wraps a source file in a markdown code fence (language from the
// extension) so the chunker treats the whole file as one unsplittable block
// and the exact-identifier index sees it verbatim. Markdown files are left
// as they are.
func fenceCode(name, body string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "md" || strings.HasPrefix(strings.TrimSpace(body), "```") {
		return body
	}
	return "```" + ext + "\n" + strings.TrimRight(body, "\n") + "\n```\n"
}

func runSearch(ctx context.Context, args []string, stdout, stderr io.Writer, explainCmd bool) error {
	name := "search"
	if explainCmd {
		name = "explain"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", retrieve.ModeAuto, "auto|hybrid|keyword|exact|semantic")
	ns := fs.String("ns", "", "namespace filter (comma-separated)")
	library := fs.String("library", "", "library filter")
	version := fs.String("version", "", "version filter")
	kind := fs.String("kind", "", "kind filter (comma-separated)")
	minTrust := fs.String("min-trust", "", "agent|user|curated")
	limit := fs.Int("limit", 10, "maximum results")
	granularity := fs.String("granularity", retrieve.GranularityChunk, "chunk|document")
	format := fs.String("format", "table", "table|json|md")
	explain := fs.Bool("explain", explainCmd, "include why each result ranked and the per-query trace")
	maxTokens := fs.Int("max-tokens", 8000, "response budget in estimated tokens")
	noEmbed := fs.Bool("no-model", false, "do not load the embedding model (keyword-only)")
	profileName := fs.String("profile", os.Getenv("MEMO_PROFILE"), "ranking profile (see `memo-mcp profiles show`)")
	withRerank := fs.Bool("rerank", os.Getenv("MEMO_RERANK") == "1", "attach the cross-encoder reranker (used by profiles with rerank on, e.g. precise)")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 || len(positional) > 2 {
		return fmt.Errorf("usage: memo-mcp %s \"<query>\" [memo://uri] [flags]", name)
	}
	query := positional[0]
	var focus string
	if len(positional) == 2 {
		focus = positional[1]
	}
	cfg, err := ResolveConfig(os.Getenv)
	if err != nil {
		return err
	}
	embedder, cleanup := loadEmbedder(ctx, !*noEmbed, stderr)
	defer cleanup()
	store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, embedder)
	if err != nil {
		return err
	}
	defer closeFn()
	profile, err := retrieve.Lookup(*profileName)
	if err != nil {
		return err
	}
	svc := retrieve.New(store, profile, cfg.LogQueries)
	if *withRerank {
		rr, closeRR, err := loadReranker(ctx, stderr)
		if err != nil {
			return err
		}
		defer closeRR()
		svc.WithReranker(rr)
	}
	rf := retrieve.FormatDetailed
	if *explain || focus != "" {
		rf = retrieve.FormatExplain
	}
	resp, err := svc.Search(ctx, retrieve.Request{Query: query, Mode: *mode, Granularity: *granularity, ResponseFormat: rf, MaxTokens: *maxTokens, Limit: *limit,
		Scope: retrieve.Scope{Namespaces: splitCSV(*ns), Kinds: splitCSV(*kind), Library: *library, Version: *version, MinTrust: *minTrust}})
	if err != nil {
		return err
	}
	if focus != "" {
		for _, r := range resp.Results {
			if r.URI == focus || r.ChunkURI == focus || r.DocumentURI == focus {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(r.Why)
			}
		}
		return fmt.Errorf("%s is not among the %d result(s) for this query", focus, len(resp.Results))
	}
	switch *format {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	case "md":
		fmt.Fprint(stdout, renderMarkdown(resp))
		return nil
	default:
		fmt.Fprint(stdout, renderTable(resp, *explain))
		return nil
	}
}

func renderTable(resp *retrieve.Response, explain bool) string {
	var sb strings.Builder
	if len(resp.Results) == 0 {
		fmt.Fprintf(&sb, "no results: %s\n", resp.Reason)
		if resp.Hint != "" {
			fmt.Fprintf(&sb, "hint: %s\n", resp.Hint)
		}
		if resp.Degraded {
			sb.WriteString("(degraded: no embedding model)\n")
		}
		return sb.String()
	}
	if resp.Degraded {
		sb.WriteString("(degraded: keyword-only, no embedding model)\n")
	}
	for _, r := range resp.Results {
		rel := "  kw  "
		if r.Relevance != nil {
			rel = fmt.Sprintf("%.2f %-8s", *r.Relevance, r.Band)
		}
		title := r.Title
		if r.SectionPath != "" {
			title += " > " + r.SectionPath
		}
		fmt.Fprintf(&sb, "%2d  %s  %-10s %-5s %s\n", r.Rank, rel, r.Provenance.Namespace, r.Provenance.Trust, title)
		fmt.Fprintf(&sb, "    %s", r.URI)
		if r.Provenance.Version != "" {
			fmt.Fprintf(&sb, "  @%s", r.Provenance.Version)
		}
		sb.WriteString("\n")
		fmt.Fprintf(&sb, "    %s\n", oneLine(r.Content, 110))
		if explain && r.Why != nil {
			fmt.Fprintf(&sb, "    why: fused %.5f × recency %.2f = %.5f", r.Why.Fused, r.Why.RecencyFactor, r.Why.Final)
			for _, a := range r.Why.Arms {
				fmt.Fprintf(&sb, " | %s #%d +%.5f", a.Arm, *a.Rank, a.Contribution)
				if len(a.MatchedTerms) > 0 {
					fmt.Fprintf(&sb, " (%s)", strings.Join(a.MatchedTerms, ","))
				}
			}
			sb.WriteString("\n")
		}
	}
	if resp.Trace != nil {
		t := resp.Trace
		fmt.Fprintf(&sb, "trace: %s (%s); scope %s; %d live docs, %d superseded/forgotten excluded; cutoff %s", t.ModeResolved, t.RoutingReason, t.Filtered.ByScope, t.Filtered.LiveDocs, t.Filtered.ByRevocation, t.Cutoff.Kind)
		for arm, ms := range t.LatencyMsPerArm {
			fmt.Fprintf(&sb, "; %s %.0fms", arm, ms)
		}
		if t.Budget.TruncatedCount > 0 {
			fmt.Fprintf(&sb, "; %d more left out by the budget", t.Budget.TruncatedCount)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderMarkdown(resp *retrieve.Response) string {
	var sb strings.Builder
	if len(resp.Results) == 0 {
		fmt.Fprintf(&sb, "_No results: %s._\n", resp.Reason)
		return sb.String()
	}
	sb.WriteString("| # | relevance | where | title | address |\n|---|---|---|---|---|\n")
	for _, r := range resp.Results {
		rel := "keyword"
		if r.Relevance != nil {
			rel = fmt.Sprintf("%.2f %s", *r.Relevance, r.Band)
		}
		fmt.Fprintf(&sb, "| %d | %s | %s/%s | %s | `%s` |\n", r.Rank, rel, r.Provenance.Namespace, r.Provenance.Kind, strings.ReplaceAll(r.Title, "|", "\\|"), r.URI)
	}
	return sb.String()
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runLog(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memo-mcp log tail [--n 20] | show <id> | replay [--n 200] | prune")
	}
	sub, rest := args[0], args[1:]
	store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	switch sub {
	case "tail":
		fs := flag.NewFlagSet("log tail", flag.ContinueOnError)
		fs.SetOutput(stderr)
		n := fs.Int("n", 20, "rows")
		if _, err := parseInterspersed(fs, rest); err != nil {
			return err
		}
		entries, err := store.QueryLogTail(ctx, *n)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			fmt.Fprintln(stdout, "(query log is empty; enable it with MEMO_QUERY_LOG=1)")
			return nil
		}
		for _, e := range entries {
			fmt.Fprintf(stdout, "%d  %s  %-22s %2d results %4dms  %s\n", e.ID, e.At.Format("2006-01-02 15:04:05"), e.Mode, e.NResults, e.LatencyMs, oneLine(loggedQueries(e.Args), 80))
		}
		return nil
	case "show":
		if len(rest) != 1 {
			return errors.New("usage: memo-mcp log show <id>")
		}
		var id int64
		if _, err := fmt.Sscanf(rest[0], "%d", &id); err != nil {
			return fmt.Errorf("bad id %q", rest[0])
		}
		e, err := store.QueryLogEntry(ctx, id)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(e)
	case "replay":
		// Turns logged queries into unlabelled eval candidates: one JSON
		// object per line with the query, scope, and the addresses that
		// came back, ready to be labelled and pasted into a corpus file.
		fs := flag.NewFlagSet("log replay", flag.ContinueOnError)
		fs.SetOutput(stderr)
		n := fs.Int("n", 200, "rows")
		if _, err := parseInterspersed(fs, rest); err != nil {
			return err
		}
		entries, err := store.QueryLogTail(ctx, *n)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		for _, e := range entries {
			var args map[string]any
			_ = json.Unmarshal([]byte(e.Args), &args)
			var top []string
			_ = json.Unmarshal(e.TopURIs, &top)
			cand := map[string]any{"id": fmt.Sprintf("log-%d", e.ID), "query": loggedQueries(e.Args), "scope": args["scope"], "mode": e.Mode, "returned": top, "relevant": []string{}, "irrelevant": []string{}, "category": "unlabelled"}
			if err := enc.Encode(cand); err != nil {
				return err
			}
		}
		return nil
	case "prune":
		n, err := store.QueryLogPrune(ctx, 10000, 30*24*time.Hour)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "pruned %d row(s)\n", n)
		return nil
	default:
		return fmt.Errorf("unknown log subcommand %q", sub)
	}
}

// loggedQueries pulls the query strings out of a logged args_json.
func loggedQueries(args string) string {
	var v struct {
		Queries []string `json:"queries"`
	}
	if err := json.Unmarshal([]byte(args), &v); err != nil || len(v.Queries) == 0 {
		return args
	}
	return strings.Join(v.Queries, " | ")
}

func runModelLs(ctx context.Context, stdout, stderr io.Writer) error {
	installed := map[string]kb.InstalledModel{}
	if store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil); err == nil {
		rows, err := store.InstalledModels(ctx)
		closeFn()
		if err != nil {
			return err
		}
		for _, m := range rows {
			installed[m.ID] = m
		}
	}
	selected, _ := selectedModel()
	fmt.Fprintf(stdout, "%-18s %-5s %-20s %-10s %s\n", "id", "dim", "licence", "vectors", "note")
	for _, m := range embedding.Known {
		marks := ""
		if m.ID == selected.ID {
			marks += "*"
		}
		if im, ok := installed[m.ID]; ok {
			if im.IsDefault {
				marks += " (kb default)"
			}
			fmt.Fprintf(stdout, "%-18s %-5d %-20s %-10d %s%s\n", m.ID, m.Dim, m.Licence, im.Vectors, m.Note, marks)
		} else {
			fmt.Fprintf(stdout, "%-18s %-5d %-20s %-10s %s%s\n", m.ID, m.Dim, m.Licence, "-", m.Note, marks)
		}
	}
	fmt.Fprintln(stdout, "\n* = selected by MEMO_MODEL (or the registry default). `model smoke --all` tests which models load.")
	return nil
}

// runModelSmoke downloads and loads candidate models, embeds three
// sentences and checks sim(a,a') > sim(a,b), and times a ~256-token input.
// It is spike S4 as a command, so the bake-off can repeat it anywhere.
func runModelSmoke(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var ids []string
	all := false
	for _, a := range args {
		if a == "--all" {
			all = true
		} else {
			ids = append(ids, a)
		}
	}
	if all {
		ids = nil
		for _, m := range embedding.Known {
			ids = append(ids, m.ID)
		}
	}
	if len(ids) == 0 {
		return errors.New("usage: memo-mcp model smoke <id>... | --all")
	}
	fmt.Fprintln(stdout, "| model | loads | sane | dim | p50 ms (1 × ~256 tok) | p50 ms (batch 16) | note |")
	fmt.Fprintln(stdout, "|---|---|---|---|---|---|---|")
	for _, id := range ids {
		info, err := embedding.LookupModel(id)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, embedding.Smoke(ctx, info, embedding.DefaultModelDir()))
	}
	return nil
}

func runReindex(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
	fs.SetOutput(stderr)
	model := fs.String("model", "", "model id (default: MEMO_MODEL or minilm)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *model != "" {
		if err := os.Setenv("MEMO_MODEL", *model); err != nil {
			return err
		}
	}
	embedder, cleanup, err := buildEmbedder(ctx)
	if err != nil {
		return fmt.Errorf("reindex needs the embedding model: %w", err)
	}
	defer cleanup()
	store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, embedder)
	if err != nil {
		return err
	}
	defer closeFn()
	last := -1
	n, err := store.Reindex(ctx, func(done, total int) {
		pct := done * 100 / max(total, 1)
		if pct/10 != last/10 {
			fmt.Fprintf(stderr, "reindex %s: %d/%d (%d%%)\n", embedder.Info().ID, done, total, pct)
			last = pct
		}
	})
	fmt.Fprintf(stdout, "embedded %d chunk(s) with %s\n", n, embedder.Info().ID)
	return err
}

func runProfiles(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "show" {
		return errors.New("usage: memo-mcp profiles show [<name>]")
	}
	if len(args) == 2 {
		p, err := retrieve.Lookup(args[1])
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, retrieve.Describe(p))
		return nil
	}
	for _, p := range retrieve.Profiles() {
		fmt.Fprint(stdout, retrieve.Describe(p))
		fmt.Fprintln(stdout)
	}
	return nil
}

// runEval is the lab instrument: it loads the fixture corpora into a fresh
// temporary knowledge base with the chosen embedding model(s), runs the
// labelled queries under the chosen profile(s), and prints quality next to
// cost. It never touches the configured knowledge base.
func runEval(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	models := fs.String("models", "hash", "comma-separated model ids (hash = the deterministic test embedder; others download)")
	profilesFlag := fs.String("profiles", "default", "comma-separated profile names, or 'all'")
	corpus := fs.String("corpus", "all", "notes|kb|all")
	format := fs.String("format", "table", "table|md|json")
	explainFailures := fs.Bool("explain-failures", false, "print why each missed query was missed")
	agentProxy := fs.Bool("agent-proxy", false, "add the two-round agent proxy strategy")
	withRerank := fs.Bool("rerank", false, "attach the cross-encoder reranker so the precise profile can use it")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	var reranker rerank.Reranker
	if *withRerank {
		rr, closeRR, err := loadReranker(ctx, stderr)
		if err != nil {
			return err
		}
		defer closeRR()
		reranker = rr
	}
	profileNames := splitCSV(*profilesFlag)
	if *profilesFlag == "all" {
		profileNames = nil
		for _, p := range retrieve.Profiles() {
			profileNames = append(profileNames, p.Name)
		}
	}
	type run struct {
		model, profile string
		notes, kbRep   *eval.Report
	}
	var runs []run
	for _, modelID := range splitCSV(*models) {
		var emb embedding.Embedder
		cleanup := func() {}
		if modelID == "hash" {
			emb = embedding.NewHashEmbedder(384)
		} else {
			info, err := embedding.LookupModel(modelID)
			if err != nil {
				return err
			}
			e, c, err := embedding.Load(ctx, info, embedding.DefaultModelDir())
			if err != nil {
				return fmt.Errorf("model %s: %w", modelID, err)
			}
			emb, cleanup = e, c
		}
		dir, err := os.MkdirTemp("", "memo-eval-")
		if err != nil {
			cleanup()
			return err
		}
		db, err := kb.Open(ctx, dir, "eval", kb.Options{})
		if err != nil {
			cleanup()
			return err
		}
		store := kb.NewStore(db, emb)
		t0 := time.Now()
		var notesIDs, kbIDs map[string]string
		var loadStats *eval.LoadStats
		if *corpus != "kb" {
			if notesIDs, _, err = eval.Load(ctx, store, eval.ToDocs(eval.Corpus())); err != nil {
				db.Close()
				cleanup()
				return err
			}
		}
		if *corpus != "notes" {
			if kbIDs, loadStats, err = eval.Load(ctx, store, eval.CorpusKB()); err != nil {
				db.Close()
				cleanup()
				return err
			}
		}
		fmt.Fprintf(stderr, "loaded corpora with %s in %.1fs\n", modelID, time.Since(t0).Seconds())
		for _, pname := range profileNames {
			p, err := retrieve.Lookup(pname)
			if err != nil {
				db.Close()
				cleanup()
				return err
			}
			svc := retrieve.New(store, p, false)
			if reranker != nil {
				svc.WithReranker(reranker)
			}
			r := run{model: modelID, profile: pname}
			opts := eval.RunOptions{RealModel: modelID != "hash"}
			if notesIDs != nil {
				if r.notes, err = eval.Run(ctx, svc, notesIDs, eval.Queries(), opts); err != nil {
					db.Close()
					cleanup()
					return err
				}
				r.notes.Strategy, r.notes.Model = pname, modelID
			}
			if kbIDs != nil {
				if r.kbRep, err = eval.Run(ctx, svc, kbIDs, eval.QueriesKB(), opts); err != nil {
					db.Close()
					cleanup()
					return err
				}
				r.kbRep.Strategy, r.kbRep.Model = pname, modelID
				if loadStats != nil {
					r.kbRep.Cost.WritePerDocMs, r.kbRep.Cost.Docs, r.kbRep.Cost.Chunks = loadStats.PerDocMs, loadStats.Docs, loadStats.Chunks
				}
			}
			runs = append(runs, r)
		}
		if *agentProxy && kbIDs != nil {
			r, err := eval.Run(ctx, retrieve.New(store, retrieve.Default, false), kbIDs, eval.QueriesKB(), eval.RunOptions{AgentProxy: true, RealModel: modelID != "hash"})
			if err == nil {
				r.Strategy, r.Model = "agent-proxy", modelID
				runs = append(runs, run{model: modelID, profile: "agent-proxy", kbRep: r})
			}
		}
		db.Close()
		cleanup()
		os.RemoveAll(dir)
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(runs)
	default:
		var notes, kbs []*eval.Report
		for _, r := range runs {
			if r.notes != nil {
				r.notes.Strategy = r.model + "/" + r.profile
				notes = append(notes, r.notes)
			}
			if r.kbRep != nil {
				r.kbRep.Strategy = r.model + "/" + r.profile
				kbs = append(kbs, r.kbRep)
			}
		}
		if len(notes) > 0 {
			fmt.Fprint(stdout, eval.CompareMarkdown("notes corpus (79 docs, 29 queries)", notes))
			fmt.Fprintln(stdout)
		}
		if len(kbs) > 0 {
			fmt.Fprint(stdout, eval.CompareMarkdown("knowledge-base corpus (314 docs, 19 queries)", kbs))
			fmt.Fprintln(stdout)
		}
		if *format == "md" && len(runs) == 1 {
			if runs[0].notes != nil {
				fmt.Fprint(stdout, runs[0].notes.Markdown("notes corpus: per query"))
			}
			if runs[0].kbRep != nil {
				fmt.Fprint(stdout, runs[0].kbRep.Markdown("knowledge-base corpus: per query"))
			}
		}
		if *explainFailures {
			for _, r := range runs {
				for _, rep := range []*eval.Report{r.notes, r.kbRep} {
					if rep == nil {
						continue
					}
					for _, q := range rep.PerQuery {
						if q.Skipped || q.Abstained != nil || q.MRR == 1 {
							continue
						}
						fmt.Fprintf(stdout, "miss  %s/%s  %s (%s): R@1 %.2f MRR %.2f nDCG %.2f; first relevant hit via %s\n", r.model, r.profile, q.QueryID, q.Category, q.RecallAt1, q.MRR, q.NDCG10, strings.Join(q.FirstHitArms, "+"))
					}
				}
			}
		}
		return nil
	}
}

// loadReranker loads the registry's default cross-encoder (MEMO_RERANKER
// names another id).
func loadReranker(ctx context.Context, stderr io.Writer) (rerank.Reranker, func(), error) {
	id := os.Getenv("MEMO_RERANKER")
	if id == "" {
		id = "ms-marco-minilm"
	}
	info, err := rerank.Lookup(id)
	if err != nil {
		return nil, nil, err
	}
	ce, err := rerank.Load(ctx, info, embedding.DefaultModelDir())
	if err != nil {
		return nil, nil, fmt.Errorf("reranker: %w", err)
	}
	fmt.Fprintf(stderr, "reranker %s loaded\n", id)
	return ce, ce.Close, nil
}

// --- facts, forgetting, trust (P4) ---

func runRemember(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("remember", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ns := fs.String("ns", "default", "namespace")
	about := fs.String("about", "", "comma-separated names the fact is about")
	validFrom := fs.String("valid-from", "", "YYYY-MM-DD when it became true")
	validTo := fs.String("valid-to", "", "YYYY-MM-DD when it stopped being true")
	supersedes := fs.String("supersedes", "", "memo://fact/<id> this fact replaces")
	evidence := fs.String("evidence", "", "memo://chunk/<n> that supports it")
	trust := fs.String("trust", kb.TrustUser, "user|curated")
	origin := fs.String("origin", kb.OriginUserSaid, "web|user-said|agent-derived")
	noEmbed := fs.Bool("no-model", false, "do not load the embedding model")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: memo-mcp remember \"<statement>\" [flags]")
	}
	if *trust != kb.TrustUser && *trust != kb.TrustCurated {
		return errors.New("--trust must be user or curated")
	}
	vf, err := parseCLIDate(*validFrom)
	if err != nil {
		return fmt.Errorf("--valid-from: %w", err)
	}
	vt, err := parseCLIDate(*validTo)
	if err != nil {
		return fmt.Errorf("--valid-to: %w", err)
	}
	embedder, cleanup := loadEmbedder(ctx, !*noEmbed, stderr)
	defer cleanup()
	store, closeFn, err := openStore(ctx, stderr, kb.Options{}, embedder)
	if err != nil {
		return err
	}
	defer closeFn()
	f, err := store.Remember(ctx, kb.RememberInput{Namespace: *ns, Statement: positional[0], About: splitCSV(*about), ValidFrom: vf, ValidTo: vt, Supersedes: *supersedes, EvidenceURI: *evidence, Origin: *origin, Trust: *trust, Actor: "cli", Channel: kb.ChannelCLI})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "recorded %s (trust %s)", f.URI, f.Trust)
	if *supersedes != "" {
		fmt.Fprintf(stdout, "; %s is now history", *supersedes)
	}
	fmt.Fprintln(stdout)
	return nil
}

func runForget(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("forget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	reason := fs.String("reason", "", "why (required; shown to anyone who reads the address later)")
	redact := fs.Bool("redact", false, "also erase the stored text")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) != 1 || *reason == "" {
		return errors.New("usage: memo-mcp forget <memo://doc/...|memo://fact/...> --reason \"<why>\" [--redact]")
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	if err := store.Forget(ctx, kb.ForgetInput{URI: positional[0], Reason: *reason, Redact: *redact, Actor: "cli", Channel: kb.ChannelCLI}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "forgot %s: %s\n", positional[0], *reason)
	return nil
}

func runFacts(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "ls" {
		return errors.New("usage: memo-mcp facts ls [--ns <name>] [--as-of YYYY-MM-DD] [--history] [--json]")
	}
	fs := flag.NewFlagSet("facts ls", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ns := fs.String("ns", "", "namespace")
	asOf := fs.String("as-of", "", "what was believed on this date")
	history := fs.Bool("history", false, "include replaced facts")
	asJSON := fs.Bool("json", false, "print JSON")
	if _, err := parseInterspersed(fs, args[1:]); err != nil {
		return err
	}
	t, err := parseCLIDate(*asOf)
	if err != nil {
		return fmt.Errorf("--as-of: %w", err)
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	facts, err := store.ListFacts(ctx, kb.FactFilter{Namespace: *ns, AsOf: t, IncludeHistory: *history})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(facts)
	}
	if len(facts) == 0 {
		fmt.Fprintln(stdout, "(no facts)")
		return nil
	}
	for _, f := range facts {
		state := "live"
		if f.InvalidatedAt != nil {
			state = "replaced " + f.InvalidatedAt.Format("2006-01-02")
		}
		window := ""
		if f.ValidFrom != nil || f.ValidTo != nil {
			window = " valid " + dateOr(f.ValidFrom, "…") + "→" + dateOr(f.ValidTo, "…")
		}
		fmt.Fprintf(stdout, "%s  %-10s %-7s %-22s %s%s\n", f.RecordedAt.Format("2006-01-02"), f.Namespace, f.Trust, state, f.Statement, window)
		fmt.Fprintf(stdout, "            %s", f.URI)
		if f.EvidenceURI != "" {
			fmt.Fprintf(stdout, "  evidence %s", f.EvidenceURI)
		}
		fmt.Fprintln(stdout)
	}
	return nil
}

func runTrust(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: memo-mcp trust ls | promote <uri> --to user|curated | demote <uri> --to agent|user")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ls":
		store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
		if err != nil {
			return err
		}
		defer closeFn()
		rows, err := store.TrustSummary(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%-10s %-10s %s\n", "trust", "documents", "facts")
		for _, r := range rows {
			fmt.Fprintf(stdout, "%-10s %-10d %d\n", r.Trust, r.Documents, r.Facts)
		}
		return nil
	case "promote", "demote":
		fs := flag.NewFlagSet("trust "+sub, flag.ContinueOnError)
		fs.SetOutput(stderr)
		to := fs.String("to", "", "target trust")
		positional, err := parseInterspersed(fs, rest)
		if err != nil {
			return err
		}
		if len(positional) != 1 || *to == "" {
			return fmt.Errorf("usage: memo-mcp trust %s <uri> --to <trust>", sub)
		}
		store, closeFn, err := openStore(ctx, stderr, kb.Options{NoCreate: true}, nil)
		if err != nil {
			return err
		}
		defer closeFn()
		if err := store.SetTrust(ctx, positional[0], *to, "cli", kb.ChannelCLI); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s: trust is now %s (audited, channel cli)\n", positional[0], *to)
		return nil
	default:
		return fmt.Errorf("unknown trust subcommand %q", sub)
	}
}

func parseCLIDate(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%q is not YYYY-MM-DD or RFC3339", s)
}

func dateOr(t *time.Time, def string) string {
	if t == nil {
		return def
	}
	return t.Format("2006-01-02")
}
