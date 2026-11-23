// Why: a spawned task runs beside its parent until the scope joins, so its
// `*q into p` clause is no one-time store: what reaches q's referent while
// the task runs may be read by the task and stored into p, and what p
// reaches may grow while the task waits to store. Applying the clause once,
// at the spawn, would miss a value sent into a channel after a reader was
// spawned, and p would keep a reference into a dead frame.
//
// What: taskFlow (one recorded clause of a task), noteTaskFlows (the
// recording at `s.go`, and at a call an argument of which reaches a scope,
// whose callee may spawn), closeFlows (the clauses applied again, from a
// worklist), flowBehind, flowOpen, the work counter and taskFlowText (how
// E0505 and E0604 name the task).
//
// How: the invariant is that, after every store and after every call that
// records a flow, wherever a recorded task may run, for each of its clauses
// every root of writable(p) holds every loan of B(q) for `*q into p`, and
// x's loans for `x into p`, as steps 3 and 4 leave them at a call made
// there. A clause is a function of the entries of
// the roots it reads: q's targets, and the roots its walk from p read (its
// region). While a task borrows a local, the parent cannot assign it, so
// those entries change only by a store (storeInto: a call's steps 3 and 4,
// the self-reach rule's stores, a write through a reference, a spawn's
// stores into a &Scope's roots, and these flows' own stores) or by a join of
// two paths. After a store into root t, the flows whose region holds t (an
// index, flowAt) are applied again, grown or not; an application that grows
// a root queues the other flows whose region holds it, until the worklist
// is empty. A join adds nothing a single path can run: what it puts
// together from two paths comes from one execution only when one ran before
// the other, around a loop, and the later path's store is then applied in
// the joined state. A spawn, or a call an argument of which reaches a
// scope, applies the flows it records once after its steps, which indexes
// their regions. An application skips the hops of the self-reach rule (no
// loan into its own target, no parameter's loan into its own node) and does
// not queue itself. A walk is reused while the roots it read changed only by
// loans to plain locals, which it meets but neither writes nor passes. The
// list is empty in a body that spawns no task with a clause, and storeInto
// reads nothing more then.

package borrow

import (
	"slices"
	"strconv"
	"sync/atomic"

	"mk/internal/mir"
	"mk/internal/types"
)

// flowWork counts the clause applications of every check, for the
// test that bounds them.
var flowWork atomic.Uint64

// FlowApplications is the number of task-flow clause applications made so
// far in this process (closeFlows).
func FlowApplications() uint64 { return flowWork.Load() }

// taskFlow is one clause `*q into p` of a spawned task, or of a callee
// given a scope.
type taskFlow struct {
	// scope is the scope root: a scope local, or another root (a &Scope
	// parameter's node, a temporary), whose scope joins after the body.
	// helper marks a flow recorded at a call that passes the scope rather
	// than at `s.go`.
	scope  rootID
	helper bool
	// q is the q argument's loans: the task reads B(q). p is the p
	// argument's loans: it stores what it read into writable(p). alloc is
	// step 4's allocator keep for q (behindKeep).
	q, p  setID
	alloc bool
	// direct marks a clause `x into p` (step 3): the task stores q, x's
	// own loans, into writable(p), rather than B(q).
	direct bool
	// queued marks the flow on the worklist.
	queued bool
	// walk is the last walk from p, reused while every root it read has
	// the same entry (flowWalk).
	walk *flowWalk
	// at is the spawn's or call's span and b its block; callee and from,
	// into name the clause for a diagnostic (taskFlowText).
	at         mir.SpanID
	b          mir.BlockID
	callee     *types.Signature
	from, into types.ParamIndex
}

// noteTaskFlows records the clauses of call t whose callee runs as a task
// (a spawn) or may spawn one (an argument reaches a scope), from each
// argument's loans before steps 3 and 4 (entryH): what a later round chained
// into q is no referent of q. It returns the flows it recorded or grew.
func (c *checker) noteTaskFlows(st *state, ctx *stmtCtx, t *mir.Terminator, fv *flowView, entryH []setID) []int {
	if fv == nil || len(fv.stores) == 0 && !slices.ContainsFunc(fv.into, func(p types.ParamIndex) bool { return p != types.NoParam }) {
		return nil
	}
	args := c.body.Args[t.Args.Start:t.Args.End()]
	scope, sp, helper := noRoot, types.NoParam, false
	if t.Spawn != mir.NoLocal {
		scope = rootID(t.Spawn)
	} else if scope, sp = c.scopeArg(st, entryH); scope == noRoot {
		return nil
	} else {
		helper = true
	}
	var touched []int
	for i, s := range fv.stores {
		if s.From == types.NoParam || s.Into == types.NoParam || s.Into == sp ||
			int(s.From) > len(entryH) || int(s.Into) > len(entryH) || !c.storesLoans(t, fv, s.From) {
			continue
		}
		q, p := entryH[s.From-1], entryH[s.Into-1]
		if q == emptySet || p == emptySet {
			continue
		}
		k := siteKey{ctx.b, uint32(i)}
		if c.taskFlowAt == nil {
			c.taskFlowAt = map[siteKey]int{}
		}
		j, ok := c.taskFlowAt[k]
		if !ok {
			f := taskFlow{scope: scope, helper: helper, at: ctx.span, b: ctx.b, from: s.From, into: s.Into,
				alloc: !c.allocReferent(args[s.From-1])}
			if t.Callee.Kind == mir.CallDirect && c.env.Callee != nil {
				f.callee = c.env.Callee(t.Callee.Fn.Item)
			}
			j = len(c.taskFlows)
			c.taskFlowAt[k] = j
			c.taskFlows = append(c.taskFlows, f)
		}
		f := &c.taskFlows[j]
		if nq, np := c.sets.unite(f.q, q), c.sets.unite(f.p, p); nq != f.q || np != f.p {
			f.q, f.p = nq, np
			c.taskFlowGrew = true
		}
		touched = append(touched, j)
	}
	for x, p := range fv.into {
		if p == types.NoParam || p == sp || x+1 > len(entryH) || int(p) > len(entryH) {
			continue
		}
		q, pl := entryH[x], entryH[p-1]
		if q == emptySet || pl == emptySet {
			continue
		}
		k := siteKey{ctx.b, 1<<20 + uint32(x)}
		if c.taskFlowAt == nil {
			c.taskFlowAt = map[siteKey]int{}
		}
		j, ok := c.taskFlowAt[k]
		if !ok {
			f := taskFlow{scope: scope, helper: helper, at: ctx.span, b: ctx.b, from: types.ParamIndex(x + 1), into: p, direct: true}
			if t.Callee.Kind == mir.CallDirect && c.env.Callee != nil {
				f.callee = c.env.Callee(t.Callee.Fn.Item)
			}
			j = len(c.taskFlows)
			c.taskFlowAt[k] = j
			c.taskFlows = append(c.taskFlows, f)
		}
		f := &c.taskFlows[j]
		if nq, np := c.sets.unite(f.q, q), c.sets.unite(f.p, pl); nq != f.q || np != f.p {
			f.q, f.p = nq, np
			c.taskFlowGrew = true
		}
		touched = append(touched, j)
	}
	return touched
}

// scopeArg is the scope a call's arguments reach, and the parameter that
// reaches it: walking from each argument's loans through the held sets of
// their targets, the first root that is a scope local or of type Scope.
// noRoot when none does.
func (c *checker) scopeArg(st *state, entryH []setID) (rootID, types.ParamIndex) {
	k := c.env.Known
	if k == nil || k.Scope == types.NoItem {
		return noRoot, types.NoParam
	}
	for i, S := range entryH {
		seen := map[rootID]bool{}
		work := slices.Clone(c.sets.get(S)) // never append to a set's own slice
		for len(work) > 0 {
			l := work[len(work)-1]
			work = work[:len(work)-1]
			t := c.loans[l].target
			if t == noRoot || seen[t] {
				continue
			}
			seen[t] = true
			ri := &c.roots[t]
			if ri.kind == rkScope || ri.typ != types.NoType && c.tab.Get(ri.typ).Kind == types.KNamed && c.tab.Get(ri.typ).Item == k.Scope {
				return t, types.ParamIndex(i + 1)
			}
			work = append(work, c.sets.get(c.held(st, t))...)
		}
	}
	return noRoot, types.NoParam
}

// indexFlow adds flow j to the worklist index at root r.
func (c *checker) indexFlow(r rootID, j int) {
	k := uint64(r)<<32 | uint64(j)
	if c.flowAtSeen[k] {
		return
	}
	if c.flowAtSeen == nil {
		c.flowAtSeen = map[uint64]bool{}
		c.flowAt = map[rootID][]int{}
	}
	c.flowAtSeen[k] = true
	c.flowAt[r] = append(c.flowAt[r], j)
}

// followTaskFlows is storeInto's hook: after a store into root t, the
// flows whose region holds t are applied again (closeFlows). Every store
// counts, grown or not: at a store in a loop the state joins what an
// earlier iteration put in a root, which no earlier application saw
// together with this path's.
func (c *checker) followTaskFlows(st *state, ctx *stmtCtx, t rootID) {
	if c.flowDepth != 0 {
		return
	}
	c.closeFlows(st, ctx, c.flowAt[t])
}

// closeFlows applies the flows of seed, and every flow queued by a root an
// application grows, until the worklist is empty: B(q) now is stored into
// writable(p) now, with the allocator filters of storeArg. A flow is not
// applied during the steps of the call that records it (flowCall), which
// apply the clause themselves. A store of the self-reach rule (csrConst)
// is followed as a real store: the task reads what lands in q's referent
// whoever put it there.
func (c *checker) closeFlows(st *state, ctx *stmtCtx, seed []int) {
	if len(seed) == 0 {
		return
	}
	c.flowDepth++
	via, later, mem, csr, sh, fl := c.via, c.laterAlloc, c.allocMem, c.csrConst, c.shIn, c.flowVia
	c.csrConst, c.shIn = false, emptySet
	var work []int
	push := func(j int) {
		if f := &c.taskFlows[j]; !f.queued {
			f.queued = true
			work = append(work, j)
		}
	}
	for _, j := range seed {
		push(j)
	}
	for len(work) > 0 {
		j := work[0]
		work = work[1:]
		f := &c.taskFlows[j]
		f.queued = false
		if c.flowCall == f.b+1 || !c.flowOpen(st, f) {
			continue
		}
		flowWork.Add(1)
		for _, l := range c.sets.get(f.q) {
			if t := c.loans[l].target; t != noRoot {
				c.indexFlow(t, j)
			}
		}
		S := f.q
		if !f.direct {
			S = c.flowBehind(st, f)
		}
		if S == emptySet {
			continue
		}
		w := c.flowWalkOf(st, f, j)
		roots, fld, la, am := w.roots, w.fld, w.later, w.mem
		c.flowVia = f
		for i, r := range roots {
			c.indexFlow(r, j)
			before := st.get(r)
			bh := c.held(st, r)
			switch a, ok := am[r]; {
			case la[r] || c.roots[r].allocator:
				c.flowStore(st, ctx, r, c.allocSelf(r, S), fld[i])
			case ok:
				c.flowStore(st, ctx, r, c.memSelf(a, S), fld[i])
				c.addHeld(st, r, S)
			default:
				// The self-reach filter: no loan into its own target, no
				// parameter's abstract loan into that parameter's node.
				c.flowStore(st, ctx, r, c.allocSelf(r, S), fld[i])
			}
			if after := st.get(r); c.held(st, r) != bh || after.fv != before.fv {
				c.taskGrew = true
				for _, k := range c.flowAt[r] {
					if k != j {
						push(k) // not j: what it stored is its own value's memory
					}
				}
			}
		}
	}
	c.via, c.laterAlloc, c.allocMem, c.csrConst, c.shIn, c.flowVia = via, later, mem, csr, sh, fl
	c.flowDepth--
}

// flowWalk is one walk writable(p) of a flow: the roots it read with their
// entries then, and its result.
type flowWalk struct {
	p        setID
	reads    []rootID
	entries  []entry
	allocGen int
	roots    []rootID
	fld      []int
	later    map[rootID]bool
	mem      map[rootID]rootID
}

// flowWalkOf is writable(p) of flow j now: its last walk when p's loans are
// the ones it walked from, every root that walk read holds the same entry and
// allocStored has not grown (the walk is a function of those), else a new
// walk, whose roots index the
// flow.
func (c *checker) flowWalkOf(st *state, f *taskFlow, j int) *flowWalk {
	if w := f.walk; w != nil && w.p == f.p && w.allocGen == c.allocGen {
		same := true
		for i, r := range w.reads {
			if e := st.get(r); e != w.entries[i] && !c.deadEndGrowth(w.entries[i], e) {
				same = false
				break
			}
		}
		if same {
			for i, r := range w.reads {
				w.entries[i] = st.get(r)
			}
			return w
		}
	}
	c.walkRead = map[rootID]bool{}
	roots, fld := c.writableSkip(st, f.p, emptySet, noRoot)
	w := &flowWalk{p: f.p, allocGen: c.allocGen, roots: roots, fld: fld, later: c.laterAlloc, mem: c.allocMem}
	for r := range c.walkRead {
		c.indexFlow(r, j)
		w.reads = append(w.reads, r)
		w.entries = append(w.entries, st.get(r))
	}
	c.walkRead = nil
	f.walk = w
	return w
}

// deadEndGrowth reports whether entry e of a root a walk read leads the walk
// where old did: e holds what old held and more, and every loan it adds
// targets a root the walk meets but neither writes nor passes (no carrier,
// allocator or abstract node: a plain local such as an i32), and no loan of
// old that was shallow is deep now (shallowHop skips a shallow one).
func (c *checker) deadEndGrowth(old, e entry) bool {
	if c.sets.minus(old.held, e.held) != emptySet {
		return false
	}
	if c.sets.minus(c.sets.meet(old.sh, old.held), e.sh) != emptySet {
		return false
	}
	for _, l := range c.sets.get(c.sets.minus(e.held, old.held)) {
		if t := c.loans[l].target; t != noRoot {
			if ri := &c.roots[t]; ri.carrier || ri.allocator || ri.kind == rkAbstract {
				return false
			}
		}
	}
	return true
}

// flowStore is storeInto for a flow applied again. Every store after the
// one that made a loan reach root r applies it again, so during the report
// pass the loans already judged at r (flowSeen) are stored unjudged: each is
// reported once, at the first store after which it reaches r.
func (c *checker) flowStore(st *state, ctx *stmtCtx, r rootID, S setID, fld int) {
	if !c.report {
		c.storeInto(st, ctx, r, S, fld)
		return
	}
	if c.flowSeen == nil {
		c.flowSeen = map[rootID]setID{}
	}
	seen := c.flowSeen[r]
	if old := c.sets.meet(S, seen); old != emptySet {
		c.report = false
		c.storeInto(st, ctx, r, old, fld)
		c.report = true
	}
	if fresh := c.sets.minus(S, seen); fresh != emptySet {
		c.flowSeen[r] = c.sets.unite(seen, fresh)
		c.storeInto(st, ctx, r, fresh, fld)
	}
}

// flowBehind is B(q) of flow f as step 4 reads it, behindKeep, except that a
// loan naming a field of a split root reads that field's set: the task reads
// what q points at, not the fields beside it.
func (c *checker) flowBehind(st *state, f *taskFlow) setID {
	out := emptySet
	for _, l := range c.sets.get(f.q) {
		one := c.behindKeep(st, c.sets.fromUnsorted([]uint32{l}), f.alloc)
		if t := c.loans[l].target; t != noRoot {
			if fi := c.fieldOfLoan(loanID(l), t); fi >= 0 {
				if fs := c.sets.fieldSets(st.get(t).fv); fs != nil && len(fs) == c.roots[t].nfields {
					h := c.held(st, t)
					one = c.sets.unite(c.sets.meet(one, fs[fi]), c.sets.minus(one, h))
				}
			}
		}
		out = c.sets.unite(out, one)
	}
	return out
}

// flowOpen reports whether flow f's task may still run here. A scope local
// holds a spawned task's q loans from the spawn to its scope end; a flow
// recorded at a call that passes the scope holds while the scope local is
// assigned, from its statement to its scope end. Any other scope root (a
// &Scope parameter's node, a temporary) joins after the body, so the flow
// holds throughout.
func (c *checker) flowOpen(st *state, f *taskFlow) bool {
	if c.roots[f.scope].kind != rkScope {
		return true
	}
	if f.helper {
		return initOf(st.get(f.scope))&initI != 0
	}
	return c.sets.intersects(st.get(f.scope).held, f.q)
}

// taskFlowText names the flow a store is being followed through, for E0505
// and E0604: " (the task `RecvInto` spawned on line 17 holds `*ch into out`
// until its scope joins, so the store reaches it)"; "" outside one.
func (c *checker) taskFlowText() string {
	f := c.flowVia
	if f == nil {
		return ""
	}
	name, clause := "", "its `*q into p`"
	if f.direct {
		clause = "its `x into p`"
	}
	if s := f.callee; s != nil {
		name = " `" + c.symbol(s.Name) + "`"
		clause = "`*" + sigParam(c, s, f.from) + " into " + sigParam(c, s, f.into) + "`"
		if f.direct {
			clause = "`" + clause[2:]
		}
	}
	at := ""
	if c.env.File != nil {
		sp := c.body.Span(f.at)
		if file := c.env.File(sp.File); file != nil && sp.Start <= file.Len() {
			at = " on line " + strconv.Itoa(int(file.LineCol(sp.Start).Line))
		}
	}
	who := "the task" + name + " spawned" + at + " holds "
	if f.helper {
		who = "a task of" + name + ", called" + at + ", holds "
	}
	return " (" + who + clause + " until its scope joins, so the store reaches it)"
}
