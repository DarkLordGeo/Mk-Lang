// Why: interior memory (a Cell, an Atomic, a Mutex's state) may be shared
// across tasks, so the borrow stacks cannot catch a data race on it; the
// tagged mode needs a race check of its own.
//
// What: vclock, epoch, rbyte and raceState, with raceSpawn, raceJoin,
// raceAccess and raceViolation.
//
// How: a vector clock per task; a spawn copies the parent's clock into the
// child and ticks the parent, a join joins each joined task's clock into the
// owner's, and an atomic intrinsic adds the edges its order makes on its
// location's clock (tagrace_order.go). An access of an interior byte that no
// happens-before edge orders after a conflicting one is VRace. The check
// orders accesses by happens-before, so it does not depend on the schedule
// the seed picked.
//
// Non-interior bytes need no clock: a conflicting access from another task
// pops a tag, and the stack rule finds it. Accesses made before the first
// spawn are ordered before every task (a child starts from its parent's
// clock), so nothing is recorded until a task exists. An atomic intrinsic's
// own access synchronizes and is not itself checked.

package eval

// vclock is one task's vector clock, indexed by task id (0 is the
// request's own task); a missing entry is 0.
type vclock []uint32

func (c vclock) at(t uint32) uint32 {
	if int(t) < len(c) {
		return c[t]
	}
	return 0
}

// set returns c with entry t set to v, grown as needed.
func (c vclock) set(t, v uint32) vclock {
	for int(t) >= len(c) {
		c = append(c, 0)
	}
	c[t] = v
	return c
}

// join returns c with every entry the maximum of c's and o's.
func (c vclock) join(o vclock) vclock {
	for i, v := range o {
		if v > c.at(uint32(i)) {
			c = c.set(uint32(i), v)
		}
	}
	return c
}

// epoch is one access: the task that made it and that task's own clock
// entry at the time.
type epoch struct{ task, clock uint32 }

// rbyte is the race state of one interior byte: its last write, and the
// reads since then, at most one per task.
type rbyte struct {
	w     epoch
	wrote bool
	reads []epoch
}

// raceState is a tagged run's race check state (tagState.race).
type raceState struct {
	on     bool     // a task has been spawned: accesses are recorded
	clocks []vclock // by task id
	bytes  map[uint64]*rbyte
	locs   map[uint64]vclock // atomic locations' clocks
	atomic bool              // inside an atomic intrinsic
	// fenceRel and fenceAcq are, by task, the clock at the last release
	// fence and the join of what relaxed loads read since the last acquire
	// fence (tagrace_order.go); casOK is whether the last CAS wrote.
	fenceRel, fenceAcq []vclock
	casOK              bool
}

func raceKey(a, off uint32) uint64 { return uint64(a)<<32 | uint64(off) }

// clock is task t's clock, made on first use with its own entry 1.
func (r *raceState) clock(t uint32) vclock {
	for int(t) >= len(r.clocks) {
		r.clocks = append(r.clocks, nil)
	}
	if r.clocks[t] == nil {
		r.clocks[t] = vclock(nil).set(t, 1)
	}
	return r.clocks[t]
}

// raceSpawn is a spawn of child by parent: the child starts from the
// parent's clock, and the parent ticks.
func (m *Machine) raceSpawn(parent, child uint32) {
	if m.tg.opt.NoRaces {
		return
	}
	r := &m.tg.race
	r.on = true
	if r.bytes == nil {
		r.bytes = map[uint64]*rbyte{}
	}
	pc := r.clock(parent)
	cc := append(vclock(nil), pc...)
	cc = cc.set(child, 1)
	r.clock(child)
	r.clocks[child] = cc
	r.clocks[parent] = pc.set(parent, pc.at(parent)+1)
}

// raceJoin joins task t's clock into owner's, at the end of a join.
func (m *Machine) raceJoin(owner, t uint32) {
	r := &m.tg.race
	if !r.on {
		return
	}
	r.clocks[owner] = r.clock(owner).join(r.clock(t))
}

// raceAccess checks n interior bytes at off of allocation a, accessed by
// the running task: a write must be ordered after the last write
// and every read since; a read after the last write. A failure is VRace,
// with Other the task of the unordered access.
func (m *Machine) raceAccess(a, off uint32, n uint64, write bool) {
	r := &m.tg.race
	if !r.on || r.atomic {
		return
	}
	t := m.tg.task.cur
	c := r.clock(t)
	now := epoch{task: t, clock: c.at(t)}
	for o := off; uint64(o-off) < n; o++ {
		k := raceKey(a, o)
		b := r.bytes[k]
		if b == nil {
			b = &rbyte{}
			r.bytes[k] = b
		}
		if b.wrote && b.w.task != t && c.at(b.w.task) < b.w.clock {
			m.raceViolation(a, off, n, b.w.task)
			return // one finding per access
		}
		if write {
			for _, rd := range b.reads {
				if rd.task != t && c.at(rd.task) < rd.clock {
					m.raceViolation(a, off, n, rd.task)
					return
				}
			}
			b.w, b.wrote, b.reads = now, true, b.reads[:0]
			continue
		}
		seen := false
		for i := range b.reads {
			if b.reads[i].task == t {
				b.reads[i].clock, seen = now.clock, true
			}
		}
		if !seen {
			b.reads = append(b.reads, now)
		}
	}
}

func (m *Machine) raceViolation(a, off uint32, n uint64, other uint32) {
	m.violation(Violation{Kind: VRace, Where: m.where(a, off), Off: off, Len: uint32(n), Task: m.tg.task.cur, Other: other})
}
