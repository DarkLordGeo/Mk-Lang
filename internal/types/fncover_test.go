// Why: a fn value converts to a fn type only when the type's declared flows
// cover the value's; check (E0608) and the MIR validator share this relation.
// What: TestFnCovers, over FnCovers and SameFnShape.
// How: table cases of value and target fn types with their expected answer.

package types

import "testing"

// TestFnCovers pins the cover rule: a fn value fits a fn type when each of
// its flows is covered by a declared flow; H(p) covers B(p); so a value with
// a flow does not fit a fn type that declares none. Stores, into and ends
// cover only themselves; a parameter's into is a flow, not shape, so
// a value with no into fits a fn type that declares one. Parameter types,
// result and error set must match exactly.
func TestFnCovers(t *testing.T) {
	w := newWorld()
	tab := w.tab
	ri := tab.Ref(I32)
	fn := func(src []Source, stores []Store, ends []ParamIndex, into ...ParamIndex) TypeID {
		ps := []FnParam{{Type: ri}, {Type: ri}}
		for i, p := range into {
			ps[i].Into = p
		}
		return tab.Fn(FnType{Params: ps, Result: ri, Sources: src, Stores: stores, Ends: ends})
	}
	none := fn(nil, nil, nil)
	h1 := fn([]Source{{Param: 1}}, nil, nil)
	b1 := fn([]Source{{Param: 1, Behind: true}}, nil, nil)
	h2 := fn([]Source{{Param: 2}}, nil, nil)
	h12 := fn([]Source{{Param: 1}, {Param: 2}}, nil, nil)
	st := fn(nil, []Store{{From: 1, Into: 2}}, nil)
	st2 := fn(nil, []Store{{From: 2, Into: 1}}, nil)
	end1 := fn(nil, nil, []ParamIndex{1})
	into := fn(nil, nil, nil, 2)
	other := tab.Fn(FnType{Params: []FnParam{{Type: ri}, {Type: ri}}, Result: Str})
	pathed := fn([]Source{{Param: 1, Behind: true, Path: []FieldStep{{Field: 0, Type: I32}}}}, nil, nil)
	cases := []struct {
		name          string
		value, target TypeID
		want          bool
	}{
		{"equal", h1, h1, true},
		{"no flow fits a declared flow (X.FnCover)", none, h1, true},
		{"H(p) covers B(p)", b1, h1, true},
		{"B(p) does not cover H(p)", h1, b1, false},
		{"a flow does not fit a fn type that declares none (Id)", h1, none, false},
		{"another parameter does not cover", h1, h2, false},
		{"a subset of declared sources", h2, h12, true},
		{"more sources than declared", h12, h1, false},
		{"a store covers itself", st, st, true},
		{"a store is not covered by none", st, none, false},
		{"a store is not covered by another", st, st2, false},
		{"no store fits a declared store", none, st, true},
		{"ends covers itself", end1, end1, true},
		{"ends is not covered by none", end1, none, false},
		{"into is a flow: no into fits a declared into (H21 accept)", none, into, true},
		{"into is a flow: into does not fit none (H21 reject)", into, none, false},
		{"into is a flow: into covers itself", into, into, true},
		{"into on another parameter is not a cover", fn(nil, nil, nil, NoParam, 1), into, false},
		{"another result is not a cover", none, other, false},
		{"paths are not compared", pathed, h1, true},
		{"not fn types", ri, ri, true},
		{"different non-fn types", ri, Str, false},
	}
	for _, c := range cases {
		if got := tab.FnCovers(c.value, c.target); got != c.want {
			t.Errorf("%s: FnCovers(%s, %s) = %v, want %v", c.name, tab.TypeString(c.value, nil), tab.TypeString(c.target, nil), got, c.want)
		}
	}
	// SameFnShape compares parameter types, result and error set, never a
	// parameter's Into.
	if !SameFnShape(tab.Get(none).Fn, tab.Get(into).Fn) {
		t.Error("SameFnShape compares Into")
	}
	if SameFnShape(tab.Get(none).Fn, tab.Get(other).Fn) {
		t.Error("SameFnShape ignores the result")
	}
	if SameFnShape(tab.Get(none).Fn, &FnType{Params: []FnParam{{Type: ri}, {Type: Str}}, Result: ri}) {
		t.Error("SameFnShape ignores a parameter type")
	}
	// FlowsCovered on its own: a parameter's into covers only itself, and
	// a value into past the target's parameters is not covered.
	if FlowsCovered(Flows{}, Flows{}, []ParamIndex{2}, []ParamIndex{NoParam}) {
		t.Error("x into p covered by no into")
	}
	if !FlowsCovered(Flows{}, Flows{}, []ParamIndex{2}, []ParamIndex{2}) {
		t.Error("x into p not covered by itself")
	}
	if FlowsCovered(Flows{}, Flows{}, []ParamIndex{NoParam, 1}, []ParamIndex{NoParam}) {
		t.Error("into past the target's parameters covered")
	}
	// Only the top level is relaxed: ?fn of fewer flows is another type.
	if tab.FnCovers(tab.Optional(none), tab.Optional(h1)) {
		t.Error("?fn compared modulo flows")
	}
}
