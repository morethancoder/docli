package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"docli/internal/engine"
	"docli/internal/store"
)

// Budget is how long a command waits for its stale check before handing the
// check to a background process.
var Budget = 1500 * time.Millisecond

// StaleNote is the stale-check status of one source, reported at the end of
// a command (and in the "stale" field of --json).
type StaleNote struct {
	Source    string    `json:"source"`
	Status    string    `json:"status"` // stale, fresh, pending, error
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Message   string    `json:"message"`
}

type checkRun struct {
	name  string
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
func (a *app) beginChecks(names []string) {
	c := &checker{start: time.Now()}
	a.checks = c
	if a.noCheck {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	for _, n := range names {
		st, err := a.store.State(n)
		if err != nil {
			continue
		}
		src, err := a.store.Config(n)
		if err != nil {
			continue
		}
		if time.Since(st.CheckedAt) < src.CheckInterval() {
			if note, ok := noteFor(n, st); ok {
				c.notes = append(c.notes, note)
			}
			continue
		}
		r := &checkRun{name: n, done: make(chan struct{})}
		c.runs = append(c.runs, r)
		go func() {
			defer close(r.done)
			r.state, r.err = engine.Check(ctx, a.store, n)
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
		var late []string
		for _, r := range c.runs {
			select {
			case <-r.done:
			case <-deadline:
				deadline = closedChan()
			}
			select {
			case <-r.done:
				if r.state != nil {
					if note, ok := noteFor(r.name, r.state); ok {
						c.notes = append(c.notes, note)
					}
				}
			default:
				late = append(late, r.name)
			}
		}
		if c.cancel != nil {
			c.cancel()
		}
		if len(late) > 0 {
			spawn := a.spawn
			if spawn == nil {
				spawn = a.spawnCheck
			}
			if err := spawn(late); err == nil {
				for _, n := range late {
					c.notes = append(c.notes, StaleNote{Source: n, Status: "pending",
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

func closedChan() <-chan time.Time {
	ch := make(chan time.Time)
	close(ch)
	return ch
}

// noteFor turns a saved state into a note. Fresh results are only reported
// in --json (as status "fresh"), so humans see a line only when it matters.
func noteFor(name string, st *store.State) (StaleNote, bool) {
	if st.CheckedAt.IsZero() {
		return StaleNote{}, false
	}
	n := StaleNote{Source: name, CheckedAt: st.CheckedAt}
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

// spawnCheck starts `docli check` detached, with no terminal attached, so it
// can finish after this command exits and save its result.
func (a *app) spawnCheck(names []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := append([]string{"check", "--background", "--path", a.store.Root}, names...)
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
		switch n.Status {
		case "stale":
			fmt.Fprintln(a.errOut, styleWarn(a, "⚠ "+n.Source+": ")+n.Message)
		case "error":
			fmt.Fprintln(a.errOut, styleWarn(a, "⚠ "+n.Source+": ")+n.Message)
		case "pending":
			fmt.Fprintln(a.errOut, styleDim(a, "· "+n.Source+": "+n.Message))
		}
	}
}

// latestFor runs --latest: check now (no budget), sync what is stale.
func (a *app) latestFor(names []string) error {
	for _, n := range names {
		st, err := engine.Check(context.Background(), a.store, n)
		if err != nil {
			return fmt.Errorf("--latest: checking %s: %w", n, err)
		}
		if !st.Stale {
			continue
		}
		src, err := a.store.Config(n)
		if err != nil {
			return err
		}
		res, _, err := engine.Sync(context.Background(), a.store, src, false)
		if err != nil {
			return fmt.Errorf("--latest: syncing %s: %w", n, err)
		}
		if !a.jsonOut {
			fmt.Fprintf(a.errOut, "%s synced to v%d (%d added, %d changed, %d removed)\n",
				n, res.Version, len(res.Added), len(res.Modified), len(res.Removed))
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
