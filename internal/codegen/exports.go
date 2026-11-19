// Why: a Mk library is entered from C, on threads the Mk runtime did not
// start, so its runtime needs explicit start and stop entries and every
// exported function needs a way onto a task.
//
// What: LibEntryNames and libEntries (<name>_mk_start, <name>_mk_stop and
// the image base the runtime reads), thunked, addThunk, emitThunk, callBody
// and imageBaseStub.
//
// How: the thunk of every export extern called from C runs the function as
// a task when the calling thread is off one (std.task.FromCNeeded, then
// std.task.RunFromC). The body keeps a symbol of its own (thunkBody), and
// callBody never inlines it into its thunk, so a fault in it reports the
// body's site.

package codegen

import (
	"strings"

	"mk/internal/llvm"
	"mk/internal/types"
)

var (
	keyFromCNeeded = types.ItemKey("std.task", "FromCNeeded")
	keyRunFromC    = types.ItemKey("std.task", "RunFromC")
)

// The C names of a library's runtime entries and the runtime's
// image-base hook (std.sys's LibraryImage).
const (
	symLibStart  = "MkLibStart"
	symLibStop   = "MkLibStop"
	symImageBase = "MkImageBase"
)

// LibEntryNames are a library's two runtime entries for library name.
func LibEntryNames(name string) (start, stop string) {
	return name + "_mk_start", name + "_mk_stop"
}

// libEntries emits a library build's runtime entries in the support pack:
// <name>_mk_start(const Options *o) calls MkLibStart with o's carriers (u32
// at 0) and stack_reserve (u64 at 8), or zeros for a null o, and returns
// its answer; <name>_mk_stop() returns MkLibStop's; MkImageBase() is the
// address of the library's own image (__ImageBase on COFF, __ehdr_start on
// ELF), which the runtime reads instead of the host's.
func (s *support) libEntries() {
	if s.cfg.LibName == "" {
		return
	}
	m := s.m
	i32, i64, ptr := m.I(32), m.I(64), m.Ptr()
	start := m.Declare(symLibStart, llvm.FuncSig{Linkage: llvm.External, Ret: i32, Params: []llvm.Type{i32, i64},
		FnAttrs: llvm.Attrs{}.Flag("nounwind")})
	stop := m.Declare(symLibStop, llvm.FuncSig{Linkage: llvm.External, Ret: i32, FnAttrs: llvm.Attrs{}.Flag("nounwind")})
	sn, tn := LibEntryNames(s.cfg.LibName)
	f := s.define(sn, i32, []llvm.Type{ptr}, s.fnAttrs())
	entry := f.NewBlock("entry")
	given, none := f.NewBlock("given"), f.NewBlock("none")
	null := m.Null()
	entry.CondBr(entry.ICmp(llvm.IEQ, f.Param(0), null), none, given)
	carriers := given.Load(i32, f.Param(0), 8)
	stack := given.Load(i64, given.GEP8(f.Param(0), m.ConstInt(i64, 8), 0), 8)
	r := given.CallFunc(start, []llvm.Value{carriers, stack})
	given.Ret(&r)
	r0 := none.CallFunc(start, []llvm.Value{m.ConstInt(i32, 0), m.ConstInt(i64, 0)})
	none.Ret(&r0)
	g := s.define(tn, i32, nil, s.fnAttrs())
	b := g.NewBlock("entry")
	r1 := b.CallFunc(stop, nil)
	b.Ret(&r1)
	base := "__ehdr_start"
	if s.t.OS == types.OSWindows {
		base = "__ImageBase"
	}
	img := m.AddGlobal(base, llvm.GlobalAttrs{Linkage: llvm.External, Visibility: llvm.Hidden, DSOLocal: true, Type: m.I(8)})
	h := s.define(symImageBase, i64, nil, s.fnAttrs())
	hb := h.NewBlock("entry")
	a := hb.Cast(llvm.PtrToInt, m.GlobalRef(img), i64)
	hb.Ret(&a)
}

// thunked reports whether Funcs[fi] gets a thunk: an export extern with a
// body, outside std, not naked, and not a hook the runtime calls itself
// (MkPanicHook runs inside a panic's report; MkEntry and the page hooks
// are freestanding's), in a build whose unit holds the runtime's
// FromCNeeded and RunFromC (a hosted build with tasks).
func (g *packGen) thunked(fi int) bool {
	f := &g.u.Funcs[fi]
	if f.Body == nil || g.nakedFn(f) || g.cfg.Target.OS == types.OSFreestanding {
		return false
	}
	it := g.items.Get(f.Item)
	fd, ok := it.Decl.(*types.FuncDecl)
	if !ok || !fd.Sig.Extern || !it.Exported || len(fd.Sig.Generics) > 0 {
		return false
	}
	path := g.items.Module(it.Module).Path
	if path == "std" || strings.HasPrefix(path, "std.") {
		return false
	}
	switch name := f.Symbol; name {
	case symPanicHook, "MkEntry", symPageAlloc, symPageFree, symThreadYield:
		return false
	}
	return g.fromCFuncs()
}

// fromCFuncs reports whether the unit holds FromCNeeded and RunFromC.
func (g *packGen) fromCFuncs() bool {
	for _, k := range []types.Key{keyFromCNeeded, keyRunFromC} {
		id, ok := g.items.ByKey(k)
		if !ok {
			return false
		}
		if _, in := g.findFunc(id, nil); !in {
			return false
		}
	}
	return true
}

// thunkBody is the symbol a thunked export's own body takes: internal to
// its pack, so the C name is the thunk's.
func thunkBody(sym string) string { return sym + ".mk" }

// addThunk creates the thunk of thunked Funcs[fi] under its C name, with
// the body's signature and attributes and the linkage lk, vis, dso the C
// name keeps, when defineHeaders creates the body: so a function value of
// the export in this pack (fnRef, a RelocFn) names the thunk, as another
// pack's declaration of the C name does. emitThunk
// fills it.
func (g *packGen) addThunk(fi int, body *llvm.Function, lk llvm.Linkage, vis llvm.Visibility, dso bool) {
	sig := body.Sig()
	sig.Linkage, sig.Visibility, sig.DSOLocal = lk, vis, dso
	if g.cthunkFn == nil {
		g.cthunkFn = map[int]*llvm.Function{}
	}
	g.cthunkFn[fi] = g.m.AddFunction(g.u.Funcs[fi].Symbol, sig)
	g.cthunks = append(g.cthunks, thunkRec{fi: fi, lk: lk, vis: vis, dso: dso})
}

// emitThunk fills the thunk of thunked Funcs[fi] (addThunk's), whose
// body is body:
//
//	if FromCNeeded() { frame := {args..., result}; RunFromC(@sym.fromc, &frame); return frame.result }
//	return body(args...)
//
// and the pack-local @sym.fromc(frame), which loads the arguments, calls
// the body on the task and stores the result. The frame holds the lowered
// arguments as they came (an indirect argument is the caller's pointer,
// alive while the caller waits), so any signature passes through.
//
// lk, vis and dso are addThunk's, which the thunk already carries.
func (g *packGen) emitThunk(fi int, body *llvm.Function, lk llvm.Linkage, vis llvm.Visibility, dso bool) {
	m := g.m
	f := &g.u.Funcs[fi]
	th := g.cthunkFn[fi]
	sig := th.Sig()
	g.extraFns = append(g.extraFns, th)
	// The frame: each parameter, then the result (none for void), each in
	// a 16-byte cell (frameCell).
	hasRet := sig.Ret != m.Void()
	cells := len(sig.Params) + 1
	// @sym.fromc(frame)
	fc := m.AddFunction(f.Symbol+".fromc", llvm.FuncSig{Linkage: llvm.Internal, Ret: m.Void(), Params: []llvm.Type{m.Ptr()},
		ParamAttrs: []llvm.Attrs{llvm.Attrs{}.Flag("noundef")}, FnAttrs: g.fnAttrs(types.Void)})
	g.extraFns = append(g.extraFns, fc)
	fb := fc.NewBlock("entry")
	var args []llvm.Value
	for i, t := range sig.Params {
		args = append(args, fb.Load(t, fb.GEP8(fc.Param(0), m.ConstInt(m.I(64), frameCell(i)), 0), 1))
	}
	r := callBody(fb, body, args)
	if hasRet {
		fb.Store(r, fb.GEP8(fc.Param(0), m.ConstInt(m.I(64), frameCell(len(sig.Params))), 0), 1)
	}
	fb.Ret(nil)
	// The thunk.
	needF := g.funcDecl(g.funcIndex(mustKey(g, keyFromCNeeded), nil))
	runFi := g.funcIndex(mustKey(g, keyRunFromC), nil)
	runF := g.funcDecl(runFi)
	tb := th.NewBlock("entry")
	direct, slow := th.NewBlock("direct"), th.NewBlock("fromc")
	need := tb.CallFunc(needF, nil)
	if need.Ty != m.I(1) {
		need = tb.ICmp(llvm.INE, need, m.ConstInt(need.Ty, 0))
	}
	tb.CondBr(need, slow, direct)
	var params []llvm.Value
	for i := range sig.Params {
		params = append(params, th.Param(i))
	}
	dr := callBody(direct, body, params)
	if hasRet {
		direct.Ret(&dr)
	} else {
		direct.Ret(nil)
	}
	frame := slow.Alloca(m.Array(uint64(cells*16), m.I(8)), 16)
	for i := range sig.Params {
		slow.Store(th.Param(i), slow.GEP8(frame, m.ConstInt(m.I(64), frameCell(i)), 0), 1)
	}
	slow.CallFunc(runF, []llvm.Value{m.FuncRef(fc), frame})
	if hasRet {
		v := slow.Load(sig.Ret, slow.GEP8(frame, m.ConstInt(m.I(64), frameCell(len(sig.Params))), 0), 1)
		slow.Ret(&v)
	} else {
		slow.Ret(nil)
	}
}

// callBody calls a thunked export's body from its thunk or its .fromc
// with its own attributes, but never inlined: a fault in the body then
// has the body's own frame and FuncTable row, so the report names its
// site (a thunk's row has none), since a function value of the export
// is the thunk in its own pack too (TestRwRt_CFaultOwnHandler's release cb
// mode).
func callBody(b *llvm.Block, body *llvm.Function, args []llvm.Value) llvm.Value {
	sig := body.Sig()
	var argAttrs []llvm.Attrs
	if len(sig.ParamAttrs) != 0 {
		argAttrs = sig.ParamAttrs
	}
	return b.Call(body.Type(), body.Ref(), args, argAttrs, sig.RetAttrs, sig.FnAttrs.Without("alwaysinline", "inlinehint").Flag("noinline"))
}

// frameCell is cell i's offset in a thunk's frame: each lowered value in a
// 16-byte cell, which holds any value the C ABI passes in registers or by
// pointer on x86_64 (at most two eightbytes) without asking the layout.
func frameCell(i int) uint64 { return uint64(i) * 16 }

// mustKey is the item of a runtime key the unit holds (fromCFuncs checked).
func mustKey(g *packGen, k types.Key) types.ItemID {
	id, _ := g.items.ByKey(k)
	return id
}

// thunkRec is a thunked export of the pack, with the linkage its C name
// keeps.
type thunkRec struct {
	fi  int
	lk  llvm.Linkage
	vis llvm.Visibility
	dso bool
}

// imageBaseStub defines a weak MkImageBase returning 0 in an executable
// whose unit holds std.sys.LibraryImage (an export extern's thunk reaches
// it through FromCNeeded's lazy start, which an executable never takes:
// its runtime runs from Start), so the link resolves the name without
// asking the image for __ImageBase or __ehdr_start.
func (s *support) imageBaseStub(u *Unit, env Env) {
	items := env.Items()
	id, ok := items.ByKey(types.ItemKey("std.sys", "LibraryImage"))
	if !ok {
		return
	}
	held := false
	for i := range u.Funcs {
		if u.Funcs[i].Item == id {
			held = true
			break
		}
	}
	if !held {
		return
	}
	m := s.m
	f := m.AddFunction(symImageBase, llvm.FuncSig{Linkage: llvm.Weak, DSOLocal: true, Ret: m.I(64), FnAttrs: s.fnAttrs()})
	s.defs = append(s.defs, f)
	z := m.ConstInt(m.I(64), 0)
	f.NewBlock("entry").Ret(&z)
}
