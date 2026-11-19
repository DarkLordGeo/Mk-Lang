// Why: some same-module compile-time clauses over a type generic are still
// left to the entry check; these tests pin today's verdicts so a change is
// seen.
// What: TestEntryCheckRoutes.
// How: layoutCall folds `SizeOf` and `AlignOf` only of a type parameter
// bound to a closed type, so a clause that also names `SizeOf` of a closed
// type (`SizeOf[u8]()`, `SizeOf[Small]()`) or of a type built on a type
// parameter (`SizeOf[[2]T]()`, `G[W[U]]`) does not fold: it is unchecked at
// a direct concrete call, a restatement of it is demanded but never judged,
// and over `SizeOf[[2]T]()` none is demanded. Such a program, with a body
// that reads past a table under the clause, panics at the entry check in
// dev and reads past the table in release. A general fold (layoutCall over
// any type closed once the call's type arguments are bound, with
// layoutFinal's guard) would close them, and these rows would flip to E0706.

package check

import "testing"

// routesTypes are the test programs' types (lines 1-12): 23 bytes, one
// byte, and a wrapper.
const routesTypes = "struct Big23 {\n\tb: [23]u8\n}\n\nstruct Small {\n\tb: u8\n}\n\nstruct W[T] {\n\ta: T\n}\n\n"

// routesG6 is x11t5's two-type callee (lines 13-17).
const routesG6 = "G6[T, V](): int\n\trequires SizeOf[T]() + SizeOf[V]() < 4 {\n\treturn 0\n}\n\n"

func TestEntryCheckRoutes(t *testing.T) {
	t.Parallel()
	runBodyCases(t, []bodyCase{
		// x11u: a direct concrete call; 23 + 1 is not below 4.
		{"SizeOf of a closed type, direct (x11u)", routesTypes +
			"Z2[T](): int\n\trequires SizeOf[T]() + SizeOf[u8]() < 4 {\n\treturn 0\n}\n\nM(): int {\n\treturn Z2[Big23]()\n}\n", ""},
		// x11u2, the control: a literal for SizeOf[u8]() folds.
		{"a literal, direct (x11u2, judged)", routesTypes +
			"Z2[T](): int\n\trequires SizeOf[T]() + 1 < 4 {\n\treturn 0\n}\n\nM(): int {\n\treturn Z2[Big23]()\n}\n", "E0706@19:9"},
		// x11t5: K restates the demand, which is then not judged at K[Big23].
		{"restated beside SizeOf[u8]() (x11t5)", routesTypes + routesG6 +
			"K[U](): int\n\trequires SizeOf[U]() + SizeOf[u8]() < 4 {\n\treturn G6[U, u8]()\n}\n\nM(): int {\n\treturn K[Big23]()\n}\n", ""},
		// x11t5's demand itself is made: K without the restatement.
		{"not restated beside SizeOf[u8]() (x11t5, the demand)", routesTypes + routesG6 +
			"K[U](): int {\n\treturn G6[U, u8]()\n}\n", "E0706@19:9"},
		// x11t3, the control: both arguments bare, the restatement folds.
		{"restated, both bare (x11t3, judged)", routesTypes + routesG6 +
			"K[U](): int\n\trequires SizeOf[U]() + SizeOf[U]() < 4 {\n\treturn G6[U, U]()\n}\n\nM(): int {\n\treturn K[Big23]()\n}\n", "E0706@24:9"},
		// x11t8: a closed struct beside U.
		{"restated beside SizeOf[Small]() (x11t8)", routesTypes + routesG6 +
			"K[U](): int\n\trequires SizeOf[U]() + SizeOf[Small]() < 4 {\n\treturn G6[U, Small]()\n}\n\nM(): int {\n\treturn K[Big23]()\n}\n", ""},
		// x11t9: a clause over SizeOf[[2]T]() gets no demand at K, and K[Big23] is unjudged.
		{"SizeOf[[2]T]() (x11t9)", routesTypes +
			"G7[T](): int\n\trequires SizeOf[[2]T]() < 8 {\n\treturn 0\n}\n\nK[U](): int {\n\treturn G7[U]()\n}\n\nM(): int {\n\treturn K[Big23]()\n}\n", ""},
		// A compound argument keeps the compound-argument exit, at K and at
		// K[Big23].
		{"a compound argument (n02g)", routesTypes +
			"G[T](): int\n\trequires SizeOf[T]() < 4 {\n\treturn 0\n}\n\nK[U](): int {\n\treturn G[W[U]]()\n}\n\nM(): int {\n\treturn K[Big23]()\n}\n", ""},
		// n02g with K stating the compound clause itself: never judged either.
		{"a compound clause stated (n02g)", routesTypes +
			"G[T](): int\n\trequires SizeOf[T]() < 4 {\n\treturn 0\n}\n\nK[U](): int\n\trequires SizeOf[W[U]]() < 4 {\n\treturn G[W[U]]()\n}\n\nM(): int {\n\treturn K[Big23]()\n}\n", ""},
		// A compound argument beside a forwarded const value is restated
		// with the type as the call spells it, so the demand is made (w1)
		// ...
		{"a compound argument, a forwarded value (w1, the demand)", routesTypes +
			"G[T](const k: int): int\n\trequires k < SizeOf[T]() {\n\treturn k\n}\n\nK[U](const j: int): int {\n\treturn G[W[U]](j)\n}\n", "E0706@19:9"},
		// ... and, stated, never judged: K[u8](3) is unchecked, 3 < 1.
		{"a compound argument, a forwarded value, stated (w2)", routesTypes +
			"G[T](const k: int): int\n\trequires k < SizeOf[T]() {\n\treturn k\n}\n\nK[U](const j: int): int\n\trequires j < SizeOf[W[U]]() {\n\treturn G[W[U]](j)\n}\n\nM(): int {\n\treturn K[u8](3)\n}\n", ""},
		// The control: over the bare U the restated clause is judged at
		// K[u8](3), 3 < 1 (checked at each concrete call).
		{"a bare argument, a forwarded value, stated (w2's control, judged)", routesTypes +
			"G[T](const k: int): int\n\trequires k < SizeOf[T]() {\n\treturn k\n}\n\nK[U](const j: int): int\n\trequires j < SizeOf[U]() {\n\treturn G[U](j)\n}\n\nM(): int {\n\treturn K[u8](3)\n}\n", "E0706@24:9"},
	})
}
