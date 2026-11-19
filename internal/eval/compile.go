// Why: interpreting MIR straight from the tree would redo type and layout
// lookups on every step; each body is prepared once and then shared by every
// evaluation that runs it.
//
// What: tinfo (what the interpreter needs of a type), compiled (a body ready
// to run: frame offsets, frame size, flat blocks with their static step
// costs), compiledOf, compiledFor, ready, and byValue, the by-value walk that
// decides whether a type needs a same-module layout.
//
// How: a compiled body lists its types at once and fills its layout-dependent
// parts on first entry under a mutex, publishing them only when complete, so
// a panic while filling leaves the body unfilled rather than half-built. The
// stage rule is checked on every type before its layout is read.

package eval

import (
	"math"
	"sync"
	"sync/atomic"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// tclass is how the interpreter moves a value of a type.
type tclass uint8

const (
	// cVoid: size 0, nothing moves.
	cVoid tclass = iota
	// cInt: an integer, or the tag of a payload-free enum (unsigned).
	cInt
	cFloat
	cBool
	cChar
	// cPtr: one pointer word: &T, mut T, *T, fn, and the niche optionals
	// ?&T, ?mut T, ?fn (null is none).
	cPtr
	// cMem: everything else, moved as bytes with their tags.
	cMem
)

// tinfo is what the interpreter needs of one concrete type: its layout, its
// class, and for scalars the width. Immutable and shared once built.
type tinfo struct {
	id     types.TypeID
	ty     *types.Type
	layout *types.Layout
	hash   source.Hash
	size   uint64
	align  uint32
	class  tclass
	width  int // bytes, for cInt, cFloat, cBool, cChar, cPtr
	signed bool
	// enum is set for a payload-free enum (class cInt): the tag check reads
	// the variants of layout.
	enum bool
}

// isScalar reports whether values of the class travel as a uint64.
func (ti *tinfo) isScalar() bool { return ti.class != cMem && ti.class != cVoid }

// c is the byte charge of n bytes: ceil(n/16), c(0) = 0, for
// any n a length word can hold.
func c(n uint64) uint64 { return n/16 + b2u(n%16 != 0) }

// tinfo returns the shared info of a concrete type. The caller has applied
// the stage rule to t (machine.ti), so Program.Layout may be asked.
func (e *Evaluator) tinfo(t types.TypeID) *tinfo {
	e.tiMu.RLock()
	ti := e.ti[t]
	e.tiMu.RUnlock()
	if ti != nil {
		return ti
	}
	ti = e.buildTinfo(t)
	e.tiMu.Lock()
	defer e.tiMu.Unlock()
	if old := e.ti[t]; old != nil {
		return old
	}
	e.ti[t] = ti
	return ti
}

func (e *Evaluator) buildTinfo(t types.TypeID) *tinfo {
	tab := e.cfg.Types
	ty := tab.Get(t)
	l, h := e.cfg.Program.Layout(t)
	if l == nil {
		source.ICEf(source.Span{}, "eval: no layout for %s", tab.TypeString(t, nil))
	}
	ti := &tinfo{id: t, ty: ty, layout: l, hash: h, size: l.Shape.Size, align: l.Shape.Align, class: cMem}
	switch ty.Kind {
	case types.KVoid, types.KNever:
		ti.class = cVoid
	case types.KInt, types.KIntPtr:
		ti.class, ti.width, ti.signed = cInt, int(l.Shape.Size), ty.Signed
	case types.KFloat:
		ti.class, ti.width = cFloat, int(l.Shape.Size)
	case types.KBool:
		ti.class, ti.width = cBool, 1
	case types.KChar:
		ti.class, ti.width = cChar, 4
	case types.KRef, types.KMut, types.KRawPtr, types.KFn:
		ti.class, ti.width = cPtr, 8
	case types.KOptional:
		if types.NicheOptional(tab.Kind(ty.Elem)) {
			ti.class, ti.width = cPtr, 8
		}
	case types.KNamed:
		if e.cfg.Items.Get(ty.Item).Kind == types.KindEnum && l.HasTag && payloadFree(l) {
			ti.class, ti.width, ti.enum = cInt, int(l.Tag.Size), true
		}
	}
	if ti.class != cMem && ti.class != cVoid {
		switch ti.width {
		case 1, 2, 4, 8:
		default:
			source.ICEf(source.Span{}, "eval: scalar %s of %d bytes", tab.TypeString(t, nil), ti.width)
		}
		if uint64(ti.width) != ti.size {
			source.ICEf(source.Span{}, "eval: scalar %s has size %d, width %d", tab.TypeString(t, nil), ti.size, ti.width)
		}
	}
	return ti
}

func payloadFree(l *types.Layout) bool {
	for _, v := range l.Variants {
		if len(v.Fields) > 0 {
			return false
		}
	}
	return l.Tag.Offset == 0
}

// walkEntry is one item a by-value walk of a type reaches: a named struct or
// enum (Type set) or an error of an error set (Type NoType).
type walkEntry struct {
	item types.ItemID
	t    types.TypeID
}

// byValue lists the items a by-value walk of t reaches, in walk order:
// KNamed and its generic arguments' by-value parts, record fields,
// [N]T, ?T, E!T and error-set members; &T, mut T, *T, []T, mut []T, str and
// fn stop the walk. A named item's declared fields are not walked: in a
// strictly lower module they name only lower types and parameters, and the
// arguments are walked; a same-module item is itself the stop.
func (e *Evaluator) byValue(t types.TypeID) []walkEntry {
	e.tiMu.RLock()
	w, ok := e.walk[t]
	e.tiMu.RUnlock()
	if ok {
		return entriesOf(w)
	}
	tab := e.cfg.Types
	var out []walkEntry
	seen := map[types.TypeID]bool{}
	seenErr := map[types.ItemID]bool{}
	var visit func(t types.TypeID)
	visit = func(t types.TypeID) {
		if t == types.NoType || seen[t] {
			return
		}
		seen[t] = true
		ty := tab.Get(t)
		switch ty.Kind {
		case types.KNamed:
			out = append(out, walkEntry{item: ty.Item, t: t})
			for _, a := range ty.Args {
				if !a.IsConst {
					visit(a.Type)
				}
			}
		case types.KArray, types.KOptional:
			visit(ty.Elem)
		case types.KErrorUnion, types.KErrorSet:
			visit(ty.Elem)
			for _, m := range tab.Members(ty.Errors) {
				if !m.IsParam && !seenErr[m.Item] {
					seenErr[m.Item] = true
					out = append(out, walkEntry{item: m.Item})
				}
			}
		case types.KRecord:
			for _, f := range ty.Fields {
				visit(f.Type)
			}
		}
	}
	visit(t)
	enc := make([]types.TypeID, 0, 2*len(out))
	for _, x := range out {
		enc = append(enc, types.TypeID(x.item), x.t)
	}
	e.tiMu.Lock()
	e.walk[t] = enc
	e.tiMu.Unlock()
	return out
}

func entriesOf(enc []types.TypeID) []walkEntry {
	out := make([]walkEntry, len(enc)/2)
	for i := range out {
		out[i] = walkEntry{item: types.ItemID(enc[2*i]), t: enc[2*i+1]}
	}
	return out
}

// compiled is one body prepared for running, shared by every evaluation and
// immutable once ready. types needs no layout and is ready at once; the rest
// is filled once (layouts) by the first evaluation the stage rule lets in.
type compiled struct {
	body *mir.Body
	// types are every type the body's values have (locals, projections,
	// operands, rvalue types), in first-seen order: the layouts its first
	// entry per segment reads. A callee's generic arguments
	// are not among them: an instance body or an intrinsic reads them.
	types []types.TypeID

	// mu and done guard the layout-dependent parts, filled once. done is set
	// only when they are complete, so a panic while filling them (an ICE)
	// leaves the body unfilled, and a later entry fills it again rather than
	// running a half-built body.
	mu       sync.Mutex
	done     atomic.Bool
	localOff []uint32
	localTI  []*tinfo
	// projTI is the type info after each step of body.Projections.
	projTI    []*tinfo
	frameSize uint64
	// blocks are the body's blocks in flat form with their static costs
	// (fast.go).
	blocks []cblock
	// slow: the tagged mode's form, every statement and terminator on the
	// general path and no block charged once.
	slow bool
}

// compiledOf returns the shared compiled form of a body (types only; ready
// fills the rest).
func (e *Evaluator) compiledOf(b *mir.Body) *compiled {
	e.codeMu.Lock()
	defer e.codeMu.Unlock()
	if cd := e.code[b]; cd != nil {
		return cd
	}
	cd := &compiled{body: b, types: bodyTypes(e.cfg.Types, b)}
	e.code[b] = cd
	return cd
}

// compiledFor is compiledOf for the tagged mode when tagged is set: a form
// of its own, shared by tagged runs, built as noFlat builds blocks.
func (e *Evaluator) compiledFor(b *mir.Body, tagged bool) *compiled {
	if !tagged {
		return e.compiledOf(b)
	}
	e.codeMu.Lock()
	defer e.codeMu.Unlock()
	if cd := e.tcode[b]; cd != nil {
		return cd
	}
	if e.tcode == nil {
		e.tcode = make(map[*mir.Body]*compiled)
	}
	cd := &compiled{body: b, types: bodyTypes(e.cfg.Types, b), slow: true}
	e.tcode[b] = cd
	return cd
}

// bodyTypes lists the distinct types a body mentions, in first-seen order.
func bodyTypes(tab *types.Table, b *mir.Body) []types.TypeID {
	var out []types.TypeID
	seen := map[types.TypeID]bool{}
	add := func(t types.TypeID) {
		if t != types.NoType && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, l := range b.Locals {
		add(l.Type)
	}
	for _, p := range b.Projections {
		add(p.Type)
	}
	for _, o := range b.Operands {
		add(b.OperandType(o))
	}
	for _, a := range b.Args {
		add(b.OperandType(a.Operand))
	}
	for i := range b.Blocks {
		bl := &b.Blocks[i]
		for j := range bl.Stmts {
			st := &bl.Stmts[j]
			if st.Kind != mir.StAssign {
				continue
			}
			r := &st.Rvalue
			for _, o := range []*mir.Operand{&r.A, &r.B} {
				if o.Kind == mir.OpConst || o.Kind == mir.OpImm {
					add(o.Type)
				}
			}
			switch r.Kind {
			case mir.RvCast, mir.RvAggregate, mir.RvSizeOf, mir.RvAlignOf, mir.RvFieldCount, mir.RvConstArg:
				add(r.Type)
			}
			if r.Len.Kind == types.CASizeOf {
				add(r.Len.Type)
			}
		}
		t := &bl.Term
		for _, o := range []*mir.Operand{&t.On, &t.Msg, &t.Callee.Value} {
			if o.Kind == mir.OpConst || o.Kind == mir.OpImm {
				add(o.Type)
			}
		}
	}
	return out
}

// maxFrame caps a frame size so that sums of frame sizes cannot wrap; any
// frame above maxAlloc is the stack limit panic when it is pushed.
const maxFrame = math.MaxUint64 / 4

// satAdd is x + y, saturated at maxFrame.
func satAdd(x, y uint64) uint64 {
	if y >= maxFrame || x >= maxFrame-y {
		return maxFrame
	}
	return x + y
}

// ready fills the layout-dependent parts once. Every type in cd.types has
// passed the stage rule in the calling evaluation. A frame of any size is
// filled: one too large to push is refused by pushFrame as the stack limit
// panic, not here; its offsets beyond 32 bits are never used.
func (e *Evaluator) ready(cd *compiled) {
	if cd.done.Load() {
		return
	}
	cd.mu.Lock()
	defer cd.mu.Unlock()
	if cd.done.Load() {
		return
	}
	{
		b := cd.body
		cd.localOff = make([]uint32, len(b.Locals))
		cd.localTI = make([]*tinfo, len(b.Locals))
		var off uint64
		for i, l := range b.Locals {
			ti := e.tinfo(l.Type)
			cd.localTI[i] = ti
			off = min(alignUp(off, uint64(max(ti.align, 1))), maxFrame)
			cd.localOff[i] = uint32(off)
			off = satAdd(off, ti.size)
		}
		cd.projTI = make([]*tinfo, len(b.Projections))
		for i, p := range b.Projections {
			cd.projTI[i] = e.tinfo(p.Type)
		}
		cd.frameSize = satAdd(alignUp(off, 16), 16) // locals rounded up to 16, plus 16
		cd.blocks = make([]cblock, len(b.Blocks))
		for i := range b.Blocks {
			bl := &b.Blocks[i]
			cs := make([]uint64, len(bl.Stmts)+1)
			for j := range bl.Stmts {
				cs[j] = 1
				if st := &bl.Stmts[j]; st.Kind == mir.StAssign {
					cs[j] += c(e.tinfo(b.PlaceType(st.Place)).size)
				}
			}
			tc := uint64(1)
			trace := false
			switch bl.Term.Kind {
			case mir.TermCall, mir.TermTailCall:
				// A tail call costs what a call does; only a call
				// is a trace hook's.
				if bl.Term.Kind == mir.TermCall && bl.Term.Callee.Kind == mir.CallDirect && traceHook(e.cfg.Items, bl.Term.Callee.Fn.Item) {
					trace = true
					tc = 0
					break
				}
				var n uint64
				for _, a := range b.Args[bl.Term.Args.Start:bl.Term.Args.End()] {
					n += e.tinfo(b.OperandType(a.Operand)).size
				}
				tc += c(n)
			case mir.TermReturn:
				tc += c(cd.localTI[mir.ReturnLocal].size)
			}
			cs[len(bl.Stmts)] = tc
			ps := make([]uint64, len(cs)+1)
			for j, x := range cs {
				ps[j+1] = ps[j] + x
			}
			cb := &cd.blocks[i]
			cb.cost, cb.prefix, cb.total, cb.pre = cs, ps, ps[len(cs)], true
			cb.trace = trace
			cb.ins = make([]instr, len(bl.Stmts))
			for j := range bl.Stmts {
				st := &bl.Stmts[j]
				if st.Kind == mir.StAssign && (st.Rvalue.Kind == mir.RvConstRef || st.Rvalue.Kind == mir.RvGlobalRef) {
					cb.pre = false
				}
				if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvBinary && e.cfg.Types.Kind(b.OperandType(st.Rvalue.A)) == types.KStr {
					cb.pre = false // strCompare charges when it runs
				}
				cb.ins[j] = e.flatStmt(cd, st)
			}
			cb.term = e.flatTerm(cd, &bl.Term)
			if e.noFlat || cd.slow {
				clear(cb.ins)
				cb.term, cb.pre = tinstr{}, false
			}
		}
	}
	cd.done.Store(true)
}

func alignUp(x, a uint64) uint64 {
	if a <= 1 {
		return x
	}
	return (x + a - 1) / a * a
}
