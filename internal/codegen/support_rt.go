// Why: a few std.rt functions depend on the whole program (which Main to
// call, which errors exist, which functions were emitted), so their bodies
// can only be written once the unit is known: in the support pack.
//
// What: rtBodies, with callMain (std.rt.CallMain, the root's Main in
// whichever of its four forms), errorName (std.rt.ErrorName), emptySlice
// (std.rt.ProfileTable outside --pgo-gen) and hostedPanicHook; the error
// and path display helpers ErrorDisplay, ErrorDisplayIn, DisplayKey and
// DisplaySitePath; and displaySite. std.rt.FuncTable is functable.go's.
//
// How: ErrorName is a binary search of the error-name table, which holds a
// row for each error that can exist as a value of the program (errnames.go).

package codegen

import (
	"sort"
	"strings"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// rtBodies emits the bodies of the std.rt intrinsics the build declares,
// through a packGen over the support module, so each signature is the abi
// lowering every caller uses.
func (s *support) rtBodies(u *Unit, env Env) {
	g := newPackGen(u, -1, env, s.cfg, s.m)
	items := env.Items()
	look := func(name string) (*types.Signature, bool) {
		id, ok := items.ByKey(types.ItemKey("std.rt", name))
		if !ok {
			return nil, false
		}
		fd, ok := items.Get(id).Decl.(*types.FuncDecl)
		if !ok {
			return nil, false
		}
		return &fd.Sig, true
	}
	if sig, ok := look("ErrorName"); ok {
		s.errorName(g, sig)
	}
	if sig, ok := look("ProfileTable"); ok {
		s.profileTable(g, sig, u) // pgo_gen.go: empty outside --pgo-gen
	}
	if sig, ok := look("CallMain"); ok {
		s.callMain(g, sig)
	}
	if _, ok := look("CallPanicHook"); ok && s.t.OS != types.OSFreestanding {
		s.hostedPanicHook()
	}
	if sig, ok := look("FuncTable"); ok {
		s.funcTable(g, sig) // last: its rows name every support function
	}
}

// hostedPanicHook emits the weak default of MkPanicHook on a hosted target:
// it returns at once, so the report follows. A package's `export extern MkPanicHook(info:
// &runtime.PanicInfo)` wins at link time, as a freestanding program's does
// over freeHooks' trapping default; std.rt's CallPanicHook calls it once
// per panic, before the report.
func (s *support) hostedPanicHook() {
	m := s.m
	f := m.AddFunction(symPanicHook, llvm.FuncSig{Linkage: llvm.Weak, DSOLocal: true, Ret: m.Void(),
		Params: []llvm.Type{m.Ptr()}, ParamAttrs: []llvm.Attrs{llvm.Attrs{}.Flag("noundef")}, FnAttrs: s.fnAttrs().Flag("cold")})
	s.defs = append(s.defs, f)
	f.NewBlock("entry").Ret(nil)
}

// defineMk defines a support-pack function of a Mk signature and returns a
// fnGen positioned in its first body block; the caller ends with done.
func (s *support) defineMk(g *packGen, sig *types.Signature, name string) *fnGen {
	var params []types.TypeID
	for _, p := range sig.Params {
		params = append(params, p.Type)
	}
	ls := g.lowerSig(params, len(params), false, sig.ReturnType(g.tt))
	fn := s.m.AddFunction(name, g.funcSig(ls, llvm.External, llvm.Default, true, g.fnAttrs(ls.result)))
	s.defs = append(s.defs, fn)
	b := mir.NewBody(sig.Item, source.Span{}, ls.result)
	for _, p := range params {
		b.AddParam(p, 0, 0)
	}
	fg := &fnGen{g: g, f: &Func{Symbol: name}, b: b, fn: fn, sig: ls}
	fg.prologue()
	first := fg.newBlock()
	fg.blocks = append(fg.blocks, first)
	fg.cur = first
	return fg
}

// done closes the entry block once every temporary is allocated.
func (fg *fnGen) done() { fg.entry.Br(fg.blocks[0]) }

// preludeModule is std.PreludeModule, which codegen
// may not import (internal/archtest): the module whose errors print bare.
const preludeModule = "std.core"

// ErrorDisplay is an error item's display form: a prelude error by its name
// (OutOfMemory), another
// std error by its module's last segment and name (fs.NotFound), a user
// error by its stable key, module path and name (main.NotFound).
func ErrorDisplay(items *types.Items, it *types.Item) string {
	return ErrorDisplayIn(items, it, nil)
}

// ErrorDisplayIn is ErrorDisplay with a dependency's canonical id shown by
// its local name: names maps a canonical id, a user key's first segment, to
// the name the root package knows it by, so lib_dbf98d23.Bad prints as
// lib.Bad. nil names, or an id it lacks, leaves the key as it is.
func ErrorDisplayIn(items *types.Items, it *types.Item, names map[string]string) string {
	key := it.Key.String()
	if it.Module == types.NoModule {
		return key
	}
	mod := items.Module(it.Module)
	if !mod.Std {
		return DisplayKey(key, names)
	}
	name, ok := strings.CutPrefix(key, mod.Path+".")
	if !ok {
		source.ICEf(it.Span, "codegen: error key %s is not in its module %s", key, mod.Path)
	}
	if mod.Path == preludeModule {
		return name
	}
	return mod.Path[strings.LastIndexByte(mod.Path, '.')+1:] + "." + name
}

// DisplayKey is a user key or module path with its first segment, a
// canonical id, replaced by its local name in names; an empty local name
// drops the segment and its dot. A key whose first segment names no
// dependency is returned as it is.
func DisplayKey(key string, names map[string]string) string {
	if len(names) == 0 {
		return key
	}
	first, rest, dotted := strings.Cut(key, ".")
	local, ok := names[first]
	if !ok {
		return key
	}
	switch {
	case !dotted:
		return local
	case local == "":
		return rest
	}
	return local + "." + rest
}

// errorName: one { i32 code, ptr name, i64 len } row per live error item of the
// program, sorted by code, named by its display form (ErrorDisplay); an
// unknown code is "unknown error". Two errors with one code are an ICE naming
// both by stable key.
func (s *support) errorName(g *packGen, sig *types.Signature) {
	m := g.m
	type row struct {
		code       uint32
		name, text string // stable key, display form
	}
	var rows []row
	live := g.liveErrors() // errnames.go
	// The slot is the ID: in a corpus session another unit's resolve may be
	// adding an item right now, and AddItem writes it.ID after the slot is
	// published. A live error is this program's, so its ID
	// is set.
	for i := 1; i < g.items.NumItems(); i++ {
		it := g.items.Get(types.ItemID(i))
		if it == nil || it.Kind != types.KindError || !live[types.ItemID(i)] {
			continue
		}
		rows = append(rows, row{g.errorCode(it.ID), it.Key.String(), ErrorDisplayIn(g.items, it, g.cfg.Display)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].code != rows[j].code {
			return rows[i].code < rows[j].code
		}
		return rows[i].name < rows[j].name
	})
	for i := 1; i < len(rows); i++ {
		if rows[i].code == rows[i-1].code {
			source.ICEf(source.Span{}, "codegen: errors %s and %s have one code %d", rows[i-1].name, rows[i].name, rows[i].code)
		}
	}
	rowT := m.Struct(false, m.I(32), m.Ptr(), m.I(64))
	var vals []llvm.Value
	for _, r := range rows {
		vals = append(vals, m.ConstStruct(false, m.ConstInt(m.I(32), uint64(r.code)), g.bytesGlobal([]byte(r.text)),
			m.ConstInt(m.I(64), uint64(len(r.text)))))
	}
	fg := s.defineMk(g, sig, "mk.std.rt.ErrorName")
	defer fg.done()
	i64 := m.I(64)
	unknown := g.bytesGlobal([]byte("unknown error"))
	out := fg.locals[mir.ReturnLocal]
	result := func(p, l llvm.Value) {
		fg.cur.Store(p, out.v, 8)
		fg.cur.Store(l, fg.gep(out.v, 8), 8)
		fg.ret()
	}
	n := uint64(len(vals))
	if n == 0 {
		result(unknown, m.ConstInt(i64, 13))
		return
	}
	init := m.ConstArray(rowT, vals...)
	table := m.GlobalRef(m.AddGlobal("mk.support.errors", llvm.GlobalAttrs{Linkage: llvm.Private, Constant: true,
		UnnamedAddr: true, Align: 8, Type: init.Ty, Init: init}))
	code := fg.loadScalar(fg.locals[1], g.reprOf(types.U32), false)
	lo, hi := fg.tempBytes(8, 8), fg.tempBytes(8, 8)
	fg.cur.Store(m.ConstInt(i64, 0), lo.v, 8)
	fg.cur.Store(m.ConstInt(i64, n), hi.v, 8)
	loop, body, found, next, miss := fg.newBlock(), fg.newBlock(), fg.newBlock(), fg.newBlock(), fg.newBlock()
	fg.cur.Br(loop)
	fg.cur = loop
	l, h := fg.cur.Load(i64, lo.v, 8), fg.cur.Load(i64, hi.v, 8)
	fg.cur.CondBr(fg.cur.ICmp(llvm.IULT, l, h), body, miss)
	fg.cur = body
	l, h = fg.cur.Load(i64, lo.v, 8), fg.cur.Load(i64, hi.v, 8)
	mid := fg.cur.Bin(llvm.LShr, fg.cur.Bin(llvm.Add, l, h, 0), m.ConstInt(i64, 1), 0)
	rp := fg.cur.GEPBytes(table, fg.cur.Bin(llvm.Mul, mid, m.ConstInt(i64, 24), 0), true)
	rc := fg.cur.Load(m.I(32), rp, 8)
	fg.cur.CondBr(fg.cur.ICmp(llvm.IEQ, rc, code), found, next)
	fg.cur = next
	less := fg.cur.ICmp(llvm.IULT, rc, code)
	fg.cur.Store(fg.cur.Select(less, fg.cur.Bin(llvm.Add, mid, m.ConstInt(i64, 1), 0), l), lo.v, 8)
	fg.cur.Store(fg.cur.Select(less, h, mid), hi.v, 8)
	fg.cur.Br(loop)
	fg.cur = found
	result(fg.cur.Load(m.Ptr(), fg.gep(rp, 8), 8), fg.cur.Load(i64, fg.gep(rp, 16), 8))
	fg.cur = miss
	result(unknown, m.ConstInt(i64, 13))
}

// emptySlice is a function returning the empty slice: a dangling pointer
// equal to the element alignment, 8, and length 0.
func (s *support) emptySlice(g *packGen, sig *types.Signature, name string) {
	m := g.m
	fg := s.defineMk(g, sig, name)
	defer fg.done()
	out := fg.locals[mir.ReturnLocal]
	fg.cur.Store(m.ConstIntToPtr(8), out.v, 8)
	fg.cur.Store(m.ConstInt(m.I(64), 0), fg.gep(out.v, 8), 8)
	fg.ret()
}

// callMain calls <root>.Main(sys) in its form (void, int, E!void, E!int),
// or Config.Main, the driver's wrapper of the same result, and returns
// MainResult { code: u32, exit: int }: code 0 and the int result (0 for
// void), or the error's code.
func (s *support) callMain(g *packGen, sig *types.Signature) {
	m := g.m
	key := s.cfg.Main
	if key.IsZero() {
		key = types.ItemKey(s.cfg.RootModule, "Main")
	}
	mainID, ok := g.items.ByKey(key)
	if !ok && s.cfg.Library {
		fg := s.defineMk(g, sig, "mk.std.rt.CallMain") // MainResult{0, 0}: never run
		fs := g.layout(sig.Result).Fields
		out := fg.locals[mir.ReturnLocal]
		fg.cur.Store(m.ConstInt(m.I(32), 0), fg.gep(out.v, fs[0].Offset), alignAt(out.align, fs[0].Offset))
		fg.cur.Store(m.ConstInt(m.I(64), 0), fg.gep(out.v, fs[1].Offset), alignAt(out.align, fs[1].Offset))
		fg.ret()
		fg.done()
		return
	}
	if !ok {
		return // no Main: a test build's runner supplies the entry
	}
	msig := g.declSig(mainID)
	if len(msig.Params) > 1 {
		source.ICEf(msig.Span, "codegen: Main takes %d parameters", len(msig.Params))
	}
	fg := s.defineMk(g, sig, "mk.std.rt.CallMain")
	defer fg.done()
	var params []types.TypeID
	var args []value
	for _, p := range msig.Params {
		params = append(params, p.Type)
		args = append(args, fg.loadAt(fg.locals[1], p.Type))
	}
	rt := msig.ReturnType(g.tt)
	ls := g.lowerSig(params, len(params), false, rt)
	vis := llvm.Hidden
	if s.t.OS == types.OSWindows {
		vis = llvm.Default
	}
	fn := m.Declare("mk."+g.items.Get(mainID).Key.String(), g.funcSig(ls, llvm.External, vis, true, llvm.Attrs{}.Flag("nounwind")))
	var res addr
	if g.reprOf(rt).kind != rZero {
		res = fg.temp(rt)
		fg.callLowered(fn.Ref(), fn.Type(), ls, args, &res, fn.Sig().FnAttrs)
	} else {
		fg.callLowered(fn.Ref(), fn.Type(), ls, args, nil, fn.Sig().FnAttrs)
	}
	out := fg.locals[mir.ReturnLocal]
	fs := g.layout(sig.Result).Fields // code u32, exit int
	code, exit := m.ConstInt(m.I(32), 0), m.ConstInt(m.I(64), 0)
	switch {
	case msig.Errors != types.EmptyErrors:
		l := g.layout(rt)
		code = fg.cur.Load(m.I(32), fg.gep(res.v, l.Tag.Offset), alignAt(res.align, l.Tag.Offset))
		if msig.Result != types.Void {
			off := g.variant(rt, 0).Fields[0].Offset
			v := fg.cur.Load(m.I(64), fg.gep(res.v, off), alignAt(res.align, off))
			exit = fg.cur.Select(fg.cur.ICmp(llvm.IEQ, code, m.ConstInt(m.I(32), 0)), v, exit)
		}
	case msig.Result != types.Void:
		exit = fg.loadScalar(res, g.reprOf(msig.Result), false)
	}
	fg.cur.Store(code, fg.gep(out.v, fs[0].Offset), alignAt(out.align, fs[0].Offset))
	fg.cur.Store(exit, fg.gep(out.v, fs[1].Offset), alignAt(out.align, fs[1].Offset))
	fg.ret()
}

// DisplaySitePath is a site's package-relative path p with a dependency's
// canonical id shown by its local name in names: a fetched package's
// "<cid>@<hex>/" prefix becomes "<name>@<hex>/", and a vendored one's
// "vendor/<cid>/" becomes "vendor/<name>/". Any other path, and every path
// with no names, is returned as it is.
func DisplaySitePath(p string, names map[string]string) string {
	if len(names) == 0 {
		return p
	}
	if rest, ok := strings.CutPrefix(p, "vendor/"); ok {
		cid, tail, ok := strings.Cut(rest, "/")
		if local := names[cid]; ok && local != "" {
			return "vendor/" + local + "/" + tail
		}
		return p
	}
	head, tail, ok := strings.Cut(p, "/")
	if !ok {
		return p
	}
	cid, hex, ok := strings.Cut(head, "@")
	if local := names[cid]; ok && local != "" {
		return local + "@" + hex + "/" + tail
	}
	return p
}

// displaySite is the initializer of a std.rt.Site constant (lower's trace
// site) whose file names a dependency by its canonical id:
// the same row with the file shown by its local name (DisplaySitePath over
// Config.Display), as a panic site's row names it (siteRow, Env.Path). The
// lowered body keeps the canonical-id path, so a module's stored MIR is the
// same whichever package uses it; Display is in every pack's key
// (PackFacts). ok is false for any other constant, or when nothing maps.
func (g *packGen) displaySite(cv *types.ConstValue) (init llvm.Value, ok bool) {
	if len(g.cfg.Display) == 0 || len(cv.Bytes) != 24 || len(cv.Relocs) != 1 ||
		cv.Relocs[0].Kind != types.RelocData || cv.Relocs[0].Offset != 0 {
		return llvm.Value{}, false
	}
	site, found := g.items.ByKey(types.ItemKey("std.rt", "Site"))
	if !found || cv.Type != g.tt.Named(site, nil) {
		return llvm.Value{}, false
	}
	file := g.env.Consts().Get(cv.Relocs[0].Data).Bytes
	n := types.LoadUint(cv.Bytes[8:], 8)
	if n > uint64(len(file)) {
		return llvm.Value{}, false
	}
	path := string(file[:n])
	shown := DisplaySitePath(path, g.cfg.Display)
	if shown == path {
		return llvm.Value{}, false
	}
	m := g.m
	return m.ConstStruct(false, g.bytesGlobal([]byte(shown)), m.ConstInt(m.I(64), uint64(len(shown))),
		m.ConstInt(m.I(32), types.LoadUint(cv.Bytes[16:], 4)), m.ConstInt(m.I(32), types.LoadUint(cv.Bytes[20:], 4))), true
}
