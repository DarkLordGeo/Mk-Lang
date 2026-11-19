// Why: a fn type may name result sources per field, and two fn types must
// compare by those clauses when one value is converted to the other.
// What: TestPerFieldCovers, TestFieldsSortedOrICE and
// TestFieldSourcesOf.
// How: a per-field clause is part of a fn type's identity, sorted by field, and
// FlowsCovered compares it field by field (E0608 when it does not cover).

package types

import "testing"

// TestPerFieldCovers pins the four cases of the per-field relation: a
// per-field value under a whole target, a whole value under a per-field
// target, two per-field clauses field by field, and a field a clause leaves
// out reading as the union, on either side.
func TestPerFieldCovers(t *testing.T) {
	w := newWorld()
	tab := w.tab
	ri := tab.Ref(I32)
	h := func(ps ...ParamIndex) []Source {
		var out []Source
		for _, p := range ps {
			out = append(out, Source{Param: p})
		}
		return out
	}
	fn := func(union []Source, fields ...FieldSources) TypeID {
		return tab.Fn(FnType{Params: []FnParam{{Type: ri}, {Type: ri}}, Result: ri, Sources: union, Fields: fields})
	}
	whole12 := fn(h(1, 2))
	whole1 := fn(h(1))
	rA := FieldSources{Field: 0, Sources: h(1)}
	sB := FieldSources{Field: 1, Sources: h(2)}
	rB := FieldSources{Field: 0, Sources: h(2)}
	sA := FieldSources{Field: 1, Sources: h(1)}
	split := fn(h(1, 2), rA, sB)   // from (r: a, s: b)
	swapped := fn(h(1, 2), rB, sA) // from (r: b, s: a)
	onlyR := fn(h(1), rA)          // from (r: a): s holds a too
	cases := []struct {
		name          string
		value, target TypeID
		want          bool
	}{
		{"per-field value, whole target covering every field", split, whole12, true},
		{"per-field value, whole target missing a source", split, whole1, false},
		{"whole value, per-field target: every named field must cover it (W4)", whole12, split, false},
		{"whole value of one source, per-field target naming it once", whole1, split, false},
		{"whole value covered by every named field", whole1, onlyR, true},
		{"per-field clauses, field by field", split, split, true},
		{"per-field clauses, fields crossed", swapped, split, false},
		{"an unnamed field reads as the union on the value side", onlyR, split, false},
		{"an unnamed field reads as the union on the target side", split, onlyR, false},
	}
	for _, c := range cases {
		if got := tab.FnCovers(c.value, c.target); got != c.want {
			t.Errorf("%s: FnCovers = %v, want %v", c.name, got, c.want)
		}
	}
	// The clause is identity: a flat clause of the same union is another type.
	if split == whole12 || split == swapped {
		t.Error("a per-field clause is not part of the fn type's key")
	}
	// The same clause interns once.
	if fn(h(1, 2), rA, sB) != split {
		t.Error("the same per-field clause interned twice")
	}
}

// TestFieldsSortedOrICE: Fields must arrive sorted by field, one entry
// each, so two spellings in another order are one type (check sorts them).
func TestFieldsSortedOrICE(t *testing.T) {
	w := newWorld()
	tab := w.tab
	ri := tab.Ref(I32)
	defer func() {
		if recover() == nil {
			t.Error("an unsorted per-field clause interned without an ICE")
		}
	}()
	tab.Fn(FnType{Params: []FnParam{{Type: ri}}, Result: ri, Sources: []Source{{Param: 1}},
		Fields: []FieldSources{{Field: 1, Sources: []Source{{Param: 1}}}, {Field: 0, Sources: []Source{{Param: 1}}}}})
}

// TestFieldSourcesOf: a named field gives its own sources, any other
// field the union.
func TestFieldSourcesOf(t *testing.T) {
	fl := Flows{Sources: []Source{{Param: 1}, {Param: 2}}, Fields: []FieldSources{{Field: 1, Sources: []Source{{Param: 2}}}}}
	if got := FieldSourcesOf(fl, 1); len(got) != 1 || got[0].Param != 2 {
		t.Errorf("named field: %v", got)
	}
	if got := FieldSourcesOf(fl, 0); len(got) != 2 {
		t.Errorf("unnamed field: %v, want the union", got)
	}
}
