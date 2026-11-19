// Why: an app row compares programs written to the same rules
// (tests/bench/README.md "Applications"), so a program's own files may not
// hide vector code, unsafe code or another language.
// What: TestAppRowViolations, TestAppDogfoodsAsRows and TestAppStampSkipsGen.
// How: checks the files a source names, the SIMD and unsafe checks of
// appRowViolations, the real dogfoods read as rows, and the build stamp over
// gen/ views. Split from findings_app_test.go under the 1,200-line limit.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// appProgramFiles is the program's own files of one source of an app row:
// every .mk file of an Mk package (a dogfood's whole package, nested
// packages and generated gen/ views left out), every .rs file under a
// crate's src, or the one file.
func appProgramFiles(src, lang string) ([]string, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []string{src}, nil
	}
	root, ext := src, ".mk"
	if lang == "rust" {
		root, ext = filepath.Join(src, "src"), ".rs"
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != root && ext == ".mk" {
			// A nested package, and the gen/<target>/ views mk build
			// writes (output, never read by the compiler).
			if _, err := os.Stat(filepath.Join(p, "pkg.mk")); err == nil || d.Name() == "gen" {
				return filepath.SkipDir
			}
		}
		if !d.IsDir() && strings.HasSuffix(p, ext) {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// appSimdRoutines are std.simd's routines that choose their vector form
// inside std at run time (README "SIMD"), which an app program may call.
var appSimdRoutines = []string{"IndexByte", "IndexAny", "Count", "Equal", "Compare", "ValidUtf8", "Dot", "Sum"}

var (
	appSimdImport = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?import\s+(?:([A-Za-z_][A-Za-z0-9_]*)\s*=\s*)?std\.simd\b`)
	appCpuWrite   = regexp.MustCompile(`\bcpu\s+"[^"]*"|\bCpu\(\)`)
)

// appMkVectorCode is the vector code of an Mk program's own: any std.simd
// name but appSimdRoutines, under the module's own name or the name an
// import gives it (`import v = std.simd`), a
// cpu clause or a Cpu() test.
func appMkVectorCode(code string) []string {
	var out []string
	names := []string{"simd"}
	for _, m := range appSimdImport.FindAllStringSubmatch(code, -1) {
		if m[1] != "" && !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	use := regexp.MustCompile(`\b(` + strings.Join(names, "|") + `)\.([A-Z][A-Za-z0-9]*)`)
	for _, m := range use.FindAllStringSubmatch(code, -1) {
		if !slices.Contains(appSimdRoutines, m[2]) {
			out = append(out, "std.simd's vector layer ("+m[1]+"."+m[2]+"): an app program calls std's routines and writes no vector code")
			break
		}
	}
	if m := appCpuWrite.FindString(code); m != "" {
		out = append(out, "run-time CPU dispatch of its own ("+m+")")
	}
	return out
}

// appRowViolations is the app table's contract over a row (README
// "Applications"): a README with the task, the bans of the other tables
// (otherTableViolations) on every file of the program's own source, and
// the header comment on each twin written for the row.
func appRowViolations(r *row) []string {
	return appRowViolationsLabelled(r, nil)
}

// pkgUnsafeModules is the unsafe_modules list of a pkg.mk's text.
var pkgUnsafeModules = regexp.MustCompile(`unsafe_modules:\s*\[([^\]]*)\]`)

// declaresExactly reports whether the Mk package's pkg.mk under dir
// declares exactly the unsafe modules want, in any order.
func declaresExactly(dir string, want []string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "pkg.mk"))
	if err != nil || len(want) == 0 {
		return false
	}
	m := pkgUnsafeModules.FindSubmatch(b)
	if m == nil {
		return false
	}
	var got []string
	for _, f := range strings.Split(string(m[1]), ",") {
		if f = strings.Trim(strings.TrimSpace(f), `"`); f != "" {
			got = append(got, f)
		}
	}
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

// appRowViolationsLabelled is appRowViolations for a row that labels its
// Mk package's unsafe modules (a server row's unsafe_modules and
// unsafe_why). The label passes a twinned row's unsafe
// modules only when the package declares exactly the modules labelled,
// so a module added later fails the row again.
func appRowViolationsLabelled(r *row, unsafeModules []string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(r.Dir, "README.md")); err != nil {
		out = append(out, "no README.md with the task")
	}
	twinned := slices.ContainsFunc(slices.Collect(maps.Keys(r.Sources)), func(l string) bool { return !isMkLang(l) })
	for _, lang := range slices.Sorted(maps.Keys(r.Sources)) {
		src := filepath.Join(r.Dir, r.Sources[lang])
		files, err := appProgramFiles(src, lang)
		if err != nil {
			out = append(out, fmt.Sprintf("%s: %v", lang, err))
			continue
		}
		if len(files) == 0 {
			out = append(out, fmt.Sprintf("%s: no source files under %s", lang, src))
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				out = append(out, err.Error())
				continue
			}
			vs := otherTableViolations("app", lang, string(b))
			if isMkLang(lang) {
				// std.simd's routines dispatch inside std, as Go's
				// bytes.IndexByte does: an app program may call them.
				// Vector code of its own (Vec, Mask, a cpu clause, a
				// Cpu() test) and unsafe modules are banned where the
				// row has a twin to be fair to; a row with no twin (Tier
				// A) compares Mk with itself, so they stay.
				labelled := declaresExactly(src, unsafeModules)
				vs = slices.DeleteFunc(vs, func(v string) bool {
					return strings.HasPrefix(v, "std.simd") || ((!twinned || labelled) && v == "an unsafe module")
				})
				if twinned {
					vs = append(vs, appMkVectorCode(stripComments(string(b)))...)
				}
			}
			for _, v := range vs {
				out = append(out, fmt.Sprintf("%s %s: uses %s, which the app table forbids in a program's own source", lang, filepath.Base(f), v))
			}
		}
		if isMkLang(lang) {
			continue
		}
		head := src
		if lang == "rust" && cargoDir(r) != "" {
			head = filepath.Join(src, "src", "main.rs")
		}
		if b, err := os.ReadFile(head); err == nil && !strings.HasPrefix(string(b), "// "+r.base()+",") {
			out = append(out, fmt.Sprintf("%s: %s does not open with the header comment \"// %s, ...\"", lang, filepath.Base(head), r.base()))
		}
	}
	return out
}

func TestAppRowViolations(t *testing.T) {
	root := appRepo(t, map[string]string{
		"Dogfoods/d/pkg.mk":                appPkg,
		"Dogfoods/d/src/main/main.mk":      "Main() {}\n",
		"Dogfoods/d/src/main/more.mk":      "F() {}\n",
		"Dogfoods/d/sub/pkg.mk":            strings.Replace(appPkg, "unsafe_modules: []", `unsafe_modules: ["std.raw"]`, 1),
		"tests/bench/app/a/README.md":      "# a\n",
		"tests/bench/app/a/a.c":            "// a, the C twin\nint main(void) { return 0; }\n",
		"tests/bench/app/a/a.zig":          "// a, the Zig twin\n",
		"tests/bench/app/a/rs/src/main.rs": "// a, the Rust twin\nfn main() {}\n",
	})
	dir := filepath.Join(root, "tests", "bench", "app", "a")
	r := &row{Name: "a", Table: "app", Dir: dir, Package: &pkgSpec{Path: "Dogfoods/d"},
		Sources: map[string]string{"mk": filepath.Join("..", "..", "..", "..", "Dogfoods", "d"), "c": "a.c", "zig": "a.zig", "rust": "rs"}}
	if v := appRowViolations(r); len(v) != 0 {
		t.Fatalf("a clean row: %q", v)
	}
	// std's own routines are allowed; so is vector code in a row with no
	// twin (Tier A compares Mk with itself).
	more := filepath.Join(root, "Dogfoods", "d", "src", "main", "more.mk")
	writeFile(t, more, "import std.simd\nF() { simd.IndexByte(b, 10) simd.Count(b, 10) }\n")
	if v := appRowViolations(r); len(v) != 0 {
		t.Fatalf("std.simd's routines flagged: %q", v)
	}
	writeFile(t, more, "import std.simd\nF() { var v: simd.Vec[u8, 16] }\n")
	solo := &row{Name: "a", Table: "app", Dir: dir, Package: r.Package, Sources: map[string]string{"mk": r.Sources["mk"]}}
	if v := appRowViolations(solo); len(v) != 0 {
		t.Fatalf("vector code in an Mk-only row flagged: %q", v)
	}
	writeFile(t, more, "F() {}\n")
	bad := map[string][2]string{
		"Dogfoods/d/src/main/more.mk":       {"import std.simd\nF() { var v: simd.Vec[u8, 16] }\n", "vector layer"},
		"Dogfoods/d/src/main/cpu.mk":        {"F() cpu \"avx2\" {}\n", "dispatch of its own"},
		"Dogfoods/d/pkg.mk":                 {strings.Replace(appPkg, "unsafe_modules: []", `unsafe_modules: ["std.raw"]`, 1), "unsafe"},
		"tests/bench/app/a/rs/src/main.rs":  {"// a, the Rust twin\nfn main() { unsafe {} }\n", "unsafe"},
		"tests/bench/app/a/rs/src/extra.rs": {"#[inline(always)]\nfn f() {}\n", "inlining"},
		"tests/bench/app/a/a.c":             {"int main(void) { return 0; }\n", "header"},
		"tests/bench/app/a/a.zig":           {"// a, the Zig twin\nfn f() void { @setRuntimeSafety(false); }\n", "@setRuntimeSafety"},
	}
	for p, c := range bad {
		full := filepath.Join(root, filepath.FromSlash(p))
		old, err := os.ReadFile(full)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		writeFile(t, full, c[0])
		v := appRowViolations(r)
		if !slices.ContainsFunc(v, func(s string) bool { return strings.Contains(s, c[1]) }) {
			t.Errorf("%s: %q not flagged (got %q)", p, c[1], v)
		}
		if old == nil {
			os.Remove(full)
		} else {
			writeFile(t, full, string(old))
		}
	}
}

// TestAppDogfoodsAsRows: real dogfoods read by appRowViolations as a
// twinned row and as an Mk-only row. mgrep calls only std.simd's routines
// and passes both ways; imgproc writes its own vector code with run-time
// dispatch, and kvd declares an unsafe module (src/snap), so each can be
// an Mk-only row (Tier A) and not, unchanged, a twinned one.
func TestAppDogfoodsAsRows(t *testing.T) {
	wd, _ := os.Getwd()
	root, err := repoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "README.md"), "# probe\n")
	writeFile(t, filepath.Join(dir, "probe.go"), "// probe, a Go twin\npackage main\n")
	for _, c := range []struct {
		name      string
		twinnedOK bool
	}{{"mgrep", true}, {"imgproc", false}, {"orderbook", true}, {"kvd", false}} {
		rel, err := filepath.Rel(dir, filepath.Join(root, "Dogfoods", c.name))
		if err != nil {
			t.Skip("the temporary directory and the repository are on different volumes")
		}
		solo := &row{Name: "probe", Table: "app", Dir: dir, Sources: map[string]string{"mk": rel}}
		if v := appRowViolations(solo); len(v) != 0 {
			t.Errorf("%s as an Mk-only row: %q", c.name, v)
		}
		twin := &row{Name: "probe", Table: "app", Dir: dir, Sources: map[string]string{"mk": rel, "go": "probe.go"}}
		v := appRowViolations(twin)
		if c.twinnedOK && len(v) != 0 {
			t.Errorf("%s as a twinned row: %q", c.name, v)
		}
		if !c.twinnedOK && len(v) == 0 {
			t.Errorf("%s as a twinned row: its own vector code is not flagged", c.name)
		}
		if c.name == "kvd" {
			// kvd's src/snap, labelled, passes a
			// twinned row; a label that is not exactly the package's
			// unsafe modules does not.
			if v := appRowViolationsLabelled(twin, []string{"src/snap"}); len(v) != 0 {
				t.Errorf("kvd twinned with src/snap labelled: %q", v)
			}
			for _, wrong := range [][]string{{"src/wal"}, {"src/snap", "src/wal"}} {
				if v := appRowViolationsLabelled(twin, wrong); len(v) == 0 {
					t.Errorf("kvd twinned with the label %q: its unsafe module is not flagged", wrong)
				}
			}
		}
	}
}

// TestAppStampSkipsGen: the gen/<target>/ views mk build writes into an Mk
// package are output, so they leave a column's stamp unchanged (a dogfood
// row is not rebuilt after its first build), while a source edit changes it.
func TestAppStampSkipsGen(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mk", "pkg.mk"), appPkg)
	writeFile(t, filepath.Join(dir, "mk", "src", "main", "main.mk"), "Main() {}\n")
	exe := filepath.Join(dir, "mk.exe")
	writeFile(t, exe, "mk")
	r := &row{Name: "r", Table: "app", Dir: dir, Sources: map[string]string{"mk": "mk"}}
	cmd := buildCmd{Args: []string{exe, "build"}}
	a, err := stamp(r, columns[0], cmd)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mk", "src", "gen", "x86_64-windows", "gen.mk"), "// Generated from the gen lines of src\n")
	if b, _ := stamp(r, columns[0], cmd); b != a {
		t.Fatal("a gen view changed the stamp")
	}
	writeFile(t, filepath.Join(dir, "mk", "src", "main", "main.mk"), "Main() { }\n")
	if b, _ := stamp(r, columns[0], cmd); b == a {
		t.Fatal("a source edit left the stamp unchanged")
	}
}
