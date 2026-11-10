package parse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/fetch"
	"github.com/morethancoder/docli/internal/outline"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return "file://" + p
}

func find(root *outline.Node, title string) *outline.Node {
	var hit *outline.Node
	outline.Walk(root, func(n *outline.Node) {
		if hit == nil && n.Title == title {
			hit = n
		}
	})
	return hit
}

func TestTelegramGeneric(t *testing.T) {
	// No preset: the generic rules must still find the docs and skip the footer.
	src := &config.Source{Name: "tg", URL: fixture(t, "telegram.html")}
	src.Defaults()
	res, err := Load(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Parser != "html" || res.Rendered {
		t.Fatalf("parser %s rendered %v", res.Parser, res.Rendered)
	}
	if res.Doc.Title != "Telegram Bot API" {
		t.Errorf("title %q", res.Doc.Title)
	}
	if n := len(res.Doc.Root.Children); n != 14 {
		t.Errorf("top sections: got %d, want 14", n)
	}
	if find(res.Doc.Root, "About") != nil {
		t.Error("footer heading 'About' was parsed as content")
	}
	sm := find(res.Doc.Root, "sendMessage")
	if sm == nil {
		t.Fatal("no sendMessage section")
	}
	if sm.Anchor != "sendmessage" {
		t.Errorf("anchor %q", sm.Anchor)
	}
	if !strings.Contains(sm.Body, "|chat_id|") || strings.Contains(sm.Body, `chat\_id`) {
		t.Errorf("parameter names not clean:\n%.400s", sm.Body)
	}
}

func TestMarkdown(t *testing.T) {
	src := &config.Source{Name: "sdk", URL: fixture(t, "llms.md")}
	src.Defaults()
	res, err := Load(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	d := res.Doc
	if res.Parser != "markdown" || d.Title != "Widget SDK" || !d.TitleInContent {
		t.Fatalf("parser %s title %q in content %v", res.Parser, d.Title, d.TitleInContent)
	}
	var titles, anchors []string
	outline.Walk(d.Root, func(n *outline.Node) {
		if n.Level > 0 {
			titles = append(titles, n.Title)
			anchors = append(anchors, n.Anchor)
		}
	})
	want := []string{"Install", "API", "createWidget", "destroyWidget", "API", "Setext Heading"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Errorf("headings %q, want %q", titles, want)
	}
	if anchors[4] != "api-1" {
		t.Errorf("second API anchor %q, want api-1", anchors[4])
	}
	if strings.Contains(d.SourceText, "generator: test fixture") {
		t.Error("front matter kept")
	}
	if !strings.Contains(find(d.Root, "Install").Body, "# this line is a shell comment") {
		t.Error("comment inside a code fence was lost or taken as a heading")
	}
}

func TestBrowserRender(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	if _, err := fetch.FindBrowser(); err != nil {
		t.Skip("no Chrome/Chromium found: ", err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Join("..", "..", "testdata"))))
	defer srv.Close()
	src := &config.Source{Name: "js", URL: srv.URL + "/js.html"}
	src.Defaults() // render auto: static first, browser when the page is empty
	res, err := Load(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rendered || res.Note == "" {
		t.Fatalf("expected browser fallback, rendered=%v note=%q", res.Rendered, res.Note)
	}
	for _, h := range []string{"Topic 1", "Topic 2", "Topic 3"} {
		if find(res.Doc.Root, h) == nil {
			t.Errorf("missing %q after rendering", h)
		}
	}
}
