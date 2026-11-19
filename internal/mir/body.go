// Why: MIR's control flow (error edges, exits, calls, spawns, loops and
// defers) carries the facts borrow, eval and codegen act on, so its shapes
// are fixed here once for every reader.
//
// What: ErrorEdge and its kinds, ExitKind (RunsOnerr, IsE0411Exit), Arg and
// ArgMode, Callee and the builtins, Terminator and TermKind, Block and
// BlockOrigin, LoopInfo, DeferInfo, Body with its builders, and
// Body.CFGChecksum.
//
// How: cleanup code is copied per exit edge, and BlockOrigin marks the
// copies. Variable-length parts are Ranges into the Body's side slices.
// CFGChecksum hashes only the block structure (terminator kinds and
// successors), so PGO profiles survive edits that keep the shape.

package mir

import (
	"mk/internal/source"
	"mk/internal/types"
)

// ErrorEdgeKind says which construct consumes a fallible call's error
// (E0802: only these three may).
type ErrorEdgeKind uint8

const (
	EdgeTry ErrorEdgeKind = iota
	EdgeElse
	EdgeMatch
	// EdgeMust is the error edge of a Must: its block panics with
	// PanicMust and the error as the message.
	EdgeMust
)

// ErrorEdge is the error side of a fallible call: Error receives the
// ErrorSet value on Block.
type ErrorEdge struct {
	Kind  ErrorEdgeKind
	Block BlockID
	Error LocalID
}

// ExitKind is why a Return leaves the function. Fail and TryEdge are error
// exits for onerr; ElseEdge is a return from inside an else handler, which
// is not one (a return never runs onerr). MayBeError is
// return e with e: E!T, an error exit for E0411 only.
type ExitKind uint8

const (
	ExitNormal ExitKind = iota
	ExitFail
	ExitTry
	ExitElse
	ExitMayBeError
)

// RunsOnerr reports whether onerr copies run on this exit.
func (k ExitKind) RunsOnerr() bool { return k == ExitFail || k == ExitTry }

// IsE0411Exit reports whether the exit produces an error for E0411 (a fail,
// a try error edge, a return of an E!T operand).
func (k ExitKind) IsE0411Exit() bool { return k == ExitFail || k == ExitTry || k == ExitMayBeError }

// ArgMode is how an argument is passed (invariant M10).
type ArgMode uint8

const (
	ArgCopy ArgMode = iota
	ArgMove
	// ArgReborrow passes a mut T or mut []T local to a declared mut
	// parameter without moving it.
	ArgReborrow
)

// Arg is one call argument.
type Arg struct {
	Operand Operand
	Mode    ArgMode
}

// CalleeKind tags a Callee.
type CalleeKind uint8

const (
	// CallDirect calls Fn: a declared name.
	CallDirect CalleeKind = iota
	// CallInterface calls method Method of interface Iface on SelfType; only
	// in generic bodies, resolved to CallDirect per instance. Fn.Item
	// is NoItem; Fn.Args are the method's own type arguments, empty when it
	// has none, appended after the impl's by mono.
	CallInterface
	// CallValue calls through the fn value Value: a parameter or field.
	CallValue
	// CallBuiltin calls a builtin the compiler implements.
	CallBuiltin
)

// Builtin is a compiler-implemented callee. Std intrinsics (sys.Syscall,
// atomics) are bodiless functions called directly and recognized by key.
type Builtin uint8

const (
	// BuiltinEvalArena is EvalArena(): an arena for compile-time evaluation.
	BuiltinEvalArena Builtin = iota + 1
	// BuiltinQuote is quote { }: Args[0] is the template, an OpConst of type
	// []u8; Args[1..] are the splice operands in source order; Dest has the
	// generator API's Code type.
	BuiltinQuote
)

// Callee is what a Call calls.
type Callee struct {
	Kind     CalleeKind
	Fn       FnRef
	Iface    types.ItemID
	Method   uint32
	SelfType types.TypeID
	Value    Operand
	Builtin  Builtin
}

// SwitchCase is one arm of a switch: a value's bit pattern and its target.
type SwitchCase struct {
	Value  uint64
	Target BlockID
}

// TermKind tags a Terminator.
type TermKind uint8

const (
	// TermGoto jumps to Target. ElseExit marks a break or continue leaving
	// its block from inside an else handler: onerr copies run on this edge.
	TermGoto TermKind = iota
	// TermSwitch branches on On (an integer, bool, char or discriminant bit
	// pattern) over Cases, else Default (NoBlock when the cases are total).
	TermSwitch
	// TermCall calls Callee with Args into Dest, then Target. Err is set iff
	// the callee can fail and the call is not spawned (M5). Spawn is the scope
	// of s.go, NoLocal otherwise: a LocalScope local, or a parameter or local
	// of type &Scope or &Scope[E] (M9); every argument of a spawned call is
	// stored into it. Args may end with environment arguments past the
	// callee's declared parameters: one per const parameter bound to a
	// capturing literal, in parameter order, and one for a direct call of a
	// capturing literal; each an ArgCopy of an environment type (IsEnvType,
	// M5). A spawned call has none.
	TermCall
	// TermTypeTest branches on generic Param being Type: Yes, else No.
	TermTypeTest
	// TermOnerrGuard runs the onerr body at Yes unless Value has moved, else
	// goes to No; borrow resolves it, MIR is not rewritten.
	TermOnerrGuard
	// TermReturn leaves the function with local 0, for reason Exit.
	TermReturn
	// TermPanic traps with Panic, and the message operand Msg when HasMsg: a
	// str, or for PanicMust an error-set value whose name the runtime prints.
	TermPanic
	// TermUnreachable is never reached; reaching it is PanicUnreachable.
	TermUnreachable
	// TermTailCall is become's guaranteed tail call: it calls Callee with Args
	// in place of the current frame and leaves the function with the callee's
	// result, so it has no Dest, no Target, no error edge and no Spawn: Dest
	// is the zero Place, Target 0, HasErr false and Spawn NoLocal (M5). Callee
	// is CallDirect or CallValue, and the callee's result and error set equal
	// the body's own (check's tail-call-signature); the arguments are as
	// TermCall's. Appended last, so no older kind is renumbered.
	TermTailCall
)

// Terminator ends a block.
type Terminator struct {
	Kind     TermKind
	Span     SpanID
	Target   BlockID
	ElseExit bool
	On       Operand
	Cases    Range
	Default  BlockID
	Callee   Callee
	Args     Range
	Dest     Place
	HasErr   bool
	Err      ErrorEdge
	Spawn    LocalID
	Param    uint32
	Type     types.TypeID
	Yes, No  BlockID
	Value    LocalID
	Exit     ExitKind
	Panic    types.PanicCode
	HasMsg   bool
	Msg      Operand
	// NewError is set on an ExitFail return whose error that fail
	// constructed.
	NewError bool
}

// BlockOrigin tells cleanup copies from ordinary code (invariant M7).
type BlockOrigin uint8

const (
	OriginBody BlockOrigin = iota
	// OriginDefer is a copy of defer Defer on one exit edge.
	OriginDefer
	// OriginOnerr is a copy of onerr Defer on one exit edge.
	OriginOnerr
)

// Block is a basic block.
type Block struct {
	Stmts  []Statement
	Term   Terminator
	Origin BlockOrigin
	Defer  DeferID
}

// LoopInfo describes one loop.
type LoopInfo struct {
	Header BlockID
	Exit   BlockID
	// Holder is the loop's holder local, NoLocal when it has none.
	Holder LocalID
	Label  source.Symbol
	Span   SpanID
	// FieldsOf is the struct or record a generic fields loop iterates, NoType
	// otherwise; FieldsParam is the hidden generic standing for the current
	// field's type. Such a loop's blocks are exactly [Header, Exit): the
	// header is goto body, every continue and the body's end go to one latch
	// whose terminator is goto Exit, and break goes to Exit; mono copies the
	// range once per field. The latch is block Header+2 (header, body, latch
	// are the loop's first three blocks, as lower allocates them), so a break,
	// which also ends in goto Exit, is never taken for it (M9).
	FieldsOf    types.TypeID
	FieldsParam uint32
}

// DeferInfo describes one defer or onerr statement.
type DeferInfo struct {
	Onerr bool
	Span  SpanID
}

// Body is the MIR of one function, test, const initializer or contract. It is
// immutable once lower returns it: borrow writes its results elsewhere, and
// eval, mono and codegen read it concurrently.
type Body struct {
	// Item is the function (or const, global, test, contract) whose body this is.
	Item types.ItemID
	// GenericCount is the length of the item's combined generic list; an
	// instance body has 0 and no KParam, KSelf, TypeTest, CallInterface or
	// loop FieldRef.
	GenericCount uint32
	// ParamCount is the number of parameters: locals 1..ParamCount. It may
	// exceed len(Sig.Params) by trailing hidden environment parameters
	// a capturing literal's body has one, a callee instance
	// one per const parameter bound to a capturing literal, in parameter
	// order. Each is a LocalParam of an environment type (IsEnvType, M1).
	ParamCount  uint32
	Locals      []Local
	Blocks      []Block
	Projections []Projection
	GenericArgs []types.GenericArg
	Operands    []Operand
	Args        []Arg
	Cases       []SwitchCase
	// Spans[0] is the body's own span; every statement and terminator has a
	// SpanID (M12). The table is never hashed (positions are relative).
	Spans  []source.Span
	Loops  []LoopInfo
	Defers []DeferInfo
	// Asms are the payloads of the body's StAsm statements.
	Asms []Asm
}

// NewBody returns a body for item with its own span as Spans[0] and the
// return local as local 0.
func NewBody(item types.ItemID, span source.Span, ret types.TypeID) *Body {
	b := &Body{Item: item, Spans: []source.Span{span}}
	b.Locals = append(b.Locals, Local{Type: ret, Kind: LocalReturn, Loop: NoLoop, Span: 0})
	return b
}

// AddParam appends parameter local p (the next one) of type t.
func (b *Body) AddParam(t types.TypeID, name source.Symbol, span SpanID) LocalID {
	b.ParamCount++
	return b.AddLocal(Local{Type: t, Kind: LocalParam, Param: types.ParamIndex(b.ParamCount), Name: name, Loop: NoLoop, Span: span})
}

// AddLocal appends a local and returns its ID.
func (b *Body) AddLocal(l Local) LocalID {
	b.Locals = append(b.Locals, l)
	return LocalID(len(b.Locals) - 1)
}

// AddBlock appends an empty block and returns its ID.
func (b *Body) AddBlock(origin BlockOrigin, d DeferID) BlockID {
	b.Blocks = append(b.Blocks, Block{Origin: origin, Defer: d, Term: Terminator{Kind: TermUnreachable}})
	return BlockID(len(b.Blocks) - 1)
}

// AddSpan appends a span and returns its ID.
func (b *Body) AddSpan(s source.Span) SpanID {
	b.Spans = append(b.Spans, s)
	return SpanID(len(b.Spans) - 1)
}

// Place builds a place on local l with the given steps.
func (b *Body) Place(l LocalID, steps ...Projection) Place {
	if len(steps) == 0 {
		return Place{Local: l}
	}
	r := Range{Start: uint32(len(b.Projections)), Len: uint32(len(steps))}
	b.Projections = append(b.Projections, steps...)
	return Place{Local: l, Proj: r}
}

// AddOperands appends operands and returns their range.
func (b *Body) AddOperands(ops ...Operand) Range {
	r := Range{Start: uint32(len(b.Operands)), Len: uint32(len(ops))}
	b.Operands = append(b.Operands, ops...)
	return r
}

// AddArgs appends call arguments and returns their range.
func (b *Body) AddArgs(args ...Arg) Range {
	r := Range{Start: uint32(len(b.Args)), Len: uint32(len(args))}
	b.Args = append(b.Args, args...)
	return r
}

// AddCases appends switch cases and returns their range.
func (b *Body) AddCases(cs ...SwitchCase) Range {
	r := Range{Start: uint32(len(b.Cases)), Len: uint32(len(cs))}
	b.Cases = append(b.Cases, cs...)
	return r
}

// AddGenericArgs appends generic arguments and returns their range.
func (b *Body) AddGenericArgs(as ...types.GenericArg) Range {
	r := Range{Start: uint32(len(b.GenericArgs)), Len: uint32(len(as))}
	b.GenericArgs = append(b.GenericArgs, as...)
	return r
}

// Proj returns the steps of a place (shared; do not modify).
func (b *Body) Proj(p Place) []Projection { return b.Projections[p.Proj.Start:p.Proj.End()] }

// PlaceType is the type of a place: the local's type with no steps, else the
// last step's type.
func (b *Body) PlaceType(p Place) types.TypeID {
	if p.Proj.Len == 0 {
		return b.Locals[p.Local].Type
	}
	return b.Projections[p.Proj.End()-1].Type
}

// OperandType is the type an operand yields.
func (b *Body) OperandType(o Operand) types.TypeID {
	if o.Kind == OpCopy || o.Kind == OpMove {
		return b.PlaceType(o.Place)
	}
	return o.Type
}

// Span returns the source span of a SpanID.
func (b *Body) Span(id SpanID) source.Span { return b.Spans[id] }

// Successors appends the successors of block id to buf, in terminator order:
// Goto's target; Switch's cases then default; Call's target then error
// block; TypeTest and OnerrGuard Yes then No; none for Return, Panic,
// Unreachable and TailCall.
func (b *Body) Successors(id BlockID, buf []BlockID) []BlockID {
	t := &b.Blocks[id].Term
	switch t.Kind {
	case TermGoto:
		buf = append(buf, t.Target)
	case TermSwitch:
		for _, c := range b.Cases[t.Cases.Start:t.Cases.End()] {
			buf = append(buf, c.Target)
		}
		if t.Default != NoBlock {
			buf = append(buf, t.Default)
		}
	case TermCall:
		buf = append(buf, t.Target)
		if t.HasErr {
			buf = append(buf, t.Err.Block)
		}
	case TermTypeTest, TermOnerrGuard:
		buf = append(buf, t.Yes, t.No)
	case TermReturn, TermPanic, TermUnreachable, TermTailCall:
	default:
		source.ICEf(b.Spans[0], "mir: terminator kind %d", t.Kind)
	}
	return buf
}

// PredecessorCounts counts, for every block, the edges into it.
func (b *Body) PredecessorCounts() []uint32 {
	n := make([]uint32, len(b.Blocks))
	var buf []BlockID
	for i := range b.Blocks {
		buf = b.Successors(BlockID(i), buf[:0])
		for _, s := range buf {
			if int(s) < len(n) {
				n[s]++
			}
		}
	}
	return n
}

// CFGChecksum is the hash of the body's block structure: per block its
// terminator kind and successor indices, in block order. PGO matches profile
// records by stable key and this checksum; it ignores spans, names and types,
// so an edit that keeps the shape keeps it.
func (b *Body) CFGChecksum() source.Hash {
	h := source.NewHasher("mk.cfg.v1")
	h.Uvarint(uint64(len(b.Blocks)))
	var buf []BlockID
	for i := range b.Blocks {
		h.Uvarint(uint64(b.Blocks[i].Term.Kind))
		buf = b.Successors(BlockID(i), buf[:0])
		h.Uvarint(uint64(len(buf)))
		for _, s := range buf {
			h.Uvarint(uint64(s))
		}
	}
	return h.Sum()
}
