// Why: operands are evaluated left to right, so a place read before a later
// operand writes it must be read into a slot first.
// What: TestR13_PlaceReadSlots, TestR13_CalleeValueFirst,
// TestR13_BorrowOfSameLocal, TestR13_RangeLoBeforeHi, TestSliceOmittedBounds.
// How: lowers bodies that once read a place after a later operand had
// written it, and checks for the slot copy in the MIR.

package lower

import (
	"strings"
	"testing"
)

// r13Src is the shared source of the evaluation-order slot tests. Every body
// here once read a place after a later operand had written it.
const r13Src = `struct C {
	n: int
}
C.Bump(mut self): int {
	self.n += 1
	return self.n
}
Bump(c: mut C): int {
	c.n += 1
	return c.n
}
Next(n: mut int): int {
	*n += 1
	return *n
}
Pair(a: int, b: int): int {
	return a * 10 + b
}
Set(n: int, p: mut int): int {
	*p = 9
	return n
}
Field(): int {
	var c := C(n: 1)
	return c.n + Bump(mut c)
}
FieldArg(): int {
	var c := C(n: 1)
	return Pair(c.n, Bump(mut c))
}
C.Deref(mut self): int {
	return self.n + self.Bump()
}
SliceLo(xs: []int): []int from xs {
	var i := 0
	return xs[i..Next(mut i)]
}
SameMut(): int {
	var n := 4
	return Set(n, mut n)
}
OtherMut(): int {
	var n := 4
	var m := 1
	return Set(n, mut m)
}
CompoundIndex(): int {
	var xs := [10, 20]
	var i := 0
	xs[Next(mut i)] += i
	return xs[1]
}
CompoundLiteral(): int {
	var xs := [10, 20]
	var i := 0
	xs[1] += i
	return xs[1]
}
PureReads(a: &C, b: &C, xs: []int): bool {
	return a.n * b.n < xs.Len()
}
`

// TestR13_PlaceReadSlots is the slot rule for every place an operand reads,
// not only a whole local: a field path (c.n + Bump(mut c)), a deref path
// (self.n + self.Bump()), a slice's lower bound before its upper bound
// runs, a whole local before a later mut of the same local, and the right
// side of a compound assignment whose index runs code. Without the slot the
// read happened after the write (the evaluator gave 4, 22, 4; the slice
// bound check and the RvSlice read two different values of i). The
// evaluator halves (evalhalves_test.go) run the same shapes.
func TestR13_PlaceReadSlots(t *testing.T) {
	l := lowerSrc(t, r13Src)
	wantIn(t, l.print("Field"), "_2 = copy _1.0", "_3 = mut _1", "call m.Bump(move _3)", "Add(copy _2, copy _4)")
	wantIn(t, l.print("FieldArg"), "_2 = copy _1.0", "_3 = mut _1", "call m.Pair(copy _2, copy _4)")
	wantIn(t, l.print("C:Deref"), "_2 = copy (*_1).0", "call m.C:Bump(reborrow copy _1)", "Add(copy _2, copy _3)")
	// The check and the slice use the one value read before Next.
	s := l.print("SliceLo")
	wantIn(t, s, "_3 = copy _2", "_4 = Trunc[uint](copy _3)", "_5 = mut _2", "call m.Next", "&_1[copy _3..copy _6]")
	wantIn(t, l.print("SameMut"), "_2 = copy _1", "_3 = mut _1", "call m.Set(copy _2, move _3)")
	// A mut of another local is inert: n stays direct.
	if o := l.print("OtherMut"); !strings.Contains(o, "call m.Set(copy _1, move _3)") {
		t.Errorf("OtherMut: n is not passed directly:\n%s", o)
	}
	wantIn(t, l.print("CompoundIndex"), "_3 = copy _2", "_4 = mut _2", "call m.Next", "Add(copy _1[_6], copy _3)")
	// An inert index runs nothing: no slot.
	// Proved by the range analysis: xs holds 10 and 20, i is 0.
	if c := l.print("CompoundLiteral"); !strings.Contains(c, "AddNoWrap(copy _1[_3], copy _2)") {
		t.Errorf("CompoundLiteral: i is slotted:\n%s", c)
	}
	// Pure reads (deref paths, the builtin Len) are inert: no slots at all.
	if p := l.print("PureReads"); !strings.Contains(p, "Mul(copy (*_1).0, copy (*_2).0)") {
		t.Errorf("PureReads: a deref read is slotted:\n%s", p)
	}
}

// TestSliceOmittedBounds is the slice check with an omitted bound: no
// test is emitted against it (0 <= u for every uint, len <= len), so xs[..]
// has no check and xs[..b], xs[a..] test one comparison each.
func TestSliceOmittedBounds(t *testing.T) {
	l := lowerSrc(t, `Whole(xs: []int): []int from xs {
	return xs[..]
}
Head(xs: []int, n: int): []int from xs {
	return xs[..n]
}
Tail(xs: []int, n: int): []int from xs {
	return xs[n..]
}
Both(xs: []int, a: int, b: int): []int from xs {
	return xs[a..b]
}
`)
	for _, c := range []struct {
		body       string
		les, panic int
	}{
		{"Whole", 0, 0},
		{"Head", 1, 1},
		{"Tail", 1, 1},
		{"Both", 2, 1},
	} {
		p := l.print(c.body)
		if n, m := strings.Count(p, "= Le("), strings.Count(p, "panic bounds"); n != c.les || m != c.panic {
			t.Errorf("%s: %d Le tests and %d panic blocks, want %d and %d:\n%s", c.body, n, m, c.les, c.panic, p)
		}
	}
	wantIn(t, l.print("Head"), "Trunc[uint](copy _2)", "Le(copy _5, copy _4)", "&_1[..copy _2]")
}

// TestR13_CalleeValueFirst: a CallValue callee read from a place is read
// before the arguments run (h.f(Reset(mut h)) calls the f read first), and
// a const fn parameter stays CallValue on its own local (mono resolves it).
func TestR13_CalleeValueFirst(t *testing.T) {
	l := lowerSrc(t, `struct H {
	f: fn(int): int
}
Inc(x: int): int {
	return x + 1
}
Dbl(x: int): int {
	return x * 2
}
Reset(h: mut H): int {
	h.f = Dbl
	return 5
}
Call(): int {
	var h := H(f: Inc)
	return h.f(Reset(mut h))
}
Five(): int {
	return 5
}
Apply(const k: fn(int): int): int {
	return k(Five())
}
`)
	wantIn(t, l.print("Call"), "_3 = copy _2.0", "_4 = mut _2", "call m.Reset(move _4)", "call (copy _3)(copy _5)")
	wantIn(t, l.print("Apply"), "call m.Five()", "call (copy _1)(copy _2)")
}

// TestR13_BorrowOfSameLocal: a shared borrow of the read's own local is
// not inert (only a borrow of a place other than x is), so Look(n, &n)
// reads n into a slot first and Look(n, &m) passes n directly.
func TestR13_BorrowOfSameLocal(t *testing.T) {
	l := lowerSrc(t, `Look(n: int, p: &int): int {
	return n + *p
}
SameShared(): int {
	n := 4
	return Look(n, &n)
}
OtherShared(): int {
	n := 4
	m := 1
	return Look(n, &m)
}
`)
	wantIn(t, l.print("SameShared"), "_2 = copy _1", "_3 = &_1", "call m.Look(copy _2, copy _3)")
	if o := l.print("OtherShared"); !strings.Contains(o, "call m.Look(copy _1, copy _3)") {
		t.Errorf("OtherShared: n is not passed directly:\n%s", o)
	}
}

// TestR13_RangeLoBeforeHi: the counter of `for i in a..b` is assigned after
// b runs, so an a that b may write is read into a slot at its own position;
// an inert b keeps a direct. The evaluator half is R13RangeLo in
// evalhalves_test.go (11; 0 without the slot).
func TestR13_RangeLoBeforeHi(t *testing.T) {
	l := lowerSrc(t, `Next(n: mut int): int {
	*n += 1
	return *n
}
Late(): int {
	var n := 1
	var s := 0
	for i in n..Next(mut n) {
		s += i
	}
	return s
}
Inert(m: int): int {
	var n := 1
	var s := 0
	for i in n..m {
		s += i
	}
	return s
}
`)
	late := l.print("Late")
	// _3 = copy _1 (the slot) comes before the call that writes n.
	slot, call := strings.Index(late, "= copy _1\n"), strings.Index(late, "call m.Next")
	if slot < 0 || call < 0 || slot > call {
		t.Fatalf("lo is not read before hi runs:\n%s", late)
	}
	// The slot is one more temporary than the inert shape: Late has n, s,
	// the slot, the mut n borrow, the call result, hi, the counter, the
	// test and i; Inert has n, s, hi, the counter, the test and i.
	if n := count(late, "// temp\n"); n != 6 {
		t.Fatalf("Late has %d temporaries, want 6:\n%s", n, late)
	}
	inert := l.print("Inert")
	if n := count(inert, "// temp\n"); n != 3 {
		t.Fatalf("Inert has %d temporaries, want 3 (no slot):\n%s", n, inert)
	}
}
