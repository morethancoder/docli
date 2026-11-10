// Package tui is docli's interactive browser: sources, folders and files in
// a list, files rendered with Glamour, name filter, content search and diffs.
package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/morethancoder/docli/internal/store"
)

// Run starts the browser. start is a source name, or "" for all sources.
func Run(st *store.Store, start string) error {
	m, err := newModel(st, start)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

// item is one row: a source, a folder, a file or a search hit.
type item struct {
	source, path, title, desc string
	folder, isSource          bool
}

func (i item) Title() string {
	if i.folder || i.isSource {
		return i.title + "/"
	}
	return i.title
}
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

// level is one screen of the list, kept on a stack for going back.
type level struct {
	title  string
	items  []list.Item
	cursor int
}

type mode int

const (
	modeList mode = iota
	modeFile
	modeSearch
)

type model struct {
	st      *store.Store
	sources []string
	stack   []level
	list    list.Model
	view    viewport.Model
	input   textinput.Model
	mode    mode
	header  string // shown above the viewport
	raw     string // markdown of the open file, re-rendered on resize
	isDiff  bool
	dark    bool
	w, h    int
	err     string
}

func newModel(st *store.Store, start string) (*model, error) {
	names, err := st.Sources()
	if err != nil {
		return nil, err
	}
	m := &model{st: st, sources: names, dark: true, w: 100, h: 30}
	d := list.NewDefaultDelegate()
	m.list = list.New(nil, d, m.w, m.h-2)
	m.list.DisableQuitKeybindings()
	m.list.SetStatusBarItemName("entry", "entries")
	m.view = viewport.New(viewport.WithWidth(m.w), viewport.WithHeight(m.h-3))
	m.input = textinput.New()
	m.input.Prompt = "search: "
	m.input.Placeholder = "words in titles or text"
	if start != "" {
		lv, err := m.folderLevel(start, "")
		if err != nil {
			return nil, err
		}
		m.push(lv)
	} else {
		m.push(m.sourcesLevel())
	}
	return m, nil
}

func (m *model) Init() tea.Cmd { return tea.RequestBackgroundColor }

// ---- levels ----

func (m *model) sourcesLevel() level {
	var items []list.Item
	for _, n := range m.sources {
		mf, err := m.st.Manifest(n)
		if err != nil {
			continue
		}
		files, toks := mf.Counts()
		items = append(items, item{source: n, title: n, isSource: true,
			desc: fmt.Sprintf("%s · v%d · %d files · %s", mf.Title, mf.Version, files, tok(toks))})
	}
	return level{title: "docli · sources", items: items}
}

// folderLevel lists one folder of a source. dir is "" for the top or
// "available-methods/" for a sub-folder.
func (m *model) folderLevel(source, dir string) (level, error) {
	mf, err := m.st.Manifest(source)
	if err != nil {
		return level{}, err
	}
	parent := dir + "INDEX.md"
	var folders, files []list.Item
	for p, f := range mf.Files {
		if f.Parent != parent || p == parent {
			continue
		}
		if f.Index {
			sub := strings.TrimSuffix(p, "INDEX.md")
			n, toks := 0, 0
			for q, g := range mf.Files {
				if g.Parent == p {
					n++
				}
				if !g.Index && strings.HasPrefix(q, sub) {
					toks += g.Tokens
				}
			}
			folders = append(folders, item{source: source, path: p, title: f.Title, folder: true,
				desc: fmt.Sprintf("%d items · %s · %s", n, tok(toks), f.Summary)})
			continue
		}
		files = append(files, item{source: source, path: p, title: f.Title,
			desc: fmt.Sprintf("%s · %s · %s", f.Kind, tok(f.Tokens), f.Summary)})
	}
	byTitle := func(s []list.Item) {
		sort.SliceStable(s, func(i, j int) bool {
			a, b := s[i].(item), s[j].(item)
			if strings.HasSuffix(a.path, "_overview.md") != strings.HasSuffix(b.path, "_overview.md") {
				return strings.HasSuffix(a.path, "_overview.md")
			}
			return strings.ToLower(a.title) < strings.ToLower(b.title)
		})
	}
	byTitle(folders)
	byTitle(files)
	title := source + "/" + dir
	if f, ok := mf.Files[parent]; ok && dir != "" {
		title += " · " + f.Title
	}
	return level{title: title, items: append(folders, files...)}, nil
}

func (m *model) push(lv level) {
	if n := len(m.stack); n > 0 {
		m.stack[n-1].cursor = m.list.Index()
	}
	m.stack = append(m.stack, lv)
	m.show(lv)
}

func (m *model) pop() bool {
	if len(m.stack) <= 1 {
		return false
	}
	m.stack = m.stack[:len(m.stack)-1]
	lv := m.stack[len(m.stack)-1]
	m.show(lv)
	m.list.Select(lv.cursor)
	return true
}

func (m *model) show(lv level) {
	m.list.ResetFilter()
	m.list.Title = lv.title
	m.list.SetItems(lv.items)
	m.list.Select(0)
	m.mode = modeList
}

// ---- update ----

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.list.Styles = list.DefaultStyles(m.dark)
		d := list.NewDefaultDelegate()
		d.Styles = list.NewDefaultItemStyles(m.dark)
		m.list.SetDelegate(d)
		if m.mode == modeFile {
			m.render()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.list.SetSize(m.w, m.h-1)
		m.view.SetWidth(m.w)
		m.view.SetHeight(m.h - 3)
		m.input.SetWidth(m.w - 10)
		if m.mode == modeFile {
			m.render()
		}
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeSearch:
			return m.updateSearch(msg)
		case modeFile:
			return m.updateFile(msg)
		}
		if m.list.FilterState() == list.Filtering {
			break // typing into the filter
		}
		m.err = ""
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "enter", "right", "l":
			return m, m.open()
		case "backspace", "left", "h", "esc":
			if m.list.FilterState() == list.FilterApplied {
				m.list.ResetFilter()
				return m, nil
			}
			if !m.pop() && msg.String() == "esc" {
				return m, tea.Quit
			}
			return m, nil
		case "s":
			m.mode = modeSearch
			m.input.SetValue("")
			return m, m.input.Focus()
		case "d":
			if src := m.currentSource(); src != "" {
				m.openDiff(src)
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.mode == modeFile {
		m.view, cmd = m.view.Update(msg)
	} else {
		m.list, cmd = m.list.Update(msg)
	}
	return m, cmd
}

func (m *model) updateFile(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "esc", "backspace", "left", "h":
		m.mode = modeList
		return m, nil
	}
	var cmd tea.Cmd
	m.view, cmd = m.view.Update(msg)
	return m, cmd
}

func (m *model) updateSearch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeList
		m.input.Blur()
		return m, nil
	case "enter":
		q := strings.TrimSpace(m.input.Value())
		m.input.Blur()
		m.mode = modeList
		if q == "" {
			return m, nil
		}
		hits, err := m.st.Search(q, m.currentSource(), 100)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		var items []list.Item
		for _, h := range hits {
			desc := fmt.Sprintf("%s · %s · %s/%s", h.Kind, tok(h.Tokens), h.Source, h.Path)
			if h.Snippet != "" {
				desc = h.Snippet
			}
			items = append(items, item{source: h.Source, path: h.Path, title: h.Title, desc: desc})
		}
		m.push(level{title: fmt.Sprintf("search %q · %d hits", q, len(items)), items: items})
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// currentSource is the source being browsed, or "" at the sources level.
func (m *model) currentSource() string {
	if it, ok := m.list.SelectedItem().(item); ok && !it.isSource {
		return it.source
	}
	for i := len(m.stack) - 1; i >= 0; i-- {
		for _, li := range m.stack[i].items {
			if it, ok := li.(item); ok && !it.isSource {
				return it.source
			}
		}
	}
	return ""
}

func (m *model) open() tea.Cmd {
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return nil
	}
	switch {
	case it.isSource:
		lv, err := m.folderLevel(it.source, "")
		if err != nil {
			m.err = err.Error()
			return nil
		}
		m.push(lv)
	case it.folder:
		lv, err := m.folderLevel(it.source, strings.TrimSuffix(it.path, "INDEX.md"))
		if err != nil {
			m.err = err.Error()
			return nil
		}
		m.push(lv)
	default:
		data, err := m.st.ReadFile(it.source, it.path)
		if err != nil {
			m.err = err.Error()
			return nil
		}
		h, body, _ := store.SplitFile(data)
		m.header = fmt.Sprintf("%s/%s · %s · %s", it.source, it.path, h.Kind, tok(h.Tokens))
		m.raw = "# " + h.Title + "\n\n" + body
		m.isDiff = false
		m.render()
		m.view.GotoTop()
		m.mode = modeFile
	}
	return nil
}

func (m *model) openDiff(src string) {
	mf, err := m.st.Manifest(src)
	if err != nil {
		m.err = err.Error()
		return
	}
	d, err := m.st.Diff(src, fmt.Sprint(max(mf.Version-1, 0)))
	if err != nil {
		m.err = err.Error()
		return
	}
	m.header = fmt.Sprintf("%s · diff v%d → v%d · %d files", src, d.Since, d.Current, len(d.Files))
	var b strings.Builder
	if len(d.Files) == 0 {
		b.WriteString("No changes in the last sync.\n")
	}
	add := lipgloss.NewStyle().Foreground(lipgloss.Color("#5FD787"))
	del := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
	hunk := lipgloss.NewStyle().Foreground(lipgloss.Color("#A28BFF"))
	bold := lipgloss.NewStyle().Bold(true)
	for _, f := range d.Files {
		b.WriteString(bold.Render(f.Status+"  "+f.Path) + "\n")
		for _, line := range strings.Split(strings.TrimRight(f.Diff, "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
				continue
			case strings.HasPrefix(line, "@@"):
				line = hunk.Render(line)
			case strings.HasPrefix(line, "+"):
				line = add.Render(line)
			case strings.HasPrefix(line, "-"):
				line = del.Render(line)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	m.raw = b.String()
	m.isDiff = true
	m.view.SetContent(m.raw)
	m.view.GotoTop()
	m.mode = modeFile
}

func (m *model) render() {
	if m.isDiff {
		m.view.SetContent(m.raw)
		return
	}
	style := "dark"
	if !m.dark {
		style = "light"
	}
	out := m.raw
	if r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(min(m.w-2, 110))); err == nil {
		if s, err := r.Render(m.raw); err == nil {
			out = s
		}
	}
	m.view.SetContent(out)
}

// ---- view ----

var (
	dim = lipgloss.NewStyle().Foreground(lipgloss.Color("#8A8A8A"))
	bad = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
)

func (m *model) View() tea.View {
	var s string
	switch m.mode {
	case modeFile:
		pct := fmt.Sprintf("%3.0f%%", m.view.ScrollPercent()*100)
		s = dim.Render(m.header) + "\n" + m.view.View() + "\n" +
			dim.Render(pct+" · ↑/↓ pgup/pgdn scroll · esc back · q quit")
	case modeSearch:
		s = m.list.View() + "\n" + m.input.View()
	default:
		foot := "enter open · esc back · / filter names · s search text · d last diff · q quit"
		if m.err != "" {
			foot = bad.Render(m.err)
		}
		s = m.list.View() + "\n" + dim.Render(foot)
	}
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

func tok(n int) string {
	switch {
	case n >= 10000:
		return fmt.Sprintf("%dk tok", (n+500)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk tok", float64(n)/1000)
	default:
		return fmt.Sprintf("%d tok", n)
	}
}
