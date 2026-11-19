// Why: evaluation order and index checks, destructuring, for-in sources,
// ?T else, the onerr guard, scope values, Must and `if T is X` each have a
// MIR shape later passes rely on, and each once lowered wrong.
// What: TestNestedIndexStaleLocal,
// TestR13_EarlierBorrowFormSlotsLaterArg, TestScopeValueWithNestedCall,
// TestMutSliceByCopyReads and the other index and copy tests, TestNestedStructDestructure, TestOptElseTakesFromTemporary, TestR21_OnerrGuard*,
// TestTypeTest_OuterDeferOnExitFromBranch.
// How: each lowers the shape, validates it and checks the MIR text.

package lower

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestNestedIndexStaleLocal: a var index local that a later
// index expression or slice bound of the same place writes is read into a
// slot before it runs, so the bounds check and the access use one value.
// Before, ys[i][Bump(mut i)] checked ys[0] and read ys[5] unchecked.
func TestNestedIndexStaleLocal(t *testing.T) {
	l := lowerSrc(t, `Bump(x: mut int): int {
	*x += 5
	return 0
}
Read(ys: [][]int): int {
	var i := 0
	return ys[i][Bump(mut i)]
}
Write() {
	var ys := [[1, 2], [3, 4]]
	var i := 0
	ys[i][Bump(mut i)] = 0
}
Slice(ys: [][]int): []int from ys {
	var i := 0
	return ys[i][0..Bump(mut i)]
}
Inert(ys: [][]int, j: int): int {
	var i := 0
	return ys[i][j]
}
`)
	// i is _2; its slot _3 is read before Bump, and the check, the inner
	// length and the access all use _3.
	r := l.print("Read")
	wantIn(t, r, "_3 = copy _2", "Trunc[uint](copy _3)", "Len(_1)", "call m.Bump", "Len(_1[_3])", "_0 = copy _1[_3][_")
	if strings.Contains(r, "_1[_2]") {
		t.Fatalf("Read: the place still indexes by the live i:\n%s", r)
	}
	wantIn(t, l.print("Write"), "_5 = copy _4", "Trunc[uint](copy _5)", "call m.Bump", "Len(_3[_5])", "_3[_5][_")
	s := l.print("Slice")
	wantIn(t, s, "_3 = copy _2", "Trunc[uint](copy _3)", "call m.Bump", "Len(_1[_3])", "&_1[_3][")
	// An inert later index runs nothing: i stays direct.
	if in := l.print("Inert"); !strings.Contains(in, "copy _1[_3][_2]") {
		t.Fatalf("Inert: i is slotted though nothing later writes it:\n%s", in)
	}
}

// TestR13_EarlierBorrowFormSlotsLaterArg: a by-value whole-local
// argument stays direct only when every operand that runs after it is
// inert, and pass-2 borrow forms written before it run after it. F(mut
// ys[Next(mut i)], i) passes the i read in pass 1 (0), not the one Next
// wrote.
func TestR13_EarlierBorrowFormSlotsLaterArg(t *testing.T) {
	l := lowerSrc(t, `Next(n: mut int): int {
	*n += 1
	return *n
}
F(p: mut int, n: int) {
}
G(xs: [4]int) {
	var i := 0
	var ys := xs
	F(mut ys[Next(mut i)], i)
}
H(xs: [4]int) {
	var i := 0
	var ys := xs
	F(mut ys[0], i)
}
`)
	g := l.print("G")
	wantIn(t, g, "_4 = copy _2", "call m.Next", "call m.F(move _", ", copy _4)")
	if strings.Contains(g, "copy _2)") {
		t.Fatalf("G: F reads i at the call, after Next wrote it:\n%s", g)
	}
	// An inert earlier borrow form leaves i direct.
	if h := l.print("H"); !strings.Contains(h, ", copy _2)") {
		t.Fatalf("H: i is slotted though the borrow form is inert:\n%s", h)
	}
}

// TestScopeValueWithNestedCall: the LocalScope destination
// belongs to the scope's own call, never to a call nested in its receiver
// or arguments (before: Validate M5, a Scope destination for an int
// result).
func TestScopeValueWithNestedCall(t *testing.T) {
	l := lowerSrc(t, `Pick(io: Io, n: int): Io {
	return io
}
N(): int {
	return 1
}
W() {
}
S(io: Io) {
	scope s := Pick(io, N()).Scope() {
		s.go W()
	}
}
`)
	b := l.body("S")
	f := l.print("S")
	var scopeLocal mir.LocalID
	for i, lc := range b.Locals {
		if lc.Kind == mir.LocalScope {
			scopeLocal = mir.LocalID(i)
		}
	}
	if scopeLocal == mir.NoLocal {
		t.Fatalf("no LocalScope local:\n%s", f)
	}
	calls := 0
	for _, blk := range b.Blocks {
		tm := blk.Term
		if tm.Kind != mir.TermCall || tm.Spawn != mir.NoLocal {
			continue
		}
		calls++
		isScope := tm.Callee.Kind == mir.CallDirect && strings.Contains(l.w.items.Get(tm.Callee.Fn.Item).Key.String(), "Scope")
		if (tm.Dest.Local == scopeLocal) != isScope {
			t.Fatalf("call %d: destination %d, LocalScope %d, scope call %v:\n%s", calls, tm.Dest.Local, scopeLocal, isScope, f)
		}
		if !isScope && b.Locals[tm.Dest.Local].Kind != mir.LocalTemp {
			t.Fatalf("a nested call's destination is not a temporary:\n%s", f)
		}
		if !isScope && strings.HasSuffix(l.w.items.Get(tm.Callee.Fn.Item).Key.String(), ".N") && b.Locals[tm.Dest.Local].Type != types.Int {
			t.Fatalf("N's destination is not an int temporary:\n%s", f)
		}
	}
	if calls != 3 {
		t.Fatalf("want N, Pick and Io.Scope, got %d calls:\n%s", calls, f)
	}
}

// TestIndexedSourceIndexWritten: a for-in source place whose
// Index step reads a var local the body writes is evaluated once:
// an array by copy is copied, a mut []T by copy or by & is viewed, before
// the loop. Before, m[j] was indexed live and j = 1 moved the loop to
// another row past the one bounds check.
func TestIndexedSourceIndexWritten(t *testing.T) {
	l := lowerSrc(t, `Arr(m: [2][3]int): int {
	var s := 0
	var j := 0
	for x in m[j] {
		s += x
		j = 1
	}
	return s
}
Mut(a: [2](mut []int)): int {
	var s := 0
	var j := 0
	for x in a[j] {
		s += x
		j = 1
	}
	return s
}
Ref(a: [2](mut []int)): int {
	var s := 0
	var j := 0
	for x in &a[j] {
		s += *x
		j = 0
	}
	return s
}
Keep(a: [2](mut []int), m: [2][3]int): int {
	var s := 0
	var j := 0
	for x in a[j] {
		s += x
	}
	for x in m[j] {
		s += x
	}
	return s
}
`)
	for _, c := range []struct{ body, before, elem string }{
		{"Arr", "_8 = copy _1[_3]", "copy _8[_10]"},
		{"Mut", "_8 = &_1[_3][..]", "copy _8[_10]"},
		{"Ref", "_8 = &_1[_3][..]", "&_8[_10]"},
	} {
		f := l.print(c.body)
		wantIn(t, f, c.before, "Len(_8)", c.elem)
		if strings.Contains(f, "_1[_3][_") {
			t.Fatalf("%s: the element is read through the live index:\n%s", c.body, f)
		}
	}
	// j unwritten: the source is indexed in place, no copy and no view.
	k := l.print("Keep")
	if strings.Contains(k, "[..]") || strings.Contains(k, "= copy _2[_4]\n") {
		t.Fatalf("Keep: a view or copy for an index the body never writes:\n%s", k)
	}
	wantIn(t, k, "Len(_1[_4])", "copy _1[_4][_", "Len(_2[_4])", "copy _2[_4][_")
}

// TestNestedStructDestructure: a nested struct sub-pattern of
// a destructure binds out of the one whole temporary. Before, it
// was an ICE.
func TestNestedStructDestructure(t *testing.T) {
	l := lowerSrc(t, resSrc+`struct Point {
	x: int
	y: int
}
F(r: (p: Point, n: int)): int {
	(p: Point(x, y), n) := r
	return x + y + n
}
linear struct Wrap {
	r: Res
	k: int
}
G(w: (q: Wrap, n: int)): int {
	(q: Wrap(r, k), n) := w
	r.Abandon()
	return k + n
}
`)
	// Record fields are name-sorted: n is field 0, p field 1.
	wantIn(t, l.print("F"), "_2 = copy _1", "_3 = copy _2.1.0", "_4 = copy _2.1.1", "_5 = copy _2.0")
	g := l.print("G")
	wantIn(t, g, "_2 = move _1", "_3 = move _2.1.0", "_4 = copy _2.1.1", "_5 = copy _2.0")
	if strings.Contains(g, "_1.") {
		t.Fatalf("G: a field is taken out of the parameter, not the temporary:\n%s", g)
	}
}

// TestOptElseTakesFromTemporary: e else { } and .Must() on a ?T
// place read the place once into a whole temporary and take the payload
// from it. Before, the payload was moved straight out of the parameter, an
// E0409 to borrow.
func TestOptElseTakesFromTemporary(t *testing.T) {
	l := lowerSrc(t, resSrc+`F(o: ?Res) {
	r := o else {
		return
	}
	r.Abandon()
}
G(o: ?Res) {
	r := o.Must()
	r.Abandon()
}
H(o: ?int): int {
	return o else {
		return 0
	}
}
`)
	for _, k := range []string{"F", "G"} {
		f := l.print(k)
		wantIn(t, f, "_2 = move _1", "_3 = Discriminant(_2)", "move (_2 as some).0")
		if strings.Contains(f, "(_1 as some)") || strings.Contains(f, "Discriminant(_1)") {
			t.Fatalf("%s: the ?T parameter is read in place:\n%s", k, f)
		}
		if b := l.body(k); b.Locals[2].Kind != mir.LocalTemp {
			t.Fatalf("%s: _2 is not a temporary", k)
		}
	}
	wantIn(t, l.print("H"), "_2 = copy _1", "Discriminant(_2)", "copy (_2 as some).0")
}

// TestR21_OnerrGuardOnDestructure: an onerr that consumes a linear
// local by destructuring it (not by a call) is entered through an
// OnerrGuard on that local, so the copy is skipped where the value has
// moved (the release rule).
func TestR21_OnerrGuardOnDestructure(t *testing.T) {
	l := lowerSrc(t, resSrc+`Save(): E!void {
	var f := try Open()
	onerr {
		Res(h) := f
		Assert(h == h)
	}
	f.Abandon()
	try Other()
}
`)
	b := l.body("Save")
	f := l.print("Save")
	// f is _3 (_2 is Open's destination).
	wantIn(t, f, "_3 = move _2", "call m.Res:Abandon(move _3)", "call m.Other()", "onerr_guard _3", "= move _3")
	guards := 0
	for _, blk := range b.Blocks {
		if blk.Term.Kind == mir.TermOnerrGuard {
			guards++
			if blk.Term.Value != 3 || b.Blocks[blk.Term.Yes].Origin != mir.OriginOnerr {
				t.Fatalf("the guard is not on f into the onerr copy:\n%s", f)
			}
		}
	}
	if guards != 1 {
		t.Fatalf("want one guard (Other's try edge), got %d:\n%s", guards, f)
	}
}

// TestTypeTest_OuterDeferOnExitFromBranch: an exit edge out of an
// if T is X branch runs the cleanups of scopes outside the branch on x, not
// on the narrowed x'; a cleanup registered inside the branch still uses x'.
// Before, the outer defer borrowed x' (Validate M4: &int to a &$0 place).
func TestTypeTest_OuterDeferOnExitFromBranch(t *testing.T) {
	l := lowerSrc(t, `Show[T](x: &T) {
}
F[T](var n: T, c: bool) {
	defer Show(&n)
	if T is int {
		defer Show(&n)
		if c {
			return
		}
	}
}
`)
	b := l.body("F")
	f := l.print("F")
	// n is _1, its narrowed copy _3.
	wantIn(t, f, "_3 = Narrow[int](move _1)")
	inner, outer := 0, 0
	for _, blk := range b.Blocks {
		if blk.Origin != mir.OriginDefer {
			continue
		}
		for _, st := range blk.Stmts {
			if st.Kind != mir.StAssign || st.Rvalue.Kind != mir.RvRef {
				continue
			}
			switch {
			case blk.Defer == 0 && st.Rvalue.Place.Local == 1:
				outer++
			case blk.Defer == 1 && st.Rvalue.Place.Local == 3:
				inner++
			default:
				t.Fatalf("defer %d borrows _%d:\n%s", blk.Defer, st.Rvalue.Place.Local, f)
			}
		}
	}
	// Two exits leave the branch (the return and the fall-through), each
	// with both copies.
	if inner != 2 || outer != 2 {
		t.Fatalf("want 2 inner copies on _3 and 2 outer on _1, got %d and %d:\n%s", inner, outer, f)
	}
}

// TestR21_OnerrGuardBindingTwin is the twin of
// TestR21_OnerrGuardOnDestructure that check accepts (its E0807 does not
// count a local the onerr body declares): onerr { g := f; g.Abandon() }
// consumes f by a binding, so the copy on Other's try edge
// is entered through an OnerrGuard on f; g, the body's own local, is never
// guarded.
func TestR21_OnerrGuardBindingTwin(t *testing.T) {
	l := lowerSrc(t, resSrc+`Save(): E!void {
	var f := try Open()
	onerr {
		g := f
		g.Abandon()
	}
	f.Abandon()
	try Other()
}
`)
	b := l.body("Save")
	f := l.print("Save")
	// f is _3 (_2 is Open's destination).
	wantIn(t, f, "_3 = move _2", "call m.Res:Abandon(move _3)", "call m.Other()", "onerr_guard _3")
	guards := 0
	for _, blk := range b.Blocks {
		if blk.Term.Kind == mir.TermOnerrGuard {
			guards++
			if blk.Term.Value != 3 || b.Blocks[blk.Term.Yes].Origin != mir.OriginOnerr {
				t.Fatalf("the guard is not on f into the onerr copy:\n%s", f)
			}
		}
	}
	if guards != 1 {
		t.Fatalf("want one guard (Other's try edge), got %d:\n%s", guards, f)
	}
}
