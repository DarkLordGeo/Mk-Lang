// Why: a value computed from a module with errors is poison; caching it, or
// a module that read it, would keep a wrong answer after the fix.
// What: TestInvalidation_PoisonNotCached, for a type error and a flow error.
// How: checks and builds a broken two-module program, fixes it, and compares
// the warm results (--json, every pack's .ll, the executable) with a cold
// build of the fixed source in a fresh cache.

package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"mk/internal/toolchain"
	"mk/internal/types"
)

// TestInvalidation_PoisonNotCached: module a has G() and a function F with a
// diagnostic; module b has const N = a.G(), used in an array type. After a
// body edit of F that leaves G alone, the warm check and build equal a cold
// check and build of the fixed source in a fresh cache: the same --json, the
// same .ll of every pack, the same executable. At module granularity "N not
// stored" is "b not stored".
//
//   - Types: F has an E0301, so module a fails; N's evaluation is poison,
//     b is not stored, and after the fix b runs again (N evaluated again).
//   - Flow: F has an E0501. A stage-7 diagnostic poisons only its own
//     body, so G is accepted, N's evaluation is
//     not poison, and b is stored. After the fix only a runs again: b's
//     read of G's body is anchored to G's own tokens (the per-item anchors
//     of frontcache.go), which the edit of F left alone, so b hits and N
//     is not evaluated again.
func TestInvalidation_PoisonNotCached(t *testing.T) {
	tools := needTools(t)
	for _, c := range []struct {
		name, broken, code string
		poisoned           bool
		warmResolved       []string
	}{
		{"Flow", "    var n := 1\n    x := mut n\n    y := mut n\n    *x += 1\n    *y += 1\n", "E0501", false, []string{"a"}},
		{"Types", "    var n := 1\n    n = true\n", "E0301", true, []string{"a", "b"}},
	} {
		t.Run(c.name, func(t *testing.T) { poisonRow(t, tools, c.broken, c.code, c.poisoned, c.warmResolved) })
	}
}

func poisonRow(t *testing.T, tools *toolchain.Tools, broken, code string, poisoned bool, warmResolved []string) {
	fixed := "    var n := 1\n    n += 1\n"
	files := map[string]string{
		"pkg.mk": manifest(map[string]string{"bins": `[pkg.Bin(name: "app", root: "b", link_script: "", cpu: "", subsystem: "console")]`}),
		"a/a.mk": "export G(): int {\n    return 3\n}\n\nexport F() {\n" + broken + "}\n",
		"b/b.mk": "import a\n\nconst N: int = a.G()\n\nexport Main(sys: &System): int {\n    var xs: [N]int = [0; N]\n    a.F()\n    return xs.Len()\n}\n",
	}
	dir := writeTree(t, files)
	cache := t.TempDir()
	cfg := Config{Root: dir, CacheDir: cache, Target: types.X86_64Windows, Tools: tools}
	cold := frontCheck(t, cfg)
	if got := codes(cold); !slices.Equal(got, []string{code}) {
		t.Fatalf("cold codes %q, want %s\n%s", got, code, cold.Report.Text(cold.Sources))
	}
	if got := slices.ContainsFunc(cold.Trace.NotStored, func(s string) bool { return strings.HasPrefix(s, "b: poison") }); got != poisoned {
		t.Errorf("not stored %q; b not stored (poison): %v, want %v", cold.Trace.NotStored, got, poisoned)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "a.mk"), []byte(strings.Replace(files["a/a.mk"], broken, fixed, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := cfg
	fresh.CacheDir = t.TempDir()
	coldFixed := frontCheck(t, fresh)
	warm := frontCheck(t, cfg)
	if len(coldFixed.Report.Diags) > 0 {
		t.Fatalf("fixed source:\n%s", coldFixed.Report.Text(coldFixed.Sources))
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, coldFixed)) {
		t.Errorf("warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, coldFixed))
	}
	if got := pkgOnly(warm.Trace.ModulesResolved); !slices.Equal(got, warmResolved) {
		t.Errorf("warm resolved %q, want %q (cold not stored %q)", got, warmResolved, cold.Trace.NotStored)
	}
	// N runs again exactly when b does.
	if !slices.Contains(cold.Trace.Evaluations, "b.N") || slices.Contains(warm.Trace.Evaluations, "b.N") != slices.Contains(warmResolved, "b") {
		t.Errorf("N not evaluated: cold %q, warm %q", cold.Trace.Evaluations, warm.Trace.Evaluations)
	}
	// The build: .ll of every pack and the executable, warm against cold.
	lls := func(c Config) (map[string]string, []byte) {
		var mu sync.Mutex
		got := map[string]string{}
		c.TestHooks.OnLL = func(name, ll string) { mu.Lock(); got[name] = ll; mu.Unlock() }
		_, exe := frontBuild(t, c, filepath.Join(t.TempDir(), "app.exe"))
		return got, exe
	}
	coldLL, coldExe := lls(fresh)
	warmLL, warmExe := lls(cfg)
	if !bytes.Equal(coldExe, warmExe) {
		t.Errorf("warm executable differs from the cold one")
	}
	var names []string
	for n := range coldLL {
		names = append(names, n)
	}
	sort.Strings(names)
	if len(warmLL) != len(coldLL) {
		t.Errorf("warm build wrote %d packs, cold %d", len(warmLL), len(coldLL))
	}
	for _, n := range names {
		if warmLL[n] != coldLL[n] {
			t.Errorf("pack %s: warm .ll differs from cold", n)
		}
	}
}
