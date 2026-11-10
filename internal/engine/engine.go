// Package engine runs the steps that touch the network: load a source,
// plan its files, write them, and check whether the remote changed.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/morethancoder/docli/internal/config"
	"github.com/morethancoder/docli/internal/outline"
	"github.com/morethancoder/docli/internal/parse"
	"github.com/morethancoder/docli/internal/plan"
	"github.com/morethancoder/docli/internal/store"
)

// Loaded is a fetched, parsed and planned source.
type Loaded struct {
	Asked       string // the URL as given, before redirects
	Result      *parse.Result
	Plan        *plan.Plan
	Fingerprint string
}

// Fingerprint identifies the content of a parsed source. The Telegram page
// sends no ETag or Last-Modified, so change detection hashes the text.
func Fingerprint(doc *outline.Document) string {
	return plan.Hash(strings.Join(strings.Fields(doc.SourceText), " "))
}

// Prepare loads and plans a source without writing anything.
func Prepare(ctx context.Context, src *config.Source) (*Loaded, error) {
	res, err := parse.Load(ctx, src)
	if err != nil {
		return nil, err
	}
	return &Loaded{Asked: src.URL, Result: res, Plan: plan.Build(res.Doc, src), Fingerprint: Fingerprint(res.Doc)}, nil
}

// CoverageError is returned when the split lost or duplicated source text.
type CoverageError struct{ Coverage plan.Coverage }

func (e *CoverageError) Error() string {
	c := e.Coverage
	return fmt.Sprintf("coverage check failed: %d of %d source words missing, %d duplicated (%s). Preview with `docli inspect`, adjust the selector or drop rules, or pass --force to write anyway",
		c.Missing, c.SourceWords, c.Duplicated, strings.Join(c.Examples, ", "))
}

// Sync loads a source and writes what changed. force writes even when the
// coverage check fails. A source whose text has not changed since the last
// sync passes without force: that split was already accepted, and a routine
// sync must not fail on it.
func Sync(ctx context.Context, st *store.Store, src *config.Source, force bool) (*store.Result, *Loaded, error) {
	l, err := Prepare(ctx, src)
	if err != nil {
		return nil, nil, err
	}
	if !l.Plan.Coverage.OK && !force && !unchanged(st, src.Name, l.Fingerprint) {
		return nil, l, &CoverageError{Coverage: l.Plan.Coverage}
	}
	if err := st.SaveConfig(src); err != nil {
		return nil, l, err
	}
	res, err := st.Apply(src, l.Plan, store.Meta{
		Title: l.Result.Doc.Title, Parser: l.Result.Parser, Rendered: l.Result.Rendered, Fingerprint: l.Fingerprint,
	}, time.Now())
	if err != nil {
		return nil, l, err
	}
	if _, err := st.UpdateChangelog(src.Name); err != nil {
		return res, l, err
	}
	return res, l, nil
}

func unchanged(st *store.Store, name, fp string) bool {
	if !st.Has(name) {
		return false
	}
	m, err := st.Manifest(name)
	return err == nil && m.Fingerprint != "" && m.Fingerprint == fp
}

// Check fetches a source and compares it with the last sync. It saves and
// returns the result.
func Check(ctx context.Context, st *store.Store, name string) (*store.State, error) {
	src, err := st.Config(name)
	if err != nil {
		return nil, err
	}
	m, err := st.Manifest(name)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	res, err := parse.Load(ctx, src)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err() // cut short by the caller; nothing learned
		}
		s := &store.State{CheckedAt: now, Error: err.Error()}
		_ = st.SaveState(name, s)
		return s, err
	}
	fp := Fingerprint(res.Doc)
	s := &store.State{CheckedAt: now, Fingerprint: fp, Stale: fp != m.Fingerprint}
	return s, st.SaveState(name, s)
}
