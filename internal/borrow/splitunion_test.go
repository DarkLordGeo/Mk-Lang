// Why: a broken join of split entries must be caught where it happens, not
// only at a later put of the same root.
// What: TestSplitUnionJoinChecked, for locals.go's split-union check in
// joinInto.
// How: joins a sound and a hand-broken split entry and expects the
// internal error only for the broken one.

package borrow

import (
	"strings"
	"testing"

	"mk/internal/source"
)

// TestSplitUnionJoinChecked: root 0 is split in both predecessors. When
// the incoming entry's fields hold all of its held set, the join is
// checked and passes. When they do not (built by hand here, as a joinFV
// or joinEntry that drops a field's loans would leave it), the join is an
// internal error, with the check switched on as every test of this package
// has it.
func TestSplitUnionJoinChecked(t *testing.T) {
	ss := newSetStore()
	one, two := ss.of(1), ss.of(2)
	old := newState(1)
	old.set(0, entry{held: one, fv: ss.internFV([]setID{one, emptySet})})
	for _, broken := range []bool{false, true} {
		fv := ss.internFV([]setID{emptySet, two}) // held {2} in field 1
		if broken {
			fv = ss.internFV([]setID{emptySet, emptySet}) // held {2} in no field
		}
		in := newState(1)
		in.set(0, entry{held: two, fv: fv})
		n0 := SplitUnionChecks()
		var out *state
		ie := source.Guard("join", source.Span{}, func() { out, _ = joinInto(ss, old.clone(), in) })
		if SplitUnionChecks() == n0 {
			t.Errorf("broken %v: the join made no split-union check", broken)
		}
		switch {
		case broken && (ie == nil || !strings.Contains(ie.What, "the union of its fields")):
			t.Errorf("broken join: got %v, want the split-union internal error", ie)
		case !broken && ie != nil:
			t.Errorf("sound join: %v", ie)
		case !broken:
			e := out.get(0)
			if e.held != ss.unite(one, two) || e.fv != ss.internFV([]setID{one, two}) {
				t.Errorf("sound join: held %v, fields %v; want {1, 2} and [{1}, {2}]", ss.get(e.held), e.fv)
			}
		}
	}
}
