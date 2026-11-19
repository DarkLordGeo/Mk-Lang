// Why: a same-module callee has no MIR yet when its caller is checked, so
// its compile-time requires must be judged without the evaluator.
// What: TestE0706_SameModuleRequiresFolded.
// How: check folds the predicate with the call's const arguments at each
// call, with no evaluator and no MIR of the predicate.

package check

import "testing"

func TestE0706_SameModuleRequiresFolded(t *testing.T) {
	t.Parallel()
	runBodyCases(t, []bodyCase{
		// tests/conformance/codes/E0706.mk
		{"const param", "Cap(const n: int): int\n\trequires n > 0\n{\n\treturn n\n}\n\nZero(): int {\n\treturn Cap(0)\n}\n", "E0706@8:9"},
		{"holds", "Cap(const n: int): int\n\trequires n > 0\n{\n\treturn n\n}\n\nOne(): int {\n\treturn Cap(1)\n}\n", ""},
		{"const generic", "F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\nK(): int {\n\treturn F[0]()\n}\n", "E0706@7:9"},
		{"const item", "const Max = 8\n\nCap(const n: int): int\n\trequires n <= Max\n{\n\treturn n\n}\n\nA(): int {\n\treturn Cap(8) + Cap(9)\n}\n", "E0706@10:18"},
		{"two params", "R(const a: int, const b: int): int\n\trequires a < b\n{\n\treturn a\n}\n\nA(): int {\n\treturn R(1, 2) + R(2, 1)\n}\n", "E0706@8:19"},
		{"folded argument", "Cap(const n: int): int\n\trequires n > 0\n{\n\treturn n\n}\n\nA(): int {\n\treturn Cap(2 - 2)\n}\n", "E0706@8:9"},
		// A predicate that does not fold (a call) is left to the evaluator
		// and unchecked here: a false accept, never a false diagnostic.
		{"call unchecked", "Pos(n: int): bool {\n\treturn n > 0\n}\n\nCap(const n: int): int\n\trequires Pos(n)\n{\n\treturn n\n}\n\nZ(): int {\n\treturn Cap(0)\n}\n", ""},
	})
	// A run-time requires (a free name that is not const) is not judged at
	// the call.
	checkBody(t, newWorld(t, true), "", "Cap(n: int): int\n\trequires n > 0\n{\n\treturn n\n}\n\nZ(): int {\n\treturn Cap(0)\n}\n")
}
