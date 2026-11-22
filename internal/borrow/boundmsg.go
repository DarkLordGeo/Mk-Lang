// Why: the walk over a parameter's referents stops at a bound
// (maxReferentsPerItem instances of one constructor, maxReferents in all)
// and takes the parameter to reach anything past it. What a referent past
// the stop takes stays with the node (cutTakes), so the bound changes no
// verdict, but a static store judged only through such a referent must say
// where the walk stopped, or the reader cannot tell which type takes it.
//
// What: boundAlt and boundText.
//
// How: abstractNodes records the first bound its layer-2 walk stopped at
// (cutReason); a node keeps it only when its takes comes from the stopped
// referent alone, and the static store's E0604 there names the bound as a
// fact.

package borrow

import (
	"strconv"

	"mk/internal/diag"
)

// boundAlt completes the static store's E0604 d at abstract node t when
// t takes stores only through a referent its walk stopped at: the fact
// `bound` names the bound.
func (c *checker) boundAlt(d *diag.Diagnostic, t rootID) {
	ri := &c.roots[t]
	if ri.kind != rkAbstract || !ri.cut.stopped() {
		return
	}
	d.Text("bound", c.boundText(ri.cut))
}

// boundText spells a bound: "more than 64 instances of `B`", "more than 64
// records holding instances of `B`" or "4096 referents".
func (c *checker) boundText(r cutReason) string {
	if r.all {
		return strconv.Itoa(maxReferents) + " referents"
	}
	name := "`" + c.symbol(c.tab.Items().Get(r.item).Name) + "`"
	if r.record {
		return "more than " + strconv.Itoa(maxReferentsPerItem) + " records holding instances of " + name
	}
	return "more than " + strconv.Itoa(maxReferentsPerItem) + " instances of " + name
}
