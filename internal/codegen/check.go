// Why: Mk keeps its run-time checks in every profile, so each one must cost
// as little as possible on the path where it passes and still report the
// exact source site when it fails.
//
// What: the panic hooks (PanicAt, PanicMsg, PanicErr) and hookFn, the site
// rows (site, siteRow), trap, trapWeights, panicCall and errCodeOf.
//
// How: every check branches to a per-site cold trap block that calls the
// panic hook and ends in unreachable, weighted 1 : 1048575 against the
// passing edge. Each site is a private constant row in the pack, made once
// per span and name.

package codegen

import (
	"strconv"

	"mk/internal/llvm"
	"mk/internal/source"
	"mk/internal/types"
)

// The panic hooks (std/rt/panic.mk). The hook is PanicAt, not Panic: Panic
// is a universe name (E0102), so std declares PanicAt. They live in std.rt
// on a hosted target and in std.free on x86_64-freestanding, with the same
// signatures and a Site of the same layout.
const (
	hookPanic    = "PanicAt"
	hookPanicMsg = "PanicMsg"
	hookPanicErr = "PanicErr"
)

// hookModule is the std module of the panic hooks and their Site for the
// build's target: std.free on freestanding, else std.rt.
func (g *packGen) hookModule() string {
	if g.cfg.Target.OS == types.OSFreestanding {
		return "std.free"
	}
	return "std.rt"
}

// hook is a std function codegen calls, declared with its lowered signature.
type hook struct {
	fn  *llvm.Function
	sig *lsig
}

// siteType is &Site when std's Site is in the build (so the facts are its
// size and alignment), else &u8, which has the same ABI.
func (g *packGen) siteRefType() types.TypeID {
	if g.siteRef != types.NoType {
		return g.siteRef
	}
	if id, ok := g.items.ByKey(types.ItemKey(g.hookModule(), "Site")); ok {
		g.siteRef = g.tt.Ref(g.tt.Named(id, nil))
	} else {
		g.siteRef = g.tt.Ref(types.U8)
	}
	return g.siteRef
}

// panicAttrs are the call-site attributes of every panic hook call.
var panicAttrs = llvm.Attrs{}.Flag("nounwind").Flag("noreturn").Flag("cold")

// hookFn is a panic hook by name (hookPanic, hookPanicMsg, hookPanicErr):
// PanicAt(code: u32, site: &Site), PanicMsg(code, site, msg: str),
// PanicErr(code, site, err: u32); never, in hookModule.
// When the unit holds the hook's std instance, the hook is that function
// (funcDecl): its definition in the pack that defines it, else the one
// declaration every call of the pack shares. Only a unit without std.rt (a
// test unit over a stub runtime) gets a declaration built here.
func (g *packGen) hookFn(name string) *hook {
	if h, ok := g.hooks[name]; ok {
		return h
	}
	if fi, ok := g.hookIndex(name); ok {
		h := &hook{fn: g.funcDecl(fi), sig: g.funcSigOf(fi)}
		g.hooks[name] = h
		return h
	}
	params := []types.TypeID{types.U32, g.siteRefType()}
	switch name {
	case hookPanicMsg:
		params = append(params, types.Str)
	case hookPanicErr:
		params = append(params, types.U32)
	}
	ls := g.lowerSig(params, len(params), false, types.Never)
	vis := llvm.Hidden
	if g.cfg.Target.OS == types.OSWindows {
		vis = llvm.Default
	}
	fn := g.m.Declare("mk."+g.hookModule()+"."+name, g.funcSig(ls, llvm.External, vis, true,
		llvm.Attrs{}.Flag("nounwind").Flag("noreturn").Flag("cold")))
	h := &hook{fn: fn, sig: ls}
	g.hooks[name] = h
	return h
}

// hookIndex is the Funcs index of the hook instance named name ("PanicAt")
// in hookModule, when the build has the item and the unit has its
// (non-generic) instance.
func (g *packGen) hookIndex(name string) (int, bool) {
	id, ok := g.items.ByKey(types.ItemKey(g.hookModule(), name))
	if !ok {
		return -1, false
	}
	return g.findFunc(id, nil)
}

// siteKey identifies one panic site row.
type siteKey struct {
	path      string
	line, col uint32
}

// site is the address of the current statement's site row: a private
// unnamed_addr constant { ptr, i64, i32, i32 } (std.rt.Site: file str,
// line u32, col u32; 24 bytes), deduplicated per pack, named
// mk.pos.<symbol>.<n> after the first function that used it.
func (fg *fnGen) site() llvm.Value {
	v, made := fg.g.siteRow(fg.spanOf(fg.span), "mk.pos."+fg.f.Symbol+"."+strconv.Itoa(fg.nsite))
	if made {
		fg.nsite++
	}
	return v
}

// siteRow is the pack's site row of span's start, deduplicated per pack;
// a new row is the global name, and made reports that it was created.
func (g *packGen) siteRow(sp source.Span, name string) (v llvm.Value, made bool) {
	k := siteKey{}
	if sp.File.IsValid() {
		k.path = g.env.Path(sp.File)
		lc := g.env.LineCol(source.Pos{File: sp.File, Off: sp.Start})
		k.line, k.col = lc.Line, lc.Col
	}
	if v, ok := g.sites[k]; ok {
		return v, false
	}
	m := g.m
	init := m.ConstStruct(false, g.bytesGlobal([]byte(k.path)), m.ConstInt(m.I(64), uint64(len(k.path))),
		m.ConstInt(m.I(32), uint64(k.line)), m.ConstInt(m.I(32), uint64(k.col)))
	gl := m.AddGlobal(name, llvm.GlobalAttrs{Linkage: llvm.Private,
		Constant: true, UnnamedAddr: true, Align: 8, Type: init.Ty, Init: init})
	v = m.GlobalRef(gl)
	g.sites[k] = v
	return v, true
}

// trapWeights is the !prof of a check's branch: the trap edge cold.
func (fg *fnGen) trapWeights(trapFirst bool) llvm.Attach {
	md := fg.g.m.Metadata()
	if trapFirst {
		return llvm.Prof(md.BranchWeights(1, 1048575))
	}
	return llvm.Prof(md.BranchWeights(1048575, 1))
}

// trap branches to a fresh trap block when cond (i1) is true; emission
// continues in the fall-through block.
func (fg *fnGen) trap(cond llvm.Value, code types.PanicCode) {
	t := fg.newBlock()
	c := fg.newBlock()
	fg.cur.CondBr(cond, t, c, fg.trapWeights(true))
	fg.cur = t
	fg.panicCall(code, nil)
	fg.cur = c
}

// panicCall ends the current block with a panic hook call and unreachable:
// PanicAt with no message, PanicMsg with a str, PanicErr with an error-set
// value (its code is the message).
func (fg *fnGen) panicCall(code types.PanicCode, msg *value) {
	fg.panicCallV(fg.g.m.ConstInt(fg.g.m.I(32), uint64(code)), msg)
}

// panicCallV is panicCall with the code as an i32 value (raw.Fail).
func (fg *fnGen) panicCallV(code llvm.Value, msg *value) {
	g := fg.g
	args := []value{{t: types.U32, kind: rScalar, v: code},
		{t: g.siteRefType(), kind: rScalar, v: fg.site()}}
	name := hookPanic
	if msg != nil {
		if g.tt.Kind(msg.t) == types.KStr {
			name = hookPanicMsg
			args = append(args, *msg)
		} else {
			name = hookPanicErr
			args = append(args, value{t: types.U32, kind: rScalar, v: fg.errCodeOf(*msg)})
		}
	}
	h := g.hookFn(name)
	fg.callLowered(h.fn.Ref(), h.fn.Type(), h.sig, args, nil, panicAttrs)
	fg.cur.Unreachable()
}

// errCodeOf is the u32 code of an error-set or error-union value.
func (fg *fnGen) errCodeOf(v value) llvm.Value {
	g, m := fg.g, fg.g.m
	if v.kind != rAgg {
		source.ICEf(fg.spanOf(fg.span), "codegen: error value of type %s is not in memory", g.tt.TypeString(v.t, nil))
	}
	l := g.layout(v.t)
	a := addr{v: fg.gep(v.a.v, l.Tag.Offset), align: alignAt(v.a.align, l.Tag.Offset)}
	return fg.cur.Load(m.I(32), a.v, a.align, m.Metadata().NoUndef())
}
