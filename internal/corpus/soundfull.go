// Why: the shapes that need io or the arena can only run in the full host,
// and the task shapes carry their own spawn bounds; keeping them apart keeps
// soundshapes.go, which dispatches them, within size.
//
// What: fullShape, the shapes that need io or the arena (S25 to S29, S31, S32,
// and arena shapes such as S37 and S38; S34 is a helper's form), maxRunSpawns,
// spawned, and taskStmt for the statements of an S28 scope.
//
// How: each shape is Run's only. Arena shapes put cells into a local
// sub-arena, since the entry's arena outlives every local. Spawns are
// counted per statement for the program's bound of 8 and per loop trip for
// the run's bound of 64.

package corpus

import "fmt"

// fullShape writes the shapes that need io or the arena, S25 to S29, S31
// and S32 (S34 is a helper's form, helper). Each is Run's only.
func (g *sgen) fullShape(id int) bool {
	if g.cur != nil {
		return false
	}
	switch id {
	case sMutex:
		if !g.refable() {
			return false
		}
		g.usesIO = true
		u := g.pick(kMutex, false)
		if u == nil || g.chance(1, 3) {
			r, _ := g.ref()
			u = g.declare(kMutex, "mx")
			g.linef("var %s := Mutex.New(%s)", u.name, r)
		}
		gd := g.fresh("g")
		g.elseReturn(fmt.Sprintf("var %s := %s.Lock(io)", gd, u.name))
		r, _ := g.ref()
		if g.chance(1, 2) {
			g.linef("%s.Store(%s)", gd, r)
		} else {
			g.linef("*%s.Mut() = %s", gd, r)
		}
		g.linef("%s.Unlock()", gd)
	case sSubArena, sDefer:
		g.usesA = true
		b := g.fresh("b")
		g.elseReturn(fmt.Sprintf("var %s := a.Sub()", b))
		s := g.declareNamed(kArena, b)
		s.fixed = true
		if id == sDefer {
			s.deferred = true
			g.linef("defer %s.Free()", b)
		}
	case sArenaCell:
		if !g.refable() {
			return false
		}
		g.usesA = true
		if w := g.pick(kACell, false); w != nil && g.chance(1, 2) {
			if g.chance(1, 2) {
				r, _ := g.ref()
				g.linef("%s.Store(%s)", w.name, r)
			} else {
				s := g.declare(kRef, "r")
				g.linef("var %s := %s.Load()", s.name, w.name)
			}
			return true
		}
		// Into a local sub-arena: the entry's arena
		// outlives every local, so a cell there could hold no local's loan.
		b := g.pick(kArena, false)
		if b == nil {
			return false
		}
		ar := b.name
		r, _ := g.ref()
		w := g.fresh("w")
		g.elseReturn(fmt.Sprintf("var %s := %s.Put(Cell.New(%s))", w, ar, r))
		g.declareNamed(kACell, w).fixed = true
	case sTasks:
		if g.spawns >= 8 || g.dyn+g.mult > maxRunSpawns || !g.has(kInt, true) {
			return false
		}
		g.usesIO = true
		s := g.fresh("s")
		g.linef("scope %s := io.Scope() {", s)
		n := 1 + g.rng.intn(4)
		for i := range n {
			g.taskStmt(s, i == 0)
		}
		g.line("}")
	case sSpawner:
		// Only inside a scope: taskStmt writes it there.
		return false
	case sArenaTie:
		return g.arenaTie()
	case sDropSlot:
		return g.dropSlot()
	case sArenaBranch:
		// S37 (a linear value is consumed once on every path): a sub-arena
		// freed on both paths of a branch; one time in six only on the first,
		// so the other path leaks it (E0402).
		g.usesA = true
		b := g.fresh("b")
		g.elseReturn(fmt.Sprintf("var %s := a.Sub()", b))
		cond := g.branch()
		g.linef("if %s {", cond)
		g.linef("    %s.Free()", b)
		if g.chance(1, 6) {
			g.leak = true
			g.line("}")
		} else {
			g.line("} else {")
			g.linef("    %s.Free()", b)
			g.line("}")
		}
	case sDeferFree:
		// S38 (linear values and defer): an explicit Free of an arena a
		// defer frees
		// (E0406); the deferred Free then runs on a consumed arena.
		// Only an arena of this block, so the Free is on every path of the
		// defer's scope and E0406 is the program's only move code.
		var s *sname
		for _, b := range g.blocks[len(g.blocks)-1].decls {
			if b.kind == kArena && b.deferred && !b.moved {
				s = b
			}
		}
		if s == nil {
			return false
		}
		s.moved = true
		g.linef("%s.Free()", s.name)
	case sGlobalMutex:
		if g.global != "" && g.chance(1, 2) || !g.refable() {
			return false
		}
		g.usesIO = true
		if g.global == "" {
			k := g.static()
			g.global = "GM"
			g.consts = append(g.consts, fmt.Sprintf("var GM: Mutex[&i64] = Mutex.New(&%s)", k))
		}
		gd := g.fresh("g")
		g.elseReturn(fmt.Sprintf("var %s := GM.Lock(io)", gd))
		var r string
		if g.chance(1, 2) {
			r = "&" + g.statics[g.rng.intn(len(g.statics))] // a static loan: the globals rule accepts it
		} else {
			r, _ = g.ref() // a frame's loan: E0605 when it reaches GM
		}
		g.linef("%s.Store(%s)", gd, r)
		g.linef("%s.Unlock()", gd)
	default:
		return false
	}
	return true
}

// maxRunSpawns bounds the spawns one run makes, counted through the loops
// around each spawn: the host Skips a run's 65th spawn.
const maxRunSpawns = 64

// spawned counts one spawn statement: once for the program's bound of 8,
// and once per trip of the loops around it for the run's.
func (g *sgen) spawned() { g.spawns++; g.dyn += g.mult }

// taskStmt writes one statement of an S28 scope s: a spawn of Bump, Read
// or Sum on int locals (tasks write only their mut arguments and read only
// their & ones), S29's Spawner, or a read or write of an int a task
// may hold (E0504 when it conflicts).
func (g *sgen) taskStmt(s string, spawn bool) {
	if (g.spawns >= 8 || g.dyn+g.mult > maxRunSpawns) && !spawn {
		g.linef("    h = h *%% 31 +%% %s", g.pick(kInt, false).name)
		return
	}
	q := g.rng.intn(10)
	if spawn {
		q %= 7 // the scope's first statement spawns, so the scope is used
	}
	switch {
	case q < 3:
		g.use(itemBump)
		g.spawned()
		g.linef("    %s.go Bump(mut %s)", s, g.pick(kInt, true).name)
	case q < 5:
		g.use(itemRead)
		g.spawned()
		g.linef("    %s.go Read(&%s, mut %s)", s, g.pick(kInt, false).name, g.pick(kInt, true).name)
	case q < 6 && g.has(kArray, false):
		g.use(itemSum)
		g.spawned()
		g.linef("    %s.go Sum(%s[..], mut %s)", s, g.pick(kArray, false).name, g.pick(kInt, true).name)
	case q < 7:
		g.use(itemSpawner)
		g.spawned()
		g.shapes[sSpawner] = true
		g.linef("    Spawner(&%s, mut %s)", s, g.pick(kInt, true).name)
	case q < 9:
		g.hUsed = true
		g.linef("    h = h *%% 31 +%% %s", g.pick(kInt, false).name)
	default:
		g.linef("    %s = %s", g.pick(kInt, true).name, g.lit())
	}
}
