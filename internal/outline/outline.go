// Package outline holds the common heading tree that every source type is
// parsed into. Fetching and parsing produce an outline; splitting, storage,
// sync and diff only ever see outlines, so a new source type needs only a new
// parser.
package outline

import (
	"strings"
	"unicode"
)

// Node is one heading and the content under it, up to the next heading of the
// same or a higher level.
type Node struct {
	Title    string  // heading text, plain
	Level    int     // 1-6 for headings, 0 for the document root
	Anchor   string  // fragment id in the source (without '#'), may be empty
	Body     string  // markdown content directly under this heading (no heading line)
	Children []*Node // sub-headings, in source order
	Parent   *Node   `json:"-"`
}

// Document is a parsed source.
type Document struct {
	Title  string
	URL    string // canonical source URL (no fragment)
	Root   *Node  // Level 0; its Body is the text before the first heading
	Format string // "html" or "markdown"
	// TitleInContent is true when Title came from a heading inside the
	// content (not from <title> or the URL).
	TitleInContent bool
	// SourceText is the plain text of the content region as it was in the
	// source, used by the coverage check.
	SourceText string
}

// Builder turns a flat stream of headings and content into a tree.
type Builder struct {
	root  *Node
	stack []*Node
	buf   strings.Builder
}

// NewBuilder starts a tree with an empty root.
func NewBuilder() *Builder {
	r := &Node{Level: 0}
	return &Builder{root: r, stack: []*Node{r}}
}

func (b *Builder) cur() *Node { return b.stack[len(b.stack)-1] }

func (b *Builder) flush() {
	c := b.cur()
	text := strings.TrimSpace(b.buf.String())
	if text != "" {
		if c.Body != "" {
			c.Body += "\n\n"
		}
		c.Body += text
	}
	b.buf.Reset()
}

// Heading opens a new section at level (1-6).
func (b *Builder) Heading(level int, title, anchor string) {
	b.flush()
	for len(b.stack) > 1 && b.cur().Level >= level {
		b.stack = b.stack[:len(b.stack)-1]
	}
	p := b.cur()
	n := &Node{Title: title, Level: level, Anchor: anchor, Parent: p}
	p.Children = append(p.Children, n)
	b.stack = append(b.stack, n)
}

// Content appends markdown to the current section.
func (b *Builder) Content(md string) {
	if strings.TrimSpace(md) == "" {
		return
	}
	if b.buf.Len() > 0 {
		b.buf.WriteString("\n\n")
	}
	b.buf.WriteString(strings.TrimSpace(md))
}

// Root finishes the tree. When the document has a single top heading that
// holds everything (a page title), it is hoisted into the root so the tree
// starts at the real subjects.
func (b *Builder) Root() (*Node, string) {
	b.flush()
	r := b.root
	title := ""
	if len(r.Children) == 1 && len(r.Children[0].Children) > 0 {
		top := r.Children[0]
		title = top.Title
		body := strings.TrimSpace(r.Body + "\n\n" + top.Body)
		r.Body = body
		r.Anchor = top.Anchor
		r.Children = top.Children
		for _, c := range r.Children {
			c.Parent = r
		}
	}
	return r, title
}

// Walk visits n and its descendants depth-first.
func Walk(n *Node, fn func(*Node)) {
	fn(n)
	for _, c := range n.Children {
		Walk(c, fn)
	}
}

// Slug makes a file-system-safe name. Single identifiers (sendMessage,
// InputFile) keep their exact spelling so agents can guess paths; anything
// else becomes kebab-case.
func Slug(title string) string {
	t := strings.TrimSpace(title)
	if t != "" && isIdent(t) {
		return t
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(t) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 60 {
		s = strings.Trim(s[:60], "-")
	}
	if s == "" {
		s = "section"
	}
	return s
}

func isIdent(s string) bool {
	if len(s) > 60 {
		return false
	}
	for i, r := range s {
		if r == '_' || (r < 128 && (unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r)))) {
			continue
		}
		return false
	}
	return true
}

// Tokens estimates tokens for text (about 4 bytes per token). It is an
// estimate for budgeting reads, not an exact tokenizer count.
func Tokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}
