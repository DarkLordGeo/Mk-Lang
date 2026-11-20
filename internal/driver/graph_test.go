// Why: loading the package graph decides which modules exist, in what
// order they run and what is reported first; errors there must be exact
// and the schedule deterministic.
// What: tests of loading (unknown and internal modules, import cycles,
// depths, unsafe), the phase rule (only the earliest failing phase reports),
// the schedule, worker panics and bin roots.
// How: checks small package trees, mostly against the stand-in std from
// helpers_test.go, and compares "path:line:col:code" lines.

package driver

import (
	"slices"
	"strings"
	"sync"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// pkgFiles is a package with pkg.mk and the given module files.
func pkgFiles(files map[string]string) map[string]string {
	out := map[string]string{"pkg.mk": manifest(nil)}
	for k, v := range files {
		out[k] = v
	}
	return out
}

func TestE0202_UnknownModule(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"src/a.mk":      "import src.utill\n\nexport F(): int {\n    return 1\n}\n",
		"src/Util/u.mk": "export U(): int {\n    return 1\n}\n",
	}))})
	expectLines(t, r, "src/a.mk:1:8:E0202")
	if len(r.Report.Diags) == 1 {
		d := r.Report.Diags[0].Diag
		if fact(d, "reason") != "not-found" || fact(d, "module") != "src.utill" {
			t.Errorf("reason %q module %q", fact(d, "reason"), fact(d, "module"))
		}
	}
	// The closest module by folded name is offered.
	r = check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"src/a.mk":      "import src.util\n\nexport F(): int {\n    return 1\n}\n",
		"src/Util/u.mk": "export U(): int {\n    return 1\n}\n",
	}))})
	if len(r.Report.Diags) != 1 || !slices.Equal(r.Report.Diags[0].Diag.Alternatives, []string{"import src.Util"}) {
		t.Errorf("got %q", lines(r))
	}
}

func TestE0202_NotImplementedStdModule(t *testing.T) {
	// The stand-in std, which provides no std.http, though the real std does.
	r := check(t, Config{Root: writeTree(t, map[string]string{"a.mk": "import std.http\n\nG() {\n}\n"}), StdDir: fakeStd(t)})
	expectLines(t, r, "a.mk:1:8:E0202")
	if len(r.Report.Diags) == 1 && fact(r.Report.Diags[0].Diag, "reason") != "not-implemented" {
		t.Errorf("reason %q", fact(r.Report.Diags[0].Diag, "reason"))
	}
}

// An import of c.<name> with no pkg.CImport of that name (here, no
// package at all) is E0202 no-cimport (cimport.go).
func TestE0202_NoCImport(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, map[string]string{"a.mk": "import c.stdio\n\nG() {\n}\n"})})
	expectLines(t, r, "a.mk:1:8:E0202")
	if len(r.Report.Diags) == 1 && fact(r.Report.Diags[0].Diag, "reason") != "no-cimport" {
		t.Errorf("reason %q", fact(r.Report.Diags[0].Diag, "reason"))
	}
}

func TestE0202_StdInternal(t *testing.T) {
	stdDir := fakeStd(t)
	// Every module of std.Internal, whether the std at hand provides it (the
	// stand-in's core, raw, sys) or not (std.rt, std.task).
	for _, sd := range []string{stdDir, ""} {
		for _, m := range stdInternal {
			bind := m[strings.LastIndexByte(m, '.')+1:]
			r := check(t, Config{Root: writeTree(t, map[string]string{
				"a.mk": "import " + m + "\n\nexport Main(sys: &System) {\n    " + bind + ".X()\n}\n"}), StdDir: sd})
			expectLines(t, r, "a.mk:1:8:E0202")
			if len(r.Report.Diags) == 1 {
				d := r.Report.Diags[0].Diag
				if fact(d, "reason") != "std-internal" || fact(d, "module") != m {
					t.Errorf("%s (std %q): reason %q module %q", m, sd, fact(d, "reason"), fact(d, "module"))
				}
			}
		}
	}
	// A public std module is importable.
	clean(t, check(t, Config{Root: writeTree(t, map[string]string{
		"a.mk": "import std.fs\n\nexport Main(sys: &System) {\n    _ = fs.ReadText()\n}\n"}), StdDir: stdDir}))
}

func TestE0202_StdCorpusNotAModule(t *testing.T) {
	stdDir := fakeStd(t)
	for _, tc := range []struct{ path, reason string }{{"std.corpus", "not-found"}, {"std.pkg", "manifest-only"}} {
		r := check(t, Config{Root: writeTree(t, map[string]string{"a.mk": "import " + tc.path + "\n\nG() {\n}\n"}),
			StdDir: stdDir})
		expectLines(t, r, "a.mk:1:8:E0202")
		if len(r.Report.Diags) == 1 && fact(r.Report.Diags[0].Diag, "reason") != tc.reason {
			t.Errorf("%s: reason %q", tc.path, fact(r.Report.Diags[0].Diag, "reason"))
		}
	}
}

func TestE0205_ImportCycle(t *testing.T) {
	// tests/conformance/codes/E0205 as a package.
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"main.mk": "import a\n\nGet(): int {\n    return a.One()\n}\n",
		"a/a.mk":  "import b\n\nexport One(): int {\n    return b.Two() - 1\n}\n",
		"b/b.mk":  "import a\n\nexport Two(): int {\n    return 2\n}\n\nexport Back(): int {\n    return a.One()\n}\n",
	}))})
	expectLines(t, r, "a/a.mk:1:1:E0205")
	if len(r.Report.Diags) == 1 {
		f, _ := r.Report.Diags[0].Diag.FactValue("cycle")
		var got []string
		for _, x := range f.List {
			got = append(got, x.Str)
		}
		if !slices.Equal(got, []string{"a", "b", "a"}) {
			t.Errorf("cycle %q", got)
		}
	}
	// A self-import.
	r = check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "import a\n\nexport One(): int {\n    return 1\n}\n",
	}))})
	expectLines(t, r, "a/a.mk:1:1:E0205")
}

func TestE1102_UnsafeNotPermitted(t *testing.T) {
	src := "export F(): int {\n    unsafe \"why\" {\n    }\n    return 1\n}\n"
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{"ffi/a.mk": src}))})
	expectLines(t, r, "ffi/a.mk:2:5:E1102")
	if len(r.Report.Diags) == 1 {
		d := r.Report.Diags[0].Diag
		if fact(d, "module") != "ffi" || fact(d, "manifest") != "unsafe_modules" || d.At.Len() != 6 {
			t.Errorf("module %q manifest %q len %d", fact(d, "module"), fact(d, "manifest"), d.At.Len())
		}
	}
	files := pkgFiles(map[string]string{"ffi/a.mk": src})
	files["pkg.mk"] = manifest(map[string]string{"unsafe_modules": `["ffi"]`})
	clean(t, check(t, Config{Root: writeTree(t, files)}))
	// Single-file mode permits no module, and an unsafe impl counts too.
	r = check(t, Config{Root: writeTree(t, map[string]string{
		"a.mk": "struct S {\n}\n\nunsafe impl Send for S {\n}\n\nexport Main(sys: &int) {\n}\n"})})
	expectLines(t, r, "a.mk:4:1:E1102")
}

func TestDepths(t *testing.T) {
	cfg, _ := Config{}.normalize()
	w := newWorld(cfg)
	b := newBuild(w, nil)
	mk := func(name string) *modNode { m := &modNode{name: name, path: name}; b.addModule(m); return m }
	c, a, d, x := mk("c"), mk("a"), mk("d"), mk("x")
	a.deps = []*modNode{c}
	d.deps = []*modNode{a, c}
	x.deps = nil
	b.depths()
	var got []string
	for _, m := range b.mods {
		got = append(got, m.path+":"+string(rune('0'+m.depth)))
	}
	if want := []string{"c:0", "x:0", "a:1", "d:2"}; !slices.Equal(got, want) {
		t.Errorf("slots %q, want %q", got, want)
	}
}

func TestEarliestPhaseOnly(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "export F(): int {\n    return (\n}\n",
		"b/b.mk": "export G(): int {\n    return Missing()\n}\n",
	}))})
	if got := codes(r); len(got) == 0 || slices.ContainsFunc(got, func(c string) bool { return c != "E0101" }) {
		t.Errorf("codes %q, want only the parse error", got)
	}
	if r.Report.Phase != diag.PhaseParse {
		t.Errorf("phase %v", r.Report.Phase)
	}
}

func TestEveryErrorOfThePhase(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "export A(): int {\n    return MissingA()\n}\n",
		"b/b.mk": "import a\n\nexport B(): int {\n    return a.A() + MissingB()\n}\n",
		"e/e.mk": "import b\n\nexport C(): int {\n    return b.B() + MissingC()\n}\n",
	}))})
	expectLines(t, r, "a/a.mk:2:12:E0201", "b/b.mk:4:20:E0201", "e/e.mk:4:20:E0201")
}

func TestE0000_WorkerPanicBecomesDiagnostic(t *testing.T) {
	r := check(t, Config{Root: writeTree(t, pkgFiles(map[string]string{
		"lib/l.mk":   "export L(): int {\n    return 1\n}\n",
		"other/o.mk": "export O(): int {\n    return Missing()\n}\n",
	})), TestHooks: TestHooks{PanicIn: "lib"}})
	got := codes(r)
	if !slices.Contains(got, "E0000") || !slices.Contains(got, "E0201") {
		t.Errorf("codes %q, want the E0000 and the other module's E0201\n%s", got, r.Report.Text(r.Sources))
	}
	if st := ExitStatus(r.Report); st != 3 {
		t.Errorf("exit status %d, want 3", st)
	}
}

func TestBinRootsPassedToResolve(t *testing.T) {
	files := pkgFiles(map[string]string{
		"a/main.mk": "export Main(sys: &int) {\n}\n",
		"b/b1.mk":   "export B(): int {\n    return 1\n}\n",
		"b/b2.mk":   "export B2(): int {\n    return 1\n}\n",
		"lib/l.mk":  "export L(): int {\n    return 1\n}\n",
	})
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "a", root: "a", link_script: "", cpu: "", subsystem: "console"), pkg.Bin(name: "b", root: "b", link_script: "", cpu: "", subsystem: "console")]`})
	expectLines(t, check(t, Config{Root: writeTree(t, files)}), "b/b1.mk:1:1:E0201")
}

func TestBinRootDotIsPackageRoot(t *testing.T) {
	// tests/bench/<k>/pkg.mk writes root: "." for a bin rooted at the
	// package directory; it names the root's own module, so a missing Main
	// there is the bin root's E0201; "./" and "lib/" are cleaned alike (an
	// empty root is refused by the reader).
	for _, root := range []string{".", "./"} {
		files := pkgFiles(map[string]string{
			"k.mk":     "export K(): int {\n    return 1\n}\n",
			"lib/l.mk": "export L(): int {\n    return 1\n}\n",
		})
		files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "k", root: "` + root + `", link_script: "", cpu: "", subsystem: "console")]`,
			"unsafe_modules": `["lib/"]`})
		expectLines(t, check(t, Config{Root: writeTree(t, files)}), "k.mk:1:1:E0201")
	}
	files := pkgFiles(map[string]string{"k.mk": "export Main(sys: &System) {\n}\n"})
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "k", root: ".", link_script: "", cpu: "", subsystem: "console")]`})
	clean(t, check(t, Config{Root: writeTree(t, files)}))
	// A root holding only pkg.mk is no module directory.
	files = pkgFiles(map[string]string{"lib/l.mk": "export L(): int {\n    return 1\n}\n"})
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "k", root: ".", link_script: "", cpu: "", subsystem: "console")]`})
	r := check(t, Config{Root: writeTree(t, files)})
	if got := codes(r); !slices.Equal(got, []string{"E0202"}) || fact(r.Report.Diags[0].Diag, "manifest") != "bins[0].root" {
		t.Errorf("root with only pkg.mk: %v\n%s", got, r.Report.Text(r.Sources))
	}
	// A root leaving the tree is refused.
	files = pkgFiles(map[string]string{"k.mk": "export Main(sys: &int) {\n}\n"})
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "k", root: "../k", link_script: "", cpu: "", subsystem: "console")]`})
	if got := codes(check(t, Config{Root: writeTree(t, files)})); !slices.Equal(got, []string{"E0202"}) {
		t.Errorf("root ../k: %v", got)
	}
}

func TestScheduleIsDeterministic(t *testing.T) {
	// The same package, checked many times on several workers, gives the
	// same bytes.
	dir := writeTree(t, pkgFiles(map[string]string{
		"a/a.mk": "export A(): int {\n    return MissingA()\n}\n",
		"b/b.mk": "import a\n\nexport B(): int {\n    return a.A() + MissingB()\n}\n",
		"e/e.mk": "import a\n\nexport C(): int {\n    return a.A() + MissingC()\n}\n",
		"d/d.mk": "import b\nimport e\n\nexport D(): int {\n    return b.B() + e.C()\n}\n",
	}))
	// std is checked once, from another package, into a seed cache; each
	// of the 8 checks starts from its own copy of it, so std's packs hit
	// and the package's modules, whose diagnostics are compared, are
	// checked and scheduled cold every time.
	seed := t.TempDir()
	other := writeTree(t, pkgFiles(map[string]string{"x/x.mk": "export X(): int {\n    return 1\n}\n"}))
	if r := check(t, Config{Root: other, CacheDir: seed}); len(r.Report.Diags) > 0 {
		t.Fatalf("seed check:\n%s", r.Report.Text(r.Sources))
	}
	caches := make([]string, 8)
	for i := range caches {
		caches[i] = t.TempDir()
		if err := copyTreeNoCache(seed, caches[i]); err != nil {
			t.Fatal(err)
		}
	}
	var first string
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(jobs int, cache string) {
			defer wg.Done()
			r := check(t, Config{Root: dir, Jobs: jobs, CacheDir: cache})
			got := string(r.Report.WriteJSON(r.Sources))
			mu.Lock()
			defer mu.Unlock()
			if first == "" {
				first = got
			} else if got != first {
				t.Errorf("jobs %d: report differs:\n%s\n%s", jobs, got, first)
			}
		}(i%4+1, caches[i])
	}
	wg.Wait()
}

func TestPreludeVisibleWithStd(t *testing.T) {
	// tests/programs/hello.mk resolves against a std that declares the prelude.
	r := check(t, Config{Root: "../../tests/programs/hello.mk", StdDir: fakeStd(t)})
	got := codes(r)
	// The stand-in Print takes one int, but resolution does not check calls.
	if slices.Contains(got, "E0201") {
		t.Errorf("prelude names unresolved:\n%s", r.Report.Text(r.Sources))
	}
	_ = types.NoModule
}
