package tui

import (
	"context"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/engine"
	"github.com/morethancoder/docli/internal/store"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;:?]*[a-zA-Z]`)

func screen(m *model) string { return ansi.ReplaceAllString(m.View().Content, "") }

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func send(m *model, msgs ...tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	for _, msg := range msgs {
		_, cmd = m.Update(msg)
	}
	return cmd
}

// pick selects the visible item with this title (a folder when folder is set).
func pick(t *testing.T, m *model, title string, folder bool) {
	t.Helper()
	for i, it := range m.list.Items() {
		if it.(item).title == title && it.(item).folder == folder {
			m.list.Select(i)
			return
		}
	}
	var got []string
	for _, it := range m.list.Items() {
		got = append(got, it.(item).title)
	}
	t.Fatalf("no item %q in %v", title, got)
}

func TestBrowse(t *testing.T) {
	st := store.Open(filepath.Join(t.TempDir(), store.DirName))
	p, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "llms.md"))
	src := &config.Source{Name: "sdk", URL: "file://" + p, FileLevel: 3}
	src.Defaults()
	if err := st.SaveConfig(src); err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.Sync(context.Background(), st, src, false); err != nil {
		t.Fatal(err)
	}

	m, err := newModel([]Source{{Name: "sdk", Store: st}}, "")
	if err != nil {
		t.Fatal(err)
	}
	send(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	if s := screen(m); !strings.Contains(s, "sdk") {
		t.Fatalf("sources screen:\n%s", s)
	}

	send(m, key("enter")) // into the source
	pick(t, m, "API", true)
	send(m, key("enter")) // into the folder
	pick(t, m, "createWidget", false)
	send(m, key("enter")) // open the file
	if m.mode != modeFile || !strings.Contains(screen(m), "Creates a widget") {
		t.Fatalf("file view (mode %d):\n%s", m.mode, screen(m))
	}
	send(m, tea.WindowSizeMsg{Width: 60, Height: 20}) // re-renders at the new width
	if !strings.Contains(screen(m), "Creates a widget") {
		t.Error("file lost after resize")
	}

	send(m, key("backspace"))
	if m.mode != modeList || len(m.stack) != 3 {
		t.Fatalf("back from file: mode %d depth %d", m.mode, len(m.stack))
	}
	send(m, key("backspace"), key("backspace"))
	if len(m.stack) != 1 {
		t.Fatalf("back to top: depth %d", len(m.stack))
	}

	// Search across the source.
	send(m, key("s"))
	if m.mode != modeSearch {
		t.Fatal("s did not open search")
	}
	for _, r := range "destroy" {
		send(m, key(string(r)))
	}
	send(m, key("enter"))
	if m.mode != modeList || len(m.list.Items()) == 0 || m.list.Items()[0].(item).title != "destroyWidget" {
		t.Fatalf("search results: %v", m.list.Items())
	}

	// "/" filters the current list; typed q goes into the filter, not quit.
	send(m, key("backspace"))
	send(m, key("/"), key("q"))
	if m.list.FilterState() != list.Filtering {
		t.Error("filter not active")
	}
	send(m, key("esc"))

	if cmd := send(m, key("q")); cmd == nil {
		t.Error("q did not quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q returned a command that is not quit")
	}
}

// TestSearchAcrossStores: with a project and a global store, search covers
// each store's own sources only (so a shadowed global copy is skipped), and
// lists project hits first.
func TestSearchAcrossStores(t *testing.T) {
	p, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "llms.md"))
	open := func(names ...string) *store.Store {
		st := store.Open(filepath.Join(t.TempDir(), store.DirName))
		for _, n := range names {
			src := &config.Source{Name: n, URL: "file://" + p, FileLevel: 3}
			src.Defaults()
			if err := st.SaveConfig(src); err != nil {
				t.Fatal(err)
			}
			if _, _, err := engine.Sync(context.Background(), st, src, false); err != nil {
				t.Fatal(err)
			}
		}
		return st
	}
	proj, glob := open("sdk"), open("sdk", "gsdk") // the global sdk is shadowed
	m, err := newModel([]Source{{Name: "sdk", Store: proj}, {Name: "gsdk", Global: true, Store: glob}}, "")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := m.search("createWidget", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, h.Source)
	}
	// Want sdk… then gsdk…, the same number of each (one copy of llms.md per
	// searched source; the shadowed global sdk adds none).
	i := 0
	for i < len(got) && got[i] == "sdk" {
		i++
	}
	if i == 0 || 2*i != len(got) || slices.ContainsFunc(got[i:], func(s string) bool { return s != "gsdk" }) {
		t.Errorf("want project hits, then the same hits from gsdk only: %v", got)
	}
	if hits, _ := m.search("createWidget", "", 1); len(hits) != 1 || hits[0].Source != "sdk" {
		t.Errorf("limit 1: %v", hits)
	}
}
