// Package cli is docli's command line: cobra commands, the --json envelope,
// human output and the stale check that runs around each command.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"docli/internal/engine"
	"docli/internal/store"
)

// Version is set at build time.
var Version = "dev"

// app holds global flags and output state for one run.
type app struct {
	jsonOut bool
	path    string
	latest  bool
	noCheck bool

	out, errOut io.Writer
	tty         bool // stdout is a terminal: styled output
	store       *store.Store
	command     string
	checks      *checker
	spawn       func(names []string) error // starts the background check; tests replace it
}

// Envelope is the fixed --json shape of every command (see docs/json.md).
type Envelope struct {
	OK      bool        `json:"ok"`
	Command string      `json:"command"`
	Data    any         `json:"data,omitempty"`
	Stale   []StaleNote `json:"stale"`
	Error   *ErrorBody  `json:"error,omitempty"`
}

// ErrorBody is the error part of the envelope.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error is an error with a stable code for --json.
type Error struct {
	Code    string
	Msg     string
	Details any
}

func (e *Error) Error() string { return e.Msg }

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// codeOf maps any error to a stable code.
func codeOf(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var amb *store.AmbiguousError
	if errors.As(err, &amb) {
		return &Error{Code: "ambiguous", Msg: err.Error(), Details: amb.Hits}
	}
	var cov *engine.CoverageError
	if errors.As(err, &cov) {
		return &Error{Code: "coverage", Msg: err.Error(), Details: cov.Coverage}
	}
	if errors.Is(err, store.ErrNotFound) {
		return &Error{Code: "not_found", Msg: err.Error()}
	}
	return &Error{Code: "error", Msg: err.Error()}
}

func (a *app) setup(cmd *cobra.Command) error {
	a.command = cmd.Name()
	root := a.path
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = filepath.Join(wd, store.DirName)
	} else if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	a.store = store.Open(root)
	a.tty = !a.jsonOut && isTerminal(os.Stdout)
	if os.Getenv("DOCLI_NO_CHECK") == "1" {
		a.noCheck = true
	}
	return nil
}

func isTerminal(f *os.File) bool { return term.IsTerminal(f.Fd()) }

// emit prints a command's result: the JSON envelope, or the human form.
func (a *app) emit(data any, human func(w io.Writer)) error {
	notes := a.finishChecks()
	if a.jsonOut {
		return writeJSON(a.out, Envelope{OK: true, Command: a.command, Data: data, Stale: notes})
	}
	if human != nil {
		human(a.out)
	}
	a.printNotes(notes)
	return nil
}

// fail prints an error in the right form and returns the exit code.
func (a *app) fail(err error) int {
	e := codeOf(err)
	if a.jsonOut {
		notes := []StaleNote{}
		if a.checks != nil {
			notes = a.finishChecks()
		}
		_ = writeJSON(a.out, Envelope{OK: false, Command: a.command, Stale: notes,
			Error: &ErrorBody{Code: e.Code, Message: e.Msg, Details: e.Details}})
		return 1
	}
	fmt.Fprintln(a.errOut, styleErr(a, "error: ")+e.Msg)
	return 1
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Execute runs docli and returns the exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	return execute(&app{out: stdout, errOut: stderr}, args)
}

// execute runs one command on a prepared app (tests set fields such as spawn).
func execute(a *app, args []string) int {
	stdout, stderr := a.out, a.errOut
	root := newRoot(a)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		if a.command == "" {
			a.command = "docli"
			if c, _, ferr := root.Find(args); ferr == nil && c != root {
				a.command = c.Name()
			}
			a.jsonOut = a.jsonOut || hasFlag(args, "--json")
			if _, isErr := err.(*Error); !isErr {
				err = &Error{Code: "usage", Msg: err.Error()}
			}
		}
		return a.fail(err)
	}
	return 0
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}
