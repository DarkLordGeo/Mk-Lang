// Why: some borrow rules are rarely hit by the general shapes, so a mutant
// that breaks them could survive; each shape here aims at one rule.
//
// What: move (S35) and uninit (S36) for moves and init, temp (S39) for
// temporaries, sharedWrite (S40) for reborrowing, reborrowKill (S41),
// arenaTie (S42), and dropSlot (S43) for argument slots. S20's cell that
// keeps every element is array, in soundshapes.go.
//
// How: each shape writes the accepted form most of the time and, one time
// in a few, the form the rule rejects (E0401, E0403, E0405, E0409, E0502,
// E0506, E0507 and others), so the mutant of that rule is shown needed.

package corpus

import "fmt"

// move is S35 (moves and init): an Sl moved into a new local, which then holds
// its loan. One move in five leaves the old name in scope, so a later use
// reads a moved value (E0401); one in six moves the unique field alone
// (E0409: there are no partial moves). Inside a loop, one move in two
// takes a name declared outside it, which moves again each trip (E0405).
func (g *sgen) move(inRun bool) bool {
	if !inRun {
		return false
	}
	s := g.pick(kSl, false)
	if g.loops > 0 && g.chance(1, 2) {
		// Prefer a name from outside the innermost loop: E0405's programs.
		for _, o := range g.pools[kSl] {
			if !o.moved && o.loop < g.loops {
				s = o
			}
		}
	}
	if s == nil {
		return false
	}
	if g.chance(1, 6) {
		t := g.declare(kMut, "t")
		g.linef("var %s := %s.m", t.name, s.name)
		return true
	}
	t := g.declare(kSl, "s")
	g.linef("var %s := %s", t.name, s.name)
	if !g.chance(1, 5) {
		s.moved = true
	}
	return true
}

// uninit is S36 (assignment before use): a local declared without a value and
// assigned on both paths of a branch; one time in eight only on the first
// (E0403).
func (g *sgen) uninit(inRun bool) bool {
	if !inRun {
		return false
	}
	u := g.fresh("un") // not "u": u16 and u32 are type names
	g.linef("%s: i64", u)
	cond := g.branch()
	g.linef("if %s {", cond)
	g.linef("    %s = %s", u, g.lit())
	if g.chance(1, 8) {
		g.line("}")
	} else {
		g.line("} else {")
		g.linef("    %s = %s", u, g.lit())
		g.line("}")
	}
	g.declareNamed(kInt, u).fixed = true
	return true
}

// temp is S39 (temporaries): a reference read out of a temporary Lx, which
// holds no loan on it; one time in four a reference to the temporary's own
// field, held past its statement (E0506).
func (g *sgen) temp(inRun bool) bool {
	if !inRun || !g.refable() {
		return false
	}
	g.use(itemLx)
	r, _ := g.ref()
	s := g.declare(kRef, "r")
	if g.chance(1, 4) {
		s.fixed = true // kept, so the ending reads it after the statement
		g.linef("var %s := &Lx(src: %s, pos: %s).pos", s.name, r, g.lit())
	} else {
		g.linef("var %s := Lx(src: %s, pos: %s).src", s.name, r, g.lit())
	}
	return true
}

// sharedWrite is S40 (reborrowing): an Sl read through a shared reference to
// it; one time in three a write through its mut field from there (E0507), while
// the reader's loan is live, so the write would pop its tag.
func (g *sgen) sharedWrite(inRun bool) bool {
	s := g.pick(kSl, false)
	if !inRun || s == nil {
		return false
	}
	g.use(itemPeekS)
	p := g.fresh("p")
	g.linef("%s := &%s", p, s.name)
	rn := g.declare(kRef, "r")
	rn.fixed = true
	g.linef("var %s := PeekS(%s)", rn.name, p)
	if g.chance(1, 3) {
		g.linef("*%s.m = %s", p, g.lit())
	}
	return true
}

// reborrowKill is S41 (reassigning a reference ends loans through it): a mut
// reference reborrowed, then reassigned to another int and written through
// while the reborrow is still read. The kill drops the reborrow's loan on *m at
// the reassignment (it keeps what m held, the loan on the first int), so the
// write through the new m conflicts with nothing; without the kill it is E0502.
// The two ints are fresh, so no earlier loan on them decides the verdict; one
// time in eight the second is the first, and the new mut borrow conflicts with
// the reborrow's loan on it (E0501).
func (g *sgen) reborrowKill(inRun bool) bool {
	if !inRun {
		return false
	}
	va := g.declare(kInt, "v")
	g.linef("var %s: i64 = %s", va.name, g.lit())
	vb := va
	if !g.chance(1, 8) {
		vb = g.declare(kInt, "v")
		g.linef("var %s: i64 = %s", vb.name, g.lit())
	}
	// m and y stay out of the pools, so their loans end at the folds below.
	m, y := g.fresh("m"), g.fresh("y")
	g.linef("var %s := mut %s", m, va.name)
	g.linef("%s := mut *%s", y, m)
	g.linef("*%s = %s", y, g.lit())
	g.linef("%s = mut %s", m, vb.name)
	g.linef("*%s = %s", m, g.lit())
	g.fold("*" + y)
	g.fold("*" + m)
	return true
}

// arenaTie is S42 (an arena's ends exemption): a sub-arena of this block (one
// in two fresh) that holds a loan on itself, a cell in it over another of its
// allocations (the exemption's case: at an ends call a loan on self whose every
// live holder is self does not conflict), read, then one time in two Reset
// before the ending's Free (the Reset clears what the arena held, the self-loan
// with it). One time in five instead, a cell over a reference is put in the
// arena and the arena freed while the cell is still read after (E0503: the
// exemption skips only loans no other live root reaches).
func (g *sgen) arenaTie() bool {
	if !g.refable() {
		return false
	}
	b := g.arenaHere()
	d := g.fresh("d")
	if g.chance(1, 5) {
		r, _ := g.ref()
		g.elseReturn(fmt.Sprintf("var %s := %s.Put(Cell.New(%s))", d, b.name, r))
		g.linef("%s.Free()", b.name)
		b.moved = true
		g.fold("*" + d + ".Load()")
		return true
	}
	w := g.fresh("w")
	g.elseReturn(fmt.Sprintf("var %s := %s.Put(i64(%s))", w, b.name, g.lit()))
	g.elseReturn(fmt.Sprintf("var %s := %s.Put(Cell.New(&*%s))", d, b.name, w))
	g.fold("*" + d + ".Load()")
	if !g.has(kACell, false) && g.chance(1, 2) {
		// No other cell in scope: one carved from b and read later would
		// make the Reset E0501.
		g.linef("%s.Reset()", b.name)
	}
	return true
}

// arenaHere is a sub-arena of the current block of Run that a shape may
// consume: the last consumable one, or one time in two (and when there is
// none) a fresh one, declared as S26 declares it, so the shape is ready
// wherever Run may take a.
func (g *sgen) arenaHere() *sname {
	g.usesA = true
	b := g.consumable()
	if b == nil || g.chance(1, 2) {
		name := g.fresh("b")
		g.elseReturn(fmt.Sprintf("var %s := a.Sub()", name))
		b = g.declareNamed(kArena, name)
		b.fixed = true
	}
	return b
}

// dropSlot is S43 (argument slots): a sub-arena passed by value to the header's
// fallible Drop with a later operand that is not inert (k ^ <site>), so lower
// passes it through an argument slot read in evaluation order. On the error
// edge the caller keeps the arena (borrow restores it through the slot), and
// the else handler frees it; without the slot's restore that Free is a use
// after a move (E0401), so M-R13-order drops the program.
func (g *sgen) dropSlot() bool {
	if g.cur != nil {
		return false
	}
	b := g.arenaHere()
	g.use(itemDrop)
	t := g.fresh("t")
	g.elseReturn(fmt.Sprintf("var %s := Drop(a, %s, %s)", t, b.name, g.kArg()))
	b.moved = true
	g.declareNamed(kInt, t).fixed = true
	return true
}
