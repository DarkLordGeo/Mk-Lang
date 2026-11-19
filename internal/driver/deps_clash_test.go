// Why: two majors of one C library, missing checksums and conflicting
// replaces each break a build in ways the user must be told about.
// What: tests of symbol-clash, sum-missing and --locked, the replace
// reason, the C definitions scan, and pkg.sum on a vendored source build.
// How: writes packages (with a git server where they fetch), builds them
// and compares the E0202 reasons.

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two majors of one source that both define one C symbol: the second Dep
// is E0202 symbol-clash naming the symbol and both versions. Distinct
// symbols coexist.
func TestDep_TwoMajorsSymbolClash(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	ext := func(name string) string {
		return "export extern " + name + "(n: i32): i32 {\n    return n\n}\n"
	}
	gs.tag("someone/x", "1.0.0", map[string]string{"pkg.mk": depManifest("x"), "src/x.mk": ext("x_twice")})
	gs.tag("someone/x", "2.0.0", map[string]string{"pkg.mk": depManifest("x"), "src/x.mk": ext("x_twice")})
	gs.tag("someone/x", "3.0.0", map[string]string{"pkg.mk": depManifest("x"), "src/x.mk": ext("x3_twice")})
	gs.tag("someone/y", "1.0.0", map[string]string{"pkg.mk": "const Package = pkg.Package(\n    name: \"y\",\n    asm_sources: [pkg.AsmSource(path: \"a.s\")],\n)\n", "a.s": ".globl y_sym\ny_sym:\n    ret\n"})
	gs.tag("someone/y", "2.0.0", map[string]string{"pkg.mk": "const Package = pkg.Package(\n    name: \"y\",\n    c_sources: [pkg.CSource(path: \"c.c\", flags: [])],\n)\n", "c.c": "static int helper(void) { return 1; }\nint y_sym(void) { return helper(); }\n"})
	x := gs.source("someone/x")
	y := gs.source("someone/y")
	check := func(deps ...string) Result {
		ws := writeTree(t, map[string]string{
			"app/pkg.mk": grantRoot("app", `"`+x+`", "`+y+`"`, deps...),
			"app/a.mk":   "export A(): int {\n    return 1\n}\n",
		})
		return depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, ""))
	}
	r := check(srcDep("x1", x, "1.0.0"), srcDep("x2", x, "2.0.0"))
	e0202(t, r, "symbol-clash", "pkg.mk:")
	if m := r.Report.Diags[0].Diag.Message; !strings.Contains(m, "x_twice") || !strings.Contains(m, "v1.0.0") || !strings.Contains(m, "v2.0.0") {
		t.Errorf("message %q", m)
	}
	r = check(srcDep("y1", y, "1.0.0"), srcDep("y2", y, "2.0.0"))
	e0202(t, r, "symbol-clash", "pkg.mk:")
	for _, d := range check(srcDep("x1", x, "1.0.0"), srcDep("x3", x, "3.0.0")).Report.Diags {
		if fact(d.Diag, "reason") == "symbol-clash" {
			t.Errorf("distinct symbols: %s", d.Diag.Message)
		}
	}
}

// A version with no pkg.sum line, met by an offline build or under
// --locked, is E0202 sum-missing (not offline); with every line present,
// --locked builds and writes nothing.
func TestSum_MissingOfflineAndLocked(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": depManifest("app", srcDep("toml", src, "1.0.0")),
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
	})
	app := filepath.Join(ws, "app")
	locked := sourceCfg(gs, "")
	locked.Locked = true
	e0202(t, depCheck(t, app, locked), "sum-missing", "pkg.mk:")
	off := sourceCfg(gs, "")
	off.Offline = true
	e0202(t, depCheck(t, app, off), "sum-missing", "pkg.mk:")
	if _, err := os.Stat(filepath.Join(app, sumFile)); err == nil {
		t.Fatal("a refused build wrote pkg.sum")
	}
	cache := t.TempDir()
	clean(t, depCheck(t, app, sourceCfg(gs, cache)))
	before := readFile(t, filepath.Join(app, sumFile))
	locked = sourceCfg(gs, cache)
	locked.Locked = true
	clean(t, depCheck(t, app, locked))
	if readFile(t, filepath.Join(app, sumFile)) != before {
		t.Error("--locked changed pkg.sum")
	}
	// A hashless url package with no line, under --locked.
	arch := tarGzOf(t, "", iniFiles())
	srv, _ := archiveServer(t, map[string][]byte{"/ini.tar.gz": arch})
	u := urlApp(t, srv.URL+"/ini.tar.gz", "")
	e0202(t, depCheck(t, u, Config{Locked: true}), "sum-missing", "pkg.mk:")
}

// A source that reaches two identities through replace (two replace
// entries for it) is E0202 replace.
func TestReplace_TwoIdentitiesRefused(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	gs.tag("someone/toml", "1.0.0", tomlFiles("1"))
	src := gs.source("someone/toml")
	ws := writeTree(t, map[string]string{
		"app/pkg.mk": "const Package = pkg.Package(name: \"app\", deps: [" + srcDep("toml", src, "1.0.0") +
			"], replace: [pkg.Dep(name: \"" + src + "\", path: \"../a\"), pkg.Dep(name: \"" + src + "\", path: \"../b\")])\n",
		"app/a.mk":   "import toml.src\n\nexport A(): int {\n    return src.V()\n}\n",
		"a/pkg.mk":   depManifest("toml"),
		"a/src/t.mk": "export V(): int {\n    return 1\n}\n",
		"b/pkg.mk":   depManifest("toml"),
		"b/src/t.mk": "export V(): int {\n    return 2\n}\n",
	})
	e0202(t, depCheck(t, filepath.Join(ws, "app"), sourceCfg(gs, "")), "replace", "pkg.mk:")
}

func TestDep_CDefinitionsScan(t *testing.T) {
	src := `#include <stdio.h>
#define X(a) \
    (a)
/* int commented(void) { } */
static int hidden(void) { return 0; }
int proto(int);
extern int ext;
typedef struct { int a; } T;
struct S { int b; };
int counter = 0;
const char *names[3];
unsigned long long *make_thing(int n, char c) { return 0; } // "int fake(void) {"
__attribute__((noinline)) int attributed(void) { return 1; }
DEFINE(foo)
int after_macro(void) { return 2; }
int (*returns_fn(void))(int) { return 0; }
`
	got := cDefinitions(src)
	// A miss is allowed (after_macro, returns_fn); a wrong name never is.
	want := []string{"counter", "names", "make_thing", "attributed"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %q, want %q", got, want)
	}
	if g := asmGlobals(".text\n.globl a_sym\n  .global b, c\n"); strings.Join(g, ",") != "a_sym,b,c" {
		t.Errorf("asm %q", g)
	}
}

// A vendored source package's index line is checked against
// pkg.sum's line for that version when pkg.sum has one (sum-mismatch).
func TestVendor_SourceCheckedAgainstSum(t *testing.T) {
	emptyNetrc(t)
	gs := newGitServer(t)
	f := tomlFiles("10")
	f["pkg.mk"] = "const Package = pkg.Package(\n    name: \"toml\",\n)\n"
	app, toml, _ := vendorSourceApp(t, gs, f)
	sp := filepath.Join(app, sumFile)
	sum := readFile(t, sp)
	var out []string
	for _, l := range strings.Split(sum, "\n") {
		if strings.HasPrefix(l, toml+" v1.0.0 ") {
			i := strings.Index(l, "tree-sha256:")
			l = l[:i] + "tree-sha256:" + strings.Repeat("0", 64)
		}
		out = append(out, l)
	}
	if err := os.WriteFile(sp, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	e0202(t, depCheck(t, app, Config{Offline: true}), "sum-mismatch", "pkg.mk:")
}
