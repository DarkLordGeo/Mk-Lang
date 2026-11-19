// Why: value arms, iterators with destructured payloads, index receivers and
// the slice and pool primitives each lower to loans the checker must track;
// the soundness generator draws them where their loans meet every other
// shape's (profiles m7, reject7 and oracle).
//
// What: shapes S55 to S62 with viewsWeight and helperShapesViews: armRef and ival
// (value arms in every value position), armDiverge, armBlock, forIter,
// indexRecv, swapRepl, splitPair, chunksScope and poolPair.
//
// How: value arms appear in bindings, assignments, returns, call arguments,
// constructor fields and array elements, nested, with arms that do not fall
// through and else value blocks on ?T and E!T. Each form lowers to what the
// checker already proves. Every form keeps the generator's invariants: no
// trap, no fuel, the same value in the evaluator and both builds.

package corpus

import (
	"fmt"
	"strings"
)

// viewsProfile reports whether profile p draws this file's shapes: m7, reject7 and
// oracle (oracle without the shapes that need io or an arena).
func viewsProfile(p Profile) bool {
	return p == ProfileViews || p == ProfileReject7 || p == ProfileOracle || fieldsProfile(p)
}

// viewsWeight is shape id's weight (id at least S55) in profile p, from its
// base weight w.
func viewsWeight(id int, p Profile, w int) int {
	if !viewsProfile(p) {
		return 0
	}
	if p == ProfileOracle && (needsIo(id) || needsArena(id)) {
		return 0
	}
	return w
}

// helperShapesViews is helperShapesLang with S59 and S66: a helper swaps and replaces
// through its mut and mut-reference parameters, and Keeps a reference in
// an element of its mut []Cr parameter (GenVersion 10), which its clauses state.
var helperShapesViews = append(append([]struct{ id, w int }{}, helperShapesLang...), struct{ id, w int }{sSwapRepl, 3}, struct{ id, w int }{sIdxKeep, 3})

// viewsShape writes shape id of GenVersion 8 when it is ready.
func (g *sgen) viewsShape(id int, inRun bool) bool {
	switch id {
	case sArmDiverge:
		return inRun && g.armDiverge()
	case sArmBlock:
		return inRun && g.armBlock()
	case sForIter:
		return inRun && g.forIter()
	case sIndexRecv:
		return inRun && g.indexRecv()
	case sSwapRepl:
		return g.swapRepl(inRun)
	case sSplitPair:
		return inRun && g.splitPair()
	case sChunksScope:
		return inRun && g.chunksScope()
	case sPoolPair:
		return inRun && g.poolPair()
	case sArmScrut:
		return inRun && g.armScrut()
	case sIdxKeep:
		return g.idxKeep(inRun)
	case sOwnIter:
		return inRun && g.ownIter()
	case sArmArg:
		return inRun && g.armArg()
	case sRefViews:
		return inRun && g.refViews()
	}
	return false
}

// wrapped reports whether the Slice[T] views go through the header's
// non-generic wrappers (Halves, Pair, Pieces, PiecesMut): in reject7, so a
// Rejected run reaches them (GenVersion 9; a Slice[T] call from a rejected
// body ends the run Skipped poisoned, which biased the rejection sample).
func (g *sgen) wrapped() bool { return g.prof == ProfileReject7 || g.prof == ProfileRejectF }

// armOK reports whether ref and ival may write a value arm here.
func (g *sgen) armOK() bool { return viewsProfile(g.prof) && g.armDepth < 2 }

// cond is a branch condition on one bit of k with no k step, so a value
// arm's condition sits inside an expression; in a helper it makes k a
// parameter.
func (g *sgen) cond() string {
	if g.cur != nil {
		g.cur.usesK = true
	}
	return fmt.Sprintf("((k >> %d) & 1) == 1", 1+g.rng.intn(62))
}

// sel is a match scrutinee over two bits of k.
func (g *sgen) sel() string {
	if g.cur != nil {
		g.cur.usesK = true
	}
	return fmt.Sprintf("(k >> %d) & 3", 1+g.rng.intn(61))
}

// armIndent is the indentation of a line extra levels inside the value
// arm being written (a match takes one arm per line).
func (g *sgen) armIndent(extra int) string { return strings.Repeat("    ", g.ind+g.armDepth+extra) }

// matchBlocks is a three-arm match on sel whose arms are the blocks given,
// for 0, 1 and the rest.
func (g *sgen) matchBlocks(b [3]string) string {
	s := g.sel()
	in, out := g.armIndent(1), g.armIndent(0)
	return "match " + s + " {\n" + in + "0 " + b[0] + "\n" + in + "1 " + b[1] + "\n" + in + "_ " + b[2] + "\n" + out + "}"
}

// matchOf is matchBlocks over three values.
func (g *sgen) matchOf(v [3]string) string {
	return g.matchBlocks([3]string{"{ " + v[0] + " }", "{ " + v[1] + " }", "{ " + v[2] + " }"})
}

// armRef is a <ref> that is a value arm (ref's draw in the m7 profiles):
// an if with an else, an else-if chain, a match, or an else value block
// on Find's ?&i64 or Get's Miss!&i64, each arm a <ref> (so arms nest to
// depth 2); its origins are every arm's, the union the lowering's local
// holds.
func (g *sgen) armRef() (string, []origin) {
	g.armDepth++
	defer func() { g.armDepth-- }()
	var org []origin
	one := func() string {
		r, o := g.ref()
		org = append(org, o...)
		return r
	}
	switch g.rng.intn(6) {
	case 0, 1:
		c := g.cond()
		a, b := one(), one()
		return "if " + c + " { " + a + " } else { " + b + " }", org
	case 2:
		c1, c2 := g.cond(), g.cond()
		a, b, c := one(), one(), one()
		return fmt.Sprintf("if %s { %s } else if %s { %s } else { %s }", c1, a, c2, b, c), org
	case 3:
		a, b, c := one(), one(), one()
		return g.matchOf([3]string{a, b, c}), org
	case 4:
		g.use(itemFind)
		ka := g.kArg()
		a, b := one(), one()
		return "Find(" + ka + ", " + a + ") else { " + b + " }", org
	}
	g.use(itemGet)
	ka := g.kArg()
	a, b := one(), one()
	return "Get(" + ka + ", " + a + ") else { " + b + " }", org
}

// refLeaf is a <ref> that is no value arm, for an operand (*r).
func (g *sgen) refLeaf() string {
	d := g.armDepth
	g.armDepth = 2
	r, _ := g.ref()
	g.armDepth = d
	return r
}

// ival is an i64 in value position: a literal, or in the m7 profiles one
// time in four a value arm (if or match) whose arms are literals, int
// locals, reads through a <ref>, or value arms again. Outside the m7
// profiles it is lit with no extra draw, so their text is version 7's.
func (g *sgen) ival() string {
	if !g.armOK() || !g.chance(1, 4) {
		return g.lit()
	}
	g.shapes[sArmVal] = true
	g.armDepth++
	defer func() { g.armDepth-- }()
	leaf := func() string {
		switch g.rng.intn(4) {
		case 0:
			if v := g.pick(kInt, false); v != nil {
				v.used = true
				return v.name
			}
		case 1:
			if g.refable() {
				return "*" + g.refLeaf()
			}
		case 2:
			return g.ival()
		}
		return g.lit()
	}
	if g.rng.intn(3) < 2 {
		c := g.cond()
		a, b := leaf(), leaf()
		return "if " + c + " { " + a + " } else { " + b + " }"
	}
	a, b, c := leaf(), leaf(), leaf()
	return g.matchOf([3]string{a, b, c})
}

// liveArenas are the sub-arenas in scope that a return must free, the
// innermost first (as elseReturn frees them).
func (g *sgen) liveArenas() []string {
	var out []string
	as := g.pools[kArena]
	for i := len(as) - 1; i >= 0; i-- {
		if !as[i].deferred && !as[i].moved {
			out = append(out, as[i].name)
		}
	}
	return out
}

// divArm is the block of an arm that does not fall through: break or
// continue inside a loop when no sub-arena would be left unfreed (a
// deferred one is freed by its defer), else return h after freeing every
// live sub-arena (every arena is freed on every path).
func (g *sgen) divArm() string {
	frees := g.liveArenas()
	if g.loops > 0 && len(frees) == 0 {
		switch g.rng.intn(3) {
		case 1:
			return "{ break }"
		case 2:
			return "{ continue }"
		}
	}
	g.hUsed = true
	if len(frees) == 0 {
		return "{ return h }"
	}
	in := g.armIndent(1)
	var b strings.Builder
	b.WriteString("{\n")
	for _, a := range frees {
		b.WriteString(in + a + ".Free()\n")
	}
	b.WriteString(in + "return h\n" + g.armIndent(0) + "}")
	return b.String()
}

// armDiverge is S55: a value arm one of whose arms does not fall through,
// bound to a new local: an if over an i64 or a <ref>, a match, or an else
// block on Find or Get.
func (g *sgen) armDiverge() bool {
	switch q := g.rng.intn(4); {
	case q == 0 || !g.refable():
		c, v, d := g.cond(), g.ival(), g.divArm()
		arms := "{ " + v + " } else " + d
		if g.chance(1, 2) {
			arms = d + " else { " + v + " }"
		}
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = if %s %s", s.name, c, arms)
	case q == 1:
		c := g.cond()
		r, _ := g.ref()
		d := g.divArm()
		s := g.declare(kRef, "r")
		g.linef("var %s := if %s { %s } else %s", s.name, c, r, d)
	case q == 2:
		fn := "Find"
		if g.chance(1, 2) {
			fn = "Get"
			g.use(itemGet)
		} else {
			g.use(itemFind)
		}
		ka := g.kArg()
		r, _ := g.ref()
		d := g.divArm()
		s := g.declare(kRef, "r")
		g.linef("var %s := %s(%s, %s) else %s", s.name, fn, ka, r, d)
	default:
		a, b := g.ival(), g.ival()
		d := g.divArm()
		bl := [3]string{"{ " + a + " }", d, "{ " + b + " }"}
		if g.chance(1, 2) {
			bl[0], bl[1] = bl[1], bl[0]
		}
		m := g.matchBlocks(bl)
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = %s", s.name, m)
	}
	return true
}

// armBlock is S56: value arms whose blocks hold statements before their
// value (an i64 binding, a <ref> assigned to an existing local, an else
// block on Find), and an else e block on Get's Miss!&i64 that binds the
// error.
func (g *sgen) armBlock() bool {
	in1, in0 := g.armIndent(1), g.armIndent(0)
	stmt := func() string {
		g.hUsed = true
		return in1 + "h = h *% 31 +% " + g.lit() + "\n"
	}
	switch q := g.rng.intn(4); {
	case q == 0 || !g.refable():
		c := g.cond()
		s1, a := stmt(), g.ival()
		b := g.ival()
		s := g.declare(kInt, "v")
		g.linef("var %s: i64 = if %s {\n%s%s%s\n%s} else {\n%s%s\n%s}", s.name, c, s1, in1, a, in0, in1, b, in0)
	case q == 1:
		g.use(itemGet)
		ka := g.kArg()
		a, _ := g.ref()
		b, _ := g.ref()
		e := g.fresh("er")
		s := g.declare(kRef, "r")
		g.linef("var %s := Get(%s, %s) else %s {\n%s_ = %s\n%s%s\n%s}", s.name, ka, a, e, in1, e, in1, b, in0)
	case q == 2:
		t := g.pick(kRef, true)
		if t == nil {
			return false
		}
		c := g.cond()
		s1, a := stmt(), ""
		a, _ = g.ref()
		b, _ := g.ref()
		g.linef("%s = if %s {\n%s%s%s\n%s} else {\n%s%s\n%s}", t.name, c, s1, in1, a, in0, in1, b, in0)
	default:
		g.use(itemFind)
		ka := g.kArg()
		a, _ := g.ref()
		s1 := stmt()
		b, _ := g.ref()
		s := g.declare(kRef, "r")
		g.linef("var %s := Find(%s, %s) else {\n%s%s%s\n%s}", s.name, ka, a, s1, in1, b, in0)
	}
	return true
}

// iterSrc is a []i64 an It walks: a whole array or its middle, or a slice
// local; "" when there is none.
func (g *sgen) iterSrc() string {
	if z := g.pick(kSlice, false); z != nil && g.chance(1, 3) {
		z.used = true
		return z.name
	}
	if xs := g.pick(kArray, false); xs != nil {
		if g.chance(1, 3) {
			return xs.name + "[1..3]"
		}
		return xs.name + "[..]"
	}
	return ""
}

// forIter is S57: for over the header's It with its record payload
// destructured and renamed (i: ip, x: xp) or bound to one name, over
// Slice.Chunks pieces, or over an array of records by pattern, with and
// without an index; the body is nested statements that see the payload's
// names (xp holds the walked array's loan).
func (g *sgen) forIter() bool {
	if g.depth >= g.maxDepth() {
		return false
	}
	q := g.rng.intn(5)
	var src string
	if q < 4 {
		src = g.iterSrc()
		if src == "" {
			q = 4
		}
	}
	g.enterLoop(4)
	switch q {
	case 0, 1:
		g.use(itemItNext)
		i, x := g.fresh("ip"), g.fresh("xp")
		g.linef("for (i: %s, x: %s) in It(xs: %s, i: 0) {", i, x, src)
		g.open()
		g.declareNamed(kInt, i).fixed = true
		g.declareNamed(kRef, x).fixed = true
	case 2:
		g.use(itemItNext)
		p := g.fresh("p")
		g.linef("for %s in It(xs: %s, i: %d) {", p, src, g.rng.intn(2))
		g.open()
		g.fold(p + ".i +% *" + p + ".x")
	case 3:
		c := g.fresh("c")
		if g.wrapped() {
			g.use(itemPieces)
			g.linef("for %s in Pieces(%s, %d) {", c, src, 1+g.rng.intn(3))
		} else {
			g.linef("for %s in %s.Chunks(%d) {", c, src, 1+g.rng.intn(3))
		}
		g.open()
		g.declareNamed(kSlice, c).fixed = true // never empty: the ending's c[0] is in range
	default:
		ps := g.fresh("ps")
		a, b, c, d := g.ival(), g.ival(), g.ival(), g.ival()
		g.linef("var %s: [2](a: i64, b: i64) = [(a: %s, b: %s), (a: %s, b: %s)]", ps, a, b, c, d)
		pa, pb := g.fresh("pa"), g.fresh("pb")
		if g.chance(1, 2) {
			g.linef("for (a: %s, b: %s) in %s {", pa, pb, ps)
			g.open()
		} else {
			ix := g.fresh("ix")
			g.linef("for %s, (a: %s, b: %s) in %s {", ix, pa, pb, ps)
			g.open()
			g.fold("i64(" + ix + ")")
		}
		g.declareNamed(kInt, pa).fixed = true
		g.declareNamed(kInt, pb).fixed = true
	}
	g.body(g.rng.intn(4))
	g.close()
	g.leaveLoop(4)
	g.line("}")
	return true
}

// intArg is an i64 argument: k, an int local or a literal.
func (g *sgen) intArg() string {
	switch g.rng.intn(3) {
	case 0:
		return "k"
	case 1:
		if v := g.pick(kInt, false); v != nil {
			v.used = true
			return v.name
		}
	}
	return g.lit()
}

// indexRecv is S58: Ct.Add (mut self) on an element of a [2]Ct
// through an index (a literal, a range variable, or through a mut slice of
// it), or a reference to an element's field kept as a <ref>: all indices
// of one array are one place, so an Add while the
// reference lives is E0501 whatever the indices.
func (g *sgen) indexRecv() bool {
	cs := g.pick(kCts, true)
	if cs == nil || g.chance(1, 3) {
		a, b := g.ival(), g.ival()
		g.use(itemCt)
		cs = g.declare(kCts, "cs")
		g.linef("var %s: [2]Ct = [Ct(n: %s), Ct(n: %s)]", cs.name, a, b)
	}
	g.use(itemCtAdd)
	switch g.rng.intn(5) {
	case 0, 1:
		g.linef("%s[%d].Add(%s)", cs.name, g.rng.intn(2), g.ival())
	case 2:
		ix := g.fresh("ix")
		g.linef("for %s in 0..2 {", ix)
		g.linef("    %s[%s].Add(%s)", cs.name, ix, g.intArg())
		g.line("}")
	case 3:
		z := g.fresh("zc")
		v := g.ival()
		g.linef("var %s := mut %s[..]", z, cs.name)
		g.linef("%s[%d].Add(%s)", z, g.rng.intn(2), v)
	default:
		r := g.declare(kRef, "r")
		g.linef("var %s := &%s[%d].n", r.name, cs.name, g.rng.intn(2))
	}
	return true
}

// swapRepl is S59: in Run, Swap of two int locals, two <ref>
// locals or two mut names, and Replace of an int or a <ref> local bound to
// a new one; in a helper, Swap of two mut or two mut-reference parameters
// and Replace through one, with the stores their clauses state (Swap's *a
// into b, *b into a; Replace's x into dst).
func (g *sgen) swapRepl(inRun bool) bool {
	if !inRun {
		return g.swapReplHelper()
	}
	switch g.rng.intn(5) {
	case 0:
		a, b := g.pick(kInt, true), g.pick(kInt, true)
		if a == nil {
			return false
		}
		g.linef("Swap(mut %s, mut %s)", a.name, b.name)
	case 1:
		a, b := g.pick(kRef, true), g.pick(kRef, true)
		if a == nil {
			return false
		}
		g.linef("Swap(mut %s, mut %s)", a.name, b.name)
	case 2:
		v := g.pick(kInt, true)
		if v == nil {
			return false
		}
		x := g.ival()
		s := g.declare(kInt, "v")
		g.linef("var %s := Replace(mut %s, %s)", s.name, v.name, x)
	case 3:
		r := g.pick(kRef, true)
		if r == nil || !g.refable() {
			return false
		}
		x, _ := g.ref()
		s := g.declare(kRef, "r")
		g.linef("var %s := Replace(mut %s, %s)", s.name, r.name, x)
	default:
		a, b := g.pick(kMut, false), g.pick(kMut, false)
		if a == nil {
			return false
		}
		a.used, b.used = true, true
		g.linef("Swap(mut *%s, mut *%s)", a.name, b.name)
	}
	return true
}

// twoParams returns two distinct parameters of kind k of the helper, or
// nil.
func (g *sgen) twoParams(k kind) (*sname, *sname) {
	var ps []*sname
	for _, p := range g.cur.params {
		if p.kind == k {
			ps = append(ps, p)
		}
	}
	if len(ps) < 2 {
		return nil, nil
	}
	i := g.rng.intn(len(ps))
	j := (i + 1 + g.rng.intn(len(ps)-1)) % len(ps)
	return ps[i], ps[j]
}

func (g *sgen) swapReplHelper() bool {
	switch g.rng.intn(4) {
	case 0:
		a, b := g.twoParams(kMut)
		if a == nil {
			return false
		}
		a.used, b.used = true, true
		g.linef("Swap(%s, %s)", a.name, b.name)
	case 1:
		a, b := g.twoParams(kRefRef)
		if a == nil {
			return false
		}
		a.used, b.used = true, true
		g.store([]origin{{a, true}}, b)
		g.store([]origin{{b, true}}, a)
		g.linef("Swap(%s, %s)", a.name, b.name)
	case 2:
		q := g.pick(kRefRef, false)
		if q == nil || !g.refable() {
			return false
		}
		q.used = true
		x, org := g.ref()
		g.store(org, q)
		g.fold("*Replace(" + q.name + ", " + x + ")")
	default:
		m := g.pick(kMut, false)
		if m == nil {
			return false
		}
		m.used = true
		g.fold("Replace(" + m.name + ", " + g.ival() + ")")
	}
	return true
}

// splitPair is S60: SplitAt of a var array into two mut views,
// which join the mut slice pool (helpers, Sort, CopyTo and the ending use
// them), or GetPair of two elements, in an if (a block that sees both mut
// names) or bound with an else that returns.
func (g *sgen) splitPair() bool {
	xs := g.pick(kArray, true)
	if xs == nil {
		a, b, c, d := g.ival(), g.ival(), g.ival(), g.ival()
		xs = g.declare(kArray, "xs")
		g.linef("var %s: [4]i64 = [%s, %s, %s, %s]", xs.name, a, b, c, d)
	}
	switch q := g.rng.intn(3); {
	case q == 0:
		i := 1 + g.rng.intn(3)
		l, r := g.fresh("lf"), g.fresh("rt")
		if g.wrapped() {
			g.use(itemHalves)
			g.linef("(left: %s, right: %s) := Halves(mut %s[..], %d)", l, r, xs.name, i)
		} else {
			g.linef("(left: %s, right: %s) := %s.SplitAt(%d)", l, r, xs.name, i)
		}
		L, R := g.declareNamed(kMSlice, l), g.declareNamed(kMSlice, r)
		L.n, R.n, L.fixed, R.fixed = i, 4-i, true, true
		if g.chance(1, 2) {
			L.used = true
			g.linef("%s[0] = %s", l, g.ival())
		}
		if g.chance(1, 2) {
			R.used = true
			g.linef("%s[%d] = %s", r, g.rng.intn(R.n), g.ival())
		}
	case q == 1 && g.depth < g.maxDepth():
		p := g.fresh("pr")
		g.linef("if %s := %s {", p, g.getPair(xs.name, g.rng.intn(4), g.rng.intn(4)))
		g.open()
		a, b := g.declareNamed(kMut, p+".a"), g.declareNamed(kMut, p+".b")
		a.fixed, b.fixed, a.used = true, true, true
		g.linef("*%s.a = *%s.a +%% %s", p, p, g.intArg())
		if g.chance(1, 3) {
			b.used = true
			g.linef("Swap(mut *%s.a, mut *%s.b)", p, p)
		}
		g.body(g.rng.intn(3))
		g.close()
		g.line("}")
	default:
		i := g.rng.intn(4)
		j := (i + 1 + g.rng.intn(3)) % 4
		p := g.fresh("pr")
		g.elseReturn(fmt.Sprintf("%s := %s", p, g.getPair(xs.name, i, j)))
		a, b := g.declareNamed(kMut, p+".a"), g.declareNamed(kMut, p+".b")
		a.fixed, b.fixed, a.used = true, true, true
		g.linef("*%s.a = *%s.b +%% %s", p, p, g.intArg())
	}
	return true
}

// getPair is GetPair of elements i and j of the var array xs, through the
// header's Pair in reject7.
func (g *sgen) getPair(xs string, i, j int) string {
	if g.wrapped() {
		g.use(itemPair)
		return fmt.Sprintf("Pair(mut %s[..], %d, %d)", xs, i, j)
	}
	return fmt.Sprintf("%s.GetPair(%d, %d)", xs, i, j)
}

// chunksScope is S61 (the cookbook's parallel loop): a scope whose
// tasks each write one ChunksMut piece of a var array or a mut slice view
// of known length through Fill; the pieces are counted as spawns through
// the loops around them (the host's bound). One time in six the scope also
// reads the array before it joins (E0502).
func (g *sgen) chunksScope() bool {
	if g.spawns >= 8 {
		return false
	}
	var src string
	n := 4
	xs := g.pick(kArray, true)
	z := g.pick(kMSlice, false)
	if z != nil && z.n > 0 && (xs == nil || g.chance(1, 3)) {
		src, n = z.name, z.n
	} else if xs != nil {
		src, z = xs.name, nil
	} else {
		return false
	}
	w := 1 + g.rng.intn(4)
	pieces := (n + w - 1) / w
	if g.dyn+pieces*g.mult > maxRunSpawns {
		return false // nothing is marked read before here (else E0208)
	}
	if z != nil {
		z.used = true
	}
	g.usesIO = true
	g.use(itemFill)
	s, c := g.fresh("s"), g.fresh("c")
	g.linef("scope %s := io.Scope() {", s)
	if g.wrapped() {
		g.use(itemPiecesMut)
		arg := src
		if z == nil {
			arg = "mut " + src + "[..]"
		}
		g.linef("    for %s in PiecesMut(%s, %d) {", c, arg, w)
	} else {
		g.linef("    for %s in %s.ChunksMut(%d) {", c, src, w)
	}
	g.linef("        %s.go Fill(%s, %s)", s, c, g.intArg())
	g.line("    }")
	if xs != nil && src == xs.name && g.chance(1, 6) {
		g.hUsed = true
		g.linef("    h = h *%% 31 +%% %s[0]", xs.name)
	}
	g.line("}")
	g.enterLoop(pieces)
	g.spawned()
	g.leaveLoop(pieces)
	return true
}

// poolPair is S62: a mem.Pool[i64] over the entry's arena or a live
// sub-arena (the pool holds its loan), and two handles' values taken
// mut together by GetPair (none when they are one handle), the live values
// walked by Iter with the payload destructured, or a value added and
// freed.
func (g *sgen) poolPair() bool {
	g.usesA, g.mem, g.hUsed = true, true, true
	p := g.pick(kPool, true)
	if p == nil || g.chance(1, 4) {
		arena := "a"
		if b := g.pick(kArena, false); b != nil && g.chance(1, 3) {
			arena = "&" + b.name
		}
		p = g.declare(kPool, "pl")
		g.linef("var %s := mem.Pool[i64].New(%s)", p.name, arena)
	}
	ha, hb := g.fresh("g"), g.fresh("g")
	switch g.rng.intn(3) {
	case 0:
		g.elseReturn(fmt.Sprintf("%s := %s.Add(%s)", ha, p.name, g.ival()))
		g.elseReturn(fmt.Sprintf("%s := %s.Add(%s)", hb, p.name, g.ival()))
		second := hb
		if g.chance(1, 6) {
			second = ha
		}
		pr := g.fresh("pr")
		g.linef("if %s := %s.GetPair(%s, %s) {", pr, p.name, ha, second)
		g.linef("    *%s.a = *%s.a +%% %s", pr, pr, g.intArg())
		if g.chance(1, 2) {
			g.linef("    Swap(mut *%s.a, mut *%s.b)", pr, pr)
		}
		g.line("}")
		if second == ha {
			g.linef("_ = %s.Free(%s)", p.name, hb)
		}
	case 1:
		hd, vl := g.fresh("hd"), g.fresh("vl")
		g.linef("for (handle: %s, value: %s) in %s.Iter() {", hd, vl, p.name)
		g.linef("    _ = %s", hd)
		g.linef("    h = h *%% 31 +%% *%s", vl)
		g.line("}")
	default:
		g.elseReturn(fmt.Sprintf("%s := %s.Add(%s)", ha, p.name, g.ival()))
		g.linef("_ = %s.Free(%s)", p.name, ha)
	}
	return true
}
