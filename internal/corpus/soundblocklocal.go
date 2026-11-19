// Why: some loans of value arms, index receivers, self-borrowing iterators
// and std's views are not drawn by the general value-arm shapes; these
// shapes aim at each of those forms directly (GenVersion 10).
//
// What: shapes S65 to S69: armScrut, idxKeep, ownIter, dval and armArg,
// refViews and refPool, with the helpers that store a block-local's loan
// into an outer holder.
//
// How: S65 is a value arm over a scrutinee that holds a loan or a
// temporary (for if and match the statement is the whole construct); S66 a
// mut method that stores its argument into self through an index, a nested
// index, ItemsMut or a helper's mut []Cr; S67 for over an iterator whose
// payload borrows the iterator itself; S68 arms that do not fall through
// in a call argument, a constructor field, an array element or a compound
// assignment; S69 references stored through SplitAt, GetPair and ChunksMut
// and into mem.Pool[&i64]. Each keeps the generator's invariants: no trap,
// no fuel, the same value in the evaluator and both builds.

package corpus

import (
	"fmt"
	"slices"
	"strings"
)

// armScrut is S65: a value match or if whose scrutinee holds a loan
// (Find's ?&i64 by value or by reference, Get's Miss!&i64, an if x := e)
// or a temporary (&Lx(..)), with a payload bound and yielded. One time in
// five an arm writes the int the scrutinee borrows before it yields the
// payload (E0502), and one time in four the Lx arm yields a reference into
// the temporary, which ends with the construct (E0506).
func (g *sgen) armScrut() bool {
	if !g.refable() {
		return false
	}
	in1, in0 := g.armIndent(1), g.armIndent(0)
	x := g.fresh("x")
	switch g.rng.intn(7) {
	case 0:
		g.use(itemFind)
		ka := g.kArg()
		if v := g.pick(kInt, true); v != nil && g.chance(1, 5) {
			v.used = true
			b, _ := g.ref()
			s := g.declare(kRef, "r")
			g.linef("var %s := match Find(%s, &%s) {\n%ssome(%s) {\n%s    %s = %s\n%s    %s\n%s}\n%snone { %s }\n%s}",
				s.name, ka, v.name, in1, x, in1, v.name, g.lit(), in1, x, in1, in1, b, in0)
			return true
		}
		a, _ := g.ref()
		b, _ := g.ref()
		s := g.declare(kRef, "r")
		g.linef("var %s := match Find(%s, %s) {\n%ssome(%s) { %s }\n%snone { %s }\n%s}", s.name, ka, a, in1, x, x, in1, b, in0)
	case 1:
		g.use(itemFind)
		ka := g.kArg()
		a, _ := g.ref()
		v := g.ival()
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = match Find(%s, %s) {\n%ssome(%s) { *%s }\n%snone { %s }\n%s}", s.name, ka, a, in1, x, x, in1, v, in0)
	case 2:
		g.use(itemLx)
		a, _ := g.ref()
		p := g.ival()
		y := x + ".src"
		if g.chance(1, 4) {
			y = "&" + x + ".pos"
		}
		s := g.declare(kRef, "r")
		g.linef("var %s := match &Lx(src: %s, pos: %s) {\n%s%s { %s }\n%s}", s.name, a, p, in1, x, y, in0)
	case 3:
		g.use(itemLx)
		a, _ := g.ref()
		p := g.ival()
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = match &Lx(src: %s, pos: %s) {\n%s%s { %s.pos +%% *%s.src }\n%s}", s.name, a, p, in1, x, x, x, in0)
	case 4:
		g.use(itemFind)
		ka := g.kArg()
		a, _ := g.ref()
		b, _ := g.ref()
		s := g.declare(kRef, "r")
		g.linef("var %s := match &Find(%s, %s) {\n%ssome(%s) { *%s }\n%snone { %s }\n%s}", s.name, ka, a, in1, x, x, in1, b, in0)
	case 5:
		g.use(itemFind)
		ka := g.kArg()
		a, _ := g.ref()
		v := g.ival()
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = if %s := Find(%s, %s) { *%s } else { %s }", s.name, x, ka, a, x, v)
	default:
		g.use(itemGet)
		ka := g.kArg()
		a, _ := g.ref()
		b, _ := g.ref()
		e := g.fresh("er")
		s := g.declare(kRef, "r")
		g.linef("var %s := match Get(%s, %s) {\n%sok(%s) { %s }\n%serr(%s) {\n%s    _ = %s\n%s    %s\n%s}\n%s}",
			s.name, ka, a, in1, x, x, in1, e, in1, e, in1, b, in1, in0)
	}
	return true
}

// crPair is a [2]Cr literal's two elements over two <ref>s.
func (g *sgen) crPair() string {
	a, _ := g.ref()
	b, _ := g.ref()
	return "[Cr(r: " + a + "), Cr(r: " + b + ")]"
}

// idxKeep is S66: Cr.Keep, whose argument goes into self,
// through an index receiver. In Run: on a [2]Cr by a literal index, a range
// index or a mut slice of it, on a [2]Gr through two indexes
// (gs[i].rows[j]), and on a List[Cr] of two through ItemsMut (not in
// oracle, which has no arena). In a helper: on its mut []Cr parameter, the
// store recorded for the clauses (r into cs), so a dropped clause is E0604
// and the twin restores it. A <ref> to a block-local int kept in an outer
// array is E0505.
func (g *sgen) idxKeep(inRun bool) bool {
	if !g.refable() {
		return false
	}
	g.use(itemCrKeep)
	if !inRun {
		cs := g.pick(kCrs, false)
		if cs == nil {
			return false
		}
		cs.used = true
		r, org := g.ref()
		g.store(org, cs)
		g.linef("%s[%d].Keep(%s)", cs.name, g.rng.intn(2), r)
		return true
	}
	switch q := g.rng.intn(6); {
	case q == 0:
		gs := g.pick(kGrs, true)
		if gs == nil || g.chance(1, 3) {
			g.use(itemGr)
			a, b := g.crPair(), g.crPair()
			gs = g.declare(kGrs, "gs")
			g.linef("var %s: [2]Gr = [Gr(rows: %s), Gr(rows: %s)]", gs.name, a, b)
		}
		r := g.keptRef(gs)
		g.linef("%s[%d].rows[%d].Keep(%s)", gs.name, g.rng.intn(2), g.rng.intn(2), r)
	case q == 1 && g.prof != ProfileOracle:
		l := g.pick(kCrList, true)
		if l == nil || g.chance(1, 3) {
			g.usesA = true
			name := g.fresh("l")
			g.linef("var %s := List[Cr].New(a)", name)
			a, _ := g.ref()
			g.elseReturn(fmt.Sprintf("%s.Push(Cr(r: %s))", name, a))
			b, _ := g.ref()
			g.elseReturn(fmt.Sprintf("%s.Push(Cr(r: %s))", name, b))
			l = g.declareNamed(kCrList, name)
		}
		r := g.keptRef(l)
		g.linef("%s[%d].Keep(%s)", l.name, g.rng.intn(2), r)
	default:
		cs := g.pick(kCrs, true)
		if cs == nil || g.chance(1, 3) {
			p := g.crPair()
			cs = g.declare(kCrs, "crs")
			g.linef("var %s: [2]Cr = %s", cs.name, p)
		}
		if g.storeInBlock(func(r string) { g.linef("%s[%d].Keep(%s)", cs.name, g.rng.intn(2), r) }) {
			return true
		}
		r := g.keptRef(cs)
		switch g.rng.intn(3) {
		case 0:
			ix := g.fresh("ix")
			g.linef("for %s in 0..2 {", ix)
			g.linef("    %s[%s].Keep(%s)", cs.name, ix, r)
			g.line("}")
		case 1:
			z := g.fresh("zr")
			g.linef("var %s := mut %s[..]", z, cs.name)
			g.linef("%s[%d].Keep(%s)", z, g.rng.intn(2), r)
		default:
			g.linef("%s[%d].Keep(%s)", cs.name, g.rng.intn(2), r)
		}
	}
	return true
}

// ownIter is S67: for over Bf, whose Next returns a reference
// into the iterator itself (from self), so the payload lives only as long
// as the hidden var that holds the evaluated iterator, the whole loop; one
// time in four the body ends by assigning the payload to a ref local of an
// outer block, which the ending reads after the loop (E0506). Or for over
// a named It local, taken by value, which is read after the loop.
func (g *sgen) ownIter() bool {
	if g.depth >= g.maxDepth() {
		return false
	}
	src := ""
	if g.chance(1, 3) {
		src = g.iterSrc() // marks a slice it takes as read: only when it is used
	}
	if src != "" {
		g.use(itemItNext)
		it, i, x := g.fresh("it"), g.fresh("ip"), g.fresh("xp")
		g.linef("var %s := It(xs: %s, i: %d)", it, src, g.rng.intn(2))
		g.linef("for (i: %s, x: %s) in %s {", i, x, it)
		g.enterLoop(4)
		g.open()
		g.declareNamed(kInt, i).fixed = true
		g.declareNamed(kRef, x).fixed = true
		g.body(g.rng.intn(3))
		g.close()
		g.leaveLoop(4)
		g.line("}")
		g.fold("i64(" + it + ".i)")
		return true
	}
	g.use(itemBfNext)
	a, b, c := g.ival(), g.ival(), g.ival()
	outer := g.pick(kRef, true)
	p := g.fresh("p")
	g.linef("for %s in Bf(buf: [%s, %s, %s], i: 0) {", p, a, b, c)
	g.enterLoop(3)
	g.open()
	g.declareNamed(kRef, p).fixed = true
	g.body(g.rng.intn(3))
	if outer != nil && g.chance(1, 4) {
		g.linef("%s = %s", outer.name, p)
	}
	g.close()
	g.leaveLoop(3)
	g.line("}")
	return true
}

// dval is an i64 value arm one of whose arms does not fall through (S55's
// arm in another value position); Run only.
func (g *sgen) dval() string {
	g.armDepth++
	defer func() { g.armDepth-- }()
	v := g.ival()
	d := g.divArm()
	if g.chance(1, 2) {
		c := g.cond()
		if g.chance(1, 2) {
			return "if " + c + " { " + v + " } else " + d
		}
		return "if " + c + " " + d + " else { " + v + " }"
	}
	w := g.ival()
	bl := [3]string{"{ " + v + " }", d, "{ " + w + " }"}
	if g.chance(1, 2) {
		bl[0], bl[1] = bl[1], bl[0]
	}
	return g.matchBlocks(bl)
}

// armArg is S68: an arm that does not fall through, or a value arm,
// in a compound assignment (+%=, *%=), a call argument (Ct.Add, Replace),
// a constructor field (Ct(n: ..)) and an array element; and, not in oracle,
// one time in eight as Drop's argument after the sub-arena it takes by
// value, so the arm leaves while the arena is in its argument slot: E0402
// (the program is marked Leak, a rejection, not a defect).
func (g *sgen) armArg() bool {
	switch q := g.rng.intn(8); {
	case q == 0 && g.prof != ProfileOracle:
		b := g.arenaHere()
		g.use(itemDrop)
		b.moved = true // the arm's return leaves it in the slot: no Free there
		dv := g.dval()
		b.moved = false
		t := g.fresh("t")
		g.elseReturn(fmt.Sprintf("var %s := Drop(a, %s, %s)", t, b.name, dv))
		b.moved = true
		g.leak = true
		g.declareNamed(kInt, t).fixed = true
	case q <= 2:
		v := g.pick(kInt, true)
		if v == nil {
			return false
		}
		op := "+%="
		if g.chance(1, 2) {
			op = "*%="
		}
		x := g.ival()
		if g.chance(1, 2) {
			x = g.dval()
		}
		g.linef("%s %s %s", v.name, op, x)
	case q == 3:
		cs := g.pick(kCts, true)
		if cs == nil {
			return false
		}
		g.use(itemCtAdd)
		g.linef("%s[%d].Add(%s)", cs.name, g.rng.intn(2), g.dval())
	case q == 4:
		g.use(itemCt)
		a, b := g.dval(), g.ival()
		if g.chance(1, 2) {
			a, b = b, a
		}
		cs := g.declare(kCts, "cs")
		g.linef("var %s: [2]Ct = [Ct(n: %s), Ct(n: %s)]", cs.name, a, b)
	case q == 5:
		v := g.pick(kInt, true)
		if v == nil {
			return false
		}
		x := g.dval()
		s := g.declare(kInt, "v")
		g.linef("var %s := Replace(mut %s, %s)", s.name, v.name, x)
	default:
		e := [4]string{g.ival(), g.ival(), g.ival(), g.ival()}
		e[g.rng.intn(4)] = g.dval()
		xs := g.declare(kArray, "xs")
		g.linef("var %s: [4]i64 = [%s]", xs.name, strings.Join(e[:], ", "))
	}
	return true
}

// refViews is S69: references stored through the views std
// hands out over a [4]&i64 (GetPair's mut &i64 pair, SplitAt's halves,
// ChunksMut's pieces; reject7 calls the header's non-generic wrappers), and
// into a mem.Pool[&i64] by Add and through GetPair (not in oracle, which
// has no arena), whose ending walks Iter and reads every reference. A
// <ref> to a block-local int stored into an outer array or pool is E0505.
func (g *sgen) refViews() bool {
	if !g.refable() {
		return false
	}
	if g.prof != ProfileOracle && g.chance(1, 3) {
		return g.refPool()
	}
	rs := g.pick(kRArr, true)
	if rs == nil || g.chance(1, 3) {
		a, _ := g.ref()
		b, _ := g.ref()
		c, _ := g.ref()
		d, _ := g.ref()
		rs = g.declare(kRArr, "rs")
		g.linef("var %s: [4]&i64 = [%s, %s, %s, %s]", rs.name, a, b, c, d)
	}
	if g.storeInBlock(func(r string) {
		c := g.fresh("c")
		if g.wrapped() {
			g.use(itemPiecesMutR)
			g.linef("for %s in PiecesMutR(mut %s[..], 2) {", c, rs.name)
		} else {
			g.linef("for %s in %s.ChunksMut(2) {", c, rs.name)
		}
		g.linef("    %s[1] = %s", c, r)
		g.line("}")
	}) {
		return true
	}
	switch g.rng.intn(3) {
	case 0:
		i := g.rng.intn(4)
		j := (i + 1 + g.rng.intn(3)) % 4
		call := fmt.Sprintf("%s.GetPair(%d, %d)", rs.name, i, j)
		if g.wrapped() {
			g.use(itemPairR)
			call = fmt.Sprintf("PairR(mut %s[..], %d, %d)", rs.name, i, j)
		}
		p := g.fresh("pr")
		r := g.keptRef(rs)
		g.linef("if %s := %s {", p, call)
		g.linef("    *%s.a = %s", p, r)
		if g.chance(1, 2) {
			g.linef("    *%s.b = *%s.a", p, p)
		}
		g.line("}")
	case 1:
		i := 1 + g.rng.intn(3)
		l, r := g.fresh("lf"), g.fresh("rt")
		if g.wrapped() {
			g.use(itemHalvesR)
			g.linef("(left: %s, right: %s) := HalvesR(mut %s[..], %d)", l, r, rs.name, i)
		} else {
			g.linef("(left: %s, right: %s) := %s.SplitAt(%d)", l, r, rs.name, i)
		}
		a := g.keptRef(rs)
		g.linef("%s[%d] = %s", l, g.rng.intn(i), a)
		if g.chance(1, 2) {
			b := g.keptRef(rs)
			g.linef("%s[%d] = %s", r, g.rng.intn(4-i), b)
		} else {
			g.linef("%s[%d] = %s[0]", r, g.rng.intn(4-i), l)
		}
	default:
		w := 1 + g.rng.intn(4)
		c := g.fresh("c")
		r := g.keptRef(rs) // before the loop: it may declare an int in this block
		if g.wrapped() {
			g.use(itemPiecesMutR)
			g.linef("for %s in PiecesMutR(mut %s[..], %d) {", c, rs.name, w)
		} else {
			g.linef("for %s in %s.ChunksMut(%d) {", c, rs.name, w)
		}
		g.linef("    %s[0] = %s", c, r)
		g.line("}")
	}
	return true
}

// refPool is S69's pool: a mem.Pool[&i64] over the entry's arena or a live
// sub-arena (the pool holds its loan), references added (x: T into self) and
// stored through GetPair's pair; its ending walks Iter and reads each one.
func (g *sgen) refPool() bool {
	g.usesA, g.mem, g.hUsed = true, true, true
	p := g.pick(kPoolR, true)
	if p == nil || g.chance(1, 3) {
		arena := "a"
		if b := g.pick(kArena, false); b != nil && g.chance(1, 3) {
			arena = "&" + b.name
		}
		p = g.declare(kPoolR, "pl")
		g.linef("var %s := mem.Pool[&i64].New(%s)", p.name, arena)
	}
	ha, hb := g.fresh("g"), g.fresh("g")
	if g.storeInBlock(func(r string) {
		g.elseReturn(fmt.Sprintf("%s := %s.Add(%s)", ha, p.name, r))
		g.linef("_ = %s", ha)
	}) {
		return true
	}
	a := g.keptRef(p)
	g.elseReturn(fmt.Sprintf("%s := %s.Add(%s)", ha, p.name, a))
	if g.chance(1, 2) {
		g.linef("_ = %s", ha)
		return true
	}
	b := g.keptRef(p)
	g.elseReturn(fmt.Sprintf("%s := %s.Add(%s)", hb, p.name, b))
	pr := g.fresh("pr")
	r := g.keptRef(p)
	g.linef("if %s := %s.GetPair(%s, %s) {", pr, p.name, ha, hb)
	g.linef("    *%s.a = %s", pr, r)
	if g.chance(1, 2) {
		g.linef("    Swap(mut *%s.a, mut *%s.b)", pr, pr)
	}
	g.line("}")
	return true
}

// keptRef is the <ref> a store into holder takes: one time in two, when
// holder was declared outside the current block, a reference to an int of
// the current block (the latest, or one it declares here), which dies at
// the block's end while holder still holds it (E0505, the case S66 and
// S69 are aimed at); else a <ref>. Called before the line that
// stores it is written, outside any block that line opens.
func (g *sgen) keptRef(holder *sname) string {
	if n := len(g.blocks); n > 1 && !slices.Contains(g.blocks[n-1].decls, holder) && g.chance(1, 2) {
		cur := g.blocks[n-1].decls
		for i := len(cur) - 1; i >= 0; i-- {
			if v := cur[i]; v.kind == kInt && !v.moved {
				v.used = true
				return "&" + v.name
			}
		}
		v := g.declare(kInt, "v")
		v.used = true
		g.linef("var %s: i64 = %s", v.name, g.lit())
		return "&" + v.name
	}
	r, _ := g.ref()
	return r
}

// storeInBlock, one time in four when a block may open, writes store inside a
// new branch block, storing a reference to an int declared in that block into a
// holder of an outer block, which outlives it: E0505 (the form S66 and S69 aim
// at: a nested block whose <ref> names a block-local int, read after the block
// by holder's ending). It reports whether it wrote.
func (g *sgen) storeInBlock(store func(r string)) bool {
	if g.depth >= g.maxDepth() || !g.chance(1, 4) {
		return false
	}
	c := g.branch()
	g.linef("if %s {", c)
	g.open()
	v := g.declare(kInt, "v")
	v.used = true
	g.linef("var %s: i64 = %s", v.name, g.lit())
	store("&" + v.name)
	g.body(g.rng.intn(2))
	g.close()
	g.line("}")
	return true
}
