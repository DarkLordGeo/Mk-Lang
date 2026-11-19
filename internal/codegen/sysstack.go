// Why: Mk tasks run on small stacks, but C code called through an extern
// expects a large one; such calls switch to the carrier's system stack.
//
// What: systemStackCall and sysStackCall (an extern call on the system
// stack), adapter (the pack-local unpacking function), callSig (the call
// signature, variadic externs included), externOutsideStd and
// systemStackItem.
//
// How: a call to a bodiless extern declared outside std runs as
// ss := std.task.SystemStack(); null: the call, directly; else
// mk_call_system(adapter, args, ss) then std.task.SystemStackDone(), where
// the adapter is a pack-local function of one pointer that unpacks the
// argument block, makes the real call and stores the result back. Without
// std.task.SystemStack in the build the call is direct.

package codegen

import (
	"strings"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

var (
	keySystemStack     = types.ItemKey("std.task", "SystemStack")
	keySystemStackDone = types.ItemKey("std.task", "SystemStackDone")
)

// externOutsideStd reports a bodiless extern whose module is not std's.
func (g *packGen) externOutsideStd(f *Func) bool {
	if f.Body != nil {
		return false
	}
	it := g.items.Get(f.Item)
	fd, ok := it.Decl.(*types.FuncDecl)
	if !ok || !fd.Sig.Extern {
		return false
	}
	path := g.items.Module(it.Module).Path
	return path != "std" && !strings.HasPrefix(path, "std.")
}

// callSig is the lowered signature of a call of Funcs[fi] with args: the
// callee's own, or for a variadic extern its fixed parameters followed by
// the extra arguments' types.
func (g *packGen) callSig(fi int, args []value) *lsig {
	ls := g.funcSigOf(fi)
	if !ls.variadic || len(args) == len(ls.params) {
		return ls
	}
	params := append([]types.TypeID(nil), ls.params...)
	for _, a := range args[len(ls.params):] {
		params = append(params, a.t)
	}
	return g.lowerSig(params, len(ls.params), true, ls.result)
}

// adapterKey identifies one system-stack adapter of a pack.
type adapterKey struct {
	fi  int
	sig string
}

// systemStackCall emits the call of extern Funcs[fi] on the system stack and
// continues at the terminator's target (or unreachable for a never extern).
func (fg *fnGen) systemStackCall(t *mir.Terminator, fi int, ls *lsig, args []value, destT types.TypeID, ssItem types.ItemID) {
	g := fg.g
	var dest *addr
	if ls.result != types.Never && g.reprOf(ls.result).kind != rZero && g.reprOf(destT).kind != rZero {
		d, _ := fg.placeAddr(t.Dest)
		dest = &d
	}
	fg.sysStackCall(fi, ls, args, dest, ssItem)
	if ls.result == types.Never {
		fg.cur.Unreachable()
		return
	}
	fg.cur.Br(fg.blocks[t.Target])
}

// sysStackCall is the system-stack call of extern Funcs[fi] under the call
// signature
// ls: ss := SystemStack(); null: the call, directly; else mk_call_system
// through the pack's adapter, then SystemStackDone(). A result is stored at
// dest (nil discards it). It leaves fg.cur at the join block, open. Direct
// calls (systemStackCall) and spawn thunks both use it.
func (fg *fnGen) sysStackCall(fi int, ls *lsig, args []value, dest *addr, ssItem types.ItemID) {
	g, m := fg.g, fg.g.m
	fn := g.funcDecl(fi)
	ssFi := g.funcIndex(ssItem, nil)
	sst := fg.temp(g.funcSigOf(ssFi).result)
	fg.callRuntime(ssFi, nil, &sst)
	ss := fg.cur.Load(m.Ptr(), sst.v, sst.align, m.Metadata().NoUndef())
	direct, viaSys, join := fg.newBlock(), fg.newBlock(), fg.newBlock()
	fg.cur.CondBr(fg.cur.ICmp(llvm.IEQ, ss, m.Null()), direct, viaSys)
	hasResult := dest != nil && ls.result != types.Never && g.reprOf(ls.result).kind != rZero
	if !hasResult {
		dest = nil
	}

	fg.cur = direct
	fg.callLowered(fn.Ref(), fn.Type(), ls, args, dest, callAttrs(fn))
	fg.cur.Br(join)

	fg.cur = viaSys
	slots := make([]types.TypeID, 0, len(args)+1)
	for _, a := range args {
		slots = append(slots, a.t)
	}
	if hasResult {
		slots = append(slots, ls.result)
	}
	offs, size, align := g.spawnBlock(slots)
	blk := fg.tempBytes(max(size, 1), align)
	for i, a := range args {
		_, al := g.sizeAlign(a.t)
		fg.store(addr{v: fg.gep(blk.v, offs[i]), align: min(alignAt(blk.align, offs[i]), max(al, 1))}, a)
	}
	ad := g.adapter(fg, fi, ls, slots, offs, align, hasResult)
	cs := m.DeclareAs(symCallSystem, llvm.FuncSig{Linkage: llvm.External, Ret: m.Void(),
		Params: []llvm.Type{m.Ptr(), m.Ptr(), m.Ptr()}, FnAttrs: llvm.Attrs{}.Flag("nounwind")})
	fg.cur.CallFunc(cs, []llvm.Value{m.FuncRef(ad), blk.v, ss})
	if done, ok := g.items.ByKey(keySystemStackDone); ok {
		fg.callRuntime(g.funcIndex(done, nil), nil, nil)
	}
	if hasResult {
		off := offs[len(offs)-1]
		_, al := g.sizeAlign(ls.result)
		fg.store(*dest, fg.loadAt(addr{v: fg.gep(blk.v, off), align: min(alignAt(blk.align, off), max(al, 1))}, ls.result))
	}
	fg.cur.Br(join)
	fg.cur = join
}

// adapter is the pack's adapter of a call of extern Funcs[fi] under ls
// (internal, void(ptr)): it loads each argument from the block, calls the
// extern, and stores a result into the block's last slot.
func (g *packGen) adapter(caller *fnGen, fi int, ls *lsig, slots []types.TypeID, offs []uint64, align uint32, hasResult bool) *llvm.Function {
	key := adapterKey{fi: fi, sig: sigKey(ls.params, ls.s.Fixed, ls.variadic, ls.result)}
	if f, ok := g.adapters[key]; ok {
		return f
	}
	m := g.m
	// Numbered by symbol, not by callee: two externs of one C name
	// have one symbol.
	sym := g.u.Funcs[fi].Symbol
	name := "mk.cs." + sym
	if n := g.nadapter[sym]; n > 0 {
		name += "." + itoa(n)
	}
	g.nadapter[sym]++
	fsig := llvm.FuncSig{Linkage: llvm.Internal, Ret: m.Void(), Params: []llvm.Type{m.Ptr()},
		ParamAttrs: []llvm.Attrs{llvm.Attrs{}.Flag("noundef")}, FnAttrs: g.fnAttrs(types.Void)}
	fn := m.AddFunction(name, fsig)
	g.adapters[key] = fn
	g.extraFns = append(g.extraFns, fn)
	ag := &fnGen{g: g, f: &Func{Symbol: name, Key: name, Item: caller.f.Item, Pack: g.p}, fi: -1, b: caller.b, fn: fn,
		sig: g.lowerSig(nil, 0, false, types.Void), span: caller.span}
	ag.subprogram(name, caller.spanOf(caller.span))
	ag.entry = fn.NewBlock("entry")
	ag.cur = ag.entry
	ag.setLoc(caller.span)
	body := ag.newBlock()
	ag.cur = body
	blk := addr{v: fn.Param(0), align: align}
	n := len(slots)
	if hasResult {
		n--
	}
	vals := make([]value, n)
	for i := 0; i < n; i++ {
		_, al := g.sizeAlign(slots[i])
		vals[i] = ag.loadAt(addr{v: ag.gep(blk.v, offs[i]), align: min(alignAt(align, offs[i]), max(al, 1))}, slots[i])
	}
	ext := g.funcDecl(fi)
	var dest *addr
	if hasResult {
		off := offs[len(offs)-1]
		_, al := g.sizeAlign(ls.result)
		dest = &addr{v: ag.gep(blk.v, off), align: min(alignAt(align, off), max(al, 1))}
	}
	ag.callLowered(ext.Ref(), ext.Type(), ls, vals, dest, ext.Sig().FnAttrs)
	if ls.result == types.Never {
		ag.cur.Unreachable()
	} else {
		ag.cur.Ret(nil)
	}
	ag.entry.Br(body)
	return fn
}

// systemStackItem is std.task.SystemStack when the unit holds its instance.
// mono holds it back when the build needs no task (internal/mono
// tasks.go lazyKeys): Main then runs on a system stack and an extern call is
// direct, as without the entry in std.
func (g *packGen) systemStackItem() (types.ItemID, bool) {
	ss, ok := g.items.ByKey(keySystemStack)
	if !ok {
		return types.NoItem, false
	}
	if _, in := g.findFunc(ss, nil); !in {
		return types.NoItem, false
	}
	return ss, true
}
