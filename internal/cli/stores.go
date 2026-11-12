package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/morethancoder/docli/internal/store"
)

// Scopes of a source: the project's ./.docli (or the --path folder), or the
// global store shared by all projects (~/.docli, or $DOCLI_HOME).
const (
	scopeProject = "project"
	scopeGlobal  = "global"
)

// srcRef is one source and the store it lives in.
type srcRef struct {
	Name  string
	Scope string
	St    *store.Store
}

// errHome means there is no home folder, so no global store (unless
// DOCLI_HOME names one).
var errHome = errors.New("no home folder for the global store (set DOCLI_HOME)")

// globalRoot is the global store folder: $DOCLI_HOME, else ~/.docli.
// DOCLI_HOME must be absolute (a leading ~/ is expanded): a relative one
// would name a different store in every folder.
func globalRoot() (string, error) {
	home, herr := os.UserHomeDir()
	if h := os.Getenv("DOCLI_HOME"); h != "" {
		if rest, ok := strings.CutPrefix(h, "~"); ok && (rest == "" || rest[0] == '/') {
			if herr != nil {
				return "", errf("usage", "DOCLI_HOME=%s starts with ~ but there is no home folder; give an absolute path", h)
			}
			h = home + rest
		}
		if !filepath.IsAbs(h) {
			return "", errf("usage", "DOCLI_HOME=%s is relative; give an absolute path", h)
		}
		return filepath.Clean(h), nil
	}
	if herr != nil {
		return "", errHome
	}
	return filepath.Join(home, store.DirName), nil
}

// sameDir reports whether two folders are the same, through symlinks
// (/tmp and /private/tmp on macOS). Missing folders compare by path.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

// openStores sets a.store (where writes go) and a.fallback (the global store
// that reads fall back to). --path names one store and turns the fallback
// off; --global makes the global store the only one.
func (a *app) openStores() error {
	if a.global && a.path != "" {
		return errf("usage", "--global and --path both name the docs folder; give one")
	}
	if a.path != "" {
		root, err := filepath.Abs(a.path)
		if err != nil {
			return err
		}
		a.store, a.scope = store.Open(root), scopeProject
		return nil
	}
	groot, err := globalRoot()
	if err != nil && (a.global || !errors.Is(err, errHome)) {
		return err
	}
	if a.global {
		a.store, a.scope = store.Open(groot), scopeGlobal
		return nil
	}
	wd, werr := os.Getwd()
	if werr != nil {
		return werr
	}
	root := filepath.Join(wd, store.DirName)
	a.store, a.scope = store.Open(root), scopeProject
	switch {
	case err != nil:
		// No home folder: work with the project store alone.
	case sameDir(groot, root):
		a.scope = scopeGlobal // running in the home folder: one store
	default:
		a.fallback = store.Open(groot)
	}
	return nil
}

func (a *app) ref(name string) srcRef { return srcRef{Name: name, Scope: a.scope, St: a.store} }

func globalRef(st *store.Store, name string) srcRef {
	return srcRef{Name: name, Scope: scopeGlobal, St: st}
}

// lookup finds a source by name: the project copy first, then the global one.
func (a *app) lookup(name string) (srcRef, bool) {
	if a.store.Has(name) {
		return a.ref(name), true
	}
	if a.fallback != nil && a.fallback.Has(name) {
		return globalRef(a.fallback, name), true
	}
	return srcRef{}, false
}

// all lists the sources reads cover: the project's, then the global ones
// the project doesn't shadow. shadowed lists global copies hidden by a
// project source of the same name.
func (a *app) all() (refs, shadowed []srcRef, err error) {
	names, err := a.store.Sources()
	if err != nil {
		return nil, nil, err
	}
	for _, n := range names {
		refs = append(refs, a.ref(n))
	}
	if a.fallback == nil {
		return refs, nil, nil
	}
	gnames, err := a.fallback.Sources()
	if err != nil {
		return nil, nil, err
	}
	for _, n := range gnames {
		if a.store.Has(n) {
			shadowed = append(shadowed, globalRef(a.fallback, n))
			continue
		}
		refs = append(refs, globalRef(a.fallback, n))
	}
	return refs, shadowed, nil
}

// globalOnly lists the global sources that reads can reach (not shadowed).
func (a *app) globalOnly(refs []srcRef) []string {
	var out []string
	for _, r := range refs {
		if r.St == a.fallback && a.fallback != nil {
			out = append(out, r.Name)
		}
	}
	return out
}

func projectNames(refs []srcRef, st *store.Store) []string {
	var out []string
	for _, r := range scoped(refs, st) {
		out = append(out, r.Name)
	}
	return out
}

// scoped keeps the refs in store st.
func scoped(refs []srcRef, st *store.Store) []srcRef {
	var out []srcRef
	for _, r := range refs {
		if r.St == st {
			out = append(out, r)
		}
	}
	return out
}

// latestRefs is what --latest may sync: the one source a command works on,
// wherever it lives, or the project's sources when it works on many. A write
// to the global store needs that source to be the answer.
func (a *app) latestRefs(refs []srcRef) []srcRef {
	if len(refs) <= 1 {
		return refs
	}
	return scoped(refs, a.store)
}

// emptyErr is the error for no docs at all, naming every store looked in.
func (a *app) emptyErr() error {
	if a.fallback == nil {
		cmd := "docli add <url>"
		if a.scope == scopeGlobal {
			cmd = "docli add --global <url>"
		}
		return errf("empty", "no docs in %s yet; add some with `%s`", a.rel(a.store.Root), cmd)
	}
	return errf("empty", "no docs in %s or in the global store %s yet; add some with `docli add <url>` (this project) or `docli add --global <url>` (all projects)",
		a.rel(a.store.Root), a.fallback.Root)
}

// notHere is the error for a write naming a source this store doesn't have.
// When the global store has it, it says to add --global.
func (a *app) notHere(cmd, name string) error {
	if a.fallback != nil && a.fallback.Has(name) {
		return errf("not_found", "%q is in the global store (%s), not this project; use `docli %s --global %s`",
			name, a.fallback.Root, cmd, name)
	}
	return errf("not_found", "no source named %q (see `docli list`)", name)
}

// find resolves a show reference: in the named source, else in the project
// store, else in the global sources the project doesn't shadow.
func (a *app) find(q, source string) (store.Hit, srcRef, error) {
	if source != "" {
		r, ok := a.lookup(source)
		if !ok {
			return store.Hit{}, srcRef{}, errf("not_found", "no source named %q (see `docli list`)", source)
		}
		h, err := r.St.Find(q, source)
		return h, r, err
	}
	refs, _, err := a.all()
	if err != nil {
		return store.Hit{}, srcRef{}, err
	}
	h, err := a.store.FindIn(q, projectNames(refs, a.store))
	if err == nil || a.fallback == nil || !errors.Is(err, store.ErrNotFound) {
		return h, a.ref(h.Source), err
	}
	gh, gerr := a.fallback.FindIn(q, a.globalOnly(refs))
	if gerr == nil || !errors.Is(gerr, store.ErrNotFound) {
		return gh, globalRef(a.fallback, gh.Source), gerr
	}
	return h, srcRef{}, err
}

// tag is the human label of a source: its name, plus "global" when it comes
// from the global store.
func tag(a *app, r srcRef) string {
	if r.Scope == scopeGlobal {
		return r.Name + " " + styled(a, dimS, "(global)")
	}
	return r.Name
}

func refNames(refs []srcRef) string {
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}
