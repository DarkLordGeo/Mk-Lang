// Why: Slice[T] methods apply to slices, arrays and implementers, and each
// receiver form passes differently.
// What: TestSliceMethodReceiverReborrows,
// TestLenStaysBuiltinUnderSliceLen, TestR12_ReborrowThroughFnValue,
// TestSliceMethodShapesTrimmed, TestMutSelfOnSharedSliceIsExclusiveView,
// TestReceiverIntoKeepsReceiverForm, and lowerCorpusSrc.
// How: check records UseMethod of the method std declares on Slice[T], with
// the receiver passed with its own type; a mut []T local is reborrowed, not
// moved. Each test checks the receiver operand in the MIR.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// lowerCorpusSrc lowers module "m" over the corpus prelude (std.core's
// surface, which declares Slice[T]'s methods).
func lowerCorpusSrc(t *testing.T, src string) *lowered {
	t.Helper()
	w := newWorldCore(t, true, corpusPrelude(t))
	b := w.add("m", opt{}, src)
	b.wantClean(t)
	return lowerModule(t, w, b)
}

// TestSliceMethodReceiverReborrows: xs.Swap(0, 1) on a mut []T local
// reborrows it, so the local is still usable after the call; it was once
// moved into the first call and moved again by the second.
// A &self method on a mut []T takes a shared view (RvSlice{Shared}, the
// for-loop view) and passes it as a []T receiver is passed, by
// copy; a []T receiver is copied, an array or implementer
// is borrowed by the receiver mode, and a field path is not a reborrow (no
// projection, as for arguments).
func TestSliceMethodReceiverReborrows(t *testing.T) {
	l := lowerCorpusSrc(t, `Less(a: &int, b: &int): bool {
	return *a < *b
}

F(xs: mut []int): bool {
	xs.Sort(Less)
	xs.Swap(0, xs.Len() - 1)
	return xs.Has(&1)
}

G[T](xs: mut []T) {
	xs.Swap(0, 1)
	xs.Reverse()
}

H(xs: []int, var a: [3]int, var l: List[int]): bool {
	a.Sort(Less)
	l.Reverse()
	return xs.Has(&2)
}

struct Box2 {
	ys: mut []int
}

K(b: mut Box2) {
	b.ys.Swap(0, 1)
}
`)
	f := l.print("F")
	for _, frag := range []string{
		"call std.core.Slice:Sort[int](reborrow copy _1, ",
		"call std.core.Slice:Swap[int](reborrow copy _1, ",
		"= &_1[..]",
		"call std.core.Slice:Has[int](copy _",
	} {
		wantIn(t, f, frag)
	}
	if strings.Contains(f, "move _1") {
		t.Fatalf("xs is moved into a Slice method:\n%s", f)
	}
	// Evaluation order: the argument xs.Len() - 1 is pass 1, the reborrow
	// pass 2.
	if i, j := strings.Index(f, "Len(_1)"), strings.Index(f, "Slice:Swap"); i < 0 || i > j {
		t.Fatalf("xs.Len() is not read before the Swap call:\n%s", f)
	}
	g := l.print("G")
	if count(g, "reborrow copy _1") != 2 || strings.Contains(g, "move _1") {
		t.Fatalf("generic mut []T receiver:\n%s", g)
	}
	h := l.print("H")
	wantAll(t, h, "= mut _2", "= mut _3", "call std.core.Slice:Has[int](copy _")
	if strings.Contains(h, "reborrow") {
		t.Fatalf("no reborrow for a []T, array or implementer receiver:\n%s", h)
	}
	k := l.print("K")
	if strings.Contains(k, "reborrow") {
		t.Fatalf("a field path receiver is not an R12 reborrow:\n%s", k)
	}
}

// TestLenStaysBuiltinUnderSliceLen: with std.core declaring Slice[T].Len
// (the corpus prelude), .Len() on []T, mut []T, [N]T and str is still the
// builtin (RvLen, no call), so the counter-index elision still applies.
func TestLenStaysBuiltinUnderSliceLen(t *testing.T) {
	l := lowerCorpusSrc(t, `F(xs: []int, ys: mut []int, a: [3]int, s: str): int {
	return xs.Len() + ys.Len() + a.Len() + s.Len()
}

T(xs: []int): int {
	var s := 0
	for i in 0..xs.Len() {
		s += xs[i]
	}
	return s
}
`)
	f := l.print("F")
	wantAll(t, f, "Len(_1)", "Len(_2)", "Len(_3)", "Len(_4)")
	if strings.Contains(f, "call") {
		t.Fatalf(".Len() lowered to a call:\n%s", f)
	}
	if g := l.print("T"); strings.Contains(g, "panic bounds") {
		t.Fatalf("the counter-index elision no longer applies:\n%s", g)
	}
}

// TestR12_ReborrowThroughFnValue: the reborrow rule's "a parameter whose
// declared type is mut T or mut []T" includes a fn value's parameters, so
// f(ys) twice reborrows ys both times; before, the first call moved it. A
// const fn parameter's calls (CallValue on its local) do the same.
func TestR12_ReborrowThroughFnValue(t *testing.T) {
	l := lowerSrc(t, `Apply(f: fn(mut []int), ys: mut []int) {
	f(ys)
	f(ys)
}
Apply2(const g: fn(mut int), n: mut int) {
	g(n)
	g(n)
}
`)
	for _, k := range []string{"Apply", "Apply2"} {
		b := l.print(k)
		if count(b, "(reborrow copy _2)") != 2 || strings.Contains(b, "move _2") {
			t.Fatalf("%s: a mut argument through a fn value is not reborrowed:\n%s", k, b)
		}
	}
}

// TestSliceMethodShapesTrimmed runs the receiver shapes above through
// the trim pass and Validate: the &self view of a mut []T is a plain []T
// temporary, whose scope end trim may drop, and no corpus seed has it.
func TestSliceMethodShapesTrimmed(t *testing.T) {
	w := newWorldCore(t, true, corpusPrelude(t))
	b := w.add("m", opt{}, `F(xs: mut []int): int {
	xs.Swap(0, 1)
	r := xs.Get(0) else {
		return 0
	}
	if xs.Has(&1) {
		return *r
	}
	return 2
}
Apply(f: fn(mut []int), ys: mut []int) {
	f(ys)
	f(ys)
}
`)
	b.wantClean(t)
	bodies, ices := lowerUnitsWith(w, b, true)
	if len(ices) > 0 || len(bodies) != 2 {
		t.Fatalf("trimmed: %d bodies, ICEs %q", len(bodies), ices)
	}
}

// TestMutSelfOnSharedSliceIsExclusiveView
// (tests/conformance/rules/R12_reject_slice_mut_method.mk): a mut self
// Slice[T] method on a []T receiver (xs.Swap(0, 1) on xs: []i32) passes the
// exclusive view RvSlice{Exclusive} of xs into a mut []T temporary, the
// analogue of check's mut *x for a mut self on &T, which borrow rejects as
// E0507. Before, lower passed copy xs, a []T into a
// mut []T, so nothing reported E0507 and mono raised an ICE. The view
// carries the call's span (borrow's E0507 anchor, as an automatic receiver
// borrow's does); a &self method on a []T is still a copy, and a []T value
// receiver (a call result) gets the same exclusive view of its temporary.
func TestMutSelfOnSharedSliceIsExclusiveView(t *testing.T) {
	l := lowerCorpusSrc(t, `View(xs: []i32): []i32 {
	return xs
}

Bad(xs: []i32): bool {
	xs.Swap(0, 1)
	View(xs).Reverse()
	return xs.Has(&2)
}
`)
	f := l.print("Bad")
	wantIn(t, f, "= mut _1[..]", "call std.core.Slice:Swap[i32](move _", "= mut _", "call std.core.Slice:Reverse[i32](move _", "call std.core.Slice:Has[i32](copy _")
	if count(f, "mut _1[..]") != 1 || count(f, "Swap[i32](copy _1") != 0 {
		t.Fatalf("xs reaches Swap as a copy, not one exclusive view:\n%s", f)
	}
	b := l.body("Bad")
	views := 0
	for _, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvSlice && st.Rvalue.Borrow == mir.Exclusive {
				views++
				if st.Rvalue.Type != l.w.tab.MutSlice(types.I32) {
					t.Errorf("view typed %s, want mut []i32", l.w.tab.TypeString(st.Rvalue.Type, nil))
				}
				if views == 1 {
					sp := b.Span(st.Span)
					if got := l.w.lc(sp); got != "6:2" || int(sp.End-sp.Start) != len("xs.Swap(0, 1)") {
						t.Errorf("view at %s+%d, want the call 6:2+%d", got, sp.End-sp.Start, len("xs.Swap(0, 1)"))
					}
				}
			}
		}
	}
	if views != 2 {
		t.Fatalf("%d exclusive views, want 2 (Swap, Reverse):\n%s", views, f)
	}
}

// TestReceiverIntoKeepsReceiverForm: a receiver's `into` changes how
// neither receiver rule forms the receiver. Over a prelude whose Swap and
// Has take `mut self into self` and `&self into self`, a mut []T local
// is still reborrowed into Swap and viewed shared for Has, and a []T is
// still the exclusive view for Swap.
func TestReceiverIntoKeepsReceiverForm(t *testing.T) {
	pre := corpusPrelude(t)
	for _, r := range [][2]string{
		{"Slice[T].Swap(mut self, i: int, j: int)", "Slice[T].Swap(mut self into self, i: int, j: int)"},
		{"Slice[T].Has(&self, x: &T): bool", "Slice[T].Has(&self into self, x: &T): bool"},
	} {
		if !strings.Contains(pre, r[0]) {
			t.Fatalf("the prelude has no %q", r[0])
		}
		pre = strings.Replace(pre, r[0], r[1], 1)
	}
	w := newWorldCore(t, true, pre)
	b := w.add("m", opt{}, `F(xs: mut []int): bool {
	xs.Swap(0, 1)
	xs.Swap(1, 0)
	return xs.Has(&1)
}

Bad(xs: []i32) {
	xs.Swap(0, 1)
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	id, ok := l.w.items.ByKey(types.KeyFromString("std.core.Slice:Swap"))
	if fd, isFn := l.w.items.Get(id).Decl.(*types.FuncDecl); !ok || !isFn || fd.Sig.Params[0].Into != 1 {
		t.Fatalf("std.core.Slice:Swap: no signature whose receiver flows into itself")
	}
	f := l.print("F")
	if count(f, "call std.core.Slice:Swap[int](reborrow copy _1, ") != 2 || strings.Contains(f, "move _1") {
		t.Fatalf("a mut []T receiver with into is not reborrowed:\n%s", f)
	}
	wantIn(t, f, "= &_1[..]", "call std.core.Slice:Has[int](copy _")
	if g := l.print("Bad"); count(g, "mut _1[..]") != 1 || count(g, "Swap[i32](copy _1") != 0 {
		t.Fatalf("a []T receiver with into is not the exclusive view:\n%s", g)
	}
}
