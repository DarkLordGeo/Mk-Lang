// Why: a std module may wrap C code, which only links when the package
// declares the C import and source, and C code calling Mk needs headers.
// What: tests of cstd.go (std modules importing C), of the std export
// extern roots a package with c_sources links (linkedMods), of std.libc's
// generated C headers (libch.go), and of the sqlite amalgamation built
// against them.
// How: realStdWith copies the repository's std with extra modules; the
// tests build, run or check packages over that copy.

package driver

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// realStdWith is a copy of the repository's std (its .mk files) with extra
// files added or replaced, and with the directories in unsafe appended to
// std/pkg.mk's unsafe_modules.
func realStdWith(t *testing.T, extra map[string]string, unsafe ...string) string {
	t.Helper()
	src := filepath.Join("..", "..", "std")
	files := map[string]string{}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".mk") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		files[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range extra {
		files[k] = v
	}
	if len(unsafe) > 0 {
		text := files["pkg.mk"]
		i := strings.Index(text, "unsafe_modules: [")
		if i < 0 {
			t.Fatal("std/pkg.mk has no unsafe_modules")
		}
		j := i + strings.Index(text[i:], "]")
		files["pkg.mk"] = text[:j] + `, "` + strings.Join(unsafe, `", "`) + `"` + text[j:]
	}
	return writeTree(t, files)
}

// cStdDB is std.db for the build tests: it imports c.k, hands C a Mk
// function as a function pointer, and exports a function only C calls.
const cStdDB = `import c.k

Twice(x: i32): i32 {
    return x * 2
}

export extern db_from_c(x: i32): i32 {
    return x + 100
}

export F(): i32 {
    unsafe "k_call is plain C" {
        return k.k_call(Twice)
    }
}

export G(): i32 {
    unsafe "k_back is plain C" {
        return k.k_back()
    }
}
`

// cStdPkg is a package whose main imports std.db; withC declares the C
// import and the C source.
func cStdPkg(withC bool) map[string]string {
	over := map[string]string{"name": `"p"`, "bins": `[pkg.Bin(name: "p", root: ".", link_script: "", cpu: "", subsystem: "console")]`}
	if withC {
		over["c_imports"] = `[pkg.CImport(name: "k", headers: ["k.h"], defines: [], macros: [], vendor: false)]`
		over["c_sources"] = `[pkg.CSource(path: "k.c", flags: [])]`
	}
	return map[string]string{
		"pkg.mk": manifest(over),
		"k.h":    "int k_call(int (*f)(int));\nint k_back(void);\nint k_libc(void);\n",
		// stdlib.h is std.libc's generated header: it declares
		// k_hook, which std.libc defines in the test's std copy.
		"k.c": "#include <stdlib.h>\n#include \"k.h\"\nint db_from_c(int);\n" +
			"int k_call(int (*f)(int)) { return f(21); }\nint k_back(void) { return db_from_c(1) + k_hook(41); }\n",
		"main.mk": "import std.db\n\nexport Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n" +
			"    try Print(mut out, \"{a} {b}\\n\", (a: db.F(), b: db.G()))\n}\n",
	}
}

// cStdTree is the std copy of the build tests: std.db, and a function in
// std.libc that only C calls.
func cStdTree(t *testing.T) string {
	return realStdWith(t, map[string]string{
		"db/db.mk":     cStdDB,
		"libc/hook.mk": "export extern k_hook(x: i32): i32 {\n    return x + 1\n}\n",
	}, "db")
}

// TestCStd_StdModuleImportingCBuildsAndRuns: a std module that
// imports c.k builds and runs, not only checks. It was "main.Main is
// reached but has no body": std.db was checked before the package's C
// build, so it was tainted, and main with it. It also covers export
// roots: db_from_c (std.db) and k_hook (std.libc) are called only from C,
// so they link only as roots, and k.c finds k_hook's prototype in the
// generated stdlib.h.
func TestCStd_StdModuleImportingCBuildsAndRuns(t *testing.T) {
	tools := needTools(t)
	std := cStdTree(t)
	dir := writeTree(t, cStdPkg(true))
	cache := t.TempDir()
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		t.Run(prof.String(), func(t *testing.T) {
			s, err := NewSession(Config{Root: dir, StdDir: std, Profile: prof, Tools: tools, CacheDir: cache})
			if err != nil {
				t.Fatal(err)
			}
			r, paths, err := s.Build(filepath.Join(t.TempDir(), "p"+exeSuffix(HostTarget())))
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Report.Diags) > 0 || len(paths) != 1 {
				t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
			}
			out, errOut, code := runExe(t, HostTarget(), paths[0], "")
			if want := "42 143\n"; code != 0 || out != want {
				t.Errorf("exit %d, stdout %q, stderr %q; want %q", code, out, errOut, want)
			}
		})
	}
}

// TestCStd_UnreachedNeverResolved: with a std module that imports a C import
// the package does not declare, a package that never imports it checks clean
// and never resolves it, and hello.mk builds.
func TestCStd_UnreachedNeverResolved(t *testing.T) {
	std := fakeStdFiles()
	std["db/db.mk"] = "import c.sqlite3\n\nexport F(): int {\n    return 1\n}\n"
	std["db/user/user.mk"] = "import std.db\n\nexport U(): int {\n    return db.F()\n}\n"
	r := check(t, Config{Root: writeTree(t, map[string]string{
		"pkg.mk":   manifest(nil),
		"src/a.mk": "import std.fs\n\nexport A(): int {\n    return fs.ReadText()\n}\n",
	}), StdDir: writeTree(t, std)})
	if len(r.Report.Diags) > 0 {
		t.Fatalf("check:\n%s", r.Report.Text(r.Sources))
	}
	for _, m := range r.Trace.ModulesResolved {
		if m == "std.db" || m == "std.db.user" {
			t.Errorf("%s was resolved", m)
		}
	}

	tools := needTools(t)
	hello, err := os.ReadFile(filepath.Join("..", "..", "tests", "programs", "hello.mk"))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(writeTree(t, map[string]string{"hello.mk": string(hello)}), "hello.mk")
	s, err := NewSession(Config{Root: file, StdDir: cStdTree(t), Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	br, paths, err := s.Build(filepath.Join(t.TempDir(), "hello"+exeSuffix(HostTarget())))
	if err != nil {
		t.Fatal(err)
	}
	if len(br.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("hello.mk with std.db present:\n%s", br.Report.Text(br.Sources))
	}
}

// TestCStd_NeedsCImportAtImporter: importing the std module without the C
// import is one E0202 needs-cimport at the importer's import line, naming
// the C import; a std module that imports it is late too, and reports
// nothing of its own.
func TestCStd_NeedsCImportAtImporter(t *testing.T) {
	std := fakeStdFiles()
	std["db/db.mk"] = "import c.sqlite3\n\nexport F(): int {\n    return 1\n}\n"
	std["db/user/user.mk"] = "import std.db\n\nexport U(): int {\n    return db.F()\n}\n"
	r := check(t, Config{Root: writeTree(t, map[string]string{
		"pkg.mk":   manifest(nil),
		"src/a.mk": "import std.db.user\n\nexport A(): int {\n    return user.U()\n}\n",
	}), StdDir: writeTree(t, std)})
	var got []*diag.Diagnostic
	for _, d := range r.Report.Diags {
		got = append(got, d.Diag)
	}
	if len(got) != 1 || got[0].Code != diag.E0202 || fact(got[0], "reason") != "needs-cimport" {
		t.Fatalf("want one E0202 needs-cimport:\n%s", r.Report.Text(r.Sources))
	}
	d := got[0]
	if p := r.Sources.File(d.At.File).Path; !strings.HasSuffix(p, "src/a.mk") {
		t.Errorf("cited in %s, want src/a.mk", p)
	}
	if !strings.Contains(d.Message, "std.db.user") || !strings.Contains(d.Message, "C import sqlite3") {
		t.Errorf("message %q", d.Message)
	}
}

// TestCStd_LibcHeaders: a C17 function std.libc defines gets the
// standard's prototype in its header, another export extern the
// mechanical one in stdlib.h, and string.h always declares the support
// pack's four functions.
func TestCStd_LibcHeaders(t *testing.T) {
	protos := []libcProto{
		{name: "strlen", header: "string.h", text: "size_t strlen(const char *);\n"},
		{name: "k_hook", header: "stdlib.h", text: "int k_hook(int);\n"},
	}
	h := libcHeaders(protos, "windows")
	for _, name := range libcHeaderNames {
		if _, ok := h[name]; !ok {
			t.Errorf("no %s", name)
		}
	}
	for file, want := range map[string]string{
		"string.h": "void *memcpy(void *, const void *, size_t);",
		"stdlib.h": "int k_hook(int);",
		"stdio.h":  "#define FILENAME_MAX 260",
		"assert.h": "#ifdef NDEBUG",
	} {
		if !strings.Contains(h[file], want) {
			t.Errorf("%s lacks %q:\n%s", file, want, h[file])
		}
	}
	if !strings.Contains(h["string.h"], "size_t strlen(const char *);") {
		t.Errorf("string.h:\n%s", h["string.h"])
	}
	if !sameShape([]string{"size_t", "void *"}, c17Libc["strlen"].types) || sameShape([]string{"int", "void *"}, c17Libc["strlen"].types) {
		t.Error("sameShape")
	}
}

// TestCStd_SqliteLibversion: the sqlite amalgamation as a CSource with the
// flags that build it without an OS layer, against a copy of the tree's std,
// whose std.libc exports C's names, and std.libc's generated headers,
// builds, and Mk code calls sqlite3_libversion. Skips when the amalgamation
// is not in the evidence directory beside the repository ($MK_EVIDENCE or
// Mk-evidence), since it is kept out of the repository.
func TestCStd_SqliteLibversion(t *testing.T) {
	tools := needTools(t)
	ev := os.Getenv("MK_EVIDENCE")
	if ev == "" {
		ev = filepath.Join("..", "..", "..", "Mk-evidence")
	}
	vendor := filepath.Join(ev, "vendor", "sqlite")
	files := map[string]string{}
	for _, f := range []string{"sqlite3.c", "sqlite3.h"} {
		b, err := os.ReadFile(filepath.Join(vendor, f))
		if err != nil {
			t.Skipf("the sqlite amalgamation is not at %s: %v", vendor, err)
		}
		files[f] = string(b)
	}
	std := realStdWith(t, nil)
	flags := `"-DSQLITE_OS_OTHER=1", "-DSQLITE_THREADSAFE=0", "-DSQLITE_OMIT_LOCALTIME", "-DSQLITE_DEFAULT_MEMSTATUS=0", ` +
		`"-DSQLITE_TEMP_STORE=3", "-DSQLITE_WITHOUT_MSIZE", "-DSQLITE_DISABLE_INTRINSIC", "-DSQLITE_OMIT_SEH"`
	files["pkg.mk"] = manifest(map[string]string{"name": `"p"`, "bins": `[pkg.Bin(name: "p", root: ".", link_script: "", cpu: "", subsystem: "console")]`, "unsafe_modules": `["."]`,
		"c_sources": `[pkg.CSource(path: "sqlite3.c", flags: [` + flags + `])]`,
		"c_imports": `[pkg.CImport(name: "sqlite3", headers: ["sqlite3.h"], defines: [], macros: [], vendor: false)]`})
	files["main.mk"] = `import c.sqlite3

export extern sqlite3_os_init(): i32 {
    return 0
}

export extern sqlite3_os_end(): i32 {
    return 0
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var n: i32 = 0
    var v := ""
    unsafe "sqlite3_libversion is a static NUL-terminated string" {
        n = sqlite3.sqlite3_libversion_number()
        v = str.FromC(sqlite3.sqlite3_libversion())
    }
    try Print(mut out, "{n} {v}\n", (n: n, v: v))
}
`
	dir := writeTree(t, files)
	s, err := NewSession(Config{Root: dir, StdDir: std, Tools: tools, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r, paths, err := s.Build(filepath.Join(t.TempDir(), "p"+exeSuffix(HostTarget())))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 || len(paths) != 1 {
		t.Fatalf("build:\n%s", r.Report.Text(r.Sources))
	}
	out, errOut, code := runExe(t, HostTarget(), paths[0], "")
	h := files["sqlite3.h"]
	i := strings.Index(h, "#define SQLITE_VERSION ")
	if i < 0 {
		t.Fatal("sqlite3.h has no SQLITE_VERSION")
	}
	ver := strings.Trim(strings.Fields(h[i:])[2], `"`)
	if code != 0 || !strings.HasSuffix(strings.TrimSpace(out), " "+ver) {
		t.Errorf("exit %d, stdout %q, stderr %q; want ... %s", code, out, errOut, ver)
	}
}
