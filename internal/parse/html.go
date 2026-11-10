package parse

import (
	"bytes"
	"fmt"
	"net/url"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"docli/internal/config"
	"docli/internal/fetch"
	"docli/internal/outline"
)

// HTML parses HTML pages: picks the content region, drops chrome, and turns
// each heading into a section whose content is converted to markdown.
type HTML struct{}

func (HTML) Name() string { return "html" }

func (HTML) Detect(raw *fetch.Raw) bool {
	ct := strings.ToLower(raw.ContentType)
	if strings.Contains(ct, "html") {
		return true
	}
	head := bytes.ToLower(bytes.TrimSpace(firstBytes(raw.Body, 512)))
	return bytes.HasPrefix(head, []byte("<!doctype html")) || bytes.HasPrefix(head, []byte("<html"))
}

// defaultDrop is removed before parsing on every HTML page.
var defaultDrop = []string{"script", "style", "noscript", "template", "svg", "iframe", "link", "meta"}

// chromeDrop is removed only when no content selector is set, since then we
// parse a whole page that still carries site menus.
var chromeDrop = []string{"nav", "header", "footer", "[role=navigation]", "[role=banner]", "[role=contentinfo]", "aside"}

// rootCandidates are tried in order when no selector is set.
var rootCandidates = []string{"main article", "article", "main", "[role=main]", "#content", ".content", ".markdown-body", "body"}

func newConverter() *converter.Converter {
	return converter.NewConverter(converter.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(),
		strikethrough.NewStrikethroughPlugin(),
		table.NewTablePlugin(
			table.WithCellPaddingBehavior(table.CellPaddingBehaviorNone),
			table.WithSpanCellBehavior(table.SpanBehaviorMirror),
		),
	))
}

// ContentRoot returns the selected content region of a page, after dropping
// unwanted elements. Exposed for the stale check and inspect.
func ContentRoot(raw *fetch.Raw, src *config.Source) (*goquery.Selection, *goquery.Document, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(raw.Body))
	if err != nil {
		return nil, nil, err
	}
	for _, sel := range defaultDrop {
		doc.Find(sel).Remove()
	}
	for _, sel := range src.Drop {
		doc.Find(sel).Remove()
	}
	var root *goquery.Selection
	if src.Selector != "" {
		root = doc.Find(src.Selector).First()
		if root.Length() == 0 {
			return nil, nil, fmt.Errorf("selector %q matched nothing on %s", src.Selector, raw.URL)
		}
	} else {
		for _, sel := range chromeDrop {
			doc.Find(sel).Remove()
		}
		for _, sel := range rootCandidates {
			if r := doc.Find(sel).First(); r.Length() > 0 && len(strings.TrimSpace(r.Text())) > 200 {
				root = r
				break
			}
		}
		if root == nil {
			root = doc.Find("body").First()
		}
		if goquery.NodeName(root) == "body" {
			root = densest(root)
		}
	}
	// Make every link absolute so the link rewriter can map them later.
	base, _ := url.Parse(src.PageURL(raw.URL))
	root.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		if h, ok := a.Attr("href"); ok && base != nil {
			if u, err := base.Parse(strings.TrimSpace(h)); err == nil {
				a.SetAttr("href", u.String())
			}
		}
	})
	root.Find("img[src]").Each(func(_ int, im *goquery.Selection) {
		if s, ok := im.Attr("src"); ok && base != nil {
			if u, err := base.Parse(strings.TrimSpace(s)); err == nil {
				im.SetAttr("src", u.String())
			}
		}
	})
	return root, doc, nil
}

// densest narrows a page with no semantic content element (Telegram puts
// its footer in plain divs) to the deepest element that still holds at least
// 90% of the h1-h4 headings, so site chrome around the docs is left out.
func densest(root *goquery.Selection) *goquery.Selection {
	const headings = "h1, h2, h3, h4"
	total := root.Find(headings).Length()
	if total < 3 {
		return root
	}
	cur := root
	for {
		var next *goquery.Selection
		cur.Children().EachWithBreak(func(_ int, c *goquery.Selection) bool {
			if c.Find(headings).Length()*10 >= total*9 {
				next = c
				return false
			}
			return true
		})
		if next == nil {
			return cur
		}
		cur = next
	}
}

func (HTML) Parse(raw *fetch.Raw, src *config.Source) (*outline.Document, error) {
	root, doc, err := ContentRoot(raw, src)
	if err != nil {
		return nil, err
	}
	conv := newConverter()
	b := outline.NewBuilder()
	var pending []*html.Node
	var convErr error
	flush := func() {
		if len(pending) == 0 {
			return
		}
		var buf bytes.Buffer
		for _, n := range pending {
			_ = html.Render(&buf, n)
		}
		pending = pending[:0]
		md, err := conv.ConvertString(buf.String())
		if err != nil && convErr == nil {
			convErr = err
		}
		b.Content(unescapeIntraword(md))
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode {
				if lvl := headingLevel(c.Data); lvl > 0 {
					flush()
					s := goquery.NewDocumentFromNode(c).Selection
					b.Heading(lvl, cleanTitle(s.Text()), headingAnchor(c))
					continue
				}
				if containsHeading(c) {
					walk(c)
					continue
				}
			}
			if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
				continue
			}
			if c.Type == html.CommentNode {
				continue
			}
			pending = append(pending, c)
		}
	}
	for _, n := range root.Nodes {
		walk(n)
	}
	flush()
	if convErr != nil {
		return nil, fmt.Errorf("html to markdown: %w", convErr)
	}
	r, title := b.Root()
	inContent := title != ""
	if title == "" {
		title = strings.TrimSpace(doc.Find("title").First().Text())
	}
	return &outline.Document{
		Title:          title,
		URL:            stripFragment(src.PageURL(raw.URL)),
		Root:           r,
		TitleInContent: inContent,
		Format:         "html",
		SourceText:     blockText(root.Nodes),
	}, nil
}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

func containsHeading(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (headingLevel(c.Data) > 0 || containsHeading(c)) {
			return true
		}
	}
	return false
}

// headingAnchor finds the fragment id of a heading: its id, or the id/name of
// an anchor inside it, or an id on its direct parent (common in doc themes).
func headingAnchor(n *html.Node) string {
	if v := attr(n, "id"); v != "" {
		return v
	}
	var found string
	var f func(*html.Node)
	f = func(x *html.Node) {
		for c := x.FirstChild; c != nil && found == ""; c = c.NextSibling {
			if c.Type == html.ElementNode {
				if v := attr(c, "id"); v != "" {
					found = v
					return
				}
				if c.Data == "a" {
					if v := attr(c, "name"); v != "" {
						found = v
						return
					}
					if h := attr(c, "href"); strings.Contains(h, "#") {
						found = h[strings.LastIndex(h, "#")+1:]
						return
					}
				}
				f(c)
			}
		}
	}
	f(n)
	return found
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func cleanTitle(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimSuffix(strings.TrimSuffix(s, "#"), "¶")
	return strings.TrimSpace(s)
}

func stripFragment(u string) string {
	if i := strings.Index(u, "#"); i >= 0 {
		return u[:i]
	}
	return u
}

// blockText is the plain text of nodes with a space at every block or
// line-break boundary, so words in neighbouring cells or lines don't glue
// together (goquery's Text() would give "colorsdark" for "colors<br>dark").
func blockText(nodes []*html.Node) string {
	var sb strings.Builder
	var f func(*html.Node)
	f = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			sb.WriteString(n.Data)
			return
		case html.ElementNode:
			if !inlineTags[n.Data] {
				sb.WriteByte(' ')
				defer sb.WriteByte(' ')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	for _, n := range nodes {
		f(n)
	}
	return sb.String()
}

var inlineTags = map[string]bool{
	"a": true, "abbr": true, "b": true, "bdi": true, "bdo": true, "cite": true, "code": true,
	"data": true, "dfn": true, "em": true, "i": true, "kbd": true, "mark": true, "q": true,
	"s": true, "samp": true, "small": true, "span": true, "strong": true, "sub": true,
	"sup": true, "time": true, "u": true, "var": true, "del": true, "ins": true,
}

// unescapeIntraword turns `chat\_id` back into `chat_id`. An underscore
// between two word characters never starts emphasis in CommonMark, so the
// backslash only costs bytes and breaks grep for field names.
func unescapeIntraword(s string) string {
	if !strings.Contains(s, `\_`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+2 < len(s) && s[i+1] == '_' && i > 0 && isWordByte(s[i-1]) && isWordByte(s[i+2]) {
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func firstBytes(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
