// Why: structured concurrency (s.go inside a scope) is compiled to calls of
// std.task's runtime; codegen builds the argument block and a thunk with
// one fixed signature, so the scheduler can start any call the same way.
//
// What: spawn (s.go: the argument block, std.task.SpawnAlloc and Spawn),
// thunk (S.go, of Mk type fn(*Io, *u8, *u8): bool), join (StJoin,
// std.task.Join), scopeIntrinsic (Io.Scope, Io.ScopeOf[E], Io.ScopeWith and
// Scope.Cancel, through std.task.Enter and Cancel), deadlineIntrinsic
// (Scope.CancelAfter, Expired and Remaining) and runtimeFn.
//
// How: thunks are emitted by the caller's pack, internal, one per (callee,
// scope error set) in that pack: mono does not say which error set a
// spawned instance is spawned under, and the caller's scope operand does;
// an internal thunk never collides with another pack's.

package codegen

import (
	"mk/internal/abi"
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// Runtime entries that s.go, StJoin and the scope
// intrinsics call (std/task/scope.mk, spawn.mk).
var (
	keyEnter      = types.ItemKey("std.task", "Enter")
	keySpawnAlloc = types.ItemKey("std.task", "SpawnAlloc")
	keySpawn      = types.ItemKey("std.task", "Spawn")
	keyJoin       = types.ItemKey("std.task", "Join")
	keyCancel     = types.ItemKey("std.task", "Cancel")
)

// deadlineEntries are the std.task entries of the deadline intrinsics,
// by the intrinsic's key.
var deadlineEntries = map[string]types.Key{
	"std.core.Scope:CancelAfter": types.ItemKey("std.task", "CancelAfter"),
	"std.core.Scope:Expired":     types.ItemKey("std.task", "Expired"),
	"std.core.Scope:Remaining":   types.ItemKey("std.task", "Remaining"),
}

// runtimeFn is the Funcs index of a non-generic std function codegen calls
// by key; its absence, as an item of the build or as an instance of the
// unit (not a mono root), is an ICE ("a missing runtime entry").
func (g *packGen) runtimeFn(k types.Key) int {
	id, ok := g.items.ByKey(k)
	if !ok {
		source.ICEf(source.Span{}, "codegen: missing runtime entry %s (std declares no such function)", k)
	}
	i, ok := g.findFunc(id, nil)
	if !ok {
		source.ICEf(source.Span{}, "codegen: missing runtime entry %s (no instance in the unit: not a mono root)", k)
	}
	return i
}

// callRuntime calls Funcs[fi] with args, typed as its parameters, and
// stores a non-void result into dest (nil discards it).
func (fg *fnGen) callRuntime(fi int, args []llvm.Value, dest *addr) {
	g := fg.g
	fn := g.funcDecl(fi)
	ls := g.funcSigOf(fi)
	if len(args) != len(ls.params) {
		source.ICEf(fg.spanOf(fg.span), "codegen: %s takes %d arguments, codegen passes %d", g.u.Funcs[fi].Symbol, len(ls.params), len(args))
	}
	vals := make([]value, len(args))
	for i, a := range args {
		vals[i] = value{t: ls.params[i], kind: rScalar, v: a}
	}
	fg.callLowered(fn.Ref(), fn.Type(), ls, vals, dest, callAttrs(fn))
}

// callRuntimeVals is callRuntime with Mk values (aggregates by address).
func (fg *fnGen) callRuntimeVals(fi int, vals []value, dest *addr) {
	g := fg.g
	fn := g.funcDecl(fi)
	ls := g.funcSigOf(fi)
	fg.callLowered(fn.Ref(), fn.Type(), ls, vals, dest, callAttrs(fn))
}

// scopeErrors is the error set E of Scope[E] (the Scope item applied
// to one error-set argument), EmptyErrors for the plain Scope.
func (g *packGen) scopeErrors(t types.TypeID) types.ErrorSetID {
	ty := g.tt.Get(t)
	if ty.Kind == types.KRef {
		ty = g.tt.Get(ty.Elem)
	}
	if ty.Kind == types.KNamed && len(ty.Args) == 1 && !ty.Args[0].IsConst && g.tt.Kind(ty.Args[0].Type) == types.KErrorSet {
		return g.tt.Get(ty.Args[0].Type).Errors
	}
	return types.EmptyErrors
}

// scopePtr is the address of the scope record of local s: a LocalScope
// local's own storage, or the pointer a &Scope / &Scope[E] local holds.
func (fg *fnGen) scopePtr(s mir.LocalID) (llvm.Value, types.TypeID) {
	g := fg.g
	t := fg.b.Locals[s].Type
	if g.tt.Kind(t) == types.KRef {
		return fg.loadScalar(fg.locals[s], g.reprOf(t), false), t
	}
	return fg.locals[s].v, t
}

// join lowers StJoin to std.task.Join(&s, cancel).
func (fg *fnGen) join(s *mir.Statement) {
	m := fg.g.m
	p, _ := fg.scopePtr(s.Local)
	fg.callRuntime(fg.g.runtimeFn(keyJoin), []llvm.Value{p, m.ConstInt(m.I(1), b2u(s.Cancel))}, nil)
}

// scopeIntrinsic lowers Io.Scope, Io.ScopeOf[E] and Io.ScopeWith to
// std.task.Enter(&dest, io, limit, stack, err_size, site), and
// Scope.Cancel to std.task.Cancel(s). The
// destination is the scope statement's LocalScope local itself (lower
// makes it so), so the record Enter links into its parent never moves. limit is
// 0 for Scope and ScopeOf, and for ScopeWith l.tasks when it is at least 1,
// else -1 (Enter panics); err_size is the size of E!void for Scope[E], else 0.
func (fg *fnGen) scopeIntrinsic(key string, t *mir.Terminator, args []value) {
	g, m := fg.g, fg.g.m
	i64 := m.I(64)
	if key == "std.core.Scope:Cancel" {
		fg.callRuntime(g.runtimeFn(keyCancel), []llvm.Value{args[0].v}, nil)
		fg.cur.Br(fg.blocks[t.Target])
		return
	}
	enter := g.runtimeFn(keyEnter)
	els := g.funcSigOf(enter)
	if len(els.params) != 6 {
		source.ICEf(fg.spanOf(t.Span), "codegen: std.task.Enter has %d parameters, want 6", len(els.params))
	}
	d, dt := fg.placeAddr(t.Dest)
	errSize := uint64(0)
	if es := g.scopeErrors(dt); es != types.EmptyErrors {
		errSize, _ = g.sizeAlign(g.tt.ErrorUnion(es, types.Void))
	}
	limit := m.ConstInt(i64, 0)
	stackT := els.params[3]
	// StackSize.Default: StackSize has a payload variant (Bytes(n)), so
	// it is a tagged value, built in a temporary with
	// Default's tag and loaded as the parameter's type.
	var stack value
	if r := g.reprOf(stackT); r.kind == rScalar {
		stack = value{t: stackT, kind: rScalar, v: m.ConstInt(r.ssa, g.variant(stackT, 0).Discr)}
	} else {
		tmp := fg.temp(stackT)
		fg.memset(tmp, 0, r.size)
		l := g.layout(stackT)
		fg.cur.Store(m.ConstInt(m.I(8*int(l.Tag.Size)), g.variant(stackT, 0).Discr), fg.gep(tmp.v, l.Tag.Offset), alignAt(tmp.align, l.Tag.Offset))
		stack = fg.loadAt(tmp, stackT)
	}
	if key == "std.core.Io:ScopeWith" {
		la := fg.addrOf(args[1])
		fs := g.layout(args[1].t).Fields
		if len(fs) != 2 {
			source.ICEf(fg.spanOf(t.Span), "codegen: Limits has %d fields, want tasks and stack", len(fs))
		}
		tasks := fg.loadAt(addr{v: fg.gep(la.v, fs[0].Offset), align: alignAt(la.align, fs[0].Offset)}, fs[0].Type).v
		ok := fg.cur.ICmp(llvm.ISGE, tasks, m.ConstInt(i64, 1))
		limit = fg.cur.Select(ok, tasks, m.ConstSInt(i64, -1))
		stack = fg.loadAt(addr{v: fg.gep(la.v, fs[1].Offset), align: alignAt(la.align, fs[1].Offset)}, fs[1].Type)
		stack.t = stackT
	}
	vals := []value{
		{t: els.params[0], kind: rScalar, v: d.v},
		args[0],
		{t: els.params[2], kind: rScalar, v: limit},
		stack,
		{t: els.params[4], kind: rScalar, v: m.ConstInt(i64, errSize)},
		{t: els.params[5], kind: rScalar, v: fg.site()},
	}
	fg.callRuntimeVals(enter, vals, nil)
	fg.cur.Br(fg.blocks[t.Target])
}

// deadlineIntrinsic lowers Scope.CancelAfter(d), Expired() and Remaining()
// to a call of the std.task entry of the same name with the same arguments,
// the scope by address (a Scope[E] keeps its Scope first), its result
// into Dest.
func (fg *fnGen) deadlineIntrinsic(key string, t *mir.Terminator, args []value) {
	g := fg.g
	fi := g.runtimeFn(deadlineEntries[key])
	fn := g.funcDecl(fi)
	fg.finishCall(t, fn.Ref(), fn.Type(), g.funcSigOf(fi), args, fg.b.PlaceType(t.Dest), callAttrs(fn))
}

// spawnBlock is the layout of a spawned call's argument block: each
// slot in order at its natural alignment.
func (g *packGen) spawnBlock(ts []types.TypeID) (offs []uint64, size uint64, align uint32) {
	align = 1
	for _, t := range ts {
		s, a := g.sizeAlign(t)
		a = max(a, 1)
		size = (size + uint64(a) - 1) / uint64(a) * uint64(a)
		offs = append(offs, size)
		size += s
		align = max(align, a)
	}
	return offs, size, align
}

// thunkKey identifies one thunk of a pack: a direct callee (fi >= 0) under
// one call signature (sig: a variadic extern's depends on the extra
// arguments), an intrinsic's wrapper (intr, its argsKey), or a
// fn type (fi < 0, intr ""), and the scope's error set.
type thunkKey struct {
	fi   int
	sig  string
	intr string
	fn   types.TypeID
	errs types.ErrorSetID
}

// thunkCallee is what a thunk calls: Funcs[fi] under the call signature ls,
// an intrinsic's wrapper w, or (neither) the fn value in slot 0.
type thunkCallee struct {
	ls *lsig
	w  *wrapperFn
}

// spawn lowers a TermCall with Spawn set: SpawnAlloc(&s, size, align,
// site), each argument stored into the block (a fn value first, for a call
// through one), Spawn(&s, thunk, block). A spawned call has no error edge
// and no destination value.
func (fg *fnGen) spawn(t *mir.Terminator) {
	g, m := fg.g, fg.g.m
	i64 := m.I(64)
	sp, st := fg.scopePtr(t.Spawn)
	errs := g.scopeErrors(st)
	var args []value
	for _, a := range fg.b.Args[t.Args.Start:t.Args.End()] {
		args = append(args, fg.operand(a.Operand))
	}
	var slots []types.TypeID
	var vals []value
	var tc thunkCallee
	key := thunkKey{fi: -1, errs: errs}
	switch t.Callee.Kind {
	case mir.CallDirect:
		if item := t.Callee.Fn.Item; g.inlineIntrinsic(item) {
			// An intrinsic has no body to call: the thunk calls its
			// wrapper.
			ga := fg.b.GenericArgs[t.Callee.Fn.Args.Start:t.Callee.Fn.Args.End()]
			w := g.wrapper(item, ga, fg.fi)
			fg.checkArgs(w.sig, args)
			key.intr, tc.w = wrapperKey(item, ga, g.wrapperFeatures(fg.fi)), &w
			slots, vals = w.sig.params, args
			break
		}
		fi := fg.callIndex(t) // the variant for this function's features
		// The call signature, not the callee's: a variadic extern's block
		// holds the extra arguments too.
		ls := g.callSig(fi, args)
		fg.checkArgs(ls, args)
		key.fi, key.sig, tc.ls = fi, sigKey(ls.params, ls.s.Fixed, ls.variadic, ls.result), ls
		slots = ls.params
		vals = args
	case mir.CallValue:
		fv := fg.operand(t.Callee.Value)
		if g.tt.Kind(fv.t) != types.KFn {
			source.ICEf(fg.spanOf(t.Span), "codegen: s.go through %s", g.tt.TypeString(fv.t, nil))
		}
		key.fn = fv.t
		slots = []types.TypeID{fv.t}
		for _, p := range g.tt.Get(fv.t).Fn.Params {
			slots = append(slots, p.Type)
		}
		vals = append([]value{fv}, args...)
	default:
		source.ICEf(fg.spanOf(t.Span), "codegen: s.go of callee kind %d", t.Callee.Kind)
	}
	if len(vals) != len(slots) {
		source.ICEf(fg.spanOf(t.Span), "codegen: s.go with %d arguments for %d slots", len(vals), len(slots))
	}
	offs, size, align := g.spawnBlock(slots)
	alloc := g.runtimeFn(keySpawnAlloc)
	bt := fg.temp(g.funcSigOf(alloc).result)
	fg.callRuntime(alloc, []llvm.Value{sp, m.ConstInt(i64, size), m.ConstInt(i64, uint64(align)), fg.site()}, &bt)
	blk := fg.cur.Load(m.Ptr(), bt.v, bt.align, m.Metadata().NoUndef())
	for i, v := range vals {
		_, a := g.sizeAlign(slots[i])
		fg.store(addr{v: fg.gep(blk, offs[i]), align: min(alignAt(align, offs[i]), max(a, 1))}, v)
	}
	th := g.thunk(key, tc, fg, slots, offs, align)
	fg.callRuntime(g.runtimeFn(keySpawn), []llvm.Value{sp, m.FuncRef(th), blk}, nil)
	fg.cur.Br(fg.blocks[t.Target])
}

// thunkSig is the lowered signature of fn(*Io, *u8, *u8): bool, exactly as
// a CallValue through that fn type lowers it (TestSpawnThunkABI).
func (g *packGen) thunkSig() *lsig {
	io := types.U8
	if id, ok := g.items.ByKey(types.ItemKey("std.core", "Io")); ok {
		io = g.tt.Named(id, nil)
	}
	ft := g.tt.Fn(types.FnType{Params: []types.FnParam{{Type: g.tt.RawPtr(io)}, {Type: g.tt.RawPtr(types.U8)},
		{Type: g.tt.RawPtr(types.U8)}}, Result: types.Bool})
	return g.fnTypeSig(ft)
}

// fnTypeSig is the lowered signature of a call through a value of fn type
// ft (CallValue).
func (g *packGen) fnTypeSig(ft types.TypeID) *lsig {
	fn := g.tt.Get(ft).Fn
	var params []types.TypeID
	for _, p := range fn.Params {
		params = append(params, p.Type)
	}
	return g.lowerSig(params, len(params), false, g.tt.ReturnType(fn.Result, fn.Errors))
}

// thunk is the pack's thunk for key, defined on first use: it loads
// each slot of the block, calls the callee, and returns false; when the
// callee can fail, an error is widened into the scope's E!void at %err and
// the thunk returns true. %io is not read.
func (g *packGen) thunk(key thunkKey, tc thunkCallee, caller *fnGen, slots []types.TypeID, offs []uint64, align uint32) *llvm.Function {
	if f, ok := g.thunks[key]; ok {
		return f
	}
	var name string
	switch {
	case tc.w != nil:
		name = tc.w.fn.Name() + ".go"
	case key.fi >= 0:
		name = g.u.Funcs[key.fi].Symbol + ".go"
	default:
		name = "mk.go.fn"
	}
	base := name
	if key.errs != types.EmptyErrors || (key.fi < 0 && tc.w == nil) || (tc.ls != nil && tc.ls.variadic) {
		g.nthunk++
		name += "." + itoa(g.nthunk)
	}
	for g.m.Has(name) { // two externs of one C name share a symbol
		g.nthunk++
		name = base + "." + itoa(g.nthunk)
	}
	ls := g.thunkSig()
	fn := g.m.AddFunction(name, g.funcSig(ls, llvm.Internal, llvm.Default, false, g.fnAttrs(types.Bool)))
	g.thunks[key] = fn
	g.extraFns = append(g.extraFns, fn)
	tf := &Func{Symbol: name, Key: name, Item: caller.f.Item, Pack: g.p}
	tg := &fnGen{g: g, f: tf, fi: -1, b: caller.b, fn: fn, sig: ls, span: caller.span}
	tg.subprogram(name, caller.spanOf(caller.span))
	tg.entry = fn.NewBlock("entry")
	tg.cur = tg.entry
	tg.setLoc(caller.span)
	body := tg.newBlock()
	tg.cur = body
	blk := fn.Param(ls.first[1])
	errp := fn.Param(ls.first[2])
	var vals []value
	for i, st := range slots {
		_, a := g.sizeAlign(st)
		vals = append(vals, tg.loadAt(addr{v: tg.gep(blk, offs[i]), align: min(alignAt(align, offs[i]), max(a, 1))}, st))
	}
	var callee llvm.Value
	var fnTy llvm.Type
	var cls *lsig
	attrs := llvm.Attrs{}.Flag("nounwind")
	// sysStack: an extern declared outside std runs on the system stack
	// from a thunk too, which runs on the spawned task's stack.
	var sysStack types.ItemID
	switch {
	case tc.w != nil:
		callee, fnTy, cls, attrs = tc.w.fn.Ref(), tc.w.fn.Type(), tc.w.sig, callAttrs(tc.w.fn)
	case key.fi >= 0:
		cf := g.funcDecl(key.fi)
		callee, fnTy, cls, attrs = cf.Ref(), cf.Type(), tc.ls, callAttrs(cf)
		if g.externOutsideStd(&g.u.Funcs[key.fi]) {
			if ss, ok := g.systemStackItem(); ok {
				sysStack = ss
			}
		}
	default:
		cls = g.fnTypeSig(key.fn)
		callee, fnTy = vals[0].v, g.m.FuncType(cls.ret, cls.ltypes, false)
		vals = vals[1:]
	}
	call := func(dest *addr) {
		if sysStack != types.NoItem {
			tg.sysStackCall(key.fi, cls, vals, dest, sysStack)
			return
		}
		tg.callLowered(callee, fnTy, cls, vals, dest, attrs)
	}
	m := g.m
	fallible := g.tt.Kind(cls.result) == types.KErrorUnion && g.calleeFallible(key, tc)
	switch {
	case cls.result == types.Never:
		call(nil)
		tg.cur.Unreachable()
	case !fallible:
		call(nil)
		tg.retBool(false)
	default:
		res := tg.temp(cls.result)
		call(&res)
		l := g.layout(cls.result)
		code := tg.cur.Load(m.I(32), tg.gep(res.v, l.Tag.Offset), alignAt(res.align, l.Tag.Offset), m.Metadata().NoUndef())
		okB, errB := tg.newBlock(), tg.newBlock()
		tg.cur.CondBr(tg.cur.ICmp(llvm.IEQ, code, m.ConstInt(m.I(32), 0)), okB, errB)
		tg.cur = okB
		tg.retBool(false)
		tg.cur = errB
		if key.errs != types.EmptyErrors {
			dt := g.tt.ErrorUnion(key.errs, types.Void)
			_, da := g.sizeAlign(dt)
			tg.widenErrorSide(addr{v: errp, align: da}, dt, value{t: cls.result, kind: rAgg, a: res})
		}
		tg.retBool(true)
	}
	tg.entry.Br(body)
	return fn
}

// calleeFallible reports whether the thunk's callee can fail (its declared
// error set, or the fn type's, is not empty).
func (g *packGen) calleeFallible(key thunkKey, tc thunkCallee) bool {
	if tc.w != nil {
		return g.declSig(tc.w.item).Errors != types.EmptyErrors
	}
	if key.fi < 0 {
		return g.tt.Get(key.fn).Fn.Errors != types.EmptyErrors
	}
	f := &g.u.Funcs[key.fi]
	if f.Body != nil {
		return g.tt.Kind(f.Body.Locals[mir.ReturnLocal].Type) == types.KErrorUnion &&
			g.declSig(f.Item).Errors != types.EmptyErrors
	}
	return g.declSig(f.Item).Errors != types.EmptyErrors
}

// retBool returns a bool constant under the function's lowered result.
func (fg *fnGen) retBool(b bool) {
	m := fg.g.m
	tmp := fg.temp(types.Bool)
	fg.storeScalar(tmp, fg.g.reprOf(types.Bool), m.ConstInt(m.I(1), b2u(b)))
	parts := fg.loadParts(tmp, types.Bool, fg.sig.s.Ret, false, true)
	if fg.sig.s.Ret.Kind != abi.Direct || len(parts) != 1 {
		source.ICEf(fg.spanOf(fg.span), "codegen: a bool result is not one Direct part")
	}
	fg.cur.Ret(&parts[0])
}
