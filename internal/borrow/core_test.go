// Why: the loan pass rests on a few data structures whose behaviour every
// rule depends on.
// What: tests of hash-consed sets, persistent states, paths and overlap, and
// liveness.
// How: drives each structure directly and checks interning, copy-on-write,
// prefix overlap and liveness over defers and branches.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

func TestSetInternCanonical(t *testing.T) {
	s := newSetStore()
	a := s.fromUnsorted([]uint32{3, 1, 2, 3})
	b := s.intern([]uint32{1, 2, 3})
	if a != b || s.len(a) != 3 {
		t.Fatalf("equal sets must have equal IDs: %d %d", a, b)
	}
	u := s.unite(s.of(5), a)
	if !s.has(u, 5) || !s.has(u, 1) || s.len(u) != 4 {
		t.Fatalf("union %v", s.get(u))
	}
	if d := s.minus(u, a); d != s.of(5) {
		t.Fatalf("minus %v", s.get(d))
	}
	if m := s.meet(u, s.fromUnsorted([]uint32{5, 9})); m != s.of(5) {
		t.Fatalf("meet %v", s.get(m))
	}
	if s.intersects(a, s.of(9)) || !s.intersects(a, s.of(2)) {
		t.Fatalf("intersects")
	}
	if s.unite(emptySet, emptySet) != emptySet || s.minus(a, a) != emptySet {
		t.Fatalf("empty")
	}
}

func TestStateCopyOnWrite(t *testing.T) {
	s := newState(70)
	s.set(3, entry{init: initI})
	c := s.clone()
	c.set(3, entry{init: initM})
	c.set(65, entry{init: initI})
	if initOf(s.get(3)) != initI || initOf(c.get(3)) != initM {
		t.Fatalf("clone wrote its source: %v %v", s.get(3), c.get(3))
	}
	if initOf(s.get(65)) != initU {
		t.Fatalf("absent root must read {U}")
	}
	if s.equal(c) || !s.equal(s.clone()) {
		t.Fatalf("equal")
	}
	// T becomes M.
	c.set(4, entry{init: initT})
	c.takenToMoved()
	if c.get(4).init != initM {
		t.Fatalf("takenToMoved: %v", c.get(4).init)
	}
}

func TestPathPrefix(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", []types.Param{k.param("xs", k.tab.MutSlice(types.I32)), k.param("r", k.tab.Mut(k.Point))}, types.Void, types.EmptyErrors))
	i := m.temp(types.Int)
	pt := newPathTable()
	tab := k.tab
	elem := pt.ofPlace(m.b, tab, m.pl(1, index(i, types.I32)))
	if pi := pt.get(elem); len(pi.steps) != 2 || pi.steps[0].kind != stDeref || pi.steps[0].mode != dmExcl || pi.steps[1].kind != stIndex {
		t.Fatalf("mut []T index is an exclusive deref then an index: %+v", pi.steps)
	}
	whole := pt.ofPlace(m.b, tab, m.local(1))
	if !pt.overlap(whole, elem, true) || !pt.overlap(elem, whole, true) {
		t.Fatalf("a prefix overlaps")
	}
	// A shallow access to xs misses loans under *xs.
	if pt.overlap(elem, whole, false) {
		t.Fatalf("a shallow write of xs must miss a loan on xs.*[_]")
	}
	x := pt.ofPlace(m.b, tab, m.pl(2, deref(k.Point), fld(0, types.I32)))
	y := pt.ofPlace(m.b, tab, m.pl(2, deref(k.Point), fld(1, types.I32)))
	if pt.overlap(x, y, true) {
		t.Fatalf("fields are disjoint")
	}
	if pt.prefix(x, 1) != pt.ofPlace(m.b, tab, m.pl(2, deref(k.Point))) {
		t.Fatalf("prefix")
	}
}

// TestLivenessDeferAndJoin: a use in a defer copy keeps a root live across
// the body, and liveness joins over both arms of a branch.
func TestLivenessDeferAndJoin(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", []types.Param{k.param("c", types.Bool)}, types.Void, types.EmptyErrors))
	a := m.user("a", refI32(k), false)
	n := m.user("n", types.I32, false)
	m.set(n, use(imm(types.I32, 1)))
	m.set(a, ref(m.local(n)))
	yes, no := m.block(), m.block()
	m.branch(cp(m.local(1)), yes, no)
	m.at(yes)
	m.jump(no)
	m.at(no)
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(a)))
	m.end(a, n)
	m.ret(mir.ExitNormal)
	m.validate(t)
	c := newChecker(m.b, k.env(m.sig))
	c.setup()
	c.order()
	lv := c.computeLiveness()
	if !c.sets.has(lv.in[yes], uint32(a)) || !c.sets.has(lv.out[0], uint32(a)) {
		t.Fatalf("a must be live across the branch")
	}
	if c.sets.has(lv.in[0], uint32(a)) {
		t.Fatalf("a is defined in block 0, not live into it")
	}
}

// TestR15_MoveUnderDeref: moving r while a loan on *r is live is E0503, a
// deep access through places.
func TestR15_MoveUnderDeref(t *testing.T) {
	k := newKit(t)
	mi := k.tab.Mut(types.I32)
	m := k.body(k.sig("F", []types.Param{k.param("r", mi)}, types.Void, types.EmptyErrors))
	q := m.user("q", refI32(k), false)
	s := m.user("s", mi, false)
	m.set(q, ref(m.pl(1, deref(types.I32))))
	bad := m.set(s, use(mv(m.local(1))))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(q)))
	m.end(s, q)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0503, bad)
}
