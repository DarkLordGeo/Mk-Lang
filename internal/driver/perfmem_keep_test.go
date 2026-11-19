// Why: the language server needs full syntax for the files a user has open
// without giving up cache hits for every other module.
// What: TestPerfMemory_KeepSyntaxFor.
// How: checks a package warm with Config.KeepSyntaxFor naming one file and
// compares which modules loaded whole and which hit the cache.

package driver

import (
	"slices"
	"testing"
)

// TestPerfMemory_KeepSyntaxFor: for the language server's latency,
// Config.KeepSyntaxFor loads whole only the modules with a named file; every
// other module is still a cache hit on a warm check, where KeepSyntax would
// load every package module whole.
func TestPerfMemory_KeepSyntaxFor(t *testing.T) {
	dir := writeTree(t, pkgFiles(map[string]string{
		"lib/l.mk":   "export Two(): int {\n    return 2\n}\n",
		"app/app.mk": "import lib\n\nexport Use(): int {\n    return lib.Two()\n}\n",
	}))
	cache := t.TempDir()
	check := func(keep func(string) bool) Result {
		t.Helper()
		s, err := NewSession(Config{Root: dir, CacheDir: cache, KeepSyntaxFor: keep})
		if err != nil {
			t.Fatal(err)
		}
		res := s.Check()
		if len(res.Report.Diags) > 0 {
			t.Fatalf("check:\n%s", res.Report.Text(res.Sources))
		}
		return res
	}
	check(nil) // cold: fills the cache
	if res := check(nil); !slices.Contains(res.Trace.ModulesFromCache, "lib") || slices.Contains(pkgOnly(res.Trace.ModulesResolved), "lib") {
		t.Fatalf("warm check without KeepSyntaxFor: resolved %q, from the cache %q", res.Trace.ModulesResolved, res.Trace.ModulesFromCache)
	}
	res := check(func(p string) bool { return p == "lib/l.mk" })
	if !slices.Contains(pkgOnly(res.Trace.ModulesResolved), "lib") || slices.Contains(res.Trace.ModulesFromCache, "lib") {
		t.Fatalf("KeepSyntaxFor lib/l.mk: lib resolved %q, from the cache %q", res.Trace.ModulesResolved, res.Trace.ModulesFromCache)
	}
	if !slices.Contains(res.Trace.ModulesFromCache, "app") {
		t.Fatalf("KeepSyntaxFor lib/l.mk loaded app whole too: resolved %q, from the cache %q", res.Trace.ModulesResolved, res.Trace.ModulesFromCache)
	}
}
