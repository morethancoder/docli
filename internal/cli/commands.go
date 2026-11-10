package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/engine"
	"github.com/morethancoder/docli/internal/outline"
	"github.com/morethancoder/docli/internal/plan"
	"github.com/morethancoder/docli/internal/store"
	"github.com/morethancoder/docli/internal/tui"
)

func newRoot(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:   "docli",
		Short: "Download docs into small indexed files that agents can read cheaply",
		Long: `docli downloads a docs site into .docli/ as small markdown files with YAML
headers, one per item, in folders with an INDEX.md map at every level. It
syncs only what changed, keeps versions, and checks for updates.

Agents: pass --json for a fixed output format (see docs/json.md), read
.docli/<source>/INDEX.md first, then open one file.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.setup(cmd)
		},
	}
	pf := root.PersistentFlags()
	pf.BoolVar(&a.jsonOut, "json", false, "print the result as JSON (fixed format, no colour, no prompts)")
	pf.StringVar(&a.path, "path", "", "docs folder to use (default: ./.docli)")
	pf.BoolVar(&a.latest, "latest", false, "check for updates first, sync if stale, then answer")
	pf.BoolVar(&a.noCheck, "no-check", false, "skip the update check")
	root.AddCommand(
		cmdAdd(a), cmdInspect(a), cmdSync(a), cmdList(a), cmdTree(a), cmdShow(a),
		cmdSearch(a), cmdDiff(a), cmdChangelog(a), cmdBrowse(a), cmdCheck(a), cmdRemove(a), cmdSkill(a),
	)
	return root
}

// ---- shared helpers ----

// target picks the sources a read command works on: the named one, or all.
func (a *app) target(name string) ([]string, error) {
	if name != "" {
		if !a.store.Has(name) {
			return nil, errf("not_found", "no source named %q (see `docli list`)", name)
		}
		return []string{name}, nil
	}
	names, err := a.store.Sources()
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errf("empty", "no docs in %s yet; add some with `docli add <url>`", a.store.Root)
	}
	return names, nil
}

// one picks a single source: the named one, or the only one there is.
func (a *app) one(name string) (string, error) {
	names, err := a.target(name)
	if err != nil {
		return "", err
	}
	if len(names) > 1 {
		return "", errf("usage", "more than one source (%s); name one", strings.Join(names, ", "))
	}
	return names[0], nil
}

// read prepares a read command: --latest first, then the background check.
func (a *app) read(names []string) error {
	if a.latest && !a.noCheck {
		if err := a.latestFor(names); err != nil {
			return err
		}
	}
	a.beginChecks(names)
	return nil
}

func (a *app) rel(p string) string {
	wd, _ := os.Getwd()
	if r, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func termWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 20 {
		return min(w, 120)
	}
	return 100
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

// ---- add / inspect ----

type ruleFlags struct {
	name, selector, render, format string
	preset, base                   string
	drop                           []string
	fileLevel, maxTokens, collapse int
}

func (r *ruleFlags) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&r.name, "name", "", "source name (folder under .docli)")
	f.StringVar(&r.preset, "preset", "", "use a built-in preset's rules ("+strings.Join(config.PresetNames(), ", ")+")")
	f.StringVar(&r.selector, "selector", "", "CSS selector of the content region (HTML)")
	f.StringSliceVar(&r.drop, "drop", nil, "CSS selectors to remove before parsing (repeatable)")
	f.StringVar(&r.render, "render", "", "auto, static or browser (browser runs JavaScript)")
	f.StringVar(&r.format, "format", "", "auto, html or markdown")
	f.StringVar(&r.base, "base", "", "real address of a saved page, so its links resolve to the site")
	f.IntVar(&r.fileLevel, "file-level", 0, "heading level that becomes one file (0 = by size)")
	f.IntVar(&r.maxTokens, "max-tokens", 0, "split a section further when it is over this many tokens")
	f.IntVar(&r.collapse, "collapse-tokens", 0, "keep a section with sub-headings as one file under this size")
}

// resolve builds a source's rules: the saved source.yaml when there is one,
// else a known preset, else defaults; then the flags that were given.
func (a *app) resolve(cmd *cobra.Command, arg string, r *ruleFlags) (*config.Source, bool, error) {
	var src *config.Source
	saved := false
	if a.store.Has(arg) && !strings.Contains(arg, "/") {
		s, err := a.store.Config(arg)
		if err != nil {
			return nil, false, err
		}
		src, saved = s, true
	} else {
		url := arg
		if !strings.Contains(url, "://") {
			if abs, err := filepath.Abs(url); err == nil {
				if _, err := os.Stat(abs); err == nil {
					url = "file://" + abs
				}
			}
		}
		var preset *config.Source
		if r.preset != "" {
			p, ok := config.Preset(r.preset)
			if !ok {
				return nil, false, errf("usage", "no preset %q (have: %s)", r.preset, strings.Join(config.PresetNames(), ", "))
			}
			preset = p
		} else if p, ok := config.ForURL(url); ok {
			preset = p
		}
		name := r.name
		if name == "" {
			if preset != nil {
				name = preset.Name
			} else {
				name = config.NameFromURL(url)
			}
		}
		if a.store.Has(name) {
			s, err := a.store.Config(name)
			if err != nil {
				return nil, false, err
			}
			src, saved = s, true
			src.URL = url
		} else if preset != nil {
			src = preset
			src.URL = url
		} else {
			src = &config.Source{URL: url}
		}
		src.Name = name
	}
	f := cmd.Flags()
	if f.Changed("selector") {
		src.Selector = r.selector
	}
	if f.Changed("drop") {
		src.Drop = r.drop
	}
	if f.Changed("render") {
		src.Render = r.render
	}
	if f.Changed("format") {
		src.Format = r.format
	}
	if f.Changed("base") {
		src.Base = r.base
	}
	if f.Changed("file-level") {
		src.FileLevel = r.fileLevel
	}
	if f.Changed("max-tokens") {
		src.MaxTokens = r.maxTokens
	}
	if f.Changed("collapse-tokens") {
		src.CollapseTokens = r.collapse
	}
	src.Defaults()
	if err := src.Validate(); err != nil {
		return nil, false, errf("usage", "%v", err)
	}
	return src, saved, nil
}

// AddData is the --json data of add and of each sync entry.
type AddData struct {
	*store.Result
	Dir      string        `json:"dir"`
	Parser   string        `json:"parser"`
	Rendered bool          `json:"rendered"`
	Note     string        `json:"note,omitempty"`
	Coverage plan.Coverage `json:"coverage"`
	Warnings []string      `json:"warnings"`
}

func cmdAdd(a *app) *cobra.Command {
	var r ruleFlags
	var force bool
	cmd := &cobra.Command{
		Use:   "add <url|file>",
		Short: "Download a docs source and split it into indexed files",
		Long: `Download a docs page (HTML, a JavaScript-built page, markdown or llms.txt),
split it by headings into folders and files, and write it under .docli/<name>/.
Rules are saved to .docli/<name>/source.yaml and reused by sync.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, _, err := a.resolve(cmd, args[0], &r)
			if err != nil {
				return err
			}
			res, l, err := engine.Sync(context.Background(), a.store, src, force)
			if err != nil {
				return err
			}
			d := addData(a, res, l)
			return a.emit(d, func(w io.Writer) {
				fmt.Fprintf(w, "%s %s v%d · %d files · %s → %s\n", styled(a, okS, "✓"), styled(a, boldS, src.Name),
					res.Version, res.Files, tok(res.Tokens), a.rel(d.Dir))
				printLoadInfo(a, w, d)
				fmt.Fprintf(w, "\nNext: read %s, or `docli show <name>`\n", a.rel(filepath.Join(d.Dir, "INDEX.md")))
			})
		},
	}
	r.bind(cmd)
	cmd.Flags().BoolVar(&force, "force", false, "write even if the coverage check fails")
	return cmd
}

func addData(a *app, res *store.Result, l *engine.Loaded) AddData {
	w := l.Plan.Warnings
	if w == nil {
		w = []string{}
	}
	return AddData{Result: res, Dir: a.store.Dir(res.Source), Parser: l.Result.Parser, Rendered: l.Result.Rendered,
		Note: l.Result.Note, Coverage: l.Plan.Coverage, Warnings: w}
}

func printLoadInfo(a *app, w io.Writer, d AddData) {
	how := d.Parser
	if d.Rendered {
		how += ", rendered in headless browser"
	}
	fmt.Fprintf(w, "  %s\n", styled(a, dimS, "parsed as "+how))
	if d.Note != "" {
		fmt.Fprintf(w, "  %s\n", styled(a, dimS, d.Note))
	}
	fmt.Fprintf(w, "  %s\n", coverageLine(a, d.Coverage))
	for _, warn := range d.Warnings {
		fmt.Fprintf(w, "  %s %s\n", styled(a, warnS, "warning:"), warn)
	}
}

// InspectData is the --json data of inspect.
type InspectData struct {
	Source   *config.Source `json:"source"`
	Saved    bool           `json:"saved"`
	Parser   string         `json:"parser"`
	Rendered bool           `json:"rendered"`
	Note     string         `json:"note,omitempty"`
	Title    string         `json:"title"`
	Headings map[string]int `json:"headings"` // count per level: "h2": 14
	Files    int            `json:"files"`
	Folders  int            `json:"folders"`
	Tokens   int            `json:"tokens"`
	Largest  *PlanItem      `json:"largest,omitempty"`
	Coverage plan.Coverage  `json:"coverage"`
	Warnings []string       `json:"warnings"`
	Tree     []*PlanNode    `json:"tree"`
}

// PlanItem is one planned file.
type PlanItem struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Tokens int    `json:"tokens"`
}

// PlanNode is a planned folder or file, for tree output.
type PlanNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Title    string      `json:"title"`
	Kind     string      `json:"kind"`
	Tokens   int         `json:"tokens"`
	Items    int         `json:"items,omitempty"`
	Children []*PlanNode `json:"children,omitempty"`
	More     int         `json:"more,omitempty"` // children not shown (depth limit)
}

func cmdInspect(a *app) *cobra.Command {
	var r ruleFlags
	var save bool
	var depth int
	cmd := &cobra.Command{
		Use:   "inspect <url|file|name>",
		Short: "Preview how a source would be split, without writing docs",
		Long: `Fetch and parse a source and show the planned folders and files, heading
counts, the largest file and the coverage check. Nothing is written unless
--save is given, which stores the rules in .docli/<name>/source.yaml so add
and sync reuse them.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, saved, err := a.resolve(cmd, args[0], &r)
			if err != nil {
				return err
			}
			l, err := engine.Prepare(context.Background(), src)
			if err != nil {
				return err
			}
			if save {
				if err := a.store.SaveConfig(src); err != nil {
					return err
				}
				saved = true
			}
			d := inspectData(src, saved, l, depth)
			return a.emit(d, func(w io.Writer) { printInspect(a, w, d) })
		},
	}
	r.bind(cmd)
	cmd.Flags().BoolVar(&save, "save", false, "save these rules for the source")
	cmd.Flags().IntVar(&depth, "depth", 2, "folder levels to show")
	return cmd
}

func inspectData(src *config.Source, saved bool, l *engine.Loaded, depth int) InspectData {
	d := InspectData{Source: src, Saved: saved, Parser: l.Result.Parser, Rendered: l.Result.Rendered,
		Note: l.Result.Note, Title: l.Result.Doc.Title, Headings: map[string]int{}, Coverage: l.Plan.Coverage,
		Warnings: l.Plan.Warnings}
	if d.Warnings == nil {
		d.Warnings = []string{}
	}
	outline.Walk(l.Result.Doc.Root, func(n *outline.Node) {
		if n.Level > 0 {
			d.Headings[fmt.Sprintf("h%d", n.Level)]++
		}
	})
	if l.Result.Doc.TitleInContent {
		d.Headings["h1"]++ // hoisted page title
	}
	var items []treeItem
	for _, f := range l.Plan.Files {
		if f.Index {
			d.Folders++
		} else {
			d.Files++
			d.Tokens += f.Tokens
		}
		items = append(items, treeItem{path: f.Path, title: f.Title, kind: f.Kind, tokens: f.Tokens, items: f.Items, total: f.Total, index: f.Index})
	}
	if lg := l.Plan.Largest; lg != nil {
		d.Largest = &PlanItem{Path: lg.Path, Title: lg.Title, Kind: lg.Kind, Tokens: lg.Tokens}
	}
	d.Tree = buildTree(items, "", depth)
	return d
}

func printInspect(a *app, w io.Writer, d InspectData) {
	fmt.Fprintf(w, "%s  %s\n", styled(a, titleS, d.Title), styled(a, dimS, d.Source.URL))
	how := d.Parser
	if d.Rendered {
		how += " (headless browser)"
	}
	levels := make([]string, 0, len(d.Headings))
	for l, n := range d.Headings {
		levels = append(levels, fmt.Sprintf("%s %d", l, n))
	}
	sort.Strings(levels)
	fmt.Fprintf(w, "parsed as %s · headings: %s\n", how, strings.Join(levels, ", "))
	if d.Note != "" {
		fmt.Fprintln(w, styled(a, dimS, d.Note))
	}
	fileLevel := "by size"
	if d.Source.FileLevel > 0 {
		fileLevel = fmt.Sprintf("h%d", d.Source.FileLevel)
	}
	fmt.Fprintf(w, "rules: name %s · selector %q · one file per %s · split over %d tok · keep whole under %d tok\n",
		d.Source.Name, d.Source.Selector, fileLevel, d.Source.MaxTokens, d.Source.CollapseTokens)
	fmt.Fprintf(w, "plan: %d files in %d folders · %s", d.Files, d.Folders, tok(d.Tokens))
	if d.Largest != nil {
		fmt.Fprintf(w, " · largest %s (%s)", d.Largest.Path, tok(d.Largest.Tokens))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, coverageLine(a, d.Coverage))
	for _, warn := range d.Warnings {
		fmt.Fprintf(w, "%s %s\n", styled(a, warnS, "warning:"), warn)
	}
	fmt.Fprintln(w)
	printTree(a, w, d.Source.Name+"/", d.Tree)
	if d.Saved {
		fmt.Fprintf(w, "\nrules saved to %s\n", a.rel(filepath.Join(a.store.Dir(d.Source.Name), "source.yaml")))
	} else {
		fmt.Fprintln(w, styled(a, dimS, "\nnothing written; --save keeps these rules, `docli add` writes the docs"))
	}
}

func coverageLine(a *app, c plan.Coverage) string {
	if c.OK {
		return styled(a, okS, fmt.Sprintf("coverage ok: all %d source words are in exactly one file", c.SourceWords))
	}
	return styled(a, warnS, fmt.Sprintf("coverage FAILED: %d missing, %d duplicated of %d words (%s)",
		c.Missing, c.Duplicated, c.SourceWords, strings.Join(c.Examples, ", ")))
}

// ---- sync ----

func cmdSync(a *app) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "sync [name...]",
		Short: "Re-fetch sources and rewrite only what changed",
		RunE: func(cmd *cobra.Command, args []string) error {
			names := args
			if len(names) == 0 {
				all, err := a.target("")
				if err != nil {
					return err
				}
				names = all
			}
			var out []AddData
			for _, n := range names {
				if !a.store.Has(n) {
					return errf("not_found", "no source named %q", n)
				}
				src, err := a.store.Config(n)
				if err != nil {
					return err
				}
				res, l, err := engine.Sync(context.Background(), a.store, src, force)
				if err != nil {
					return fmt.Errorf("%s: %w", n, err)
				}
				out = append(out, addData(a, res, l))
			}
			return a.emit(out, func(w io.Writer) {
				for _, d := range out {
					if !d.Changed {
						fmt.Fprintf(w, "%s %s up to date (v%d, %d files)\n", styled(a, okS, "✓"), styled(a, boldS, d.Source), d.Version, d.Files)
						continue
					}
					fmt.Fprintf(w, "%s %s v%d · %s added, %s changed, %s removed · %d files written\n",
						styled(a, okS, "✓"), styled(a, boldS, d.Source), d.Version,
						styled(a, addS, fmt.Sprint(len(d.Added))), styled(a, warnS, fmt.Sprint(len(d.Modified))),
						styled(a, delS, fmt.Sprint(len(d.Removed))), d.Written)
					for _, p := range limitList(d.Added, 10) {
						fmt.Fprintf(w, "  %s %s\n", styled(a, addS, "+"), p)
					}
					for _, p := range limitList(d.Modified, 10) {
						fmt.Fprintf(w, "  %s %s\n", styled(a, warnS, "~"), p)
					}
					for _, p := range limitList(d.Removed, 10) {
						fmt.Fprintf(w, "  %s %s\n", styled(a, delS, "-"), p)
					}
					if d.Version > 1 {
						fmt.Fprintf(w, "  see the changes: docli diff %s --since %d\n", d.Source, d.Version-1)
					}
				}
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "write even if the coverage check fails")
	return cmd
}

func limitList(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string{}, s[:n]...), fmt.Sprintf("… and %d more", len(s)-n))
}

// ---- list ----

// SourceInfo is one row of list.
type SourceInfo struct {
	Name     string    `json:"name"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Dir      string    `json:"dir"`
	Version  int       `json:"version"`
	Files    int       `json:"files"`
	Tokens   int       `json:"tokens"`
	SyncedAt time.Time `json:"synced_at"`
}

func cmdList(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the docs sources in this folder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := a.store.Sources()
			if err != nil {
				return err
			}
			if err := a.read(names); err != nil {
				return err
			}
			out := []SourceInfo{}
			for _, n := range names {
				m, err := a.store.Manifest(n)
				if err != nil {
					return err
				}
				files, toks := m.Counts()
				out = append(out, SourceInfo{Name: n, Title: m.Title, URL: m.URL, Dir: a.store.Dir(n),
					Version: m.Version, Files: files, Tokens: toks, SyncedAt: m.SyncedAt})
			}
			return a.emit(out, func(w io.Writer) {
				if len(out) == 0 {
					fmt.Fprintf(w, "No docs in %s yet. Add some with: docli add <url>\n", a.rel(a.store.Root))
					return
				}
				for _, s := range out {
					fmt.Fprintf(w, "%s  %s\n  %s\n", styled(a, titleS, s.Name), s.Title, styled(a, dimS,
						fmt.Sprintf("v%d · %d files · %s · synced %s · %s", s.Version, s.Files, tok(s.Tokens), ago(s.SyncedAt), s.URL)))
				}
			})
		},
	}
}

// ---- tree ----

type treeItem struct {
	path, title, kind    string
	tokens, items, total int
	index                bool
}

// buildTree turns flat file paths into nested nodes under prefix, cut at
// depth folder levels.
func buildTree(items []treeItem, prefix string, depth int) []*PlanNode {
	byDir := map[string][]treeItem{}
	idx := map[string]treeItem{}
	for _, it := range items {
		if !strings.HasPrefix(it.path, prefix) {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(it.path))
		if dir == "." {
			dir = ""
		}
		if it.index {
			idx[dir] = it
			if dir == strings.TrimSuffix(prefix, "/") {
				continue
			}
			parent := filepath.ToSlash(filepath.Dir(dir))
			if parent == "." {
				parent = ""
			}
			byDir[parent] = append(byDir[parent], it)
			continue
		}
		byDir[dir] = append(byDir[dir], it)
	}
	var walk func(dir string, d int) []*PlanNode
	walk = func(dir string, d int) []*PlanNode {
		var out []*PlanNode
		for _, it := range byDir[dir] {
			if it.index {
				fdir := strings.TrimSuffix(it.path, "/INDEX.md")
				n := &PlanNode{Name: filepath.Base(fdir) + "/", Path: it.path, Title: it.title, Kind: "folder", Tokens: it.total, Items: it.items}
				if d < depth {
					n.Children = walk(fdir, d+1)
				} else {
					n.More = it.items
				}
				out = append(out, n)
				continue
			}
			out = append(out, &PlanNode{Name: filepath.Base(it.path), Path: it.path, Title: it.title, Kind: it.kind, Tokens: it.tokens})
		}
		sort.SliceStable(out, func(i, j int) bool {
			fi, fj := out[i].Kind == "folder", out[j].Kind == "folder"
			if out[i].Kind == "overview" || out[j].Kind == "overview" {
				return out[i].Kind == "overview" && out[j].Kind != "overview"
			}
			if fi != fj {
				return fi
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		return out
	}
	return walk(strings.TrimSuffix(prefix, "/"), 1)
}

func cmdTree(a *app) *cobra.Command {
	var depth int
	cmd := &cobra.Command{
		Use:   "tree [source] [folder]",
		Short: "Show the folders and files of a source",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			n, err := a.one(name)
			if err != nil {
				return err
			}
			if err := a.read([]string{n}); err != nil {
				return err
			}
			m, err := a.store.Manifest(n)
			if err != nil {
				return err
			}
			prefix := ""
			if len(args) > 1 {
				prefix = strings.Trim(args[1], "/") + "/"
				if _, ok := m.Files[prefix+"INDEX.md"]; !ok {
					return errf("not_found", "no folder %q in %s", strings.TrimSuffix(prefix, "/"), n)
				}
			}
			var items []treeItem
			for p, f := range m.Files {
				it := treeItem{path: p, title: f.Title, kind: f.Kind, tokens: f.Tokens, index: f.Index}
				items = append(items, it)
			}
			fillFolderTotals(items, m)
			nodes := buildTree(items, prefix, depth)
			return a.emit(nodes, func(w io.Writer) { printTree(a, w, n+"/"+prefix, nodes) })
		},
	}
	cmd.Flags().IntVar(&depth, "depth", 2, "folder levels to show")
	return cmd
}

// fillFolderTotals counts items and tokens of each folder from the manifest.
func fillFolderTotals(items []treeItem, m *store.Manifest) {
	for i := range items {
		if !items[i].index {
			continue
		}
		dir := strings.TrimSuffix(items[i].path, "INDEX.md")
		for p, f := range m.Files {
			if f.Parent == items[i].path {
				items[i].items++
			}
			if !f.Index && strings.HasPrefix(p, dir) {
				items[i].total += f.Tokens
			}
		}
	}
}

func printTree(a *app, w io.Writer, root string, nodes []*PlanNode) {
	fmt.Fprintln(w, styled(a, titleS, root))
	var walk func(ns []*PlanNode, pre string)
	walk = func(ns []*PlanNode, pre string) {
		for i, n := range ns {
			branch, next := "├── ", "│   "
			if i == len(ns)-1 {
				branch, next = "└── ", "    "
			}
			label := n.Name
			meta := ""
			if n.Kind == "folder" {
				label = styled(a, boldS, n.Name)
				meta = fmt.Sprintf("%d items · %s", n.Items, tok(n.Tokens))
			} else {
				meta = fmt.Sprintf("%s · %s", n.Kind, tok(n.Tokens))
			}
			fmt.Fprintf(w, "%s%s%s  %s\n", styled(a, dimS, pre), styled(a, dimS, branch), label, styled(a, dimS, meta))
			walk(n.Children, pre+next)
		}
	}
	walk(nodes, "")
}

// ---- show ----

// ShowData is the --json data of show.
type ShowData struct {
	Source    string `json:"source"`
	Path      string `json:"path"`
	File      string `json:"file"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	Tokens    int    `json:"tokens"`
	Bytes     int    `json:"bytes"`
	Version   int    `json:"version,omitempty"`
	SourceURL string `json:"source_url"`
	Content   string `json:"content"`
}

func cmdShow(a *app) *cobra.Command {
	var source string
	var raw bool
	cmd := &cobra.Command{
		Use:   "show <name|path|folder>",
		Short: "Print one section (or a folder's INDEX)",
		Long: `Print one stored file. The reference can be a section title (sendMessage),
a path (available-methods/sendMessage), or a folder (available-methods), which
prints that folder's INDEX.md.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := a.target(source)
			if err != nil {
				return err
			}
			if a.latest && !a.noCheck {
				// Sync first, so a section that is new upstream can be found.
				if err := a.latestFor(names); err != nil {
					return err
				}
				a.latest = false
			}
			hit, err := a.store.Find(args[0], source)
			if err != nil {
				return err
			}
			// Check only the source the answer came from.
			if err := a.read([]string{hit.Source}); err != nil {
				return err
			}
			data, err := a.store.ReadFile(hit.Source, hit.Path)
			if err != nil {
				return err
			}
			h, body, _ := store.SplitFile(data)
			d := ShowData{Source: hit.Source, Path: hit.Path, File: filepath.Join(a.store.Dir(hit.Source), filepath.FromSlash(hit.Path)),
				Title: h.Title, Kind: h.Kind, Tokens: h.Tokens, Bytes: len(data), Version: h.Version, SourceURL: h.Source, Content: string(data)}
			return a.emit(d, func(w io.Writer) {
				if !a.tty || raw {
					w.Write(data)
					return
				}
				fmt.Fprintf(w, "%s  %s\n", styled(a, dimS, hit.Source+"/"+hit.Path),
					styled(a, dimS, fmt.Sprintf("%s · %s · %s", h.Kind, tok(h.Tokens), h.Source)))
				fmt.Fprint(w, renderMarkdown("# "+h.Title+"\n\n"+body, termWidth()))
			})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "look only in this source")
	cmd.Flags().BoolVar(&raw, "raw", false, "print the file as stored, even in a terminal")
	return cmd
}

func renderMarkdown(md string, width int) string {
	initStyles()
	style := "dark"
	if !isDark {
		style = "light"
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err != nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	return out
}

// ---- search ----

func cmdSearch(a *app) *cobra.Command {
	var source string
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find sections by name, then by content",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names, err := a.target(source)
			if err != nil {
				return err
			}
			if err := a.read(names); err != nil {
				return err
			}
			q := strings.Join(args, " ")
			ms, err := a.store.Search(q, source, limit)
			if err != nil {
				return err
			}
			if ms == nil {
				ms = []store.Match{}
			}
			return a.emit(ms, func(w io.Writer) {
				if len(ms) == 0 {
					fmt.Fprintf(w, "no matches for %q\n", q)
					return
				}
				for _, m := range ms {
					fmt.Fprintf(w, "%s  %s  %s\n", styled(a, boldS, m.Title), styled(a, kindS, m.Kind),
						styled(a, dimS, fmt.Sprintf("%s/%s · %s", m.Source, m.Path, tok(m.Tokens))))
					if m.Snippet != "" {
						fmt.Fprintf(w, "    %s\n", styled(a, dimS, fmt.Sprintf("line %d: ", m.Line))+m.Snippet)
					}
				}
			})
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "search only this source")
	cmd.Flags().IntVar(&limit, "limit", 20, "most results to return (0 = all)")
	return cmd
}

// ---- diff ----

func cmdDiff(a *app) *cobra.Command {
	var since string
	var stat bool
	cmd := &cobra.Command{
		Use:   "diff [source]",
		Short: "Show what changed since a version or a date (no git needed)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			n, err := a.one(name)
			if err != nil {
				return err
			}
			if err := a.read([]string{n}); err != nil {
				return err
			}
			if since == "" {
				m, err := a.store.Manifest(n)
				if err != nil {
					return err
				}
				since = fmt.Sprint(max(m.Version-1, 0))
			}
			d, err := a.store.Diff(n, since)
			if err != nil {
				return errf("usage", "%v", err)
			}
			if stat {
				for i := range d.Files {
					d.Files[i].Diff = ""
				}
			}
			return a.emit(d, func(w io.Writer) { printDiff(a, w, d, stat) })
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "version (3, v3) or date (2026-11-01); default: the previous version")
	cmd.Flags().BoolVar(&stat, "stat", false, "list changed files only")
	return cmd
}

func printDiff(a *app, w io.Writer, d *store.DiffResult, stat bool) {
	if len(d.Files) == 0 {
		fmt.Fprintf(w, "%s: no changes since v%d (now v%d)\n", d.Source, d.Since, d.Current)
		return
	}
	fmt.Fprintf(w, "%s: %d files changed since v%d (now v%d)\n\n", styled(a, titleS, d.Source), len(d.Files), d.Since, d.Current)
	for _, f := range d.Files {
		mark := map[string]string{"added": "+", "removed": "-", "changed": "~"}[f.Status]
		fmt.Fprintf(w, "%s %s  %s\n", mark, styled(a, boldS, f.Path), styled(a, dimS, f.Status))
		if stat {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(f.Diff, "\n"), "\n") {
			switch {
			case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
				fmt.Fprintln(w, styled(a, dimS, line))
			case strings.HasPrefix(line, "@@"):
				fmt.Fprintln(w, styled(a, hunkS, line))
			case strings.HasPrefix(line, "+"):
				fmt.Fprintln(w, styled(a, addS, line))
			case strings.HasPrefix(line, "-"):
				fmt.Fprintln(w, styled(a, delS, line))
			default:
				fmt.Fprintln(w, line)
			}
		}
		fmt.Fprintln(w)
	}
}

// ---- changelog ----

func cmdChangelog(a *app) *cobra.Command {
	var since, until string
	cmd := &cobra.Command{
		Use:   "changelog [source]",
		Short: "Show the source's dated changelog (e.g. Telegram's Recent changes)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			n, err := a.one(name)
			if err != nil {
				return err
			}
			if err := a.read([]string{n}); err != nil {
				return err
			}
			for _, d := range []string{since, until} {
				if d != "" {
					if _, err := time.Parse("2006-01-02", d); err != nil {
						return errf("usage", "dates are YYYY-MM-DD, got %q", d)
					}
				}
			}
			all, err := a.store.Changelog(n)
			if err != nil {
				return err
			}
			out := []store.Change{}
			for _, c := range all {
				if (since == "" || c.Date >= since) && (until == "" || c.Date <= until) {
					out = append(out, c)
				}
			}
			return a.emit(out, func(w io.Writer) {
				if len(out) == 0 {
					src, _ := a.store.Config(n)
					if src != nil && src.Changelog == "" {
						fmt.Fprintf(w, "%s has no changelog section set (changelog: in source.yaml)\n", n)
						return
					}
					fmt.Fprintln(w, "no changelog entries in that range")
					return
				}
				for _, c := range out {
					fmt.Fprintf(w, "%s  %s\n", styled(a, titleS, c.Date), styled(a, dimS, c.Path))
					for _, it := range c.Items {
						fmt.Fprintf(w, "  • %s\n", it)
					}
					fmt.Fprintln(w)
				}
			})
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "entries on or after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&until, "until", "", "entries on or before this date (YYYY-MM-DD)")
	return cmd
}

// ---- check ----

func cmdCheck(a *app) *cobra.Command {
	var background bool
	cmd := &cobra.Command{
		Use:   "check [name...]",
		Short: "Check now whether sources changed upstream (does not sync)",
		RunE: func(cmd *cobra.Command, args []string) error {
			names := args
			if len(names) == 0 {
				all, err := a.target("")
				if err != nil {
					return err
				}
				names = all
			}
			notes := []StaleNote{}
			for _, n := range names {
				if !a.store.Has(n) {
					return errf("not_found", "no source named %q", n)
				}
				st, err := engine.Check(context.Background(), a.store, n)
				if st == nil {
					if background {
						continue
					}
					return err
				}
				note, _ := noteFor(n, st)
				notes = append(notes, note)
			}
			if background {
				return nil // result is saved; the next command reports it
			}
			return a.emit(notes, func(w io.Writer) {
				for _, n := range notes {
					mark := styled(a, okS, "✓")
					if n.Status != "fresh" {
						mark = styled(a, warnS, "⚠")
					}
					fmt.Fprintf(w, "%s %s: %s\n", mark, styled(a, boldS, n.Source), n.Message)
				}
			})
		},
	}
	cmd.Flags().BoolVar(&background, "background", false, "save the result only (used by the automatic check)")
	_ = cmd.Flags().MarkHidden("background")
	return cmd
}

// ---- remove ----

func cmdRemove(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a source's docs and record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.store.Remove(args[0]); err != nil {
				return errf("not_found", "%v", err)
			}
			return a.emit(map[string]string{"removed": args[0]}, func(w io.Writer) {
				fmt.Fprintf(w, "removed %s\n", args[0])
			})
		},
	}
}

// ---- browse ----

func cmdBrowse(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "browse [source]",
		Short: "Browse docs in an interactive terminal UI",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.jsonOut {
				return errf("usage", "browse is interactive; use tree, show, search or diff with --json")
			}
			if !isTerminal(os.Stdout) || !isTerminal(os.Stdin) {
				return errf("usage", "browse needs a terminal; use tree, show or search instead")
			}
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			names, err := a.target(name)
			if err != nil {
				return err
			}
			if err := a.read(names); err != nil {
				return err
			}
			start := ""
			if len(names) == 1 {
				start = names[0]
			}
			if err := tui.Run(a.store, start); err != nil {
				return err
			}
			a.printNotes(a.finishChecks())
			return nil
		},
	}
}
