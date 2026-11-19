// Why: std.crypto's public surface must be exactly what doc/reference.md
// declares: every line as written, and nothing exported without one.
// What: TestSTDCRYPTO_APIMatchesReference.
// How: reuses std-mem_api_test.go's parser, so both tests judge a line the
// same way, as std-coll_api_test.go does for std.coll.

package std

import (
	"slices"
	"strings"
	"testing"
)

// TestSTDCRYPTO_APIMatchesReference: both directions for std.crypto.
func TestSTDCRYPTO_APIMatchesReference(t *testing.T) {
	const mod = "std.crypto"
	var want []string
	for _, it := range parseReferenceAPI(t) {
		if it.mod != mod {
			continue
		}
		if it.block {
			// Blocks (enum Curve, HashKind, SigFormat, PublicKey,
			// PrivateKey) are TestAPIMatchesReference's;
			// this test compares the one-line items.
			continue
		}
		want = append(want, normalizeFor(mod, it.text, nil))
	}
	if len(want) == 0 {
		t.Fatal("the reference routes no line to std.crypto")
	}
	types := moduleTable(t)[mod]
	if len(types) == 0 {
		t.Fatal("the module map has no name for std.crypto")
	}
	decls := moduleDecls(t, mod)
	if len(decls) == 0 {
		t.Fatal("std/crypto declares no export (run from the std directory)")
	}
	have := map[string]bool{}
	for _, d := range decls {
		if blockKind(d.text) || strings.HasPrefix(d.text, "type ") {
			if n := blockName(d.text); !slices.Contains(types, n) {
				t.Errorf("std.crypto exports type %s (%s), which the module map does not list", n, d.file)
			}
			if !strings.HasPrefix(d.text, "type ") {
				continue
			}
		}
		have[d.text] = true
		if !slices.Contains(want, d.text) {
			t.Errorf("std.crypto exports a name the std.crypto reference does not list:\n    export %s   (%s)", d.text, d.file)
		}
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("a line of the std.crypto reference is not declared in std/crypto:\n    want: export %s", w)
		}
	}
	for _, n := range types {
		found := false
		for _, d := range decls {
			// The import table names std.crypto's functions too (Hmac256,
			// X25519, Random, Equal), not only its types.
			isType := blockKind(d.text) || strings.HasPrefix(d.text, "type ")
			if isType && blockName(d.text) == n || !isType && strings.HasPrefix(d.text, n+"(") {
				found = true
			}
		}
		if !found {
			t.Errorf("the module map names std.crypto's %s, which std/crypto does not declare", n)
		}
	}
}
