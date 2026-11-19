// Why: a C import belongs to the package that declares it, and a dependency
// with C code must build inside the package that uses it.
// What: tests of a path dependency's C import, a url dependency's C import
// from its archive, and C and assembly sources without a C import.
// How: builds and runs packages in temp directories (cDepFiles) and checks
// which c.k each module sees and that every source links.

package driver

import (
	"os"
	"path/filepath"
	"testing"

	"mk/internal/types"
)

// cDepFiles is a dependency "cl" with its own C import c.k, a C source and
// an unsafe module lib that wraps the C function.
func cDepFiles() map[string]string {
	return map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"cl"`,
			"c_sources":      `[pkg.CSource(path: "csrc/k.c", flags: ["-DBASE=40"])]`,
			"c_imports":      `[pkg.CImport(name: "k", headers: ["csrc/k.h"], defines: [], macros: [], vendor: false)]`,
			"unsafe_modules": `["lib"]`}),
		"csrc/k.h":   "int k_add(int a, int b);\n#define K_SEVEN 7\n",
		"csrc/k.c":   "#include \"k.h\"\nint k_add(int a, int b) { return a + b + BASE - 40; }\n",
		"lib/lib.mk": "import c.k\n\nexport Add(a: i32, b: i32): i32 {\n    unsafe \"k_add is plain C\" {\n        return k.k_add(a, b) + k.KSeven - 7\n    }\n}\n",
	}
}

// TestCImport_Dependency: a path dependency's C import, C source and
// module build and run inside the package, beside the package's own C
// import of the same name c.k over another header: each package's c.k is
// its own (module cl.c.k in the dependency), and both C sources link.
func TestCImport_Dependency(t *testing.T) {
	tools := needTools(t)
	files := map[string]string{
		"app/pkg.mk": manifest(map[string]string{"name": `"app"`, "bins": `[pkg.Bin(name: "app", root: "main", link_script: "", cpu: "", subsystem: "console")]`,
			"deps":           "[" + pathDep("cl", "../cl") + "]",
			"c_sources":      `[pkg.CSource(path: "c/m.c", flags: [])]`,
			"c_imports":      `[pkg.CImport(name: "k", headers: ["c/m.h"], defines: [], macros: [], vendor: false)]`,
			"unsafe_modules": `["main"]`}),
		"app/c/m.h": "int k_mul(int a, int b);\n",
		"app/c/m.c": "#include \"m.h\"\nint k_mul(int a, int b) { return a * b; }\n",
		"app/main/main.mk": `import c.k
import cl.lib

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    a := lib.Add(2, 3)
    unsafe "k_mul is plain C" {
        m := k.k_mul(6, 7)
        try Print(mut out, "{a} {m}\n", (a: a, m: m))
    }
}
`,
	}
	for name, text := range cDepFiles() {
		files["cl/"+name] = text
	}
	ws := writeTree(t, files)
	s, err := NewSession(Config{Root: filepath.Join(ws, "app"), Target: types.X86_64Windows, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "app.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	out, errOut, code := runExe(t, types.X86_64Windows, paths[0], "")
	if code != 0 || out != "5 42\n" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 5 42", code, out, errOut)
	}
}

// TestCImport_FetchedDependency: a url dependency's C import is translated
// from its archive, unpacked once under the cache directory, and checks.
func TestCImport_FetchedDependency(t *testing.T) {
	tools := needTools(t)
	arch := tarGzOf(t, "cl-1", cDepFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/cl-1.tar.gz": arch})
	app := writeTree(t, map[string]string{
		// The package asks for unsafe code, so the root grants it.
		"pkg.mk":   grantRoot("app", `"`+srv.URL+"/cl-1.tar.gz"+`"`, urlDep("cl", srv.URL+"/cl-1.tar.gz", sha(arch))),
		"src/a.mk": "import cl.lib\n\nexport A(): i32 {\n    return lib.Add(1, 2)\n}\n",
	})
	cache := t.TempDir()
	for i := 0; i < 2; i++ {
		clean(t, depCheck(t, app, Config{CacheDir: cache, Tools: tools}))
	}
	ents, err := os.ReadDir(filepath.Join(cache, "cdeps"))
	if err != nil || len(ents) != 1 {
		t.Fatalf("cdeps: %v, %v; want the archive unpacked once", ents, err)
	}
	if _, err := os.Stat(filepath.Join(cache, "cdeps", ents[0].Name(), "csrc", "k.h")); err != nil {
		t.Error(err)
	}
	// The dependency's c.k is its own: the package, which declares no C
	// import, cannot import it.
	writeFiles(t, app, map[string]string{"src/b.mk": "import c.k\n"})
	e0202(t, depCheck(t, app, Config{CacheDir: cache, Tools: tools}), "no-cimport", "src/b.mk")
}

// TestCImport_SourcesWithoutImport: a package with an assembly source and
// a C source but no C import links both (they were ignored while
// c_imports was empty).
func TestCImport_SourcesWithoutImport(t *testing.T) {
	tools := needTools(t)
	dir := writeTree(t, map[string]string{
		"pkg.mk": manifest(map[string]string{"name": `"so"`, "bins": `[pkg.Bin(name: "so", root: "app", link_script: "", cpu: "", subsystem: "console")]`,
			"c_sources":      `[pkg.CSource(path: "lib/n.c", flags: [])]`,
			"asm_sources":    `[pkg.AsmSource(path: "lib/seven.s", target: "")]`,
			"unsafe_modules": `["app"]`}),
		"lib/n.c":     "int c_nine(void) { return 9; }\n",
		"lib/seven.s": "\t.text\n\t.globl asm_seven\nasm_seven:\n\tmovl $7, %eax\n\tret\n",
		"app/main.mk": `extern asm_seven(): i32
extern c_nine(): i32

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    unsafe "plain C and assembly" {
        a := asm_seven()
        b := c_nine()
        try Print(mut out, "{a} {b}\n", (a: a, b: b))
    }
}
`,
	})
	s, err := NewSession(Config{Root: dir, Target: types.X86_64Windows, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "so.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build: %d paths\n%s", len(paths), r.Report.Text(r.Sources))
	}
	if out, errOut, code := runExe(t, types.X86_64Windows, paths[0], ""); code != 0 || out != "7 9\n" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 7 9", code, out, errOut)
	}
}
