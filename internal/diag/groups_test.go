// Why: some codes are reported in more than one phase group, and the report's
// earliest-phase rule depends on which groups a code belongs to.
// What: tests of GroupForms, Groups, MultiGroup and DefaultPhase.
// How: checks the table's order and rows, pins the multi-group codes and
// their groups, and checks that every other code has only its DefaultPhase.

package diag

import (
	"reflect"
	"testing"
)

// TestGroupFormsTable: the table is ordered by code then phase, every
// row names a known code, a user group, a form and an emitter, no (code,
// phase) repeats, every code has at least two groups, and DefaultPhase is
// always one of them.
func TestGroupFormsTable(t *testing.T) {
	rows := GroupForms()
	if len(rows) == 0 {
		t.Fatal("GroupForms is empty")
	}
	for i, r := range rows {
		if !r.Code.IsKnown() {
			t.Errorf("row %d: unknown code %v", i, r.Code)
		}
		if r.Phase <= PhaseInternal || r.Phase >= PhaseNative {
			t.Errorf("row %d (%v): phase %v is not a user group", i, r.Code, r.Phase)
		}
		if r.Form == "" || r.Emitter == "" {
			t.Errorf("row %d (%v %v): empty form or emitter", i, r.Code, r.Phase)
		}
		if i > 0 {
			p := rows[i-1]
			if p.Code > r.Code || p.Code == r.Code && p.Phase >= r.Phase {
				t.Errorf("rows %d and %d out of order or repeated: %v %v, %v %v", i-1, i, p.Code, p.Phase, r.Code, r.Phase)
			}
		}
	}
	for _, c := range multiGroupCodes(rows) {
		gs := Groups(c)
		if len(gs) < 2 {
			t.Errorf("%v has %d group, want at least 2", c, len(gs))
		}
		found := false
		for _, g := range gs {
			found = found || g == DefaultPhase(c)
		}
		if !found {
			t.Errorf("%v: DefaultPhase %v is not among its groups %v", c, DefaultPhase(c), gs)
		}
	}
}

// TestMultiGroupCodes pins which codes the table places in more than
// one group, and their groups.
func TestMultiGroupCodes(t *testing.T) {
	want := map[Code][]Phase{
		E0101: {PhaseParse, PhaseNames, PhaseTypes},
		E0204: {PhaseNames, PhaseTypes},
		E0206: {PhaseImports, PhaseNames},
		E0207: {PhaseNames, PhaseTypes},
		E0406: {PhaseTypes, PhaseFlow},
		E0507: {PhaseTypes, PhaseFlow},
		E0609: {PhaseParse, PhaseTypes, PhaseFlow},
		E0905: {PhaseParse, PhaseImports, PhaseNames, PhaseTypes, PhaseFlow},
		E1002: {PhaseTypes, PhaseFlow},
		E1102: {PhaseImports, PhaseFlow},
		E1204: {PhaseParse, PhaseTypes},
		E1221: {PhaseParse, PhaseNames, PhaseTypes},
	}
	got := multiGroupCodes(GroupForms())
	if len(got) != len(want) {
		t.Errorf("multi-group codes = %v, want %d codes", got, len(want))
	}
	for c, phases := range want {
		if !MultiGroup(c) {
			t.Errorf("MultiGroup(%v) = false", c)
		}
		if g := Groups(c); !reflect.DeepEqual(g, phases) {
			t.Errorf("Groups(%v) = %v, want %v", c, g, phases)
		}
	}
}

// TestSingleGroupIsDefault: a code outside the table has exactly its
// DefaultPhase, for every catalogue code.
func TestSingleGroupIsDefault(t *testing.T) {
	for _, e := range Catalogue() {
		if MultiGroup(e.Code) {
			continue
		}
		if g := Groups(e.Code); len(g) != 1 || g[0] != DefaultPhase(e.Code) {
			t.Errorf("Groups(%v) = %v, want [%v]", e.Code, g, DefaultPhase(e.Code))
		}
	}
}

// TestGroupFormsIsACopy: changing the returned slice does not change
// the table.
func TestGroupFormsIsACopy(t *testing.T) {
	a := GroupForms()
	a[0].Phase = PhaseFlow
	if b := GroupForms(); b[0].Phase == PhaseFlow {
		t.Error("GroupForms returned the table itself")
	}
}

func multiGroupCodes(rows []GroupForm) []Code {
	var out []Code
	for i, r := range rows {
		if i == 0 || rows[i-1].Code != r.Code {
			out = append(out, r.Code)
		}
	}
	return out
}
