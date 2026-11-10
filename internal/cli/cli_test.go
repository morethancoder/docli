package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/morethancoder/docli/internal/store"
	"github.com/morethancoder/docli/internal/update"
	"github.com/morethancoder/docli/internal/update/updatetest"
)

// docServer serves one markdown page whose text and delay tests can change.
type docServer struct {
	mu    sync.Mutex
	body  string
	delay time.Duration
	hits  int
}

func (d *docServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	body, delay := d.body, d.delay
	d.hits++
	d.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func (d *docServer) set(body string, delay time.Duration) {
	d.mu.Lock()
	d.body, d.delay = body, delay
	d.mu.Unlock()
}

type env struct {
	t       *testing.T
	root    string
	srv     *httptest.Server
	docs    *docServer
	spawned [][]string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	text, err := os.ReadFile(filepath.Join("..", "..", "testdata", "llms.md"))
	if err != nil {
		t.Fatal(err)
	}
	d := &docServer{body: string(text)}
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	return &env{t: t, root: filepath.Join(t.TempDir(), ".docli"), srv: srv, docs: d}
}

// run executes docli with --path set and returns the exit code, stdout and
// stderr.
func (e *env) run(args ...string) (int, string, string) {
	e.t.Helper()
	var out, errOut bytes.Buffer
	a := &app{out: &out, errOut: &errOut, spawn: func(names []string) error {
		e.spawned = append(e.spawned, names)
		return nil
	}}
	code := execute(a, append(args, "--path", e.root))
	return code, out.String(), errOut.String()
}

// envelope runs a --json command and decodes its output.
func (e *env) envelope(args ...string) (int, Envelope, map[string]any) {
	e.t.Helper()
	code, out, stderr := e.run(append(args, "--json")...)
	var env Envelope
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		e.t.Fatalf("%v: output is not JSON: %v\n%s\nstderr: %s", args, err, out, stderr)
	}
	_ = json.Unmarshal([]byte(out), &env)
	for _, k := range []string{"ok", "command", "stale"} {
		if _, ok := raw[k]; !ok {
			e.t.Errorf("%v: envelope has no %q: %s", args, k, out)
		}
	}
	return code, env, raw
}

func (e *env) add() {
	e.t.Helper()
	code, env, raw := e.envelope("add", e.srv.URL+"/llms.md", "--name", "sdk", "--file-level", "3")
	if code != 0 || !env.OK {
		e.t.Fatalf("add failed: %v", raw)
	}
}

// expire makes the next read run a stale check.
func (e *env) expire() {
	e.t.Helper()
	st := store.Open(e.root)
	if err := st.SaveState("sdk", &store.State{}); err != nil {
		e.t.Fatal(err)
	}
}

func data(raw map[string]any) map[string]any {
	m, _ := raw["data"].(map[string]any)
	return m
}

func TestAddShowEnvelope(t *testing.T) {
	e := newEnv(t)
	code, env, raw := e.envelope("add", e.srv.URL+"/llms.md", "--name", "sdk", "--file-level", "3")
	if code != 0 || !env.OK || env.Command != "add" {
		t.Fatalf("add: code %d %v", code, raw)
	}
	if d := data(raw); d["version"] != 1.0 || d["source"] != "sdk" {
		t.Errorf("add data: %v", d)
	}
	if _, err := os.Stat(filepath.Join(e.root, "sdk", "api", "createWidget.md")); err != nil {
		t.Errorf("file not written: %v", err)
	}

	code, env, raw = e.envelope("show", "createWidget")
	if code != 0 || !env.OK {
		t.Fatalf("show: %v", raw)
	}
	d := data(raw)
	if d["path"] != "api/createWidget.md" || !strings.Contains(d["content"].(string), "createWidget") {
		t.Errorf("show data: %v", d)
	}
	if s, ok := raw["stale"].([]any); !ok || len(s) != 1 || s[0].(map[string]any)["status"] != "fresh" {
		// add records a fresh check, so a read right after reports "fresh".
		t.Errorf("stale field: %v", raw["stale"])
	}

	// Without --json, a pipe gets the raw file: YAML header, then the body.
	code, out, _ := e.run("show", "createWidget")
	if code != 0 || !strings.HasPrefix(out, "---\n") || !strings.Contains(out, "path: api/createWidget.md") {
		t.Errorf("plain show:\n%s", out)
	}
}

func TestErrors(t *testing.T) {
	e := newEnv(t)
	code, env, raw := e.envelope("show", "anything")
	if code != 1 || env.OK || env.Error == nil || env.Error.Code != "empty" {
		t.Errorf("show with no sources: code %d %v", code, raw)
	}
	e.add()
	code, env, raw = e.envelope("show", "no-such-thing-xyz")
	if code != 1 || env.OK || env.Error == nil || env.Error.Code != "not_found" {
		t.Errorf("not found: code %d %v", code, raw)
	}
	code, env, raw = e.envelope("diff", "--since", "banana")
	if code != 1 || env.Error == nil {
		t.Errorf("bad --since: code %d %v", code, raw)
	}
	code, env, raw = e.envelope("frobnicate")
	if code != 1 || env.Error == nil || env.Error.Code != "usage" {
		t.Errorf("unknown command: code %d %v", code, raw)
	}
}

func TestStaleCheckPendingWithinBudget(t *testing.T) {
	e := newEnv(t)
	e.add()
	e.expire()
	e.docs.set(e.docs.body, 10*time.Second) // upstream hangs

	start := time.Now()
	code, _, raw := e.envelope("show", "createWidget")
	took := time.Since(start)
	if code != 0 {
		t.Fatalf("show failed: %v", raw)
	}
	if took > Budget+time.Second {
		t.Errorf("command took %v; budget is %v", took, Budget)
	}
	s, _ := raw["stale"].([]any)
	if len(s) != 1 || s[0].(map[string]any)["status"] != "pending" {
		t.Errorf("want a pending note, got %v", raw["stale"])
	}
	if len(e.spawned) != 1 || e.spawned[0][0] != "sdk" {
		t.Errorf("background check not handed off: %v", e.spawned)
	}

	// Human form: the note goes to stderr, the answer to stdout.
	e.expire()
	_, out, stderr := e.run("show", "createWidget")
	if !strings.Contains(stderr, "sdk: checking for updates in the background") || strings.Contains(out, "checking") {
		t.Errorf("stdout %q\nstderr %q", out, stderr)
	}
}

func TestStaleCheckFindsChange(t *testing.T) {
	e := newEnv(t)
	e.add()
	e.expire()
	e.docs.set(strings.Replace(e.docs.body, "Creates a widget", "Creates a shiny widget", 1), 0)

	_, _, raw := e.envelope("show", "createWidget")
	s, _ := raw["stale"].([]any)
	if len(s) != 1 || s[0].(map[string]any)["status"] != "stale" {
		t.Fatalf("want a stale note, got %v", raw["stale"])
	}
	if len(e.spawned) != 0 {
		t.Errorf("fast check should not spawn: %v", e.spawned)
	}
	// The check never syncs by itself.
	if d := data(raw); strings.Contains(d["content"].(string), "shiny") {
		t.Error("stale check changed the files")
	}

	// --latest checks, syncs, then answers.
	code, _, raw := e.envelope("show", "createWidget", "--latest")
	if code != 0 || !strings.Contains(data(raw)["content"].(string), "shiny") {
		t.Fatalf("--latest did not sync: %v", raw)
	}
	if v := data(raw)["version"]; v != 2.0 {
		t.Errorf("file version %v, want 2", v)
	}
	code, _, raw = e.envelope("diff", "sdk")
	if code != 0 || !strings.Contains(data(raw)["files"].([]any)[0].(map[string]any)["diff"].(string), "+") {
		t.Errorf("diff after --latest: %v", raw)
	}
}

func TestSkill(t *testing.T) {
	e := newEnv(t)
	out := filepath.Join(t.TempDir(), "SKILL.md")
	if code, _, raw := e.envelope("skill", "--out", out); code != 0 {
		t.Fatalf("skill: %v", raw)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.HasPrefix(string(b), "---\nname: docli") {
		t.Fatalf("SKILL.md: %v %.80q", err, b)
	}
	if code, _, _ := e.envelope("skill", "--out", out); code != 0 {
		t.Error("same content again should succeed")
	}
	_ = os.WriteFile(out, []byte("edited by hand"), 0o644)
	if code, env, _ := e.envelope("skill", "--out", out); code != 1 || env.Error == nil {
		t.Error("an edited SKILL.md must not be overwritten without --force")
	}
	if code, _, _ := e.envelope("skill", "--out", out, "--force"); code != 0 {
		t.Error("--force should overwrite")
	}
}

func TestVersion(t *testing.T) {
	e := newEnv(t)
	code, env, raw := e.envelope("version")
	d := data(raw)
	if code != 0 || !env.OK || d["version"] == "" || d["os"] == "" || d["go"] == "" {
		t.Fatalf("version: %v", raw)
	}
}

func TestUpdate(t *testing.T) {
	f := &updatetest.FakeRelease{Tag: "v0.1.0", GOOS: runtime.GOOS, ARCH: runtime.GOARCH, Binary: []byte("NEW")}
	srv := f.Server(t)
	exe := filepath.Join(t.TempDir(), "docli")
	_ = os.WriteFile(exe, []byte("OLD"), 0o755)
	run := func(version string, args ...string) (int, Envelope, map[string]any) {
		old := Version
		Version = version
		defer func() { Version = old }()
		var out bytes.Buffer
		a := &app{out: &out, errOut: &out, exePath: exe,
			newUpdater: func() *update.Client {
				return &update.Client{API: srv.URL, HTTP: srv.Client(), GOOS: runtime.GOOS, ARCH: runtime.GOARCH}
			}}
		code := execute(a, append(args, "--json", "--path", filepath.Join(t.TempDir(), ".docli")))
		var env Envelope
		var raw map[string]any
		_ = json.Unmarshal(out.Bytes(), &env)
		_ = json.Unmarshal(out.Bytes(), &raw)
		return code, env, raw
	}
	read := func() string { b, _ := os.ReadFile(exe); return string(b) }

	if code, _, raw := run("v0.0.9", "update", "--check"); code != 0 || data(raw)["newer"] != true || read() != "OLD" {
		t.Fatalf("--check: %v", raw)
	}
	if code, env, _ := run("dev", "update"); code != 1 || env.Error == nil || env.Error.Code != "usage" || read() != "OLD" {
		t.Fatalf("dev build must be refused without --force: %v", env.Error)
	}
	if code, _, raw := run("v0.1.0", "update"); code != 0 || data(raw)["updated"] != false || read() != "OLD" {
		t.Fatalf("same version must not update: %v", raw)
	}
	if code, _, raw := run("v0.0.9", "update"); code != 0 || data(raw)["updated"] != true || read() != "NEW" {
		t.Fatalf("update: %v, binary %q", raw, read())
	}
}

func TestShowWithSourceArg(t *testing.T) {
	e := newEnv(t)
	e.add()
	code, _, raw := e.envelope("show", "sdk", "createWidget")
	if code != 0 || data(raw)["path"] != "api/createWidget.md" {
		t.Fatalf("show sdk createWidget: %v", raw)
	}
	if code, env, _ := e.envelope("show", "nope", "createWidget"); code != 1 || env.Error.Code != "not_found" {
		t.Errorf("unknown source: %v", env.Error)
	}
}

func TestSearchMissHint(t *testing.T) {
	e := newEnv(t)
	e.add()
	_, _, raw := e.envelope("search", "zzqx-not-there")
	h, _ := raw["hint"].(string)
	if !strings.Contains(h, "sdk (") || !strings.Contains(h, "docli add") {
		t.Errorf("hint %q", h)
	}
	if _, _, raw := e.envelope("search", "createWidget"); raw["hint"] != nil {
		t.Errorf("hint on a hit: %v", raw["hint"])
	}
}

func TestInspectCoverageFailureExits1(t *testing.T) {
	e := newEnv(t)
	page := filepath.Join(t.TempDir(), "lossy.html")
	_ = os.WriteFile(page, []byte(`<html><body><main><h1>T</h1><p>Intro text that is long enough to count as the content of this page.</p>
<textarea>zebra quokka</textarea><h2>A</h2><p>more text</p></main></body></html>`), 0o644)
	code, env, raw := e.envelope("inspect", page)
	if code != 1 || env.Error == nil || env.Error.Code != "coverage" {
		t.Fatalf("want coverage error, got code %d %v", code, raw)
	}
	det, _ := raw["error"].(map[string]any)["details"].(map[string]any)
	if det["tree"] == nil || det["coverage"] == nil {
		t.Errorf("preview missing from details: %v", det)
	}
	// add --force writes anyway and says so.
	code, out, _ := e.run("add", page, "--force")
	if code != 0 || !strings.Contains(out, "written with --force") {
		t.Errorf("add --force: %d\n%s", code, out)
	}
}

func TestTreeSourceSlashFolder(t *testing.T) {
	e := newEnv(t)
	e.add()
	c1, _, a := e.envelope("tree", "sdk", "api")
	c2, _, b := e.envelope("tree", "sdk/api")
	if c1 != 0 || c2 != 0 || fmt.Sprint(a["data"]) != fmt.Sprint(b["data"]) {
		t.Fatalf("tree sdk api (%d) and tree sdk/api (%d) differ:\n%v\n%v", c1, c2, a, b)
	}
}
