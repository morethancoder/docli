package plan

import (
	"fmt"
	"html"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/morethancoder/docli/internal/outline"
)

// fillIndexes writes every INDEX.md body: one line per child with kind, token
// count and a one-line summary, so an agent can choose what to open from the
// index alone.
func (b *builder) fillIndexes() {
	children := map[string][]*File{}
	for _, f := range b.files {
		if f.Parent != "" {
			children[f.Parent] = append(children[f.Parent], f)
		}
	}
	// Deepest first so a folder's totals are known before its parent's.
	idx := []*File{}
	for _, f := range b.files {
		if f.Index {
			idx = append(idx, f)
		}
	}
	sort.SliceStable(idx, func(i, j int) bool {
		return strings.Count(idx[i].Path, "/") > strings.Count(idx[j].Path, "/")
	})
	for _, ix := range idx {
		dir := path.Dir(ix.Path)
		var lines []string
		total, items := 0, 0
		var overview *File
		for _, c := range children[ix.Path] {
			if c.Kind == "overview" {
				overview = c
				break
			}
		}
		for _, c := range children[ix.Path] {
			rel := relPath(dir, c.Path)
			if c.Index {
				lines = append(lines, fmt.Sprintf("- [%s/](%s) folder · %d items · %s — %s",
					c.Title, rel, c.Items, tok(c.Total), c.Summary))
				total += c.Total
				items++
				continue
			}
			c.Summary = summarize(c.Body)
			name := c.Title
			if c.Kind == "overview" {
				name = "_overview"
			}
			lines = append(lines, fmt.Sprintf("- [%s](%s) %s · %s — %s", name, rel, c.Kind, tok(c.Tokens), c.Summary))
			total += c.Tokens
			items++
		}
		ix.Total, ix.Items = total, items
		if overview != nil {
			ix.Summary = overview.Summary
		} else {
			ix.Summary = fmt.Sprintf("%d entries", items)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "> %d items · %s total · source: %s\n", items, tok(total), ix.Source)
		if ix.Parent != "" {
			fmt.Fprintf(&sb, "> up: [INDEX.md](%s)\n", relPath(dir, ix.Parent))
		}
		sb.WriteString("\n")
		sb.WriteString(strings.Join(lines, "\n"))
		ix.Body = sb.String()
		ix.Hash = hash(ix.Body)
		ix.Tokens = outline.Tokens(ix.Body) + outline.Tokens(ix.Title) + 2
	}
}

func tok(n int) string {
	switch {
	case n >= 10000:
		return fmt.Sprintf("%dk tok", (n+500)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk tok", float64(n)/1000)
	default:
		return fmt.Sprintf("%d tok", n)
	}
}

var (
	mdLinkText  = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdMarks     = regexp.MustCompile("[*_`>#]+")
	sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)
)

// summarize returns the first sentence of the first prose line, at most about
// 110 characters.
func summarize(body string) string {
	for _, line := range strings.Split(body, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "|") || strings.HasPrefix(l, "```") || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "<") {
			continue
		}
		l = strings.TrimLeft(l, "-*+0123456789. ")
		l = mdLinkText.ReplaceAllString(l, "$1")
		l = strings.ReplaceAll(l, `\`, "")
		l = mdMarks.ReplaceAllString(l, "")
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			continue
		}
		if loc := sentenceEnd.FindStringIndex(l); loc != nil && loc[0] < 140 {
			l = l[:loc[0]+1]
		}
		if r := []rune(l); len(r) > 110 {
			cut := string(r[:110])
			if i := strings.LastIndex(cut, " "); i > 60 {
				cut = cut[:i]
			}
			l = cut + "…"
		}
		return l
	}
	return ""
}

// Coverage compares the words of the source with the words that landed in
// files. Every word must appear exactly as often as in the source.
type Coverage struct {
	SourceWords int      `json:"source_words"`
	OutputWords int      `json:"output_words"`
	Missing     int      `json:"missing"`
	Duplicated  int      `json:"duplicated"`
	Examples    []string `json:"examples,omitempty"`
	OK          bool     `json:"ok"`
}

var (
	linkTarget = regexp.MustCompile(`\]\([^)]*\)`)
	imageAlt   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`) // alt text is not page text
	refDef     = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s*\S+.*$`)
	olMarker   = regexp.MustCompile(`(?m)^\s*\d+[.)]\s`)
)

func words(s string) map[string]int {
	s = imageAlt.ReplaceAllString(s, " ")
	s = linkTarget.ReplaceAllString(s, "]")
	s = refDef.ReplaceAllString(s, "")
	s = olMarker.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.NewReplacer("`", "", "*", "", "~", "").Replace(s)
	m := map[string]int{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		m[w]++
	}
	return m
}

func checkCoverage(source string, covered []string) Coverage {
	src := words(source)
	out := words(strings.Join(covered, "\n"))
	c := Coverage{}
	type diff struct {
		w string
		n int
	}
	var miss, dup []diff
	for w, n := range src {
		c.SourceWords += n
		if o := out[w]; o < n {
			c.Missing += n - o
			miss = append(miss, diff{w, n - o})
		}
	}
	for w, n := range out {
		c.OutputWords += n
		if s := src[w]; n > s {
			c.Duplicated += n - s
			dup = append(dup, diff{w, n - s})
		}
	}
	sort.Slice(miss, func(i, j int) bool { return miss[i].n > miss[j].n || (miss[i].n == miss[j].n && miss[i].w < miss[j].w) })
	sort.Slice(dup, func(i, j int) bool { return dup[i].n > dup[j].n || (dup[i].n == dup[j].n && dup[i].w < dup[j].w) })
	for i := 0; i < len(miss) && i < 8; i++ {
		c.Examples = append(c.Examples, fmt.Sprintf("missing %q ×%d", miss[i].w, miss[i].n))
	}
	for i := 0; i < len(dup) && i < 8; i++ {
		c.Examples = append(c.Examples, fmt.Sprintf("extra %q ×%d", dup[i].w, dup[i].n))
	}
	c.OK = c.Missing == 0 && c.Duplicated == 0
	return c
}
