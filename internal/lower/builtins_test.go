// Why: builtins, literals, Must, index checks and contracts each lower to a
// fixed MIR shape that later passes and the back end depend on.
// What: TestBuiltins, TestMustNamesError,
// TestNegativeNarrowIndexPanics, TestFieldReadOfTemporaryIsCopy and
// TestContracts_*.
// How: lowers small programs and compares the MIR text line by line.

package lower

import (
	"strings"
	"testing"
)

func TestBuiltins(t *testing.T) {
	l := lowerSrc(t, `B(x: i32, y: u8, o: ?int, f: f32): int {
	a := i64(x)
	b := Cast[i64](x)
	c := Cast[i8](x)
	d := Trunc[u8](x)
	e := SizeOf[i64]() + AlignOf[i32]()
	g: ?u8 = some(y)
	h: ?int = none
	k := o else { 3 }
	m := o.IsSome() && h.IsNone() && g.IsSome() && true && !false
	n := f64(f)
	if m {
		return Cast[int](a + b) + Cast[int](c) + Cast[int](d) + e + k + Cast[int](n)
	}
	return 0
}
Lit(): u8 {
	return u8(200)
}
`)
	f := l.print("B")
	wantIn(t, f,
		"_5 = Lossless[i64](copy _1)", // T(x)
		"_6 = Lossless[i64](copy _1)", // Cast lossless by type: no check
		"_7 = Checked[i8](copy _1)",   // Cast that may trap
		"_8 = Trunc[u8](copy _1)",
		"_9 = AddNoWrap(int 8, int 4)", // SizeOf and AlignOf of closed types, from check's values; the range analysis proves the sum
		"_10 = some(copy _2)",
		"_11 = none()",
		"Discriminant(", "as some).0", "= int 3", // the else default: a switch
		"Eq(copy _20, u64 1)", "Eq(copy _22, u64 0)", // IsSome, IsNone
		"Lossless[f64](copy _4)",
		"Checked[int](copy _28)")
	// A conversion of a constant is its value (never a narrowing Lossless).
	wantIn(t, l.print("Lit"), "_0 = u8 200")
	if strings.Contains(l.print("Lit"), "Lossless") {
		t.Fatalf("u8(200) is a cast:\n%s", l.print("Lit"))
	}
}

func TestMustNamesError(t *testing.T) {
	l := lowerSrc(t, `error Denied
Load(id: int): Denied!str {
	if id < 0 {
		fail Denied
	}
	return "data"
}
MustLoad(): str {
	return Load(-1).Must()
}
`)
	f := l.print("MustLoad")
	// An EdgeMust whose panic block carries the error value
	// itself, so the report names Denied; no constant message.
	wantIn(t, f, "err must _1 -> bb1", "panic must copy _1")
	if strings.Contains(f, `"must"`) {
		t.Fatalf("a constant message:\n%s", f)
	}
}

func TestNegativeNarrowIndexPanics(t *testing.T) {
	l := lowerSrc(t, `F(xs: []u8, i: i8): u8 {
	return xs[i]
}
`)
	// i8 is sign-extended to int (value-defined), then reinterpreted as uint
	// at equal width: -1 becomes 2^64-1 and fails i < len, where a
	// zero-extension would read element 255. The evaluator half (E0901 in a
	// const evaluation) and the native half are tested in eval and by the
	// native corpus run.
	wantIn(t, l.print("F"), "_3 = Lossless[int](copy _2)", "_4 = Trunc[uint](copy _3)", "_6 = Trunc[uint](copy _5)",
		"Lt(copy _4, copy _6)", "panic bounds", "copy _1[_2]")
}

func TestFieldReadOfTemporaryIsCopy(t *testing.T) {
	l := lowerSrc(t, resSrc+`struct P {
	h: Res
	n: int
}
MakeP(): P {
	return P(h: Res(h: 1), n: 2)
}
Take(): int {
	P(h, n) := MakeP()
	h.Abandon()
	return n + MakeP().n
}
Read() {
	x := MakeP().h
	x.Abandon()
}
`)
	f := l.print("Take")
	// A destructure takes the linear field (a move out of the temporary);
	// a plain field of a temporary is a copy.
	wantIn(t, f, "_2 = move _1.0", "_3 = copy _1.1", "copy _6.1")
	// A non-plain field read of a temporary is a Copy into a fresh
	// temporary (borrow's E0409), never a take.
	r := l.print("Read")
	wantIn(t, r, "= call m.MakeP()", "= copy _1.0")
	if strings.Contains(r, "move _1.0") {
		t.Fatalf("a take of a temporary's field:\n%s", r)
	}
}

func TestContracts_EnsuresNonPlainResult(t *testing.T) {
	l := lowerSrc(t, resSrc+`New(): E!Res
	ensures result.h == 1
{
	return Res(h: 1)
}
`)
	f := l.print("New")
	// The check borrows the value's place, its frame ends, then _0 moves
	// the value; _0 is never borrowed.
	wantIn(t, f, "_1 = m.Res(int 1)", "_2 = &_1", "call m.New#ensures0(copy _2)", "scope_end _3", "scope_end _2", "_0 = ok(move _1)")
	if strings.Contains(f, "&_0") {
		t.Fatalf("_0 is borrowed:\n%s", f)
	}
}

func TestContracts_GenericArgs(t *testing.T) {
	l := lowerSrc(t, `First[T](a: &T, n: int): &T
	requires n >= 0
	ensures n >= 0
{
	return a
}
struct Range[T] {
	lo: int
	hi: int
	v: T
	invariant lo <= hi
}
MkR(): Range[int] {
	return Range(lo: 1, hi: 2, v: 3)
}
MkG[T](v: T): Range[T] {
	return Range(lo: 0, hi: 0, v: v)
}
`)
	// requires and ensures pass the function's identity arguments; an
	// invariant passes the struct's own arguments as the value's type has
	// them: int, or the body's $0.
	wantIn(t, l.print("First"), "m.First#requires0[$0](", "m.First#ensures0[$0](")
	r := l.print("MkR")
	if count(r, "m.Range#invariant0[int](") != 2 {
		t.Fatalf("after the construction and on the returned value:\n%s", r)
	}
	if count(l.print("MkG"), "m.Range#invariant0[$0](") != 2 {
		t.Fatalf("generic body:\n%s", l.print("MkG"))
	}
}
