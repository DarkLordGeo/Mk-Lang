// Why: adding an invariant to a struct makes it no longer zero-valid, which
// changes verdicts in importers that never named the invariant.
// What: TestFuncReuseInvariantAdded.
// How: adds `invariant n > 0` to a foreign struct and requires the warm
// check to drop the cached accept, as a cold check does.

package driver

import (
	"bytes"
	"slices"
	"testing"
)

// TestFuncReuseInvariantAdded: a struct with an invariant is not zero-valid,
// so adding `invariant n > 0` to a foreign lib.Inner makes a cached accept
// of `Arena.Slice[lib.R]` (R holding an Inner) wrong. check records
// ReadSignature lib.Inner for the zero-valid walk; iface's mk.decl.v1 of a
// struct must therefore cover its invariants, or the warm check reuses the
// stale accept.
func TestFuncReuseInvariantAdded(t *testing.T) {
	files := map[string]string{
		"pkg.mk":     manifest(nil),
		"lib/lib.mk": "export struct Inner {\n    export n: int\n}\n\nexport struct R {\n    export i: Inner\n}\n",
		"a/a.mk": "import lib\n\nexport F(a: &Arena): OutOfMemory!void {\n    _ = try a.Slice[lib.R](1)\n}\n\n" +
			"export Other(): int {\n    return 1\n}\n",
	}
	first := frontCheck(t, Config{Root: writeTree(t, files), CacheDir: t.TempDir()})
	clean(t, first)
	warm, cold := warmAfter(t, files, "lib/lib.mk",
		replace("    export n: int\n}\n\nexport struct R", "    export n: int\n    invariant n > 0\n}\n\nexport struct R"))
	if got := codes(cold); !slices.Equal(got, []string{"E0701"}) {
		t.Fatalf("cold codes after the edit %q, want E0701\n%s", got, cold.Report.Text(cold.Sources))
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	if got := pkgItems(warm.Trace.ItemsChecked); !slices.Contains(got, "a.F") {
		t.Errorf("a.F not rechecked (rechecked %q, from cache %q)", got, pkgItems(warm.Trace.ItemsFromCache))
	}
}
