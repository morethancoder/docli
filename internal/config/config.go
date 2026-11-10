// Package config holds the per-source rules (source.yaml). A source's rules
// are written on `docli add`, can be edited by hand or by an agent after
// `docli inspect`, and are reused by every later sync.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// KindRule labels a section. Title and Under are regular expressions matched
// against the section title and its parent's title; empty means "any".
type KindRule struct {
	Kind  string `yaml:"kind"`
	Title string `yaml:"title,omitempty"`
	Under string `yaml:"under,omitempty"`
}

// Source is one doc source and the rules for splitting it.
type Source struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
	// Format: auto | html | markdown. auto decides from the response.
	Format string `yaml:"format"`
	// Render: auto | static | browser. browser loads the page in headless
	// Chrome so docs built by JavaScript are complete. auto tries static first
	// and switches to browser when the static page has no real content.
	Render string `yaml:"render"`
	// Selector is the CSS selector of the content region (HTML only).
	Selector string `yaml:"selector,omitempty"`
	// Drop lists CSS selectors removed before parsing (menus, footers).
	Drop []string `yaml:"drop,omitempty"`
	// FileLevel is the heading level written as one file each (0 = decide by
	// size). Shallower headings become folders.
	FileLevel int `yaml:"file_level"`
	// CollapseTokens: a section whose whole subtree is under this many tokens
	// stays one file when FileLevel is 0.
	CollapseTokens int `yaml:"collapse_tokens"`
	// MaxTokens: a file over this size is split at its next heading level.
	MaxTokens int `yaml:"max_tokens"`
	// Kinds labels sections (method, type, guide...). First match wins;
	// unmatched sections are "section".
	Kinds []KindRule `yaml:"kinds,omitempty"`
	// Base is the page's real address when URL is a saved copy (a file):
	// links on the page resolve against it, so they still point at the site.
	Base string `yaml:"base,omitempty"`
	// Changelog is the title of the section whose children are dated entries.
	Changelog string `yaml:"changelog,omitempty"`
	// CheckEvery is how old the last check may get before a read starts a
	// background stale check (Go duration, e.g. 24h).
	CheckEvery string `yaml:"check_every"`
}

// Defaults fills unset fields.
func (s *Source) Defaults() {
	if s.Format == "" {
		s.Format = "auto"
	}
	if s.Render == "" {
		s.Render = "auto"
	}
	if s.CollapseTokens == 0 {
		s.CollapseTokens = 1500
	}
	if s.MaxTokens == 0 {
		s.MaxTokens = 6000
	}
	if s.CheckEvery == "" {
		s.CheckEvery = "24h"
	}
}

// Validate reports rule errors before any work is done.
func (s *Source) Validate() error {
	if s.Name == "" || strings.ContainsAny(s.Name, `/\ `) || strings.HasPrefix(s.Name, ".") {
		return fmt.Errorf("invalid source name %q (use letters, digits, dashes)", s.Name)
	}
	u, err := url.Parse(s.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "file") {
		return fmt.Errorf("invalid url %q (http, https or file)", s.URL)
	}
	if s.Base != "" {
		if b, err := url.Parse(s.Base); err != nil || (b.Scheme != "http" && b.Scheme != "https") {
			return fmt.Errorf("invalid base %q (http or https)", s.Base)
		}
	}
	switch s.Format {
	case "auto", "html", "markdown":
	default:
		return fmt.Errorf("format must be auto, html or markdown, got %q", s.Format)
	}
	switch s.Render {
	case "auto", "static", "browser":
	default:
		return fmt.Errorf("render must be auto, static or browser, got %q", s.Render)
	}
	if s.FileLevel < 0 || s.FileLevel > 6 {
		return fmt.Errorf("file_level must be 0-6, got %d", s.FileLevel)
	}
	if _, err := time.ParseDuration(s.CheckEvery); err != nil {
		return fmt.Errorf("check_every: %v", err)
	}
	for _, k := range s.Kinds {
		for _, re := range []string{k.Title, k.Under} {
			if _, err := regexp.Compile(re); err != nil {
				return fmt.Errorf("kind %q: bad regex %q: %v", k.Kind, re, err)
			}
		}
	}
	return nil
}

// PageURL is the address links on the page are relative to: Base when set,
// else the fetched URL.
func (s *Source) PageURL(fetched string) string {
	if s.Base != "" {
		return s.Base
	}
	return fetched
}

// CheckInterval returns CheckEvery as a duration.
func (s *Source) CheckInterval() time.Duration {
	d, err := time.ParseDuration(s.CheckEvery)
	if err != nil {
		return 24 * time.Hour
	}
	return d
}

// KindOf labels a section by the first matching rule.
func (s *Source) KindOf(title, parentTitle string) string {
	if s.Changelog != "" && strings.EqualFold(parentTitle, s.Changelog) {
		return "changelog-entry"
	}
	for _, k := range s.Kinds {
		if k.Title != "" && !regexp.MustCompile(k.Title).MatchString(title) {
			continue
		}
		if k.Under != "" && !regexp.MustCompile(k.Under).MatchString(parentTitle) {
			continue
		}
		return k.Kind
	}
	return "section"
}

// Load reads a source.yaml.
func Load(path string) (*Source, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Source
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Defaults()
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Save writes a source.yaml with a short comment header.
func Save(path string, s *Source) error {
	b, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	head := "# docli source rules. Edit, then run `docli inspect " + s.Name + "` to preview\n" +
		"# and `docli sync " + s.Name + "` to apply. Field notes: docs/config.md\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(head), b...), 0o644)
}

// NameFromURL derives a short source name: host without www/docs/core plus
// the path, kebab-cased.
func NameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "docs"
	}
	host := u.Hostname()
	for _, p := range []string{"www.", "docs.", "core.", "developer.", "developers."} {
		host = strings.TrimPrefix(host, p)
	}
	if i := strings.LastIndex(host, "."); i > 0 {
		host = host[:i] // drop TLD
	}
	parts := []string{strings.ReplaceAll(host, ".", "-")}
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		seg = strings.TrimSuffix(seg, filepath.Ext(seg))
		if seg != "" && seg != "index" {
			parts = append(parts, seg)
		}
	}
	name := strings.ToLower(strings.Join(parts, "-"))
	name = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(name, "-")
	return strings.Trim(name, "-")
}

// preset is a known source with tested rules.
type preset struct {
	prefix string
	src    Source
}

var presets = []preset{
	{
		prefix: "https://core.telegram.org/bots/api",
		src: Source{
			Name:      "telegram-bot-api",
			Format:    "html",
			Render:    "static",
			Selector:  "#dev_page_content",
			FileLevel: 4,
			Kinds: []KindRule{
				{Kind: "changelog-entry", Under: `^Recent changes$`},
				{Kind: "type", Title: `^[A-Z][A-Za-z0-9]+$`},
				{Kind: "method", Title: `^[a-z][A-Za-z0-9]+$`},
				{Kind: "guide"},
			},
			Changelog: "Recent changes",
		},
	},
}

// ForURL returns rules for a URL: a tested preset when one matches, generic
// defaults otherwise. The second result says whether a preset was used.
func ForURL(raw string) (*Source, bool) {
	clean := strings.TrimRight(strings.SplitN(raw, "#", 2)[0], "/")
	for _, p := range presets {
		if strings.TrimRight(p.prefix, "/") == clean {
			s := p.src
			s.URL = clean
			s.Defaults()
			return &s, true
		}
	}
	s := &Source{Name: NameFromURL(clean), URL: clean}
	s.Defaults()
	return s, false
}

// Preset returns a tested preset by its name, for local copies of a known
// site (e.g. a saved Telegram page). Its URL is left empty.
func Preset(name string) (*Source, bool) {
	for _, p := range presets {
		if p.src.Name == name {
			s := p.src
			s.Base = p.prefix
			s.Defaults()
			return &s, true
		}
	}
	return nil, false
}

// PresetNames lists the presets.
func PresetNames() []string {
	var out []string
	for _, p := range presets {
		out = append(out, p.src.Name)
	}
	return out
}
