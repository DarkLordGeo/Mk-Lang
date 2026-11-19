// Why: lower must not re-derive anything check worked out; it reads one
// checked unit per function, const, global, test or literal, and needs that
// unit's facts per AST node in a flat, cheap layout.
//
// What: Body (the side tables of one unit: Types, Uses, Adjust, Consts,
// Locals, Generic, Diverges and the rarer tables), Copy (one field's copy of
// an unrolled fields loop), Local, Use and UseKind, Adjust and AdjustKind,
// Builtin, Bits, and the slot helpers check fills the tables through.
//
// How: the parser numbers one item's nodes in a contiguous ID range, so
// every table is a slice indexed by NodeID-Base, and an ID outside the range
// is an internal compiler error. Rare tables are allocated on first use.

package check

import (
	"slices"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

// Body is one checked unit: side tables over the unit's AST nodes, which
// lower turns into MIR. Tables are indexed by NodeID-Base; the parser gives
// one item's nodes a contiguous ID range, and an ID outside [Base, Base+len)
// is an ICE.
type Body struct {
	Item types.ItemID
	File *ast.File
	Node ast.Node         // the FuncDecl, ExternDecl, TestItem, ConstDecl, GlobalDecl, FnLit, Contract or InvariantMember
	Sig  *types.Signature // nil for a const or global initializer
	Base ast.NodeID

	// Types is the type of every Expr, Pattern and Type node after
	// inference. types.Poison marks a node under an earlier error.
	Types []types.TypeID
	// Uses says what each name, member, call, construction or pattern head
	// means; zero for nodes that are not uses.
	Uses []Use
	// Adjust is the implicit step applied to an expression's value.
	Adjust []Adjust
	// Consts is the compile-time value of every node that has one.
	Consts []types.ConstID
	// Locals are the unit's locals in declaration order: Locals[0] is the
	// return place, then parameters (equal to their ParamIndex), then
	// bindings. Bind maps a binding node to its local.
	Locals []Local
	Bind   []LocalID
	// Generic holds the generic arguments of each generic use, indexed by
	// Use.Inst; slot 0 is empty.
	Generic [][]types.GenericArg
	// Diverges marks statements and expressions after which control never
	// continues.
	Diverges Bits
	// Generics are the generics the unit was checked under, in Sig.Generics'
	// order and number: Sig.Generics, with the implicit
	// bound added to T in a `Slice[T]` namespace function's body (`Fill` and
	// `CopyFrom`: Plain; `Has` and `Index`: Eq). nil when Sig is nil or has
	// no generics. Facts of a type parameter in the body answer from these,
	// never from Sig.Generics alone, so lower and borrow agree with check.
	Generics []types.GenericParam
	// Hidden are the hidden generics of the unit's generic fields loops,
	// indexed after Sig.Generics.
	Hidden []types.GenericParam
	// Unrolled holds one Copy per field of every fields loop over a
	// concrete struct or record.
	Unrolled []Copy
	Diags    []*diag.Diagnostic
	// PoisonReads are the consts whose value the body reads but could not
	// get, each once, in first-read order: a const with no type (an untyped
	// const whose initializer failed to type, or a member of a typing
	// cycle), or a same-module const whose use request gave poison (its own
	// evaluation fails, or it is in an evaluation cycle). Such a read is
	// Poison, and check reports nothing for it in this body; where the
	// const's failure is reported is the driver's question (accounting).
	// PoisonAt are the spans that Poison starts from: each read, and a
	// binding statement whose declared type holds one. The driver holds
	// lower's E0000 only at a span that overlaps one of them, and drops it
	// when every const listed accounts for it. A read lower never
	// lowers (a branch foldCond drops) raises nothing.
	PoisonReads []types.ItemID
	PoisonAt    []source.Span
	Reads       types.ReadSet
	// Envs are the environments of the capturing literals this unit passes
	// as const arguments, in typing order (the enclosing side).
	Envs []LitEnv
	// Captures are this unit's captures when it is a capturing literal:
	// field i of the environment; EnvRef is the hidden parameter's type,
	// NoType for every other unit and for a literal with no environment.
	Captures []CaptureType
	EnvRef   types.TypeID
	// ImplicitRecord marks a Print or Arena.Fmt CallExpr written without its
	// record because its format string has no hole; lower
	// supplies the (). Allocated on first use, so most bodies carry none.
	ImplicitRecord Bits
	// Values marks the IfStmt and MatchStmt nodes checked as value
	// constructs, nested last-line ones included, and the ElseExpr nodes
	// whose handler yields a value; lower
	// reads it through Input.IsValue. Allocated on first use.
	Values Bits
	// ErrorFormat is set on the unit of a provided Format method; its Node
	// is the error's *ast.ErrorDecl and it
	// has no node tables of its own.
	ErrorFormat *ErrorFormat

	// win is set while a fields-loop copy is checked into scratch tables
	// (fields.go): the slots it touches outside the loop are recorded so the
	// scratch can be zeroed again.
	win window
}

// Copy is one field's checking of a concretely unrolled fields loop body.
// Its tables are indexed by NodeID-Base over the loop body's node range.
type Copy struct {
	Loop     ast.NodeID // the ForStmt
	Field    uint32     // index into the struct's Fields (a record: sorted order)
	Parent   int32      // the Copy this loop sits in, or -1 for none
	Base     ast.NodeID // the loop body's first node
	Types    []types.TypeID
	Uses     []Use
	Adjust   []Adjust
	Consts   []types.ConstID
	Bind     []LocalID // bindings of this copy: fresh locals appended to Body.Locals
	Diverges Bits
}

// LocalID indexes Body.Locals; 0 is the return place and never bound.
type LocalID uint32

// Local is one local of a unit.
type Local struct {
	Name  source.Symbol
	Type  types.TypeID
	Var   bool // var local or var parameter: may be assigned and borrowed mut
	Param types.ParamIndex
	Span  source.Span
	Node  ast.NodeID // the binding node
	// Capture is 1 + the index of the capture this local stands for in a
	// capturing literal's unit, 0 for every other local.
	Capture uint32
}

// Use is the meaning of one use node after checking.
type Use struct {
	Kind  UseKind
	Item  types.ItemID // the function, method, type, const, global, error, variant's enum, interface
	Index uint32       // field index, variant index, interface method index, builtin id
	Local LocalID      // for UseLocal; for a UseField on a bare Ident in a fn literal of an invariant, the literal's capture of self
	Inst  uint32       // index into Body.Generic, 0 when not generic
}

// UseKind tags a Use.
type UseKind uint8

const (
	UseNone UseKind = iota
	UseLocal
	UseItem
	UseField
	UseVariant
	UseError
	UseMethod
	UseIfaceMethod
	UseFnValueCall
	UseBuiltin
	UseConst
	UseGlobal
	UseConstruct
)

// Adjust is the implicit step applied to an expression's value.
type Adjust struct {
	Kind  AdjustKind
	Deref uint8        // number of reference layers removed (field access, receivers)
	To    types.TypeID // AdjWiden: the union the error value is widened to
}

// AdjustKind tags an Adjust.
type AdjustKind uint8

const (
	AdjNone AdjustKind = iota
	AdjRefShared
	AdjRefMut
	AdjDeref
	AdjCopyThrough
	AdjWiden
)

// Builtin is the Use.Index of a UseBuiltin.
type Builtin uint32

const (
	BuiltinNone Builtin = iota
	BuiltinAssert
	BuiltinPanic
	BuiltinCast
	BuiltinTrunc
	BuiltinSizeOf
	BuiltinAlignOf
	BuiltinTarget
	BuiltinEvalArena
	BuiltinConvert // T(x)
	BuiltinOptMust
	BuiltinOptIsSome
	BuiltinOptIsNone
	BuiltinErrMust
	BuiltinLen
	BuiltinSyscall
	BuiltinTypeIs // the IsCond of if T is X
)

// Bits is a dense bitset over NodeID-Base.
type Bits struct{ w []uint64 }

// newBits makes a set over n nodes.
func newBits(n int) Bits { return Bits{w: make([]uint64, (n+63)/64)} }

// Has reports whether node id is in the set of a table based at base.
func (b Bits) Has(id ast.NodeID, base ast.NodeID) bool {
	i := int(id) - int(base)
	if i < 0 || i/64 >= len(b.w) {
		return false
	}
	return b.w[i/64]&(1<<(uint(i)%64)) != 0
}

// set adds node id; out of range is an ICE.
func (b Bits) set(id ast.NodeID, base ast.NodeID) {
	i := int(id) - int(base)
	if i < 0 || i/64 >= len(b.w) { // not Assertf: its boxed arguments would allocate on every call
		source.ICEf(source.Span{}, "check: bit %d outside [%d, +%d)", id, base, len(b.w)*64)
	}
	b.w[i/64] |= 1 << (uint(i) % 64)
}

// unset removes slot i (an index, not a NodeID).
func (b Bits) unset(i int) {
	if i >= 0 && i/64 < len(b.w) {
		b.w[i/64] &^= 1 << (uint(i) % 64)
	}
}

// nodeRange returns the smallest and largest NodeID in the subtree of n.
func nodeRange(n ast.Node) (lo, hi ast.NodeID) {
	lo = ^ast.NodeID(0)
	ast.Inspect(n, func(x ast.Node) bool {
		if x == nil {
			return false
		}
		id := x.ID()
		lo, hi = min(lo, id), max(hi, id)
		return true
	})
	return lo, hi
}

// newBody allocates a unit's tables over the node range of its root, a node
// of fx's file.
func newBody(item types.ItemID, fx *fileIndex, root ast.Node) *Body {
	f := fx.file
	lo, hi := fx.rangeOf(root)
	n := int(hi-lo) + 1
	b := &Body{Item: item, File: f, Node: root, Base: lo,
		Types:    make([]types.TypeID, n),
		Bind:     make([]LocalID, n),
		Locals:   make([]Local, 1),
		Generic:  make([][]types.GenericArg, 1),
		Diverges: newBits(n),
	}
	return b
}

// slot returns the table index of node id, or an ICE when it is outside the
// unit's range.
func (b *Body) slot(id ast.NodeID) int {
	i := int(id) - int(b.Base)
	if i < 0 || i >= len(b.Types) {
		source.ICEf(b.Node.Span(), "check: node %d outside unit range [%d, %d)", id, b.Base, int(b.Base)+len(b.Types))
	}
	if b.win.hi != 0 && (i < b.win.lo || i >= b.win.hi) {
		b.win.out = append(b.win.out, i)
	}
	return i
}

// poisonRead records a read at at of const c, whose value the body could
// not get (PoisonReads, PoisonAt).
func (b *Body) poisonRead(c types.ItemID, at source.Span) {
	if !slices.Contains(b.PoisonReads, c) {
		b.PoisonReads = append(b.PoisonReads, c)
	}
	b.PoisonAt = append(b.PoisonAt, at)
}

// markImplicitRecord sets ImplicitRecord for call node id, allocating the
// set on first use; a fields-loop copy marks the same node (its format text
// does not depend on the field).
func (b *Body) markImplicitRecord(id ast.NodeID) {
	b.slot(id)
	if b.ImplicitRecord.w == nil {
		b.ImplicitRecord = newBits(len(b.Types))
	}
	b.ImplicitRecord.set(id, b.Base)
}

// use returns the Use slot of node id, allocating the table on first use.
func (b *Body) use(id ast.NodeID) *Use {
	i := b.slot(id)
	if b.Uses == nil {
		b.Uses = make([]Use, len(b.Types))
	}
	return &b.Uses[i]
}

// adjust returns the Adjust slot of node id, allocating on first use.
func (b *Body) adjust(id ast.NodeID) *Adjust {
	i := b.slot(id)
	if b.Adjust == nil {
		b.Adjust = make([]Adjust, len(b.Types))
	}
	return &b.Adjust[i]
}

// diverge marks node id in Diverges.
func (b *Body) diverge(id ast.NodeID) {
	i := b.slot(id)
	b.Diverges.w[i/64] |= 1 << (uint(i) % 64)
}

// setConst records the compile-time value of node id.
func (b *Body) setConst(id ast.NodeID, c types.ConstID) {
	i := b.slot(id)
	if b.Consts == nil {
		b.Consts = make([]types.ConstID, len(b.Types))
	}
	b.Consts[i] = c
}

// TypeOf is the type of node id, NoType when none was recorded.
func (b *Body) TypeOf(id ast.NodeID) types.TypeID { return b.Types[b.slot(id)] }
