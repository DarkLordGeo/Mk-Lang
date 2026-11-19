// Why: field-level loan sets track each field of a local struct apart, which
// opens new ways for the borrow checker to be wrong; the soundness generator
// needs shapes that aim at exactly those rules.
//
// What: the per-field stratum, shapes S70 to S81 of the fields and rejectf
// profiles: fpNew, fpWrite, fpRead, fpWhole, fpJoin, fpLoop, faHole, fhHole,
// fqDeep, fwMix, frMerge and fpGen, dispatched by fieldShape and weighted by
// fieldWeight. The header items (Fp to FrSet, and StashL to KkPoke for S82
// to S88) are in soundheader.go.
//
// How: each shape splits a local, uses it field by field or whole (a mut
// borrow, a mut self call, &self on a type with a Cell inline, a store
// through a whole loan), passes per-field results across calls, joins paths
// that wrote different fields, or draws the arena-cell and self-reach hole
// shapes. S78 to S81 aim at rules the first shapes rarely reached under
// mutant runs. Self-reach through a forwarding parameter is S85, in
// soundholes.go.

package corpus

import (
	"fmt"
	"regexp"
)

// fieldsProfile reports whether profile p draws the stratum: fields (m7's
// shapes and the stratum) and rejectf (reject7's and the stratum).
func fieldsProfile(p Profile) bool { return p == ProfileFields || p == ProfileRejectF }

// fieldWeight is shape id's weight (id at least S70) in profile p. The
// hole strata S82 to S88 (soundholes.go) are fields' only, so rejectf's
// programs keep their GenVersion 12 text but for the version line.
func fieldWeight(id int, p Profile, w int) int {
	if !fieldsProfile(p) || id >= sC3List && p != ProfileFields {
		return 0
	}
	return w
}

// fieldsEnding is the fold of a stratum local at its block's end: every
// field, and through the holder's cell, so a dead loan any field kept is
// read (the dynamic checker sees only the dead read).
func fieldsEnding(s *sname) string {
	n := s.name
	switch s.kind {
	case kFp:
		return fmt.Sprintf("*%[1]s.a +%% *%[1]s.b", n)
	case kFq:
		return fmt.Sprintf("*%[1]s.p.a +%% *%[1]s.p.b +%% *%[1]s.c", n)
	case kFw:
		return fmt.Sprintf("*%[1]s.a +%% *%[1]s.c.Load()", n)
	case kFa:
		return fmt.Sprintf("*%[1]s.c.Load() +%% *%[1]s.s", n)
	}
	return fmt.Sprintf("*%s.s.b", n) // kFh
}

// fieldShape writes stratum shape id when it is ready. Every shape is Run's.
func (g *sgen) fieldShape(id int, inRun bool) bool {
	if !inRun {
		return false
	}
	switch id {
	case sFpNew:
		return g.fpNew()
	case sFpWrite:
		return g.fpWrite()
	case sFpRead:
		return g.fpRead()
	case sFpWhole:
		return g.fpWhole()
	case sFpJoin:
		return g.fpJoin()
	case sFpLoop:
		return g.fpLoop()
	case sFaHole:
		return g.faHole()
	case sFhHole:
		return g.fhHole()
	case sFqDeep:
		return g.fqDeep()
	case sFwMix:
		return g.fwMix()
	case sFrMerge:
		return g.frMerge()
	case sFpGen:
		return g.fpGen()
	}
	return g.holeShape(id)
}

// fpBlock opens a branch block with an int local: the block-local loan a
// targeted shape stores (S78 to S80), or, one time in two, a long-lived
// <ref> drawn before the block in its place, so the shape also writes
// accepted programs. It returns the reference to store; the caller writes
// the body and calls g.close() and g.line("}").
func (g *sgen) fpBlock(long string) string {
	g.linef("if %s {", g.branch())
	g.open()
	y := g.declare(kInt, "v")
	g.linef("var %s: i64 = %s", y.name, g.lit())
	if g.chance(1, 2) {
		return long
	}
	return "&" + y.name
}

// fqDeep is S78 (a deep write p.f.g = e adds to f's set): in a block, a
// block-local's loan written into one sub-field of an Fq's p, then a long-lived
// one into the other sub-field; M-F8-S2c-strong-deep makes the second replace
// p's set, so the first is lost.
func (g *sgen) fqDeep() bool {
	n := g.pick(kFq, true)
	if n == nil || g.depth >= g.maxDepth() || !g.refable() {
		return false
	}
	n.used = true
	long, _ := g.ref()
	long2, _ := g.ref()
	v := g.fpBlock(long)
	f1, f2 := "a", "b"
	if g.chance(1, 2) {
		f1, f2 = f2, f1
	}
	g.linef("%s.p.%s = %s", n.name, f1, v)
	g.linef("%s.p.%s = %s", n.name, f2, long2)
	g.close()
	g.line("}")
	return true
}

// fwMix is S79 (a type with a Cell inline is never split past &w): in a
// block, w.a takes a block-local's loan, FwMix(&w) moves it into w's inline
// Cell with no into clause, and w.a is rewritten; w.c's
// read at the ending must see the loan.
func (g *sgen) fwMix() bool {
	w := g.pick(kFw, false)
	if w == nil || g.depth >= g.maxDepth() || !g.refable() {
		return false
	}
	w.used = true
	long, _ := g.ref()
	long2, _ := g.ref()
	v := g.fpBlock(long)
	g.linef("%s.a = %s", w.name, v)
	g.use(itemFwMix)
	g.linef("FwMix(&%s)", w.name)
	g.linef("%s.a = %s", w.name, long2)
	g.close()
	g.line("}")
	return true
}

// frMerge is S80 (a store that knows no field of a split root lands in
// every field): in a block, an Fr holds mut borrows of both fields of an
// Fp, and a reference is stored through its q, directly or by FrSet.
func (g *sgen) frMerge() bool {
	t := g.pick(kFp, true)
	if t == nil || g.depth >= g.maxDepth() || !g.refable() {
		return false
	}
	t.used = true
	long, _ := g.ref()
	long2, _ := g.ref()
	v := g.fpBlock(long)
	w := g.fresh("fr")
	g.use(itemFr)
	g.linef("%s := Fr(p: mut %s.a, q: mut %s.b)", w, t.name, t.name)
	if g.chance(1, 2) {
		g.linef("*%s.q = %s", w, v)
	} else {
		g.use(itemFrSet)
		g.linef("FrSet(%s, mut %s)", v, w)
	}
	// Field a rewritten after Fr's last use: only b may still hold the
	// block-local's loan, which the store put there through q.
	g.linef("%s.a = %s", t.name, long2)
	g.close()
	g.line("}")
	return true
}

// fpGen is S81 (per-field result sources at the callee): a generated helper
// FgN(x, y): Fp with a per-field clause and a body that builds its result field
// by field (a literal, a split local, a per-field call, a destructuring); each
// field's clause names the parameter the body puts there, both, or (one time in
// four per field) the other one only, which is E0602 at the return. Its result
// is bound to a new local or written over an Fp.
func (g *sgen) fpGen() bool {
	if !g.refable() || len(g.fgen) >= 3 {
		return false
	}
	name := fmt.Sprintf("Fg%d", len(g.fgen)+1)
	p := [2]string{"x", "y"}
	ea, eb := p[g.rng.intn(2)], p[g.rng.intn(2)]
	clause := func(e string) string {
		switch q := g.rng.intn(8); {
		case q < 2:
			if e == "x" {
				return "y"
			}
			return "x"
		case q < 3:
			return "x, y"
		}
		return e
	}
	sa, sb := clause(ea), clause(eb)
	var body string
	switch g.rng.intn(4) {
	case 0:
		body = fmt.Sprintf("    return Fp(a: %s, b: %s)\n", ea, eb)
	case 1:
		body = fmt.Sprintf("    var t := Fp(a: %s, b: %s)\n    t.b = %s\n    return t\n", ea, ea, eb)
	case 2:
		g.use(itemFpMk)
		body = fmt.Sprintf("    return FpMk(%s, %s)\n", ea, eb)
	default:
		g.use(itemFpMk)
		body = fmt.Sprintf("    Fp(a: u, b: w) := FpMk(%s, %s)\n    return Fp(a: u, b: w)\n", ea, eb)
	}
	g.use(itemFp)
	g.fgen = append(g.fgen, fmt.Sprintf("%s(x: &i64, y: &i64): Fp from (a: %s, b: %s) {\n    _ = *x +%% *y\n%s}\n", name, sa, sb, body))
	r1, r2 := g.fpPair()
	if t := g.pick(kFp, true); t != nil && g.chance(1, 2) {
		t.used = true
		g.linef("%s = %s(%s, %s)", t.name, name, r1, r2)
		return true
	}
	s := g.declare(kFp, "fp")
	g.linef("var %s := %s(%s, %s)", s.name, name, r1, r2)
	return true
}

// fpPair is two <ref>s.
func (g *sgen) fpPair() (string, string) {
	r1, _ := g.ref()
	r2, _ := g.ref()
	return r1, r2
}

// fpCall is a per-field call result over <ref>s: FpMk (a literal
// returned), FpSw (a split local returned, its clause crossed) or FpK (a
// third reference flowing into the first field's source).
func (g *sgen) fpCall() string {
	r1, r2 := g.fpPair()
	switch g.rng.intn(5) {
	case 0, 1:
		g.use(itemFpMk)
		return fmt.Sprintf("FpMk(%s, %s)", r1, r2)
	case 2, 3:
		g.use(itemFpSw)
		return fmt.Sprintf("FpSw(%s, %s)", r1, r2)
	}
	// c's loan reaches field a only through c into x, stored at step 3 of
	// the call rule before step 5 reads x's set (mutant
	// M-F8-W6-res-before).
	r3, _ := g.ref()
	g.use(itemFpK)
	return fmt.Sprintf("FpK(%s, %s, %s)", r3, r1, r2)
}

// fpNew is S70: a split local from a literal, a per-field call result
// bound whole, a copy of another; an Fq around an
// Fp; an Fw with a Cell inline.
func (g *sgen) fpNew() bool {
	if !g.refable() {
		return false
	}
	q := g.rng.intn(10)
	u := g.pick(kFp, false)
	switch {
	case q < 3 || q == 5 && u == nil:
		r1, r2 := g.fpPair()
		g.use(itemFp)
		s := g.declare(kFp, "fp")
		g.linef("var %s := Fp(a: %s, b: %s)", s.name, r1, r2)
	case q < 5:
		c := g.fpCall()
		s := g.declare(kFp, "fp")
		g.linef("var %s := %s", s.name, c)
	case q == 5:
		u.used = true
		s := g.declare(kFp, "fp")
		g.linef("var %s := %s", s.name, u.name)
	case q < 8:
		var in string
		switch {
		case u != nil && g.chance(1, 2):
			u.used = true
			in = u.name
		case g.chance(1, 2):
			in = g.fpCall()
		default:
			r1, r2 := g.fpPair()
			g.use(itemFp)
			in = fmt.Sprintf("Fp(a: %s, b: %s)", r1, r2)
		}
		r, _ := g.ref()
		g.use(itemFq)
		s := g.declare(kFq, "fq")
		g.linef("var %s := Fq(p: %s, c: %s)", s.name, in, r)
	default:
		r1, r2 := g.fpPair()
		g.use(itemFw)
		s := g.declare(kFw, "fw")
		s.fixed = true
		g.linef("var %s := Fw(a: %s, c: Cell.New(%s))", s.name, r1, r2)
	}
	return true
}

// fpField is a or b.
func (g *sgen) fpField() string { return [2]string{"a", "b"}[g.rng.intn(2)] }

// fpWrite is S71: a field write, a store through mut p.f, Swap2 of the two
// fields (Swap's rule), or the deep write n.p.f = r and n.c = r on an Fq.
func (g *sgen) fpWrite() bool {
	t, n := g.pick(kFp, true), g.pick(kFq, true)
	if t == nil && n == nil || !g.refable() {
		return false
	}
	if t != nil && (n == nil || g.chance(2, 3)) {
		t.used = true
		f := g.fpField()
		switch q := g.rng.intn(6); {
		case q < 3:
			r, _ := g.ref()
			g.linef("%s.%s = %s", t.name, f, r)
		case q < 5:
			r, _ := g.ref()
			g.use(itemSetRef)
			g.linef("SetRef(mut %s.%s, %s)", t.name, f, r)
		default:
			g.use(itemSwap2)
			g.linef("Swap2(mut %s.a, mut %s.b)", t.name, t.name)
		}
		return true
	}
	n.used = true
	r, _ := g.ref()
	g.linef("%s.%s = %s", n.name, [3]string{"p.a", "p.b", "c"}[g.rng.intn(3)], r)
	return true
}

// fpRead is S72: a field read into h or a ref local; a destructuring of a
// local or of a per-field call result, with and without ..; F().b; an Fq's
// nested Fp copied out.
func (g *sgen) fpRead() bool {
	t, n := g.pick(kFp, false), g.pick(kFq, false)
	if t == nil && n == nil && !g.refable() {
		return false
	}
	switch q := g.rng.intn(8); {
	case q < 2 && t != nil:
		t.used = true
		g.fold("*" + t.name + "." + g.fpField())
	case q < 3 && t != nil:
		t.used = true
		s := g.declare(kRef, "r")
		g.linef("var %s := %s.%s", s.name, t.name, g.fpField())
	case q < 5 && t != nil:
		t.used = true
		g.fpDestructure(t.name)
	case q < 6 && n != nil:
		n.used = true
		s := g.declare(kFp, "fp")
		g.linef("var %s := %s.p", s.name, n.name)
	case q < 7 && g.refable():
		c := g.fpCall()
		s := g.declare(kRef, "r")
		g.linef("var %s := %s.%s", s.name, c, g.fpField())
	default:
		if !g.refable() {
			return false
		}
		g.fpDestructure(g.fpCall())
	}
	return true
}

// fpDestructure binds e's fields by an Fp pattern, both or one with ..
func (g *sgen) fpDestructure(e string) {
	g.use(itemFp)
	switch g.rng.intn(3) {
	case 0:
		a, b := g.declare(kRef, "q"), g.declare(kRef, "q")
		a.fixed, b.fixed = true, true
		g.linef("Fp(a: %s, b: %s) := %s", a.name, b.name, e)
	default:
		f := g.fpField()
		a := g.declare(kRef, "q")
		a.fixed = true
		g.linef("Fp(%s: %s, ..) := %s", f, a.name, e)
	}
}

// fpWhole is S73: the whole value used. &p of a type with nothing that
// writes past a shared reference (FpShow), by value (FpTake, and
// FpPick, whose by-value parameter is split in the callee), the uses that
// make p whole (FpMix's mut p, the mut self call Rot, FpSet's store
// through the whole loan, Fw.Stow's &self on a Cell inline), and p = e by
// a literal, a per-field result or another local.
func (g *sgen) fpWhole() bool {
	if w := g.pick(kFw, false); w != nil && g.chance(1, 4) && g.refable() {
		w.used = true
		r, _ := g.ref()
		g.use(itemFwStow)
		g.linef("%s.Stow(%s)", w.name, r)
		return true
	}
	t := g.pick(kFp, true)
	if t == nil || !g.refable() {
		return false
	}
	t.used = true
	switch g.rng.intn(9) {
	case 0:
		g.use(itemFpShow)
		g.fold("FpShow(&" + t.name + ")")
	case 1:
		g.use(itemFpTake)
		s := g.declare(kRef, "r")
		g.linef("var %s := FpTake(%s)", s.name, t.name)
	case 2:
		r, _ := g.ref()
		g.use(itemFpPick)
		s := g.declare(kRef, "r")
		g.linef("var %s := FpPick(%s, %s)", s.name, t.name, r)
	case 3:
		g.use(itemFpMix)
		g.linef("FpMix(mut %s)", t.name)
	case 4:
		g.use(itemFpRot)
		g.linef("%s.Rot()", t.name)
	case 5:
		r, _ := g.ref()
		g.use(itemFpSet)
		g.linef("FpSet(mut %s, %s)", t.name, r)
	case 6:
		r1, r2 := g.fpPair()
		g.linef("%s = Fp(a: %s, b: %s)", t.name, r1, r2)
	case 7:
		g.linef("%s = %s", t.name, g.fpCall())
	default:
		u := g.pick(kFp, false)
		if u == t {
			g.use(itemFpShow)
			g.fold("FpShow(&" + t.name + ")")
			return true
		}
		u.used = true
		g.linef("%s = %s", t.name, u.name)
	}
	return true
}

// fpJoin is S74: a block whose int local's loan goes into one field on one
// path and into the other field (or a long-lived loan into it) on the
// other; the first field is then rewritten by a long-lived loan one time
// in two, so some programs keep no dead loan and are accepted (the rejected
// form is the join example of doc/memory.md).
func (g *sgen) fpJoin() bool {
	t := g.pick(kFp, true)
	if t == nil || g.depth >= g.maxDepth() || !g.refable() {
		return false
	}
	t.used = true
	long, _ := g.ref()
	long2, _ := g.ref()
	cond := g.branch()
	g.linef("if %s {", cond)
	g.open()
	y := g.declare(kInt, "v")
	g.linef("var %s: i64 = %s", y.name, g.lit())
	f1 := g.fpField()
	f2 := "b"
	if f1 == "b" {
		f2 = "a"
	}
	other := "&" + y.name
	if g.chance(1, 2) {
		other = long
	}
	g.linef("if %s {", g.cond())
	g.linef("    %s.%s = &%s", t.name, f1, y.name)
	g.line("} else {")
	g.linef("    %s.%s = %s", t.name, f2, other)
	g.line("}")
	if g.chance(1, 2) {
		g.fold("*" + t.name + "." + f2)
	}
	if g.chance(1, 2) {
		g.linef("%s.%s = %s", t.name, f1, long2)
	}
	g.close()
	g.line("}")
	return true
}

// fpLoop is S75: a loop that moves field b into a and stores a loan into
// b, a block-local's one time in two (the fixpoint over field sets); the
// fields are rewritten by long-lived loans after it one time in two each.
func (g *sgen) fpLoop() bool {
	t := g.pick(kFp, true)
	if t == nil || g.depth >= g.maxDepth() || !g.refable() {
		return false
	}
	t.used = true
	long, _ := g.ref()
	n := 2 + g.rng.intn(2)
	i := g.fresh("ix")
	g.linef("for %s in 0..%d {", i, n)
	g.enterLoop(n)
	g.open()
	g.fold("i64(" + i + ")")
	if g.chance(1, 2) {
		g.fold("*" + t.name + ".a")
	}
	g.linef("%s.a = %s.b", t.name, t.name)
	if g.chance(1, 2) {
		y := g.declare(kInt, "v")
		g.linef("var %s: i64 = %s", y.name, g.lit())
		g.linef("%s.b = &%s", t.name, y.name)
	} else {
		g.linef("%s.b = %s", t.name, long)
	}
	g.close()
	g.leaveLoop(n)
	g.line("}")
	for _, f := range []string{"a", "b"} {
		if g.chance(1, 2) {
			g.linef("%s.%s = %s", t.name, f, long)
		}
	}
	return true
}

// faHole is S76 (a store that reaches an arena cell on a later hop): a
// holder Fa of a cell of a local sub-arena beside a reference field. A store
// through &holder by FaStash, whose clause names the holder, reaches the
// cell only on a later hop through the arena, which the later-hop allocator
// rule stores into; the field s is written beside it (the Put forms); a
// reference is read back through the holder.
func (g *sgen) faHole() bool {
	if !g.refable() {
		return false
	}
	if p := g.pick(kFa, true); p != nil && g.chance(3, 4) {
		p.used = true
		r, _ := g.ref()
		switch g.rng.intn(4) {
		case 0, 1:
			g.use(itemFaStash)
			g.linef("FaStash(&%s, %s)", p.name, r)
		case 2:
			g.linef("%s.s = %s", p.name, r)
		default:
			s := g.declare(kRef, "r")
			g.linef("var %s := %s.c.Load()", s.name, p.name)
		}
		return true
	}
	w := g.pick(kACell, false)
	if w == nil {
		b := g.pick(kArena, false)
		if b == nil {
			g.usesA = true
			name := g.fresh("b")
			g.elseReturn(fmt.Sprintf("var %s := a.Sub()", name))
			b = g.declareNamed(kArena, name)
			b.fixed = true
		}
		r, _ := g.ref()
		cn := g.fresh("w")
		g.elseReturn(fmt.Sprintf("var %s := %s.Put(Cell.New(%s))", cn, b.name, r))
		w = g.declareNamed(kACell, cn)
		w.fixed = true
	}
	w.used = true
	r, _ := g.ref()
	g.use(itemFa)
	p := g.declare(kFa, "pp")
	g.linef("var %s := Fa(c: &*%s, s: %s)", p.name, w.name, r)
	return true
}

// fhHole is S77 (self-reach): a holder Fh of an Fs whose a is a mut loan on a
// reference local d; FhPut(mut l, x) stores x into l. Up to GenVersion 11 it
// also wrote x.b through x.a, so the reference local x.a borrows pointed where
// x.b did; the callee's own body rejects that write, so FhPut only stores,
// and the caller's self-reach rule and call rule are what S77 draws. x is a
// literal or a split local, and its b never reads the reference it borrows
// (such a literal is E0504 on its own). The reference borrowed is one declared
// before the holder, so the holder's ending reads it last.
func (g *sgen) fhHole() bool {
	if !g.refable() {
		return false
	}
	if l := g.pick(kFh, true); l != nil && g.chance(3, 4) {
		l.used = true
		refs := g.pools[kRef][:min(len(g.pools[kRef]), g.fhRefs[l])]
		var cand []*sname
		for _, s := range refs {
			if !s.fixed && !s.moved {
				cand = append(cand, s)
			}
		}
		if len(cand) == 0 || g.chance(1, 5) {
			g.fold("*" + l.name + ".s.b")
			return true
		}
		t := cand[g.rng.intn(len(cand))]
		t.used = true
		word := regexp.MustCompile(`(^|[^A-Za-z0-9_])` + t.name + `($|[^A-Za-z0-9_])`)
		b := ""
		for range 8 {
			if r, _ := g.ref(); !word.MatchString(r) {
				b = r
				break
			}
		}
		if b == "" {
			g.fold("*" + l.name + ".s.b")
			return true
		}
		g.use(itemFhPut)
		if g.chance(3, 5) {
			g.linef("FhPut(mut %s, Fs(a: mut %s, b: %s))", l.name, t.name, b)
		} else {
			x := g.fresh("x")
			g.linef("var %s := Fs(a: mut %s, b: %s)", x, t.name, b)
			g.linef("FhPut(mut %s, %s)", l.name, x)
		}
		return true
	}
	r1, r2 := g.fpPair()
	d := g.declare(kRef, "d")
	d.fixed = true
	g.linef("var %s: &i64 = %s", d.name, r1)
	g.use(itemFh)
	l := g.declare(kFh, "l")
	g.linef("var %s := Fh(s: Fs(a: mut %s, b: %s))", l.name, d.name, r2)
	if g.fhRefs == nil {
		g.fhRefs = map[*sname]int{}
	}
	g.fhRefs[l] = len(g.pools[kRef]) - 1 // the references declared before the holder, d excluded
	return true
}
