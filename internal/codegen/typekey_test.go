// Why: std.dyn.erased.Key[T]() identifies a type at run time, so keys must be
// distinct, stable and equal to what internal/eval gives.
// What: TestTypeKeyDistinctAndStable and TestTypeKeyIntrinsic (typekey.go).
// How: two tables interned in different orders must give the same keys; the
// first values are pinned, and a key collision is an ICE naming both types.

package codegen

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// typeKeyCases builds, in e, the types whose keys the tests pin: distinct
// types that a careless encoding would confuse (bare names of two modules,
// field names, mut against shared, signedness, array lengths).
func typeKeyCases(e *testEnv) []types.TypeID {
	a := e.structIn("geo", "Point", types.I32, types.I32)
	b := e.structIn("other", "Point", types.I32, types.I32)
	rec := func(names ...string) types.TypeID {
		var fs []types.RecordField
		for _, n := range names {
			fs = append(fs, types.RecordField{Name: e.in.Intern(n), Type: types.I64})
		}
		return e.tt.Record(fs)
	}
	return []types.TypeID{
		types.I32, types.U32, types.I64, types.F64, types.Bool, types.Str,
		a, b, e.tt.Ref(a), e.tt.Mut(a), e.tt.Slice(a), e.tt.MutSlice(a), e.tt.RawPtr(a), e.tt.Optional(a),
		e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 4)}, types.U8),
		e.tt.Array(types.ConstArg{Kind: types.CAValue, Value: e.intConst(types.Int, 5)}, types.U8),
		rec("x"), rec("y"), rec("x", "y"),
	}
}

// TestTypeKeyDistinctAndStable: distinct types get distinct keys, the key
// does not depend on the order types were interned (a second table built in
// another order gives the same values), and the first values are pinned, so
// a change of encoding is seen (internal/eval must give the same values).
func TestTypeKeyDistinctAndStable(t *testing.T) {
	e1 := newTestEnv()
	ts1 := typeKeyCases(e1)
	e2 := newTestEnv()
	e2.tt.Slice(types.Str) // shift every later TypeID of e2
	e2.structIn("pad", "Pad", types.U8)
	ts2 := typeKeyCases(e2)
	seen := map[uint64]string{}
	for i, ty := range ts1 {
		k := typeKey(e1.tt, ty)
		name := e1.tt.TypeString(ty, nil)
		if o, ok := seen[k]; ok {
			t.Errorf("%s and %s share the key %#x", o, name, k)
		}
		seen[k] = fmt.Sprintf("%s (case %d)", name, i)
		if k2 := typeKey(e2.tt, ts2[i]); k2 != k {
			t.Errorf("%s: key %#x in one table, %#x in another", name, k, k2)
		}
	}
	// Pinned values (encoding mk.typekey.v1).
	for _, c := range []struct {
		ty   types.TypeID
		want uint64
	}{{types.I32, 0x4d1ab0b45079db60}, {types.Str, 0x2fbdb948dbbba685}, {ts1[6], 0xcdb4c64cadb22ddd}} {
		if got := typeKey(e1.tt, c.ty); got != c.want {
			t.Errorf("typeKey(%s) = %#x, want %#x", e1.tt.TypeString(c.ty, nil), got, c.want)
		}
	}
}

// TestTypeKeyIntrinsic emits Key[T]() for two T and checks that each call
// is its type's key as an i64 constant, and that a key collision in the
// program is an ICE naming both types.
func TestTypeKeyIntrinsic(t *testing.T) {
	e := newTestEnv()
	key := e.stdFn("std.dyn.erased", "", "Key", 1, nil, types.U64)
	pt := e.structIn("geo", "Point", types.I32, types.I32)
	f := e.body(e.fn("Keys", nil, types.U64, 0))
	k1 := f.call(key, []types.GenericArg{types.TypeArg(pt)}, types.U64)
	k2 := f.call(key, []types.GenericArg{types.TypeArg(types.Str)}, types.U64)
	f.assign(place(0), use(mir.Copy(place(k1))), f.at(21, 1))
	_ = k2
	f.ret(f.at(21, 1))
	u := unitOf(e, []*mir.Body{f.b})
	u.Funcs = append(u.Funcs, Func{Symbol: "-", Key: e.items.Get(key).Key.String(), Item: key,
		Args: []types.GenericArg{types.TypeArg(pt)}, Pack: -1})
	ll := emit(t, e, u, types.X86_64Windows, types.Release)
	for _, ty := range []types.TypeID{pt, types.Str} {
		want := fmt.Sprintf("i64 %d", int64(typeKey(e.tt, ty)))
		if !strings.Contains(ll, want) {
			t.Errorf("no %q (the key of %s) in the IR", want, e.tt.TypeString(ty, nil))
		}
	}
	// A collision, planted: two types under one key are named, and every
	// later Key call is an ICE that says so.
	clash := keyClashes(e.tt, map[types.TypeID]uint64{pt: 1, types.Str: 1, types.I32: 2})
	if clash != "Point and str share the type key 0000000000000001" {
		t.Errorf("keyClashes = %q", clash)
	}
	k := &typeKeys{ice: clash}
	ie := source.Guard("key", source.Span{}, func() { k.key(e.tt, pt) })
	if ie == nil || !strings.Contains(ie.String(), clash) {
		t.Errorf("a collision gave %v, want an ICE naming both types", ie)
	}
}
