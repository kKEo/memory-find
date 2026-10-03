// Package graph recognises the things a passage talks about (entities),
// links them to the passages that mention them, and walks those links as one
// more search arm. It is an index over the text, not a replacement for it
// (roadmap P7; research D-E, D-F, D-G, D-H).
package graph

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Mention is one entity name found in a passage, with a weight that says how
// strongly the passage is about it (heading > code span > prose).
type Mention struct {
	Name   string
	Type   string // identifier | name | heading
	Weight float64
}

var (
	// backticked spans: `Client.Connect`, `net/http`
	codeSpanRe = regexp.MustCompile("`([^`\n]{2,80})`")
	// dotted, camel or snake identifiers: quorum.RotateKeys3, useCallback, ERR_CONN_RESET, QRM-1234
	identRe = regexp.MustCompile(`\b(?:[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)+|[a-z]+[A-Z][A-Za-z0-9]*|[A-Z][A-Z0-9]+(?:[_-][A-Z0-9]+)+|[A-Za-z]+(?:/[A-Za-z0-9_.-]+)+)\b`)
	// capitalised spans of one to four words, not at a sentence start
	capSpanRe = regexp.MustCompile(`(?:[A-Z][a-z0-9]+)(?:[ -][A-Z][a-z0-9]+){0,3}`)
	headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
)

// stop lists common words that start sentences or appear capitalised without
// naming anything.
var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`The A An This That These Those It Its In On At For To From With Without By Of And Or But If Then When Where While Which Who What Why How Here There Not No Yes All Any Some Each Every Both Either Neither Other Another Such Only Also Just Even Still Already Always Never Often Since Until Before After Because Although Though Unless Whether So As Than Too Very Much Many More Most Less Least Few First Second Third Last Next New Old Chapter Section Appendix Note Notes Example Examples Errors Error Usage Overview Introduction Summary General Quick Standup Today Ask Use See Run Call Set Add Remove Check Try Make Keep Pass Return Start Stop Open Close Read Write Put Get Let Do Does Did Is Are Was Were Has Have Had Can Could Should Would Will May Might Must Monday Tuesday Wednesday Thursday Friday Saturday Sunday January February March April May June July August September October November December`) {
		stop[w] = true
	}
}

// Extract runs rung 1 of the extraction ladder (heuristics only) over one
// passage. title is the document title (counted as a heading mention).
func Extract(title, text string) []Mention {
	seen := map[string]*Mention{}
	add := func(name, typ string, w float64) {
		name = Normalize(name)
		if name == "" || len(name) < 2 || stop[name] || isNumber(name) {
			return
		}
		if m := seen[strings.ToLower(name)]; m != nil {
			if w > m.Weight {
				m.Weight = w
				m.Type = typ
			} else {
				m.Weight += 0.1
			}
			return
		}
		seen[strings.ToLower(name)] = &Mention{Name: name, Type: typ, Weight: w}
	}
	if title != "" {
		add(title, "heading", 1.0)
		for _, id := range identRe.FindAllString(title, -1) {
			add(id, "identifier", 1.0)
		}
	}
	for _, h := range headingRe.FindAllStringSubmatch(text, -1) {
		add(h[1], "heading", 0.9)
	}
	body := headingRe.ReplaceAllString(text, "")
	for _, c := range codeSpanRe.FindAllStringSubmatch(body, -1) {
		span := c[1]
		if id := identRe.FindString(span); id != "" && !strings.ContainsAny(span, " (") {
			add(id, "identifier", 0.8)
		} else if id != "" {
			add(id, "identifier", 0.7)
		} else if len(strings.Fields(span)) <= 3 {
			add(span, "identifier", 0.6)
		}
	}
	plain := codeSpanRe.ReplaceAllString(body, " ")
	for _, id := range identRe.FindAllString(plain, -1) {
		if strings.Count(id, ".") > 0 && isNumber(strings.ReplaceAll(id, ".", "")) {
			continue // version numbers like 1.7
		}
		add(id, "identifier", 0.6)
	}
	// Capitalised spans, skipping the first word of a sentence unless it is
	// part of a longer span (sentence starts are usually ordinary words).
	for _, sentence := range splitSentences(plain) {
		for _, m := range capSpanRe.FindAllStringIndex(sentence, -1) {
			words := strings.Fields(sentence[m[0]:m[1]])
			// Leading ordinary words ("The Ledger Store") are not part of
			// the name; a lone capitalised word at a sentence start is
			// usually just the start of a sentence.
			for len(words) > 0 && stop[words[0]] {
				words = words[1:]
			}
			if len(words) == 0 || (m[0] == 0 && len(words) == 1) {
				continue
			}
			span := strings.Join(words, " ")
			if len(span) < 3 || identRe.MatchString(span) {
				continue
			}
			add(span, "name", 0.4)
		}
	}
	out := make([]Mention, 0, len(seen))
	for _, m := range seen {
		if m.Weight > 1 {
			m.Weight = 1
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Normalize trims punctuation and whitespace around a name, keeping its case
// (identifiers are case-sensitive; matching lowercases separately).
func Normalize(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	name = strings.TrimFunc(name, func(r rune) bool { return unicode.IsPunct(r) && r != '_' || unicode.IsSpace(r) })
	return name
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func splitSentences(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == '.' || r == '!' || r == '?' || r == '\n' || r == ';' || r == ':' {
			if seg := strings.TrimSpace(s[start:i]); seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	if seg := strings.TrimSpace(s[start:]); seg != "" {
		out = append(out, seg)
	}
	return out
}
