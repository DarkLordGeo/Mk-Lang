// Why: a value used after it was moved, or before it was assigned, is the
// first class of memory error the checker rejects, and the error must point
// at the right move and the right use.
//
// What: useCheck and movedUse (E0401, E0403, E0405, E0406), siteBlock and
// siteSpan, movedInLoop, loopBlocks and reachesInIteration.
//
// How: each root's init state carries its move sites; a use reports once
// per move site. E0405 (moved in an earlier iteration) holds when every site
// lies in a loop that contains the use and no site reaches the use within
// one iteration. E0406 is a move of a value a registered defer still uses.

package borrow

import (
	"slices"

	"mk/internal/diag"
	"mk/internal/mir"
)

// useCheck is the use check on root r. allowTaken says the use is one of
// the three a taken temporary allows (a take, a plain field copy, a
// discriminant). Globals, abstract nodes and slots are exempt.
func (c *checker) useCheck(st *state, ctx *stmtCtx, r rootID, allowTaken bool) {
	if !c.report {
		return
	}
	ri := &c.roots[r]
	if !ri.isLocal() {
		return
	}
	e := st.get(r)
	in := initOf(e)
	if in&initM != 0 || in&initT != 0 && !allowTaken {
		c.movedUse(ctx, r, e.moved)
		return
	}
	if in&initU != 0 {
		c.errAt(diag.E0403, ctx.span, "%s is used before it is assigned on every path", c.rootName(r)).
			Text("place", c.rootName(r)).Loc("declared_at", c.body.Span(c.body.Locals[ri.local].Span))
	}
}

// movedUse reports a use of a moved root.
func (c *checker) movedUse(ctx *stmtCtx, r rootID, sites setID) {
	if sites == emptySet {
		return // the recovery of E0409 moved it; that is the one diagnostic
	}
	blk := &c.body.Blocks[ctx.b]
	if (blk.Origin == mir.OriginDefer || blk.Origin == mir.OriginOnerr) && int(blk.Defer) < len(c.body.Defers) {
		dsp := c.body.Span(c.body.Defers[blk.Defer].Span)
		var later []uint32
		for _, s := range c.sets.get(sites) {
			if sp := c.body.Span(c.siteSpan(s)); sp.File == dsp.File && sp.Start > dsp.Start {
				later = append(later, s)
			}
		}
		if len(later) > 0 {
			for _, s := range later {
				c.errAt(diag.E0406, c.siteSpan(s), "%s is moved here, but a defer registered earlier still uses it", c.rootName(r)).
					Text("place", c.rootName(r)).Loc("defer_at", dsp)
			}
			c.reported = c.sets.unite(c.reported, sites)
			return
		}
	}
	if c.sets.minus(sites, c.reported) == emptySet {
		return // only the first use per move site
	}
	first := c.sets.get(sites)[0]
	if loop, ok := c.movedInLoop(sites, ctx); ok {
		c.errAt(diag.E0405, ctx.span, "%s was moved in an earlier iteration of this loop", c.rootName(r)).
			Text("place", c.rootName(r)).Loc("moved_at", c.body.Span(c.siteSpan(first))).
			Loc("loop", c.body.Span(c.body.Loops[loop].Span))
	} else {
		c.errAt(diag.E0401, ctx.span, "%s is used after it was moved", c.rootName(r)).
			Text("place", c.rootName(r)).Loc("moved_at", c.body.Span(c.siteSpan(first)))
	}
	c.reported = c.sets.unite(c.reported, sites)
}

// siteBlock returns the block and statement index of a site.
func (c *checker) siteBlock(s uint32) (mir.BlockID, int) {
	base := c.live.base
	b, found := slices.BinarySearch(base, s)
	if !found {
		b--
	}
	// Empty-statement blocks share no base, so b is exact.
	return mir.BlockID(b), int(s - base[b])
}

// siteSpan is the span of the statement or terminator at site s.
func (c *checker) siteSpan(s uint32) mir.SpanID {
	b, i := c.siteBlock(s)
	blk := &c.body.Blocks[b]
	if i >= len(blk.Stmts) {
		return blk.Term.Span
	}
	return blk.Stmts[i].Span
}

// movedInLoop is E0405's condition: every site lies inside a
// loop L that also contains the use, and inside one iteration of L (its
// blocks with the back edges into L.Header removed) no site reaches the use.
func (c *checker) movedInLoop(sites setID, ctx *stmtCtx) (mir.LoopID, bool) {
	for li := range c.body.Loops {
		in := c.loopBlocks(mir.LoopID(li))
		if !in[ctx.b] {
			continue
		}
		all := true
		for _, s := range c.sets.get(sites) {
			if b, _ := c.siteBlock(s); !in[b] {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		reaches := false
		for _, s := range c.sets.get(sites) {
			if c.reachesInIteration(mir.LoopID(li), in, s, ctx) {
				reaches = true
				break
			}
		}
		if !reaches {
			return mir.LoopID(li), true
		}
	}
	return mir.NoLoop, false
}

// loopBlocks is the blocks of loop l: reachable from its header without
// passing its exit.
func (c *checker) loopBlocks(l mir.LoopID) []bool {
	if c.loopSets == nil {
		c.loopSets = make([][]bool, len(c.body.Loops))
	}
	if c.loopSets[l] != nil {
		return c.loopSets[l]
	}
	lp := &c.body.Loops[l]
	in := make([]bool, len(c.body.Blocks))
	work := []mir.BlockID{lp.Header}
	in[lp.Header] = true
	var buf []mir.BlockID
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		buf = c.body.Successors(b, buf[:0])
		for _, s := range buf {
			if s == lp.Exit || in[s] {
				continue
			}
			in[s] = true
			work = append(work, s)
		}
	}
	c.loopSets[l] = in
	return in
}

// reachesInIteration reports whether site s reaches the use at ctx inside
// one iteration of loop l (no edge into the header is followed).
func (c *checker) reachesInIteration(l mir.LoopID, in []bool, s uint32, ctx *stmtCtx) bool {
	b, i := c.siteBlock(s)
	if b == ctx.b && i < ctx.i {
		return true
	}
	header := c.body.Loops[l].Header
	seen := make([]bool, len(c.body.Blocks))
	work := []mir.BlockID{b}
	var buf []mir.BlockID
	for len(work) > 0 {
		x := work[len(work)-1]
		work = work[:len(work)-1]
		buf = c.body.Successors(x, buf[:0])
		for _, y := range buf {
			if y == header || !in[y] || seen[y] {
				continue
			}
			if y == ctx.b {
				return true
			}
			seen[y] = true
			work = append(work, y)
		}
	}
	return false
}
