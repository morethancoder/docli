// Package store writes planned files under .docli/<source>/ and keeps docli's
// own record next to them in .meta/: the manifest of current files, one
// version record per change, the stale-check state and the changelog.
//
// The content files stay plain markdown so the project's git tracks them
// normally; .meta/ lets diff and versions work without git.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"docli/internal/config"
	"docli/internal/plan"
)

// DirName is the default store folder, created where docli runs.
const DirName = ".docli"

// Store is a .docli folder.
type Store struct {
	Root string
}

// Open returns the store at root (it may not exist yet).
func Open(root string) *Store { return &Store{Root: root} }

// FileMeta is what the manifest keeps per file.
type FileMeta struct {
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Anchor  string `json:"anchor,omitempty"`
	Source  string `json:"source"`
	Parent  string `json:"parent,omitempty"`
	Hash    string `json:"hash"`
	Tokens  int    `json:"tokens"`
	Version int    `json:"version"` // version in which this file last changed
	Summary string `json:"summary,omitempty"`
	Index   bool   `json:"index,omitempty"`
}

// Manifest is the current state of one source.
type Manifest struct {
	Name        string               `json:"name"`
	URL         string               `json:"url"`
	Title       string               `json:"title"`
	Version     int                  `json:"version"`
	SyncedAt    time.Time            `json:"synced_at"`
	Parser      string               `json:"parser"`
	Rendered    bool                 `json:"rendered"`
	Fingerprint string               `json:"fingerprint"`
	Files       map[string]*FileMeta `json:"files"`
}

// Counts sums content files and tokens (INDEX files excluded).
func (m *Manifest) Counts() (files, tokens int) {
	for _, f := range m.Files {
		if !f.Index {
			files++
			tokens += f.Tokens
		}
	}
	return
}

// Version is the record of one sync that changed something.
type Version struct {
	Version int                `json:"version"`
	Time    time.Time          `json:"time"`
	Added   []string           `json:"added"`
	Changed []string           `json:"changed"`
	Removed []string           `json:"removed"`
	Old     map[string]OldFile `json:"old,omitempty"` // changed and removed files as they were
}

// OldFile is a file's previous hash and text.
type OldFile struct {
	Hash  string `json:"hash"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// State is the result of the last stale check.
type State struct {
	CheckedAt   time.Time `json:"checked_at"`
	Fingerprint string    `json:"fingerprint,omitempty"`
	Stale       bool      `json:"stale"`
	Error       string    `json:"error,omitempty"`
	Shown       bool      `json:"shown,omitempty"` // a background result was already reported
}

// Dir is a source's folder.
func (s *Store) Dir(name string) string { return filepath.Join(s.Root, name) }

func (s *Store) meta(name string, parts ...string) string {
	return filepath.Join(append([]string{s.Root, name, ".meta"}, parts...)...)
}

// Sources lists the names of added sources, sorted.
func (s *Store) Sources() ([]string, error) {
	ents, err := os.ReadDir(s.Root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			if _, err := os.Stat(filepath.Join(s.Root, e.Name(), "source.yaml")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// Has reports whether a source exists.
func (s *Store) Has(name string) bool {
	_, err := os.Stat(filepath.Join(s.Dir(name), "source.yaml"))
	return err == nil
}

// Config loads a source's saved rules.
func (s *Store) Config(name string) (*config.Source, error) {
	return config.Load(filepath.Join(s.Dir(name), "source.yaml"))
}

// SaveConfig writes a source's rules.
func (s *Store) SaveConfig(src *config.Source) error {
	return config.Save(filepath.Join(s.Dir(src.Name), "source.yaml"), src)
}

// Manifest loads a source's manifest (an empty one if never synced).
func (s *Store) Manifest(name string) (*Manifest, error) {
	m := &Manifest{Name: name, Files: map[string]*FileMeta{}}
	if err := readJSON(s.meta(name, "manifest.json"), m); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if m.Files == nil {
		m.Files = map[string]*FileMeta{}
	}
	return m, nil
}

// State loads the last stale-check result.
func (s *Store) State(name string) (*State, error) {
	st := &State{}
	if err := readJSON(s.meta(name, "state.json"), st); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return st, nil
}

// SaveState writes the stale-check result atomically.
func (s *Store) SaveState(name string, st *State) error {
	return writeJSON(s.meta(name, "state.json"), st)
}

// Versions loads all version records, oldest first.
func (s *Store) Versions(name string) ([]*Version, error) {
	ents, err := os.ReadDir(s.meta(name, "versions"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Version
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		v := &Version{}
		if err := readJSON(s.meta(name, "versions", e.Name()), v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Remove deletes a source and its record.
func (s *Store) Remove(name string) error {
	if !s.Has(name) {
		return fmt.Errorf("no source named %q", name)
	}
	if err := os.RemoveAll(s.Dir(name)); err != nil {
		return err
	}
	return s.WriteRootIndex()
}

// Header is the YAML header at the top of every file.
type Header struct {
	Title   string `yaml:"title"`
	Kind    string `yaml:"kind"`
	Source  string `yaml:"source"`
	Path    string `yaml:"path"`
	Parent  string `yaml:"parent,omitempty"`
	Version int    `yaml:"version,omitempty"`
	Items   int    `yaml:"items,omitempty"`
	Hash    string `yaml:"hash"`
	Tokens  int    `yaml:"tokens"`
}

// Render is the full text of a file as written to disk.
func Render(h Header, body string) []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	_ = enc.Encode(h)
	_ = enc.Close()
	buf.WriteString("---\n\n# ")
	buf.WriteString(h.Title)
	buf.WriteString("\n")
	if body != "" {
		buf.WriteString("\n")
		buf.WriteString(body)
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

// SplitFile separates a stored file into its header and its body (the text
// after the "# Title" line).
func SplitFile(data []byte) (Header, string, error) {
	var h Header
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return h, s, errors.New("no YAML header")
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return h, s, errors.New("unterminated YAML header")
	}
	if err := yaml.Unmarshal([]byte(s[4:4+end]), &h); err != nil {
		return h, s, err
	}
	rest := strings.TrimLeft(s[4+end+5:], "\n")
	if strings.HasPrefix(rest, "# ") {
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			rest = rest[i+1:]
		} else {
			rest = ""
		}
	}
	return h, strings.Trim(rest, "\n"), nil
}

// Meta is how a source was loaded, recorded in the manifest.
type Meta struct {
	Title       string
	Parser      string
	Rendered    bool
	Fingerprint string
}

// Result reports what an Apply changed.
type Result struct {
	Source   string   `json:"source"`
	Version  int      `json:"version"`
	Changed  bool     `json:"changed"`
	Added    []string `json:"added"`
	Modified []string `json:"changed_files"`
	Removed  []string `json:"removed"`
	Written  int      `json:"files_written"`
	Files    int      `json:"files"`
	Tokens   int      `json:"tokens"`
}

// Apply writes a plan for a source: only files whose text differs are
// written, removed files are deleted, and a version record is saved when any
// content file was added, changed or removed.
func (s *Store) Apply(src *config.Source, p *plan.Plan, meta Meta, now time.Time) (*Result, error) {
	dir := s.Dir(src.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	old, err := s.Manifest(src.Name)
	if err != nil {
		return nil, err
	}
	res := &Result{Source: src.Name, Added: []string{}, Modified: []string{}, Removed: []string{}}
	next := old.Version + 1
	planned := map[string]*plan.File{}
	for _, f := range p.Files {
		planned[f.Path] = f
		if f.Index {
			continue
		}
		prev, ok := old.Files[f.Path]
		switch {
		case !ok:
			res.Added = append(res.Added, f.Path)
		case prev.Hash != f.Hash:
			res.Modified = append(res.Modified, f.Path)
		}
	}
	for pth, prev := range old.Files {
		if _, ok := planned[pth]; !ok && !prev.Index {
			res.Removed = append(res.Removed, pth)
		}
	}
	sort.Strings(res.Added)
	sort.Strings(res.Modified)
	sort.Strings(res.Removed)
	res.Changed = len(res.Added)+len(res.Modified)+len(res.Removed) > 0
	version := old.Version
	if res.Changed || version == 0 {
		version = next
	}

	// Keep the old text of changed and removed files for diff.
	var rec *Version
	if res.Changed {
		rec = &Version{Version: version, Time: now.UTC(), Added: res.Added, Changed: res.Modified, Removed: res.Removed, Old: map[string]OldFile{}}
		for _, pth := range append(append([]string{}, res.Modified...), res.Removed...) {
			if data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(pth))); err == nil {
				h, body, _ := SplitFile(data)
				rec.Old[pth] = OldFile{Hash: old.Files[pth].Hash, Title: h.Title, Body: body}
			} else {
				rec.Old[pth] = OldFile{Hash: old.Files[pth].Hash, Title: old.Files[pth].Title}
			}
		}
	}

	nm := &Manifest{
		Name: src.Name, URL: src.URL, Title: meta.Title, Version: version, SyncedAt: now.UTC(),
		Parser: meta.Parser, Rendered: meta.Rendered, Fingerprint: meta.Fingerprint, Files: map[string]*FileMeta{},
	}
	for _, f := range p.Files {
		v := version
		if prev, ok := old.Files[f.Path]; ok && prev.Hash == f.Hash && prev.Version > 0 {
			v = prev.Version
		}
		h := Header{Title: f.Title, Kind: f.Kind, Source: f.Source, Path: f.Path, Parent: f.Parent, Hash: f.Hash, Tokens: f.Tokens}
		if f.Index {
			h.Items = f.Items
		} else {
			h.Version = v
		}
		data := Render(h, f.Body)
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		if cur, err := os.ReadFile(full); err != nil || !bytes.Equal(cur, data) {
			if err := writeFile(full, data); err != nil {
				return nil, err
			}
			res.Written++
		}
		nm.Files[f.Path] = &FileMeta{
			Title: f.Title, Kind: f.Kind, Anchor: f.Anchor, Source: f.Source, Parent: f.Parent,
			Hash: f.Hash, Tokens: f.Tokens, Version: v, Summary: f.Summary, Index: f.Index,
		}
	}
	for pth := range old.Files {
		if _, ok := planned[pth]; !ok {
			full := filepath.Join(dir, filepath.FromSlash(pth))
			if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			pruneEmpty(filepath.Dir(full), dir)
		}
	}
	if rec != nil {
		if err := writeJSON(s.meta(src.Name, "versions", fmt.Sprintf("%04d.json", version)), rec); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(s.meta(src.Name, "manifest.json"), nm); err != nil {
		return nil, err
	}
	if err := s.SaveState(src.Name, &State{CheckedAt: now.UTC(), Fingerprint: meta.Fingerprint}); err != nil {
		return nil, err
	}
	res.Version = version
	res.Files, res.Tokens = nm.Counts()
	return res, s.WriteRootIndex()
}

// WriteRootIndex rewrites .docli/INDEX.md, the map of all sources.
func (s *Store) WriteRootIndex() error {
	names, err := s.Sources()
	if err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("# Docs (docli)\n\n")
	sb.WriteString("> Read a source's INDEX.md, pick one entry, open only that file. Each file starts with a YAML header (title, kind, source, tokens).\n\n")
	if len(names) == 0 {
		sb.WriteString("No sources yet. Add one with `docli add <url>`.\n")
	}
	for _, n := range names {
		m, err := s.Manifest(n)
		if err != nil {
			return err
		}
		files, toks := m.Counts()
		fmt.Fprintf(&sb, "- [%s](%s/INDEX.md) · %d files · %d tok · v%d · synced %s — %s\n",
			n, n, files, toks, m.Version, m.SyncedAt.Format("2006-01-02"), m.Title)
	}
	full := filepath.Join(s.Root, "INDEX.md")
	if cur, err := os.ReadFile(full); err == nil && string(cur) == sb.String() {
		return nil
	}
	return writeFile(full, []byte(sb.String()))
}

// ReadFile reads a stored file of a source.
func (s *Store) ReadFile(name, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.Dir(name), filepath.FromSlash(rel)))
}

func pruneEmpty(dir, stop string) {
	for dir != stop && strings.HasPrefix(dir, stop) {
		if err := os.Remove(dir); err != nil { // fails when not empty
			return
		}
		dir = filepath.Dir(dir)
	}
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(data, '\n'))
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
