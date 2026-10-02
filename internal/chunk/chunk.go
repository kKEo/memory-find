// Package chunk splits a markdown document into passages ("chunks") small
// enough to match precisely and large enough to carry meaning. It is
// deterministic and needs no model: token counts are estimated.
//
// Strategy (docs/schema.md §5.4, roadmap OD-3): headings → paragraphs →
// sentences → hard rune split. Blocks are packed greedily into chunks of
// about Target estimated tokens, never beyond Max; a fenced code block is
// never split; consecutive chunks of the same section overlap by roughly
// Overlap tokens (the tail sentences of the previous chunk).
package chunk

import (
	"math"
	"strings"
	"unicode"
)

// Chunk is one passage of a document.
type Chunk struct {
	Ord         int    // position within the document, from 0
	SectionPath string // heading trail, e.g. "Interceptors > Ordering"
	Text        string // the passage itself (no context header)
	EstTokens   int    // estimated token count of Text
	Lang        string // code fence language, if the chunk is a code block
}

// Options tune the splitter. Zero values take the defaults.
type Options struct {
	Target  int // preferred size in estimated tokens (default 200)
	Max     int // hard cap (default 400); clamp to the embedding model's limit
	Overlap int // approximate tokens repeated from the previous chunk (default 40)
}

func (o Options) withDefaults() Options {
	if o.Target <= 0 {
		o.Target = 200
	}
	if o.Max <= 0 {
		o.Max = 400
	}
	if o.Max < o.Target {
		o.Max = o.Target
	}
	if o.Overlap < 0 {
		o.Overlap = 0
	} else if o.Overlap == 0 {
		o.Overlap = 40
	}
	return o
}

// EstimateTokens approximates how many word-pieces a model will see,
// without loading a tokenizer: ASCII words cost ~1.4 (sub-word splits),
// non-ASCII runes ~1.2 each (CJK and symbols are often one piece per
// character), and runs of punctuation ~0.5.
func EstimateTokens(s string) int {
	var est float64
	var nonASCII, punctRuns, wordLen int
	inPunct := false
	endWord := func() {
		if wordLen > 0 {
			// A short word is ~1.4 pieces; long identifiers and
			// space-less runs split into roughly one piece per 5 chars.
			est += math.Max(1.4, float64(wordLen)/5)
			wordLen = 0
		}
	}
	for _, r := range s {
		switch {
		case r > unicode.MaxASCII:
			endWord()
			nonASCII++
			inPunct = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			wordLen++
			inPunct = false
		case unicode.IsSpace(r):
			endWord()
			inPunct = false
		default: // punctuation / symbols
			endWord()
			if !inPunct {
				punctRuns++
			}
			inPunct = true
		}
	}
	endWord()
	est += float64(nonASCII)*1.2 + float64(punctRuns)*0.5
	return int(est + 0.5)
}

// ContextHeader is the line prepended to a chunk's text when it is embedded
// and indexed, so the vector and the keyword index carry where the passage
// sits: "title > section > subsection", optionally followed by a
// client-written document context.
func ContextHeader(title, sectionPath, docContext string) string {
	parts := make([]string, 0, 2)
	if t := strings.TrimSpace(title); t != "" {
		parts = append(parts, t)
	}
	if sectionPath != "" {
		parts = append(parts, sectionPath)
	}
	h := strings.Join(parts, " > ")
	if c := strings.TrimSpace(docContext); c != "" {
		if h != "" {
			h += " — "
		}
		h += c
	}
	return h
}

// block is a structural unit of the document.
type block struct {
	section string
	text    string
	code    bool
	lang    string
	tokens  int
}

// Split cuts markdown into chunks.
func Split(markdown string, opts Options) []Chunk {
	opts = opts.withDefaults()
	blocks := parseBlocks(markdown)
	var out []Chunk
	add := func(section, text, lang string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		out = append(out, Chunk{Ord: len(out), SectionPath: section, Text: text, EstTokens: EstimateTokens(text), Lang: lang})
	}

	var cur strings.Builder
	curTokens := 0
	curSection := ""
	flush := func() {
		if cur.Len() > 0 {
			add(curSection, cur.String(), "")
		}
		cur.Reset()
		curTokens = 0
	}

	for _, b := range blocks {
		if b.code {
			// A code fence is a chunk of its own and is never split.
			flush()
			add(b.section, b.text, b.lang)
			continue
		}
		if b.section != curSection {
			flush()
			curSection = b.section
		}
		for _, piece := range splitOversized(b.text, opts.Max) {
			pt := EstimateTokens(piece)
			if curTokens > 0 && curTokens+pt > opts.Target {
				prev := cur.String()
				flush()
				tail := overlapTail(prev, opts.Overlap)
				if tt := EstimateTokens(tail); tail != "" && tt+pt <= opts.Max {
					cur.WriteString(tail)
					cur.WriteString("\n\n")
					curTokens = tt
				}
			}
			if cur.Len() > 0 {
				cur.WriteString("\n\n")
			}
			cur.WriteString(piece)
			curTokens += pt
		}
	}
	flush()
	return out
}

// parseBlocks turns markdown into heading-tracked paragraphs and code fences.
func parseBlocks(md string) []block {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var blocks []block
	trail := []string{} // heading text per level, index = level-1
	section := func() string {
		parts := make([]string, 0, len(trail))
		for _, t := range trail {
			if t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, " > ")
	}
	var para []string
	flushPara := func() {
		if len(para) > 0 {
			text := strings.TrimSpace(strings.Join(para, "\n"))
			if text != "" {
				blocks = append(blocks, block{section: section(), text: text, tokens: EstimateTokens(text)})
			}
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flushPara()
			fence := trimmed[:3]
			lang := strings.TrimSpace(strings.TrimPrefix(trimmed, fence))
			var code []string
			code = append(code, line)
			for i++; i < len(lines); i++ {
				code = append(code, lines[i])
				if strings.HasPrefix(strings.TrimSpace(lines[i]), fence) {
					break
				}
			}
			text := strings.Join(code, "\n")
			blocks = append(blocks, block{section: section(), text: text, code: true, lang: lang, tokens: EstimateTokens(text)})
			continue
		}
		if level, title, ok := heading(trimmed); ok {
			flushPara()
			if level > len(trail) {
				for len(trail) < level {
					trail = append(trail, "")
				}
			} else {
				trail = trail[:level]
			}
			trail[level-1] = title
			continue
		}
		if trimmed == "" {
			flushPara()
			continue
		}
		para = append(para, line)
	}
	flushPara()
	return blocks
}

func heading(line string) (level int, title string, ok bool) {
	if !strings.HasPrefix(line, "#") {
		return 0, "", false
	}
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, "", false
	}
	title = strings.TrimSpace(strings.TrimRight(line[level:], "#"))
	return level, title, title != ""
}

// splitOversized returns text as is when it fits under max tokens, else
// splits it into sentences packed under max, hard-splitting any single
// sentence that is still too long.
func splitOversized(text string, max int) []string {
	if EstimateTokens(text) <= max {
		return []string{text}
	}
	var out []string
	var cur strings.Builder
	curTokens := 0
	for _, s := range sentences(text) {
		st := EstimateTokens(s)
		if st > max {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
				curTokens = 0
			}
			out = append(out, hardSplit(s, max)...)
			continue
		}
		if curTokens > 0 && curTokens+st > max {
			out = append(out, cur.String())
			cur.Reset()
			curTokens = 0
		}
		if cur.Len() > 0 {
			cur.WriteByte(' ')
		}
		cur.WriteString(s)
		curTokens += st
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// sentences splits on sentence-ending punctuation followed by whitespace.
func sentences(text string) []string {
	var out []string
	start := 0
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if (runes[i] == '.' || runes[i] == '!' || runes[i] == '?') && (i+1 == len(runes) || unicode.IsSpace(runes[i+1])) {
			s := strings.TrimSpace(string(runes[start : i+1]))
			if s != "" {
				out = append(out, s)
			}
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(string(runes[start:])); rest != "" {
		out = append(out, rest)
	}
	return out
}

// hardSplit cuts a single over-long sentence on word boundaries (or runes
// when there are no spaces) into pieces under max estimated tokens.
func hardSplit(s string, max int) []string {
	var out []string
	words := strings.Fields(s)
	if len(words) <= 1 {
		runes := []rune(s)
		step := max * 2 // ~2 runes per token is a safe lower bound for dense text
		if step < 1 {
			step = 1
		}
		for i := 0; i < len(runes); i += step {
			end := i + step
			if end > len(runes) {
				end = len(runes)
			}
			out = append(out, string(runes[i:end]))
		}
		return out
	}
	var cur []string
	curTokens := 0
	for _, w := range words {
		wt := EstimateTokens(w)
		if curTokens > 0 && curTokens+wt > max {
			out = append(out, strings.Join(cur, " "))
			cur, curTokens = nil, 0
		}
		cur = append(cur, w)
		curTokens += wt
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, " "))
	}
	return out
}

// overlapTail returns the trailing sentences of text worth about n tokens.
func overlapTail(text string, n int) string {
	if n <= 0 {
		return ""
	}
	ss := sentences(text)
	var tail []string
	tokens := 0
	for i := len(ss) - 1; i >= 0 && tokens < n; i-- {
		tail = append([]string{ss[i]}, tail...)
		tokens += EstimateTokens(ss[i])
	}
	if len(tail) == len(ss) { // the whole previous chunk would be repeated: skip overlap
		return ""
	}
	return strings.Join(tail, " ")
}
