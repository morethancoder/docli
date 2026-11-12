package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/morethancoder/docli/internal/engine"
	"github.com/morethancoder/docli/internal/store"
)

// Budget is how long a command waits for its stale check before handing the
// check to a background process.
var Budget = 1500 * time.Millisecond

// StaleNote is the stale-check status of one source, reported at the end of
// a command (and in the "stale" field of --json).
type StaleNote struct {
	Source    string    `json:"source"`
	Scope     string    `json:"scope"`  // project or global: the store of the copy checked
	Status    string    `json:"status"` // stale, fresh, pending, error
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Message   string    `json:"message"`
}

type checkRun struct {
	ref   srcRef
	done  chan struct{}
	state *store.State
	err   error
}

// checker runs the stale checks of one command.
type checker struct {
	start  time.Time
	cancel context.CancelFunc
	runs   []*checkRun
	notes  []StaleNote
	once   sync.Once
	result []StaleNote
}

// beginChecks reports saved results (from earlier background checks) and
// starts checks for sources whose last check is older than their interval.
// It never blocks.
func (a *app) beginChecks(refs []srcRef) {
	c := &checker{start: time.Now()}
	a.checks = c
	if a.noCheck {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	for _, ref := range refs {
		st, err := ref.St.State(ref.Name)
		if err != nil {
			continue
		}
		src, err := ref.St.Config(ref.Name)
		if err != nil {
			continue
		}
		if time.Since(st.CheckedAt) < src.CheckInterval() {
			if note, ok := noteFor(ref, st); ok {
				c.notes = append(c.notes, note)
			}
			continue
		}
		r := &checkRun{ref: ref, done: make(chan struct{})}
		c.runs = append(c.runs, r)
		go func() {
			defer close(r.done)
			r.state, r.err = engine.Check(ctx, ref.St, ref.Name)
		}()
	}
}

// finishChecks waits for running checks until the budget is spent, then
// moves the rest to a detached `docli check` and returns all notes.
func (a *app) finishChecks() []StaleNote {
	c := a.checks
	if c == nil {
		return []StaleNote{}
	}
	c.once.Do(func() {
		deadline := time.After(time.Until(c.start.Add(Budget)))
		var late []*checkRun
		for _, r := range c.runs {
			select {
			case <-r.done:
			case <-deadline:
				deadline = closedChan()
			}
			select {
			case <-r.done:
				if r.state != nil {
					if note, ok := noteFor(r.ref, r.state); ok {
						c.notes = append(c.notes, note)
					}
				}
			default:
				late = append(late, r)
			}
		}
		if c.cancel != nil {
			c.cancel()
		}
		// One background check per store, so each looks in the right folder.
		spawn := a.spawn
		if spawn == nil {
			spawn = spawnCheck
		}
		for _, st := range storesOf(late) {
			var names []string
			for _, r := range late {
				if r.ref.St == st {
					names = append(names, r.ref.Name)
				}
			}
			if err := spawn(st.Root, names); err != nil {
				continue
			}
			for _, r := range late {
				if r.ref.St == st {
					c.notes = append(c.notes, StaleNote{Source: r.ref.Name, Scope: r.ref.Scope, Status: "pending",
						Message: "checking for updates in the background; the result shows on the next command"})
				}
			}
		}
		c.result = c.notes
		if c.result == nil {
			c.result = []StaleNote{}
		}
	})
	return c.result
}

// storesOf lists the stores of runs, in first-seen order.
func storesOf(runs []*checkRun) []*store.Store {
	var out []*store.Store
	for _, r := range runs {
		if !slices.Contains(out, r.ref.St) {
			out = append(out, r.ref.St)
		}
	}
	return out
}

func closedChan() <-chan time.Time {
	ch := make(chan time.Time)
	close(ch)
	return ch
}

// noteFor turns a saved state into a note. Fresh results are only reported
// in --json (as status "fresh"), so humans see a line only when it matters.
func noteFor(r srcRef, st *store.State) (StaleNote, bool) {
	if st.CheckedAt.IsZero() {
		return StaleNote{}, false
	}
	name := r.Name
	if r.Scope == scopeGlobal {
		name = "--global " + name
	}
	n := StaleNote{Source: r.Name, Scope: r.Scope, CheckedAt: st.CheckedAt}
	switch {
	case st.Error != "":
		n.Status, n.Message = "error", "update check failed: "+st.Error
	case st.Stale:
		n.Status, n.Message = "stale", fmt.Sprintf("the docs changed upstream (checked %s). Update with: docli sync %s", ago(st.CheckedAt), name)
	default:
		n.Status, n.Message = "fresh", fmt.Sprintf("up to date (checked %s)", ago(st.CheckedAt))
	}
	return n, true
}

// noteStaleGlobal adds a note for each global source whose last recorded
// check found it stale (no new fetch). sync without --global uses it to say
// what it left out.
func (a *app) noteStaleGlobal() error {
	if a.fallback == nil {
		return nil
	}
	names, err := a.fallback.Sources()
	if err != nil {
		return err
	}
	for _, n := range names {
		if a.store.Has(n) {
			continue // shadowed by the project copy, which this sync covers
		}
		st, err := a.fallback.State(n)
		if err != nil || !st.Stale || st.Error != "" {
			continue
		}
		a.notes = append(a.notes, StaleNote{Source: n, Scope: scopeGlobal, Status: "stale", CheckedAt: st.CheckedAt,
			Message: fmt.Sprintf("the global copy changed upstream (checked %s) and this sync covers the project only. Update with: docli sync --global %s", ago(st.CheckedAt), n)})
	}
	return nil
}

// spawnCheck starts `docli check` on the store at root, detached and with no
// terminal attached, so it can finish after this command exits and save its
// result.
func spawnCheck(root string, names []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{"check", "--background", "--path", root}, names...)
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil // /dev/null
	cmd.Env = append(os.Environ(), "DOCLI_NO_CHECK=1")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (a *app) printNotes(notes []StaleNote) {
	for _, n := range notes {
		name := n.Source
		if n.Scope == scopeGlobal {
			name += " (global)"
		}
		switch n.Status {
		case "stale", "error":
			fmt.Fprintln(a.errOut, styleWarn(a, "⚠ "+name+": ")+n.Message)
		case "pending":
			fmt.Fprintln(a.errOut, styleDim(a, "· "+name+": "+n.Message))
		}
	}
}

// latestFor runs --latest: check now (no budget), sync what is stale.
// A global source is synced in the global store: asking for --latest is
// consent for that one write.
func (a *app) latestFor(refs []srcRef) error {
	for _, r := range refs {
		n := r.Name
		st, err := engine.Check(context.Background(), r.St, n)
		if err != nil {
			return fmt.Errorf("--latest: checking %s: %w", n, err)
		}
		if !st.Stale {
			continue
		}
		src, err := r.St.Config(n)
		if err != nil {
			return err
		}
		res, _, err := engine.Sync(context.Background(), r.St, src, false)
		if err != nil {
			return fmt.Errorf("--latest: syncing %s: %w", n, err)
		}
		if !a.jsonOut {
			fmt.Fprintf(a.errOut, "%s synced to v%d (%d added, %d changed, %d removed)\n",
				tag(a, r), res.Version, len(res.Added), len(res.Modified), len(res.Removed))
		}
	}
	return nil
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
