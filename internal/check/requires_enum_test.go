// Why: const generic arguments and compile-time requires had three
// precision bugs around bool literals and enum values.
// What: TestBoolLiteralArg, TestSameModuleEnumConst,
// TestEnumClauseFolded, TestEnumClauseCases,
// TestPayloadVariantMessage.
// How: a literal true or false is a const argument; a same-module const
// whose value is a variant of an enum with no fields is a const argument,
// its value built from the layout; and a same-module clause over such an
// enum value (`D == lib.Mode.B`) is folded at a call.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestBoolLiteralArg: `G[true]()` was E0711 and E0705,
// although a literal is a const argument.
func TestBoolLiteralArg(t *testing.T) {
	t.Parallel()
	const g = "G[const B: bool](): int\n\trequires B {\n\treturn 1\n}\n\n" // lines 1-5
	const gt = "G[T, const B: bool](x: &T): int\n\trequires B {\n\treturn 1\n}\n\n"
	const n = "N[const K: int](): int {\n\treturn K\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"true holds", g + "F(): int {\n\treturn G[true]()\n}\n", ""},
		{"false is reported", g + "F(): int {\n\treturn G[false]()\n}\n", "E0706@7:9"},
		{"value, false", g + "F(): fn(): int {\n\treturn G[false]\n}\n", "E0706@7:9"},
		{"in a generic, true", gt + "H[T](x: &T): int {\n\treturn G[T, true](x)\n}\n", ""},
		{"in a generic, false", gt + "H[T](x: &T): int {\n\treturn G[T, false](x)\n}\n", "E0706@7:9"},
		{"not a bool generic", n + "F(): int {\n\treturn N[true]()\n}\n", "E0301@6:11 E0711@6:9"},
	})
}

// TestSameModuleEnumConst: a same-module named enum
// const as a const argument was E0908 (with E0711). It is its value,
// so the call's other clauses are judged; a clause that reads
// it inside a generic is judged too, as a closed argument (closedClause).
func TestSameModuleEnumConst(t *testing.T) {
	t.Parallel()
	const m = "enum Mode {\n\tA\n\tB\n}\n\nconst MA: Mode = Mode.A\n\nconst MB = Mode.B\n\n"             // lines 1-9
	const g = m + "G[T, const D: Mode, const N: int](x: &T): int\n\trequires N > 0 {\n\treturn N\n}\n\n" // lines 10-14
	const gd = m + "GD[T, const D: Mode](x: &T): int\n\trequires D == Mode.B {\n\treturn 1\n}\n\n"       // lines 10-14
	runBodyCases(t, []bodyCase{
		{"closed clause beside, concrete", g + "C(): int {\n\tv := 1\n\treturn G[int, MA, 0](&v)\n}\n", "E0706@17:9"},
		{"closed clause beside, holds", g + "C(): int {\n\tv := 1\n\treturn G[int, MA, 1](&v)\n}\n", ""},
		{"closed clause beside, in a generic", g + "H[T](x: &T): int {\n\treturn G[T, MB, 0](x)\n}\n", "E0706@16:9"},
		{"enum clause, concrete, false", gd + "C(): int {\n\tv := 1\n\treturn GD[int, MA](&v)\n}\n", "E0706@17:9"},
		{"enum clause, concrete, holds", gd + "C(): int {\n\tv := 1\n\treturn GD[int, MB](&v)\n}\n", ""},
		// A clause that reads the enum argument inside a generic is judged
		// there: the value is spelled, so the clause reads no open argument
		// (closedClause). A variant written as the argument is a const
		// argument (TestEnumClauseCases); it was E0705.
		{"enum clause, in a generic", gd + "H[T](x: &T): int {\n\treturn GD[T, MA](x)\n}\n", "E0706@16:9"},
		{"enum clause, in a generic, holds", gd + "H[T](x: &T): int {\n\treturn GD[T, MB](x)\n}\n", ""},
		{"variant as the argument", gd + "C(): int {\n\tv := 1\n\treturn GD[int, Mode.B](&v)\n}\n", ""},
		{"variant as the argument, false", gd + "C(): int {\n\tv := 1\n\treturn GD[int, Mode.A](&v)\n}\n", "E0706@17:9"},
		// Any initializer that folds with enum values: a const of a const,
		// and a bool const over a comparison.
		{"const of a const", gd + "const MC: Mode = MB\n\nC(): int {\n\tv := 1\n\treturn GD[int, MC](&v)\n}\n", ""},
		{"bool const over a comparison", m + "const IsB: bool = MA == Mode.B\n\nGB[const B: bool](): int\n\trequires B {\n\treturn 1\n}\n\nC(): int {\n\treturn GB[IsB]()\n}\n", "E0706@18:9"},
		// A value of another type is E0301, no longer hidden by E0908.
		{"another enum", gd + "enum Other {\n\tX\n}\n\nconst OX: Other = Other.X\n\nC(): int {\n\tv := 1\n\treturn GD[int, OX](&v)\n}\n", "E0301@23:17 E0711@23:9"},
		{"enum against int", m + "N[const K: int](): int {\n\treturn K\n}\n\nC(): int {\n\treturn N[MA]()\n}\n", "E0301@15:11 E0711@15:9"},
		// In a declaration's type the enum's facts are not final yet in
		// stage 5: still E0908, never requireFinal's ICE.
		{"in a declaration type", m + "struct S[const D: Mode] {\n\tx: int\n}\n\nF(s: S[MA]): int {\n\treturn s.x\n}\n", "E0908@14:8"},
	})
}

// TestEnumClauseFolded: a same-module clause over an
// imported enum value was not folded at a concrete call. The imported
// values are set by hand in the evaluator's form (the driver evaluates
// lib's consts first), with a declared u16 tag, so a variant check builds
// must equal them byte for byte: a mismatch would make the holding cases
// false.
func TestEnumClauseFolded(t *testing.T) {
	t.Parallel()
	const lib = "export enum Mode: u16 {\n\tA = 5\n\tB = 300\n}\n\nexport const MA: Mode = Mode.A\n\nexport const MB: Mode = Mode.B\n"
	const gd = "import lib\n\nGD[T, const D: lib.Mode](x: &T): int\n\trequires D == lib.Mode.B {\n\treturn 1\n}\n\n" // lines 1-7
	const gn = "import lib\n\nGN[const D: lib.Mode](): int\n\trequires D != lib.Mode.B {\n\treturn 1\n}\n\n"
	const gv = "import lib\n\nGV(const m: lib.Mode): int\n\trequires m == lib.Mode.B {\n\treturn 1\n}\n\n"
	for _, tc := range []struct{ name, src, want string }{
		{"==, false", gd + "C(): int {\n\tv := 1\n\treturn GD[int, lib.MA](&v)\n}\n", "E0706@10:9"},
		{"==, holds", gd + "C(): int {\n\tv := 1\n\treturn GD[int, lib.MB](&v)\n}\n", ""},
		{"!=, false", gn + "C(): int {\n\treturn GN[lib.MB]()\n}\n", "E0706@9:9"},
		{"!=, holds", gn + "C(): int {\n\treturn GN[lib.MA]()\n}\n", ""},
		{"value parameter, false", gv + "C(): int {\n\treturn GV(lib.MA)\n}\n", "E0706@9:9"},
		{"value parameter, holds", gv + "C(): int {\n\treturn GV(lib.MB)\n}\n", ""},
		{"value, false", gd + "C(): fn(&int): int {\n\treturn GD[int, lib.MA]\n}\n", "E0706@9:9"},
		// An imported variant as the argument.
		{"variant, false", gd + "C(): int {\n\tv := 1\n\treturn GD[int, lib.Mode.A](&v)\n}\n", "E0706@10:9"},
		{"variant, holds", gd + "C(): int {\n\tv := 1\n\treturn GD[int, lib.Mode.B](&v)\n}\n", ""},
		{"value parameter, variant, false", gv + "C(): int {\n\treturn GV(lib.Mode.A)\n}\n", "E0706@9:9"},
		{"value parameter, variant, holds", gv + "C(): int {\n\treturn GV(lib.Mode.B)\n}\n", ""},
		// An imported enum const for a generic typed by its type
		// parameter, in a declaration's type and in a body.
		{"param-typed, declaration type", "import lib\n\nstruct P[T, const V: T] {\n\tx: int\n}\n\nTake(p: P[lib.Mode, lib.MB]): int {\n\treturn p.x\n}\n\nC(): int {\n\treturn Take(P[lib.Mode, lib.MB](x: 4))\n}\n", ""},
		{"param-typed, another type", "import lib\n\nstruct P[T, const V: T] {\n\tx: int\n}\n\nC(): int {\n\treturn P[bool, lib.MB](x: 4).x\n}\n", "E0301@8:17"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			w.add("lib", opt{}, lib)
			for name, discr := range map[string]uint64{"MA": 5, "MB": 300} {
				id, ok := w.items.ByKey(types.ItemKey("lib", name))
				if !ok {
					t.Fatalf("no lib.%s", name)
				}
				d := w.items.Get(id).Decl.(*types.ConstDecl)
				d.Value, d.HasValue = w.c.scalarConst(d.Type, 2, discr), true
			}
			checkBody(t, w, tc.want, tc.src)
		})
	}
}

// TestEnumClauseCases pins these fixes. A const generic typed by a type
// parameter (`GT[T, const V: T]`) is not judged against its unsubstituted
// `$0` (the str forms need the evaluator: internal/driver
// TestParamTypedConstArg). A named int or bool const against an enum
// or bool generic of another type is E0301 (the instance read a Pick's tag
// and payload out of an int); an integer const for an integer generic keeps
// its reading. A same-module clause over a const value parameter folds a
// variant and a same-module enum const argument. A variant without a
// payload is a const argument, as the reference lists. A bool fold and a
// name in parentheses are bool arguments.
func TestEnumClauseCases(t *testing.T) {
	t.Parallel()
	// Lines: m 1-9, gt 10-13, pick 1-11, gb 10-27, v 10-14, gd 10-22, g 1-11.
	const m = "enum Mode {\n\tA\n\tB\n}\n\nconst MA: Mode = Mode.A\n\nconst MB = Mode.B\n\n"
	const gt = m + "GT[T, const V: T](): T {\n\treturn V\n}\n\n"
	const pick = "enum Pick {\n\tName(s: str)\n\tNum(n: int)\n}\n\nconst K: int = 0\n\nGD[const D: Pick](): int {\n\treturn 1\n}\n\n"
	const gb = m + "const K: int = 7\n\nconst U: u8 = 3\n\nconst IsB: bool = true\n\nGB[const B: bool](): int {\n\treturn 1\n}\n\nN[const V: int](): int {\n\treturn V\n}\n\nGM[const D: Mode](): int {\n\treturn 1\n}\n\n"
	const v = m + "V(const x: Mode): int\n\trequires x == Mode.B {\n\treturn 1\n}\n\n"
	const gd = m + "enum Other {\n\tX\n}\n\nGD[const D: Mode](): int\n\trequires D == Mode.B {\n\treturn 1\n}\n\nN[const V: int](): int {\n\treturn V\n}\n\n"
	const g = "const T1: bool = true\n\nG[const B: bool](): int\n\trequires B {\n\treturn 1\n}\n\nN[const V: int](): int {\n\treturn V\n}\n\n"
	// Lines 1-35; a case's code starts at line 36.
	const r2 = m + "const K: int = 7\n\nGR[const V: T, T](): T {\n\treturn V\n}\n\nstruct Box[T] {\n\ta: int\n}\n\nBox[T].M[const V: T](&self): T {\n\treturn V\n}\n\n" +
		"GT[T, const V: T](): T {\n\treturn V\n}\n\nG8[const N: u8](): int {\n\treturn 1\n}\n\nGI[const N: int](): int {\n\treturn N\n}\n\n"
	runBodyCases(t, []bodyCase{
		// A const generic typed by a type parameter.
		{"param-typed, enum const", gt + "F(): Mode {\n\treturn GT[Mode, MB]()\n}\n", ""},
		{"param-typed, in a declaration type", gt + "const KK: u8 = 3\n\nstruct P[T, const V: T] {\n\tx: int\n}\n\nF(p: P[u8, KK]): int {\n\treturn p.x\n}\n\nC(): int {\n\treturn F(P[u8, KK](x: 5)) + P[Mode, MB](x: 1).x\n}\n", ""},
		// A named const against a generic of another type.
		{"int const for a payload enum", pick + "F(): int {\n\treturn GD[K]()\n}\n", "E0301@13:12 E0711@13:9"},
		{"int const for a plain enum", gb + "F(): int {\n\treturn GM[K]()\n}\n", "E0301@29:12 E0711@29:9"},
		{"int const for a bool", gb + "F(): int {\n\treturn GB[K]()\n}\n", "E0301@29:12 E0711@29:9"},
		{"bool const for an int", gb + "F(): int {\n\treturn N[IsB]()\n}\n", "E0301@29:11 E0711@29:9"},
		{"u8 const for an int keeps its reading", gb + "F(): int {\n\treturn N[U]()\n}\n", ""},
		// The same through a generic typed by its type parameter, once the
		// type argument is written (paramTyped).
		{"param-typed, int const for an enum", gt + "const K: int = 0\n\nF(): Mode {\n\treturn GT[Mode, K]()\n}\n", "E0301@17:18 E0711@17:9"},
		{"param-typed, enum const for a bool", gt + "F(): bool {\n\treturn GT[bool, MA]()\n}\n", "E0301@15:18 E0711@15:9"},
		{"param-typed, literal for an enum", gt + "F(): Mode {\n\treturn GT[Mode, 0]()\n}\n", "E0301@15:18 E0711@15:9"},
		{"param-typed, int const for an enum, a type", gt + "const K: int = 0\n\nstruct P[T, const V: T] {\n\tx: int\n}\n\nF(): int {\n\treturn P[Mode, K](x: 1).x\n}\n", "E0301@21:17"},
		{"param-typed, u8 literal", gt + "F(): u8 {\n\treturn GT[u8, 3]()\n}\n", ""},
		// A same-module clause over a const value parameter.
		{"value parameter, variant, false", v + "F(): int {\n\treturn V(Mode.A)\n}\n", "E0706@16:9"},
		{"value parameter, same-module const, false", v + "F(): int {\n\treturn V(MA)\n}\n", "E0706@16:9"},
		{"value parameter, variant, holds", v + "F(): int {\n\treturn V(Mode.B)\n}\n", ""},
		{"value parameter, same-module const, holds", v + "F(): int {\n\treturn V(MB)\n}\n", ""},
		// A variant without a payload.
		{"variant, holds", gd + "F(): int {\n\treturn GD[Mode.B]()\n}\n", ""},
		{"variant, false", gd + "F(): int {\n\treturn GD[Mode.A]()\n}\n", "E0706@24:9"},
		{"variant, value, false", gd + "F(): fn(): int {\n\treturn GD[Mode.A]\n}\n", "E0706@24:9"},
		{"variant of another enum", gd + "F(): int {\n\treturn GD[Other.X]()\n}\n", "E0301@24:12 E0711@24:9"},
		{"variant for an int", gd + "F(): int {\n\treturn N[Mode.A]()\n}\n", "E0301@24:11 E0711@24:9"},
		{"variant in a generic", gd + "H[T](x: &T): int {\n\treturn GD[Mode.B]()\n}\n", ""},
		// A call in a generic that passes no open argument is judged as at
		// a concrete site (closedClause).
		{"variant in a generic, false", gd + "H[T](x: &T): int {\n\treturn GD[Mode.A]()\n}\n", "E0706@24:9"},
		{"variant, param-typed", gt + "F(): Mode {\n\treturn GT[Mode, Mode.A]()\n}\n", ""},
		// In a declaration's type the enum's facts are not final yet: the
		// old E0705 stands.
		{"variant in a declaration type", m + "struct S[const D: Mode] {\n\tx: int\n}\n\nF(s: S[Mode.A]): int {\n\treturn s.x\n}\n", "E0705@14:8"},
		// A bool fold and a name in parentheses.
		{"bool fold", g + "F(): int {\n\treturn G[!false]()\n}\n", ""},
		{"bool fold over a const", g + "F(): int {\n\treturn G[true && T1]()\n}\n", ""},
		{"bool fold, false", g + "F(): int {\n\treturn G[T1 == false]()\n}\n", "E0706@13:9"},
		{"true in parentheses", g + "F(): int {\n\treturn G[(true)]()\n}\n", ""},
		{"const in parentheses", g + "F(): int {\n\treturn G[(T1)]()\n}\n", ""},
		{"true in parentheses, a declaration type", g + "struct W[const B: bool] {\n\tx: int\n}\n\nF(w: W[(true)]): int {\n\treturn w.x\n}\n", ""},
		{"bool fold for an int", g + "F(): int {\n\treturn N[!false]()\n}\n", "E0301@13:11 E0711@13:9"},

		// A const argument is judged at its generic's type once the type
		// arguments are bound, every type argument before the const ones
		// and the receiver before a method's brackets. u2's two forms stay
		// accepted.
		{"V before T", r2 + "F(): int {\n\treturn GR[K, int]()\n}\n", ""},
		{"V before T, another type", r2 + "F(): Mode {\n\treturn GR[K, Mode]()\n}\n", "E0301@37:12 E0711@37:9"},
		{"V before T, in a type", r2 + "struct Q[const V: T, T] {\n\tx: int\n}\n\nF(): int {\n\treturn Q[K, int](x: 1).x + Q[MB, Mode](x: 2).x\n}\n", ""},
		{"V before T, in a type, another type", r2 + "struct Q[const V: T, T] {\n\tx: int\n}\n\nF(): int {\n\treturn Q[K, Mode](x: 1).x\n}\n", "E0301@41:11"},
		{"owner-typed method const", r2 + "F(): int {\n\tb := Box[int](a: 1)\n\treturn b.M[K]()\n}\n", ""},
		{"owner-typed method const, another type", r2 + "F(): Mode {\n\tb := Box[Mode](a: 1)\n\treturn b.M[K]()\n}\n", "E0301@38:13 E0711@38:11"},
		// The owning type's T bound by a value argument: judged once it is.
		{"namespace form, owner bound later", r2 + "F(): int {\n\tb := Box[int](a: 1)\n\treturn Box.M[K](&b)\n}\n", ""},
		{"namespace form, owner bound later, another type", r2 + "F(): Mode {\n\tb := Box[Mode](a: 1)\n\treturn Box.M[K](&b)\n}\n", "E0301@38:15"},
		// T's argument is the caller's open parameter: a value of a closed
		// type is E0301 (s1 to s3, n01), no instance is checked again; the
		// caller's own const of that type stands.
		{"open T, variant", r2 + "H[U](): U {\n\treturn GT[U, Mode.B]()\n}\n", "E0301@37:15 E0711@37:9"},
		{"open T, same-module enum const", r2 + "H[U](): U {\n\treturn GT[U, MB]()\n}\n", "E0301@37:15 E0711@37:9"},
		{"open T, int const", r2 + "H[U](): U {\n\treturn GT[U, K]()\n}\n", "E0301@37:15 E0711@37:9"},
		{"open T, literal", r2 + "H[U](): U {\n\treturn GT[U, 3]()\n}\n", "E0301@37:15 E0711@37:9"},
		{"open T, in a type", r2 + "struct P[T, const V: T] {\n\tx: int\n}\n\nH[U](): int {\n\treturn P[U, MB](x: 1).x\n}\n", "E0301@41:14"},
		{"open T, the caller's own const", r2 + "H[U, const W: U](): U {\n\treturn GT[U, W]()\n}\n", ""},
		// A forwarded const parameter is judged by its declared type.
		{"forwarded const parameter, another type", r2 + "H[const J: int](): int {\n\treturn G8[J]()\n}\n", "E0301@37:12 E0711@37:9"},
		{"forwarded const parameter, its type", r2 + "H[const J: u8](): int {\n\treturn G8[J]()\n}\n", ""},
		{"forwarded const parameter, an array length", r2 + "H[const J: u8](): int {\n\ta: [J]u8 = [0; 3]\n\treturn a.Len()\n}\n", "E0301@37:6"},
		// An integer const is converted to the generic's type.
		{"int const fits u8", r2 + "F(): int {\n\treturn G8[K]()\n}\n", ""},
		{"int const past u8", r2 + "const Big: int = 300\n\nF(): int {\n\treturn G8[Big]()\n}\n", "E0303@39:12 E0711@39:9"},
		{"negative int const for u8", r2 + "const Neg: int = -1\n\nF(): int {\n\treturn G8[Neg]()\n}\n", "E0303@39:12 E0711@39:9"},
		{"u8 const is an int instance", r2 + "const U8: u8 = 7\n\nF(): int {\n\treturn GI[U8]() + GI[7]()\n}\n", ""},
		// SizeOf is an int: any integer generic takes it, any other type
		// is E0301 (it was accepted, and the instance read a Mode out of
		// the int).
		{"SizeOf for u8", r2 + "F(): int {\n\treturn G8[SizeOf[int]()]()\n}\n", ""},
		{"SizeOf for an int", r2 + "F(): int {\n\treturn GI[SizeOf[int]()]()\n}\n", ""},
		{"SizeOf for an enum", r2 + "F(): Mode {\n\treturn GT[Mode, SizeOf[int]()]()\n}\n", "E0301@37:18 E0711@37:9"},
		{"SizeOf for the caller's open T", r2 + "H[U](): U {\n\treturn GT[U, SizeOf[int]()]()\n}\n", "E0301@37:15 E0711@37:9"},
		{"SizeOf of the caller's T for an int", r2 + "H[U](): int {\n\treturn GI[SizeOf[U]()]()\n}\n", ""},
		// For an integer generic other than int, a closed T's size
		// is converted, E0303 when it does not fit (300 read as 44 for
		// a u8); an open T's is E0301, as is a same-module type's in
		// a declaration, whose layout is not final there.
		{"SizeOf past u8", r2 + "struct Big {\n\ta: [300]u8\n}\n\nF(): int {\n\treturn G8[SizeOf[Big]()]()\n}\n", "E0303@41:12 E0711@41:9"},
		{"SizeOf fits u8, one instance with the literal", r2 + "struct Small {\n\ta: [7]u8\n}\n\nF(): int {\n\treturn G8[SizeOf[Small]()]() + G8[7]()\n}\n", ""},
		{"SizeOf past u8, in a type", r2 + "struct Big {\n\ta: [300]u8\n}\n\nstruct B8[const N: u8] {\n\tx: int\n}\n\nF(): int {\n\treturn B8[SizeOf[Big]()](x: 1).x\n}\n", "E0303@45:12"},
		{"SizeOf past u8, owner bound later", r2 + "struct Big {\n\ta: [300]u8\n}\n\nF(): u8 {\n\tb := Box[u8](a: 1)\n\treturn Box.M[SizeOf[Big]()](&b)\n}\n", "E0303@42:15"},
		{"SizeOf fits u8, owner bound later", r2 + "struct Small {\n\ta: [7]u8\n}\n\nF(): u8 {\n\tb := Box[u8](a: 1)\n\treturn Box.M[SizeOf[Small]()](&b)\n}\n", ""},
		{"SizeOf of the caller's T for u8", r2 + "H[U](): int {\n\treturn G8[SizeOf[U]()]()\n}\n", "E0301@37:12 E0711@37:9"},
		{"SizeOf of a same-module type for u8, a declaration", r2 + "struct Small {\n\ta: [7]u8\n}\n\nstruct B8[const N: u8] {\n\tx: int\n}\n\nF(b: B8[SizeOf[Small]()]): int {\n\treturn b.x\n}\n", "E0301@44:9"},
		// An error union's layout reads each error's payload, so a
		// same-module payload is not final in a declaration either: E0301,
		// where it was an internal error.
		{"SizeOf of an error union with a same-module payload, a declaration", r2 + "struct Small {\n\ta: [10]u8\n}\n\nerror Bad(b: Small)\n\nstruct B8[const N: u8] {\n\tx: int\n}\n\nF(b: B8[SizeOf[Bad!u8]()]): int {\n\treturn b.x\n}\n", "E0301@46:9"},
		{"SizeOf of an error set with a same-module payload, a declaration", r2 + "struct Small {\n\ta: [10]u8\n}\n\nerror Bad(b: Small)\n\nstruct B8[const N: u8] {\n\tx: int\n}\n\nF(b: B8[SizeOf[Bad!void]()]): int {\n\treturn b.x\n}\n", "E0301@46:9"},
		// A reference or a slice is one or two pointers whatever its
		// element, so it is converted in a declaration too; nested in
		// another type it still needs the element's facts (E0301).
		{"SizeOf of a reference and a slice, a declaration", r2 + "struct Small {\n\ta: [10]u8\n}\n\nstruct B8[const N: u8] {\n\tx: int\n}\n\nF(b: B8[SizeOf[&Small]()], c: B8[SizeOf[[]Small]()], d: B8[SizeOf[*Small]()]): int {\n\treturn b.x + c.x + d.x\n}\n\nC(): int {\n\treturn F(B8[8](x: 1), B8[16](x: 2), B8[8](x: 3))\n}\n", ""},
		{"SizeOf of an optional reference, a declaration", r2 + "struct Small {\n\ta: [10]u8\n}\n\nstruct B8[const N: u8] {\n\tx: int\n}\n\nF(b: B8[SizeOf[?&Small]()]): int {\n\treturn b.x\n}\n", "E0301@44:9"},
		// A char const for an int generic is E0301;
		// a char const needs the evaluator, so it is pinned in
		// internal/driver TestParamTypedConstArg.
		// A variant of an enum with a payload in some variant.
		{"variant of a payload enum", r2 + "enum Opt {\n\tSome(n: int)\n\tNothing\n}\n\nGO[const O: Opt](): int {\n\treturn 1\n}\n\nF(): int {\n\treturn GO[Opt.Nothing]()\n}\n", "E0705@46:12 E0711@46:9"},
	})
}

// TestPayloadVariantMessage: the E0705 for a
// variant of an enum with a payload names the restriction.
func TestPayloadVariantMessage(t *testing.T) {
	t.Parallel()
	w := newWorld(t, true)
	b := checkBody(t, w, "E0705@11:12 E0711@11:9", "enum Opt {\n\tSome(n: int)\n\tNothing\n}\n\nGO[const O: Opt](): int {\n\treturn 1\n}\n\nF(): int {\n\treturn GO[Opt.Nothing]()\n}\n")
	for _, d := range append(b.diags(), b.bodyDiags(t)...) {
		if d.Code == diag.E0705 && !strings.Contains(d.Message, "no payload in any variant") {
			t.Errorf("E0705 message %q, want the payload restriction", d.Message)
		}
	}
}
