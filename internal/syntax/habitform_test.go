// Why: the forms of habitform.go share a prefix with Mk, so a report at the
// token after it ("one statement per line", "comparisons do not chain")
// would name neither the habit nor the Mk form.
// What: tests of the float, brace literal, angle call, ternary, bare
// result, dyn, anonymous literal and multiple assignment messages, and that
// the Mk forms beside them still parse.
// How: runCases asserts the code and the message; runHabits asserts the
// target and that a proven fix reparses cleanly.

package syntax

import "testing"

// TestFloatDigits: a float needs a digit on both sides of its dot.
func TestFloatDigits(t *testing.T) {
	runCases(t, []pcase{
		{name: "trailing dot", src: "F(): f64 {\n    x := 1.\n    return x\n}\n", codes: "E0101", msg: "a float needs digits on both sides of `.`: write `1.0`"},
		{name: "trailing dot in parens", src: "F(): f64 {\n    return (1.)\n}\n", codes: "E0101", msg: "write `1.0`"},
		{name: "leading dot", src: "F(): f64 {\n    x := .5\n    return x\n}\n", codes: "E0101", msg: "a float needs digits on both sides of `.`: write `0.5`"},
		{name: "leading dot argument", src: "F() {\n    G(1, .25)\n}\n", codes: "E0101", msg: "write `0.25`"},
		{name: "float", src: "F(): f64 {\n    return 1.5\n}\n"},
		{name: "range", src: "F() {\n    for i in 0..10 {\n    }\n}\n"},
		{name: "member of a literal", src: "F() {\n    x := 1.Fmt()\n}\n"},
		{name: "record index", src: "F() {\n    x := t.0\n}\n", codes: "E0101", msg: "records have names"},
	})
}

// TestHabitForms: the habits that share a prefix with Mk name the Mk form.
func TestHabitForms(t *testing.T) {
	runCases(t, []pcase{
		{name: "ternary", src: "F(c: bool, a: int, b: int): int {\n    return c ? a : b\n}\n", codes: "E1208", msg: "write `if c { a } else { b }`"},
		{name: "ternary comparison", src: "F(n: int): int {\n    return n > 0 ? 1 : 2\n}\n", codes: "E1208", msg: "write `if n > 0 { 1 } else { 2 }`"},
		{name: "map lookup", src: "F() {\n    v, ok := m[k]\n}\n", codes: "E0101", msg: "write `if v := m.Get(&k) { }`"},
		{name: "go error", src: "F() {\n    x, err := G(1)\n}\n", codes: "E0101", msg: "write `x := try G(1)`"},
		{name: "record destructure", src: "F() {\n    a, b := G()\n}\n", codes: "E0101", msg: "write `(a, b) := F()`"},
		{name: "zig anonymous literal", src: "F() {\n    G(\"{}\", .{x})\n}\n", codes: "E0101", msg: "Mk has no `.{ }` literal: pass a record, `(x)`"},
		{name: "zig named anonymous literal", src: "F() {\n    G(.{ .x = 1 })\n}\n", codes: "E0101", msg: "pass a record, `(x: 1)`"},
		{name: "dyn parameter", src: "F(w: dyn Format) {\n}\n", codes: "E1221", msg: "`gen dyn.Of(Format)` declares `FormatDyn`"},
		{name: "go struct literal", src: "F() {\n    p := Point{x: 1, y: 2}\n}\n", codes: "E0101", msg: "write `Point(x: 1, y: 2)`"},
		{name: "zig struct literal", src: "F() {\n    p := geo.Point{ .x = 1, .y = 2 }\n}\n", codes: "E0101", msg: "write `geo.Point(x: 1, y: 2)`"},
		{name: "block after a name", src: "F() {\n    if x == Limit { y = 1 }\n}\n"},
		{name: "empty block after a name", src: "F() {\n    for x in Items {\n    }\n}\n"},
		{name: "typed bind in an if body", src: "F() {\n    if a == Limit { n: int = 0 }\n}\n"},
		{name: "typed bind in a for body", src: "F() {\n    for x in Items { s: str = x }\n}\n"},
		{name: "adjacent empty if body", src: "F() {\n    if c == Max{}\n}\n"},
		{name: "struct literal in a body", src: "F() {\n    if c {\n        p := Point{x: 1}\n    }\n}\n", codes: "E0101", msg: "write `Point(x: 1)`"},
		{name: "angle call after try", src: "F(): E!int {\n    return try G<int>(1)\n}\n", codes: "E0101", msg: "write `G[int](…)`"},
		{name: "angle call", src: "F(): int {\n    return G<int>(1)\n}\n", codes: "E0101", msg: "type arguments use brackets: write `G[int](…)`"},
		{name: "comparison chain", src: "F() {\n    x := a < b < c\n}\n", codes: "E0101", msg: "comparisons do not chain"},
		{name: "go result", src: "F() int {\n    return 1\n}\n", codes: "E0101", msg: "the result type follows `:`: write `): int`"},
		{name: "flow after params", src: "F(a: str): str from a {\n    return a\n}\n"},
		// Valid Mk that shares the habits' tokens stays clean.
		{name: "contract before an empty body", src: "const Max = 10\n\nF(n: int) requires n < Max{}\n"},
		{name: "contract before a one-line body", src: "F(n: int): int requires n < Max { y: int = n\n    return y\n}\n"},
		{name: "ensures before a one-line body", src: "F(n: int): int ensures result < Max { y: int = 0\n    return y\n}\n"},
		{name: "two comparisons as arguments", src: "G(a: bool, b: bool) {\n}\n\nF(a: int, b: int, c: int, d: int) {\n    G(a<b, c>(d+1))\n}\n"},
		{name: "int member chain broken after the dot", src: "F(x: int): bool {\n    return 1.\n        Eq(&x)\n}\n"},
		{name: "two index values", src: "F() {\n    a, b := xs[i], ys[j]\n}\n", codes: "E0101", msg: "write `(a, b) := F()`"},
		{name: "nested brace literal", src: "F(): Q {\n    return Q{p: P{x: 1}}\n}\n", codes: "E0101", msg: "write `Q(field: value, …)`"},
		{name: "zig literal with a positional value", src: "F(): P {\n    return P{ .x = 1, 2 }\n}\n", codes: "E0101", msg: "write `P(field: value, …)`"},
	})
	runHabits(t, []hcase{
		{name: "pub fn", src: "pub fn F() int {\n    return 1\n}\n", code: "E1215", habits: "[E1215 E1205 E0101]", target: "export F(): int {"},
		{name: "struct literal fix", src: "F() {\n    p := Point{x: 1, y: 2}\n}\n", code: "E0101", target: "    p := Point(x: 1, y: 2)"},
		{name: "zig literal fix", src: "F() {\n    p := Point{ .x = 1 }\n}\n", code: "E0101", target: "    p := Point(x: 1)"},
		{name: "angle call target", src: "F(): int {\n    return G<int>(1)\n}\n", code: "E0101", target: "    return G[int](1)", alts: []string{"G[int]"}, wantNoFixes: true},
		{name: "dyn target", src: "F(w: dyn Format) {\n}\n", code: "E1221", target: "F(w: FormatDyn) {", alts: []string{"gen dyn.Of(Format)", "FormatDyn"}},
	})
}
