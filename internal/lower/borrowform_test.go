// Why: places (qualified globals, an index inside a write target, a slice of
// an implementer), slice borrows and a reference match scrutinee each have
// one correct evaluation order and place, which lower must keep.
// What: TestR13_SliceBorrowFormedLast, TestMatch_RefScrutineeOnce,
// TestIndexInsideWriteTargetReads, TestSliceOfImplementer,
// TestQualifiedGlobalIsPlace.
// How: each lowers the shape and checks the MIR. Borrow's half of each
// shape (no false E0504, E0507, E0409 or E0506) cannot run here (archtest's
// test edges for lower are syntax and eval).

package lower

import (
	"strings"
	"testing"
)

// TestR13_SliceBorrowFormedLast: an argument written mut xs[a..b]
// (or &xs[a..b]) is a borrow form, formed in pass 2 after every other
// operand, its bound expressions and bounds check included.
// Before, Fill(mut a[I()..], G()) called I, checked and formed the
// exclusive slice, then called G with the slice live.
func TestR13_SliceBorrowFormedLast(t *testing.T) {
	l := lowerSrc(t, `I(): int {
	return 0
}
G(): int {
	return 1
}
Fill(xs: mut []int, n: int) {
}
Show(xs: &[]int, n: int) {
}
F() {
	var a := [1, 2, 3]
	Fill(mut a[I()..], G())
}
H() {
	var a := [1, 2, 3]
	Fill(mut a[0..], a.Len())
}
S() {
	a := [1, 2, 3]
	Show(&a[I()..2], G())
}
`)
	f := l.print("F")
	wantIn(t, f, "_2 = call m.G()", "_3 = call m.I()", "Le(", "_8 = mut _1[copy _3..]", "call m.Fill(move _8, copy _2)")
	// H: a.Len() is read before the exclusive slice exists, so borrow sees
	// no access of a under the loan (the false E0504 of the defect).
	h := l.print("H")
	wantIn(t, h, "_2 = Len(_1)", "Le(", "_7 = mut _1[int 0..]", "call m.Fill(move _7, copy _2)")
	// &a[I()..2] is typed &[]int by check: a reference to the shared
	// view's temporary, formed last too. Before this change it lowered to
	// the bare []int, which Validate rejected (M5).
	wantIn(t, l.print("S"), "_2 = call m.G()", "_3 = call m.I()", "Le(", "_10 = &_1[copy _3..int 2]", "_11 = &_10",
		"call m.Show(copy _11, copy _2)")
}

// TestMatch_RefScrutineeOnce: a reference-typed scrutinee that is not a
// place is materialized into one temporary: its call runs
// once. Before, matchStmt called placeProper (which fell back to tempOf)
// and then tempOf again, so Get ran twice.
func TestMatch_RefScrutineeOnce(t *testing.T) {
	l := lowerSrc(t, `enum Shape {
	Circle(r: int)
	Square(w: int)
}
Get(s: &Shape): &Shape from s {
	return s
}
Area(s: &Shape): int {
	match Get(s) {
		.Circle(r) {
			return *r
		}
		.Square(w) {
			return *w
		}
	}
}
`)
	a := l.print("Area")
	if n := strings.Count(a, "call m.Get"); n != 1 {
		t.Fatalf("Area: %d calls of Get, want 1:\n%s", n, a)
	}
	wantIn(t, a, "_2 = call m.Get(copy _1)", "Discriminant((*_2))")

	// The evaluator half: the scrutinee call counts its runs.
	got, err := evalH(t, `enum Shape {
	Circle(r: int)
	Square(w: int)
}
Get(n: mut int, s: &Shape): &Shape from s {
	*n += 1
	return s
}
H(): int {
	var n := 0
	s := Shape.Circle(r: 7)
	match Get(mut n, &s) {
		.Circle(r) {
			return n * 10 + *r
		}
		.Square(w) {
			return n * 10 + *w
		}
	}
}
`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if got != 17 {
		t.Fatalf("H() = %d, want 17 (Get ran once, r = 7)", got)
	}
}

// TestIndexInsideWriteTargetReads: placeMut, set for a write
// target, holds on the place's spine only. An index expression inside it
// is a read: o[0] in l[o[0]] = 5 indexes o through Items(&*o), and ItemsMut
// is called once, for l. Before, o went through ItemsMut(mut *o), which
// borrow rejects (E0507 through a shared reference).
func TestIndexInsideWriteTargetReads(t *testing.T) {
	l := lowerSrc(t, `Get(l: &List[int], i: int): &List[int] from l {
	return l
}
Set(l: mut List[int], o: &List[int]) {
	l[o[0]] = 5
}
Add(l: mut List[int], o: &List[int]) {
	l[o[0]] += 1
}
Arg(p: mut int) {
}
Pass(l: mut List[int], o: &List[int]) {
	Arg(mut l[o[0]])
}
Val(l: mut List[int], o: &List[int]) {
	Arg(mut l[Get(o, o[0])[0]])
}
`)
	for _, k := range []string{"Set", "Add", "Pass", "Val"} {
		b := l.print(k)
		if n := strings.Count(b, ":ItemsMut[int]("); n != 1 {
			t.Fatalf("%s: %d ItemsMut calls, want 1 (for l):\n%s", k, n, b)
		}
		if strings.Contains(b, "mut (*_2)") {
			t.Fatalf("%s: o is borrowed mut:\n%s", k, b)
		}
		// The index runs before l is borrowed (left-to-right evaluation).
		wantIn(t, b, "&(*_2)", ":Items[int](copy _", "mut (*_1)", ":ItemsMut[int](move _")
	}
}

// TestSliceOfImplementer: slicing a Slice implementer slices its
// Items() (shared) or ItemsMut() (exclusive) view, and the bounds check
// reads that view's length. Before, the RvSlice's place was the List
// itself, which Validate rejects (M4).
func TestSliceOfImplementer(t *testing.T) {
	l := lowerSrc(t, `F(o: &List[int]): []int from o {
	return o[0..1]
}
G(o: mut List[int]) {
	s := mut o[0..1]
	s[0] = 1
}
Fill(xs: mut []int) {
}
P(o: mut List[int]) {
	Fill(mut o[1..])
}
`)
	f := l.print("F")
	// The bounds run before the view is borrowed (left-to-right evaluation).
	wantIn(t, f, "_2 = Trunc[uint](int 0)", "_3 = &(*_1)", "_4 = call std.core#impl(std.core.Slice for std.core.List):Items[int](copy _3)",
		"Len(_4)", "Le(", "&_4[int 0..int 1]")
	g := l.print("G")
	wantIn(t, g, "_3 = mut (*_1)", "_4 = call std.core#impl(std.core.Slice for std.core.List):ItemsMut[int](move _3)",
		"Len(_4)", "Le(", "mut _4[int 0..int 1]")
	if strings.Contains(f, ":ItemsMut[") || strings.Contains(g, ":Items[int]") {
		t.Fatalf("wrong view:\n%s\n%s", f, g)
	}
	wantIn(t, l.print("P"), ":ItemsMut[int](move _", "Len(_", "Le(", "mut _4[int 1..]", "call m.Fill(move _")
}

// TestQualifiedGlobalIsPlace: a module-qualified var global
// (other.Hits, a FieldExpr with UseGlobal) is the place *(&G), as the
// unqualified name is: &other.Hits and a receiver
// borrow on it borrow the global through its static reference. Before,
// isPlaceExpr said no, so both moved the global into a temporary and
// borrowed the temporary.
func TestQualifiedGlobalIsPlace(t *testing.T) {
	w := newWorld(t, true)
	o := w.add("other", opt{}, `export view struct Counter {
	m: Mutex[int]
}
export Counter.Hit(&self) {
}
export var Hits: Counter = Make()
Make(): Counter {
	Panic("x")
}
In(): &Counter {
	Hits.Hit()
	return &Hits
}
`)
	o.wantClean(t)
	in := lowerModule(t, w, o).print("In")
	m := w.add("m", opt{}, `import other
Out(): &other.Counter {
	other.Hits.Hit()
	return &other.Hits
}
`)
	m.wantClean(t)
	out := lowerModule(t, w, m).print("Out")
	want := []string{"_1 = &global other.Hits", "_2 = &(*_1)", "call other.Counter:Hit(copy _2)",
		"_4 = &global other.Hits", "_5 = &(*_4)", "_0 = copy _5"}
	wantIn(t, in, want...)
	wantIn(t, out, want...)
	if strings.Contains(out, "move (*") {
		t.Fatalf("Out: the global is moved out:\n%s", out)
	}
	body := func(s string) string { return s[strings.Index(s, "bb0:"):] }
	if body(in) != body(out) {
		t.Fatalf("qualified and unqualified forms differ:\n%s\n%s", in, out)
	}
}
