package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/parse"
	"github.com/morethancoder/docli/internal/plan"
)

const v1 = `# Kit

Intro text.

## Methods

### open

Opens a thing.

### close

Closes a thing.

### ping

Checks the thing.

## Types

### Thing

A thing has a name.

### Other

Another type. See [open](#open).

## Changes

### March 2, 2026

- Added ping.

### January 5, 2026

- First release.
`

// apply writes doc text as a source named "kit" and returns the result.
func apply(t *testing.T, st *Store, doc string, now time.Time) *Result {
	t.Helper()
	// One source file per store, so the source URL stays the same across syncs.
	file := filepath.Join(filepath.Dir(st.Root), "kit.md")
	if err := os.WriteFile(file, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	src := &config.Source{Name: "kit", URL: "file://" + file, FileLevel: 3, Changelog: "Changes"}
	src.Defaults()
	res, err := parse.Load(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	p := plan.Build(res.Doc, src)
	if !p.Coverage.OK {
		t.Fatalf("coverage %+v", p.Coverage)
	}
	if err := st.SaveConfig(src); err != nil {
		t.Fatal(err)
	}
	r, err := st.Apply(src, p, Meta{Title: res.Doc.Title, Parser: res.Parser, Fingerprint: "fp"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateChangelog("kit"); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestApplyVersionsAndDiff(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), DirName))
	day1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	r1 := apply(t, st, v1, day1)
	if r1.Version != 1 || !r1.Changed || len(r1.Added) == 0 {
		t.Fatalf("first apply: %+v", r1)
	}

	// Same text again: no new version, nothing written.
	r := apply(t, st, v1, day1.Add(time.Hour))
	if r.Version != 1 || r.Changed || r.Written != 0 {
		t.Fatalf("unchanged apply: version %d changed %v written %d", r.Version, r.Changed, r.Written)
	}

	// Change one method, remove one, add one.
	v2 := strings.Replace(v1, "Opens a thing.", "Opens a thing quickly.", 1)
	v2 = strings.Replace(v2, "### ping\n\nChecks the thing.\n\n", "", 1)
	v2 = strings.Replace(v2, "## Types", "### reset\n\nResets the thing.\n\n## Types", 1)
	day3 := time.Date(2026, 3, 3, 10, 0, 0, 0, time.UTC)
	r2 := apply(t, st, v2, day3)
	if r2.Version != 2 {
		t.Fatalf("version %d", r2.Version)
	}
	if !slices.Contains(r2.Added, "methods/reset.md") || !slices.Contains(r2.Modified, "methods/open.md") ||
		!slices.Contains(r2.Removed, "methods/ping.md") {
		t.Fatalf("added %v changed %v removed %v", r2.Added, r2.Modified, r2.Removed)
	}
	if slices.Contains(r2.Modified, "types/Thing.md") {
		t.Error("untouched file reported as changed")
	}
	if _, err := os.Stat(filepath.Join(st.Dir("kit"), "methods", "ping.md")); !os.IsNotExist(err) {
		t.Error("removed file still on disk")
	}

	// Unchanged files keep the version they were last changed in.
	h := header(t, st, "types/Thing.md")
	if h.Version != 1 {
		t.Errorf("Thing.md version %d, want 1", h.Version)
	}
	if h := header(t, st, "methods/open.md"); h.Version != 2 {
		t.Errorf("open.md version %d, want 2", h.Version)
	}

	for _, since := range []string{"1", "v1", "2026-03-02"} {
		d, err := st.Diff("kit", since)
		if err != nil {
			t.Fatal(err)
		}
		if d.Since != 1 || d.Current != 2 {
			t.Errorf("since %s: base %d current %d", since, d.Since, d.Current)
		}
		st := map[string]string{}
		for _, f := range d.Files {
			st[f.Path] = f.Status
		}
		if st["methods/open.md"] != "changed" || st["methods/ping.md"] != "removed" || st["methods/reset.md"] != "added" {
			t.Errorf("since %s: statuses %v", since, st)
		}
		for _, f := range d.Files {
			if f.Path == "methods/open.md" && !strings.Contains(f.Diff, "+Opens a thing quickly.") {
				t.Errorf("diff of open.md:\n%s", f.Diff)
			}
		}
	}

	cl, err := st.Changelog("kit")
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 2 || cl[0].Date != "2026-03-02" || cl[1].Date != "2026-01-05" || cl[0].Items[0] != "Added ping." {
		t.Errorf("changelog %+v", cl)
	}
}

func header(t *testing.T, st *Store, p string) Header {
	t.Helper()
	data, err := st.ReadFile("kit", p)
	if err != nil {
		t.Fatal(err)
	}
	h, _, err := SplitFile(data)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestFindAndSearch(t *testing.T) {
	st := Open(filepath.Join(t.TempDir(), DirName))
	apply(t, st, v1, time.Now())

	for ref, want := range map[string]string{
		"open":              "methods/open.md",
		"kit/open":          "methods/open.md",
		"methods/open":      "methods/open.md",
		"methods/open.md":   "methods/open.md",
		"THING":             "types/Thing.md",
		"methods":           "methods/INDEX.md",
		"kit:types/Thing":   "",
		"no-such-thing-xyz": "",
	} {
		h, err := st.Find(ref, "")
		switch {
		case want == "" && err == nil:
			t.Errorf("Find(%q) = %s, want an error", ref, h.Path)
		case want != "" && (err != nil || h.Path != want):
			t.Errorf("Find(%q) = %q, %v; want %s", ref, h.Path, err, want)
		}
	}
	if _, err := st.Find("no-such-thing-xyz", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("not found error: %v", err)
	}

	ms, err := st.Search("thing", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].Title != "Thing" || ms[0].By != "name" {
		t.Fatalf("search: %+v", ms)
	}
	var content bool
	for _, m := range ms {
		if m.Path == "methods/open.md" && m.By == "content" && m.Line > 0 && strings.Contains(m.Snippet, "thing") {
			content = true
		}
	}
	if !content {
		t.Errorf("no content hit for open.md: %+v", ms)
	}
}

func TestHeaderRoundTrip(t *testing.T) {
	h := Header{Title: "a: b", Kind: "method", Source: "https://x/#a", Path: "m/a.md", Parent: "m/INDEX.md", Version: 3, Hash: "ab", Tokens: 9}
	data := Render(h, "Body with --- inside.\n")
	got, body, err := SplitFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Errorf("header %+v, want %+v", got, h)
	}
	if !strings.Contains(body, "Body with --- inside.") {
		t.Errorf("body %q", body)
	}
}
