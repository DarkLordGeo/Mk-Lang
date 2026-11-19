// Why: a static "hello world" should not link the whole I/O table or the
// profile writer when it uses neither; both are reached only through data,
// so the linker alone cannot drop them.
//
// What: deadIoEntry and deadEntry (unused std.core.IoTable entries) and
// emptyProfileTable (std.rt.ProfileTable outside --pgo-gen).
//
// How: a fn relocation of a const std.core.IoTable whose function mono did
// not collect is emitted as a pointer to the pack's trap stub. mono collects
// an IoTable entry's function only when some instance body reads that field
// (internal/mono/deadio.go), so the stub is never called, and the entry's
// code is not linked.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

var keyIoTable = types.ItemKey("std.core", "IoTable")

// isIoTable reports whether t is the named type std.core.IoTable.
func (g *packGen) isIoTable(t types.TypeID) bool {
	ty := g.tt.Get(t)
	return ty.Kind == types.KNamed && g.items.Get(ty.Item).Key == keyIoTable
}

// deadIoEntry reports whether rl, a relocation of a const IoTable, names a
// function the unit does not hold: an entry no instance reads.
func (g *packGen) deadIoEntry(rl types.Reloc) bool {
	if rl.Kind != types.RelocFn || g.inlineIntrinsic(rl.Fn.Item) {
		return false
	}
	_, ok := g.findFunc(rl.Fn.Item, rl.Fn.Args)
	return !ok
}

// deadEntry is the pack's private mk.dead.ioentry: a trap. Its address
// stands for every entry deadIoEntry reports.
func (g *packGen) deadEntry() llvm.Value {
	m := g.m
	if g.deadStub == nil {
		fn := m.AddFunction("mk.dead.ioentry", llvm.FuncSig{Linkage: llvm.Internal, Ret: m.Void(),
			FnAttrs: g.fnAttrs(types.Void).Flag("noinline").Flag("cold").Flag("noreturn")})
		b := fn.NewBlock("entry")
		b.CallFunc(m.IntrinsicFn("trap"), nil)
		b.Unreachable()
		g.deadStub = fn
	}
	return m.FuncRef(g.deadStub)
}

var keyProfileTable = types.ItemKey("std.rt", "ProfileTable")

// emptyProfileTable is a call of std.rt.ProfileTable() outside --pgo-gen:
// the support pack's body returns the empty slice (support_rt.go
// emptySlice), so the call is that constant in its caller, and a test of
// the table's length folds (so std/rt/profile.mk's writer is not linked).
func (fg *fnGen) emptyProfileTable(t *mir.Terminator) {
	m := fg.g.m
	d, _ := fg.placeAddr(t.Dest)
	fg.cur.Store(m.ConstIntToPtr(8), d.v, d.align)
	fg.cur.Store(m.ConstInt(m.I(64), 0), fg.gep(d.v, 8), alignAt(d.align, 8))
	fg.cur.Br(fg.blocks[t.Target])
}
