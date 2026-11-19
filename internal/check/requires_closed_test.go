// Why: inside a generic, a compile-time `requires` whose arguments are all
// closed must be judged as at a concrete site, at a call and at a fn value,
// or a false clause reaches a release build unjudged.
// What: TestClosedInGeneric, TestUnspelledArgument,
// TestUnspelledForwarded, TestClosedMessage,
// TestSizeOfArgument, TestSimd.
// How: `SizeOf[T]()` as a const argument is evaluated for a closed T and
// restated for a type parameter, and a same-module clause folds SizeOf and
// AlignOf of a closed type argument. Each value is paired with its call; the
// impl rule is in impl_requires_test.go.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

func TestClosedInGeneric(t *testing.T) {
	t.Parallel()
	const g2 = "G2[T, const N: int](x: &T): int\n\trequires N > 0 {\n\treturn N\n}\n\n"               // lines 1-5
	const g3 = "G3[const A: int, const B: int](): int\n\trequires A > 0 {\n\treturn A + B\n}\n\n"     // lines 1-5
	const g4 = "G4[T, const N: int](x: &T, const k: int): int\n\trequires k < N {\n\treturn k\n}\n\n" // lines 1-5
	runBodyCases(t, []bodyCase{
		// An open type argument beside a closed const one.
		{"open type argument, value", g2 + "H[T](x: &T): fn(&T): int {\n\treturn G2[T, 0]\n}\n", "E0706@7:9"},
		{"open type argument, call", g2 + "H[T](x: &T): int {\n\treturn G2[T, 0](x)\n}\n", "E0706@7:9"},
		{"open type argument, value holds", g2 + "H[T](x: &T): fn(&T): int {\n\treturn G2[T, 1]\n}\n", ""},
		{"open type argument, call holds", g2 + "H[T](x: &T): int {\n\treturn G2[T, 1](x)\n}\n", ""},
		// A forwarded const generic the clause does not read: the clause is
		// judged, not restated.
		{"unread forwarded generic, call", g3 + "H[const M: int](): int {\n\treturn G3[0, M]()\n}\n", "E0706@7:9"},
		{"unread forwarded generic, value", g3 + "H[const M: int](): fn(): int {\n\treturn G3[0, M]\n}\n", "E0706@7:9"},
		{"unread forwarded generic, call holds", g3 + "H[const M: int](): int {\n\treturn G3[1, M]()\n}\n", ""},
		{"unread forwarded generic, value holds", g3 + "H[const M: int](): fn(): int {\n\treturn G3[1, M]\n}\n", ""},
		// A forwarded generic the clause reads is still restated.
		{"read forwarded generic, not restated", g3 + "H[const M: int](): int {\n\treturn G3[M, 0]()\n}\n", "E0706@7:9"},
		{"read forwarded generic, restated", g3 + "H[const M: int](): int\n\trequires M > 0 {\n\treturn G3[M, 0]()\n}\n", ""},
		// A clause over a const value parameter with the generics closed.
		{"value parameter, false", g4 + "H[T](x: &T): int {\n\treturn G4[T, 2](x, 3)\n}\n", "E0706@7:9"},
		{"value parameter, holds", g4 + "H[T](x: &T): int {\n\treturn G4[T, 2](x, 1)\n}\n", ""},
	})
}

// TestUnspelledArgument: an argument genericSubs
// spells (a bool value) or cannot spell (`SizeOf[W[T]]()`) no longer leaves
// every clause of the call unchecked. A closed clause is judged; a clause
// that reads the unspelled argument stays unchecked, as before.
func TestUnspelledArgument(t *testing.T) {
	t.Parallel()
	const gb = "const Yes: bool = true\n\nconst No: bool = false\n\nG[T, const B: bool, const N: int](x: &T): int\n\trequires N > 0 {\n\treturn N\n}\n\n" // lines 1-9
	const gc = "const Yes: bool = true\n\nconst No: bool = false\n\nG[T, const B: bool](x: &T): int\n\trequires B {\n\treturn 1\n}\n\n"                   // lines 1-9
	const gs = "G[const N: int, const M: int](): int\n\trequires M > 0 {\n\treturn N + M\n}\n\nstruct W[T] {\n\ta: T\n}\n\n"                              // lines 1-9
	const f = "F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\nstruct W[T] {\n}\n\n"                                                         // lines 1-7
	runBodyCases(t, []bodyCase{
		// a9: a bool const generic beside the clause's int one.
		{"bool beside, call", gb + "H[T](x: &T): int {\n\treturn G[T, Yes, 0](x)\n}\n", "E0706@11:9"},
		{"bool beside, value", gb + "H[T](x: &T): fn(&T): int {\n\treturn G[T, Yes, 0]\n}\n", "E0706@11:9"},
		{"bool beside, holds", gb + "H[T](x: &T): int {\n\treturn G[T, No, 1](x)\n}\n", ""},
		// a10: the clause is the bool itself.
		{"bool clause, call", gc + "H[T](x: &T): int {\n\treturn G[T, No](x)\n}\n", "E0706@11:9"},
		{"bool clause, value", gc + "H[T](x: &T): fn(&T): int {\n\treturn G[T, No]\n}\n", "E0706@11:9"},
		{"bool clause, holds", gc + "H[T](x: &T): int {\n\treturn G[T, Yes](x)\n}\n", ""},
		// a1b: SizeOf of an open type that is not a bare type parameter,
		// beside the clause's argument.
		{"open SizeOf beside, call", gs + "H[T](): int {\n\treturn G[SizeOf[W[T]](), 0]()\n}\n", "E0706@11:9"},
		{"open SizeOf beside, value", gs + "H[T](): fn(): int {\n\treturn G[SizeOf[W[T]](), 0]\n}\n", "E0706@11:9"},
		{"open SizeOf beside, holds", gs + "H[T](): int {\n\treturn G[SizeOf[W[T]](), 1]()\n}\n", ""},
		// A clause that reads that argument is not judged here: the entry
		// check's.
		{"open SizeOf read, call", f + "H[T](): int {\n\treturn F[SizeOf[W[T]]()]()\n}\n", ""},
		{"open SizeOf read, value", f + "H[T](): fn(): int {\n\treturn F[SizeOf[W[T]]()]\n}\n", ""},
	})
}

// TestUnspelledForwarded: at a call whose generic arguments are all
// closed, a clause that forwards the caller's const value parameter and
// also reads an argument genericSubs cannot spell is not restated, since
// the restatement would name the callee's own generic D, which no caller
// can state (a false E0706). A same-module callee's enum value is spelled
// as the caller's file names it (`lib.Mode.A`), so its clause is restated
// like the int twin's; an imported callee's, whose text names the enum as
// lib does, is E0706 "cannot be judged here" (the closed conjunct
// `D == Mode.A` may be false whatever the forwarded value, and the entry
// check of a release build never judges it). This also refuses a valid
// program of the same shape: the rule's cost, since accepting it would also
// accept one that reads past a table in release.
func TestUnspelledForwarded(t *testing.T) {
	t.Parallel()
	const lib = "export enum Mode {\n\tA\n\tB\n}\n\nexport const MA: Mode = Mode.A\n\n" +
		"export GV[const D: Mode](const k: int): int\n\trequires k < 3 && D == Mode.A {\n\treturn k\n}\n\n" +
		"export GN[const D: int](const k: int): int\n\trequires k < 3 && D == 0 {\n\treturn k\n}\n\n" +
		"export GV2[const D: Mode](const k: int): int\n\trequires k < 3 {\n\treturn k\n}\n\n" +
		"export enum Lvl {\n\tLo\n\tD\n}\n"
	const gv = "import lib\n\nGV[const D: lib.Mode](const k: int): int\n\trequires k < 3 && D == lib.Mode.A {\n\treturn k\n}\n\n" // lines 1-7
	const gn = "import lib\n\nGN[const D: int](const k: int): int\n\trequires k < 3 && D == 0 {\n\treturn k\n}\n\n"               // lines 1-7
	for _, tc := range []struct{ name, src, want, clause string }{
		{"enum, same module", gv + "W(const j: int): int\n\trequires j < 3 {\n\treturn GV[lib.MA](j)\n}\n", "E0706@10:9", "j < 3 && lib.Mode.A == lib.Mode.A"},
		{"enum, same module, restated", gv + "W(const j: int): int\n\trequires j < 3 && lib.Mode.A == lib.Mode.A {\n\treturn GV[lib.MA](j)\n}\n", "", ""},
		{"enum, imported (R5-S1)", "import lib\n\nW(const j: int): int\n\trequires j < 3 {\n\treturn lib.GV[lib.MA](j)\n}\n", "E0706@5:9", "k < 3 && D == Mode.A"},
		{"int twin, same module, restated", gn + "W(const j: int): int\n\trequires j < 3 && 0 == 0 {\n\treturn GN[0](j)\n}\n", "", ""},
		{"int twin, same module, not restated", gn + "W(const j: int): int\n\trequires j < 3 {\n\treturn GN[0](j)\n}\n", "E0706@10:9", "j < 3 && 0 == 0"},
		{"int twin, imported, not restated", "import lib\n\nW(const j: int): int\n\trequires j < 3 {\n\treturn lib.GN[0](j)\n}\n", "E0706@5:9", "j < 3 && 0 == 0"},
		// A clause that does not read the enum generic is still restated, so
		// a caller that does not state it is E0706; and `lib.Lvl.D` is a
		// member of Lvl, not the generic D, so it does not stop the
		// restatement.
		{"enum unread, same module, not restated", "import lib\n\nGV2[const D: lib.Mode](const k: int): int\n\trequires k < 3 {\n\treturn k\n}\n\nW(const j: int): int {\n\treturn GV2[lib.MA](j)\n}\n", "E0706@9:9", "j < 3"},
		{"enum unread, imported, not restated", "import lib\n\nW(const j: int): int {\n\treturn lib.GV2[lib.MA](j)\n}\n", "E0706@4:9", "j < 3"},
		{"qualified member named as the generic, not restated", "import lib\n\nGX[const D: lib.Mode](const k: int): int\n\trequires k < 3 && lib.Lvl.D != lib.Lvl.Lo {\n\treturn k\n}\n\nW(const j: int): int {\n\treturn GX[lib.MA](j)\n}\n", "E0706@9:9", "j < 3 && lib.Lvl.D != lib.Lvl.Lo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			w.add("lib", opt{}, lib)
			// The driver evaluates an imported module's consts before its
			// importers check; here lib.MA gets Mode.A's tag by hand.
			id, ok := w.items.ByKey(types.ItemKey("lib", "MA"))
			if !ok {
				t.Fatal("no lib.MA")
			}
			d := w.items.Get(id).Decl.(*types.ConstDecl)
			d.Value, d.HasValue = w.c.scalarConst(d.Type, 1, 0), true
			b := checkBody(t, w, tc.want, tc.src)
			if tc.clause != "" {
				if got := fact(b.bodyDiags(t)[0], "clause"); got != tc.clause {
					t.Fatalf("clause %q, want %q", got, tc.clause)
				}
			}
		})
	}
}

// TestClosedMessage: the unread forwarded generic is judged, not
// restated, so the message is the false clause's, never "state it on this
// function" (which the restate rule once gave at the same place).
func TestClosedMessage(t *testing.T) {
	t.Parallel()
	const g3 = "G3[const A: int, const B: int](): int\n\trequires A > 0 {\n\treturn A + B\n}\n\n"
	for _, tc := range []struct{ name, src string }{
		{"call", g3 + "H[const M: int](): int {\n\treturn G3[0, M]()\n}\n"},
		{"value", g3 + "H[const M: int](): fn(): int {\n\treturn G3[0, M]\n}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, "E0706@7:9", tc.src)
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code == diag.E0706 && (!strings.Contains(d.Message, "which is false") || strings.Contains(d.Message, "state it on this function")) {
					t.Errorf("E0706 message %q, want the false clause's", d.Message)
				}
			}
		})
	}
}

func TestSizeOfArgument(t *testing.T) {
	t.Parallel()
	const f = "F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\nstruct E {\n}\n\nstruct P {\n\ta: int\n}\n\n" // lines 1-12
	const h = "H[T](): int\n\trequires SizeOf[T]() > 0 {\n\treturn F[SizeOf[T]()]()\n}\n\n"                               // lines 13-17
	const k = "K[T](): int\n\trequires AlignOf[T]() == 8 {\n\treturn 1\n}\n\n"                                            // lines 13-17
	const g = "G[T, const N: int](): int\n\trequires SizeOf[T]() >= N {\n\treturn N\n}\n\n"                               // lines 13-17
	const w = "struct W[T] {\n}\n\n"                                                                                      // lines 18-20
	const gk = "G[T](const k: int): int\n\trequires k < SizeOf[T]() {\n\treturn k\n}\n\n"                                 // lines 1-5
	runBodyCases(t, []bodyCase{
		// SizeOf of a closed type is its value: an empty struct is 0.
		{"closed, value", f + "J(): fn(): int {\n\treturn F[SizeOf[E]()]\n}\n", "E0706@14:9"},
		{"closed, call", f + "J(): int {\n\treturn F[SizeOf[E]()]()\n}\n", "E0706@14:9"},
		{"closed, value holds", f + "J(): fn(): int {\n\treturn F[SizeOf[P]()]\n}\n", ""},
		{"closed, call holds", f + "J(): int {\n\treturn F[SizeOf[P]()]()\n}\n", ""},
		// SizeOf of a type parameter is restated.
		{"type parameter, value not restated", f + "J[T](): fn(): int {\n\treturn F[SizeOf[T]()]\n}\n", "E0706@14:9"},
		{"type parameter, call not restated", f + "J[T](): int {\n\treturn F[SizeOf[T]()]()\n}\n", "E0706@14:9"},
		{"type parameter, restated", f + h, ""},
		// The restated clause folds at a concrete caller.
		{"restated, caller false", f + h + "J(): int {\n\treturn H[E]()\n}\n", "E0706@19:9"},
		{"restated, caller value false", f + h + "J(): fn(): int {\n\treturn H[E]\n}\n", "E0706@19:9"},
		{"restated, caller holds", f + h + "J(): int {\n\treturn H[P]()\n}\n", ""},
		{"AlignOf folds, false", f + k + "J(): int {\n\treturn K[u8]()\n}\n", "E0706@19:9"},
		{"AlignOf folds, holds", f + k + "J(): int {\n\treturn K[int]()\n}\n", ""},
		// A type on a layout cycle has no size to read, so
		// the rejected program gains no cascade E0706.
		{"layout cycle, no cascade", f + "struct Rec {\n\ta: Rec2\n}\n\nstruct Rec2 {\n\tb: Rec\n}\n\nJ(): int {\n\treturn F[SizeOf[Rec]()]()\n}\n", "E0907@13:8"},
		// A generic caller of the restating H, with H's type generic bound to
		// the caller's own type parameter, restates the clause over it, which
		// is then judged at its concrete caller (it once read past a table in
		// release).
		{"two levels, call restated", f + h + "K[U](): int {\n\treturn H[U]()\n}\n", "E0706@19:9"},
		{"two levels, value restated", f + h + "K[U](): fn(): int {\n\treturn H[U]\n}\n", "E0706@19:9"},
		{"two levels, stated", f + h + "K[U](): int\n\trequires SizeOf[U]() > 0 {\n\treturn H[U]()\n}\n", ""},
		{"two levels, stated, caller false", f + h + "K[U](): int\n\trequires SizeOf[U]() > 0 {\n\treturn H[U]()\n}\n\nJ(): int {\n\treturn K[E]()\n}\n", "E0706@24:9"},
		// Beside a closed const argument the same (`SizeOf[U]() >= 100`);
		// with the type argument W[U] rather than U the clause is still not
		// judged (the compound-argument exit): the same-module fold reads no
		// layout of a compound type over a generic, so a restatement would
		// not be judged either. The entry check judges it (dev exit 101,
		// release runs it).
		{"open type beside a closed const, call restated", f + g + "K[U](): int {\n\treturn G[U, 100]()\n}\n", "E0706@19:9"},
		{"open type beside a closed const, value restated", f + g + "K[U](): fn(): int {\n\treturn G[U, 100]\n}\n", "E0706@19:9"},
		{"open wrapped type, call not judged", f + h + w + "K[U](): int {\n\treturn H[W[U]]()\n}\n", ""},
		{"open wrapped type, value not judged", f + h + w + "K[U](): fn(): int {\n\treturn H[W[U]]\n}\n", ""},
		// The same clause over an open type generic, when it also reads a
		// const value parameter the caller passes on, is restated as the
		// call spells it and judged at each concrete call.
		{"open type with a forwarded value, not restated", gk + "K[U](const j: int): int {\n\treturn G[U](j)\n}\n", "E0706@7:9"},
		{"open type with a forwarded value, restated, holds", gk + "K[U](const j: int): int\n\trequires j < SizeOf[U]() {\n\treturn G[U](j)\n}\n\nJ(): int {\n\treturn K[u64](5)\n}\n", ""},
		{"open type with a forwarded value, restated, false", gk + "K[U](const j: int): int\n\trequires j < SizeOf[U]() {\n\treturn G[U](j)\n}\n\nJ(): int {\n\treturn K[u8](5)\n}\n", "E0706@12:9"},
	})
}

// TestSimd runs over the real std.simd and evaluator (mk check over the
// overlay): an imported clause over the const generics alone, with the lane
// type open, is evaluated with a stand-in for the lane type it does not read
// (standIns).
func TestSimd(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, src, want string }{
		{"Splat value, 3 lanes", "G[T: simd.Lane](): fn(x: T): simd.Vec[T, 3] {~    return simd.Vec[T, 3].Splat~}~", "main.mk:4:12: E0706"},
		{"Splat call, 3 lanes", "G[T: simd.Lane](x: T): simd.Vec[T, 3] {~    return simd.Vec[T, 3].Splat(x)~}~", "main.mk:4:12: E0706"},
		{"Splat value, 4 lanes", "G[T: simd.Lane](): fn(x: T): simd.Vec[T, 4] {~    return simd.Vec[T, 4].Splat~}~", ""},
		{"Splat call, 4 lanes", "G[T: simd.Lane](x: T): simd.Vec[T, 4] {~    return simd.Vec[T, 4].Splat(x)~}~", ""},
		{"With out of range", "G[T: simd.Lane](v: simd.Vec[T, 4], x: T): simd.Vec[T, 4] {~    return v.With(7, x)~}~", "main.mk:4:12: E0706"},
		{"With in range", "G[T: simd.Lane](v: simd.Vec[T, 4], x: T): simd.Vec[T, 4] {~    return v.With(1, x)~}~", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "import std.simd~~" + tc.src + "~export Main(sys: &System) {~}~"
			out, code := mkOverlay(t, strings.ReplaceAll(src, "~", string(rune(10))), "check")
			if tc.want == "" && code != 0 || tc.want != "" && (code == 0 || !strings.Contains(out, tc.want)) {
				t.Fatalf("exit %d, want %q:%c%s", code, tc.want, rune(10), out)
			}
		})
	}
}
