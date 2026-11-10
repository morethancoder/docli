package parse

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/fetch"
	"github.com/morethancoder/docli/internal/outline"
)

// Markdown parses markdown and llms.txt files. goldmark finds the real
// headings (so a "# comment" inside a code block is not a heading), and each
// section's body is the original markdown between two headings, unchanged.
type Markdown struct{}

func (Markdown) Name() string { return "markdown" }

func (Markdown) Detect(raw *fetch.Raw) bool {
	ct := strings.ToLower(raw.ContentType)
	if strings.Contains(ct, "markdown") {
		return true
	}
	p := strings.ToLower(raw.URL)
	if u, err := url.Parse(raw.URL); err == nil {
		p = strings.ToLower(u.Path)
	}
	if strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".markdown") || strings.HasSuffix(p, ".mdx") || strings.HasSuffix(p, "llms.txt") || strings.HasSuffix(p, "llms-full.txt") {
		return true
	}
	if strings.Contains(ct, "text/plain") {
		return !bytes.HasPrefix(bytes.TrimSpace(raw.Body), []byte("<"))
	}
	return false
}

var frontMatter = regexp.MustCompile(`(?s)\A---\r?\n.*?\r?\n---\r?\n`)

type mdHeading struct {
	level      int
	title      string
	start, end int // byte range of the whole heading block
}

func (Markdown) Parse(raw *fetch.Raw, src *config.Source) (*outline.Document, error) {
	source := frontMatter.ReplaceAll(raw.Body, nil)
	source = bytes.ReplaceAll(source, []byte("\r\n"), []byte("\n"))
	doc := goldmark.DefaultParser().Parse(text.NewReader(source))

	var hs []mdHeading
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		lines := h.Lines()
		if lines.Len() == 0 {
			return ast.WalkSkipChildren, nil
		}
		first, last := lines.At(0), lines.At(lines.Len()-1)
		start := bytes.LastIndexByte(source[:first.Start], '\n') + 1
		end := lineEnd(source, last.Stop)
		if source[start] != '#' { // setext: the underline is the next line
			end = lineEnd(source, end)
		}
		hs = append(hs, mdHeading{
			level: h.Level,
			title: plainInline(string(lines.Value(source))),
			start: start,
			end:   end,
		})
		return ast.WalkSkipChildren, nil
	})

	b := outline.NewBuilder()
	prev := 0
	seen := map[string]int{}
	for _, h := range hs {
		b.Content(string(source[prev:h.start]))
		b.Heading(h.level, h.title, githubAnchor(h.title, seen))
		prev = h.end
	}
	b.Content(string(source[prev:]))
	r, title := b.Root()
	inContent := title != ""
	if title == "" {
		title = config.NameFromURL(raw.URL)
	}
	return &outline.Document{
		Title:          title,
		URL:            stripFragment(src.PageURL(raw.URL)),
		Root:           r,
		TitleInContent: inContent,
		Format:         "markdown",
		SourceText:     string(source),
	}, nil
}

func lineEnd(b []byte, from int) int {
	if from >= len(b) {
		return len(b)
	}
	if i := bytes.IndexByte(b[from:], '\n'); i >= 0 {
		return from + i + 1
	}
	return len(b)
}

var (
	mdLink         = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis     = regexp.MustCompile("[*_`]+")
	trailingHashes = regexp.MustCompile(`\s+#+\s*$`)
)

// plainInline strips inline markdown from heading text.
func plainInline(s string) string {
	s = trailingHashes.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdEmphasis.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// githubAnchor makes the anchor GitHub would give a heading.
func githubAnchor(title string, seen map[string]int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	a := b.String()
	if n := seen[a]; n > 0 {
		seen[a] = n + 1
		return a + "-" + strconv.Itoa(n)
	}
	seen[a] = 1
	return a
}
