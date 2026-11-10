// Package parse turns a fetched source into an outline. Each source type is a
// Parser; adding a type means adding one Parser to the list below and nothing
// else (splitting, storage, sync and output work on outlines only).
package parse

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"docli/internal/config"
	"docli/internal/fetch"
	"docli/internal/outline"
)

// Parser turns one kind of source into an outline.
type Parser interface {
	Name() string
	Detect(raw *fetch.Raw) bool
	Parse(raw *fetch.Raw, src *config.Source) (*outline.Document, error)
}

// Parsers is the registry, tried in order for format: auto.
var Parsers = []Parser{Markdown{}, HTML{}}

func byName(name string) Parser {
	for _, p := range Parsers {
		if p.Name() == name {
			return p
		}
	}
	return nil
}

// Result is a parsed source plus how it was obtained.
type Result struct {
	Doc      *outline.Document
	Raw      *fetch.Raw
	Parser   string
	Rendered bool   // loaded through the headless browser
	Note     string // why the browser was used, when it was
}

// Load fetches and parses a source by its rules.
func Load(ctx context.Context, src *config.Source) (*Result, error) {
	var raw *fetch.Raw
	var err error
	if src.Render == "browser" {
		raw, err = fetch.Browser(ctx, src.URL)
	} else {
		raw, err = fetch.Static(ctx, src.URL)
	}
	if err != nil {
		return nil, err
	}
	res, err := parseRaw(raw, src)
	if src.Render != "auto" || (err == nil && !looksEmpty(res.Doc)) {
		return res, err
	}
	// auto: the static page had no real content, so JavaScript probably
	// builds it. Load it in the browser.
	braw, berr := fetch.Browser(ctx, src.URL)
	if berr != nil {
		if errors.Is(berr, fetch.ErrNoBrowser) && err == nil {
			res.Note = "static page looks empty and no browser was found; " + berr.Error()
			return res, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%v; browser fallback: %v", err, berr)
		}
		return nil, berr
	}
	bres, berr := parseRaw(braw, src)
	if berr != nil {
		return nil, berr
	}
	bres.Note = "static page had no real content; loaded with headless browser"
	return bres, nil
}

func parseRaw(raw *fetch.Raw, src *config.Source) (*Result, error) {
	var p Parser
	if src.Format != "auto" {
		p = byName(src.Format)
	} else {
		for _, c := range Parsers {
			if c.Detect(raw) {
				p = c
				break
			}
		}
		if p == nil {
			p = HTML{}
		}
	}
	doc, err := p.Parse(raw, src)
	if err != nil {
		return nil, err
	}
	if doc.Title == "" {
		doc.Title = src.Name
	}
	return &Result{Doc: doc, Raw: raw, Parser: p.Name(), Rendered: raw.Rendered}, nil
}

// looksEmpty says a page has no real docs content: no headings and very
// little text (an app shell waiting for JavaScript).
func looksEmpty(d *outline.Document) bool {
	return len(d.Root.Children) == 0 && len(strings.TrimSpace(d.SourceText)) < 500
}
