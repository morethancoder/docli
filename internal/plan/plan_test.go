package plan

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"docli/internal/config"
	"docli/internal/parse"
)

func load(t *testing.T, fixture string, src *config.Source) (*Plan, *config.Source) {
	t.Helper()
	p, _ := filepath.Abs(filepath.Join("..", "..", "testdata", fixture))
	src.URL = "file://" + p
	res, err := parse.Load(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	return Build(res.Doc, src), src
}

func telegram(t *testing.T) (*Plan, *config.Source) {
	src, ok := config.Preset("telegram-bot-api")
	if !ok {
		t.Fatal("no telegram preset")
	}
	return load(t, "telegram.html", src)
}

func byPath(p *Plan) map[string]*File {
	m := map[string]*File{}
	for _, f := range p.Files {
		m[f.Path] = f
	}
	return m
}

func TestTelegramPlan(t *testing.T) {
	p, src := telegram(t)
	if !p.Coverage.OK {
		t.Fatalf("coverage: %+v", p.Coverage)
	}
	if len(p.Warnings) > 0 {
		t.Errorf("warnings: %v", p.Warnings)
	}
	files := byPath(p)
	for _, want := range []string{"INDEX.md", "available-methods/INDEX.md", "available-methods/sendMessage.md",
		"available-types/Message.md", "games/INDEX.md", "recent-changes/INDEX.md"} {
		if files[want] == nil {
			t.Errorf("missing %s", want)
		}
	}
	sm := files["available-methods/sendMessage.md"]
	if sm != nil && (sm.Kind != "method" || sm.Title != "sendMessage") {
		t.Errorf("sendMessage kind %q title %q", sm.Kind, sm.Title)
	}
	if m := files["available-types/Message.md"]; m != nil && m.Kind != "type" {
		t.Errorf("Message kind %q", m.Kind)
	}
	// AC7 is measured, not trimmed: report the size.
	t.Logf("sendMessage body: %d bytes, %d tokens", len(sm.Body), sm.Tokens)

	dirs := map[string]bool{}
	// Links to the page itself must become file links (or in-file anchors).
	// Only anchors the page does not define may stay absolute.
	external := regexp.MustCompile(`\]\(https://core\.telegram\.org/bots/api#[^)]*\)`)
	html, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "telegram.html"))
	for _, f := range p.Files {
		d := filepath.Dir(f.Path)
		dirs[d] = true
		if d != strings.ToLower(d) {
			t.Errorf("folder not lowercase: %s", f.Path)
		}
		if !f.Index && f.Tokens > src.MaxTokens {
			t.Errorf("%s: %d tokens over max %d", f.Path, f.Tokens, src.MaxTokens)
		}
		if strings.Contains(f.Body, "](file://") {
			t.Errorf("%s: link resolved against the local file", f.Path)
		}
		for _, l := range external.FindAllString(f.Body, -1) {
			frag := strings.TrimSuffix(l[strings.Index(l, "#")+1:], ")")
			if strings.Contains(string(html), `name="`+frag+`"`) {
				t.Errorf("%s: link to #%s left unrewritten", f.Path, frag)
			}
		}
	}
	for d := range dirs {
		if files[filepath.Join(d, "INDEX.md")] == nil && !(d == "." && files["INDEX.md"] != nil) {
			t.Errorf("folder %s has no INDEX.md", d)
		}
	}
	if !strings.Contains(sm.Body, "](../available-types/Message.md)") {
		t.Error("sendMessage does not link to Message relatively")
	}
	idx := files["available-methods/INDEX.md"].Body
	if !regexp.MustCompile(`(?m)^- \[sendMessage\]\(sendMessage\.md\) method · \d+ tok — Use this method to send text messages\.`).MatchString(idx) {
		t.Errorf("INDEX line for sendMessage wrong:\n%.600s", idx)
	}
}

func TestMarkdownPlan(t *testing.T) {
	src := &config.Source{Name: "sdk"}
	src.Defaults()
	p, _ := load(t, "llms.md", src)
	if !p.Coverage.OK {
		t.Fatalf("coverage: %+v", p.Coverage)
	}
	files := byPath(p)
	// Small doc: sections stay whole; the duplicate "API" title gets a suffix.
	for _, want := range []string{"INDEX.md", "Install.md", "API.md", "API-2.md", "setext-heading.md"} {
		if files[want] == nil {
			var got []string
			for k := range files {
				got = append(got, k)
			}
			t.Fatalf("missing %s; have %v", want, got)
		}
	}
	if !strings.Contains(files["API.md"].Body, "](#createwidget)") && !strings.Contains(files["API.md"].Body, "createWidget") {
		t.Error("API section lost its content")
	}
}

func TestSplitByFileLevel(t *testing.T) {
	src := &config.Source{Name: "sdk", FileLevel: 3}
	src.Defaults()
	p, _ := load(t, "llms.md", src)
	files := byPath(p)
	if files["api/createWidget.md"] == nil || files["api/INDEX.md"] == nil {
		var got []string
		for k := range files {
			got = append(got, k)
		}
		t.Fatalf("file_level 3 should make api/ a folder with one file per h3; have %v", got)
	}
	if !strings.Contains(files["api/destroyWidget.md"].Body, "](createWidget.md)") {
		t.Errorf("anchor link not rewritten to a file: %q", files["api/destroyWidget.md"].Body)
	}
	if !p.Coverage.OK {
		t.Fatalf("coverage: %+v", p.Coverage)
	}
}
