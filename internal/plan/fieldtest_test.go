package plan

import (
	"strings"
	"testing"

	"github.com/morethancoder/docli/internal/config"
)

// Regression tests for the gaps the agent field test found (docs/field-test.md).
// The fixtures are rendered pages saved with scripts and styles stripped.

func TestFieldFixturesCoverage(t *testing.T) {
	for _, f := range []string{"antd-home.html", "antd-toast.html", "datastar-attributes.html"} {
		t.Run(f, func(t *testing.T) {
			src := &config.Source{Name: "x", Render: "static"}
			src.Defaults()
			p, _ := load(t, f, src)
			if !p.Coverage.OK {
				t.Errorf("coverage: %+v", p.Coverage)
			}
		})
	}
}

func TestCodeHasNoLineNumbers(t *testing.T) {
	src := &config.Source{Name: "ds", Render: "static"}
	src.Defaults()
	p, _ := load(t, "datastar-attributes.html", src)
	var body string
	for _, f := range p.Files {
		if strings.HasSuffix(f.Path, "data-on-interval.md") {
			body = f.Body
		}
	}
	if !strings.Contains(body, "<div data-on-interval=\"$count++\"></div>") {
		t.Fatalf("example missing or numbered:\n%s", body)
	}
	if strings.Contains(body, "1<div") {
		t.Errorf("line numbers baked into code:\n%s", body)
	}
}

func TestTableWithLineBreakStaysATable(t *testing.T) {
	p, _ := telegram(t)
	f := byPath(p)["available-types/InlineKeyboardButton.md"]
	if f == nil {
		t.Fatal("no InlineKeyboardButton file")
	}
	if !strings.Contains(f.Body, "|callback_data|String|") {
		t.Errorf("type fields are not a table:\n%.600s", f.Body)
	}
}

func TestCoverageIgnoresMarkupEdges(t *testing.T) {
	cases := []struct {
		name, source, out string
	}{
		{"fence label", "Toast.config({ duration: 1000 })", "```ts\nToast.config({ duration: 1000 })\n```"},
		{"split at a link", "Made with love byXTech", "Made with love by[XTech](https://x.example)"},
		{"joined links", "Get Started Preview", "[Get Started](/a)[Preview](/b)"},
		{"br in a table cell", "May be empty. This offers", "|May be empty.<br />This offers|"},
	}
	for _, c := range cases {
		if cov := checkCoverage(c.source, []string{c.out}); !cov.OK {
			t.Errorf("%s: %+v", c.name, cov)
		}
	}
	// A real loss is still caught.
	if cov := checkCoverage("alpha beta gamma", []string{"alpha gamma"}); cov.OK || cov.Missing != 1 {
		t.Errorf("lost word not caught: %+v", cov)
	}
}
