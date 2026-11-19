// Why: some mutant rows guard holes the per-field stratum never draws in a
// body of its own, so a mutant run could not show the row is needed. These
// shapes draw each such hole directly.
//
// What: the hole strata S82 to S88, profile fields only: c3Hole (S82 to
// S84), selfFwd (S85), fpCross (S86), shallowHole with kkSetups (S87) and
// globalCall (S88), dispatched by holeShape.
//
// How: each shape writes one small generated function (three for S85) and
// a call to it from Run. One time in holeOdds the function holds the hole:
// the witness's form, which the tree rejects and the row's mutant accepts,
// so a run of that mutant reads a dead local. Otherwise the hole's loan is
// replaced by one that lives long enough. Holes sit in functions of their
// own because a body that may reach the self-reach remnant is rechecked
// with one set per root, which Run's other shapes would trip; these
// functions split nothing and make no such call, so the row's rule alone
// decides them.
//
// S82 to S84 are later-hop stores into an allocator, which the later-hop
// allocator rule records in the allocator itself: a List in the arena in front of
// its cell (D612-d462-d7, M-C3-subarena and M-C3-later), a cell of a second
// arena that holds the first arena's cell (FH-d462-r1c and r1d,
// M-C3-content), and a local cell in front of that one (FH-d462-r1e,
// M-C3-deep). Arena.Sub is an independent arena on the same heap, so b.Sub()
// is r1d's Arena.New(heap). S85 is self-reach through a forwarding
// parameter (FH-selfreach-r3, M-D694): the header's Fs and Fh, a callee
// that stores x into l and writes x.b through x.a, a forwarder, and the
// caller whose local x.b borrows. S86 is W2 (M-F8-W2-ret-union): a
// per-field helper that returns its fields crossed, called by a function of
// its own that keeps the field its clause says comes from its parameter.
// S87 is shallow stores (the w-* witnesses of
// tests/soundness/witness/fld3/, with &i64 for str): a holder Kk that gets
// a cell's loan through one of seven forms (direct, a carrier's field, a
// shallow store then a deep one of the same loan, the two on two paths, a
// clause through a middle parameter, `*q into k`, a deep store then a
// shallow one), then a later store through it.
//
// S88 is a global laundered through a call result: a function, a fn
// literal value or a fn type whose result names a local returns S31's
// global mutex, and a local's loan is stored through it (M-D756-result,
// M-D756-indirect); or a function whose result is from its parameter
// returns the global, which its caller then stores a local's loan through
// (M-D756-boundary).
//
// Not drawn: the self-reach call rule (M-CSR), whose only remaining form is
// a callee no body judges, an unsafe or extern one, and the generator
// writes no unsafe (CSR-unsafe-u1 and CSR-extern-e1 carry the row); the
// shallow-store rule's class P rows; and split views (the M-P4-view rows),
// whose witnesses all keep a view's loans in raw storage, which only unsafe
// code writes. M-F3-shallow-add-lowers's order is drawn, but its mutant is
// pending, so no run shows its teeth.

package corpus

import "fmt"

// holeCap is how many functions one hole shape writes in a program.
const holeCap = 2

// holeOdds: a hole function writes its hole one time in holeOdds. Each hole
// is rejected by the tree and takes its whole program with it, so this and
// holeWeight (each hole shape's weight in profile fields, shapeBase) set
// what the strata cost the other shapes' accepted programs.
const (
	holeOdds   = 3
	holeWeight = 2
)

// holeDraw draws whether a hole function writes its hole.
func (g *sgen) holeDraw() bool { return g.chance(1, holeOdds) }

// holeName is the next generated name with prefix p (Cl1, Sp2, ...).
func (g *sgen) holeName(p string) string {
	if g.holeN == nil {
		g.holeN = map[string]int{}
	}
	g.holeN[p]++
	return fmt.Sprintf("%s%d", p, g.holeN[p])
}

// holeFull reports whether shape prefix p has written holeCap functions.
func (g *sgen) holeFull(p string) bool { return g.holeN[p] >= holeCap }

// holeShape writes hole shape id (S82 to S88) when it is ready.
func (g *sgen) holeShape(id int) bool {
	switch id {
	case sC3List:
		return g.c3Hole("Cl", 0)
	case sC3Nest:
		return g.c3Hole("Cn", 1)
	case sC3Front:
		return g.c3Hole("Cf", 2)
	case sSelfFwd:
		return g.selfFwd()
	case sFpCross:
		return g.fpCross()
	case sShallow:
		return g.shallowHole()
	case sGlobalCall:
		return g.globalCall()
	}
	return false
}

// holeLoan is the loan a hole function stores: its local z's (the hole,
// holeDraw), else its parameter r, with z read so it is used.
func (g *sgen) holeLoan() (loan, after string, hole bool) {
	if g.holeDraw() {
		return "&z", "", true
	}
	return "r", "    _ = z\n", false
}

// c3Hole is S82 (form 0), S83 (1) and S84 (2): a function Xn(b: &Arena,
// r: &i64 into b) that puts a cell c holding r in b, reaches it again
// through a List in b (0), a cell of a second arena (1) or a local cell in
// front of that one (2), stores through that holder and returns &*c; Run
// passes a sub-arena, a new one or (one time in three) one in scope, and
// reads the cell at once.
func (g *sgen) c3Hole(prefix string, form int) bool {
	if g.holeFull(prefix) || !g.refable() {
		return false
	}
	name := g.holeName(prefix)
	loan, after, _ := g.holeLoan()
	z := g.lit()
	var body string
	switch form {
	case 0:
		body = "    var l := List[&Cell[&i64]].New(b)\n" +
			"    try l.Push(&*c)\n" +
			"    z: i64 = " + z + "\n" +
			"    StashL(&l, " + loan + ")\n" + after
		g.use(itemStashL)
	default:
		body = "    z: i64 = " + z + "\n" +
			"    var ar := b.Sub() else {\n        return &*c\n    }\n" +
			"    hc := ar.Put(Cell[?&Cell[&i64]].New(none)) else {\n        ar.Free()\n        return &*c\n    }\n" +
			"    hc.Store(some(&*c))\n"
		if form == 1 {
			body += "    StashC2(&*hc, " + loan + ")\n" + after
			g.use(itemStashC2)
		} else {
			body += "    top := Cell[?&Cell[?&Cell[&i64]]].New(some(&*hc))\n" +
				"    StashC3(&top, " + loan + ")\n" + after
			g.use(itemStashC3)
		}
		body += "    ar.Free()\n"
	}
	g.hgen = append(g.hgen, fmt.Sprintf("%s(b: &Arena, r: &i64 into b): OutOfMemory!&Cell[&i64] from b {\n"+
		"    c := try b.Put(Cell.New(r))\n%s    return &*c\n}\n", name, body))
	b := g.pick(kArena, false)
	if b == nil || g.chance(2, 3) {
		g.usesA = true
		bn := g.fresh("b")
		g.elseReturn(fmt.Sprintf("var %s := a.Sub()", bn))
		b = g.declareNamed(kArena, bn)
		b.fixed = true
	}
	b.used = true
	r, _ := g.ref()
	w := g.fresh("w")
	g.elseReturn(fmt.Sprintf("var %s := %s(&%s, %s)", w, name, b.name, r))
	g.fold("*" + w + ".Load()")
	return true
}

// selfFwd is S85 (witness FH-selfreach-r3): Spn(l: mut Fh, x: Fs into l)
// stores x into l and, in the hole, writes x.b through x.a (E0604 x into x);
// Son forwards its by-value x to it with a holder of its own; Sin(p: mut
// &i64) passes Fs(a: p, b: &n), n its local, as a literal or a local. Run
// passes a reference var of its own (a new one when none is free) and
// reads it at once: in the hole p's referent then borrows the dead n.
func (g *sgen) selfFwd() bool {
	if g.holeFull("Sp") {
		return false
	}
	var cand []*sname
	for _, s := range g.pools[kRef] {
		if !s.fixed && !s.moved && !s.inQ {
			cand = append(cand, s)
		}
	}
	var r *sname
	if len(cand) > 0 {
		r = cand[g.rng.intn(len(cand))]
	} else {
		if !g.refable() {
			return false
		}
		e, _ := g.ref()
		r = g.declare(kRef, "r")
		g.linef("var %s := %s", r.name, e)
	}
	sp, so, si := g.holeName("Sp"), g.holeName("So"), g.holeName("Si")
	write := "    _ = v\n"
	if g.holeDraw() {
		write = "    *l.s.a = v\n"
	}
	pass := fmt.Sprintf("    %s(Fs(a: p, b: &n))\n", so)
	if g.chance(1, 2) {
		pass = fmt.Sprintf("    var x := Fs(a: p, b: &n)\n    %s(x)\n", so)
	}
	g.use(itemFh)
	g.hgen = append(g.hgen,
		fmt.Sprintf("%s(l: mut Fh, x: Fs into l) {\n    v := x.b\n    l.s = x\n%s}\n", sp, write),
		fmt.Sprintf("%s(x: Fs) {\n    d: i64 = %s\n    var dr: &i64 = &d\n    var l := Fh(s: Fs(a: mut dr, b: &d))\n    %s(mut l, x)\n}\n", so, g.lit(), sp),
		fmt.Sprintf("%s(p: mut &i64) {\n    n: i64 = %s\n%s}\n", si, g.lit(), pass))
	r.used = true
	g.linef("%s(mut %s)", si, r.name)
	g.fold("*" + r.name)
	return true
}

// fpCross is S86 (witness W2): Gxn(x, y): Fp with a per-field clause that names
// x for one field f and y for the other, returning a literal with the two
// crossed in the hole (E0602) or as its clause says; Gcn(r: &i64): &i64 from r
// calls it with its own local's loan as y, by a destructuring or a local, reads
// the other field and returns f. Run reads the result at once.
func (g *sgen) fpCross() bool {
	if g.holeFull("Gx") || !g.refable() {
		return false
	}
	gx, gc := g.holeName("Gx"), g.holeName("Gc")
	f, o := "a", "b"
	if g.chance(1, 2) {
		f, o = o, f
	}
	vf, vo := "x", "y"
	if g.holeDraw() {
		vf, vo = vo, vf // the hole: f gets y's loan
	}
	lit := map[string]string{f: vf, o: vo}
	clause := map[string]string{f: "x", o: "y"}
	var call string
	if g.chance(1, 2) {
		dst := map[string]string{f: "u", o: "w"}
		call = fmt.Sprintf("    Fp(a: %s, b: %s) := %s(r, &y)\n    _ = *w\n    return u\n", dst["a"], dst["b"], gx)
	} else {
		call = fmt.Sprintf("    t := %s(r, &y)\n    _ = *t.%s\n    return t.%s\n", gx, o, f)
	}
	g.use(itemFp)
	g.hgen = append(g.hgen,
		fmt.Sprintf("%s(x: &i64, y: &i64): Fp from (a: %s, b: %s) {\n    return Fp(a: %s, b: %s)\n}\n", gx, clause["a"], clause["b"], lit["a"], lit["b"]),
		fmt.Sprintf("%s(r: &i64): &i64 from r {\n    y: i64 = %s\n%s}\n", gc, g.lit(), call))
	r, _ := g.ref()
	g.fold("*" + gc + "(" + r + ")")
	return true
}

// kkSetups are S87's ways to put the cell's loan into k: the w-* witnesses
// of tests/soundness/witness/fld3/ (w-direct, w-carrier, w-deepadd, w-join,
// w-mid, w-starchain, w-shallowadd), each its row's
// (M-F3-nonshallow-referent, M-F3-carrier-referent,
// M-F3-deep-add-keeps-shallow, M-F3-join-keeps-shallow,
// M-F3-mark-from-middle, M-F3-star-chain, M-F3-shallow-add-lowers). The
// last row is pending (its patch makes the solve not monotone), so no
// mutant run shows w-shallowadd's teeth, but the long run draws its order: the
// deep store first, then the shallow one.
var kkSetups = []struct {
	text  string
	items []int
}{
	{"    KkCell(mut k, &cell)\n", []int{itemKkCell}},
	{"    x := Ks(name: r, slot: &cell)\n    KkSlot(mut k, &x)\n", []int{itemKkSlot}},
	{"    c := &cell\n    KkName(mut k, KkNm(c))\n    KkCell(mut k, c)\n", []int{itemKkName, itemKkNm, itemKkCell}},
	{"    c := &cell\n    if *r != *r {\n        KkName(mut k, KkNm(c))\n    } else {\n        KkCell(mut k, c)\n    }\n", []int{itemKkName, itemKkNm, itemKkCell}},
	{"    KkMid(mut k, &cell, r)\n", []int{itemKkMid}},
	{"    nm := Kn(s: r, k: 1)\n    KkStar(mut k, &nm, &cell)\n", []int{itemKkStar}},
	{"    c := &cell\n    KkCell(mut k, c)\n    KkName(mut k, KkNm(c))\n", []int{itemKkCell, itemKkName, itemKkNm}},
}

// shallowHole is S87 (shallow stores): Kfn(r: &i64): i64 puts a local cell
// holding r into a holder k by one of kkSetups, then in a block KkPoke stores
// its block-local's loan (the hole) or r through k, and returns what the cell
// holds; the hole's runs read the dead local there. Run folds the result.
func (g *sgen) shallowHole() bool {
	if g.holeFull("Kf") || !g.refable() {
		return false
	}
	kf := g.holeName("Kf")
	s := kkSetups[g.rng.intn(len(kkSetups))]
	for _, it := range s.items {
		g.use(it)
	}
	g.use(itemKkPoke)
	g.use(itemYes)
	poke := "        KkPoke(mut k, r)\n        _ = y\n"
	if g.holeDraw() {
		poke = "        KkPoke(mut k, &y)\n"
	}
	g.hgen = append(g.hgen, fmt.Sprintf("%s(r: &i64): i64 {\n    cell := Cell.New(r)\n    var k := Kk(c: none, n: none)\n%s"+
		"    if Yes() {\n        y: i64 = %s\n%s    }\n    return *cell.Load()\n}\n", kf, s.text, g.lit(), poke))
	r, _ := g.ref()
	g.fold(kf + "(" + r + ")")
	return true
}

// globalCall is S88 (a global laundered through a call result, the forms
// G756-b27a, G756-lit, G756-fnty and G756-src): Gsn(io: Io): i64 reaches S31's
// global mutex GM through a call, locks it and stores a loan through it. The
// call is one of four, each one time in four: Gmn() or a fn literal value with
// no captures (M-D756-result), or Gyn(x: &i64), whose result names no source,
// held as a fn type whose result is from x and called with a local declared
// after n (M-D756-indirect); in these three Gsn stores its local n's loan (the
// hole, E0605, one time in holeOdds) or a static's. The fourth is Gbn(d:
// &Mutex[&i64]), called with a local Mutex declared after n, which returns &GM
// (the hole, E0602 at its return, M-D756-boundary, one time in holeOdds) or d;
// Gsn always stores &n into it. Grn reads GM's reference after Gsn returned.
// Run calls both and folds them.
func (g *sgen) globalCall() bool {
	if g.holeFull("Gs") {
		return false
	}
	g.usesIO = true
	if g.global == "" {
		k := g.static()
		g.global = "GM"
		g.consts = append(g.consts, fmt.Sprintf("var GM: Mutex[&i64] = Mutex.New(&%s)", k))
	}
	gs, gr := g.holeName("Gs"), g.holeName("Gr")
	// pre comes before Gsn's local n, post after it; val is what Gsn
	// stores, &n in the hole.
	pre, post, val, bnd := "", "", "&n", false
	switch g.rng.intn(4) {
	case 0: // G756-b27a: a function with no source returns &GM
		gm := g.holeName("Gm")
		g.hgen = append(g.hgen, fmt.Sprintf("%s(): &Mutex[&i64] {\n    return &GM\n}\n", gm))
		pre = fmt.Sprintf("    m := %s()\n", gm)
	case 1: // G756-lit: a fn literal value with no captures
		pre = "    f: fn(): &Mutex[&i64] = fn(): &Mutex[&i64] { return &GM }\n    m := f()\n"
	case 2: // G756-fnty: a function with no source behind a fn type whose result is from x, x a local after n
		gy := g.holeName("Gy")
		g.hgen = append(g.hgen, fmt.Sprintf("%s(x: &i64): &Mutex[&i64] {\n    _ = *x\n    return &GM\n}\n", gy))
		post = fmt.Sprintf("    z: i64 = %s\n    f: fn(x: &i64): &Mutex[&i64] from x = %s\n    m := f(&z)\n", g.lit(), gy)
	default: // G756-src: a result from d returns &GM in the hole (E0602 there), else d; Gsn always stores &n into it
		gb := g.holeName("Gb")
		bnd = true
		ret := "d"
		if g.holeDraw() {
			ret = "&GM"
		}
		g.hgen = append(g.hgen, fmt.Sprintf("%s(d: &Mutex[&i64]): &Mutex[&i64] {\n    return %s\n}\n", gb, ret))
		post = fmt.Sprintf("    var lm := Mutex[&i64].New(&%s)\n    m := %s(&lm)\n", g.statics[g.rng.intn(len(g.statics))], gb)
	}
	if !bnd && !g.holeDraw() {
		val = "&" + g.statics[g.rng.intn(len(g.statics))]
	}
	g.hgen = append(g.hgen,
		fmt.Sprintf("%s(io: Io): i64 {\n%s    n: i64 = %s\n%s    var gd := m.Lock(io) else {\n        return 0\n    }\n"+
			"    gd.Store(%s)\n    gd.Unlock()\n    return n\n}\n", gs, pre, g.lit(), post, val),
		fmt.Sprintf("%s(io: Io): i64 {\n    var gd := GM.Lock(io) else {\n        return 0\n    }\n    v := **gd.Ref()\n    gd.Unlock()\n    return v\n}\n", gr))
	g.fold(gs + "(io)")
	g.fold(gr + "(io)")
	return true
}
