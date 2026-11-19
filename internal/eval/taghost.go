// Why: soundness tests exercise code that takes Io, spawns tasks and opens
// scopes, which plain evaluation refuses; the tagged mode needs a host that
// serves exactly those items and stays deterministic.
//
// What: the soundness host: hostParam (the Io and &Arena parameters
// RunTagged fills itself), tgHostCall (the Io items it serves), scopes
// (hostScope), tgSpawn, tgJoin, runTask, the test prelude (hostPrelude), and
// the unit's var globals (tgGlobal, tgGlobalsEnd).
//
// How: a task runs to completion once started, at a yield point the Seed
// stream picks, so the schedule is reproducible. Exactly the listed Io items
// are served; any other makes the run Skipped, never a silent refusal. A run
// holds at most 64 tasks. The race check is tagrace.go; this file calls its
// spawn and join edges.

package eval

import (
	"slices"
	"strings"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// maxTasks bounds a run's tasks: a 65th spawn is Skipped.
const maxTasks = 64

// joinPoint is a pending task's start when the seed chose the join.
const joinPoint = ^uint32(0)

// ttask is one spawned task.
type ttask struct {
	id     uint32
	scope  uint32
	owner  uint32 // the task that owns the scope
	cs     callSite
	at     source.Span
	args   []loc // the arguments, copied at the spawn into own allocations
	start  uint32
	state  uint8 // taskPending, taskRunning, taskDone
	spawnI int   // spawn order
}

const (
	taskPending uint8 = iota
	taskRunning
	taskDone
)

// tscope is one scope record.
type tscope struct {
	id        uint32
	owner     uint32
	cancelled bool
}

// tasking is the host's part of tagState.
type tasking struct {
	tasks  []*ttask
	scopes []*tscope // index 0 unused: a scope id is never 0
	cur    uint32    // the running task; 0 is the request's own
	yields map[uint32]uint32
	res    int32 // Acquire's counter
}

// next draws the next 64 bits of the run's Seed stream (splitmix64).
func (tg *tagState) next() uint64 {
	tg.rng += 0x9e3779b97f4a7c15
	z := tg.rng
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// failAlloc draws whether this page allocation fails (TagOptions.FailAlloc).
func (tg *tagState) failAlloc() bool {
	if !tg.armed || tg.opt.FailAlloc == 0 {
		return false
	}
	tg.frng += 0x9e3779b97f4a7c15
	z := tg.frng
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return (z^(z>>31))%uint64(tg.opt.FailAlloc) == 0
}

// hostParam fills a parameter of the request's outermost frame that the
// host provides: an Io over std's inert table, or an &Arena. It
// reports whether the parameter was one.
func (m *Machine) hostParam(f *frame, id mir.LocalID) bool {
	ty := f.local(id).ti.ty
	k := &m.cfg.Known
	switch {
	case ty.Kind == types.KNamed && ty.Item == k.Io && k.Io != types.NoItem:
		m.hostIoParam(id)
		return true
	case ty.Kind == types.KRef && k.Arena != types.NoItem && m.tab().Get(ty.Elem).Kind == types.KNamed && m.tab().Get(ty.Elem).Item == k.Arena:
		m.hostArenaParam(id)
		return true
	}
	return false
}

// hostArenaParam gives an &Arena parameter of the entry the arena
// Config.EvalArena makes (MakeEvalArena): its body runs as a frame above
// the entry's, as Machine.Arena runs it for a host, and its pages are own
// memory, so the arena's pointer carries the owner's tag.
func (m *Machine) hostArenaParam(id mir.LocalID) {
	if m.cfg.EvalArena == types.NoItem {
		m.skip("no EvalArena")
	}
	fi := len(m.frames) - 1
	if m.seg().arena == 0 {
		m.hostArena()
	}
	m.setVal(0)
	m.store(m.frames[fi].local(id), m.seg().arena)
}

// inertIoKey is std's InertIoValue(), the Io over the inert table
// (std/core/core_iodecl.mk).
var inertIoKey = types.KeyFromString("std.core.InertIoValue")

// hostIoParam gives an Io parameter of the entry the value InertIoValue()
// makes, run as a detached frame whose result lands in the parameter.
func (m *Machine) hostIoParam(id mir.LocalID) {
	item, ok := m.items().ByKey(inertIoKey)
	if !ok {
		m.skip("no InertIoValue")
	}
	fi := len(m.frames) - 1
	m.runDetached(item, nil, m.frames[fi].local(id), true)
}

// runDetached runs item's body with args (own allocations, or nil) as a
// detached frame above the top one, in a nested run of the loop (as
// hostArena does), its result copied into sink when hasSink.
func (m *Machine) runDetached(item types.ItemID, args []loc, sink loc, hasSink bool) {
	it := m.items().Get(item)
	at := m.curSpan()
	cs := m.resolve(types.FnRef{Item: item}, it, at)
	m.pushDetached(cs, at, args, sink, hasSink)
}

// pushDetached pushes cs's frame, copies args into its parameters, makes
// the entry retag, and runs it to its return.
func (m *Machine) pushDetached(cs callSite, at source.Span, args []loc, sink loc, hasSink bool) {
	floor := len(m.frames)
	f := m.pushFrame(cs.cd, cs.ce, cs.item, cs.onerr, at, 0)
	f.detached, f.sink, f.hasSink = true, sink, hasSink
	m.entering = types.NoItem
	for i, a := range args {
		m.copyValue(f.local(mir.LocalID(i+1)), a)
	}
	m.tgEnter(&m.frames[len(m.frames)-1])
	pre, preFrame := m.preBlock, m.preFrame
	m.preBlock = nil
	st := m.runLoop(floor)
	m.preBlock, m.preFrame = pre, preFrame
	if st != nil {
		panic(st)
	}
}

// starts is the schedule taken (TaggedResult.Starts): one yield point per
// spawned task, in spawn order.
func (tg *tagState) starts() []uint32 { return tg.startList }

// tgHostCall serves the host's items at a call of fn and reports
// whether it did: IoSleep and IoYield (yield points; the table's other
// entries make the run Skipped), the contended lock (Skipped: would block),
// Io.Scope and its forms, Scope.Cancel, the deadline intrinsics
// Scope.CancelAfter, Expired and Remaining, and the test prelude's
// bodiless items. A bodiless function that takes Io and is none of these
// is Skipped.
func (m *Machine) tgHostCall(f *frame, t *mir.Terminator, fn types.FnRef, it *types.Item, fd *types.FuncDecl) bool {
	name := m.cfg.Interner.Str(it.Name)
	md := m.items().Module(it.Module)
	std := md != nil && md.Std
	key := it.Key.String()
	switch {
	case key == "std.core.IoSleep" || key == "std.core.IoYield":
		m.yieldPoint(m.tg.task.cur)
		dst := m.dest(f, t)
		m.tgZero(dst) // IoResult(value: 0, err: SysErr.None, os: 0)
		if m.tgCancelled() {
			m.tgCanceledResult(dst)
		}
	case key == "std.core.IsNative":
		// A tagged run's Io is InertIoValue's, never std.task's native
		// table, and IsNative's body reads a std var global (refused):
		// false, so CancelPoint takes its full check through IoSleep.
		m.tgZero(m.dest(f, t))
	case key == "std.sync.MutexLockSlow" || key == "std.core.MutexLockSlow":
		m.skip("would block")
	case std && strings.HasPrefix(name, "Inert") && it.Key != inertIoKey:
		// The inert table's entries are I/O. InertIoValue() itself
		// only makes the Io value (std's text writers over a buffer call it,
		// as Arena.Fmt does), so its body runs.
		m.skip("io " + strings.ToLower(strings.TrimPrefix(name, "Inert")))
	case fd.Bodiless && std && it.Owner != types.NoItem && m.ownerIs(it, "Io") && (name == "Scope" || name == "ScopeOf" || name == "ScopeWith"):
		m.hostScope(f, t)
	case fd.Bodiless && std && m.ownerIs(it, "Scope") && name == "Cancel":
		if s := m.scopeOf(f, m.argOp(f, t, 0)); s != nil {
			s.cancelled = true
		}
	case fd.Bodiless && std && m.ownerIs(it, "Scope") && name == "CancelAfter":
		// A scope here has no clock, so a deadline arms nothing.
		m.scopeOf(f, m.argOp(f, t, 0))
	case fd.Bodiless && std && m.ownerIs(it, "Scope") && (name == "Expired" || name == "Remaining"):
		// No deadline ever fires here, so Expired is false and
		// Remaining is none (a none optional is all zero bytes, AggNone).
		m.scopeOf(f, m.argOp(f, t, 0))
		m.tgZero(m.dest(f, t))
	case fd.Bodiless && !std:
		return m.hostPrelude(f, t, it, name)
	case fd.Bodiless && m.takesIo(&fd.Sig):
		m.skip("io " + name)
	default:
		return false
	}
	f.block, f.stmt = t.Target, 0
	return true
}

// tgCancelled reports whether the running task belongs to a cancelled scope:
// host Io entries then return Canceled to it. The request's own
// task belongs to no scope.
func (m *Machine) tgCancelled() bool {
	tk := &m.tg.task
	if tk.cur == 0 || int(tk.cur) > len(tk.tasks) {
		return false
	}
	s := tk.tasks[tk.cur-1].scope
	return int(s) < len(tk.scopes) && tk.scopes[s] != nil && tk.scopes[s].cancelled
}

// sysErrKey is std's SysErr (std/core/core_iodecl.mk).
var sysErrKey = types.KeyFromString("std.core.SysErr")

// tgCanceledResult writes SysErr.Canceled into the err field of a host Io
// result dst (IoResult's field of type SysErr). A result with no such field,
// or a std without SysErr.Canceled, keeps its zero bytes.
func (m *Machine) tgCanceledResult(dst loc) {
	se, ok := m.items().ByKey(sysErrKey)
	if !ok {
		return
	}
	ed, _ := m.items().Get(se).Decl.(*types.EnumDecl)
	if ed == nil {
		return
	}
	discr, found := uint64(0), false
	for _, v := range ed.Variants {
		if m.cfg.Interner.Str(v.Name) == "Canceled" {
			discr, found = v.Discr, true
		}
	}
	if !found || dst.ti.layout == nil {
		return
	}
	for _, fl := range dst.ti.layout.Fields {
		if ft := m.tab().Get(fl.Type); ft.Kind == types.KNamed && ft.Item == se {
			fd := m.at(dst, fl.Offset, fl.Type)
			fd.tag = dst.tag
			m.writeTag(fd, fd.ti.layout, discr)
			return
		}
	}
}

// tgGlobal is a var global's storage in a tagged run: for a global
// of the unit's own modules, an allocation of the run written from the
// global's value at its first reference; its memory is own memory, so the
// reference carries the owner's tag. A std var global stays refused,
// and a run that reaches one is Skipped, as is a global with no
// value (an extern var, or a poisoned initializer), except the ones of
// tgStdGlobals.
func (m *Machine) tgGlobal(it *types.Item) uint32 {
	key := it.Key.String()
	if md := m.items().Module(it.Module); md == nil || md.Std && !tgStdGlobals[key] {
		m.skip("std global " + key)
	}
	d, _ := it.Decl.(*types.GlobalDecl)
	if d == nil || d.Extern {
		m.skip("global " + key)
	}
	v := types.NoConst
	if d.HasValue {
		v = d.Value
	} else if c := m.cfg.Program.Const(it.ID); !c.Poisoned {
		v = c.Value
	}
	if v == types.NoConst {
		m.skip("global " + key + " has no value")
	}
	ti := m.ti(d.Type)
	a := m.mem.newAlloc(allocOwn, ti.size)
	m.globals[it.ID] = a
	m.copyValue(loc{a: a, ti: ti}, loc{a: m.constAlloc(v), ti: ti})
	return a
}

// tgStdGlobals are the std var globals a tagged run serves as a unit's: a
// run-local allocation written from the initializer, so the run behaves as a
// fresh process. Each holds no pointer and decides no value a program can
// read. std.mem.PoolIds is the counter Pool.New takes each pool's generation
// base from, which only a Handle's private ver field holds.
var tgStdGlobals = map[string]bool{"std.mem.PoolIds": true}

// tgGlobalsEnd ends the run's unit globals after the entry returns: a unit
// global is still in scope when the entry returns, so every
// pointer word a global allocation of the run holds is read once, through
// the tag it carries. A frame's loan that reached a global, which E0605
// forbids, is then a read of a location whose lifetime ended, as
// the next call that read the global would make it. Plain mode never
// allocates a global, so it never runs this.
func (m *Machine) tgGlobalsEnd() {
	ids := make([]types.ItemID, 0, len(m.globals))
	for id := range m.globals {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		a := m.globals[id]
		if !m.mem.isLive(a) {
			continue
		}
		al := m.mem.at(a)
		if al.ptr == nil {
			continue
		}
		for o := uint32(0); o+8 <= uint32(len(al.bytes)); o++ {
			if !bitAt(al.ptr, o) {
				continue
			}
			p := types.LoadUint(al.bytes[o:], 8)
			if ta := uint32(p >> 32); ta != 0 {
				m.tacc(ta, uint32(p), 1, al.ptag[o], accRead)
			}
		}
	}
}

// ownerIs reports whether item it is a method of the type named owner.
func (m *Machine) ownerIs(it *types.Item, owner string) bool {
	if it.Owner == types.NoItem {
		return false
	}
	return m.cfg.Interner.Str(m.items().Get(it.Owner).Name) == owner
}

// tgZero writes zero bytes over l (a host result): one write access, then
// the bytes and their pointer bits cleared.
func (m *Machine) tgZero(l loc) {
	n := l.ti.size
	if n == 0 {
		return
	}
	m.tacc(l.a, l.off, n, l.tag, accWrite)
	al := m.mem.access(l.a, l.off, n, true)
	al.clearRange(l.off, uint32(n))
	clear(al.bytes[l.off : l.off+uint32(n)])
}

// hostScope makes a scope record (Io.Scope, ScopeOf, ScopeWith): the
// result's first word holds the scope's id (a Scope[E] keeps its Scope
// first, and its err word zero: success).
func (m *Machine) hostScope(f *frame, t *mir.Terminator) {
	tk := &m.tg.task
	if tk.scopes == nil {
		tk.scopes = []*tscope{nil}
	}
	s := &tscope{id: uint32(len(tk.scopes)), owner: tk.cur}
	tk.scopes = append(tk.scopes, s)
	dst := m.dest(f, t)
	m.tgZero(dst)
	m.store(m.at(dst, 0, types.U64), uint64(s.id))
}

// scopeOf reads the scope record a Scope operand or an &Scope one names.
func (m *Machine) scopeOf(f *frame, op *mir.Operand) *tscope {
	if op.Kind != mir.OpCopy && op.Kind != mir.OpMove {
		return nil
	}
	return m.scopeAt(m.place(f, op.Place))
}

func (m *Machine) scopeAt(l loc) *tscope {
	if l.ti.ty.Kind == types.KRef {
		w := m.load(l)
		el := m.ti(l.ti.ty.Elem)
		l = loc{a: ptrAlloc(w), off: ptrOff(w), ti: el, tag: m.takeVal()}
	}
	id := m.load(m.at(l, 0, types.U64))
	tk := &m.tg.task
	if id == 0 || int(id) >= len(tk.scopes) {
		m.skip("scope")
	}
	return tk.scopes[id]
}

// tgSpawn is a TermCall with Spawn set: the arguments are
// evaluated here, in the parent, into a block of their own; the task is
// made, the spawn is a yield point of the scope's owner, and the Seed
// stream chooses the task's start: now (one in three), the j-th next yield
// point with j uniform in 1 to 4 (one in three), or the join.
func (m *Machine) tgSpawn(f *frame, t *mir.Terminator) {
	tk := &m.tg.task
	if len(tk.tasks) >= maxTasks {
		m.skip("tasks")
	}
	s := m.scopeAt(m.place(f, mir.Place{Local: t.Spawn}))
	var fn types.FnRef
	switch t.Callee.Kind {
	case mir.CallDirect:
		fn = m.fnRef(f, t.Callee.Fn)
	case mir.CallValue:
		fn = m.fnOf(m.scalar(f, &t.Callee.Value))
	default:
		m.skip("spawn of a builtin")
	}
	it := m.items().Get(fn.Item)
	if fd, ok := it.Decl.(*types.FuncDecl); !ok || fd.Bodiless {
		m.skip("spawn of a bodiless function")
	}
	at := f.curSpan()
	cs := m.resolve(fn, it, at)
	m.entering = types.NoItem
	args := f.code.body.Args[t.Args.Start:t.Args.End()]
	task := &ttask{id: uint32(len(tk.tasks) + 1), scope: s.id, owner: s.owner, cs: cs, at: at, spawnI: len(tk.tasks)}
	for i := range args {
		ti := cs.cd.localTI[i+1]
		a := m.mem.newAlloc(allocOwn, uint64(ti.size))
		dst := loc{a: a, off: 0, ti: ti}
		m.writeOp(f, &args[i].Operand, dst)
		if args[i].Mode == mir.ArgMove && args[i].Operand.Kind == mir.OpCopy {
			m.tgMoved(f, args[i].Operand.Place, m.place(f, args[i].Operand.Place))
		}
		task.args = append(task.args, dst)
	}
	tk.tasks = append(tk.tasks, task)
	f.block, f.stmt = t.Target, 0
	if tk.yields == nil {
		tk.yields = map[uint32]uint32{}
	}
	switch m.tg.next() % 3 {
	case 0:
		task.start = tk.yields[task.owner] + 1 // this spawn's own point
	case 1:
		task.start = tk.yields[task.owner] + 1 + uint32(m.tg.next()%4) + 1
	default:
		task.start = joinPoint
	}
	m.raceSpawn(tk.cur, task.id)
	m.yieldPoint(task.owner)
}

// yieldPoint is a yield point of task owner: its count moves on, and every
// pending task of a scope it owns that chose this point starts, in spawn
// order.
func (m *Machine) yieldPoint(owner uint32) {
	tk := &m.tg.task
	if tk.yields == nil {
		tk.yields = map[uint32]uint32{}
	}
	tk.yields[owner]++
	p := tk.yields[owner]
	for _, task := range tk.tasks {
		if task.state == taskPending && task.owner == owner && task.start == p {
			m.runTask(task)
		}
	}
}

// runTask runs task to completion: a detached frame of its callee
// above the current frames, its arguments copied in with their tags.
func (m *Machine) runTask(task *ttask) {
	tg := m.tg
	task.state = taskRunning
	tg.startList = append(tg.startList, tg.task.yields[task.owner])
	prev := tg.task.cur
	tg.task.cur = task.id
	m.pushDetached(task.cs, task.at, task.args, loc{}, false)
	tg.task.cur = prev
	task.state = taskDone
	for _, a := range task.args {
		m.mem.free(a.a)
	}
}

// tgJoin is StJoin of scope local x: with cancel, the scope is
// cancelled first; then every pending task of the scope starts in spawn
// order, including tasks spawned meanwhile, until none is left. The join
// is a yield point of the scope's owner.
func (m *Machine) tgJoin(f *frame, x mir.LocalID, cancel bool) {
	s := m.scopeAt(m.place(f, mir.Place{Local: x}))
	if cancel {
		s.cancelled = true
	}
	m.yieldPoint(s.owner)
	for again := true; again; {
		again = false
		for _, task := range m.tg.task.tasks {
			if task.state == taskPending && task.scope == s.id {
				m.runTask(task)
				again = true
			}
		}
	}
	for _, task := range m.tg.task.tasks {
		if task.scope == s.id && task.state == taskDone {
			m.raceJoin(m.tg.task.cur, task.id) // the join orders the task before what follows
		}
	}
}

// hostPrelude serves the test prelude's bodiless items: Cond() from
// TagOptions.Cond or the Seed stream; Acquire(name) a fresh Res; Res.Close
// and Take consume their argument; MakePoint() a zero Point; Exchange swaps
// its referents with two write accesses through the arguments' tags. It
// reports whether name is one.
func (m *Machine) hostPrelude(f *frame, t *mir.Terminator, it *types.Item, name string) bool {
	switch name {
	case "Cond":
		v := uint64(0)
		switch m.tg.opt.Cond {
		case 1:
			v = 1
		case -1:
		default:
			v = m.tg.next() & 1
		}
		m.store(m.dest(f, t), v)
	case "Acquire":
		dst := m.dest(f, t)
		m.tgZero(dst)
		m.tg.task.res++
		m.store(m.at(dst, 0, types.I32), uint64(m.tg.task.res))
	case "Close", "Take":
		m.tgMovedArgs(f, t)
	case "MakePoint":
		m.tgZero(m.dest(f, t))
	case "Exchange":
		a, b := m.argPtr(f, t, 0), m.argPtr(f, t, 1)
		tmp := m.mem.newAlloc(allocOwn, uint64(a.ti.size))
		tl := loc{a: tmp, ti: a.ti}
		m.copyValue(tl, a)
		m.copyValue(a, b)
		m.copyValue(b, tl)
		m.mem.free(tmp)
	default:
		return false
	}
	f.block, f.stmt = t.Target, 0
	return true
}

// argPtr is the referent of call argument i, a mut T, with the argument's
// tag.
func (m *Machine) argPtr(f *frame, t *mir.Terminator, i int) loc {
	op := m.argOp(f, t, i)
	ti := m.operandTI(f, op)
	w := m.scalar(f, op)
	tag := m.takeVal()
	return loc{a: ptrAlloc(w), off: ptrOff(w), ti: m.ti(ti.ty.Elem), tag: tag}
}
