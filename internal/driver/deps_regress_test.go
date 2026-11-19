// Why: workspaces, replace, vendoring, pkg.sum and grants each had a hole
// that let the wrong package in, or failed where they should go on.
// What: regression tests of ancestor workspaces, transitive replace,
// the old vendor layout, path dependencies of fetched packages, an
// unwritable pkg.sum, the root module's name and credentials in URLs.
// How: each test writes manifests and sources (and a git server where it
// fetches), builds or checks, and compares the diagnostics and files.

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An ancestor pkg.mk whose text mentions "members" but whose members
// argument, read syntactically, does not name the package is never loaded,
// so its errors never reach the child; one that lists other members is
// ignored with a note. One that names the package is the workspace, and its
// errors are the build's.
func TestWorkspace_AncestorNotNamingPackageIgnored(t *testing.T) {
	child := map[string]string{
		"app/pkg.mk":  "const Package = pkg.Package(\n    name: \"app\",\n)\n",
		"app/main.mk": "export A(): int {\n    return 1\n}\n",
	}
	with := func(rootMk string) string {
		files := map[string]string{"pkg.mk": rootMk}
		for k, v := range child {
			files[k] = v
		}
		return filepath.Join(writeTree(t, files), "app")
	}
	// A comment mentions members; the manifest does not load (an unknown
	// field), and a newer mk minimum.
	clean(t, depCheck(t, with("// an outer project; members: none\nconst Package = pkg.Package(\n    name: \"outer\",\n    nonsense: 1,\n)\n"), Config{}))
	clean(t, depCheck(t, with("// members\nconst Package = pkg.Package(\n    name: \"outer\",\n    mk: \"9.0\",\n)\n"), Config{}))
	// A members list naming others only: ignored, with a note.
	r := depCheck(t, with("const Package = pkg.Package(\n    name: \"outer\",\n    members: [\"other\"],\n    nonsense: 1,\n)\n"), Config{})
	clean(t, r)
	if !strings.Contains(strings.Join(r.Notes, "\n"), "does not list this package") {
		t.Errorf("notes %q, want the ignored workspace's note", r.Notes)
	}
	// One naming app is the workspace: its error stops the build.
	r = depCheck(t, with("const Package = pkg.Package(\n    name: \"outer\",\n    members: [\"./app\"],\n    nonsense: 1,\n)\n"), Config{})
	if len(r.Report.Diags) == 0 {
		t.Errorf("a workspace root naming the package that fails to load: no diagnostic")
	}
}

// A root replace applies inside the version walk, to a fetched package's
// requirements too, and the replaced original is never fetched, built or
// recorded.
// A fetched package's requirement on a source the root replaces by a path:
// the replace applies before the walk, so the original source is never
// read (it is not even on the server here).
func TestReplace_TransitiveByPath(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	x := gs.source("someone/x")
	gs.tag("someone/p", "1.0.0", map[string]string{
		"pkg.mk":   depManifest("p", srcDep("x", x, "1.0.0")),
		"src/p.mk": "import x.src\n\nexport P(): int {\n    return src.X()\n}\n",
	})
	p := gs.source("someone/p")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("p", p, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + x + "\", path: \"../xfork\")])\n",
		"app/a.mk":       "import p.src\n\nexport A(): int {\n    return src.P()\n}\n",
		"xfork/pkg.mk":   depManifest("x"),
		"xfork/src/x.mk": "export X(): int {\n    return 7\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, ""))
	t.Logf("diagnostics:\n%s", r.Report.Text(r.Sources))
	clean(t, r)
}

// The same with a replacement by another source and version.
func TestReplace_TransitiveBySource(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	x := gs.source("someone/x")
	y := gs.source("other/y")
	gs.tag("other/y", "2.0.0", map[string]string{
		"pkg.mk":   depManifest("y"),
		"src/x.mk": "export X(): int {\n    return 9\n}\n",
	})
	gs.tag("someone/p", "1.0.0", map[string]string{
		"pkg.mk":   depManifest("p", srcDep("x", x, "1.0.0")),
		"src/p.mk": "import x.src\n\nexport P(): int {\n    return src.X()\n}\n",
	})
	p := gs.source("someone/p")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("p", p, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + x + "\", source: \"" + y + "\", version: \"2.0.0\")])\n",
		"app/a.mk": "import p.src\n\nexport A(): int {\n    return src.P()\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, ""))
	t.Logf("diagnostics:\n%s", r.Report.Text(r.Sources))
	clean(t, r)
}

// With the original on the server: the build is clean, but the replaced
// original is fetched and built (its modules are in the build), and p uses
// the fork.
func TestReplace_TransitiveOriginalNeverFetched(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	x := gs.source("someone/x")
	gs.tag("someone/x", "1.0.0", map[string]string{"pkg.mk": depManifest("x"), "src/x.mk": "export X(): int {\n    return 1\n}\n"})
	gs.tag("someone/p", "1.0.0", map[string]string{
		"pkg.mk":   depManifest("p", srcDep("x", x, "1.0.0")),
		"src/p.mk": "import x.src\n\nexport P(): int {\n    return src.X()\n}\n",
	})
	p := gs.source("someone/p")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("p", p, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + x + "\", path: \"../xfork\")])\n",
		"app/a.mk":       "import p.src\n\nexport A(): int {\n    return src.P()\n}\n",
		"xfork/pkg.mk":   depManifest("x"),
		"xfork/src/x.mk": "export X(): int {\n    return 7\n}\n",
	})
	app := filepath.Join(ws, "app")
	r := depCheck(t, app, sourceCfg(gs, ""))
	clean(t, r)
	_, xcid := sourceID(x, semver{Major: 1})
	t.Logf("fetch commands %d (filtered %d); modules %v", gs.fetches, gs.filtered, r.Trace.ModulesResolved)
	for _, m := range r.Trace.ModulesResolved {
		if strings.HasPrefix(m, xcid) {
			t.Errorf("the replaced original %s is in the build: %s", x, m)
		}
	}
	sum, _ := os.ReadFile(filepath.Join(app, sumFile))
	t.Logf("pkg.sum:\n%s", sum)
	if strings.Contains(string(sum), x+" ") {
		t.Errorf("pkg.sum records the replaced original %s", x)
	}
}

// In the old vendor layout, two Deps of one name with two path identities
// both reach vendor/<name>; they are one package, never an internal error
// for a file read twice.
func TestVendor_OldLayoutTwoIdentitiesOnePackage(t *testing.T) {
	units := "export U(): int {\n    return 1\n}\n"
	aSrc := "import units\n\nexport A(): int {\n    return units.U()\n}\n"
	aMan := depManifest("a", pathDep("units", "../units2"))
	ws := writeTree(t, map[string]string{
		"a/pkg.mk":                  aMan,
		"a/a.mk":                    aSrc,
		"units/pkg.mk":              depManifest("units"),
		"units/units.mk":            units,
		"units2/pkg.mk":             depManifest("units"),
		"units2/units.mk":           units,
		"app/pkg.mk":                depManifest("app", pathDep("a", "../a"), pathDep("units", "../units")),
		"app/main.mk":               "import a\nimport units\n\nexport F(): int {\n    return a.A() + units.U()\n}\n",
		"app/vendor/a/pkg.mk":       aMan,
		"app/vendor/a/a.mk":         aSrc,
		"app/vendor/units/pkg.mk":   depManifest("units"),
		"app/vendor/units/units.mk": units,
	})
	r := depCheck(t, filepath.Join(ws, "app"), Config{})
	clean(t, r)
	n := 0
	for _, m := range r.Trace.ModulesResolved {
		if strings.HasPrefix(m, "units_") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("modules %v: want one units package", r.Trace.ModulesResolved)
	}
	// Only two path Deps are one: a url Dep of a name whose vendor/<name>
	// a path Dep took is a second source (E0202 conflict), never a path
	// package that needs no grant.
	ws = writeTree(t, map[string]string{
		"a/pkg.mk":                  depManifest("a", urlDep("units", "https://example.com/units.tar.gz", "sha256:"+strings.Repeat("1", 64))),
		"a/a.mk":                    aSrc,
		"units/pkg.mk":              depManifest("units"),
		"units/units.mk":            units,
		"app/pkg.mk":                depManifest("app", pathDep("units", "../units"), pathDep("a", "../a")),
		"app/main.mk":               "import a\nimport units\n\nexport F(): int {\n    return a.A() + units.U()\n}\n",
		"app/vendor/a/pkg.mk":       depManifest("a", urlDep("units", "https://example.com/units.tar.gz", "sha256:"+strings.Repeat("1", 64))),
		"app/vendor/a/a.mk":         aSrc,
		"app/vendor/units/pkg.mk":   depManifest("units"),
		"app/vendor/units/units.mk": units,
	})
	e0202(t, depCheck(t, filepath.Join(ws, "app"), Config{Offline: true}), "conflict", "vendor/a/pkg.mk:")
}

// A vendored url package is a fetched package, so its path dependencies
// are refused as they are unvendored (they would otherwise join the build
// as path packages that need no grant), in the index layout and in the old
// layout.
func TestVendor_FetchedPackagePathDepRefused(t *testing.T) {
	const url = "https://example.com/u.tar.gz"
	u := map[string]string{
		"pkg.mk":         depManifest("u", pathDep("inner", "inner")),
		"u.mk":           "import inner\n\nexport G(): int {\n    return inner.F()\n}\n",
		"inner/pkg.mk":   depManifest("inner"),
		"inner/inner.mk": "export F(): int {\n    return 7\n}\n",
	}
	for _, layout := range []string{"index", "1.2"} {
		files := map[string]string{
			"pkg.mk":  depManifest("app", urlDep("u", url, "sha256:"+strings.Repeat("2", 64))),
			"main.mk": "import u\n\nexport A(): int {\n    return u.G()\n}\n",
		}
		dir := "vendor/u/"
		if layout == "index" {
			cid := urlCID(url)
			dir = "vendor/" + cid + "/"
			files["vendor/index.txt"] = url + " - " + cid + " tree-sha256:" + strings.Repeat("a", 64) + "\n"
		}
		for k, v := range u {
			files[dir+k] = v
		}
		// Each layout on its own: e0202 stops the test, so the old-layout
		// half would never run after a failing index half.
		t.Run(layout, func(t *testing.T) {
			app := writeTree(t, files)
			e0202(t, depCheck(t, app, Config{Offline: true}), "invalid", dir+"pkg.mk:")
		})
	}
}

// The same hole through replace: a fetched package's Dep that the
// root replaces by a path is relative to the root, and the Deps after it
// in that fetched manifest stay fetched (a path dependency among them is
// refused, not read relative to the root).
func TestReplace_FetchedPathDepAfterReplacedStaysRefused(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	x := gs.source("someone/x")
	gs.tag("someone/p", "1.0.0", map[string]string{
		"pkg.mk":   depManifest("p", srcDep("x", x, "1.0.0"), pathDep("evil", "evil")),
		"src/p.mk": "import x.src\n\nexport P(): int {\n    return src.X()\n}\n",
	})
	p := gs.source("someone/p")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("p", p, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + x + "\", path: \"../xfork\")])\n",
		"app/a.mk":        "import p.src\n\nexport A(): int {\n    return src.P()\n}\n",
		"app/evil/pkg.mk": depManifest("evil"),
		"app/evil/e.mk":   "export E(): int {\n    return 1\n}\n",
		"xfork/pkg.mk":    depManifest("x"),
		"xfork/src/x.mk":  "export X(): int {\n    return 7\n}\n",
	})
	r := depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, ""))
	if got := lines(r); len(got) != 1 || !strings.HasSuffix(got[0], ":E0202") || fact(r.Report.Diags[0].Diag, "reason") != "invalid" {
		t.Fatalf("want one E0202 invalid, got %q\n%s", got, r.Report.Text(r.Sources))
	}
}

// A build whose pkg.sum cannot be written goes on, with a note.
func TestSum_UnwritableSumIsANote(t *testing.T) {
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	app := urlApp(t, srv.URL+"/ini.tar.gz", sha(arch))
	p := filepath.Join(app, sumFile)
	if err := os.WriteFile(p, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(p, 0o644) })
	r := depCheck(t, app, Config{})
	clean(t, r)
	if b, _ := os.ReadFile(p); len(b) == 0 && !strings.Contains(strings.Join(r.Notes, "\n"), "pkg.sum") {
		t.Errorf("pkg.sum not written and no note: %q", r.Notes)
	}
}

// The root module (unsafe_modules: ["."]) is named "." in E0215 and
// the perimeter, never as nothing.
func TestGrant_RootModuleNamedDot(t *testing.T) {
	files := iniFiles()
	files["pkg.mk"] = "const Package = pkg.Package(\n    name: \"ini\",\n    unsafe_modules: [\".\"],\n)\n"
	arch := tarGzOf(t, "", files)
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	app := writeTree(t, map[string]string{
		"pkg.mk": grantRoot("app", "", urlDep("ini", srv.URL+"/ini.tar.gz", "")),
		"a.mk":   "import ini.src\n\nexport A(): int {\n    return src.Keys()\n}\n",
	})
	d := e0215(t, depCheck(t, app, Config{}), "pkg.mk:3:")
	if !strings.Contains(d.Message, "unsafe module .,") && !strings.Contains(d.Message, "unsafe module ., ") && !strings.Contains(d.Message, "unsafe module . ") {
		t.Errorf("message %q does not name the root module", d.Message)
	}
	if a := asksOf(&Manifest{UnsafeModules: []string{""}}); a.lines()[0] != "unsafe module ." {
		t.Errorf("perimeter line %q", a.lines()[0])
	}
}

// A url's user information, query and fragment are never printed (a
// diagnostic, the perimeter) nor written to vendor/index.txt; the
// vendored copy is still found.
func TestGrant_URLCredentialsNeverShown(t *testing.T) {
	files := iniFiles()
	files["pkg.mk"] = "const Package = pkg.Package(\n    name: \"ini\",\n    unsafe_modules: [\"src\"],\n)\n"
	arch := tarGzOf(t, "", files)
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	url := strings.Replace(srv.URL, "http://", "http://user:pw-SECRET@", 1) + "/ini.tar.gz?token=SECRET#SECRET"
	use := "import ini.src\n\nexport A(): int {\n    return src.Keys()\n}\n"
	app := writeTree(t, map[string]string{"pkg.mk": grantRoot("app", "", urlDep("ini", url, sha(arch))), "a.mk": use})
	r := depCheck(t, app, Config{})
	d := e0215(t, r, "pkg.mk:3:")
	// The quoted source line is the user's own pkg.mk; what mk writes is
	// the message, the facts and the alternatives.
	txt := d.Message + " " + strings.Join(d.Alternatives, " ")
	for _, f := range d.Facts {
		txt += " " + f.Value.Str
	}
	if strings.Contains(txt, "SECRET") {
		t.Errorf("E0215 shows the credentials:\n%s", txt)
	}
	// Granted by its local name: the build, the perimeter and mk vendor.
	app = writeTree(t, map[string]string{"pkg.mk": grantRoot("app", `"ini"`, urlDep("ini", url, sha(arch))), "a.mk": use})
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ans, res, err := s.Query("perimeter", nil)
	if err != nil || ans == nil {
		t.Fatal(err)
	}
	clean(t, res)
	if j := strings.Join(ans.Answer, "\n") + string(ans.JSON()); strings.Contains(j, "SECRET") {
		t.Errorf("the perimeter shows the credentials:\n%s", j)
	}
	if _, err := s.Vendor(); err != nil {
		t.Fatal(err)
	}
	if vr, err := s.Vendor(); err != nil || strings.Contains(fmt.Sprint(vr.Deps), "SECRET") {
		t.Errorf("mk vendor's result shows the credentials: %v %v", vr.Deps, err)
	}
	if idx := readFile(t, filepath.Join(app, "vendor", "index.txt")); strings.Contains(idx, "SECRET") || strings.Contains(idx, "user") {
		t.Errorf("vendor/index.txt holds the credentials:\n%s", idx)
	}
	srv.Close()
	clean(t, depCheck(t, app, Config{Offline: true}))
}
