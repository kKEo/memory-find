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
	"github.com/kKEo/memory-find/internal/kb"
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
	if len(args) != 1 {
		return errors.New("usage: memo-mcp read <memo://doc/...|memo://chunk/...|memo://source/...>")
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
	text, prov, err := store.Read(ctx, args[0])
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
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *md == "" {
		return errors.New("usage: memo-mcp export --md <dir> [--ns <name>]")
	}
	store, closeFn, err := openStore(ctx, stderr, kb.Options{ReadOnly: true}, nil)
	if err != nil {
		return err
	}
	defer closeFn()
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
