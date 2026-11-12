package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morethancoder/docli/internal/store"
)

// These tests run docli without --path, from a temp project folder, with
// $DOCLI_HOME (set by newEnv) as the global store. The real ~/.docli is never
// touched.

// project makes an empty project folder and moves into it.
func (e *env) project() string {
	e.t.Helper()
	dir := e.t.TempDir()
	e.t.Chdir(dir)
	return dir
}

// json runs a command without --path and decodes the envelope.
func (e *env) json(args ...string) (int, map[string]any) {
	e.t.Helper()
	code, out, stderr := e.exec(append(args, "--json")...)
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		e.t.Fatalf("%v: output is not JSON: %v\n%s\nstderr: %s", args, err, out, stderr)
	}
	return code, raw
}

func (e *env) addGlobal(name string) {
	e.t.Helper()
	code, raw := e.json("add", e.srv.URL+"/llms.md", "--name", name, "--file-level", "3", "--global")
	if code != 0 || data(raw)["scope"] != scopeGlobal {
		e.t.Fatalf("add --global: %v", raw)
	}
}

func list(raw map[string]any) []map[string]any {
	var out []map[string]any
	for _, x := range raw["data"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func errCode(raw map[string]any) string {
	if e, ok := raw["error"].(map[string]any); ok {
		return e["code"].(string)
	}
	return ""
}

func TestGlobalAddThenReadFromAnotherFolder(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("sdk")
	if _, err := os.Stat(filepath.Join(e.global, "sdk", "api", "createWidget.md")); err != nil {
		t.Fatalf("--global add did not write to $DOCLI_HOME: %v", err)
	}
	if _, err := os.Stat(filepath.Join(".docli")); err == nil {
		t.Error("--global add also wrote ./.docli")
	}

	e.project() // a different project with no docs of its own
	code, raw := e.json("show", "createWidget")
	if code != 0 || data(raw)["scope"] != scopeGlobal || !strings.HasPrefix(data(raw)["file"].(string), e.global) {
		t.Fatalf("fallback show: %v", raw)
	}
	code, raw = e.json("show", "sdk/api/createWidget")
	if code != 0 || data(raw)["path"] != "api/createWidget.md" {
		t.Errorf("show with a global source prefix: %v", raw)
	}
	code, raw = e.json("search", "createWidget")
	if code != 0 || len(list(raw)) == 0 || list(raw)[0]["scope"] != scopeGlobal {
		t.Errorf("fallback search: %v", raw)
	}
	for _, args := range [][]string{{"tree", "sdk"}, {"diff", "sdk"}, {"changelog", "sdk"}, {"check", "sdk"}} {
		if code, raw := e.json(args...); code != 0 {
			t.Errorf("%v on a global source: %v", args, raw)
		}
	}
	code, raw = e.json("list")
	if l := list(raw); code != 0 || len(l) != 1 || l[0]["scope"] != scopeGlobal {
		t.Errorf("list: %v", raw)
	}
	_, out, _ := e.exec("list")
	if !strings.Contains(out, "sdk (global)") {
		t.Errorf("human list has no global tag:\n%s", out)
	}
}

func TestProjectCopyShadowsGlobal(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("sdk")
	e.addGlobal("gsdk")
	e.docs.set(strings.Replace(e.docs.body, "Creates a widget", "Creates a shiny widget", 1), 0)
	if code, raw := e.json("add", e.srv.URL+"/llms.md", "--name", "sdk", "--file-level", "3"); code != 0 || data(raw)["scope"] != scopeProject {
		t.Fatalf("project add: %v", raw)
	}

	code, raw := e.json("show", "sdk/api/createWidget")
	if code != 0 || data(raw)["scope"] != scopeProject || !strings.Contains(data(raw)["content"].(string), "shiny") {
		t.Errorf("a name in both stores must resolve to the project copy: %v", raw)
	}

	code, raw = e.json("list")
	l := list(raw)
	if code != 0 || len(l) != 3 {
		t.Fatalf("list: %v", raw)
	}
	var shadowed int
	for _, s := range l {
		if s["shadowed"] == true {
			shadowed++
			if s["name"] != "sdk" || s["scope"] != scopeGlobal {
				t.Errorf("wrong copy marked shadowed: %v", s)
			}
		}
	}
	if shadowed != 1 {
		t.Errorf("want one shadowed copy: %v", l)
	}

	// Project hits first, then global ones; the shadowed global sdk is skipped.
	code, raw = e.json("search", "createWidget", "--limit", "0")
	hits := list(raw)
	if code != 0 || len(hits) < 2 {
		t.Fatalf("search: %v", raw)
	}
	seenGlobal := false
	for _, h := range hits {
		switch {
		case h["scope"] == scopeGlobal:
			seenGlobal = true
			if h["source"] != "gsdk" {
				t.Errorf("hit from a shadowed global source: %v", h)
			}
		case seenGlobal:
			t.Errorf("project hit after a global one: %v", hits)
		}
	}
	if !seenGlobal {
		t.Errorf("no global hits: %v", hits)
	}
	_, raw = e.json("search", "createWidget", "--limit", "1")
	if h := list(raw); len(h) != 1 || h[0]["scope"] != scopeProject {
		t.Errorf("--limit applies to the combined list, project first: %v", h)
	}
}

func TestGlobalWithPathRejected(t *testing.T) {
	e := newEnv(t)
	code, raw := e.json("list", "--global", "--path", e.root)
	if code != 1 || errCode(raw) != "usage" {
		t.Errorf("--global with --path: %v", raw)
	}
}

func TestEmptyNamesBothStores(t *testing.T) {
	e := newEnv(t)
	e.project()
	code, raw := e.json("show", "anything")
	if code != 1 || errCode(raw) != "empty" {
		t.Fatalf("empty: %v", raw)
	}
	msg := raw["error"].(map[string]any)["message"].(string)
	if !strings.Contains(msg, ".docli") || !strings.Contains(msg, e.global) ||
		!strings.Contains(msg, "docli add <url>") || !strings.Contains(msg, "docli add --global <url>") {
		t.Errorf("empty message: %s", msg)
	}
}

func TestWritesStayInProjectWithoutGlobal(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("sdk")

	code, raw := e.json("remove", "sdk")
	if code != 1 || errCode(raw) != "not_found" || !strings.Contains(raw["error"].(map[string]any)["message"].(string), "--global") {
		t.Errorf("remove of a global-only source without --global: %v", raw)
	}
	code, raw = e.json("sync", "sdk")
	if code != 1 || errCode(raw) != "not_found" {
		t.Errorf("sync of a global-only source without --global: %v", raw)
	}
	if !store.Open(e.global).Has("sdk") {
		t.Fatal("global source was removed")
	}

	if code, raw := e.json("add", e.srv.URL+"/llms.md", "--name", "local", "--file-level", "3"); code != 0 {
		t.Fatalf("project add: %v", raw)
	}
	if store.Open(e.global).Has("local") {
		t.Error("a project add wrote to the global store")
	}

	if code, raw := e.json("remove", "sdk", "--global"); code != 0 {
		t.Errorf("remove --global: %v", raw)
	}
	if store.Open(e.global).Has("sdk") {
		t.Error("remove --global left the source")
	}
}

func TestSyncProjectOnlyNotesStaleGlobal(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("gsdk")
	if code, raw := e.json("add", e.srv.URL+"/llms.md", "--name", "local", "--file-level", "3"); code != 0 {
		t.Fatalf("project add: %v", raw)
	}
	g := store.Open(e.global)
	if err := g.SaveState("gsdk", &store.State{CheckedAt: time.Now(), Stale: true}); err != nil {
		t.Fatal(err)
	}
	e.docs.set(strings.Replace(e.docs.body, "Creates a widget", "Creates a shiny widget", 1), 0)

	code, raw := e.json("sync")
	if code != 0 {
		t.Fatalf("sync: %v", raw)
	}
	if l := list(raw); len(l) != 1 || l[0]["source"] != "local" || l[0]["version"] != 2.0 {
		t.Errorf("sync should cover the project only: %v", l)
	}
	notes, _ := raw["stale"].([]any)
	found := false
	for _, n := range notes {
		n := n.(map[string]any)
		if n["source"] == "gsdk" && n["scope"] == scopeGlobal && n["status"] == "stale" &&
			strings.Contains(n["message"].(string), "docli sync --global gsdk") {
			found = true
		}
	}
	if !found {
		t.Errorf("no note for the stale global source: %v", raw["stale"])
	}
	if m, _ := g.Manifest("gsdk"); m.Version != 1 {
		t.Errorf("sync without --global changed the global source to v%d", m.Version)
	}

	_, _, stderr := e.exec("sync")
	if !strings.Contains(stderr, "gsdk (global)") || !strings.Contains(stderr, "docli sync --global gsdk") {
		t.Errorf("human sync note:\n%s", stderr)
	}
}

func TestGlobalStaleCheck(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("sdk")
	g := store.Open(e.global)
	expire := func() {
		if err := g.SaveState("sdk", &store.State{}); err != nil {
			t.Fatal(err)
		}
	}

	// A slow check moves to the background, pointed at the global store.
	expire()
	e.docs.set(e.docs.body, 10*time.Second)
	code, raw := e.json("show", "createWidget")
	if code != 0 {
		t.Fatalf("show: %v", raw)
	}
	if len(e.spawnRoots) != 1 || e.spawnRoots[0] != e.global || e.spawned[0][0] != "sdk" {
		t.Errorf("background check: roots %v names %v, want %s", e.spawnRoots, e.spawned, e.global)
	}
	s, _ := raw["stale"].([]any)
	if len(s) != 1 || s[0].(map[string]any)["status"] != "pending" || s[0].(map[string]any)["scope"] != scopeGlobal {
		t.Errorf("pending note: %v", raw["stale"])
	}

	// A fast check finds the change and says how to sync the global copy.
	expire()
	e.docs.set(strings.Replace(e.docs.body, "Creates a widget", "Creates a shiny widget", 1), 0)
	_, raw = e.json("show", "createWidget")
	s, _ = raw["stale"].([]any)
	if len(s) != 1 || s[0].(map[string]any)["status"] != "stale" ||
		!strings.Contains(s[0].(map[string]any)["message"].(string), "docli sync --global sdk") {
		t.Errorf("stale note: %v", raw["stale"])
	}

	// --latest syncs the global copy before answering.
	code, raw = e.json("show", "createWidget", "--latest")
	if code != 0 || !strings.Contains(data(raw)["content"].(string), "shiny") {
		t.Fatalf("--latest on a global source: %v", raw)
	}
	if m, _ := g.Manifest("sdk"); m.Version != 2 {
		t.Errorf("global source at v%d after --latest, want 2", m.Version)
	}
}

func TestSkillGlobal(t *testing.T) {
	e := newEnv(t)
	e.project()
	home := t.TempDir()
	t.Setenv("HOME", home)
	code, raw := e.json("skill", "--global")
	if code != 0 || data(raw)["path"] != filepath.Join(home, ".claude", "skills", "docli", "SKILL.md") {
		t.Fatalf("skill --global: %v", raw)
	}
	b, _ := os.ReadFile(data(raw)["path"].(string))
	if !strings.Contains(string(b), "## Two stores") {
		t.Error("skill text does not explain the two stores")
	}
}

func (e *env) addLocal(name string) {
	e.t.Helper()
	if code, raw := e.json("add", e.srv.URL+"/llms.md", "--name", name, "--file-level", "3"); code != 0 {
		e.t.Fatalf("project add: %v", raw)
	}
}

// A command that needs one source picks the project's when no name is given,
// and a global one only when the project has none.
func TestOneSourcePrefersProject(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("gsdk")
	treeOf := func(raw map[string]any) any {
		if n, _ := raw["stale"].([]any); len(n) == 1 {
			return n[0].(map[string]any)["source"]
		}
		return nil
	}
	code, raw := e.json("tree")
	if code != 0 || treeOf(raw) != "gsdk" {
		t.Errorf("tree with only a global source: %v", raw)
	}
	e.addLocal("local")
	for _, cmd := range []string{"tree", "diff", "changelog"} {
		if code, raw := e.json(cmd); code != 0 {
			t.Errorf("%s with one project and one global source: %v", cmd, raw)
		}
	}
	if _, raw := e.json("tree"); treeOf(raw) != "local" {
		t.Errorf("tree picked %v, want the project's local", treeOf(raw))
	}
}

// --latest over many sources syncs the project's only; a global source is
// synced when it is the one the answer comes from.
func TestLatestWritesGlobalOnlyForItsAnswer(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("gsdk")
	e.addLocal("local")
	g := store.Open(e.global)
	e.docs.set(strings.Replace(e.docs.body, "Creates a widget", "Creates a shiny widget", 1), 0)

	if code, raw := e.json("list", "--latest"); code != 0 {
		t.Fatalf("list --latest: %v", raw)
	}
	if m, _ := g.Manifest("gsdk"); m.Version != 1 {
		t.Errorf("list --latest synced the global source to v%d", m.Version)
	}
	if m, _ := store.Open(".docli").Manifest("local"); m.Version != 2 {
		t.Errorf("list --latest left the project source at v%d", m.Version)
	}

	code, raw := e.json("show", "gsdk/api/createWidget", "--latest")
	if code != 0 || !strings.Contains(data(raw)["content"].(string), "shiny") {
		t.Fatalf("show of a global section with --latest: %v", raw)
	}
	if m, _ := g.Manifest("gsdk"); m.Version != 2 {
		t.Errorf("show --latest of the global answer left it at v%d", m.Version)
	}
}

// show --latest with no source name: when the answer is in a global source,
// that copy is synced before answering.
func TestShowLatestSyncsGlobalAnswer(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("gsdk")
	// A project source from its own file, without the section.
	other := filepath.Join(t.TempDir(), "other.md")
	if err := os.WriteFile(other, []byte("# Other\n\n## Thing\n\nNothing about widgets here.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, raw := e.json("add", other, "--name", "local"); code != 0 {
		t.Fatalf("project add: %v", raw)
	}
	e.docs.set(strings.Replace(e.docs.body, "Creates a widget", "Creates a shiny widget", 1), 0)

	code, raw := e.json("show", "createWidget", "--latest")
	if code != 0 || data(raw)["scope"] != scopeGlobal || !strings.Contains(data(raw)["content"].(string), "shiny") {
		t.Fatalf("show --latest answered from global: %v", raw)
	}
}

// Late checks in both stores start one background check per store.
func TestBackgroundCheckPerStore(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("gsdk")
	e.addLocal("local")
	for _, st := range []*store.Store{store.Open(e.global), store.Open(".docli")} {
		names, _ := st.Sources()
		if err := st.SaveState(names[0], &store.State{}); err != nil {
			t.Fatal(err)
		}
	}
	e.docs.set(e.docs.body, 10*time.Second)
	if code, raw := e.json("list"); code != 0 {
		t.Fatalf("list: %v", raw)
	}
	wd, _ := os.Getwd()
	want := map[string]string{filepath.Join(wd, ".docli"): "local", e.global: "gsdk"}
	if len(e.spawnRoots) != 2 {
		t.Fatalf("want two background checks, got roots %v", e.spawnRoots)
	}
	for i, root := range e.spawnRoots {
		if w, ok := want[root]; !ok || len(e.spawned[i]) != 1 || e.spawned[i][0] != w {
			t.Errorf("check %d: root %s names %v", i, root, e.spawned[i])
		}
	}
}

// Running in the global store's own parent folder (here through a symlink)
// opens one store, as global.
func TestHomeFolderIsOneStore(t *testing.T) {
	e := newEnv(t)
	real := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(filepath.Join(real, ".docli"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCLI_HOME", filepath.Join(real, ".docli"))
	t.Chdir(link)
	if code, raw := e.json("add", e.srv.URL+"/llms.md", "--name", "sdk", "--file-level", "3"); code != 0 || data(raw)["scope"] != scopeGlobal {
		t.Errorf("add in the home folder should be global: %v", raw)
	}
	if _, raw := e.json("list"); len(list(raw)) != 1 {
		t.Errorf("one store, one entry: %v", raw)
	}
}

func TestDocliHomeMustBeAbsolute(t *testing.T) {
	e := newEnv(t)
	e.project()
	t.Setenv("DOCLI_HOME", "rel/docs")
	if code, raw := e.json("list"); code != 1 || errCode(raw) != "usage" {
		t.Errorf("relative DOCLI_HOME: %v", raw)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOCLI_HOME", "~/shared-docs")
	e.addGlobal("sdk")
	if !store.Open(filepath.Join(home, "shared-docs")).Has("sdk") {
		t.Error("~ in DOCLI_HOME was not expanded")
	}
}

// sync names stale global sources, but not one the project shadows.
func TestSyncNoteSkipsShadowed(t *testing.T) {
	e := newEnv(t)
	e.project()
	e.addGlobal("sdk")
	e.addLocal("sdk")
	if err := store.Open(e.global).SaveState("sdk", &store.State{CheckedAt: time.Now(), Stale: true}); err != nil {
		t.Fatal(err)
	}
	_, raw := e.json("sync")
	for _, n := range raw["stale"].([]any) {
		if n.(map[string]any)["scope"] == scopeGlobal {
			t.Errorf("note for a shadowed global source: %v", n)
		}
	}
}
