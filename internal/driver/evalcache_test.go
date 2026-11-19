// Why: a warm check must give the same answer as a cold one, also when an
// edit in one module changes what a diagnostic in another lists.
// What: end-to-end tests over the module and function caches
// (frontcache.go, funccache.go): member lists in E0203, private fields
// blocking construction, and eval traces after an edit.
// How: each checks cold, edits, checks warm in the same cache and cold in
// a fresh one, and wants the same --json.

package driver

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// d07Pkg is a two-module package: a imports b; a.f does what the row
// needs, a.g reads nothing of b.
func d07Pkg(b, f string) map[string]string {
	return map[string]string{
		"pkg.mk": manifest(nil),
		"b/b.mk": b,
		"a/a.mk": "import b\n\n" + f + "\nexport Other(x: int): int {\n    return x + 1\n}\n",
	}
}

// TestReadMembersForE0203: a.f calls t.Frob() on a b.T, an
// E0203 that lists T's members. Adding the method T.Bar in b makes the
// warm --json list Bar, as a cold check does.
func TestReadMembersForE0203(t *testing.T) {
	b := "export struct T {\n    export a: u32\n}\n\nexport T.Get(&self): u32 {\n    return self.a\n}\n"
	f := "export F(t: &b.T): u32 {\n    return t.Frob()\n}\n"
	warm, cold := warmAfter(t, d07Pkg(b, f), "b/b.mk", func(s string) string {
		return s + "\nexport T.Bar(&self): u32 {\n    return 0\n}\n"
	})
	if got := codes(cold); !slices.Equal(got, []string{"E0203"}) {
		t.Fatalf("cold codes %q, want E0203\n%s", got, cold.Report.Text(cold.Sources))
	}
	if !strings.Contains(string(jsonOf(t, cold)), "Bar") {
		t.Fatalf("the cold E0203 does not list Bar: %s", jsonOf(t, cold))
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
}

// TestPrivateFieldBlocksForeignConstruction: a.F builds
// b.T(a: 1, b: 2); adding a private c: u8 (size and alignment unchanged)
// makes the construction an error, warm as cold, and a.Other, which reads
// nothing of T, is not rechecked.
func TestPrivateFieldBlocksForeignConstruction(t *testing.T) {
	b := "export struct T {\n    export a: u64\n    export b: u8\n}\n"
	f := "export F(): u64 {\n    t := b.T(a: 1, b: 2)\n    return t.a\n}\n"
	warm, cold := warmAfter(t, d07Pkg(b, f), "b/b.mk", replace("    export b: u8\n", "    export b: u8\n    c: u8\n"))
	if len(cold.Report.Diags) == 0 {
		t.Fatal("the cold check of the edited source is clean; a construction naming no private field must fail")
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
	checked := pkgItems(warm.Trace.ItemsChecked)
	if !slices.Contains(checked, "a.F") || slices.Contains(checked, "a.Other") {
		t.Errorf("rechecked %q: want a.F and not a.Other", checked)
	}
}

// TestEvalTraceRebasesOrStale: const N = g.Helper(), and
// Helper divides by zero, an E0901 whose trace points into g. Editing
// "return a / 0" to "return 0 + a / 0" moves the trace's tokens; the warm
// --json equals a cold check's.
func TestEvalTraceRebasesOrStale(t *testing.T) {
	files := map[string]string{
		"pkg.mk": manifest(nil),
		"g/g.mk": "export Helper(): int {\n    a := 1\n    return a / 0\n}\n",
		"a/a.mk": "import g\n\nexport const N: int = g.Helper()\n\nexport Other(x: int): int {\n    return x + 1\n}\n",
	}
	warm, cold := warmAfter(t, files, "g/g.mk", replace("return a / 0", "return 0 + a / 0"))
	if got := codes(cold); !slices.Contains(got, "E0901") {
		t.Fatalf("cold codes %q, want an E0901\n%s", got, cold.Report.Text(cold.Sources))
	}
	if !bytes.Equal(jsonOf(t, warm), jsonOf(t, cold)) {
		t.Errorf("warm --json %s\ncold %s", jsonOf(t, warm), jsonOf(t, cold))
	}
}
