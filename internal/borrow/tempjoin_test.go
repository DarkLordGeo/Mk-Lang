// Why: a temporary's initialization state at a join must not raise a false
// E0402.
// What: a regression test of the {U, M} join of compiler temporaries.
// How: builds the two-edge shape on hand-built MIR and checks the codes.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// TestProcmanTempJoinUM: a temporary that is assigned and consumed
// on one edge and never assigned on the other joins as {U, M}: it holds no
// value on either, so the join is no E0402 (a call's destination under a
// value `else`). A user local keeps the stricter rule (TestR2_JoinUM),
// and a temporary left live on one edge ({U, I}) is still E0402.
func TestProcmanTempJoinUM(t *testing.T) {
	for _, live := range []bool{false, true} {
		k := newKit(t)
		m := k.body(k.sig("F", []types.Param{k.param("c", types.Bool)}, types.Void, types.EmptyErrors))
		x := m.temp(k.Res)
		yes, join := m.block(), m.block()
		m.branch(cp(m.local(1)), yes, join)
		m.at(yes)
		m.acquire(x)
		if !live {
			m.close(x)
		}
		m.jump(join)
		m.at(join)
		m.end(x)
		m.ret(mir.ExitNormal)
		if live {
			r := m.check(t)
			if len(r.Diags) == 0 || r.Diags[0].Code != diag.E0402 {
				t.Errorf("{U, I} on a temporary: want E0402")
			}
			continue
		}
		m.expect(t, m.check(t))
	}
}
