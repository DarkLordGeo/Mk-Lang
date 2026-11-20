// Why: an operator over integer literals whose run-time operator panics
// must be E0303 at check time, with the run time's verdict exactly: each
// step at the type it runs in, wrapping operators exempt, any operand that
// is not a constant left to run time.
//
// What: TestE0303_ConstOperands, TestE0303_ConstOperandsPanic and
// TestFoldMinByMinusOne.
//
// How: checks small bodies and compares codes, and the panic fact the
// diagnostic names. An imported const operand needs the driver's const
// values; the conformance unit E0303_imported_const covers it.

package check

import "testing"

func TestE0303_ConstOperands(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"sum", "F(): u8 {\n\treturn 200 + 100\n}\n", "E0303@2:9"},
		{"product", "F(): i32 {\n\treturn 70000 * 70000\n}\n", "E0303@2:9"},
		{"unsigned underflow", "F(): u32 {\n\treturn 0 - 1\n}\n", "E0303@2:9"},
		{"each step at its type", "F(): u8 {\n\treturn 200 + 100 - 100\n}\n", "E0303@2:9"},
		{"step after a sound one", "F(): u8 {\n\treturn 100 + (200 + 100)\n}\n", "E0303@2:16"},
		{"divide by zero", "F(): int {\n\treturn 7 / 0\n}\n", "E0303@2:9"},
		{"remainder by a folded zero", "F(): int {\n\treturn 7 % (1 - 1)\n}\n", "E0303@2:9"},
		{"minimum by minus one", "F(): i8 {\n\treturn -128 / -1\n}\n", "E0303@2:9"},
		{"minimum remainder minus one", "F(): i8 {\n\treturn -128 % -1\n}\n", "E0303@2:9"},
		{"shift past the width", "F(): int {\n\treturn 1 << 64\n}\n", "E0303@2:9"},
		{"negated minimum", "F(): i8 {\n\treturn -(-128)\n}\n", "E0303@2:9"},
		{"in a comparison", "F(): bool {\n\treturn 9223372036854775807 + 1 > 0\n}\n", "E0303@2:9"},
		{"a literal that does not fit, once", "F(): u8 {\n\treturn 300 + 1\n}\n", "E0303@2:9"},
		{"fits", "F(): u8 {\n\treturn 200 + 55\n}\n", ""},
		{"untyped is int", "F(): int {\n\tx := 200 + 100\n\treturn x\n}\n", ""},
		{"wrapping", "F(): u8 {\n\treturn 200 +% 100 -% 255 *% 3\n}\n", ""},
		{"shift drops bits", "F(): u8 {\n\treturn 255 << 1\n}\n", ""},
		{"complement", "F(): u8 {\n\treturn ~0 - 1\n}\n", ""},
		{"variable operand", "F(v: u8): u8 {\n\treturn v + 100 + 200\n}\n", ""},
		{"float", "F(): f64 {\n\treturn 1e308 * 10.0\n}\n", ""},
		{"byte literal", "F(): u8 {\n\treturn b'a' + 200\n}\n", ""},
		{"call operand", "F(): int {\n\treturn 8 / (SizeOf[u32]() - 4)\n}\n", ""},
		{"const operand", "const Z = 0\n\nF(): int {\n\treturn 8 / Z\n}\n", ""},
		// Judged wherever it is written, as a literal that does not fit is.
		{"code that never runs", "F(): u8 {\n\tvar x: u8 = 0\n\tif false {\n\t\tx = 200 + 100\n\t}\n\treturn x\n}\n", "E0303@4:7"},
		// The evaluator judges these initializers (E0901, and the decided
		// side of && is never evaluated); this harness has no evaluator.
		{"global initializer", "var G: u8 = 200 + 100\n", ""},
		{"global initializer short-circuit", "var G: bool = false && 1 / 0 > 0\n", ""},
		{"const initializer", "const K: u8 = 200 + 100\n", ""},
	})
}

// TestFoldMinByMinusOne: the folder of const arguments and array lengths
// judges the type's minimum by -1 as a zero divisor, `%` too, typed or
// untyped, as the evaluator and the run time do.
func TestFoldMinByMinusOne(t *testing.T) {
	const a = "const A: int = -9223372036854775807 - 1\n\n"
	for _, tc := range []struct{ name, src, want, panic string }{
		{"remainder", a + "struct S {\n\tx: [A % -1]u8\n}\n", "E0901@4:6", "divide by zero"},
		{"quotient", a + "struct S {\n\tx: [A / -1]u8\n}\n", "E0901@4:6", "divide by zero"},
		{"untyped remainder", "struct S {\n\tx: [(-9223372036854775807 - 1) % -1]u8\n}\n", "E0901@2:6", "divide by zero"},
		{"untyped quotient", "struct S {\n\tx: [(-9223372036854775807 - 1) / -1]u8\n}\n", "E0901@2:6", "divide by zero"},
		{"other divisor", a + "struct S {\n\tx: [A % -2 + 1]u8\n}\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true)
			b := checkBody(t, w, tc.want, tc.src)
			if tc.panic == "" {
				return
			}
			ds := append(b.diags(), b.bodyDiags(t)...)
			if got := fact(ds[0], "panic"); got != tc.panic {
				t.Errorf("panic %q, want %q", got, tc.panic)
			}
		})
	}
}

// TestE0303_ConstOperandsPanic: the diagnostic names the run-time panic,
// `divide by zero` for the minimum by -1 as for a zero divisor.
func TestE0303_ConstOperandsPanic(t *testing.T) {
	for _, tc := range []struct{ name, src, panic string }{
		{"overflow", "F(): u8 {\n\treturn 200 + 100\n}\n", "overflow"},
		{"zero", "F(): int {\n\treturn 7 / 0\n}\n", "divide by zero"},
		{"minimum by minus one", "F(): i64 {\n\treturn -9223372036854775808 / -1\n}\n", "divide by zero"},
		{"shift", "F(): u8 {\n\treturn 1 >> 8\n}\n", "shift amount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, true)
			b := checkBody(t, w, "E0303@2:9", tc.src)
			if got := fact(b.bodyDiags(t)[0], "panic"); got != tc.panic {
				t.Errorf("panic %q, want %q", got, tc.panic)
			}
		})
	}
}
