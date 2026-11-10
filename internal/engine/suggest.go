package engine

import (
	"bytes"
	"context"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/morethancoder/docli/internal/fetch"
)

// Suggestion is a likely docs address found next to a thin page.
type Suggestion struct {
	URL string `json:"url"`
	Why string `json:"why"`
}

// ThinFiles is the most files a page can split into and still look like a
// landing page rather than docs.
const ThinFiles = 12

// docSegments are first path segments that usually hold docs.
var docSegments = map[string]bool{
	"docs": true, "doc": true, "documentation": true, "guide": true, "guides": true, "reference": true,
	"api": true, "components": true, "component": true, "manual": true, "learn": true, "tutorial": true,
	"tutorials": true, "handbook": true, "how_tos": true, "how-tos": true, "examples": true,
}

// wholeDocs are files some sites publish with every page in one text file.
var wholeDocs = []struct{ path, why string }{
	{"/llms-full.txt", "all docs in one Markdown file (llms-full.txt)"},
	{"/llms.txt", "an llms.txt map of the docs"},
	{"/docs.md", "all docs in one Markdown file"},
}

// Thin says a loaded page is probably a landing page: an HTML page outside
// any docs path (/docs, /components …) that split into only a few files. A
// short page under a docs path (one component) is real docs.
func Thin(l *Loaded) bool {
	if l.Result.Parser != "html" || !few(l) {
		return false
	}
	u, err := url.Parse(l.Result.Raw.URL)
	return err != nil || !docSegments[firstSegment(u.Path)]
}

// few says a web page (HTML, or Markdown served at a page URL like
// trpc.io/docs) split into only a few files. A text file such as llms.txt or
// docs.md is the whole docs already.
func few(l *Loaded) bool {
	raw := l.Result.Raw.URL
	if strings.HasPrefix(raw, "file:") || (l.Result.Parser != "html" && l.Result.Parser != "markdown") {
		return false
	}
	if u, err := url.Parse(raw); err != nil || path.Ext(u.Path) != "" {
		return false
	}
	n := 0
	for _, f := range l.Plan.Files {
		if !f.Index {
			n++
		}
	}
	return n <= ThinFiles
}

// Suggest finds where the real docs may be when a page split into few files.
// For a landing page (Thin) that is whole-docs files at the site root and
// doc-like links on the page, menus included. For the short root page of a
// docs path (an overview like /docs or /guide) it is only whole-docs files, at
// the site root and under that path (/docs/llms-full.txt): its menu links are
// the docs already, and a docs root that redirects (resend.com/docs to
// /docs/introduction) counts too. A deeper short page (one component) gets
// nothing. Files are checked over the network, 5 s at most.
func Suggest(ctx context.Context, l *Loaded) []Suggestion {
	if !few(l) {
		return nil
	}
	page, err := url.Parse(l.Result.Raw.URL)
	if err != nil || page.Host == "" {
		return nil
	}
	landing := Thin(l)
	if !landing && deep(page.Path) {
		if asked, err := url.Parse(l.Asked); err != nil || deep(asked.Path) {
			return nil
		}
	}
	base := page.Scheme + "://" + page.Host
	// Files under the docs path are the more specific ones, so they go first.
	var cands []Suggestion
	if seg := firstSegment(page.Path); !landing && seg != "" {
		for _, w := range wholeDocs[:2] { // llms-full.txt and llms.txt under the docs path
			cands = append(cands, Suggestion{URL: base + "/" + seg + w.path, Why: w.why})
		}
	}
	for _, w := range wholeDocs {
		cands = append(cands, Suggestion{URL: base + w.path, Why: w.why})
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	found := make([]bool, len(cands))
	var wg sync.WaitGroup
	for i, c := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = fetch.Probe(ctx, c.URL)
		}()
	}
	wg.Wait()
	var out []Suggestion
	for i, c := range cands {
		if found[i] {
			out = append(out, c)
		}
	}
	if !landing {
		return out
	}
	return append(out, DocLinks(l.Result.Raw.Body, page, 8)...)
}

// DocLinks lists up to max same-site links on a page whose first path
// segment usually holds docs (/docs, /guide, /components …), one per path.
func DocLinks(body []byte, page *url.URL, max int) []Suggestion {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	seen := map[string]bool{strings.TrimSuffix(page.Path, "/"): true}
	var out []Suggestion
	doc.Find("a[href]").EachWithBreak(func(_ int, a *goquery.Selection) bool {
		h, _ := a.Attr("href")
		u, err := page.Parse(strings.TrimSpace(h))
		if err != nil || u.Host != page.Host || (u.Scheme != "http" && u.Scheme != "https") {
			return true
		}
		p := strings.TrimSuffix(u.Path, "/")
		if !docSegments[firstSegment(p)] || seen[p] {
			return true
		}
		seen[p] = true
		u.Fragment, u.RawQuery = "", ""
		text := strings.Join(strings.Fields(a.Text()), " ")
		if r := []rune(text); len(r) > 60 {
			text = string(r[:60])
		}
		why := "docs link on the page"
		if text != "" {
			why += ": " + text
		}
		out = append(out, Suggestion{URL: u.String(), Why: why})
		return len(out) < max
	})
	return out
}

// deep says a path is more than one segment long (/components/toast).
func deep(p string) bool { return strings.Contains(strings.Trim(p, "/"), "/") }

func firstSegment(p string) string {
	return strings.ToLower(strings.SplitN(strings.TrimPrefix(p, "/"), "/", 2)[0])
}
