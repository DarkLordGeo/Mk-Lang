// Why: some invariants are about the shape of the block graph, not single
// statements: cleanup code copied per exit edge, spawn scopes and the
// generic fields loop that mono unrolls.
//
// What: cleanupCopies (invariant M7), spawnScope and fieldsLoop (M9).
//
// How: a cleanup copy is what its entry block reaches through blocks of
// its own Origin and Defer; no two exit edges share a copy. A fields loop's
// blocks are exactly [Header, Exit), with the latch at Header+2.

package mir

import (
	"strconv"

	"mk/internal/types"
)

// cleanupCopies checks M7 over every block whose Origin is not OriginBody.
// A copy's entry is a cleanup block with exactly one predecessor that is the
// body or another copy (a different Origin or Defer). The copy is what the
// entry reaches through blocks of its own Origin and Defer. Every edge into
// a cleanup block that is not an entry comes from a block of the same copy:
// so a block inside a copy may have several predecessors (an if in the
// defer body), but no two exit edges ever share a copy. A cleanup block with
// no predecessor is dead code and passes.
func (v *validator) cleanupCopies(preds []uint32) {
	b := v.b
	n := len(b.Blocks)
	same := func(x, y BlockID) bool {
		bx, by := &b.Blocks[x], &b.Blocks[y]
		return bx.Origin == by.Origin && bx.Defer == by.Defer
	}
	var buf []BlockID
	pred1 := make([]BlockID, n) // a predecessor; the only one when preds is 1
	for x := range b.Blocks {
		buf = b.Successors(BlockID(x), buf[:0])
		for _, y := range buf {
			if int(y) < n {
				pred1[y] = BlockID(x)
			}
		}
	}
	entry := func(x BlockID) bool {
		return b.Blocks[x].Origin != OriginBody && preds[x] == 1 && !same(pred1[x], x)
	}
	owner := make([]int32, n) // entry+1 of the block's copy, 0 for none
	for e := range b.Blocks {
		if !entry(BlockID(e)) {
			continue
		}
		work := []BlockID{BlockID(e)}
		for len(work) > 0 {
			x := work[len(work)-1]
			work = work[:len(work)-1]
			if owner[x] == int32(e)+1 {
				continue
			}
			if owner[x] != 0 {
				v.at = "bb" + strconv.Itoa(int(x))
				v.fail("M7", "cleanup block is in the copies entered at bb%d and bb%d", owner[x]-1, e)
			}
			owner[x] = int32(e) + 1
			buf = b.Successors(x, buf[:0])
			for _, y := range buf {
				if int(y) < n && same(x, y) {
					work = append(work, y)
				}
			}
		}
	}
	for x := range b.Blocks {
		buf = b.Successors(BlockID(x), buf[:0])
		for _, y := range buf {
			if int(y) >= n || b.Blocks[y].Origin == OriginBody || entry(y) {
				continue
			}
			if owner[y] == 0 || !same(BlockID(x), y) || owner[x] != owner[y] {
				v.at = "bb" + strconv.Itoa(int(y))
				v.fail("M7", "edge from bb%d enters a cleanup copy that is not its own (%d predecessors)", x, preds[y])
			}
		}
	}
}

// spawnScope checks M9 for TermCall.Spawn: a LocalScope local, or a parameter
// or local of type &Scope or &Scope[E]. Without Env.Known any reference to a
// named type is accepted.
func (v *validator) spawnScope(s LocalID) {
	l := v.b.Locals[s]
	if l.Kind == LocalScope {
		return
	}
	ty := v.tab.Get(l.Type)
	if ty.Kind == types.KRef {
		el := v.tab.Get(ty.Elem)
		if el.Kind == types.KNamed && (v.env.Known == nil || el.Item == v.env.Known.Scope) {
			return
		}
	}
	v.fail("M9", "spawn into _%d of %s, which is neither a scope local nor a &Scope", s, v.ts(l.Type))
}

// fieldsLoop checks M9 for a generic fields loop: it lives in a generic
// body, its hidden parameter is one of the body's generics, and its blocks
// are exactly [Header, Exit).
func (v *validator) fieldsLoop(l LoopID, lp LoopInfo) {
	b := v.b
	if b.GenericCount == 0 {
		v.fail("M2", "generic fields loop in an instance body")
	}
	v.typeOK(lp.FieldsOf)
	if k := v.tab.Kind(lp.FieldsOf); k != types.KNamed && k != types.KRecord && k != types.KParam {
		v.fail("M9", "fields loop over %s", v.ts(lp.FieldsOf))
	}
	if lp.FieldsParam >= b.GenericCount {
		v.fail("M9", "hidden generic %d of %d", lp.FieldsParam, b.GenericCount)
	}
	if lp.Header >= lp.Exit {
		v.fail("M9", "fields loop blocks [bb%d, bb%d) are empty", lp.Header, lp.Exit)
	}
	// The latch is Header+2 and ends in goto Exit.
	if latch := lp.Header + 2; latch >= lp.Exit || int(latch) >= len(b.Blocks) {
		v.fail("M9", "fields loop [bb%d, bb%d) has no latch bb%d", lp.Header, lp.Exit, latch)
	} else if t := b.Blocks[latch].Term; t.Kind != TermGoto || t.Target != lp.Exit {
		v.fail("M9", "fields loop latch bb%d does not goto bb%d", latch, lp.Exit)
	}
	for x := range b.Blocks {
		in := BlockID(x) >= lp.Header && BlockID(x) < lp.Exit
		if v.inLoop(l, BlockID(x)) != in {
			v.fail("M9", "bb%d: a fields loop's blocks are exactly [bb%d, bb%d)", x, lp.Header, lp.Exit)
		}
	}
}
