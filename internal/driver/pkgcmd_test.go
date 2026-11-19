// Why: mk new, install, remove, update, tree and why change a package's
// manifest and lock files, and they must do it without a real registry.
// What: TestPkgCmd_EditKeepsComments, TestPkgCmd_New,
// TestPkgCmd_InstallTreeWhyUpdateRemove, TestPkgCmd_InstallFromSeed and
// their helpers.
// How: serves packages from a synthetic git server, runs each command, and
// checks that a manifest edit changes only the edited list.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/fetch"
)

// commented is a formatted manifest full of comments, with one Dep.
const commented = `// The app's manifest: every comment here must survive an edit.
const Package = pkg.Package(
    name: "app", // the package
    // its dependencies
    deps: [
        pkg.Dep(name: "util", path: "../util"), // a path package
    ],
    // no unsafe code
    unsafe_modules: [],
) // end
`

func cmdSession(t *testing.T, cfg Config, root string) *Session {
	t.Helper()
	cfg.Root = root
	if cfg.StdDir == "" {
		cfg.StdDir = fakeStd(t)
	}
	s, err := NewSession(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPkgCmd_EditKeepsComments(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    commented,
		"app/a.mk":      "export A(): int {\n    return 1\n}\n",
		"util/pkg.mk":   depManifest("util"),
		"util/src/u.mk": "export U(): int {\n    return 1\n}\n",
	})
	app := filepath.Join(ws, "app")
	cfg := Config{StdDir: fakeStd(t)}
	e, err := openManifest(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.add("deps", `pkg.Dep(name: "more", path: "../util")`); err != nil {
		t.Fatal(err)
	}
	if err := e.add("test_deps", `pkg.Dep(name: "golden", path: "../util")`); err != nil {
		t.Fatal(err)
	}
	got, err := e.finish(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(commented, "// a path package\n    ],",
		"// a path package\n        pkg.Dep(name: \"more\", path: \"../util\"),\n    ],", 1)
	want = strings.Replace(want, "    unsafe_modules: [],\n", "    unsafe_modules: [],\n    test_deps: [pkg.Dep(name: \"golden\", path: \"../util\")],\n", 1)
	if got != want {
		t.Fatalf("after adding:\n%s\nwant:\n%s", got, want)
	}
	// Removing both gives the manifest back byte for byte, but for the
	// empty test_deps list left behind.
	for _, n := range []string{"more", "golden"} {
		e, err := openManifest(app)
		if err != nil {
			t.Fatal(err)
		}
		_, al, i, err := e.depElem(n)
		if err != nil || al == nil {
			t.Fatalf("%s: %v", n, err)
		}
		if err := e.remove(al, i); err != nil {
			t.Fatal(err)
		}
		if got, err = e.finish(cfg, false); err != nil {
			t.Fatal(err)
		}
	}
	back := strings.Replace(commented, "    unsafe_modules: [],\n", "    unsafe_modules: [],\n    test_deps: [],\n", 1)
	if got != back {
		t.Fatalf("after removing:\n%s\nwant:\n%s", got, back)
	}
	// A Package that is no constructor call, or a list that is no array
	// literal, is refused with the hand edit.
	for _, bad := range []string{"const Package = base\n", "const Package = pkg.Package(name: \"app\", deps: more)\n"} {
		if err := os.WriteFile(filepath.Join(app, "pkg.mk"), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		e, err := openManifest(app)
		if err == nil {
			err = e.add("deps", `pkg.Dep(name: "x", path: "../util")`)
		}
		if err == nil || !strings.Contains(err.Error(), "by hand") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestPkgCmd_New(t *testing.T) {
	dir := t.TempDir()
	root, err := NewPackage(dir, "hello", MinorOf(Release))
	if err != nil {
		t.Fatal(err)
	}
	man, _ := os.ReadFile(filepath.Join(root, "pkg.mk"))
	if !strings.Contains(string(man), `mk: "0.1"`) || !strings.Contains(string(man), `pkg.Bin(name: "hello"`) {
		t.Errorf("pkg.mk:\n%s", man)
	}
	clean(t, check(t, Config{Root: root, CacheDir: t.TempDir()}))
	if _, err := NewPackage(dir, "hello", "0.1"); err == nil {
		t.Error("mk new over an existing directory")
	}
	if _, err := NewPackage(dir, "no-good", "0.1"); err == nil {
		t.Error("mk new with a name that is no identifier")
	}
}

// The package commands on one recorded workspace: install picks the
// newest tag within @1, a used name is refused naming --name, tree and why
// show the requirement, update raises a pinned version, remove drops the
// Dep and its pkg.sum lines.
func TestPkgCmd_InstallTreeWhyUpdateRemove(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("10"))
	gs.tag("someone/toml", "1.1.0", tomlFiles("11"))
	gs.tag("someone/toml", "2.0.0", tomlFiles("20"))
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk":    commented,
		"app/a.mk":      "export A(): int {\n    return 1\n}\n",
		"util/pkg.mk":   "const Package = pkg.Package(\n    name: \"util\",\n    deps: [" + srcDep("toml", src, "1.0.0") + "],\n)\n",
		"util/src/u.mk": "export U(): int {\n    return 1\n}\n",
	})
	app := filepath.Join(ws, "app")
	cfg := sourceCfg(gs, t.TempDir())
	res, err := cmdSession(t, cfg, app).Install(src+"@1", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Check)
	if len(res.Lines) != 1 || res.Lines[0] != "added toml ("+src+" v1.1.0)" {
		t.Errorf("install: %q", res.Lines)
	}
	sum, _ := os.ReadFile(filepath.Join(app, sumFile))
	if !strings.Contains(string(sum), src+" v1.1.0 ") {
		t.Errorf("pkg.sum %q", sum)
	}
	if _, err := cmdSession(t, cfg, app).Install(src, PkgOptions{}); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Errorf("a used name: %v", err)
	}
	tree, err := cmdSession(t, cfg, app).Tree()
	if err != nil {
		t.Fatal(err)
	}
	wantTree := []string{"app", "  util (../util)", "    toml (" + src + " v1.1.0), wants v1.0.0", "  toml (" + src + " v1.1.0) (above)"}
	if strings.Join(tree.Lines, "\n") != strings.Join(wantTree, "\n") {
		t.Errorf("tree:\n%s\nwant:\n%s", strings.Join(tree.Lines, "\n"), strings.Join(wantTree, "\n"))
	}
	why, err := cmdSession(t, cfg, app).Why(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(why.Lines) != 3 || !strings.Contains(why.Lines[0], "app -> util (../util) -> toml") ||
		why.Lines[1] != "app -> toml ("+src+" v1.1.0)" || !strings.HasPrefix(why.Lines[2], "v1.1.0 is selected by the minimum in app") {
		t.Errorf("why: %q", why.Lines)
	}
	// Pin to 1.0.0 by hand, then update: back to 1.1.0, never 2.0.0.
	man, _ := os.ReadFile(filepath.Join(app, "pkg.mk"))
	pinned := strings.Replace(string(man), `version: "1.1.0"`, `version: "1.0.0"`, 1)
	if err := os.WriteFile(filepath.Join(app, "pkg.mk"), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	up, err := cmdSession(t, cfg, app).Update("", PkgOptions{DryRun: true})
	if err != nil || len(up.Lines) != 1 || up.Lines[0] != "toml ("+src+"): v1.0.0 -> v1.1.0" {
		t.Errorf("update --dry-run: %q %v", up.Lines, err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "pkg.mk")); string(b) != pinned {
		t.Error("update --dry-run wrote pkg.mk")
	}
	up, err = cmdSession(t, cfg, app).Update("toml", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "pkg.mk")); string(b) != string(man) {
		t.Errorf("update wrote:\n%s\nwant:\n%s", b, man)
	}
	// remove: the Dep goes; util still needs toml, so its lines stay.
	rm, err := cmdSession(t, cfg, app).Remove("toml")
	if err != nil {
		t.Fatal(err)
	}
	clean(t, rm.Check)
	if b, _ := os.ReadFile(filepath.Join(app, "pkg.mk")); string(b) != commented {
		t.Errorf("remove left:\n%s", b)
	}
	sum2, _ := os.ReadFile(filepath.Join(app, sumFile))
	if !strings.Contains(string(sum2), src+" v1.0.0 ") || strings.Contains(string(sum2), src+" v1.1.0 ") {
		t.Errorf("pkg.sum after remove %q (lines %q)", sum2, rm.Lines)
	}
}

// mk install <short name> reads the seed: the Dep and its pkg.sum line,
// and the tree from <seed>/pkg/<hash>, with no fetch at all.
func TestPkgCmd_InstallFromSeed(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("10"))
	src := gs.source("someone/toml")
	tr := &fetch.Tree{}
	for p, d := range tomlFiles("10") {
		tr.Files = append(tr.Files, fetch.File{Path: p, Data: []byte(d)})
	}
	h := tr.Hash()
	seed := t.TempDir()
	if err := tr.Write(filepath.Join(seed, "pkg", fmt.Sprintf("%x", h[:]))); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("# first-party\ntoml %s v1.0.0 %s %s\n", src, strings.Repeat("ab", 20), h)
	if err := os.WriteFile(filepath.Join(seed, "first-party.txt"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	old := seedDir
	seedDir = func() string { return seed }
	t.Cleanup(func() { seedDir = old })
	app := writeTree(t, map[string]string{"pkg.mk": depManifest("app"), "a.mk": "export A(): int {\n    return 1\n}\n"})
	n := gs.fetches
	cfg := sourceCfg(gs, t.TempDir())
	cfg.Offline = true
	res, err := cmdSession(t, cfg, app).Install("toml", PkgOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clean(t, res.Check)
	if gs.fetches != n {
		t.Errorf("an install from the seed fetched %d times", gs.fetches-n)
	}
	sum, _ := os.ReadFile(filepath.Join(app, sumFile))
	if !strings.Contains(string(sum), src+" v1.0.0 "+strings.Repeat("ab", 20)+" "+h.String()) {
		t.Errorf("pkg.sum %q", sum)
	}
}
