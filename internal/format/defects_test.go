// Why: the formatter once rewrote some valid layouts found by dogfooding
// programs (or-patterns, asm operands, cpu clause lines).
// What: regression tests, each named after the dogfood defect it closes.
// How: wantFixed checks that the input is a fixed point of mustFormat; some
// tests also check that the old wrong layout is formatted to the right one.

package format

import "testing"

func wantFixed(t *testing.T, in string) {
	t.Helper()
	if got := mustFormat(t, in); got != in {
		t.Errorf("got:\n%s\nwant:\n%s", got, in)
	}
}

// TestLoxOrPatternSpaced: `.A | .B` keeps a space on both sides of
// the bar (also seen in the notes and kvd programs); it was `.A |.B`. An
// error set's tight `A|B!T` is unchanged.
func TestLoxOrPatternSpaced(t *testing.T) {
	wantFixed(t, "F(o: Op): int {\n    match o {\n        .Plus | .Minus { 1 }\n        .Set | .Del | .Get {\n            return 2\n        }\n    }\n}\n")
	if got := mustFormat(t, "F(o: Op): int {\n    match o {\n        .Plus |.Minus { 1 }\n    }\n}\n"); got != "F(o: Op): int {\n    match o {\n        .Plus | .Minus { 1 }\n    }\n}\n" {
		t.Errorf("got:\n%s", got)
	}
	wantFixed(t, "F(): A|B!int {\n    return 1\n}\n")
}

// TestKernelAsmAndSection: `asm(`, an operand's `in(reg)` and the
// space before an initializer's `section` are kept; they were `asm (`,
// `in (reg)` and `]section`. A `for x in (e)` keeps its space.
func TestKernelAsmAndSection(t *testing.T) {
	wantFixed(t, "var Table: [2]u32 = [1, 2] section \".data.x\"\n\nPause(x: u64) {\n    unsafe \"pause\" {\n        asm(\"pause\", in(reg) x, in(\"eax\") x, options(nomem, nostack))\n    }\n    for y in (xs) {\n        G(y)\n    }\n}\n")
	if got := mustFormat(t, "P(x: u64) {\n    asm (\"pause\", in (reg) x)\n}\n"); got != "P(x: u64) {\n    asm(\"pause\", in(reg) x)\n}\n" {
		t.Errorf("got:\n%s", got)
	}
	wantFixed(t, "var b: []u8 = []\n")
}

// TestImgprocCpuContinuation: a signature's cpu line, and a comment
// between its clause lines, keep the continuation indent that requires has;
// both were moved to column 0.
func TestImgprocCpuContinuation(t *testing.T) {
	wantFixed(t, "Twice(n: int): int\n    // a comment between continuation lines\n    requires n > 0\n    cpu \"x86-64-v3\"\n{\n    return n * 2\n}\n")
	wantFixed(t, "A() {\n}\n\n// B's comment\nB() {\n}\n")
}
