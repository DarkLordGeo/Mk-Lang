// Why: a test-only dependency must be available to test blocks without
// leaking into the program the package ships.
// What: tests that test_deps are resolved whenever the package is checked,
// that their names are E0217 outside test blocks, that a move between deps
// and test_deps checks warm as it does cold, and that only mk test links
// what they link.
// How: testDepApp and appManifest write the package; the tests check, build
// or test it and compare diagnostics, the perimeter and the link list.

package driver

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// testDepApp is app with h as a test dependency (or a dependency, when
// asDep), and a.mk's text.
func testDepApp(t *testing.T, asDep bool, src string) string {
	t.Helper()
	field := "test_deps"
	if asDep {
		field = "deps"
	}
	ws := writeTree(t, map[string]string{
		"h/pkg.mk":   depManifest("h"),
		"h/src/h.mk": "export H(): int {\n    return 1\n}\n\nexport struct T {\n    export v: int\n}\n",
		"app/pkg.mk": appManifest(field, pathDep("h", "../h")),
		"app/a.mk":   src,
	})
	return filepath.Join(ws, "app")
}

// appManifest is app's manifest with dep in field (deps or test_deps),
// the other fields left out.
func appManifest(field, dep string) string {
	return "const Package = pkg.Package(\n    name: \"app\",\n    " + field + ": [" + dep + "],\n)\n"
}

const testDepUse = "import h.src\n\nexport A(): int {\n    return 2\n}\n\ntest \"uses h\" {\n    Assert(src.H() == 1)\n}\n"

func TestTestDeps_ResolvedForTestBlocks(t *testing.T) {
	r := depCheck(t, testDepApp(t, false, testDepUse), Config{})
	clean(t, r)
	if !slices.ContainsFunc(r.Trace.ModulesResolved, func(m string) bool { return strings.HasPrefix(m, "h_") }) {
		t.Errorf("modules %v: the test dependency is not resolved", r.Trace.ModulesResolved)
	}
}

func TestE0217_TestDepOutsideTest(t *testing.T) {
	src := "import h.src\n\nexport A(): int {\n    return src.H()\n}\n\nexport G(x: src.T): int {\n    return x.v\n}\n\ntest \"uses h\" {\n    Assert(src.H() == 1)\n}\n"
	r := depCheck(t, testDepApp(t, false, src), Config{})
	got := lines(r)
	for _, d := range r.Report.Diags {
		if d.Diag.Code != diag.E0217 {
			t.Errorf("unexpected %v: %s", d.Diag.Code, d.Diag.Message)
		}
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "a.mk:4:") || !strings.HasPrefix(got[1], "a.mk:7:") {
		t.Errorf("E0217 at %q, want a.mk:4 (a body) and a.mk:7 (a signature)\n%s", got, r.Report.Text(r.Sources))
	}
	// The same uses through deps are fine.
	clean(t, depCheck(t, testDepApp(t, true, src), Config{}))
}

// Moving h between deps and test_deps flips E0217 on an unchanged a.mk:
// warm must equal cold at each step.
func TestTestDeps_WarmMatchesColdAcrossMove(t *testing.T) {
	src := "import h.src\n\nexport A(): int {\n    return src.H()\n}\n"
	ws := writeTree(t, map[string]string{
		"h/pkg.mk":   depManifest("h"),
		"h/src/h.mk": "export H(): int {\n    return 1\n}\n",
		"app/a.mk":   src,
	})
	app := filepath.Join(ws, "app")
	warm := t.TempDir()
	for i, field := range []string{"deps", "test_deps", "deps", "test_deps"} {
		writeFiles(t, ws, map[string]string{"app/pkg.mk": appManifest(field, pathDep("h", "../h"))})
		w := check(t, Config{Root: app, CacheDir: warm, Target: types.X86_64Windows})
		c := check(t, Config{Root: app, CacheDir: t.TempDir(), Target: types.X86_64Windows})
		if !slices.Equal(lines(w), lines(c)) {
			t.Errorf("step %d (%s): warm %q, cold %q", i, field, lines(w), lines(c))
		}
		if want := field == "test_deps"; (len(lines(c)) == 1) != want {
			t.Errorf("step %d (%s): cold %q", i, field, lines(c))
		}
	}
}

// A test-only package's links are mk test's alone.
func TestTestDeps_LinkedOnlyForTest(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"h/pkg.mk": "const Package = pkg.Package(\n    name: \"h\",\n" +
			"    links: [pkg.Link(name: \"hlib\", kind: \"dynamic\", path: \"\", target: \"x86_64-windows\")],\n)\n",
		"h/src/h.mk": "export H(): int {\n    return 1\n}\n",
		"app/pkg.mk": appManifest("test_deps", pathDep("h", "../h")),
		"app/a.mk":   testDepUse,
	})
	app := filepath.Join(ws, "app")
	for _, testing := range []bool{false, true} {
		s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		s.testing = testing
		// The stand-in std has no test runner support; the link list
		// needs only the walk.
		if r := s.runCheck(); len(s.deps) != 1 {
			t.Fatalf("testing %v: deps %v\n%s", testing, s.deps, r.Report.Text(r.Sources))
		}
		links, ds := s.collectLinks(s.pkgMan, app)
		if len(ds) > 0 {
			t.Fatal(ds)
		}
		has := slices.ContainsFunc(links, func(l pkgLink) bool { return l.Name == "hlib" })
		if has != testing {
			t.Errorf("testing %v: hlib linked %v", testing, has)
		}
	}
}

// The perimeter lists what a test-only package asks for in every command
// (mk query perimeter is no test session), though only mk test links it.
func TestTestDeps_PerimeterShowsTestOnlyAsks(t *testing.T) {
	ws := writeTree(t, map[string]string{
		"h/pkg.mk": "const Package = pkg.Package(\n    name: \"h\",\n" +
			"    links: [pkg.Link(name: \"hlib\", kind: \"dynamic\", path: \"\", target: \"x86_64-windows\")],\n)\n",
		"h/src/h.mk": "export H(): int {\n    return 1\n}\n",
		"app/pkg.mk": appManifest("test_deps", pathDep("h", "../h")),
		"app/a.mk":   testDepUse,
	})
	s, err := NewSession(Config{Root: filepath.Join(ws, "app"), StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ans, _, err := s.Query("perimeter", nil)
	if err != nil || ans == nil {
		t.Fatal(err)
	}
	if text := strings.Join(ans.Answer, "\n"); !strings.Contains(text, "link hlib") {
		t.Errorf("perimeter:\n%s", text)
	}
}

// A dependency's own test_deps bind when it is checked inside another
// package's build: its modules import them for their test blocks. A use
// outside a test block is E0217 in the dependency, and mk vendor copies
// the test dependency.
func TestTestDeps_DependencyOwnTestDeps(t *testing.T) {
	p := func(body string) map[string]string {
		return map[string]string{
			"h/pkg.mk":   depManifest("h"),
			"h/src/h.mk": "export H(): int {\n    return 1\n}\n",
			"p/pkg.mk":   "const Package = pkg.Package(\n    name: \"p\",\n    test_deps: [" + pathDep("h", "../h") + "],\n)\n",
			"p/src/p.mk": "import h.src\n\nexport P(): int {\n    return " + body + "\n}\n\ntest \"p\" {\n    Assert(src.H() == 1)\n}\n",
			"app/pkg.mk": depManifest("app", pathDep("p", "../p")),
			"app/a.mk":   "import p.src\n\nexport A(): int {\n    return src.P()\n}\n",
		}
	}
	ws := writeTree(t, p("2"))
	app := filepath.Join(ws, "app")
	clean(t, depCheck(t, app, Config{}))
	r := depCheck(t, filepath.Join(writeTree(t, p("src.H()")), "app"), Config{})
	if got := lines(r); len(got) != 1 || !strings.HasSuffix(got[0], ":E0217") || !strings.HasPrefix(got[0], "../p/src/p.mk:4:") {
		t.Errorf("want one E0217 in p, got %q\n%s", got, r.Report.Text(r.Sources))
	}
	s, err := NewSession(Config{Root: app, StdDir: fakeStd(t), CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Vendor()
	if err != nil || !slices.ContainsFunc(res.Deps, func(d VendorDep) bool { return d.Name == "h" }) {
		t.Errorf("mk vendor: %v %+v", err, res.Deps)
	}
}

// The link itself (real std and toolchain): a test-only package with a C
// source is left out of mk build's link, and the program links and runs;
// mk test links it, and its test calls the C function.
func TestTestDeps_CSourceLinkedOnlyByTest(t *testing.T) {
	tools := needTools(t)
	ws := writeTree(t, map[string]string{
		"h/pkg.mk":   "const Package = pkg.Package(\n    name: \"h\",\n    c_sources: [pkg.CSource(path: \"c/h.c\", flags: [])],\n    unsafe_modules: [\"src\"],\n)\n",
		"h/c/h.c":    "int h_c(void) { return 7; }\n",
		"h/src/h.mk": "extern h_c(): i32\n\nexport H(): i32 {\n    var x: i32 = 0\n    unsafe \"h_c is plain C\" {\n        x = h_c()\n    }\n    return x\n}\n",
		"app/pkg.mk": "const Package = pkg.Package(\n    name: \"app\",\n    bins: [pkg.Bin(name: \"app\", root: \".\", subsystem: \"console\")],\n" +
			"    test_deps: [" + pathDep("h", "../h") + "],\n)\n",
		"app/main.mk": "import h.src\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n    try Print(mut out, \"ok\\n\", ())\n}\n\n" +
			"test \"h links\" {\n    Assert(src.H() == 7)\n}\n",
	})
	app := filepath.Join(ws, "app")
	s, err := NewSession(Config{Root: app, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "app"+exeSuffix(types.X86_64Windows)))
	if err != nil || len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("mk build: %v\n%s", err, r.Report.Text(r.Sources))
	}
	if out, errOut, code := runExe(t, types.X86_64Windows, paths[0], ""); code != 0 || out != "ok\n" {
		t.Errorf("run: exit %d, %q %q", code, out, errOut)
	}
	if !slices.ContainsFunc(s.deps, func(p *depPkg) bool { return p.testOnly && p.man != nil && len(p.man.CSources) == 0 }) {
		t.Error("mk build saw the test-only package's C source")
	}
	ts, err := NewSession(Config{Root: app, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tr, rep, err := ts.Test(TestOptions{})
	if err != nil || rep == nil || rep.Passed != 1 || rep.Failed != 0 {
		t.Fatalf("mk test: %v %+v\n%s", err, rep, tr.Report.Text(tr.Sources))
	}
}
