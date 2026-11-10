// Package plan decides how an outline becomes folders and files: which
// sections are folders, which are files, file names, INDEX maps, link
// rewriting and the coverage check. It writes nothing; store does.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/outline"
)

// File is one planned file.
type File struct {
	Path    string `json:"path"` // relative to the source folder, slash separated
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Anchor  string `json:"anchor,omitempty"`
	Source  string `json:"source"`
	Parent  string `json:"parent,omitempty"` // path of the INDEX.md that lists it
	Body    string `json:"-"`                // markdown after the H1
	Hash    string `json:"hash"`
	Tokens  int    `json:"tokens"`
	Summary string `json:"summary,omitempty"`
	Index   bool   `json:"index,omitempty"` // an INDEX.md
	Items   int    `json:"items,omitempty"` // INDEX only: entries listed
	Total   int    `json:"total_tokens,omitempty"`
	Level   int    `json:"level"`
}

// Plan is the full set of files for a source.
type Plan struct {
	Files    []*File
	Warnings []string
	Coverage Coverage
	// Largest is the biggest content file, for inspect.
	Largest *File
}

type builder struct {
	src     *config.Source
	doc     *outline.Document
	files   []*File
	anchors map[string]string // source anchor -> file path
	warns   []string
	covered []string // text that landed in files, for coverage
}

// Build plans the files for a parsed document.
func Build(doc *outline.Document, src *config.Source) *Plan {
	b := &builder{src: src, doc: doc, anchors: map[string]string{}}
	root := doc.Root
	title := doc.Title
	if title == "" {
		title = src.Name
	}
	b.folder(root, title, "", "", 0)
	b.rewriteLinks()
	for _, f := range b.files {
		f.Hash = hash(f.Body)
		f.Tokens = outline.Tokens(f.Body) + outline.Tokens(f.Title) + 2
	}
	for _, f := range b.files {
		if f.Tokens > b.src.MaxTokens {
			b.warns = append(b.warns, fmt.Sprintf("%s: %d tokens and no sub-headings to split on", f.Path, f.Tokens))
		}
	}
	b.fillIndexes()
	p := &Plan{Files: b.files, Warnings: b.warns}
	p.Coverage = checkCoverage(doc.SourceText, b.covered)
	for _, f := range b.files {
		if !f.Index && (p.Largest == nil || f.Tokens > p.Largest.Tokens) {
			p.Largest = f
		}
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	return p
}

func subtreeTokens(n *outline.Node) int {
	t := outline.Tokens(n.Title) + outline.Tokens(n.Body)
	for _, c := range n.Children {
		t += subtreeTokens(c)
	}
	return t
}

func (b *builder) asFile(n *outline.Node) bool {
	if len(n.Children) == 0 {
		return true
	}
	size := subtreeTokens(n)
	if size > b.src.MaxTokens {
		return false // over the limit: split at the next heading level
	}
	if b.src.FileLevel > 0 {
		return n.Level >= b.src.FileLevel
	}
	return size <= b.src.CollapseTokens
}

// folder writes a node as a directory: an optional _overview.md for its own
// text, its children, and an INDEX.md (filled later).
func (b *builder) folder(n *outline.Node, title, dir, parentIndex string, level int) {
	idx := &File{
		Path: path.Join(dir, "INDEX.md"), Title: title, Kind: "index", Anchor: n.Anchor,
		Source: b.sourceURL(n.Anchor), Parent: parentIndex, Index: true, Level: level,
	}
	b.files = append(b.files, idx)
	if n.Anchor != "" {
		b.anchors[n.Anchor] = idx.Path
	}
	used := map[string]bool{"index": true, "_overview": true}
	if strings.TrimSpace(n.Body) != "" {
		ov := &File{
			Path: path.Join(dir, "_overview.md"), Title: title, Kind: "overview", Anchor: n.Anchor,
			Source: b.sourceURL(n.Anchor), Parent: idx.Path, Body: n.Body, Level: level,
		}
		b.files = append(b.files, ov)
		b.covered = append(b.covered, n.Body)
	}
	if level > 0 || b.doc.TitleInContent {
		b.covered = append(b.covered, title)
	}
	for _, c := range n.Children {
		if b.asFile(c) {
			slug := unique(outline.Slug(c.Title), used)
			f := &File{
				Path: path.Join(dir, slug+".md"), Title: c.Title, Kind: b.src.KindOf(c.Title, n.Title),
				Anchor: c.Anchor, Source: b.sourceURL(c.Anchor), Parent: idx.Path,
				Body: renderSubtree(c), Level: level + 1,
			}
			b.files = append(b.files, f)
			b.covered = append(b.covered, c.Title, f.Body)
			outline.Walk(c, func(x *outline.Node) {
				if x.Anchor != "" {
					if _, ok := b.anchors[x.Anchor]; !ok {
						b.anchors[x.Anchor] = f.Path
					}
				}
			})
			continue
		}
		fslug := unique(strings.ToLower(outline.Slug(c.Title)), used)
		b.folder(c, c.Title, path.Join(dir, fslug), idx.Path, level+1)
	}
}

// renderSubtree is a section's text plus its sub-sections, with headings
// re-levelled so the file's own title is the only H1.
func renderSubtree(n *outline.Node) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(n.Body))
	var walk func(c *outline.Node, depth int)
	walk = func(c *outline.Node, depth int) {
		lvl := min(depth+1, 6)
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(strings.Repeat("#", lvl) + " " + c.Title)
		if body := strings.TrimSpace(c.Body); body != "" {
			sb.WriteString("\n\n" + body)
		}
		for _, g := range c.Children {
			walk(g, depth+1)
		}
	}
	for _, c := range n.Children {
		walk(c, 1)
	}
	return sb.String()
}

func unique(slug string, used map[string]bool) string {
	s := slug
	for i := 2; used[strings.ToLower(s)]; i++ { // case-insensitive: macOS file systems
		s = fmt.Sprintf("%s-%d", slug, i)
	}
	used[strings.ToLower(s)] = true
	return s
}

func (b *builder) sourceURL(anchor string) string {
	if anchor == "" {
		return b.doc.URL
	}
	return b.doc.URL + "#" + anchor
}

var linkRe = regexp.MustCompile(`\]\(([^)\s]+)((?:\s+"[^"]*")?)\)`)

// rewriteLinks points links to sections of this source at the local files.
func (b *builder) rewriteLinks() {
	base := b.doc.URL
	for _, f := range b.files {
		if f.Index || f.Body == "" {
			continue
		}
		dir := path.Dir(f.Path)
		f.Body = linkRe.ReplaceAllStringFunc(f.Body, func(m string) string {
			sub := linkRe.FindStringSubmatch(m)
			target, title := sub[1], sub[2]
			page, frag, _ := strings.Cut(target, "#")
			if page != base && page != "" {
				return m
			}
			local := ""
			if frag == "" {
				local = "INDEX.md"
			} else if p, ok := b.anchors[frag]; ok {
				local = p
			} else {
				return m
			}
			if local == f.Path && frag != "" && frag != f.Anchor {
				return "](#" + frag + title + ")" // a heading inside this file
			}
			return "](" + relPath(dir, local) + title + ")"
		})
	}
}

// relPath is the path from directory dir to file target (both relative to
// the source root, slash separated).
func relPath(dir, target string) string {
	if dir == "." {
		dir = ""
	}
	from := strings.Split(dir, "/")
	if dir == "" {
		from = nil
	}
	to := strings.Split(target, "/")
	i := 0
	for i < len(from) && i < len(to)-1 && from[i] == to[i] {
		i++
	}
	parts := make([]string, 0, len(from)-i+len(to)-i)
	for range from[i:] {
		parts = append(parts, "..")
	}
	parts = append(parts, to[i:]...)
	return strings.Join(parts, "/")
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// Hash exposes the content hash used for files.
func Hash(s string) string { return hash(s) }
