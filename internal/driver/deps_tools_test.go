// Why: the package manager's surface, replace, vendoring, seeds, pkg.sum
// and manifest edits each had a hole that must stay closed.
// What: regression tests of what apidiff, doc and query find see,
// replace pins and cycles, vendoring in workspaces, tampered seeds,
// concurrent pkg.sum writers, CRLF manifests and dependency cycles.
// How: each test writes a small workspace (depChangeApp and others), runs
// the command and compares diagnostics and files.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// depChangeApp writes a workspace of app over util with Gone's parameters
// gone, so two calls give two versions where only util, a dependency,
// changes Gone's signature.
func depChangeApp(t *testing.T, gone string) string {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":             depManifest("app", pathDep("u", "../util")),
		"app/src/a.mk":           "import u.src\n\nexport A(): int {\n    return src.Twelve()\n}\n",
		"util/pkg.mk":            depManifest("util", pathDep("d", "../deep")),
		"util/src/t.mk":          "export Twelve(): int {\n    return 12\n}\n\nexport Gone(" + gone + "): int {\n    return 1\n}\n",
		"util/internal/tok/k.mk": "export Tok(): int {\n    return 1\n}\n",
		"deep/pkg.mk":            depManifest("deep"),
		"deep/src/d.mk":          "export Deep(): int {\n    return 1\n}\n",
	})
	return filepath.Join(ws, "app")
}

// mk apidiff and mk doc see the package's own surface only.
func TestQuery_DependencyItemsNotInAPIOrDoc(t *testing.T) {
	a, b := depChangeApp(t, ""), depChangeApp(t, "x: int")
	r, err := APIDiff(Config{Root: a, StdDir: fakeStd(t)}, Config{Root: b, StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitStatus() != 0 || strings.Contains(r.Text(), "Gone") {
		t.Errorf("apidiff over a dependency's change:\n%s", r.Text())
	}
	s, err := NewSession(Config{Root: a, StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	dp, _, err := s.Doc(DocOptions{})
	if err != nil || dp == nil {
		t.Fatal(err)
	}
	for _, m := range dp.Modules {
		if strings.Contains(m.Path, "util") || strings.Contains(m.Path, "deep") {
			t.Errorf("mk doc lists the dependency module %s", m.Path)
		}
	}
}

// mk query find answers only names the root can import: no dependency's
// internal module, no package the root does not name.
func TestQuery_FindOnlyWhatTheRootImports(t *testing.T) {
	s, err := NewSession(Config{Root: depChangeApp(t, ""), StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Tok", "Deep"} {
		if a, _, _ := s.Query("find", []string{w}); a != nil && strings.Contains(a.Text(), "."+strings.ToLower(w)) {
			t.Errorf("find %s:\n%s", w, a.Text())
		}
	}
	if a, _, _ := s.Query("find", []string{"Twelve"}); a == nil || !strings.Contains(a.Text(), "u.src") {
		t.Errorf("find Twelve lost the dependency item")
	}
}

// A replace of a source by itself at another version is a version pin, as
// Go's `replace x => x v1.1.0`, the chain's last step.
func TestReplace_SameSourcePin(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	x := gs.source("someone/x")
	gs.tag("someone/x", "1.0.0", map[string]string{"pkg.mk": depManifest("x"), "src/x.mk": "export X(): int {\n    return 1\n}\n"})
	gs.tag("someone/x", "1.1.0", map[string]string{"pkg.mk": depManifest("x"), "src/x.mk": "export X(): int {\n    return 1\n}\n\nexport Y(): int {\n    return 2\n}\n"})
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("x", x, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + x + "\", source: \"" + x + "\", version: \"1.1.0\")])\n",
		"app/a.mk": "import x.src\n\nexport A(): int {\n    return src.Y()\n}\n",
	})
	clean(t, depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, "")))
	// The pin is the chain's last step: a -> x, then x pinned.
	dw := &depWalk{replace: []Dep{{Name: x, Source: x, Version: "1.1.0"}, {Name: "example.com/x/a", Source: x, Version: "1.0.0"}}}
	if got, _ := dw.replaced(Dep{Name: "t", Source: "example.com/x/a", Version: "1.0.0"}); got.Source != x || got.Version != "1.1.0" {
		t.Errorf("chain then pin: %s@%s", got.Source, got.Version)
	}
}

// Replace entries that form a cycle are E0202 replace.
func TestReplace_CycleRefused(t *testing.T) {
	a, b := "example.com/x/a", "example.com/x/b"
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("t", a, "1.0.0") + "], replace: [" +
			`pkg.Dep(name: "` + a + `", source: "` + b + `", version: "1.0.0"), pkg.Dep(name: "` + b + `", source: "` + a + `", version: "1.0.0")])` + "\n",
		"app/a.mk": "export A(): int {\n    return 1\n}\n",
	})
	e0202(t, depCheck(t, filepath.Join(ws, "app"), Config{Offline: true}), "replace", "pkg.mk:")
}

// mk vendor in a workspace member does not vendor another member (the user's
// own code, read from its directory), so the next check is clean; mk vendor
// at a workspace root with no dependency of its own succeeds.
func TestVendor_WorkspaceMembers(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"pkg.mk":        "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"app\", \"util\"],\n)\n",
		"app/pkg.mk":    depManifest("app", pathDep("util", "../util")),
		"app/a.mk":      "import util.src\n\nexport A(): int {\n    return src.T()\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/t.mk": "export T(): int {\n    return 1\n}\n",
	})
	app := filepath.Join(ws, "app")
	for _, dir := range []string{app, ws} {
		s, err := NewSession(Config{Root: dir, StdDir: fakeStd(t), CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		res, err := s.Vendor()
		if err != nil || len(res.Result.Report.Diags) > 0 {
			t.Fatalf("mk vendor in %s: %v %q", dir, err, lines(res.Result))
		}
	}
	r := depCheck(t, app, Config{})
	clean(t, r)
	if len(r.Notes) != 0 {
		t.Errorf("notes %q", r.Notes)
	}
}

// A tampered seed whose line names the server's real commit is never read:
// neither its pkg.mk (by the version walk) nor its files (by the build);
// the install fetches and checks clean.
func TestPkgCmd_TamperedSeedContentNeverRead(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("10"))
	src := gs.source("someone/toml")
	seed := seedFor(t, "toml", src, "1.0.0", tomlFiles("10"))
	ents, _ := os.ReadDir(filepath.Join(seed, "pkg"))
	dir := filepath.Join(seed, "pkg", ents[0].Name())
	fp, _ := os.ReadFile(filepath.Join(seed, "first-party.txt"))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(seed, "first-party.txt"), []byte(strings.Replace(string(fp), strings.Repeat("ab", 20), gs.repos["someone/toml"].tags["v1.0.0"].String(), 1)), 0o644))
	must(os.WriteFile(filepath.Join(dir, "src", "toml.mk"), []byte("export W(): int {\n    return 1\n}\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "pkg.mk"), []byte(depManifest("toml", srcDep("zz", "example.invalid/a/b", "1.0.0"))), 0o644))
	app := writeTree(t, map[string]string{"pkg.mk": depManifest("app"), "a.mk": "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n"})
	res, err := cmdSession(t, sourceCfg(gs, t.TempDir()), app).Install("toml", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Check)
	clean(t, depCheck(t, app, sourceCfg(gs, t.TempDir())))
}

// Concurrent builds' pkg.sum lines merge: a line another build wrote after
// this one read pkg.sum is kept, as Go merges go.sum.
func TestSum_ConcurrentWritersMerge(t *testing.T) {
	dir := t.TempDir()
	ours := newPkgSum()
	ours.add(sumLine{Source: "example.com/a/b", Version: "1.0.0", Commit: strings.Repeat("1", 40), Hash: "tree-sha256:" + strings.Repeat("1", 64)})
	theirs := "example.com/c/d v2.0.0 " + strings.Repeat("2", 40) + " tree-sha256:" + strings.Repeat("2", 64) + "\n"
	if err := os.WriteFile(filepath.Join(dir, sumFile), []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	dw := &depWalk{wsRoot: dir, sum: ours, sumDirt: true, s: &Session{}}
	dw.saveSum()
	got := readFile(t, filepath.Join(dir, sumFile))
	if !strings.Contains(got, "example.com/a/b") || !strings.Contains(got, "example.com/c/d") {
		t.Errorf("pkg.sum %q: a line was lost", got)
	}
	if _, err := os.Stat(filepath.Join(dir, sumFile+".lock")); err == nil {
		t.Error("the lock file stayed")
	}
}

// An edit of a manifest not in mk fmt form keeps its CRLF line ends (every
// byte not edited stays).
func TestPkgCmd_EditKeepsCRLF(t *testing.T) {
	text := "const Package = pkg.Package(\r\n    name:   \"app\",\r\n    deps: [],\r\n)\r\n"
	app := writeTree(t, map[string]string{"pkg.mk": text})
	e, err := openManifest(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.add("deps", `pkg.Dep(name: "u", path: "../u")`); err != nil {
		t.Fatal(err)
	}
	got, err := e.finish(Config{StdDir: fakeStd(t)}, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(text, "deps: [],", "deps: [pkg.Dep(name: \"u\", path: \"../u\")],", 1); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// snapshotFiles never deletes a file it could not read.
func TestPkgCmd_SnapshotKeepsUnreadable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub") // a directory: reading it fails, and it exists
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotFiles(p)()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("restore removed what it could not read: %v", err)
	}
}

// A unit's dependency cycle is E0202 cycle, as the build's walk refuses it
// (deps_walk.go).
func TestCorpusUnit_DependencyCycle(t *testing.T) {
	c, err := NewCorpusSession(Config{StdDir: fakeStd(t)})
	if err != nil {
		t.Fatal(err)
	}
	out := c.CheckUnit(UnitInput{Name: "cyc", Files: []UnitFile{
		{"main.mk", "import a.src\n\nexport M(): int {\n    return src.A()\n}\n"},
		{"pkg.mk", "const Package = pkg.Package(\n    name: \"codes\",\n    deps: [pkg.Dep(name: \"a\", path: \"a\")],\n)\n"},
		{"a/pkg.mk", "const Package = pkg.Package(\n    name: \"a\",\n    deps: [pkg.Dep(name: \"b\", path: \"../b\")],\n)\n"},
		{"a/src/a.mk", "export A(): int {\n    return 1\n}\n"},
		{"b/pkg.mk", "const Package = pkg.Package(\n    name: \"b\",\n    deps: [pkg.Dep(name: \"a\", path: \"../a\")],\n)\n"},
		{"b/src/b.mk", "export B(): int {\n    return 1\n}\n"},
	}})
	found := false
	for _, d := range out.Report.Diags {
		if r, _ := d.Diag.FactValue("reason"); d.Diag.Code.String() == "E0202" && r.Str == "cycle" {
			found = true
		}
	}
	if !found {
		t.Errorf("no E0202 cycle:\n%s", out.Report.Text(out.Sources))
	}
}

// A member's replace is noted once.
func TestWorkspace_MemberReplaceNotedOnce(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"pkg.mk":        "const Package = pkg.Package(\n    name: \"ws\",\n    members: [\"app\", \"util\"],\n)\n",
		"app/pkg.mk":    depManifest("app", pathDep("u", "../util")),
		"app/a.mk":      "import u.src\n\nexport A(): int {\n    return src.U()\n}\n",
		"util/pkg.mk":   "const Package = pkg.Package(\n    name: \"util\",\n    replace: [pkg.Dep(name: \"example.com/a/b\", path: \"../deep\")],\n)\n",
		"util/src/u.mk": "export U(): int {\n    return 1\n}\n",
		"deep/pkg.mk":   depManifest("deep"),
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	clean(t, r)
	if n := strings.Count(strings.Join(r.Notes, "\n"), "replace"); n != 1 {
		t.Errorf("notes %q: replace named %d times", r.Notes, n)
	}
}
