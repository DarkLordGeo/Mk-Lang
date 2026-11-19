// Why: a const that reads a const whose check failed should report the
// first failure alone, never an internal error, and never stop the rest of
// the module from building.
// What: tests that lower's E0000 on a body that reads a const with no value
// (checker.Body.PoisonReads) is dropped when that const accounts for it
// (accounting), and that an untyped const typed in stage 5 that names an
// early const (a repeat count, a const argument) gets that const's value
// from the evaluator, as typeConsts runs after initEarly.
// How: case tables of small programs, checked and run cold and warm (the
// front cache and the function cache) and across edits; each test is the
// witness of one mechanism.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// d695Run runs root with the cache at cache, and returns the report's
// "path:line:col:code" lines and the exit code (-1 when it did not run).
func d695Run(t *testing.T, root, cache string) ([]string, int) {
	t.Helper()
	s, err := NewSession(Config{Root: root, CacheDir: cache})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, code, err := s.Run(nil, RunIO{Stdout: &out, Stderr: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Report.Diags) > 0 {
		return baseLines(r), -1
	}
	return nil, code
}

// TestFailedConstColdWarmEdits: `const A = 1 + true`, `const B = A`,
// Main returning B gives A's E0301 alone, cold and warm; fixing A runs (3,
// warm), and breaking A again gives the E0301 alone (warm), so no poisoned
// body or function result of one build leaks into the next.
func TestFailedConstColdWarmEdits(t *testing.T) {
	t.Parallel()
	needTools(t)
	bad := "const A = 1 + true\nconst B = A\n\nexport Main(sys: &System): int {\n\treturn B\n}\n"
	good := "const A = 1 + 2\nconst B = A\n\nexport Main(sys: &System): int {\n\treturn B\n}\n"
	file := filepath.Join(t.TempDir(), "main.mk")
	cache := t.TempDir()
	want := []string{"main.mk:1:15:E0301"}
	step := func(name, src string, wantLines []string, wantCode int) {
		t.Helper()
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		got, code := d695Run(t, file, cache)
		if !slices.Equal(got, wantLines) || code != wantCode {
			t.Errorf("%s: diagnostics %q exit %d, want %q exit %d", name, got, code, wantLines, wantCode)
		}
	}
	step("cold", bad, want, -1)
	step("warm", bad, want, -1)
	step("fixed", good, nil, 3)
	step("broken again", bad, want, -1)
	step("broken again, warm", bad, want, -1)
}

// TestCheckColdWarm: each program's `mk check`, cold then warm, is
// exactly its own diagnostics, with no E0000.
func TestCheckColdWarm(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, src, want, text string }{
		// f14: the rest of the module lowers, so the unrelated E0901 stays.
		{"f14", "const A = 1 + true\n\nDiv(a: int, b: int): int {\n\treturn a / b\n}\n\nconst Z: int = Div(1, 0)\n\nexport Main(sys: &System): int {\n\treturn Z\n}\n",
			"main.mk:1:15:E0301 main.mk:7:7:E0901", ""},
		// c26: F reads A only in a branch lower folds, so F lowers
		// and Z's evaluation reports its E0901.
		{"c26", "const A = 1 + true\n\nF(): int {\n\tif false {\n\t\treturn A\n\t}\n\treturn 1\n}\n\nDiv(a: int, b: int): int {\n\treturn a / b\n}\n\nconst Z: int = Div(F(), 0)\n\nexport Main(sys: &System): int {\n\treturn Z\n}\n",
			"main.mk:1:15:E0301 main.mk:14:7:E0901", ""},
		// f20: a fn literal held in a const reads A; the walk treats Main,
		// which reads the const, as a leaf.
		{"f20", "const A = 1 + true\nconst F = fn(x: int): int { return x + A }\n\nexport Main(sys: &System): int {\n\treturn F(1)\n}\n",
			"main.mk:1:15:E0301", ""},
		// d1: B's count names N, whose evaluation fails (E0901);
		// N's failure accounts for the E0000s on B and Main.
		{"d1", "import std.math\n\nconst J = 1\nconst N = J + 10 / (math.OnesCount64(7) - 3)\nconst B = [7; N]\n\nexport Main(sys: &System): int {\n\treturn B[0] + 10\n}\n",
			"main.mk:4:7:E0901", ""},
		// e18: the early const K's Prepared body reads N, whose evaluation
		// fails; lower's E0000 on K (held in Prepare) and on Main are N's.
		{"e18", "import std.math\n\nconst N = 10 / (math.OnesCount64(7) - 3)\nconst K = [1; N][0]\n\nexport Main(sys: &System): int {\n\tvar a: [K]int = [2; K]\n\treturn a[0]\n}\n",
			"main.mk:3:7:E0901", ""},
		// h1: Main reads A, whose E0301 explains nothing about
		// T's step-4 Poison, so lower's E0000 at the binding t, which is not
		// at A's read, is reported.
		{"h1", "import std.math\n\nconst A = 1 + true\nconst M = math.OnesCount64(7)\nconst N = [1; M][0]\n\ntype T = [N]int\n\nexport Main(sys: &System): int {\n\tvar t: T = [4; N]\n\treturn t[0] + M + A\n}\n",
			"main.mk:3:15:E0301 main.mk:10:6:E0000", "binding t of no type"},
		// r1: A's failure is its own stage-7 E0402, which accounts
		// for the E0000s at B's count and at Main's read of B.
		{"r1", "linear struct L {\n\tx: int\n}\n\nconst A = L(x: 1).x\nconst B = [0; A]\n\nexport Main(sys: &System): int {\n\treturn B[0]\n}\n",
			"main.mk:5:11:E0402", ""},
		// r2: A is early, so its E0402 comes from Prepare.
		{"r2", "linear struct L {\n\tx: int\n}\n\nconst A = L(x: 1).x\n\nexport Main(sys: &System): int {\n\tvar a: [A]int = [0; A]\n\treturn a[0]\n}\n",
			"main.mk:5:11:E0402", ""},
		// The binding a's declared type reads K: lower's E0000 at the
		// binding is that read's (PoisonAt holds the statement).
		{"bind", "import std.math\n\nconst N = 10 / (math.OnesCount64(7) - 3)\nconst K = [1; N][0]\n\nexport Main(sys: &System): int {\n\tvar b := [2, 3]\n\tvar a: [K]int = b\n\treturn a[0]\n}\n",
			"main.mk:3:7:E0901", ""},
		// cy10: Main reads C, a member of A's E0909 cycle.
		{"cy10", "const A: int = B + 1\nconst B: int = C + 1\nconst C: int = A + 1\n\nexport Main(sys: &System): int {\n\tvar x: [C]int = [0; C]\n\treturn x[0]\n}\n",
			"main.mk:1:7:E0909", ""},
		// A typing cycle through an early const's initializer names it.
		{"cycle", "const C = [1; N][0]\nconst N = C\n\nexport Main(sys: &System): int {\n\treturn C\n}\n",
			"main.mk:1:7:E0909", "C -> N -> C"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := writeTree(t, map[string]string{"main.mk": c.src})
			cache := t.TempDir()
			for _, run := range []string{"cold", "warm"} {
				r := check(t, Config{Root: filepath.Join(dir, "main.mk"), CacheDir: cache})
				if got := strings.Join(baseLines(r), " "); got != c.want {
					t.Errorf("%s: %q, want %q\n%s", run, got, c.want, r.Report.Text(r.Sources))
				}
				if c.text != "" && !strings.Contains(r.Report.Text(r.Sources), c.text) {
					t.Errorf("%s: report does not say %q\n%s", run, c.text, r.Report.Text(r.Sources))
				}
			}
		})
	}
}

// TestRunsColdWarm: programs whose consts are typed through early
// consts run, cold then warm, with the result each program is written to
// give.
func TestRunsColdWarm(t *testing.T) {
	t.Parallel()
	needTools(t)
	cases := []struct {
		name, src string
		code      int
	}{
		// t03d: B's count is an imported call's value (initEarly before
		// typeConsts).
		{"t03d", "import std.math\n\nconst N = math.OnesCount64(7)\nconst B = [7; N]\n\nexport Main(sys: &System): int {\n\treturn B[2] + N\n}\n", 10},
		// e06: N's initializer reads J, declared after it: J is typed on
		// demand in N's early check and Prepared before N is evaluated.
		{"e06", "import std.math\n\nconst B = [7; N]\nconst N = J + math.OnesCount64(7)\nconst J = 1\n\nexport Main(sys: &System): int {\n\treturn B[2] + N\n}\n", 11},
		// c02: K is first typed for T in step 4, before the early
		// set exists, and typed again (untypeProvisional).
		{"c02", "import std.math\n\nconst B = [7; N]\nconst N = 1 + math.OnesCount64(7)\nconst K = B[0]\n\ntype T = [K]int\n\nexport Main(sys: &System): int {\n\treturn N\n}\n", 4},
		// c25: K's initializer reads B in a branch lower folds.
		{"c25", "import std.math\n\nconst Debug = false\nconst B = [7; N]\nconst N = 1 + math.OnesCount64(7)\nconst K: int = [if Debug { B[0] } else { 3 }][0]\n\nexport Main(sys: &System): int {\n\tvar a: [K]int = [2; K]\n\treturn a[0] + K + 10\n}\n", 15},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(writeTree(t, map[string]string{"main.mk": c.src}), "main.mk")
			cache := t.TempDir()
			for _, run := range []string{"cold", "warm"} {
				if got, code := d695Run(t, file, cache); got != nil || code != c.code {
					t.Errorf("%s: diagnostics %q exit %d, want exit %d", run, got, code, c.code)
				}
			}
		})
	}
}

// d695Pkg is a package with an untyped const whose repeat count names an
// early const holding an imported function's value.
func d695Pkg(n string) map[string]string {
	return pkgFiles(map[string]string{
		"src/lib/lib.mk":   "export Three(): int {\n\treturn " + n + "\n}\n",
		"src/main/main.mk": "import src.lib\n\nconst N = lib.Three()\nconst B = [7; N]\n\nexport Main(sys: &System): int {\n\treturn B[1] + N\n}\n",
	})
}

// TestEarlyConstCountColdWarm: d695Pkg runs (7 + 3), cold and warm; after
// lib.Three is edited to return 4, the warm run equals a cold run of the
// edited tree (7 + 4): B's type, which reads lib's value, is not stale.
func TestEarlyConstCountColdWarm(t *testing.T) {
	t.Parallel()
	needTools(t)
	files := d695Pkg("3")
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "p01", root: "src/main", link_script: "", cpu: "", subsystem: "console")]`})
	dir := writeTree(t, files)
	cache := t.TempDir()
	for _, run := range []string{"cold", "warm"} {
		if got, code := d695Run(t, dir, cache); got != nil || code != 10 {
			t.Errorf("%s: diagnostics %q exit %d, want exit 10", run, got, code)
		}
	}
	edited := d695Pkg("4")
	if err := os.WriteFile(filepath.Join(dir, "src", "lib", "lib.mk"), []byte(edited["src/lib/lib.mk"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, code := d695Run(t, dir, cache); got != nil || code != 11 {
		t.Errorf("warm after the edit: diagnostics %q exit %d, want exit 11", got, code)
	}
	edited["pkg.mk"] = files["pkg.mk"]
	if got, code := d695Run(t, writeTree(t, edited), t.TempDir()); got != nil || code != 11 {
		t.Errorf("cold of the edited tree: diagnostics %q exit %d, want exit 11", got, code)
	}
}

// TestTestCommandColdWarm: `mk test` over a package with an early
// const's count in a module with a test) passes cold and warm; with a
// failed untyped const read by the test, it reports the E0301 alone.
func TestTestCommandColdWarm(t *testing.T) {
	t.Parallel()
	needTools(t)
	lib := "import std.math\n\nconst N = math.OnesCount64(7)\nconst B = [7; N]\n\nexport Get(): int {\n\treturn N\n}\n\ntest \"get\" {\n\tAssert(Get() == 3)\n}\n"
	files := pkgFiles(map[string]string{
		"src/lib/lib.mk":   lib,
		"src/main/main.mk": "import src.lib\n\nexport Main(sys: &System): int {\n\treturn lib.Get()\n}\n",
	})
	files["pkg.mk"] = manifest(map[string]string{"bins": `[pkg.Bin(name: "p03", root: "src/main", link_script: "", cpu: "", subsystem: "console")]`})
	dir := writeTree(t, files)
	cache := t.TempDir()
	for _, run := range []string{"cold", "warm"} {
		rep := mkTest(t, Config{Root: dir, CacheDir: cache}, TestOptions{})
		if got := results(rep); got != "src.lib get pass" {
			t.Errorf("%s: %q, want src.lib get pass", run, got)
		}
	}
	bad := filepath.Join(t.TempDir(), "main.mk")
	src := "const A = 1 + true\n\ntest \"t\" {\n\tAssert(A == 2)\n}\n\nexport Main(sys: &System): int {\n\treturn 0\n}\n"
	if err := os.WriteFile(bad, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cache = t.TempDir()
	for _, run := range []string{"cold", "warm"} {
		s, err := NewSession(Config{Root: bad, CacheDir: cache})
		if err != nil {
			t.Fatal(err)
		}
		r, rep, err := s.Test(TestOptions{})
		if err != nil || rep != nil {
			t.Fatalf("%s: mk test: %v report %v", run, err, rep)
		}
		if got := baseLines(r); !slices.Equal(got, []string{"main.mk:1:15:E0301"}) {
			t.Errorf("%s: %q, want the E0301 alone\n%s", run, got, r.Report.Text(r.Sources))
		}
	}
}

// baseLines is lines with each path cut to its last element: a file root's
// diagnostics name its absolute path.
func baseLines(r Result) []string {
	out := lines(r)
	for i, l := range out {
		out[i] = l[strings.LastIndex(l, "/")+1:]
	}
	return out
}

// TestAccounting: lower's E0000 on a body that reads consts with no
// value is dropped only when each const accounts for it: its own unit or
// evaluation reported, or its initializer reads only consts that do. A
// const with no diagnostic anywhere (a check gap), one outside the module
// and a cycle do not, so their E0000 is reported (no silent failure, and an
// unrelated diagnostic does not hide it).
func TestAccounting(t *testing.T) {
	const (
		a types.ItemID = iota + 1 // E0301 in its own unit
		b                         // reads a
		c                         // no diagnostic anywhere
		d                         // reads c
		e                         // evaluation failed
		x                         // cycle with y
		y
		z // reported nothing, but another unit did
	)
	units := []types.ItemID{a, b, c, d, e, x, y, z}
	res := []unitResult{
		{diags: []*diag.Diagnostic{diag.New(diag.E0301, source.Span{}, "")}},
		{poisonReads: []types.ItemID{a}},
		{},
		{poisonReads: []types.ItemID{c}},
		{},
		{poisonReads: []types.ItemID{y}},
		{poisonReads: []types.ItemID{x}},
		{},
	}
	acct := newAccounting(units, nil, nil, res, map[types.ItemID]bool{e: true})
	for _, tc := range []struct {
		reads []types.ItemID
		want  bool
	}{
		{[]types.ItemID{a}, true},
		{[]types.ItemID{b}, true},
		{[]types.ItemID{e}, true},
		{[]types.ItemID{a, e}, true},
		{[]types.ItemID{c}, false},
		{[]types.ItemID{d}, false},
		{[]types.ItemID{a, c}, false},
		{[]types.ItemID{x}, false},
		{[]types.ItemID{z}, false},
		{[]types.ItemID{99}, false},
		{nil, false},
	} {
		if got := acct.covers(tc.reads); got != tc.want {
			t.Errorf("covers(%v) = %v, want %v", tc.reads, got, tc.want)
		}
	}
}

// TestUnaccountedHeldReported: a const left with no type and no
// diagnostic anywhere (a check gap, simulated by TestHooks.UntypeConst)
// does not account for lower's E0000 at a read of it, so that E0000 is
// reported: the build never ends with a body unlowered in silence.
func TestUnaccountedHeldReported(t *testing.T) {
	t.Parallel()
	src := "const A = 2\n\nexport Main(sys: &System): int {\n\tvar a: [A]int = [0; A]\n\treturn a[0] + A\n}\n"
	file := filepath.Join(writeTree(t, map[string]string{"main.mk": src}), "main.mk")
	r := check(t, Config{Root: file, NoCache: true, TestHooks: TestHooks{UntypeConst: "main.A"}})
	if got := strings.Join(baseLines(r), " "); got != "main.mk:5:16:E0000" {
		t.Errorf("%q, want lower's E0000 at the read of A\n%s", got, r.Report.Text(r.Sources))
	}
}
