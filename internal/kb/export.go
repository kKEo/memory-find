package kb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ExportOptions filters Export.
type ExportOptions struct {
	Namespace string // empty = all
}

// ExportResult says what was written.
type ExportResult struct {
	Files      int
	Namespaces []string
}

// Export writes every live document as a markdown file with YAML front
// matter carrying its provenance, laid out as <dir>/<namespace>/<kind>/
// <slug>-<shortid>.md, plus an _index.md per namespace. The files open in
// any editor or Obsidian; importing the folder back yields zero new
// revisions because the front matter names each document.
func (s *Store) Export(ctx context.Context, dir string, opts ExportOptions) (*ExportResult, error) {
	entries, err := s.List(ctx, ListOptions{Namespace: opts.Namespace, Limit: 1 << 30})
	if err != nil {
		return nil, err
	}
	res := &ExportResult{}
	byNS := map[string][]ListEntry{}
	for _, e := range entries {
		byNS[e.Namespace] = append(byNS[e.Namespace], e)
	}
	for ns, list := range byNS {
		res.Namespaces = append(res.Namespaces, ns)
		var index strings.Builder
		fmt.Fprintf(&index, "# %s\n\nExported from memo-mcp on %s. One line per document; open the file for the text and its provenance.\n\n", ns, s.now().UTC().Format("2006-01-02"))
		sort.Slice(list, func(i, j int) bool { return list[i].Title < list[j].Title })
		for _, e := range list {
			d, err := s.ReadDocument(ctx, e.DocumentID)
			if err != nil {
				return nil, err
			}
			rel := filepath.Join(ns, d.Prov.Kind, slug(d.Prov.Title)+"-"+shortID(d.ID)+".md")
			full := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(full, []byte(renderMarkdown(d)), 0o644); err != nil {
				return nil, err
			}
			fmt.Fprintf(&index, "- [%s](%s) — %s, %s%s\n", orTitle(d.Prov.Title), filepath.ToSlash(filepath.Join(d.Prov.Kind, filepath.Base(rel))), d.Prov.Kind, d.Prov.Trust, versionSuffix(d.Prov.Version))
			res.Files++
		}
		if err := os.WriteFile(filepath.Join(dir, ns, "_index.md"), []byte(index.String()), 0o644); err != nil {
			return nil, err
		}
	}
	sort.Strings(res.Namespaces)
	return res, nil
}

func versionSuffix(v string) string {
	if v == "" {
		return ""
	}
	return ", " + v
}

func orTitle(t string) string {
	if t == "" {
		return "(untitled)"
	}
	return t
}

// renderMarkdown is the exported file: front matter, then the content.
func renderMarkdown(d *Document) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "memo_uri: %s\n", d.URI)
	if d.Prov.SourceURI != "" {
		fmt.Fprintf(&sb, "source_uri: %s\n", d.Prov.SourceURI)
	}
	fmt.Fprintf(&sb, "title: %s\n", yamlStr(d.Prov.Title))
	fmt.Fprintf(&sb, "kind: %s\n", d.Prov.Kind)
	fmt.Fprintf(&sb, "namespace: %s\n", d.Prov.Namespace)
	if d.Prov.Library != "" {
		fmt.Fprintf(&sb, "library: %s\n", d.Prov.Library)
	}
	if d.Prov.Version != "" {
		fmt.Fprintf(&sb, "version: %s\n", d.Prov.Version)
	}
	fmt.Fprintf(&sb, "revision: %d\n", d.Prov.Revision)
	fmt.Fprintf(&sb, "content_hash: %s\n", d.Prov.Hash)
	fmt.Fprintf(&sb, "fetched_at: %s\n", d.Prov.FetchedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&sb, "trust: %s\n", d.Prov.Trust)
	fmt.Fprintf(&sb, "origin: %s\n", d.Prov.Origin)
	if d.Context != "" {
		fmt.Fprintf(&sb, "context: %s\n", yamlStr(d.Context))
	}
	sb.WriteString("---\n\n")
	sb.WriteString(d.Content)
	return sb.String()
}

// FrontMatter is what ParseExported recovers from an exported file.
type FrontMatter struct {
	MemoURI   string
	SourceURI string
	Title     string
	Kind      string
	Namespace string
	Library   string
	Version   string
	Trust     string
	Origin    string
	Context   string
}

// ParseExported splits an exported file into front matter and body. Files
// without front matter return an empty FrontMatter and the whole text.
func ParseExported(text string) (FrontMatter, string) {
	var fm FrontMatter
	if !strings.HasPrefix(text, "---\n") {
		return fm, text
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return fm, text
	}
	head, body := text[4:4+end], text[4+end+5:]
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = unquote(strings.TrimSpace(v))
		switch strings.TrimSpace(k) {
		case "memo_uri":
			fm.MemoURI = v
		case "source_uri":
			fm.SourceURI = v
		case "title":
			fm.Title = v
		case "kind":
			fm.Kind = v
		case "namespace":
			fm.Namespace = v
		case "library":
			fm.Library = v
		case "version":
			fm.Version = v
		case "trust":
			fm.Trust = v
		case "origin":
			fm.Origin = v
		case "context":
			fm.Context = v
		}
	}
	return fm, strings.TrimPrefix(body, "\n")
}

func yamlStr(s string) string {
	if s == "" || strings.ContainsAny(s, ":#\"'\n") || strings.TrimSpace(s) != s {
		return fmt.Sprintf("%q", s)
	}
	return s
}

func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		var out string
		if _, err := fmt.Sscanf(v, "%q", &out); err == nil {
			return out
		}
	}
	return v
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(title string) string {
	s := nonSlug.ReplaceAllString(strings.ToLower(title), "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "untitled"
	}
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}
