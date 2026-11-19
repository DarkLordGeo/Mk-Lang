// Why: each `for ... in` form (by copy, &, mut, over an implementer) binds
// its element differently, and lower elides checks and copies in loops it
// can prove safe.
// What: TestR20_* (holders and elements), TestArrayIteratedByCopy, TestMutSliceHolderReborrows and their kin (the
// loop forms), TestForLoopsCarryNone, TestImplementerImplArgs.
// How: lowers small loops and checks locals, types and checks in the MIR.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
)

func TestR20_HolderAndElements(t *testing.T) {
	l := lowerSrc(t, `AllArr() {
	var a := [1, 2, 3]
	for x in mut a {
		*x += 1
	}
}
AllSlice(xs: mut []int) {
	for x in mut xs {
		*x += 1
	}
}
`)
	for _, k := range []string{"AllArr", "AllSlice"} {
		b := l.body(k)
		f := l.print(k)
		if len(b.Loops) != 1 || b.Loops[0].Holder == mir.NoLocal {
			t.Fatalf("%s: one loop with a holder:\n%s", k, f)
		}
		h := b.Loops[0].Holder
		if b.Locals[h].Kind != mir.LocalLoopHolder {
			t.Fatalf("%s: the holder is a LocalLoopHolder", k)
		}
		// One OriginLoopHolder reslice into the holder, elements through it.
		wantIn(t, f, "= mut holder(loop0) _1[..]", "= mut elem(loop0) _2[_")
		if count(f, "holder(loop0)") != 1 {
			t.Fatalf("%s: the holder is assigned once:\n%s", k, f)
		}
	}
}

func TestR20_ImplementerItemsMut(t *testing.T) {
	l := lowerSrc(t, `Bump(list: mut HeapList[int]) {
	for x in mut list {
		*x += 1
	}
}
`)
	b := l.body("Bump")
	f := l.print("Bump")
	// The holder borrows list's Self place, ItemsMut reborrows the holder,
	// and its result lives to the loop's exit.
	wantIn(t, f, "_2 = mut holder(loop0) (*_1)", "_3 = call std.core#impl(std.core.Slice for std.core.HeapList):ItemsMut[int](reborrow copy _2)",
		"= mut elem(loop0) _3[_", "scope_end _3", "scope_end _2")
	if b.Loops[0].Holder != 2 {
		t.Fatalf("the holder is _2: %+v", b.Loops[0])
	}
}

func TestR20_ImplementerReceiverDerefs(t *testing.T) {
	l := lowerSrc(t, `Bump(list: mut HeapList[int]) {
	for x in mut list {
		*x += 1
	}
}
Read(list: &HeapList[int]): int {
	var s := 0
	for x in list {
		s += x
	}
	return s + list[0]
}
`)
	// The operand passed to ItemsMut has exactly its mut self type, mut
	// HeapList[int]; to Items, &HeapList[int]. Validate's M5 checks the
	// types; the locals' types are pinned here.
	bump := l.body("Bump")
	if got := l.w.tab.TypeString(bump.Locals[2].Type, nil); got != "mut HeapList[int]" {
		t.Fatalf("the ItemsMut receiver is %s", got)
	}
	read := l.body("Read")
	f := l.print("Read")
	wantIn(t, f, "_3 = &holder(loop0) (*_1)", "Items[int](copy _3)", "_11 = &(*_1)", "Items[int](copy _11)")
	for _, k := range []mir.LocalID{3, 11} {
		if got := l.w.tab.TypeString(read.Locals[k].Type, nil); got != "&HeapList[int]" {
			t.Fatalf("the Items receiver _%d is %s", k, got)
		}
	}
}

func TestMutSliceByCopyReads(t *testing.T) {
	l := lowerSrc(t, `F(xs: mut []i32) {
	var s: i32 = 0
	for x in xs {
		s += x
	}
	xs[0] = s
}
`)
	f := l.print("F")
	// No move of xs: the loop indexes the place, its length read once.
	if strings.Contains(f, "move _1") {
		t.Fatalf("xs is moved:\n%s", f)
	}
	wantIn(t, f, "_3 = Len(_1)", "= copy _1[_4]", "_1[_8] = copy _7")
	if count(f, "Len(_1)") != 2 {
		t.Fatalf("one Len before the loop, one for the bounds check after:\n%s", f)
	}
}

func TestArrayIteratedByCopy(t *testing.T) {
	l := lowerSrc(t, `F(): int {
	var a := [1, 2, 3]
	for x in a {
		a[0] += x
	}
	return a[0]
}
G(): int {
	a := [1, 2, 3]
	var s := 0
	for x in a {
		s += x
	}
	return s
}
`)
	// a is written in the body: one copy before the loop, elements from it,
	// no loan on a.
	f := l.print("F")
	wantIn(t, f, "_2 = copy _1", "_3 = Len(_2)", "= copy _2[_4]", "_1[_7] = Add(copy _1[_7]")
	if strings.Contains(f, "&_1") || strings.Contains(f, "holder") {
		t.Fatalf("a loan on a:\n%s", f)
	}
	// Not written: the copy is elided and the loop indexes a.
	g := l.print("G")
	wantIn(t, g, "_3 = Len(_1)", "= copy _1[_4]")
	if strings.Contains(g, "= copy _1\n") {
		t.Fatalf("the copy is not elided:\n%s", g)
	}
}

func TestMutSliceHolderReborrows(t *testing.T) {
	l := lowerSrc(t, `G(l: mut HeapList[int]) {
	xs := l.ItemsMut()
	for x in mut xs {
		*x += 1
	}
}
`)
	b := l.body("G")
	f := l.print("G")
	wantIn(t, f, "_3 = move _2", "_4 = mut holder(loop0) _3[..]", "_8 = mut elem(loop0) _4[_6]")
	if got := l.w.tab.TypeString(b.Locals[4].Type, nil); got != "mut []int" || b.Loops[0].Holder != 4 {
		t.Fatalf("the holder is an exclusive reslice of type mut []int, got %s, %+v", got, b.Loops[0])
	}
}

func TestForLoopsCarryNone(t *testing.T) {
	l := lowerSrc(t, `A(xs: []int): int {
	var s := 0
	for x in xs {
		s += x
	}
	for i, x in xs {
		s += x + i
	}
	for x in &xs {
		s += *x
	}
	return s
}
B(var a: [4]int, ys: mut []int, list: mut HeapList[int]): int {
	for x in mut a {
		*x += 1
	}
	for x in mut ys {
		*x += 1
	}
	for x in mut list {
		*x += 1
	}
	var s := 0
	for x in &a {
		s += *x
	}
	for x in list {
		s += x
	}
	return s
}
`)
	for _, k := range []string{"A", "B"} {
		if f := l.print(k); strings.Contains(f, "panic bounds") {
			t.Fatalf("%s carries a bounds check:\n%s", k, f)
		}
	}
}

func TestSourceScans(t *testing.T) {
	l := lowerSrc(t, `Reassign(var xs: mut []int, ys: mut []int): int {
	var s := 0
	for x in xs {
		s += x
		xs = ys
	}
	return s
}
ElemWrite(var xs: mut []int): int {
	var s := 0
	for x in xs {
		s += x
		xs[0] = 1
	}
	return s
}
VarArrayRead(): int {
	var a := [1, 2, 3]
	var s := 0
	for x in a {
		s += x
	}
	a[0] = s
	return a[0]
}
`)
	// A var mut []T reassigned in its loop: a shared view holds it.
	wantIn(t, l.print("Reassign"), "_4 = &_1[..]", "Len(_4)", "copy _4[_")
	// An element write changes neither pointer nor length: no view.
	e := l.print("ElemWrite")
	wantIn(t, e, "Len(_1)", "copy _1[_")
	if strings.Contains(e, "&_1[..]") {
		t.Fatalf("an element write takes a view:\n%s", e)
	}
	// A var array the body does not write is indexed directly (the copy is
	// elided by the scan).
	v := l.print("VarArrayRead")
	wantIn(t, v, "_3 = Len(_1)", "copy _1[_4]")
}

// TestImplementerImplArgs: Items gets the impl's own generic arguments,
// bound by matching the impl's target type, not the implementer type's
// arguments: none for impl Slice[int] for Ints, and [int, u8] for
// impl[A, B] Slice[A] for Pair[B, A] at Pair[u8, int].
func TestImplementerImplArgs(t *testing.T) {
	l := lowerSrc(t, `struct Ints {
	p: *int
	n: int
}
impl Slice[int] for Ints {
	Items(&self): []int from self {
		Panic("x")
	}
	ItemsMut(mut self): mut []int from self {
		Panic("x")
	}
}
struct Pair[K, V] {
	k: *K
	v: *V
}
impl[A, B] Slice[A] for Pair[B, A] {
	Items(&self): []A from self {
		Panic("x")
	}
	ItemsMut(mut self): mut []A from self {
		Panic("x")
	}
}
Sum(xs: &Ints): int {
	var s := 0
	for x in xs {
		s += x
	}
	return s
}
SumP(xs: &Pair[u8, int]): int {
	var s := 0
	for x in xs {
		s += x
	}
	return s + xs[0]
}
`)
	wantIn(t, l.print("Sum"), "for m.Ints):Items(copy _")
	p := l.print("SumP")
	wantIn(t, p, "for m.Pair):Items[int, u8](copy _", "for m.Pair):Items[int, u8](copy _")
	if strings.Contains(p, "Items[u8, int]") {
		t.Fatalf("Items got the implementer type's arguments:\n%s", p)
	}
}

// TestArrayElisionNoInteriorWrites retires the worry that the
// array-copy elision's name scan misses an interior write (a Cell element
// changed through &a while for x in a runs). A by-copy loop over a non-plain
// element type is rejected: lower reads the element as Copy(a[c]) whether or
// not the copy is elided, and borrow reports E0409 on a Copy of a non-plain
// place with steps (a by-copy loop needs a plain T). Every accepted loop has
// a plain element type, and a plain type holds no Cell (anything holding a
// Cell is Unique); an element &Cell[int] is a reference value that a write
// through it does not change, so the elided and the copied loop read the
// same values. The scan counts names, which over-approximates paths, so it
// keeps copies, never drops one.
func TestArrayElisionNoInteriorWrites(t *testing.T) {
	l := lowerWith(t, `Take(c: Cell[int]) {
}
F(a: [2]Cell[int]) {
	for x in a {
		Take(x)
	}
}
G(a: [2]&Cell[int]): int {
	var s := 0
	for x in a {
		a[1].Store(5)
		s += x.Load()
	}
	return s
}
`, func(c *Config) { c.Trim = true })
	f := l.print("F")
	// x (_5, Cell[int], never borrowed) keeps its scope end under trim, so
	// the harness's facts call Cell[int] non-plain; its element read is the
	// Copy of a place with an Index step that borrow rejects.
	wantIn(t, f, "let _5: Cell[int] // x", "_5 = copy _1[_3]", "scope_end _5")
	if strings.Contains(f, "= copy _1\n") {
		t.Fatalf("an array copy before the loop:\n%s", f)
	}
	// [2]&Cell[int]: a is not var, so the loop indexes a directly; the
	// Store goes through a copied &Cell element and writes no element of a.
	g := l.print("G")
	wantIn(t, g, "_6 = copy _1[_4]", "= &(*_1[")
	if strings.Contains(g, "= copy _1\n") {
		t.Fatalf("an array copy before the loop:\n%s", g)
	}
}
