// Why: import c.<name> turns C headers into Mk bindings, and every form a
// header may hold must bind, build, link and cache correctly.
// What: end-to-end tests on both targets, the cimport cache, std modules
// that need a C import, opaque accessors, vendored views, --all-targets, a
// one-function edit and the windows.h measurement.
// How: cimportPackage and cEditPkg write packages in temp directories, so
// no corpus is needed; the tests build, run or check them and compare
// output, views and cache hits.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/toolchain"
	"mk/internal/types"
)

// cimportPackage is a package with one C import (a header with a struct, a
// bitfield struct, a static inline function, an object-like and a
// function-like macro), one C source compiled with a flag, and one
// assembly source. The test writes it, so no corpus is needed. Flags is
// transparent on both targets: with a short c, SysV gives it align 4 over
// a u8 unit, which Mk says only with align(n) (TestCImport_Linux covers
// that opaque form).
func cimportPackage() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"e2e"`, "bins": `[pkg.Bin(name: "e2e", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"c_sources":      `[pkg.CSource(path: "lib/calc.c", flags: ["-DBASE=40"])]`,
			"c_imports":      `[pkg.CImport(name: "calc", headers: ["lib/calc.h"], defines: [], macros: ["int TWICE(int x)"], vendor: false)]`,
			"asm_sources":    `[pkg.AsmSource(path: "lib/seven.s", target: "")]`,
			"unsafe_modules": `["app"]`}),
		"lib/calc.h": `#define CALC_VERSION 3
#define CALC_NAME "calc"
#define TWICE(x) ((x) * 2)
typedef struct Point { int x; int y; } Point;
typedef struct Flags { unsigned a:3, b:5; unsigned c; } Flags;
int calc_add(int a, int b);
int calc_norm1(Point p);
int calc_flags(Flags *f);
const char *calc_name(void);
static inline int calc_sub(int a, int b) { return a - b; }
`,
		"lib/calc.c": `#include "calc.h"
int calc_add(int a, int b) { return a + b + BASE - 40; }
int calc_norm1(Point p) { return (p.x < 0 ? -p.x : p.x) + (p.y < 0 ? -p.y : p.y); }
int calc_flags(Flags *f) { return f->a * 100 + f->b * 10 + f->c; }
const char *calc_name(void) { return "calc-lib"; }
`,
		"lib/seven.s": "\t.text\n\t.globl asm_seven\nasm_seven:\n\tmovl $7, %eax\n\tret\n",
		"app/main.mk": `import c.calc
import std.raw

extern asm_seven(): i32

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    p := calc.Point(x: -3, y: 4)
    var f := calc.Flags(bits0: 0, c: 7)
    f.SetA(5)
    f.SetB(2)
    unsafe "calc's functions and asm_seven are plain C" {
        a := calc.calc_add(2, 3)
        n := calc.calc_norm1(p)
        s := calc.calc_sub(10, 4)
        t := calc.TWICE(21)
        fl := calc.calc_flags(raw.OfMut(mut f))
        name := str.FromC(calc.calc_name())
        k := asm_seven()
        try Print(mut out, "{a} {n} {s} {t} {fl} {v} {name} {cn} {k}\n", (a: a, n: n, s: s, t: t, fl: fl, v: calc.CalcVersion, name: name, cn: calc.CalcNameConst, k: k))
    }
}
`,
	}
}

// TestCImport_EndToEndWindows and ...Linux: import c.calc builds and runs
// at dev and release: a struct by value, a bitfield's accessors against
// C's own reads, a static inline shim, a declared function-like macro, an
// object-like integer and string macro, str.FromC, a C source with its
// flag, and an assembly source.
func TestCImport_EndToEndWindows(t *testing.T) { testCImportEndToEnd(t, types.X86_64Windows) }
func TestCImport_EndToEndLinux(t *testing.T)   { testCImportEndToEnd(t, types.X86_64Linux) }

func testCImportEndToEnd(t *testing.T, target types.Target) {
	tools := needTools(t)
	if prefix, ok := toolchain.Runner(target.LLVMTriple); ok && len(prefix) > 0 && !linuxRun() {
		t.Skip(linuxOff)
	}
	dir := writeTree(t, cimportPackage())
	cache := t.TempDir()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			s, err := NewSession(Config{Root: dir, Target: target, Profile: prof, Tools: tools, CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "e2e"+exeSuffix(target)))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
			}
			out, errOut, code := runExe(t, target, paths[0], "")
			if want := "5 7 6 42 527 3 calc-lib calc 7\n"; code != 0 || out != want {
				t.Errorf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, want)
			}
		})
	}
	// The readable binding is the view gen/<target>/c/calc.mk.
	view, err := os.ReadFile(filepath.Join(dir, "gen", target.Name, "c", "calc.mk"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"export extern calc_add(a: i32, b: i32): i32", "export Flags.SetA(mut self, v: u32)",
		"export const CalcVersion: i32 = 3", "export extern TWICE(x: i32): i32", "// mk gen view "} {
		if !strings.Contains(string(view), w) {
			t.Errorf("view lacks %q", w)
		}
	}
}

// TestCImport_CacheHit: a second check of the same package reads the
// bindings from the cache, and the result is the same; a new header that
// shadows one the import read (a negative lookup) is a miss.
func TestCImport_CacheHit(t *testing.T) {
	tools := needTools(t)
	files := cimportPackage()
	files["inc/x.h"] = "#define X_VALUE 1\n"
	files["lib/calc.h"] = "#include <x.h>\n" + files["lib/calc.h"]
	dir := writeTree(t, files)
	// <x.h> is found through the sysroot, inc/; the package root, the one
	// -I, is searched before it.
	t.Setenv("MK_SYSROOT_x86_64-windows", filepath.Join(dir, "inc"))
	t.Setenv("MK_SYSROOT_x86_64-linux", filepath.Join(dir, "inc"))
	cache := t.TempDir()
	run := func() (*cImports, Result) {
		s, err := NewSession(Config{Root: dir, Tools: tools, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		r := s.Check()
		if len(r.Report.Diags) > 0 {
			t.Fatalf("check:\n%s", r.Report.Text(r.Sources))
		}
		return s.w.cimports, r
	}
	first, _ := run()
	if first.hit {
		t.Fatal("first check hit the cache")
	}
	second, _ := run()
	if !second.hit {
		t.Fatal("second check did not hit the cache")
	}
	// A shadowing x.h in the package root (searched before the sysroot).
	if err := os.WriteFile(filepath.Join(dir, "x.h"), []byte("#define X_VALUE 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, _ := run()
	if third.hit {
		t.Fatal("a new shadowing header still hit the cache")
	}
	view, _ := os.ReadFile(filepath.Join(dir, "gen", HostTarget().Name, "c", "calc.mk"))
	if !strings.Contains(string(view), "export const XValue: i32 = 2") {
		t.Errorf("the view does not read the shadowing header:\n%s", view)
	}
}

// TestCImport_NeedsCImport: a std module that imports c.<name>
// resolves only in a package that declares that C import; elsewhere its
// importer's import line is E0202 needs-cimport (cstd.go).
func TestCImport_NeedsCImport(t *testing.T) {
	std := fakeStdFiles()
	std["db/db.mk"] = "import c.sqlite3\n\nexport F(): int {\n    return 1\n}\n"
	r := check(t, Config{Root: writeTree(t, map[string]string{
		"pkg.mk":   manifest(nil),
		"src/a.mk": "import std.db\n\nexport A(): int {\n    return db.F()\n}\n",
	}), StdDir: writeTree(t, std)})
	var found bool
	for _, d := range r.Report.Diags {
		if fact(d.Diag, "reason") == "needs-cimport" {
			found = true
		}
	}
	if !found {
		t.Errorf("no needs-cimport:\n%s", r.Report.Text(r.Sources))
	}
}

// TestCImport_OpaqueChecks: a binding's generated accessors (a union, an
// anonymous union member, a flexible array member, a packed record, a
// typedef of a function pointer) check clean, and a program uses them.
func TestCImport_OpaqueChecks(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"op"`, "bins": `[pkg.Bin(name: "op", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"c_imports":      `[pkg.CImport(name: "op", headers: ["op.h"], defines: ["OP_LEVEL=2"], macros: [], vendor: false)]`,
			"unsafe_modules": `["app"]`}),
		"op.h": `typedef union Num { int i; float f; double d; } Num;
struct Rec { int kind; union { int a; short b; }; struct Rec *next; };
struct Msg { int len; char text[]; };
#pragma pack(push, 2)
struct Packed { char c; int x; };
#pragma pack(pop)
typedef void (*Visit)(struct Rec *r, void *ctx);
int walk(struct Rec *r, Visit v, void *ctx);
#define OP_DOUBLE (OP_LEVEL * 2)
`,
		"app/main.mk": `import c.op

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var n := op.Num(bytes: [0])
    n.SetI(41)
    var p := op.Packed(c: 1, x: 2)
    p.x = n.I() + 1
    try Print(mut out, "{x} {d}\n", (x: p.x, d: op.OpDouble))
}
`,
	})
	s, err := NewSession(Config{Root: dir, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "op"+exeSuffix(HostTarget())))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		view, _ := os.ReadFile(filepath.Join(dir, "gen", HostTarget().Name, "c", "op.mk"))
		t.Fatalf("build:\n%s\n%s", r.Report.Text(r.Sources), view)
	}
	out, errOut, code := runExe(t, HostTarget(), paths[0], "")
	if code != 0 || out != "42 4\n" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 42 4", code, out, errOut)
	}
}

// TestCImport_WindowsHMeasure records the clean and warm cost of
// importing windows.h from mingw-w64 (the target is 2 s): only when
// MK_CIMPORT_WINDOWS_SYSROOT names the mingw-w64 include directory. It
// asserts no time; it logs the numbers and the share of declarations bound.
func TestCImport_WindowsHMeasure(t *testing.T) {
	sys := os.Getenv("MK_CIMPORT_WINDOWS_SYSROOT")
	if sys == "" {
		t.Skip("MK_CIMPORT_WINDOWS_SYSROOT is not set")
	}
	tools := needTools(t)
	t.Setenv("MK_SYSROOT_x86_64-windows", sys)
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"wh"`,
			"c_imports": `[pkg.CImport(name: "win", headers: ["windows.h"], defines: [], macros: [], vendor: false)]`}),
		"src/a.mk": "import c.win\n\nexport Max(): i32 {\n    return win.MaxPath\n}\n",
	})
	cache := t.TempDir()
	for _, pass := range []string{"clean", "warm"} {
		cfg := Config{Root: dir, Target: types.X86_64Windows, Tools: tools, CacheDir: cache}
		// MK_CIMPORT_WHOLE=1 checks the whole binding, not the used items.
		cfg.TestHooks.CImportWhole = os.Getenv("MK_CIMPORT_WHOLE") == "1"
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		r := s.Check()
		took := time.Since(start)
		if len(r.Report.Diags) > 0 {
			text := r.Report.Text(r.Sources)
			if len(text) > 4000 {
				text = text[:4000]
			}
			t.Fatalf("%s check: %d diagnostics\n%s", pass, len(r.Report.Diags), text)
		}
		st := s.w.cimports.stats[0]
		t.Logf("%s mk check of a package importing windows.h: %v (cache hit %v); %d of %d declarations bound (%d direct, %d generated); skipped %v",
			pass, took, s.w.cimports.hit, st.Bound(), st.Decls, st.Direct, st.Generated, st.Skipped)
	}
}

// TestCImport_StdImportsC: a std module that imports c.<name>
// resolves it through the package's C import and checks, the package
// declaring a C source (TestCImport_NeedsCSource is the other half).
func TestCImport_StdImportsC(t *testing.T) {
	tools := needTools(t)
	std := fakeStdFiles()
	std["pkg.mk"] = strings.Replace(std["pkg.mk"], `unsafe_modules: ["core"`, `unsafe_modules: ["db", "core"`, 1)
	std["db/db.mk"] = "import c.k\n\nexport F(): i32 {\n    unsafe \"k_one is plain C\" {\n        return k.k_one() + k.KTwo\n    }\n}\n"
	r := check(t, Config{Root: writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"c_imports": `[pkg.CImport(name: "k", headers: ["k.h"], defines: [], macros: [], vendor: false)]`,
			"c_sources": `[pkg.CSource(path: "k.c", flags: [])]`}),
		"k.h":      "int k_one(void);\n#define K_TWO 2\n",
		"k.c":      "int k_one(void) { return 1; }\n",
		"src/a.mk": "import std.db\n\nexport A(): i32 {\n    return db.F()\n}\n",
	}), StdDir: writeTree(t, std), Tools: tools})
	if len(r.Report.Diags) > 0 {
		t.Fatalf("check:\n%s", r.Report.Text(r.Sources))
	}
}

// cEditPkg is a package with one C import whose src module calls nothing
// from it in G, so an edit of G is a one-function edit.
func cEditPkg(g string) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"ce"`, "unsafe_modules": `["src"]`,
			"c_imports": `[pkg.CImport(name: "k", headers: ["k.h"], defines: [], macros: [], vendor: false)]`}),
		"k.h":      "int k_one(void);\nint k_unused(int);\n#define K_THREE 3\n",
		"src/a.mk": "import c.k\n\nexport F(): i32 {\n    return k.KThree\n}\n\nexport G(x: i32): i32 {\n    " + g + "\n}\n",
	}
}

// TestCImport_OneFunctionEdit: with a C import in the package, a body edit
// of a function that uses nothing new from it re-resolves only its module:
// the translation hits the cimport cache and the binding module c.k the
// front cache. A body that names a new C item changes the pruned binding,
// so c.k misses too, and the new item is found (lazy items keep the key
// exact). Timing is tools/gate's (MK_TIMING), not this test's.
func TestCImport_OneFunctionEdit(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, cEditPkg("return x + 1"))
	// A fake std keeps the test to the C import's code, independent of
	// changes to std.
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, Tools: tools, StdDir: writeTree(t, fakeStdFiles())}
	if cold := frontCheck(t, cfg); len(cold.Report.Diags) > 0 {
		t.Fatalf("cold check:\n%s", cold.Report.Text(cold.Sources))
	}
	src := filepath.Join(dir, "src", "a.mk")
	edit := func(g string, missed []string) {
		t.Helper()
		if err := os.WriteFile(src, []byte(cEditPkg(g)["src/a.mk"]), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := NewSession(cfg)
		if err != nil {
			t.Fatal(err)
		}
		warm := s.Check()
		if len(warm.Report.Diags) > 0 {
			t.Fatalf("warm check after %q:\n%s", g, warm.Report.Text(warm.Sources))
		}
		if !s.w.cimports.hit {
			t.Errorf("after %q: the C import missed its cache", g)
		}
		got := pkgOnly(warm.Trace.ModulesResolved)
		slices.Sort(got)
		if !slices.Equal(got, missed) {
			t.Errorf("after %q: resolved %q, want %q", g, got, missed)
		}
	}
	edit("return x + 2", []string{"src"})
	edit("unsafe \"k_one is plain C\" {\n        return x + k.k_one()\n    }", []string{"c.k", "src"})
}

// TestCImport_VendorBinding: with vendor: true, the saved view is the
// binding when the headers cannot be read; without it, that is E0001.
func TestCImport_VendorBinding(t *testing.T) {
	tools := needTools(t)
	files := map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"vb"`,
			"c_imports": `[pkg.CImport(name: "v", headers: ["v.h"], defines: [], macros: [], vendor: true)]`}),
		"v.h":      "#define V_VAL 41\n",
		"src/a.mk": "import c.v\n\nexport F(): i32 {\n    return v.VVal + 1\n}\n",
	}
	dir := writeTree(t, files)
	cfg := Config{Root: dir, CacheDir: t.TempDir(), Target: types.X86_64Windows, Tools: tools, StdDir: writeTree(t, fakeStdFiles())}
	if r := frontCheck(t, cfg); len(r.Report.Diags) > 0 {
		t.Fatalf("first check:\n%s", r.Report.Text(r.Sources))
	}
	view := filepath.Join(dir, "gen", "x86_64-windows", "c", "v.mk")
	if _, err := os.Stat(view); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "v.h")); err != nil {
		t.Fatal(err)
	}
	if r := frontCheck(t, cfg); len(r.Report.Diags) > 0 {
		t.Fatalf("vendored check:\n%s", r.Report.Text(r.Sources))
	}
	man := filepath.Join(dir, "pkg.mk")
	data, _ := os.ReadFile(man)
	if err := os.WriteFile(man, []byte(strings.Replace(string(data), "vendor: true", "vendor: false", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	r := frontCheck(t, cfg)
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0001 {
		t.Fatalf("without vendor: want one E0001, got\n%s", r.Report.Text(r.Sources))
	}
}

// TestCImport_AllTargets: under --all-targets the C import is translated
// for the other target too (its view is written), a used item whose API
// differs between the targets is E0213 (C's long is i32 on Windows and i64
// on Linux), an unused one is not, and a target with no sysroot and no
// vendored view is E0213 no-sysroot.
func TestCImport_AllTargets(t *testing.T) {
	tools := needTools(t)
	pkg := func(body string) map[string]string {
		return map[string]string{
			"pkg.mk": manifest(map[string]string{"name": `"at"`, "unsafe_modules": `["src"]`,
				"c_imports": `[pkg.CImport(name: "k", headers: ["k.h"], defines: [], macros: [], vendor: false)]`}),
			"k.h":      "long k_long(long x);\nint k_int(void);\n",
			"src/a.mk": "import c.k\n\nexport F(): i32 {\n    unsafe \"plain C\" {\n        " + body + "\n    }\n}\n",
		}
	}
	t.Setenv("MK_EVIDENCE", "")
	t.Setenv("MK_SYSROOT_x86_64-linux", t.TempDir())
	std := writeTree(t, fakeStdFiles())
	cfg := func(dir string) Config {
		return Config{Root: dir, Target: types.X86_64Windows, Tools: tools, CacheDir: t.TempDir(), AllTargets: true, StdDir: std}
	}
	dir := writeTree(t, pkg("return k.k_int()"))
	if r := frontCheck(t, cfg(dir)); len(r.Report.Diags) > 0 {
		t.Fatalf("portable use:\n%s", r.Report.Text(r.Sources))
	}
	if _, err := os.Stat(filepath.Join(dir, "gen", "x86_64-linux", "c", "k.mk")); err != nil {
		t.Errorf("no Linux view: %v", err)
	}
	dir = writeTree(t, pkg("return k.k_long(1) + k.k_int()"))
	r := frontCheck(t, cfg(dir))
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0213 {
		t.Fatalf("long: want one E0213, got\n%s", r.Report.Text(r.Sources))
	}
	if text := r.Report.Text(r.Sources); !strings.Contains(text, "k_long") || strings.Contains(text, "k_int") {
		t.Errorf("E0213 should name k_long only:\n%s", text)
	}
	t.Setenv("MK_SYSROOT_x86_64-linux", "")
	r = frontCheck(t, cfg(writeTree(t, pkg("return k.k_int()"))))
	if len(r.Report.Diags) != 1 || r.Report.Diags[0].Diag.Code != diag.E0213 || !strings.Contains(r.Report.Text(r.Sources), "no-sysroot") {
		t.Fatalf("no sysroot: want E0213 no-sysroot, got\n%s", r.Report.Text(r.Sources))
	}
}
