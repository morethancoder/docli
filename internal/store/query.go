package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aymanbagabas/go-udiff"
)

// ---- changelog ----

// Change is one dated entry of a source's changelog.
type Change struct {
	Date  string   `json:"date"` // YYYY-MM-DD
	Title string   `json:"title"`
	Path  string   `json:"path"`
	Items []string `json:"items"`
}

var dateLayouts = []string{"January 2, 2006", "Jan 2, 2006", "2006-01-02", "2 January 2006", "January 2006"}

// ParseDate reads the date forms docs use in changelog headings.
func ParseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

var mdLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

// UpdateChangelog reads the changelog-entry files of a source and merges
// them into .meta/changelog.json. Entries are kept across syncs, so history
// grows even when the page only shows recent changes.
func (s *Store) UpdateChangelog(name string) ([]Change, error) {
	m, err := s.Manifest(name)
	if err != nil {
		return nil, err
	}
	have, err := s.Changelog(name)
	if err != nil {
		return nil, err
	}
	byDate := map[string]Change{}
	for _, c := range have {
		byDate[c.Date+"|"+c.Title] = c
	}
	for p, f := range m.Files {
		if f.Kind != "changelog-entry" {
			continue
		}
		t, ok := ParseDate(f.Title)
		if !ok {
			continue
		}
		data, err := s.ReadFile(name, p)
		if err != nil {
			return nil, err
		}
		_, body, _ := SplitFile(data)
		c := Change{Date: t.Format("2006-01-02"), Title: f.Title, Path: p, Items: listItems(body)}
		byDate[c.Date+"|"+c.Title] = c
	}
	out := make([]Change, 0, len(byDate))
	for _, c := range byDate {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, writeJSON(s.meta(name, "changelog.json"), out)
}

// Changelog returns the saved changelog, newest first.
func (s *Store) Changelog(name string) ([]Change, error) {
	var out []Change
	if err := readJSON(s.meta(name, "changelog.json"), &out); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return out, nil
}

// listItems returns the top-level list items of markdown as plain text.
func listItems(body string) []string {
	var items []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			if len(items) > 0 && strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
				items[len(items)-1] += " " + strings.TrimSpace(mdLink.ReplaceAllString(line, "$1"))
			}
			continue
		}
		items = append(items, strings.TrimSpace(mdLink.ReplaceAllString(line[2:], "$1")))
	}
	return items
}

// ---- find ----

// Hit is a stored file that matches a lookup.
type Hit struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Tokens int    `json:"tokens"`
}

// ErrNotFound is returned when nothing matches.
var ErrNotFound = errors.New("not found")

// AmbiguousError lists the candidates when a lookup matches more than one
// file equally well.
type AmbiguousError struct{ Hits []Hit }

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	b.WriteString("more than one match, name one of:")
	for _, h := range e.Hits {
		fmt.Fprintf(&b, "\n  %s/%s", h.Source, h.Path)
	}
	return b.String()
}

// Find resolves a reference to one file. A reference can be a path
// ("available-methods/sendMessage.md", with or without .md, with or without
// the source prefix), a folder (gives its INDEX.md), or a section title
// ("sendMessage", "Recent changes"). source limits the search when set.
func (s *Store) Find(ref, source string) (Hit, error) {
	if source != "" {
		if !s.Has(source) {
			return Hit{}, fmt.Errorf("no source named %q", source)
		}
		return s.find(strings.Trim(strings.TrimSpace(ref), "/"), []string{source})
	}
	names, err := s.Sources()
	if err != nil {
		return Hit{}, err
	}
	return s.FindIn(ref, names)
}

// FindIn is Find over the given sources only. A "<source>/" prefix, or a
// bare source name, is understood when that source is one of names.
func (s *Store) FindIn(ref string, names []string) (Hit, error) {
	ref = strings.Trim(strings.TrimSpace(ref), "/")
	if src, rest, ok := strings.Cut(ref, "/"); ok && slices.Contains(names, src) {
		return s.find(rest, []string{src})
	}
	if slices.Contains(names, ref) {
		return s.find("", []string{ref})
	}
	return s.find(ref, names)
}

func (s *Store) find(ref string, names []string) (Hit, error) {
	// Tiers: exact path, exact title, case-insensitive title, path suffix.
	tiers := make([][]Hit, 4)
	for _, n := range names {
		m, err := s.Manifest(n)
		if err != nil {
			return Hit{}, err
		}
		for p, f := range m.Files {
			h := Hit{Source: n, Path: p, Title: f.Title, Kind: f.Kind, Tokens: f.Tokens}
			bare := strings.TrimSuffix(p, ".md")
			dir := strings.TrimSuffix(p, "/INDEX.md")
			switch {
			case ref == "" && p == "INDEX.md",
				p == ref, bare == ref, f.Index && dir == ref:
				tiers[0] = append(tiers[0], h)
			case f.Kind == "overview":
				continue // shares its folder's title; the folder INDEX wins
			case f.Title == ref:
				tiers[1] = append(tiers[1], h)
			case strings.EqualFold(f.Title, ref):
				tiers[2] = append(tiers[2], h)
			case ref != "" && (strings.HasSuffix(bare, "/"+ref) || strings.EqualFold(path.Base(bare), ref)):
				tiers[3] = append(tiers[3], h)
			}
		}
	}
	for _, t := range tiers {
		if len(t) == 1 {
			return t[0], nil
		}
		if len(t) > 1 {
			sort.Slice(t, func(i, j int) bool { return t[i].Source+t[i].Path < t[j].Source+t[j].Path })
			return Hit{}, &AmbiguousError{Hits: t}
		}
	}
	return Hit{}, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

// ---- search ----

// Match is a search result.
type Match struct {
	Source  string `json:"source"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Tokens  int    `json:"tokens"`
	By      string `json:"match"` // "name" or "content"
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	score   int
}

// Search finds files whose name or title matches, then files whose text
// contains the query (case-insensitive). Name matches rank first.
func (s *Store) Search(query, source string, limit int) ([]Match, error) {
	names, err := s.Sources()
	if err != nil {
		return nil, err
	}
	if source != "" {
		if !s.Has(source) {
			return nil, fmt.Errorf("no source named %q", source)
		}
		names = []string{source}
	}
	return s.SearchIn(query, names, limit)
}

// SearchIn is Search over the given sources only.
func (s *Store) SearchIn(query string, names []string, limit int) ([]Match, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil, errors.New("empty query")
	}
	sq, words := squash(q), strings.Fields(q)
	var out []Match
	for _, n := range names {
		m, err := s.Manifest(n)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(m.Files))
		for p := range m.Files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			f := m.Files[p]
			if f.Index {
				continue
			}
			base := Match{Source: n, Path: p, Title: f.Title, Kind: f.Kind, Tokens: f.Tokens}
			t := strings.ToLower(f.Title)
			st := squash(t)
			switch {
			case t == q || st == sq:
				base.By, base.score = "name", 0
			case strings.HasPrefix(st, sq):
				base.By, base.score = "name", 1
			case strings.Contains(st, sq) || strings.Contains(squash(strings.ToLower(p)), sq):
				base.By, base.score = "name", 2
			}
			if base.By != "" {
				out = append(out, base)
				continue
			}
			data, err := s.ReadFile(n, p)
			if err != nil {
				continue
			}
			_, body, _ := SplitFile(data)
			if c, ok := contentMatch(base, body, q, words); ok {
				// Count lines from the top of the file, header included, so
				// the number matches grep -n and sed -n on the stored file.
				if head := strings.Index(string(data), body); head >= 0 {
					c.Line += strings.Count(string(data[:head]), "\n")
				}
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score < out[j].score })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// squash drops spaces, underscores and dashes, so "reply markup" matches
// editMessageReplyMarkup and reply_markup.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '_' || r == '-' {
			return -1
		}
		return r
	}, s)
}

// contentMatch finds the query in a body: the phrase (or its squashed form)
// on one line ranks 3, all the words anywhere in the file rank 4.
func contentMatch(base Match, body, q string, words []string) (Match, bool) {
	lower := strings.ToLower(body)
	lines := strings.Split(body, "\n")
	at := func(score int, find func(l string) (int, int)) (Match, bool) {
		for i, line := range lines {
			if j, n := find(strings.ToLower(line)); j >= 0 {
				c := base
				c.By, c.score, c.Line, c.Snippet = "content", score, i+1, snippet(line, j, n)
				return c, true
			}
		}
		return Match{}, false
	}
	if strings.Contains(lower, q) {
		return at(3, func(l string) (int, int) { return strings.Index(l, q), len(q) })
	}
	if len(words) > 1 {
		for _, w := range words {
			if !strings.Contains(lower, w) {
				return Match{}, false
			}
		}
		if m, ok := at(3, func(l string) (int, int) {
			// reply_markup for "reply markup"
			for _, sep := range []string{"_", "-", ""} {
				if k := strings.Index(l, strings.Join(words, sep)); k >= 0 {
					return k, len(strings.Join(words, sep))
				}
			}
			return -1, 0
		}); ok {
			return m, true
		}
		return at(4, func(l string) (int, int) { return strings.Index(l, words[0]), len(words[0]) })
	}
	return Match{}, false
}

func snippet(line string, at, n int) string {
	line = mdLink.ReplaceAllString(line, "$1")
	lower := strings.ToLower(line)
	if i := strings.Index(lower, strings.ToLower(line[min(at, len(line)):min(at+n, len(line))])); i >= 0 {
		at = i
	}
	// Wide enough to reach the value after a name ("timeout … 1 second").
	start := max(0, at-80)
	end := min(len(line), at+n+160)
	for start > 0 && !utf8Start(line[start]) {
		start--
	}
	for end < len(line) && !utf8Start(line[end]) {
		end++
	}
	s := strings.TrimSpace(line[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(line) {
		s += "…"
	}
	return s
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// ---- diff ----

// FileDiff is the change of one file since a point in time.
type FileDiff struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Status string `json:"status"` // added, changed, removed
	Diff   string `json:"diff"`
}

// DiffResult is what changed in a source since a version or date.
type DiffResult struct {
	Source   string     `json:"source"`
	Since    int        `json:"since_version"`
	Current  int        `json:"current_version"`
	Versions []int      `json:"versions"`
	Files    []FileDiff `json:"files"`
}

// ResolveSince turns "3", "v3" or a date (YYYY-MM-DD) into the version the
// diff starts from: changes made after it are shown.
func ResolveSince(since string, versions []*Version) (int, error) {
	s := strings.TrimPrefix(strings.TrimSpace(since), "v")
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && !strings.Contains(s, "-") {
		return n, nil
	}
	t, err := time.Parse("2006-01-02", strings.TrimSpace(since))
	if err != nil {
		return 0, fmt.Errorf("--since takes a version (3 or v3) or a date (2026-11-01), got %q", since)
	}
	base := 0
	for _, v := range versions {
		if v.Time.Before(t) {
			base = v.Version
		}
	}
	return base, nil
}

// Diff compares each file as it was at version since with the current file.
func (s *Store) Diff(name, since string) (*DiffResult, error) {
	if !s.Has(name) {
		return nil, fmt.Errorf("no source named %q", name)
	}
	vs, err := s.Versions(name)
	if err != nil {
		return nil, err
	}
	m, err := s.Manifest(name)
	if err != nil {
		return nil, err
	}
	base, err := ResolveSince(since, vs)
	if err != nil {
		return nil, err
	}
	res := &DiffResult{Source: name, Since: base, Current: m.Version, Versions: []int{}, Files: []FileDiff{}}
	type state struct {
		existed bool
		old     OldFile
	}
	first := map[string]*state{} // the file as it was at base
	for _, v := range vs {
		if v.Version <= base {
			continue
		}
		res.Versions = append(res.Versions, v.Version)
		for _, p := range v.Added {
			if _, ok := first[p]; !ok {
				first[p] = &state{existed: false}
			}
		}
		for _, p := range append(append([]string{}, v.Changed...), v.Removed...) {
			if _, ok := first[p]; !ok {
				first[p] = &state{existed: true, old: v.Old[p]}
			}
		}
	}
	paths := make([]string, 0, len(first))
	for p := range first {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		st := first[p]
		cur, inNow := m.Files[p]
		var now string
		title := st.old.Title
		if inNow {
			data, err := s.ReadFile(name, p)
			if err != nil {
				return nil, err
			}
			_, now, _ = SplitFile(data)
			title = cur.Title
		}
		fd := FileDiff{Path: p, Title: title}
		switch {
		case !st.existed && !inNow:
			continue // added then removed in the range
		case !st.existed:
			fd.Status = "added"
		case !inNow:
			fd.Status = "removed"
		default:
			if st.old.Hash == cur.Hash {
				continue // changed and changed back
			}
			fd.Status = "changed"
		}
		oldText := ""
		if st.existed {
			oldText = withNL(st.old.Body)
		}
		newText := ""
		if inNow {
			newText = withNL(now)
		}
		fd.Diff = udiff.Unified("a/"+p, "b/"+p, oldText, newText)
		res.Files = append(res.Files, fd)
	}
	return res, nil
}

func withNL(s string) string {
	if s == "" || bytes.HasSuffix([]byte(s), []byte("\n")) {
		return s
	}
	return s + "\n"
}
