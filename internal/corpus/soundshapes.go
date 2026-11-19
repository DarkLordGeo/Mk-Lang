// Why: the soundness generator's coverage comes from its shapes, each a
// small statement form that creates, moves or uses loans; their ids, weights
// and readiness rules decide what every profile draws.
//
// What: the shape IDs S01 to S88 (stable and append-only, like
// types.PanicCode, so coverage counts stay comparable across generator
// versions), shapeBase and shapeWeight, stmt and helperShapes, and the
// general shapes S01 to S24, S30, S33 and S34 (deep, d2, mutField, lx, array,
// readLoop, slices, aliased, callHelper, ...).
//
// How: stmt draws a shape by weight and falls back to S01 when its pools
// are empty; nested blocks weight locals, stores and helper calls up. The
// io, arena and task shapes are in soundfull.go, the shapes aimed at one
// rule each in soundrules.go, and the later strata in soundlang.go,
// soundviews.go, soundblocklocal.go, soundfields.go and soundholes.go; the header
// they call is soundheader.go.

package corpus

import (
	"fmt"
	"strconv"
	"strings"
)

// The shape IDs.
const (
	sIntLocal     = 1 + iota // S01 var vN: i64 = <lit>
	sRefLocal                // S02 var rN := <ref>
	sReassign                // S03 rN = <ref>
	sReadRef                 // S04 h = h *% 31 +% *rN
	sWriteInt                // S05 vN = <lit>
	sMutBorrow               // S06 var mN := mut vN
	sWriteMut                // S07 *mN = <lit>
	sCell                    // S08 cN := Cell.New(<ref>)
	sCellStore               // S09 cN.Store(<ref>), Put(&cN, <ref>)
	sRefRef                  // S10 var qN := mut rN
	sRefRefStore             // S11 *qN = <ref>, SetRef(qN, <ref>)
	sDeep                    // S12 Deep, Mix, Set2
	sD2                      // S13 D2, Mix2
	sBranch                  // S14 if <branch> { } [else { }]
	sCounted                 // S15 counted loop with a break
	sRange                   // S16 for i in 0..n
	sMutField                // S17 Sl, PeekS, *s.m = <lit>
	sLx                      // S18 Lx sources
	sReborrow                // S19 mut **p chain
	sArray                   // S20 [4]i64 and for x in mut xs
	sReadLoop                // S21 for x in &xs, for i, x in xs
	sSlices                  // S22 sub-slices
	sAliased                 // S23 aliased arguments
	sCallHelper              // S24 a generated helper
	sMutex                   // S25 Mutex[&i64] and its guard
	sSubArena                // S26 a sub-arena, freed on every path
	sArenaCell               // S27 a cell in an arena
	sTasks                   // S28 scope and s.go
	sSpawner                 // S29 a spawn through a helper
	sStatic                  // S30 &KN of a const
	sGlobalMutex             // S31 a global Mutex
	sDefer                   // S32 defer bN.Free()
	sOptional                // S33 if r := Find(..)
	sOnerr                   // S34 onerr in a helper
	sMove                    // S35 an Sl moved; a stale use (E0401), the field alone (E0409)
	sUninit                  // S36 x: i64 assigned on both paths, or one (E0403)
	sArenaBranch             // S37 a sub-arena freed on both paths, or one (E0402)
	sDeferFree               // S38 an explicit Free of a deferred arena (E0406)
	sTemp                    // S39 a reference out of a temporary; or into it, held (E0506)
	sSharedWrite             // S40 an Sl read through a shared reference; or written (E0507)
	sReborrowKill            // S41 a reborrowed mut ref reassigned, written through (the kill)
	sArenaTie                // S42 an arena holding a loan on itself; Reset, or freed while read
	sDropSlot                // S43 an arena passed by value through an argument slot to Drop
	sCapRead                 // S44 a capturing literal read through Fold or FoldFwd
	sCapResult               // S45 Pass with a literal whose result is its parameter, or a capture (E0609)
	sCapRecv                 // S46 a mut receiver (Sl.Apply, Sort) against captured names (E0501, E0504)
	sCapMisuse               // S47 a capture form check rejects (E0609 in the types group)
	sFnValue                 // S48 generic instances as fn values: Ident[i64], IdR[i64], Bx[i64].Get
	sFmtShort                // S49 b.Fmt("..") with no record
	sVecMake                 // S50 a simd.Vec[i64, 2] from an array, a slice, Splat or FromArray
	sVecArith                // S51 lane-wise wrapping and bitwise operators, Min, Max, With, Reverse
	sVecStore                // S52 CopyTo through a mut slice of an array (one bounds check per span)
	sVecReduce               // S53 reductions, lanes and masks folded into h
	sVec32                   // S54 a simd.Vec[i32, 4] of literals, folded
	sArmDiverge              // S55 a value arm with an arm that does not fall through: return, break, continue
	sArmBlock                // S56 value arms with statements before the value, else e { } on E!T, assigned
	sForIter                 // S57 for over It with a destructured payload, Chunks, records by pattern
	sIndexRecv               // S58 a mut receiver through an index: cs[i].Add
	sSwapRepl                // S59 Swap and Replace of ints and references
	sSplitPair               // S60 SplitAt and GetPair views of an array
	sChunksScope             // S61 ChunksMut pieces written by tasks under a scope
	sPoolPair                // S62 mem.Pool: Add, GetPair, Iter, Free
	sArmRef                  // S63 a value arm drawn by ref (never drawn as a statement)
	sArmVal                  // S64 a value arm drawn by ival (never drawn as a statement)
	sArmScrut                // S65 a value match or if over a scrutinee that holds a loan or a temporary
	sIdxKeep                 // S66 Cr.Keep (into self) through an index, a nested index, ItemsMut, a mut []Cr parameter
	sOwnIter                 // S67 for over Bf, whose payload borrows the hidden iterator var; over a named It
	sArmArg                  // S68 a non-falling-through arm in a call argument, constructor field, array element, compound assignment
	sRefViews                // S69 references stored through SplitAt, GetPair, ChunksMut views and mem.Pool[&i64]
	sFpNew                   // S70 a split local: Fp literal, per-field call result, copy, Fq, Fw
	sFpWrite                 // S71 a field write: p.f = r, p.f.g = r (deep), SetRef(mut p.f, r), Swap2 of two fields
	sFpRead                  // S72 a field read, a destructuring with and without .., F().b
	sFpWhole                 // S73 whole uses: &p, by value, mut p, a mut self call, &self on Fw, p = e
	sFpJoin                  // S74 a block-local's loan put in different fields on two paths, then a field read (the join)
	sFpLoop                  // S75 fields rotated through a loop that stores a block-local's loan (the fixpoint)
	sFaHole                  // S76 a holder of an arena cell, stores through it, a field written beside it (later-hop store)
	sFhHole                  // S77 self-reach: FhPut on a holder with a literal or a split Fs
	sFqDeep                  // S78 two deep writes into one field of an Fq, the first a block-local's loan (p.f.g = e adds)
	sFwMix                   // S79 a field's loan moved into an Fw's inline Cell by a clause-free &w callee
	sFrMerge                 // S80 mut borrows of two fields in one Fr, a store through it (a store that knows no field)
	sFpGen                   // S81 a generated per-field helper, its clause wrong one time in four (E0602 at the return)
	sC3List                  // S82 later hop: a store through a List in front of an arena cell, a local's loan one time in three (M-C3-subarena, D612-d462-d7)
	sC3Nest                  // S83 later hop: a store through a second arena's cell that holds the first arena's cell (M-C3-content, FH-d462-r1c)
	sC3Front                 // S84 later hop: the same through a local cell in front of the second arena's cell (M-C3-deep, FH-d462-r1e)
	sSelfFwd                 // S85 self-reach through a forwarding parameter, the write one time in three (M-D694, FH-selfreach-r3)
	sFpCross                 // S86 W2: a per-field result crossed one time in three, kept by a caller of its own (M-F8-W2-ret-union)
	sShallow                 // S87 shallow stores: a cell's loan put into a holder deep, shallow or both, then a later store through it (the M-F3 rows)
	sGlobalCall              // S88 a local's loan stored into S31's global mutex reached through a call, or the global returned for a result from a parameter, one time in three (M-D756-result, -indirect, -boundary)
	nShapes       = sGlobalCall
)

// shapeBase is each shape's ProfileFull weight.
var shapeBase = [nShapes + 1]uint16{0,
	8, 10, 6, 6, 5, 5, 5, 4, 5, 4, // S01 to S10
	5, 4, 3, 6, 4, 3, 3, 3, 3, 4, // S11 to S20
	2, 3, 4, 5, 2, 2, 3, 3, 1, 1, // S21 to S30
	1, 1, 2, 1, 3, 2, 1, 1, 2, 2, // S31 to S40
	2, 2, 2, // S41 to S43
	4, 3, 2, 1, 3, 2, 3, 3, 2, 3, // S44 to S53 (GenVersion 7, profiles lang and vec)
	1,                      // S54
	2, 2, 3, 2, 3, 3, 2, 2, // S55 to S62 (GenVersion 8, profiles m7, reject7 and oracle)
	0, 0, // S63, S64: drawn inside ref and ival
	2, 2, 2, 2, 2, // S65 to S69 (GenVersion 10)
	8, 8, 6, 6, 3, 2, 4, 4, 2, 2, 2, 3, // S70 to S81 (GenVersion 11, profiles fields and rejectf only)
	holeWeight, holeWeight, holeWeight, holeWeight, holeWeight, holeWeight, holeWeight, // S82 to S88 (GenVersion 13, profile fields only; soundholes.go)
}

// needsIo and needsArena are the shapes that make Run take io or a.
func needsIo(id int) bool {
	return id == sMutex || id == sTasks || id == sSpawner || id == sGlobalMutex || id == sChunksScope || id == sGlobalCall
}
func needsArena(id int) bool {
	return id == sSubArena || id == sArenaCell || id == sDefer || id == sOnerr || id == sArenaBranch || id == sDeferFree || id == sArenaTie || id == sDropSlot || id == sFmtShort || id == sPoolPair ||
		id == sC3List || id == sC3Nest || id == sC3Front
}

// shapeWeight is shape id's weight in profile p.
func shapeWeight(id int, p Profile) int {
	w := int(shapeBase[id])
	if id >= sFpNew {
		return fieldWeight(id, p, w)
	}
	if id >= sArmDiverge {
		return viewsWeight(id, p, w)
	}
	if id >= sCapRead {
		return langWeight(id, p, w)
	}
	switch p {
	case ProfileCore, ProfileOracle:
		if needsIo(id) || needsArena(id) {
			return 0
		}
	case ProfileTasks:
		if id == sTasks || id == sSpawner {
			return 8
		}
	case ProfileReject, ProfileReject7, ProfileRejectF:
		if id == sOnerr {
			return 0
		}
	}
	return w
}

// nestedBoost are the shapes that get double weight inside a block, the
// model's bias to locals, stores and helper calls in nested blocks.
func nestedBoost(id int) bool {
	switch id {
	case sIntLocal, sWriteMut, sCellStore, sRefRefStore, sDeep, sCallHelper, sArenaCell, sFpWrite, sFpWhole, sFaHole, sFhHole:
		return true
	}
	return false
}

// stmt draws one statement of Run by weight; a shape whose pools are empty
// falls back to S01.
func (g *sgen) stmt() {
	total := 0
	for id := 1; id <= nShapes; id++ {
		total += g.weight(id)
	}
	r := g.rng.intn(total)
	id := 1
	for ; id <= nShapes; id++ {
		r -= g.weight(id)
		if r < 0 {
			break
		}
	}
	if !g.emit(id) {
		g.emit(sIntLocal)
	}
}

func (g *sgen) weight(id int) int {
	w := shapeWeight(id, g.prof)
	if g.depth > 0 && nestedBoost(id) {
		w *= 2
	}
	return w
}

// helperShapes are the shapes a helper's body draws from, with weights:
// those that work on its parameter pools.
var helperShapes = []struct{ id, w int }{
	{sReadRef, 6}, {sWriteMut, 5}, {sCellStore, 5}, {sRefRefStore, 5}, {sDeep, 4},
	{sBranch, 6}, {sRange, 3}, {sSlices, 3}, {sCallHelper, 5},
}

// hstmt draws one statement of a helper's body; a shape that is not ready
// falls back to reading a parameter.
func (g *sgen) hstmt() {
	hs := helperShapes
	switch {
	case viewsProfile(g.prof):
		hs = helperShapesViews
	case langProfile(g.prof):
		hs = helperShapesLang
	}
	total := 0
	for _, s := range hs {
		total += s.w
	}
	r := g.rng.intn(total)
	for _, s := range hs {
		r -= s.w
		if r < 0 {
			if !g.emit(s.id) {
				p := g.cur.params[g.rng.intn(len(g.cur.params))]
				g.useParam(p)
			}
			return
		}
	}
}

// maxDepth is the nesting bound: 3 in Run, 2 in a helper, whose body is 1
// to 6 statements.
func (g *sgen) maxDepth() int {
	if g.cur != nil {
		return 2
	}
	return 3
}

// nested is the statement count of a nested block: 1 to 5, 1 to 3
// in a helper.
func (g *sgen) nested() int {
	if g.cur != nil {
		return 1 + g.rng.intn(3)
	}
	return 1 + g.rng.intn(5)
}

// body draws n statements of the function being written.
func (g *sgen) body(n int) {
	for range n {
		if g.cur != nil {
			g.hstmt()
		} else {
			g.stmt()
		}
	}
}

// refable reports whether ref can draw a <ref>.
func (g *sgen) refable() bool {
	if g.has(kInt, false) || g.has(kRef, false) || g.has(kCell, false) || g.has(kACell, false) {
		return true
	}
	return g.cur != nil && (g.has(kRefRef, false) || g.has(kDeep, false) || g.has(kD2, false))
}

// emit writes shape id when it is ready, and reports whether it did.
func (g *sgen) emit(id int) bool {
	ok := g.emitShape(id)
	if ok {
		g.shapes[id] = true
	}
	return ok
}

func (g *sgen) emitShape(id int) bool {
	inRun := g.cur == nil
	if id >= sFpNew {
		return g.fieldShape(id, inRun)
	}
	if id >= sCapRead {
		return g.langShape(id, inRun)
	}
	switch id {
	case sIntLocal:
		if !inRun {
			return false
		}
		v := g.ival()
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = %s", s.name, v)
	case sRefLocal:
		if !inRun || !g.refable() {
			return false
		}
		r, _ := g.ref()
		s := g.declare(kRef, "r")
		g.linef("var %s := %s", s.name, r)
	case sReassign:
		if !inRun || !g.has(kRef, true) || !g.refable() {
			return false
		}
		s := g.pick(kRef, true)
		r, _ := g.ref()
		g.linef("%s = %s", s.name, r)
	case sReadRef:
		s := g.pick(kRef, false)
		if s == nil {
			return false
		}
		s.used = true
		g.fold("*" + s.name)
	case sWriteInt:
		s := g.pick(kInt, true)
		if !inRun || s == nil {
			return false
		}
		g.linef("%s = %s", s.name, g.ival())
	case sMutBorrow:
		v := g.pick(kInt, true)
		if !inRun || v == nil {
			return false
		}
		s := g.declare(kMut, "m")
		g.linef("var %s := mut %s", s.name, v.name)
	case sWriteMut:
		s := g.pick(kMut, false)
		if s == nil {
			return false
		}
		s.used = true
		g.linef("*%s = %s", s.name, g.ival())
	case sCell:
		if !inRun || !g.refable() {
			return false
		}
		r, _ := g.ref()
		s := g.declare(kCell, "c")
		s.fixed = true
		g.linef("%s := Cell.New(%s)", s.name, r)
	case sCellStore:
		c := g.pick(kCell, false)
		if c == nil || !g.refable() {
			return false
		}
		r, org := g.ref()
		g.store(org, c)
		switch {
		case g.chance(1, 2):
			g.linef("%s.Store(%s)", c.name, r)
		case c.param:
			g.use(itemPut)
			g.linef("Put(%s, %s)", c.name, r)
		default:
			g.use(itemPut)
			g.linef("Put(&%s, %s)", c.name, r)
		}
	case sRefRef:
		r := g.pick(kRef, true)
		if !inRun || r == nil {
			return false
		}
		s := g.declare(kRefRef, "q")
		g.linef("var %s := mut %s", s.name, r.name)
		r.inQ = true
	case sRefRefStore:
		q := g.pick(kRefRef, false)
		if q == nil || !g.refable() {
			return false
		}
		q.used = true
		r, org := g.ref()
		g.store(org, q)
		if g.chance(1, 2) {
			g.linef("*%s = %s", q.name, r)
		} else {
			g.use(itemSetRef)
			g.linef("SetRef(%s, %s)", q.name, r)
		}
	case sDeep:
		return g.deep(inRun)
	case sD2:
		return g.d2(inRun)
	case sBranch:
		if g.depth >= g.maxDepth() {
			return false
		}
		cond := g.branch()
		g.linef("if %s {", cond)
		g.open()
		g.body(g.nested())
		g.close()
		if g.chance(1, 2) {
			g.line("} else {")
			g.open()
			g.body(g.nested())
			g.close()
		}
		g.line("}")
	case sCounted:
		if !inRun || g.depth >= g.maxDepth() {
			return false
		}
		n := g.fresh("n")
		g.linef("var %s := 0", n)
		g.linef("for %s < 4 {", n)
		g.enterLoop(4)
		g.open()
		g.linef("%s += 1", n)
		g.body(g.nested())
		g.closeWith(func() {
			cond := g.branch()
			g.linef("if %s {", cond)
			g.line("    break")
			g.line("}")
		})
		g.leaveLoop(4)
		g.line("}")
	case sRange:
		if g.depth >= g.maxDepth() {
			return false
		}
		n := 1 + g.rng.intn(4)
		i := g.fresh("ix")
		g.linef("for %s in 0..%d {", i, n)
		g.enterLoop(n)
		g.open()
		g.fold("i64(" + i + ")")
		if a := g.pick(kArray, false); a != nil && g.chance(1, 2) {
			g.fold(a.name + "[" + i + "]")
		}
		g.body(g.nested())
		g.close()
		g.leaveLoop(n)
		g.line("}")
	case sMutField:
		return g.mutField(inRun)
	case sLx:
		return g.lx(inRun)
	case sReborrow:
		m := g.pick(kMut, true)
		if !inRun || m == nil {
			return false
		}
		m.used = true
		p, y := g.fresh("p"), g.fresh("y")
		g.linef("%s := mut %s", p, m.name)
		g.linef("%s := mut **%s", y, p)
		g.linef("*%s = 6", y)
		if g.chance(1, 2) {
			g.linef("**%s = 7", p)
		}
	case sArray:
		return g.array(inRun)
	case sReadLoop:
		return g.readLoop(inRun)
	case sSlices:
		return g.slices(inRun)
	case sAliased:
		return g.aliased(inRun)
	case sCallHelper:
		return g.callHelper(false)
	case sStatic:
		if !inRun {
			return false
		}
		k := g.static()
		if r := g.pick(kRef, true); r != nil && g.chance(1, 2) {
			g.linef("%s = &%s", r.name, k)
		} else {
			s := g.declare(kRef, "r")
			g.linef("var %s := &%s", s.name, k)
		}
	case sTemp:
		return g.temp(inRun)
	case sSharedWrite:
		return g.sharedWrite(inRun)
	case sReborrowKill:
		return g.reborrowKill(inRun)
	case sMove:
		return g.move(inRun)
	case sUninit:
		return g.uninit(inRun)
	case sOptional:
		if !inRun || !g.refable() {
			return false
		}
		g.use(itemFind)
		r, _ := g.ref()
		name := g.fresh("r")
		g.linef("if %s := Find(%s, %s) {", name, g.kArg(), r)
		g.open()
		g.declareNamed(kRef, name).fixed = true
		g.close()
		g.line("}")
	default:
		return g.fullShape(id)
	}
	return true
}

// consumable is the last sub-arena of Run a consuming helper may take by
// value: not deferred (E0406), not moved, and declared in the calling
// block itself, so the move is on every path of the arena's scope (a move
// in a branch leaks it on the other path, E0402; from outside a loop, it
// is E0405).
func (g *sgen) consumable() *sname {
	if g.cur != nil || len(g.blocks) == 0 {
		return nil
	}
	var c *sname
	for _, b := range g.blocks[len(g.blocks)-1].decls {
		if b.kind == kArena && !b.deferred && !b.moved {
			c = b
		}
	}
	return c
}

// enterLoop and leaveLoop bracket a loop body of at most n trips: the loop
// depth S35 reads, and the multiplier of a spawn's count (the host's bound).
func (g *sgen) enterLoop(n int) { g.loops++; g.mult *= n }
func (g *sgen) leaveLoop(n int) { g.loops--; g.mult /= n }

// closeWith is close with tail written after the ending, inside the block.
func (g *sgen) closeWith(tail func()) {
	b := &g.blocks[len(g.blocks)-1]
	g.ending(b.decls)
	b.decls = nil
	tail()
	g.close()
}

// deep is S12: a Deep built from a reference and a ref local, or Mix and
// Set2 on one.
func (g *sgen) deep(inRun bool) bool {
	d := g.pick(kDeep, inRun)
	if d != nil && (g.chance(1, 2) || !inRun) {
		if g.chance(1, 2) || !g.refable() {
			d.used = true
			g.use(itemDeepMix)
			g.store([]origin{{d, true}}, d)
			g.linef("%s.Mix()", d.name)
			return true
		}
		r, org := g.ref()
		g.store(org, d)
		g.use(itemSet2)
		if d.param {
			g.linef("Set2(%s, %s)", d.name, r)
		} else {
			g.linef("Set2(mut %s, %s)", d.name, r)
		}
		return true
	}
	b := g.pick(kRef, true)
	if !inRun || b == nil || !g.refable() {
		return false
	}
	r, _ := g.ref()
	g.use(itemDeep)
	s := g.declare(kDeep, "dp")
	g.linef("var %s := Deep(a: %s, b: mut %s)", s.name, r, b.name)
	return true
}

// d2 is S13: a D2 over a cell, or Mix2 on one.
func (g *sgen) d2(inRun bool) bool {
	if !inRun {
		return false
	}
	if e := g.pick(kD2, true); e != nil && g.chance(1, 2) {
		g.use(itemD2Mix2)
		g.linef("%s.Mix2()", e.name)
		return true
	}
	c := g.pick(kCell, false)
	if c == nil || !g.refable() {
		return false
	}
	r, _ := g.ref()
	g.use(itemD2)
	s := g.declare(kD2, "e")
	g.linef("var %s := D2(a: %s, c: &%s)", s.name, r, c.name)
	return true
}

// mutField is S17: a struct with a mut field.
func (g *sgen) mutField(inRun bool) bool {
	if !inRun {
		return false
	}
	if s := g.pick(kSl, false); s != nil && g.chance(1, 2) {
		if g.chance(1, 2) {
			g.use(itemPeekS)
			r := g.declare(kRef, "r")
			g.linef("var %s := PeekS(&%s)", r.name, s.name)
		} else {
			g.linef("*%s.m = %s", s.name, g.ival())
		}
		return true
	}
	v := g.pick(kInt, true)
	if v == nil {
		return false
	}
	g.use(itemSl)
	s := g.declare(kSl, "s")
	g.linef("var %s := Sl(m: mut %s)", s.name, v.name)
	if g.chance(1, 2) {
		g.use(itemPeekS)
		r := g.declare(kRef, "r")
		g.linef("var %s := PeekS(&%s)", r.name, s.name)
	}
	if g.chance(1, 2) {
		g.linef("*%s.m = %s", s.name, g.lit())
	}
	return true
}

// lx is S18: where a result comes from, over a struct with a reference
// field.
func (g *sgen) lx(inRun bool) bool {
	if !inRun || !g.refable() {
		return false
	}
	l := g.pick(kLx, true)
	if l == nil || g.chance(1, 3) {
		r, _ := g.ref()
		g.use(itemLx)
		l = g.declare(kLx, "l")
		g.linef("var %s := Lx(src: %s, pos: 0)", l.name, r)
	}
	if g.chance(1, 2) {
		r := g.declare(kRef, "r")
		switch g.rng.intn(3) {
		case 0:
			g.use(itemCur)
			g.linef("var %s := Cur(mut %s)", r.name, l.name)
		case 1:
			g.use(itemSrc)
			g.linef("var %s := Src(&%s)", r.name, l.name)
		default:
			g.use(itemOwn)
			g.linef("var %s := Own(&%s)", r.name, l.name)
		}
	}
	switch q := g.rng.intn(20); {
	case q < 7:
		r, _ := g.ref()
		g.linef("%s.src = %s", l.name, r)
	case q < 14:
		g.linef("%s.pos = 5", l.name)
	}
	return true
}

// array is S20: a [4]i64 and a for x in mut xs over it (disjoint elements).
func (g *sgen) array(inRun bool) bool {
	if !inRun {
		return false
	}
	e0, e1, e2, e3 := g.ival(), g.ival(), g.ival(), g.ival()
	xs := g.declare(kArray, "xs")
	g.linef("var %s: [4]i64 = [%s, %s, %s, %s]", xs.name, e0, e1, e2, e3)
	if g.depth >= g.maxDepth() {
		return true
	}
	outer := g.pick(kRef, true)
	// keep (GenVersion 4): a cell that stores every element and is read
	// right after the loop, outside the pools so its loans end there. The
	// element loans of one activation do not conflict, so the next
	// trip's element borrow is clean though the cell's loan is live.
	keep := ""
	if g.refable() && g.chance(1, 4) {
		r, _ := g.ref()
		keep = g.fresh("c")
		g.linef("%s := Cell.New(%s)", keep, r)
	}
	x := g.fresh("x")
	g.linef("for %s in mut %s {", x, xs.name)
	g.enterLoop(4)
	g.open()
	s := g.declareNamed(kMut, x)
	s.fixed, s.used = true, true
	g.linef("*%s = *%s +%% 1", x, x)
	if outer != nil && g.chance(3, 10) {
		g.linef("%s = &*%s", outer.name, x)
	}
	if keep != "" {
		g.linef("%s.Store(&*%s)", keep, x)
	}
	g.body(g.rng.intn(4))
	g.close()
	g.leaveLoop(4)
	g.line("}")
	if keep != "" {
		g.fold("*" + keep + ".Load()")
	}
	return true
}

// readLoop is S21: the read loops over an array.
func (g *sgen) readLoop(inRun bool) bool {
	xs := g.pick(kArray, false)
	if !inRun || xs == nil || g.depth >= g.maxDepth() {
		return false
	}
	x := g.fresh("x")
	g.enterLoop(4)
	if g.chance(1, 2) {
		g.linef("for %s in &%s {", x, xs.name)
		g.open()
		g.declareNamed(kRef, x).fixed = true
	} else {
		i := g.fresh("ix")
		g.linef("for %s, %s in %s {", i, x, xs.name)
		g.open()
		g.fold("i64(" + i + ")")
		g.declareNamed(kInt, x).fixed = true
	}
	g.body(g.rng.intn(4))
	g.close()
	g.leaveLoop(4)
	g.line("}")
	return true
}

// slices is S22: sub-slices of an array, or work on a helper's slice
// parameter.
func (g *sgen) slices(inRun bool) bool {
	if !inRun {
		if z := g.pick(kMSlice, false); z != nil && g.chance(1, 2) {
			z.used = true
			x := g.fresh("x")
			g.linef("for %s in mut %s {", x, z.name)
			g.linef("    *%s = *%s +%% 1", x, x)
			g.line("}")
			return true
		}
		z := g.pick(kSlice, false)
		if z == nil {
			return false
		}
		z.used = true
		g.fold("i64(" + z.name + ".Len())")
		return true
	}
	xs := g.pick(kArray, true)
	if xs == nil {
		return false
	}
	switch q := g.rng.intn(4); {
	case q < 2:
		z := g.declare(kMSlice, "z")
		z.n = 2
		g.linef("var %s := mut %s[1..3]", z.name, xs.name)
		if g.chance(1, 2) {
			z.used = true
			g.linef("%s[0] = %s", z.name, g.lit())
		}
	case q == 2:
		z := g.declare(kSlice, "z")
		z.n = 2
		g.linef("var %s := %s[1..3]", z.name, xs.name)
	default:
		a, b := g.declare(kMSlice, "z"), g.declare(kMSlice, "z")
		a.n, b.n = 2, 2
		g.linef("var %s := mut %s[0..2]", a.name, xs.name)
		g.linef("var %s := mut %s[2..4]", b.name, xs.name)
		a.used, b.used = true, true
		g.linef("%s[0] = %s", a.name, g.lit())
		g.linef("%s[0] = %s", b.name, g.lit())
	}
	return true
}

// aliased is S23: two arguments on one root, or on two (E0501, E0502).
func (g *sgen) aliased(inRun bool) bool {
	if !inRun {
		return false
	}
	switch g.rng.intn(3) {
	case 0:
		a := g.pick(kInt, true)
		if a == nil {
			return false
		}
		b := a
		if g.chance(1, 2) {
			b = g.pick(kInt, true)
		}
		g.use(itemTwo)
		g.linef("Two(&%s, mut %s)", a.name, b.name)
	case 1:
		a := g.pick(kRef, true)
		if a == nil {
			return false
		}
		b := a
		if g.chance(1, 2) {
			b = g.pick(kRef, true)
		}
		g.use(itemSwap2)
		g.linef("Swap2(mut %s, mut %s)", a.name, b.name)
	default:
		return g.callHelper(true)
	}
	return true
}

// callHelper is S24 (and S23's helper form when alias is set): a call of a
// generated helper whose parameters the pools can serve. In a helper, only
// an earlier helper is called, and its declared stores are restated.
func (g *sgen) callHelper(alias bool) bool {
	var cands []*shelper
	for _, h := range g.hs {
		if h == g.cur {
			break
		}
		if g.canCall(h) {
			cands = append(cands, h)
		}
	}
	if len(cands) == 0 {
		return false
	}
	h := cands[g.rng.intn(len(cands))]
	var av, ar *sname // the shared roots of an aliased call
	if alias && g.cur == nil {
		av, ar = g.pick(kInt, true), g.pick(kRef, true)
	}
	args := make([]string, 0, len(h.params)+2)
	if h.usesK {
		args = append(args, g.kArg())
	}
	if h.arena {
		g.usesA = true
		args = append(args, "a")
	}
	var bp *sname // the sub-arena a consuming helper takes by value
	if h.consumes {
		bp = g.consumable()
		args = append(args, bp.name)
	}
	if h.late {
		args = append(args, g.kArg())
	}
	type argOrg struct {
		org []origin
		to  *sname
	}
	ao := make([]argOrg, len(h.params))
	for i, p := range h.params {
		var a string
		switch p.kind {
		case kRef:
			switch {
			case av != nil && g.chance(1, 2):
				a = "&" + av.name
			case ar != nil && g.chance(1, 2):
				a, ao[i].org = ar.name, nil
			default:
				a, ao[i].org = g.ref()
			}
		case kMut:
			if g.cur != nil {
				s := g.pick(kMut, false)
				s.used = true
				a = s.name
			} else {
				s := av
				if s == nil {
					s = g.pick(kInt, true)
				}
				a = "mut " + s.name
			}
		case kRefRef:
			if g.cur != nil {
				s := g.pick(kRefRef, false)
				s.used = true
				a, ao[i].to = s.name, s
				ao[i].org = []origin{{s, true}}
			} else if q := g.pick(kRefRef, false); q != nil && ar == nil && (!g.has(kRef, true) || g.chance(1, 2)) {
				q.used = true
				a = q.name // a reborrow of the reference to a reference
			} else {
				s := ar
				if s == nil {
					s = g.pick(kRef, true)
				}
				a = "mut " + s.name
			}
		case kCell:
			s := g.pick(kCell, false)
			s.used = true
			if g.cur != nil {
				a, ao[i].to, ao[i].org = s.name, s, []origin{{s, true}}
			} else {
				a = "&" + s.name
			}
		case kDeep:
			if g.cur != nil {
				s := g.pick(kDeep, false)
				s.used = true
				a, ao[i].to, ao[i].org = s.name, s, []origin{{s, true}}
			} else {
				a = "mut " + g.pick(kDeep, true).name
			}
		case kD2:
			s := g.pick(kD2, false)
			s.used = true
			if g.cur != nil {
				a, ao[i].org = s.name, []origin{{s, true}}
			} else {
				a = "&" + s.name
			}
		case kSlice:
			if g.cur != nil {
				s := g.pick(kSlice, false)
				s.used = true
				a = s.name
			} else if z := g.pick(kSlice, false); z != nil && (g.chance(1, 2) || !g.has(kArray, false)) {
				z.used = true
				a = z.name
			} else {
				a = g.pick(kArray, false).name + "[..]"
			}
		case kMSlice:
			if g.cur != nil {
				s := g.pick(kMSlice, false)
				s.used = true
				a = s.name
			} else if z := g.pick(kMSlice, false); z != nil && (g.chance(1, 2) || !g.has(kArray, true)) {
				z.used = true
				a = z.name
			} else {
				a = "mut " + g.pick(kArray, true).name + "[..]"
			}
		case kCrs:
			s := g.pick(kCrs, g.cur == nil)
			s.used = true
			if g.cur != nil {
				a, ao[i].to, ao[i].org = s.name, s, []origin{{s, true}}
			} else {
				a = "mut " + s.name + "[..]"
			}
		}
		args = append(args, a)
	}
	// A helper restates what the callee declares it stores.
	if g.cur != nil {
		for _, st := range h.declared {
			fi, ti := indexOf(h.params, st.from), indexOf(h.params, st.to)
			if ti < 0 || fi < 0 || ao[ti].to == nil {
				continue
			}
			g.store(ao[fi].org, ao[ti].to)
		}
	}
	call := h.name + "(" + strings.Join(args, ", ") + ")"
	switch {
	case h.arena: // S34's caller handles the error with else
		t := g.fresh("t")
		g.elseReturn("var " + t + " := " + call) // the handler frees bp: the caller keeps it on failure
		g.declareNamed(kInt, t).fixed = true
		if bp != nil {
			bp.moved = true
		}
	case !h.refRes:
		g.fold(call)
	case g.cur != nil:
		g.fold("*" + call)
	default:
		if r := g.pick(kRef, true); r != nil && g.chance(1, 2) {
			g.linef("%s = %s", r.name, call)
		} else {
			s := g.declare(kRef, "r")
			g.linef("var %s := %s", s.name, call)
		}
	}
	return true
}

func indexOf(ps []*sname, s *sname) int {
	for i, p := range ps {
		if p == s {
			return i
		}
	}
	return -1
}

// canCall reports whether the pools can serve every parameter of h. Only
// Run has an arena to pass to an S34 helper.
func (g *sgen) canCall(h *shelper) bool {
	if h.arena && g.cur != nil || h.consumes && g.consumable() == nil {
		return false
	}
	for _, p := range h.params {
		ok := false
		inRun := g.cur == nil
		switch p.kind {
		case kRef:
			ok = g.refable()
		case kMut:
			ok = (inRun && g.has(kInt, true)) || (!inRun && g.has(kMut, false))
		case kRefRef:
			ok = (inRun && (g.has(kRef, true) || g.has(kRefRef, false))) || (!inRun && g.has(kRefRef, false))
		case kCell:
			ok = g.has(kCell, false)
		case kDeep:
			ok = g.has(kDeep, inRun)
		case kD2:
			ok = g.has(kD2, false)
		case kSlice:
			ok = g.has(kSlice, false) || (inRun && g.has(kArray, false))
		case kMSlice:
			ok = g.has(kMSlice, false) || (inRun && g.has(kArray, true))
		case kCrs:
			ok = g.has(kCrs, inRun)
		}
		if !ok {
			return false
		}
	}
	return true
}

// static declares a new const KN: i64 for a static loan (S30, S31).
func (g *sgen) static() string {
	k := "K" + strconv.Itoa(len(g.statics))
	g.statics = append(g.statics, k)
	g.consts = append(g.consts, fmt.Sprintf("const %s: i64 = %s", k, g.lit()))
	return k
}

// elseReturn writes an else handler that frees every live arena whose
// free is not deferred, innermost first, and returns h (every local arena
// is freed on every path, the else handlers included).
func (g *sgen) elseReturn(head string) {
	g.line(head + " else {")
	as := g.pools[kArena]
	for i := len(as) - 1; i >= 0; i-- {
		if !as[i].deferred && !as[i].moved {
			g.linef("    %s.Free()", as[i].name)
		}
	}
	g.line("    return h")
	g.line("}")
}
