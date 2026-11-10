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
	// A fence's info string ("```tsx") is not page text.
	fenceLine = regexp.MustCompile("(?m)^[ \t]*(?:`{3,}|~{3,}).*$")
	brTag     = regexp.MustCompile(`(?i)<br\s*/?>`) // line breaks kept inside table cells
)

func words(s string) map[string]int {
	s = imageAlt.ReplaceAllString(s, " ")
	s = fenceLine.ReplaceAllString(s, " ")
	s = brTag.ReplaceAllString(s, " ")
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
	miss, dup := map[string]int{}, map[string]int{}
	for w, n := range src {
		c.SourceWords += n
		if o := out[w]; o < n {
			miss[w] = n - o
		}
	}
	for w, n := range out {
		c.OutputWords += n
		if s := src[w]; n > s {
			dup[w] = n - s
		}
	}
	// Markdown can join or split words at a markup edge without losing text:
	// "by<a>XTech</a>" is one word on the page but "by [XTech]" in markdown,
	// and two link buttons "Get Started" "Preview" become "[…Started][Preview]".
	settleJoins(miss, dup)
	settleJoins(dup, miss)
	type diff struct {
		w string
		n int
	}
	var missL, dupL []diff
	for w, n := range miss {
		c.Missing += n
		missL = append(missL, diff{w, n})
	}
	for w, n := range dup {
		c.Duplicated += n
		dupL = append(dupL, diff{w, n})
	}
	sort.Slice(missL, func(i, j int) bool {
		return missL[i].n > missL[j].n || (missL[i].n == missL[j].n && missL[i].w < missL[j].w)
	})
	sort.Slice(dupL, func(i, j int) bool { return dupL[i].n > dupL[j].n || (dupL[i].n == dupL[j].n && dupL[i].w < dupL[j].w) })
	for i := 0; i < len(missL) && i < 8; i++ {
		c.Examples = append(c.Examples, fmt.Sprintf("missing %q ×%d", missL[i].w, missL[i].n))
	}
	for i := 0; i < len(dupL) && i < 8; i++ {
		c.Examples = append(c.Examples, fmt.Sprintf("extra %q ×%d", dupL[i].w, dupL[i].n))
	}
	c.OK = c.Missing == 0 && c.Duplicated == 0
	return c
}

// settleJoins cancels a word in one side that is exactly two words of the
// other side glued together ("byxtech" against "by" + "xtech").
func settleJoins(joined, parts map[string]int) {
	for w, n := range joined {
		for i := 1; i < len(w) && n > 0; i++ {
			a, b := w[:i], w[i:]
			k := min(n, parts[a], parts[b])
			if a == b {
				k = min(n, parts[a]/2)
			}
			if k == 0 {
				continue
			}
			n -= k
			parts[a] -= k
			parts[b] -= k
			for _, x := range []string{a, b} {
				if parts[x] == 0 {
					delete(parts, x)
				}
			}
		}
		if n == 0 {
			delete(joined, w)
		} else {
			joined[w] = n
		}
	}
}
