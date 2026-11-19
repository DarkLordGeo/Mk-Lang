// Why: two front-end cases must end in a diagnostic, never an internal
// error: an interface method with an unknown result type, and a
// compile-time requires whose check failed.
// What: TestStage4UnknownMethodResultNoICE and TestFailedContractIsWalkLeaf.
// How: checks a small package for each case and requires the user-facing
// diagnostic and no E0000.

package driver

import (
	"slices"
	"testing"
)

// TestStage4UnknownMethodResultNoICE: an interface method whose result
// type names nothing, in a module with a gen line, is the unknown-name
// diagnostic, never stage 4's ICE "types.Table.Get: unknown type 0"
// (check stage4.go method read the type before testing NoType).
func TestStage4UnknownMethodResultNoICE(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pkg.mk": libManifest(),
		"g/g.mk": "export Make(g: mut Gen) {\n    g.Emit(quote {\n        export Helper(): int {\n            return 1\n        }\n    })\n}\n",
		"a/a.mk": "import g\n\ninterface Device {\n    Send(mut self): Nope\n}\n\ngen g.Make()\n",
	})
	r := check(t, Config{Root: dir})
	cs := codes(r)
	if slices.Contains(cs, "E0000") || len(cs) == 0 {
		t.Fatalf("codes %q, want the unknown name and no E0000:\n%s", cs, r.Report.Text(r.Sources))
	}
}

// TestFailedContractIsWalkLeaf: a compile-time requires whose check
// failed has no lowered body, so the instance-limit walk treats its
// function, and a body that calls it, as a leaf: the report holds the
// clause's own E0204 and no E0000 of the walk ("iface: no body for
// a.Ld#requires0, which a hash requires").
func TestFailedContractIsWalkLeaf(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"pkg.mk": libManifest(),
		"a/a.mk": "import std.atomic\n\nLd(a: &atomic.Atomic[i64], const o: atomic.Order): i64\n    requires o.v != 2 && o.v != 3 {\n    return a.Load(o)\n}\n\n" +
			"export Use(a: &atomic.Atomic[i64]): i64 {\n    return Ld(a, atomic.Acquire)\n}\n",
	})
	r := check(t, Config{Root: dir})
	cs := codes(r)
	if slices.Contains(cs, "E0000") || !slices.Contains(cs, "E0204") {
		t.Fatalf("codes %q, want E0204 and no E0000:\n%s", cs, r.Report.Text(r.Sources))
	}
}
