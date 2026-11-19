// Why: a value `if T is X` must instantiate both at a T that is X and at one
// that is not, with a valid body each time.
// What: TestTypeTestValueInstances, on MIR lower built from source.
// How: collects both instances with mir.Validate on.

package mono

import "testing"

// TestTypeTestValueInstances: the binding, return and assignment forms
// of a value `if T is X`, instantiated at int and bool, collect and validate.
func TestTypeTestValueInstances(t *testing.T) {
	const roots = "H(): int {\n    var n := 0\n    if y := Opt(3) {\n        n += y\n    }\n    if y := Opt(true) {\n        _ = y\n        n += 10\n    }\n" +
		"    _ = Twice(true)\n    return n + Twice(6)\n}\n"
	for _, c := range []struct{ name, src string }{
		{"binding", "Opt[T: Plain](v: T): ?T {\n    x: ?T = if T is int { none } else { some(v) }\n    return x\n}\n" +
			"Twice[T: Plain](v: T): T {\n    x := if T is int { v + v } else { v }\n    return x\n}\n"},
		{"return", "Opt[T: Plain](v: T): ?T {\n    return if T is int { none } else { some(v) }\n}\n" +
			"Twice[T: Plain](v: T): T {\n    return if T is int { v + v } else { v }\n}\n"},
		{"assignment", "Opt[T: Plain](v: T): ?T {\n    var x: ?T = none\n    x = if T is int { none } else { some(v) }\n    return x\n}\n" +
			"Twice[T: Plain](v: T): T {\n    var x := v\n    x = if T is int { v + v } else { v }\n    return x\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sw := newSrcWorld(t, c.src+roots)
			in, ds := sw.collect(t, Options{}, sw.limitRoots()...)
			if len(ds) != 0 {
				t.Fatalf("diagnostics: %v", ds)
			}
			for _, name := range []string{"Opt", "Twice"} {
				if n := len(instancesOf(in, sw.item(t, name))); n != 2 {
					t.Errorf("%d instances of %s, want 2 (int, bool):\n%s", n, name, summary(in))
				}
			}
		})
	}
}
