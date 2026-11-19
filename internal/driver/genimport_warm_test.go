// Why: a generator module's quote may name an aliased import, and a cache
// hit must bind imports as the source does.
// What: TestWarmAliasedGeneratorImport and
// TestImportEditMissesGeneratorModule over dg7r5Tree.
// How: checks the tree cold and warm, and after edits of the generator's
// import line, each against a fresh cache.

package driver

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// dg7r5Tree is a user generator module whose quote names an aliased import
// (`import l = a.lib`, quote `l.One()`), and an app module that runs it.
func dg7r5Tree(t *testing.T) string {
	return writeTree(t, map[string]string{
		"pkg.mk":       libManifest(),
		"a/lib/lib.mk": "export One(): i32 {\n    return 1\n}\n",
		"b/lib/lib.mk": "export Two(): i32 {\n    return 2\n}\n",
		"gens/gens.mk": "import l = a.lib\n\nexport Make(g: mut Gen, t: Type) {\n    g.Emit(quote {\n        export $(t.Name()).Val(self): i32 {\n            return l.One()\n        }\n    })\n}\n",
		"app/app.mk":   "import gens\n\nexport struct Point {\n    export x: i32\n}\n\ngen gens.Make(Point)\n\nexport Use(p: &Point): i32 {\n    return p.Val()\n}\n",
	})
}

// TestWarmAliasedGeneratorImport: a module hit of a generator module
// keeps its files' import bindings (resolve.Session.LoadWith from the
// parse summaries), so a quote naming an aliased import checks warm as it
// does cold, with the generator module from the cache. A hit through
// Session.Load, which binds each import by its path's last segment, would
// make the warm check E0905 "unknown name `l`".
func TestWarmAliasedGeneratorImport(t *testing.T) {
	dir := dg7r5Tree(t)
	cfg := Config{Root: dir, CacheDir: t.TempDir()}
	cold := frontCheck(t, cfg)
	clean(t, cold)
	warm := frontCheck(t, cfg)
	clean(t, warm)
	if !slices.Contains(warm.Trace.ModulesFromCache, "gens") {
		t.Fatalf("gens did not come from the cache (the case is not exercised): %q", warm.Trace.ModulesFromCache)
	}
	if string(jsonOf(t, warm)) != string(jsonOf(t, cold)) {
		t.Errorf("warm --json %s, cold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
}

// TestImportEditMissesGeneratorModule: editing only the generator
// module's import line, its quote unchanged, misses that module, and the
// warm result equals a check of the edited tree into a fresh cache. Re-alias
// (l to m: E0208 at the import, E0905 at the gen line) and re-point (a.lib
// to b.lib: E0905, no item One).
func TestImportEditMissesGeneratorModule(t *testing.T) {
	for _, edit := range []struct{ name, line string }{
		{"realias", "import m = a.lib"},
		{"repoint", "import l = b.lib"},
	} {
		t.Run(edit.name, func(t *testing.T) {
			dir := dg7r5Tree(t)
			cfg := Config{Root: dir, CacheDir: t.TempDir()}
			clean(t, frontCheck(t, cfg))
			p := filepath.Join(dir, "gens", "gens.mk")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			src := edit.line + string(b[len("import l = a.lib"):])
			if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			warm := frontCheck(t, cfg)
			if slices.Contains(warm.Trace.ModulesFromCache, "gens") {
				t.Errorf("gens came from the cache after its import line changed: %q", warm.Trace.ModulesFromCache)
			}
			fresh := frontCheck(t, Config{Root: dir, CacheDir: t.TempDir()})
			if len(fresh.Report.Diags) == 0 {
				t.Fatalf("the edited tree checks clean; the edit should break the quote")
			}
			if string(jsonOf(t, warm)) != string(jsonOf(t, fresh)) {
				t.Errorf("warm after the edit --json %s, fresh cold %s", jsonOf(t, warm), jsonOf(t, fresh))
			}
		})
	}
}
