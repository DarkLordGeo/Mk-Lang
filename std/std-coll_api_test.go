// Why: std.coll's public surface must match the language reference's API
// listing, and TestNoUnlistedPublicNames in std-mem_api_test.go does not cover std.coll.
// What: TestSTDCOLL_APIMatchesReference, both directions for std.coll.
// How: reuses std-mem_api_test.go's parser (parseReferenceAPI, moduleDecls,
// normalizeFor), so both tests judge a line the same way.

package std

import (
	"slices"
	"strings"
	"testing"
)

// TestSTDCOLL_APIMatchesReference: both directions for std.coll.
// Every `// std.coll` line of the language reference's API listing is declared as
// written, nothing is exported without a line, and every type the
// reference's import table names for the module is declared.
func TestSTDCOLL_APIMatchesReference(t *testing.T) {
	const mod = "std.coll"
	var want []string // the reference's lines, as std.coll writes them
	for _, it := range parseReferenceAPI(t) {
		if it.mod != mod {
			continue
		}
		if it.block {
			t.Errorf("the reference has a block for std.coll (%q); this test compares one-line items only", it.text)
			continue
		}
		want = append(want, normalizeFor(mod, it.text, nil))
	}
	if len(want) == 0 {
		t.Fatal("the reference routes no line to std.coll")
	}
	types := moduleTable(t)[mod]
	if len(types) == 0 {
		t.Fatal("the module map has no name for std.coll")
	}
	decls := moduleDecls(t, mod)
	if len(decls) == 0 {
		t.Fatal("std/coll declares no export (run from the std directory)")
	}
	have := map[string]bool{}
	for _, d := range decls {
		if blockKind(d.text) || strings.HasPrefix(d.text, "type ") {
			// A type: the import table names it, and the API listing has no block for it.
			if n := blockName(d.text); !slices.Contains(types, n) {
				t.Errorf("std.coll exports type %s (%s), which the module map does not list", n, d.file)
			}
			continue
		}
		have[d.text] = true
		if !slices.Contains(want, d.text) {
			t.Errorf("std.coll exports a name the std.coll reference does not list:\n    export %s   (%s)", d.text, d.file)
		}
	}
	// An impl method satisfies its line (HeapDeque[T].Free is impl[T] Free
	// for HeapDeque[T]), as TestAPIMatchesReference counts it; it is never unlisted.
	for _, d := range implMethodDecls(t, mod) {
		have[d.text] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("a line of the std.coll reference is not declared in std/coll:\n    want: export %s", w)
		}
	}
	// Every type the import table lists is declared.
	for _, n := range types {
		found := false
		for _, d := range decls {
			if blockKind(d.text) && blockName(d.text) == n {
				found = true
			}
		}
		if !found {
			t.Errorf("the module map names std.coll's %s, which std/coll does not declare", n)
		}
	}
}
