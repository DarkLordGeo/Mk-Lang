// Why: field-level loan sets let a call's result say which argument each
// field comes from; the bookkeeping must be exact or a caller loses fields
// it may use, or keeps loans it should not.
// What: tests of fields.go: the field key after payload pairs, the field-wise
// vectors and their join, the callee's check at `_0` field by field and
// whole, and step 5 of the call rule per field, read after steps 3 and 4.
// How: a Pair result type (fldPair) and per-field signatures (fldPerField)
// on hand-built MIR, checked for E0602 and E0505.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// fldPair declares struct Pair { r: &i32, s: &i32 } and returns its type.
func fldPair(k *kit) types.TypeID {
	ri := refI32(k)
	k.structItem(k.main, "Pair", 0, false, false, field("r", ri), field("s", ri))
	return k.named("Pair")
}

// fldPerField declares name(a: &i32, b: &i32): result from (r: ra, s: sb)
// for the given source parameters (1 is a, 2 is b) of fields 0 and 1.
func fldPerField(k *kit, name string, result types.TypeID, r, s types.ParamIndex) *types.Signature {
	ri := refI32(k)
	sig := k.fn(name, []types.Param{k.param("a", ri), k.param("b", ri)}, result, types.EmptyErrors)
	sig.Flows = types.Flows{Declared: true, Sources: []types.Source{{Param: 1}, {Param: 2}},
		Fields: []types.FieldSources{{Field: 0, Sources: []types.Source{{Param: r}}}, {Field: 1, Sources: []types.Source{{Param: s}}}}}
	return sig
}

// TestFieldKey: payload pairs (a downcast to some or ok, then field 0)
// are skipped and the next field names the field; any other step
// there is a whole read.
func TestFieldKey(t *testing.T) {
	sf := func(a uint32) step { return step{kind: stField, a: a} }
	sv := func(sel mir.VariantSel) step { return step{kind: stVariant, sel: sel} }
	cases := []struct {
		name  string
		steps []step
		k     keyKind
		f     uint32
	}{
		{"the root", nil, keyWhole, 0},
		{"a field", []step{sf(1)}, keyField, 1},
		{"a field then more", []step{sf(1), {kind: stDeref}}, keyField, 1},
		{"some payload", []step{sv(mir.SelSome), sf(0)}, keyPayload, 0},
		{"shape 14: some payload, field 1", []step{sv(mir.SelSome), sf(0), sf(1)}, keyField, 1},
		{"ok then some payload, field 0", []step{sv(mir.SelOk), sf(0), sv(mir.SelSome), sf(0), sf(0)}, keyField, 0},
		{"the error side", []step{sv(mir.SelErr), sf(0)}, keyWhole, 0},
		{"a user enum's variant", []step{sv(mir.SelEnum), sf(0)}, keyWhole, 0},
		{"a deref", []step{{kind: stDeref}, sf(0)}, keyWhole, 0},
		{"a fields-loop step", []step{{kind: stField, any: true}}, keyWhole, 0},
	}
	for _, c := range cases {
		k, f, _ := fieldKey(&pathInfo{steps: c.steps})
		if k != c.k || f != c.f {
			t.Errorf("%s: key %d field %d, want %d %d", c.name, k, f, c.k, c.f)
		}
	}
}

// TestFieldVectors: the join is field-wise between two split
// vectors of one shape and whole otherwise; a removal narrows every field.
func TestFieldVectors(t *testing.T) {
	s := newSetStore()
	a, b := s.of(1), s.of(2)
	v1 := s.internFV([]setID{a, emptySet})
	v2 := s.internFV([]setID{emptySet, b})
	if s.internFV([]setID{a, emptySet}) != v1 {
		t.Fatal("a vector interned twice")
	}
	j := s.fieldSets(s.joinFV(v1, v2))
	if len(j) != 2 || j[0] != a || j[1] != b {
		t.Errorf("join = %v, want [a b]", j)
	}
	if s.joinFV(v1, 0) != 0 || s.joinFV(0, v2) != 0 {
		t.Error("whole does not win a join")
	}
	if s.joinFV(v1, s.internFV([]setID{a})) != 0 {
		t.Error("vectors of two lengths joined field-wise")
	}
	m := s.fieldSets(s.meetFV(s.joinFV(v1, v2), b))
	if m[0] != emptySet || m[1] != b {
		t.Errorf("meet = %v, want [{} b]", m)
	}
}

// fldCallee builds F's body `_t = Pair(r: x0, s: x1); _0 = move _t`, with
// x0 and x1 the parameters given, and returns the `_0` statement's span.
func fldCallee(k *kit, sig *types.Signature, pair types.TypeID, x0, x1 mir.LocalID) (*mb, mir.SpanID) {
	m := k.body(sig)
	tmp := m.temp(pair)
	m.set(tmp, m.agg(mir.AggStruct, k.item("Pair"), pair, cp(m.local(x0)), cp(m.local(x1))))
	at := m.set(0, use(mv(m.local(tmp))))
	m.end(tmp)
	m.ret(mir.ExitNormal)
	return m, at
}

// TestCalleeFieldByField: a struct literal result, checked field by
// field (E0602 when the fields are crossed, clean when they match); a field
// the clause leaves out holds every source of the clause, and no more.
func TestCalleeFieldByField(t *testing.T) {
	k := newKit(t)
	pair := fldPair(k)
	m, _ := fldCallee(k, fldPerField(k, "F", pair, 1, 2), pair, 1, 2)
	m.expect(t, m.check(t))

	k = newKit(t)
	pair = fldPair(k)
	m, at := fldCallee(k, fldPerField(k, "F", pair, 1, 2), pair, 2, 1)
	m.expect(t, m.check(t), diag.E0602, at)

	k = newKit(t)
	pair = fldPair(k)
	ri := refI32(k)
	sig := k.fn("F", []types.Param{k.param("a", ri), k.param("b", ri)}, pair, types.EmptyErrors)
	sig.Flows = types.Flows{Declared: true, Sources: []types.Source{{Param: 1}},
		Fields: []types.FieldSources{{Field: 0, Sources: []types.Source{{Param: 1}}}}}
	m, at = fldCallee(k, sig, pair, 1, 2)
	m.expect(t, m.check(t), diag.E0602, at) // s holds a only
}

// TestCalleeWholeOperand: a whole value at `_0` (here the result of
// a flat clause) must be covered by every named field's sources.
func TestCalleeWholeOperand(t *testing.T) {
	k := newKit(t)
	pair := fldPair(k)
	ri := refI32(k)
	flat := k.fn("Flat", []types.Param{k.param("a", ri), k.param("b", ri)}, pair, types.EmptyErrors)
	flat.Flows = types.Flows{Declared: true, Sources: []types.Source{{Param: 1}, {Param: 2}}}
	m := k.body(fldPerField(k, "F", pair, 1, 2))
	tmp := m.temp(pair)
	next := m.block()
	m.call(flat.Item, nil, []mir.Arg{argCopy(m.local(1)), argCopy(m.local(2))}, m.local(tmp), next)
	m.at(next)
	at := m.set(0, use(mv(m.local(tmp))))
	m.end(tmp)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0602, at)
}

// TestCallerSplitsAtDestructuring: step 5 gives each field its own
// sources' sets, so a take of field r holds only x's loan, of s only y's,
// also through a `?Pair` payload (`(_t as some).0.1`).
func TestCallerSplitsAtDestructuring(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opt     bool
		field   uint32
		dangles bool
	}{
		{"r from a", false, 0, false},
		{"s from b", false, 1, true},
		{"?Pair: r from a", true, 0, false},
		{"?Pair: s from b", true, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newKit(t)
			pair := fldPair(k)
			ri := refI32(k)
			res := pair
			if tc.opt {
				res = k.tab.Optional(pair)
			}
			f := fldPerField(k, "F", res, 1, 2)
			m := k.body(k.sig("Caller", nil, types.Void, types.EmptyErrors))
			x := m.user("x", types.I32, false)
			u := m.user("u", ri, false)
			m.set(x, use(imm(types.I32, 1)))
			y := m.user("y", types.I32, false)
			m.set(y, use(imm(types.I32, 2)))
			tx, ty := m.temp(ri), m.temp(ri)
			m.set(tx, ref(m.local(x)))
			bad := m.set(ty, ref(m.local(y)))
			tmp := m.temp(res)
			next := m.block()
			m.call(f.Item, nil, []mir.Arg{argCopy(m.local(tx)), argCopy(m.local(ty))}, m.local(tmp), next)
			m.at(next)
			var pl mir.Place
			if tc.opt {
				pl = m.pl(tmp, down(mir.SelSome, 0, res), fld(0, pair), fld(tc.field, ri))
			} else {
				pl = m.pl(tmp, fld(tc.field, ri))
			}
			m.assign(m.local(u), use(cp(pl)))
			m.end(tmp, ty, tx, y)
			m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(u)))
			m.end(u, x)
			m.ret(mir.ExitNormal)
			if tc.dangles {
				m.expect(t, m.check(t), diag.E0505, bad)
			} else {
				m.expect(t, m.check(t))
			}
		})
	}
}

// TestBoundWholeSplit: a named local bound whole from the call splits by
// field: p.r holds only x's loan, so y may end while u is live (a whole local
// would keep y's loan: E0505 at y's borrow).
func TestBoundWholeSplit(t *testing.T) {
	k := newKit(t)
	pair := fldPair(k)
	ri := refI32(k)
	f := fldPerField(k, "F", pair, 1, 2)
	m := k.body(k.sig("Caller", nil, types.Void, types.EmptyErrors))
	x := m.user("x", types.I32, false)
	u := m.user("u", ri, false)
	m.set(x, use(imm(types.I32, 1)))
	y := m.user("y", types.I32, false)
	m.set(y, use(imm(types.I32, 2)))
	tx, ty := m.temp(ri), m.temp(ri)
	m.set(tx, ref(m.local(x)))
	m.set(ty, ref(m.local(y)))
	tmp := m.temp(pair)
	p := m.user("p", pair, false)
	next := m.block()
	m.call(f.Item, nil, []mir.Arg{argCopy(m.local(tx)), argCopy(m.local(ty))}, m.local(tmp), next)
	m.at(next)
	m.set(p, use(cp(m.local(tmp))))
	m.assign(m.local(u), use(cp(m.pl(p, fld(0, ri)))))
	m.end(tmp, ty, tx, p, y)
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(u)))
	m.end(u, x)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// TestStepFiveAfterStores: K(c: &i32 into a, a: &i32, b: &i32):
// Pair from (r: a, s: b); field r reads argH[a] after step 3 stored c's
// loan into it, so r holds y's loan (E0505), never a's entry set.
func TestStepFiveAfterStores(t *testing.T) {
	k := newKit(t)
	pair := fldPair(k)
	ri := refI32(k)
	kk := k.fn("K", []types.Param{{Name: k.in.Intern("c"), Type: ri, Into: 2}, k.param("a", ri), k.param("b", ri)}, pair, types.EmptyErrors)
	kk.Flows = types.Flows{Declared: true, Sources: []types.Source{{Param: 2}, {Param: 3}},
		Fields: []types.FieldSources{{Field: 0, Sources: []types.Source{{Param: 2}}}, {Field: 1, Sources: []types.Source{{Param: 3}}}}}
	m := k.body(k.sig("Caller", nil, types.Void, types.EmptyErrors))
	x := m.user("x", types.I32, false)
	u := m.user("u", ri, false)
	m.set(x, use(imm(types.I32, 1)))
	y := m.user("y", types.I32, false)
	m.set(y, use(imm(types.I32, 2)))
	ty, tx1, tx2 := m.temp(ri), m.temp(ri), m.temp(ri)
	bad := m.set(ty, ref(m.local(y)))
	m.set(tx1, ref(m.local(x)))
	m.set(tx2, ref(m.local(x)))
	tmp := m.temp(pair)
	next := m.block()
	m.call(kk.Item, nil, []mir.Arg{argCopy(m.local(ty)), argCopy(m.local(tx1)), argCopy(m.local(tx2))}, m.local(tmp), next)
	m.at(next)
	m.assign(m.local(u), use(cp(m.pl(tmp, fld(0, ri)))))
	m.end(tmp, tx2, tx1, ty, y)
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(u)))
	m.end(u, x)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0505, bad)
}

// TestStoreMakesWhole: a store into a split root lands in no known
// field and may repeat a loan another field holds, so the root becomes
// whole (fields.go, put and addHeld); a removal narrows every field.
func TestStoreMakesWhole(t *testing.T) {
	k := newKit(t)
	pair := fldPair(k)
	m := k.body(fldPerField(k, "F", pair, 1, 2))
	tmp := m.temp(pair)
	m.ret(mir.ExitNormal)
	c := newChecker(m.b, k.env(m.sig))
	c.setup()
	st := newState(len(c.roots))
	r := rootID(tmp)
	if c.roots[r].nfields != 2 {
		t.Fatalf("a Pair temporary keeps %d field sets, want 2", c.roots[r].nfields)
	}
	if c.roots[0].nfields != 2 {
		t.Fatalf("local 0 keeps %d field sets, want 2", c.roots[0].nfields)
	}
	la, lb := c.sets.of(1), c.sets.of(2)
	c.setSplit(st, r, c.sets.unite(la, lb), c.sets.internFV([]setID{la, lb}))
	if fs := c.sets.fieldSets(st.get(r).fv); len(fs) != 2 || fs[0] != la || fs[1] != lb {
		t.Fatalf("split write: %v", fs)
	}
	c.setHeld(st, r, lb) // a removal of loan 1
	if fs := c.sets.fieldSets(st.get(r).fv); len(fs) != 2 || fs[0] != emptySet || fs[1] != lb {
		t.Fatalf("after a removal: %v, want [{} {2}]", fs)
	}
	c.addHeld(st, r, lb) // a store of a loan field s already holds
	if e := st.get(r); e.fv != 0 || e.held != lb {
		t.Fatalf("after a store: fv %d held %v, want whole {2}", e.fv, c.sets.get(e.held))
	}
}
