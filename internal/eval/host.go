// Why: the generator API (quote, reading and building code) is not eval's
// own; the gen package supplies it as host functions, and those must reach
// evaluation memory without weakening the interpreter's checks.
//
// What: the Host interface, HostFunc, Call and Value, hostCall, and the
// Machine methods a host reads and writes memory through, among them
// Target and Arena.
//
// How: arguments are copied into allocations that live for the call only,
// so a host keeps handles, never a Value. Every Machine method a host uses
// checks its access as the interpreter's own accesses are checked. A host
// call is one terminator, charged one step plus whatever the host charges.

package eval

import (
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// Host implements intrinsics an evaluation needs beyond eval's own: the
// generator API. eval looks a bodiless intrinsic up in its own table first,
// then asks the host by the callee's stable key (BuiltinQuote under the key
// "#quote").
type Host interface {
	Intrinsic(key types.Key) (HostFunc, bool)
}

// HostFunc runs one call. It reads its arguments and writes its result
// through m; it may charge extra steps. A non-nil error ends the evaluation
// (a host failure is an EvalPanic with PanicExplicit and a message, or an
// EvalRefused).
type HostFunc func(m *Machine, c *Call) *types.EvalError

// Call is one intrinsic call: callee, argument values, the call's span. The
// argument values are copies that live for the call only (freed when the
// HostFunc returns): a host keeps handles, never a Value of Args.
type Call struct {
	Callee types.ItemID
	Args   []Value
	Result Value // the destination, of the callee's result type
	Span   source.Span
}

// Value is a typed location in evaluation memory. Hosts read and write it
// only through Machine.
type Value struct {
	Type  types.TypeID
	alloc uint32
	off   uint32
}

func (m *Machine) loc(v Value) loc { return loc{a: v.alloc, off: v.off, ti: m.ti(v.Type)} }

// hostCall runs a host function: arguments are copied into own allocations,
// which live for the call only (freed after it, so a host call leaves dead
// records only), the result goes to t.Dest, one step is charged. f is the
// top frame; the host may push frames above it (Arena), so it is found again
// by index afterwards.
func (m *Machine) hostCall(f *frame, t *mir.Terminator, fn types.FnRef, hf HostFunc) {
	fi := len(m.frames) - 1
	b := f.code.body
	args := b.Args[t.Args.Start:t.Args.End()]
	c := &Call{Callee: fn.Item, Span: f.curSpan()}
	for i := range args {
		ti := m.operandTI(f, &args[i].Operand)
		a := m.mem.newAlloc(allocOwn, max(ti.size, 1))
		m.writeOp(f, &args[i].Operand, loc{a: a, ti: ti})
		c.Args = append(c.Args, Value{Type: ti.id, alloc: a})
	}
	dst := m.place(f, t.Dest)
	c.Result = Value{Type: dst.ti.id, alloc: dst.a, off: dst.off}
	m.charge(1)
	if err := hf(m, c); err != nil {
		panic(&stop{err: err})
	}
	for _, v := range c.Args {
		m.mem.free(v.alloc)
	}
	f = &m.frames[fi]
	f.block, f.stmt = t.Target, 0
}

// Int reads any integer type, sign-extended.
func (m *Machine) Int(v Value) int64 {
	l := m.loc(v)
	x := m.load(l)
	if l.ti.signed {
		return int64(signExtend(x, l.ti.width))
	}
	return int64(x)
}

// Uint reads any integer type, zero-extended.
func (m *Machine) Uint(v Value) uint64 { return m.load(m.loc(v)) }

// Bool reads a bool.
func (m *Machine) Bool(v Value) bool { return m.load(m.loc(v)) != 0 }

// Char reads a char.
func (m *Machine) Char(v Value) rune { return rune(m.load(m.loc(v))) }

// Str reads a str value's bytes.
func (m *Machine) Str(v Value) (s string, err *types.EvalError) {
	st := m.guard(func() {
		d, n := m.sliceWords(m.loc(v))
		s = string(m.readBytes(d, n, m.takeVal()))
	})
	if st != nil {
		return "", st.err
	}
	return s, nil
}

// Handle reads the u32 field of a handle struct.
func (m *Machine) Handle(v Value) uint32 {
	l := m.loc(v)
	return uint32(m.mem.access(l.a, l.off, 4, false).loadScalar(l.off, 4))
}

// Deref follows a &T or mut T value.
func (m *Machine) Deref(v Value) (out Value, err *types.EvalError) {
	st := m.guard(func() {
		l := m.loc(v)
		w := m.load(l)
		if w == 0 {
			refuse(types.RefuseRawAccess, "null")
		}
		out = Value{Type: l.ti.ty.Elem, alloc: ptrAlloc(w), off: ptrOff(w)}
	})
	if st != nil {
		return Value{}, st.err
	}
	return out, nil
}

// SetHandle writes the u32 field of a handle struct.
func (m *Machine) SetHandle(dst Value, h uint32) {
	l := m.loc(dst)
	m.mem.access(l.a, l.off, 4, true).storeScalar(l.off, 4, uint64(h))
}

// SetBool writes a bool.
func (m *Machine) SetBool(dst Value, b bool) { m.store(m.loc(dst), b2u(b)) }

// SetStr writes a str: s is copied into a fresh own allocation, charged.
func (m *Machine) SetStr(dst Value, s string) {
	m.charge(c(uint64(len(s))))
	l := m.loc(dst)
	var w uint64
	if len(s) > 0 {
		a := m.mem.newAlloc(allocOwn, uint64(len(s)))
		copy(m.mem.at(a).bytes, s)
		w = ptrWord(a, 0)
	}
	al := m.mem.access(l.a, l.off, 16, true)
	al.storePtr(l.off, w)
	al.storeScalar(l.off+8, 8, uint64(len(s)))
}

// SetUint writes an integer or a payload-free enum's tag, truncated to the
// type's width (the generator API's TypeKind and Receiver results).
func (m *Machine) SetUint(dst Value, x uint64) { m.store(m.loc(dst), x) }

// Copy copies the value at src into dst, charged ceil(size/16) steps as an
// assignment of its bytes is; pointer words keep their tags (gen's
// Gen.Arena hands out the run's arena value). The two types must have one
// size, else an ICE (a host's misuse).
func (m *Machine) Copy(dst, src Value) *types.EvalError {
	d, s := m.loc(dst), m.loc(src)
	if d.ti.size != s.ti.size {
		source.ICEf(source.Span{}, "eval: Machine.Copy of %d bytes into %d", s.ti.size, d.ti.size)
	}
	st := m.guard(func() {
		m.charge(c(d.ti.size))
		m.copyValue(d, s)
	})
	if st != nil {
		return st.err
	}
	return nil
}

// SetHandles writes a []T of handle structs of length len(hs) into a fresh
// own allocation, charged ceil(4·len/16) steps.
func (m *Machine) SetHandles(dst Value, elem types.TypeID, hs []uint32) {
	et := m.ti(elem)
	n := uint64(len(hs)) * et.size
	m.charge(c(n))
	l := m.loc(dst)
	var w uint64
	if len(hs) > 0 {
		a := m.mem.newAlloc(allocOwn, n)
		for i, h := range hs {
			m.mem.at(a).storeScalar(uint32(uint64(i)*et.size), 4, uint64(h))
		}
		w = ptrWord(a, 0)
	}
	al := m.mem.access(l.a, l.off, 16, true)
	al.storePtr(l.off, w)
	al.storeScalar(l.off+8, 8, uint64(len(hs)))
}

// Charge takes extra steps; E0902 when fuel runs out.
func (m *Machine) Charge(steps uint64) *types.EvalError {
	st := m.guard(func() { m.charge(steps) })
	if st != nil {
		return st.err
	}
	return nil
}

// Reads is the running segment's read set.
func (m *Machine) Reads() *types.ReadSet { return &m.seg().reads }

// Target is the build's target.
func (m *Machine) Target() types.Target { return m.cfg.Target }

// Arena is the running segment's EvalArena as a &Arena value: the same arena
// the MIR builtin EvalArena() gives, so one run has one arena, made on
// first use by whichever asks first. When a host asks first,
// Config.EvalArena's body runs here to completion (hostArena), charged as it
// is for the builtin apart from the call terminator, and a failure of it ends
// the evaluation as any failure inside the run does. The value's own word is
// a host result, charged c(8) steps.
func (m *Machine) Arena() (Value, *types.EvalError) {
	if v := m.seg().arenaVal; v.alloc != 0 {
		return v, nil
	}
	if m.cfg.EvalArena == types.NoItem {
		source.ICEf(source.Span{}, "eval: Machine.Arena with no Config.EvalArena function")
	}
	fd, ok := m.items().Get(m.cfg.EvalArena).Decl.(*types.FuncDecl)
	if !ok {
		source.ICEf(source.Span{}, "eval: Config.EvalArena is not a function")
	}
	if m.seg().arena == 0 {
		m.hostArena()
	}
	var v Value
	st := m.guard(func() {
		m.charge(c(8))
		a := m.mem.newAlloc(allocOwn, 8)
		m.mem.at(a).storePtr(0, m.seg().arena)
		v = Value{Type: fd.Sig.Result, alloc: a}
	})
	if st != nil {
		return Value{}, st.err
	}
	m.seg().arenaVal = v
	return v, nil
}

// hostArena builds the running segment's arena for a host's first Arena():
// Config.EvalArena's body runs as a frame above the host call's, in a nested
// run of the loop that ends when that frame returns. The host call
// is one terminator, so the nesting is one Go call deep, never more: a host
// asking for the arena while it is being made is an ICE. A stop inside the
// body propagates through the host to the run that called it, with the
// arena's frames in its trace.
func (m *Machine) hostArena() {
	if m.seg().makingArena {
		source.ICEf(source.Span{}, "eval: Machine.Arena while Config.EvalArena runs")
	}
	it := m.items().Get(m.cfg.EvalArena)
	at := m.frames[len(m.frames)-1].curSpan()
	cs := m.resolve(types.FnRef{Item: it.ID}, it, at)
	if cs.cd.body.ParamCount != 0 {
		source.ICEf(at, "eval: Config.EvalArena %s has parameters", it.Key)
	}
	floor := len(m.frames)
	f := m.pushFrame(cs.cd, cs.ce, cs.item, cs.onerr, at, 0)
	f.arena, f.hostArena = true, true
	m.entering = types.NoItem
	pre, preFrame := m.preBlock, m.preFrame
	m.preBlock = nil
	m.seg().makingArena = true
	st := m.runLoop(floor)
	m.seg().makingArena = false
	m.preBlock, m.preFrame = pre, preFrame
	if st != nil {
		panic(st)
	}
}
