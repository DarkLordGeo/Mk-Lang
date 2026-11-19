// Why: a program that never names a socket, a process or a directory should
// not carry their code: every function stored in a const std.core.IoTable
// would otherwise be collected and linked.
//
// What: the dead-entry analysis: an IoTable entry is requested only when
// some built instance body reads its field (ioReads, markAllRead,
// reinterprets, externGetsTable). Codegen emits an unrequested entry as a
// trap stub (internal/codegen deadEntry), so the linker drops its code.
//
// How: an entry's function is reached from the table only through a load
// of its field, and every such load is a place with a field step on an
// IoTable (ProjField with the step's input type IoTable). ioReads walks
// every place of a body: statement and rvalue places and operands, the
// operand and argument pools, the terminator's operands, callee value and
// destination, and asm operands. A write to a field counts as a read (a
// table built at run time from a copy keeps every entry it names). The ways
// around a field step reinterpret the table's bytes or hand them to code
// mono cannot see, and each marks every field read and records the body as
// reading every field, so the UsesTasks fact sees its task fields too
// (markAllRead): a std.raw call other than the pointer forms below with
// IoTable among its generic arguments; a raw.Addr of an IoTable whose
// integer is used other than as a comparison operand (FromAddr can make it
// a pointer of another type); and an IoTable, or a pointer or reference to
// one, passed to an extern function or as an asm operand. Not covered: a
// value that holds a table pointer in a field (an Io) passed to an extern
// or an asm operand; the extern then reads the table only through its own
// pointer arithmetic, which the dead entries' trap stubs turn into a trap.

package mono

import (
	"strings"

	"mk/internal/mir"
	"mk/internal/types"
)

// ioState is the dead-entry bookkeeping of one collection.
type ioState struct {
	inited bool
	// tab is the named type std.core.IoTable; NoType when std declares none.
	tab types.TypeID
	// off is each field's byte offset in tab's layout, by field index.
	off  []uint64
	read []bool
	all  bool
	// pending holds, per field, the requests of entries waiting for a read.
	pending [][]pend
	// activated are the requests a read released, merged after the body
	// that read them (drain).
	activated []pend
}

var keyIoTable = types.ItemKey("std.core", "IoTable")

// rawModule is std.raw's module path (a variable for the tests' world).
var rawModule = "std.raw"

// rawPointerForms are the std.raw functions that only make, test or move a
// pointer to their generic argument: an IoTable named through them is still
// read field by field. Addr is one only while its integer is only compared
// (reinterprets).
var rawPointerForms = map[string]bool{"Of": true, "OfMut": true, "FromAddr": true, "IsNull": true,
	"Null": true, "Addr": true, "Offset": true}

func (c *collector) ioInit() {
	s := &c.io
	if s.inited {
		return
	}
	s.inited = true
	id, ok := c.env.Items().ByKey(keyIoTable)
	if !ok {
		return
	}
	if _, isStruct := c.env.Items().Get(id).Decl.(*types.StructDecl); !isStruct {
		return
	}
	t := c.env.Table().Named(id, nil)
	l := c.env.Layout(t)
	if l == nil {
		return
	}
	s.tab = t
	for _, f := range l.Fields {
		s.off = append(s.off, f.Offset)
	}
	s.read = make([]bool, len(s.off))
	s.pending = make([][]pend, len(s.off))
}

// ioFieldAt is the field of IoTable at byte offset off when t is IoTable,
// else -1.
func (c *collector) ioFieldAt(t types.TypeID, off uint32) int {
	c.ioInit()
	if c.io.tab == types.NoType || t != c.io.tab {
		return -1
	}
	for i, o := range c.io.off {
		if o == uint64(off) {
			return i
		}
	}
	return -1
}

// deferEntry holds r, the request of the function in IoTable field f, until
// the field is read; false when it is already read (request it now).
func (c *collector) deferEntry(f int, r request) bool {
	s := &c.io
	if s.all || s.read[f] {
		return false
	}
	s.pending[f] = append(s.pending[f], pend{caller: c.cur, req: r})
	return true
}

// markRead records a read of field f and releases its waiting entries.
func (c *collector) markRead(f int) {
	s := &c.io
	if f < 0 || f >= len(s.read) || s.read[f] {
		return
	}
	s.read[f] = true
	s.activated = append(s.activated, s.pending[f]...)
	s.pending[f] = nil
}

// markAllRead releases every entry and records that the body being built
// (c.cur) reads every field: after a reinterpret any entry can be called
// from it, so the UsesTasks fact counts it as reading the task fields.
func (c *collector) markAllRead() {
	for f := range c.io.read {
		c.markRead(f)
		c.tg.reads[c.cur] = append(c.tg.reads[c.cur], f)
	}
	c.io.all = true
}

// ioReads marks every IoTable field b reads (the walk the file comment
// describes). It runs on every body, also once every entry is released
// (c.io.all): the UsesTasks fact reads each body's own field reads (tg.reads).
func (c *collector) ioReads(b *mir.Body) {
	c.ioInit()
	tab := c.io.tab
	if tab == types.NoType {
		return
	}
	place := func(p mir.Place) {
		if p.Proj.Len == 0 || int(p.Local) >= len(b.Locals) {
			return
		}
		t := b.Locals[p.Local].Type
		for _, pr := range b.Proj(p) {
			if pr.Kind == mir.ProjField && !pr.FieldLoop && t == tab {
				c.tg.reads[c.cur] = append(c.tg.reads[c.cur], int(pr.A))
				c.markRead(int(pr.A))
			}
			t = pr.Type
		}
	}
	operand := func(o *mir.Operand) {
		if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
			place(o.Place)
		}
	}
	for i := range b.Operands {
		operand(&b.Operands[i])
	}
	for i := range b.Args {
		operand(&b.Args[i].Operand)
	}
	escapes := false
	for i := range b.Asms {
		for j := range b.Asms[i].Operands {
			op := &b.Asms[i].Operands[j]
			operand(&op.In)
			place(op.Out)
			// A table, or a pointer to one, in an asm operand is read
			// by code mono cannot see.
			escapes = escapes || c.pointsAtTable(op.Type) || c.pointsAtTable(operandType(b, &op.In)) ||
				c.pointsAtTable(placeType(b, op.Out))
		}
	}
	for bi := range b.Blocks {
		blk := &b.Blocks[bi]
		for si := range blk.Stmts {
			st := &blk.Stmts[si]
			place(st.Place)
			place(st.Rvalue.Place)
			operand(&st.Rvalue.A)
			operand(&st.Rvalue.B)
		}
		t := &blk.Term
		operand(&t.On)
		operand(&t.Msg)
		operand(&t.Callee.Value)
		place(t.Dest)
		if isCall(t) && (c.reinterprets(b, t) || c.externGetsTable(b, t)) {
			escapes = true
		}
	}
	if escapes {
		c.markAllRead()
	}
}

// reinterprets reports a std.raw call outside rawPointerForms with IoTable
// in its generic arguments (a pointer to it included), or a raw.Addr of an
// IoTable whose integer is used other than as a comparison operand.
func (c *collector) reinterprets(b *mir.Body, t *mir.Terminator) bool {
	if t.Callee.Kind != mir.CallDirect {
		return false
	}
	f := t.Callee.Fn
	it := c.env.Items().Get(f.Item)
	if c.env.Items().Module(it.Module).Path != rawModule {
		return false
	}
	name := strings.TrimPrefix(it.Key.String(), rawModule+".")
	if rawPointerForms[name] && name != "Addr" {
		return false
	}
	named := false
	for _, a := range b.GenericArgs[f.Args.Start:f.Args.End()] {
		if !a.IsConst && c.pointsAtTable(a.Type) {
			named = true
		}
	}
	if !named {
		return false
	}
	if name == "Addr" {
		return !onlyCompared(b, t)
	}
	return true
}

// pointsAtTable reports IoTable, or a pointer or reference to it.
func (c *collector) pointsAtTable(t types.TypeID) bool {
	if t == types.NoType || c.io.tab == types.NoType {
		return false
	}
	tt := c.env.Table()
	for k := tt.Kind(t); k == types.KRawPtr || k == types.KRef || k == types.KMut; k = tt.Kind(t) {
		t = tt.Get(t).Elem
	}
	return t == c.io.tab
}

// externGetsTable reports a call of an extern function with an argument
// that is an IoTable or points at one.
func (c *collector) externGetsTable(b *mir.Body, t *mir.Terminator) bool {
	if t.Callee.Kind != mir.CallDirect {
		return false
	}
	fd, ok := c.env.Items().Get(t.Callee.Fn.Item).Decl.(*types.FuncDecl)
	if !ok || !fd.Sig.Extern {
		return false
	}
	for i := t.Args.Start; i < t.Args.End(); i++ {
		if c.pointsAtTable(operandType(b, &b.Args[i].Operand)) {
			return true
		}
	}
	return false
}

// operandType is the type of operand o in b.
func operandType(b *mir.Body, o *mir.Operand) types.TypeID {
	if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
		return placeType(b, o.Place)
	}
	return o.Type
}

// placeType is the type of place p in b, NoType for no place.
func placeType(b *mir.Body, p mir.Place) types.TypeID {
	if p.Local == mir.NoLocal || int(p.Local) >= len(b.Locals) {
		return types.NoType
	}
	if p.Proj.Len == 0 {
		return b.Locals[p.Local].Type
	}
	pr := b.Proj(p)
	return pr[len(pr)-1].Type
}

// onlyCompared reports that call t's destination is a whole local whose
// every use in b is an operand of a comparison (IoHooked's
// `raw.Addr(io.table) != raw.Addr(skip)`): the integer then never becomes
// a pointer. Any other use (a copy, a return, an argument, arithmetic, a
// borrow, an index) or a second definition is an escape.
func onlyCompared(b *mir.Body, t *mir.Terminator) bool {
	l := t.Dest.Local
	if t.Dest.Proj.Len != 0 || l == mir.NoLocal || l == 0 || int(l) <= int(b.ParamCount) {
		return false
	}
	other := false
	inPlace := func(p mir.Place) {
		if p.Local == l {
			other = true
		}
		if p.Proj.Len != 0 {
			for _, pr := range b.Projections[p.Proj.Start:p.Proj.End()] {
				if pr.Kind == mir.ProjIndex && mir.LocalID(pr.A) == l {
					other = true
				}
			}
		}
	}
	inOp := func(o *mir.Operand) {
		if o.Kind == mir.OpCopy || o.Kind == mir.OpMove {
			inPlace(o.Place)
		}
	}
	for i := range b.Operands {
		inOp(&b.Operands[i])
	}
	for i := range b.Args {
		inOp(&b.Args[i].Operand)
	}
	for i := range b.Asms {
		for j := range b.Asms[i].Operands {
			inOp(&b.Asms[i].Operands[j].In)
			inPlace(b.Asms[i].Operands[j].Out)
		}
	}
	for bi := range b.Blocks {
		blk := &b.Blocks[bi]
		for si := range blk.Stmts {
			st := &blk.Stmts[si]
			rv := &st.Rvalue
			inPlace(st.Place) // a second definition, or a write through it
			if rv.Kind == mir.RvBinary && rv.Bin.IsCompare() {
				for _, o := range []*mir.Operand{&rv.A, &rv.B} {
					if (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && o.Place.Local == l && o.Place.Proj.Len == 0 {
						continue
					}
					inOp(o)
				}
			} else {
				inOp(&rv.A)
				inOp(&rv.B)
			}
			inPlace(rv.Place)
		}
		tm := &blk.Term
		inOp(&tm.On)
		inOp(&tm.Msg)
		inOp(&tm.Callee.Value)
		if tm != t {
			inPlace(tm.Dest)
		}
	}
	return !other
}
