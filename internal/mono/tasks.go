// Why: a program with no task construct should not link the scheduler, so
// std.rt.UsesTasks() is a constant of the build, decided by what the
// program reaches.
//
// What: the UsesTasks fold (taskFold, taskKeys, taskFields, lazyKeys,
// UsesTasks, foldSwitchesOn) and the fact it settles.
//
// How: collection runs with every call folded to false; when the instances
// reached from a non-runtime root (Main, a test, an export or an
// address-taken callback) spawn, enter or cancel a scope, wait on a word, or
// read an IoTable entry that needs the poller or another task (taskFields),
// the fact is true, every instance whose body called UsesTasks is rebuilt
// with true, and collection continues from them. The runtime roots
// (std.task.Start, the panic hooks, the scope and spawn entries) never make
// the fact true: Start is what the fact decides.
//
// Soundness: false only changes code under `if rt.UsesTasks()`, which std
// writes only around the scheduler's own state (std/task: Start, the pool's
// park, a file or socket on the poller); every construct that needs that
// state when it runs is one of the criteria above, and no criterion is
// itself under such a test in std, so pruning under false hides none.

package mono

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// taskFold is the fold of std.rt.UsesTasks(): its item (NoItem: none) and
// the value a call becomes.
type taskFold struct {
	item  types.ItemID
	value bool
}

// calls reports a direct call of UsesTasks.
func (f taskFold) calls(t *mir.Terminator) bool {
	if f.item != types.NoItem && t.Kind == mir.TermTailCall && t.Callee.Kind == mir.CallDirect && t.Callee.Fn.Item == f.item {
		noTail(t, "std.rt.UsesTasks")
	}
	return f.item != types.NoItem && t.Kind == mir.TermCall && t.Callee.Kind == mir.CallDirect && t.Callee.Fn.Item == f.item
}

var keyUsesTasks = types.ItemKey("std.rt", "UsesTasks")

// taskKeys are the items whose reach makes the fact true: the scope intrinsics
// codegen lowers to std.task.Enter and Cancel, the deadline intrinsics it
// lowers to std.task.CancelAfter, Expired and Remaining, the runtime entries
// themselves, WaitWord, rt.TakeSlot and task.PollBackend.
var taskKeys = []types.Key{
	types.KeyFromString("std.core.Io:Scope"), types.KeyFromString("std.core.Io:ScopeOf"),
	types.KeyFromString("std.core.Io:ScopeWith"), types.KeyFromString("std.core.Scope:Cancel"),
	types.ItemKey("std.task", "Enter"), types.ItemKey("std.task", "SpawnAlloc"), types.ItemKey("std.task", "Spawn"),
	types.ItemKey("std.task", "Join"), types.ItemKey("std.task", "Cancel"), types.ItemKey("std.task", "WaitWord"),
	// The deadline intrinsics and their entries: a deadline's timer
	// is the poller's, as a scope's cancel is the scheduler's.
	types.KeyFromString("std.core.Scope:CancelAfter"), types.KeyFromString("std.core.Scope:Expired"),
	types.KeyFromString("std.core.Scope:Remaining"), types.ItemKey("std.task", "CancelAfter"),
	types.ItemKey("std.task", "Expired"), types.ItemKey("std.task", "Remaining"),
	// A task stack slot: Start reserves the slot regions only for a build
	// with tasks (std/task/start.mk), so taking one is a task construct.
	types.ItemKey("std.rt", "TakeSlot"),
	// The poller's backend: PollBackend reads the TaskState page, which
	// Start maps only for a build with tasks, so asking which poller runs
	// is a task construct.
	types.ItemKey("std.task", "PollBackend"),
}

// taskFields are the IoTable entries whose native function needs the
// poller, the pool's threads or another task: sockets, timers, signals,
// name resolution, processes, and open (a file is opened for the poller).
// The rest (read, write, size, sync, close, rename, remove, now, random,
// mkdir, stat, opendir, readdir, enter, leave) run inline on the calling
// thread off a task, on the three standard handles or a handle that only a
// task field could have made; wait and wake have off-task forms
// (std/task/io_misc.mk NativeWait: the calling thread waits until the word
// changes, which only another OS thread can do, as Main parked as a task
// waits for a wake). A stream write reads wait, so wait cannot be a
// task field without linking the scheduler into every program.
var taskFields = map[string]bool{"open": true, "listen": true, "accept": true, "dial": true, "recv": true,
	"send": true, "sleep": true, "yield": true, "signal": true, "udp": true,
	"sendto": true, "recvfrom": true, "resolve": true, "spawn": true, "procwait": true, "prockill": true,
	"inherited": true}

// taskGraph is what the fact reads: per node, the nodes it requested and
// the IoTable fields its body reads; per field, the nodes of its entries.
type taskGraph struct {
	edges     map[int][]int // lookup only
	reads     map[int][]int // lookup only
	entries   [][]int
	userRoots []int
	// folded are the nodes whose body called UsesTasks (built with false).
	folded []int
	// lazy are the runtime roots held back until the fact is true.
	lazy []Root
}

func (c *collector) tasksInit() {
	if id, ok := c.env.Items().ByKey(keyUsesTasks); ok {
		c.opt.tasks = taskFold{item: id}
	}
	c.tg.edges = map[int][]int{}
	c.tg.reads = map[int][]int{}
}

// edge records that caller requested node j; field is the IoTable field
// the request's function is stored in, or -1.
func (c *collector) edge(caller, j, field int) {
	c.tg.edges[caller] = append(c.tg.edges[caller], j)
	if field >= 0 {
		for len(c.tg.entries) <= field {
			c.tg.entries = append(c.tg.entries, nil)
		}
		c.tg.entries[field] = append(c.tg.entries[field], j)
	}
}

// noteFolded records node i when its generic body calls UsesTasks.
func (c *collector) noteFolded(i int, gb *mir.Body) {
	if gb == nil || c.opt.tasks.item == types.NoItem || c.opt.tasks.value {
		return
	}
	for bi := range gb.Blocks {
		if c.opt.tasks.calls(&gb.Blocks[bi].Term) {
			c.tg.folded = append(c.tg.folded, i)
			return
		}
	}
}

// settleTasks decides the fact after the first drain, and when it is true
// rebuilds the folded nodes with true and drains again.
func (c *collector) settleTasks() {
	if c.opt.tasks.item == types.NoItem || c.opt.tasks.value || !c.needsTasks() {
		return
	}
	c.opt.tasks.value = true
	level := c.addRoots(c.tg.lazy)
	var reqs []pend
	for _, i := range c.tg.folded {
		if c.nodes[c.nodes[i].root].stopped {
			continue
		}
		for _, r := range c.build(i) {
			reqs = append(reqs, pend{i, r})
		}
		reqs = append(reqs, c.io.activated...)
		c.io.activated = c.io.activated[:0]
	}
	for _, p := range reqs {
		if j := c.merge(p.caller, p.req); j >= 0 {
			level = append(level, j)
		}
	}
	c.sortLevel(level)
	c.drain(level)
}

// UsesTasks reports the build's UsesTasks fact as collected: true when std
// declares no UsesTasks (nothing was folded) or the program needs tasks.
func (in *Instances) UsesTasks() bool { return in.usesTasks }

// needsTasks walks the graph from the non-runtime roots (the file comment).
func (c *collector) needsTasks() bool {
	items := c.env.Items()
	keys := map[types.ItemID]bool{} // lookup only
	for _, k := range taskKeys {
		if id, ok := items.ByKey(k); ok {
			keys[id] = true
		}
	}
	var fields []bool
	if id, ok := items.ByKey(keyIoTable); ok {
		if sd, ok := items.Get(id).Decl.(*types.StructDecl); ok {
			in := c.env.Table().Interner()
			for _, f := range sd.Fields {
				fields = append(fields, taskFields[in.Str(f.Name)])
			}
		}
	}
	seen := make([]bool, len(c.nodes))
	stack := append([]int(nil), c.tg.userRoots...)
	for _, i := range stack {
		seen[i] = true
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := c.nodes[i]
		if keys[n.inst.Item] {
			return true
		}
		if b := n.inst.Body; b != nil {
			for bi := range b.Blocks {
				if t := &b.Blocks[bi].Term; isCall(t) && t.Spawn != mir.NoLocal { // a tail call never spawns
					return true
				}
			}
		}
		push := func(j int) {
			if !seen[j] {
				seen[j] = true
				stack = append(stack, j)
			}
		}
		for _, f := range c.tg.reads[i] {
			if f < len(fields) && fields[f] {
				return true
			}
			if f < len(c.tg.entries) {
				for _, j := range c.tg.entries[f] {
					push(j)
				}
			}
		}
		for _, j := range c.tg.edges[i] {
			push(j)
		}
	}
	return false
}

// foldSwitchesOn turns every switch on local p (no projection) into a goto
// to the arm of value v, when p's one definition is the folded call (now
// the assignment of v) and p is never borrowed. A parameter is never folded:
// its incoming argument is a definition no statement shows. It
// reports a fold.
func foldSwitchesOn(b *mir.Body, p mir.Place, v uint64) bool {
	if p.Proj.Len != 0 || isParam(b, p.Local) {
		return false
	}
	l := p.Local
	defs := 0
	for bi := range b.Blocks {
		blk := &b.Blocks[bi]
		for si := range blk.Stmts {
			st := &blk.Stmts[si]
			if st.Place.Local == l && st.Kind == mir.StAssign {
				defs++
			}
			if st.Rvalue.Place.Local == l && (st.Rvalue.Kind == mir.RvRef || st.Rvalue.Kind == mir.RvSlice) {
				return false
			}
		}
		if t := &blk.Term; isCall(t) && t.Dest.Local == l { // a tail call's Dest is _0
			defs++
		}
	}
	if defs != 1 {
		return false
	}
	folded := false
	// A copy or a negation of p is the constant too (`if !rt.UsesTasks()`).
	for bi := range b.Blocks {
		for si := range b.Blocks[bi].Stmts {
			st := &b.Blocks[bi].Stmts[si]
			rv := &st.Rvalue
			if st.Kind != mir.StAssign || (rv.A.Kind != mir.OpCopy && rv.A.Kind != mir.OpMove) ||
				rv.A.Place.Local != l || rv.A.Place.Proj.Len != 0 || st.Place.Local == l {
				continue
			}
			switch {
			case rv.Kind == mir.RvUse:
				*rv = mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, v)}
				folded = foldSwitchesOn(b, st.Place, v) || folded
			case rv.Kind == mir.RvUnary && rv.Un == mir.Not:
				*rv = mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1-v)}
				folded = foldSwitchesOn(b, st.Place, 1-v) || folded
			}
		}
	}
	for bi := range b.Blocks {
		t := &b.Blocks[bi].Term
		if t.Kind != mir.TermSwitch || (t.On.Kind != mir.OpCopy && t.On.Kind != mir.OpMove) ||
			t.On.Place.Local != l || t.On.Place.Proj.Len != 0 {
			continue
		}
		to := t.Default
		for ci := t.Cases.Start; ci < t.Cases.End(); ci++ {
			if b.Cases[ci].Value == v {
				to = b.Cases[ci].Target
			}
		}
		if to == mir.NoBlock {
			continue
		}
		*t = mir.Terminator{Kind: mir.TermGoto, Span: t.Span, Target: to}
		folded = true
	}
	return folded
}

// isParam reports a parameter local (1..ParamCount, hidden ones included).
func isParam(b *mir.Body, l mir.LocalID) bool {
	return l >= 1 && uint32(l) <= b.ParamCount
}

func b2u(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

// lazyKeys are the runtime roots codegen calls only for a construct that
// makes the fact true (s.go, a scope, a join, a cancel, a deadline's
// CancelAfter, Expired or Remaining: each is a taskKeys intrinsic) or,
// for the system-stack pair, for an extern call off std, which without them
// is a direct call (internal/codegen sysstack.go): with
// no task, Main already runs on a system stack. A program with no task
// construct, hello world, collects and links none of them.
var lazyKeys = map[string]bool{"std.task.Enter": true, "std.task.SpawnAlloc": true, "std.task.Spawn": true,
	"std.task.Join": true, "std.task.Cancel": true, "std.task.CancelAfter": true, "std.task.Expired": true,
	"std.task.Remaining": true, "std.task.SystemStack": true, "std.task.SystemStackDone": true}

// lazyRoot reports a root held back until the fact is decided true.
func (c *collector) lazyRoot(r Root) bool {
	return r.Kind == RootRuntime && c.opt.tasks.item != types.NoItem && !c.opt.tasks.value &&
		lazyKeys[c.env.Items().Get(r.Item).Key.String()]
}

// foldConstBools folds the switches on every bool local whose one
// definition left is an immediate and which is never borrowed or written
// through a projection: after a UsesTasks fold is pruned, the join of
// `rt.UsesTasks() && x` is such a local. It reports a fold.
func foldConstBools(b *mir.Body) bool {
	n := len(b.Locals)
	defs := make([]int, n)
	imm := make([]int, n) // 1 + the immediate of the local's last definition, 0 if not one
	bad := make([]bool, n)
	for bi := range b.Blocks {
		blk := &b.Blocks[bi]
		for si := range blk.Stmts {
			st := &blk.Stmts[si]
			if int(st.Place.Local) < n && st.Kind == mir.StAssign {
				l := st.Place.Local
				defs[l]++
				if st.Place.Proj.Len != 0 {
					bad[l] = true
				}
				imm[l] = 0
				if rv := &st.Rvalue; rv.Kind == mir.RvUse && rv.A.Kind == mir.OpImm && rv.A.Type == types.Bool {
					imm[l] = 1 + int(rv.A.Bits&1)
				}
			}
			if rv := &st.Rvalue; int(rv.Place.Local) < n && (rv.Kind == mir.RvRef || rv.Kind == mir.RvSlice) {
				bad[rv.Place.Local] = true
			}
		}
		if t := &blk.Term; isCall(t) && int(t.Dest.Local) < n { // a tail call's Dest is _0
			defs[t.Dest.Local]++
			imm[t.Dest.Local] = 0
		}
	}
	folded := false
	for l := 1; l < n; l++ {
		// A parameter's incoming argument is a definition too (a var
		// bool parameter assigned an immediate once is not that constant).
		if isParam(b, mir.LocalID(l)) {
			continue
		}
		if defs[l] == 1 && imm[l] != 0 && !bad[l] && b.Locals[l].Type == types.Bool {
			if foldSwitchesOn(b, mir.LocalPlace(mir.LocalID(l)), uint64(imm[l]-1)) {
				folded = true
			}
		}
	}
	return folded
}
