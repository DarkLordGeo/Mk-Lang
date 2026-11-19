// Why: an error return should say where it happened. Every error return
// records its site in a trace the runtime prints after `error: <Name>`, and
// lower is where the return sites are known.
//
// What: traceInfo (finds the std.rt hooks once per body), siteShape,
// traceCall and siteConst, which emit the call and its constant Site.
//
// How: right before its TermReturn, every error return calls
// std.rt.TraceFail(&site) (a fail of a new error value, NewError) or
// std.rt.TraceTry(&site) (a try edge, or a fail of a bound error value), as
// an ordinary direct TermCall in every profile. mono drops both calls in
// release, and the evaluator runs them as no-ops.

package lower

import (
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// traceHooks are the items the trace needs, found by key (the KnownItems
// "inert" rule: when std.rt, its Site or a hook is absent or not as
// expected, no call is emitted).
type traceHooks struct {
	fail, try types.ItemID
	siteRef   types.TypeID // &std.rt.Site
	site      types.ItemID
}

// traceInfo looks the hooks up once per body. ok is false in a body of
// std.rt itself (the hooks' own callees must not trace), without Sources,
// or when std.rt does not declare the hooks and Site in the expected shape.
func (bl *builder) traceInfo() (*traceHooks, bool) {
	if bl.trace != nil {
		return bl.trace, bl.trace.fail != types.NoItem
	}
	bl.trace = &traceHooks{}
	items := bl.cfg.Items
	if bl.cfg.Sources == nil {
		return bl.trace, false
	}
	if it := items.Get(bl.b.Item); it != nil {
		if m := items.Module(it.Module); m != nil && m.Path == "std.rt" {
			return bl.trace, false
		}
	}
	site, ok := items.ByKey(types.ItemKey("std.rt", "Site"))
	if !ok || !siteShape(bl.cfg, site) {
		return bl.trace, false
	}
	siteRef := bl.tab.Ref(bl.tab.Named(site, nil))
	hook := func(name string) types.ItemID {
		id, ok := items.ByKey(types.ItemKey("std.rt", name))
		if !ok {
			return types.NoItem
		}
		fd, ok := items.Get(id).Decl.(*types.FuncDecl)
		if !ok || len(fd.Sig.Params) != 1 || fd.Sig.Params[0].Type != siteRef ||
			fd.Sig.Result != types.Void || fd.Sig.Errors != types.EmptyErrors || len(fd.Sig.Generics) != 0 {
			return types.NoItem
		}
		return id
	}
	f, t := hook("TraceFail"), hook("TraceTry")
	if f == types.NoItem || t == types.NoItem {
		return bl.trace, false
	}
	*bl.trace = traceHooks{fail: f, try: t, siteRef: siteRef, site: site}
	return bl.trace, true
}

// siteShape reports whether std.rt.Site is {file: str, line: u32, col: u32}
// padded, so its layout is the 24 bytes codegen's site rows also use
// file at 0, line at 16, col at 20.
func siteShape(cfg *Config, site types.ItemID) bool {
	d, ok := cfg.Items.Get(site).Decl.(*types.StructDecl)
	if !ok || d.Packed != 0 || len(d.Generics) != 0 || len(d.Fields) != 3 {
		return false
	}
	u32 := cfg.Types.IntType(32, false)
	want := []struct {
		name string
		t    types.TypeID
	}{{"file", types.Str}, {"line", u32}, {"col", u32}}
	for i, w := range want {
		if cfg.Interner.Str(d.Fields[i].Name) != w.name || d.Fields[i].Type != w.t {
			return false
		}
	}
	return true
}

// traceCall emits, before an error return at at, TraceFail(&site) for a new
// error and TraceTry(&site) otherwise; the site is at's start, with the
// path codegen's panic sites use (Config.SitePath).
func (bl *builder) traceCall(newError bool, at source.Span) {
	if !bl.open() || !at.File.IsValid() {
		return
	}
	h, ok := bl.traceInfo()
	if !ok {
		return
	}
	hook := h.try
	if newError {
		hook = h.fail
	}
	site := bl.siteConst(h, at)
	dest := bl.hiddenLocal(types.Void, mir.LocalTemp, at)
	next := bl.newBlock()
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: hook}},
		Args: bl.b.AddArgs(mir.Arg{Operand: site, Mode: mir.ArgCopy}), Dest: bl.b.Place(dest), Target: next, Spawn: mir.NoLocal}, at)
	bl.cur = next
}

// siteConst is the &Site constant of at: a reference to a Site value
// {file, line, col}, the file a str constant of the site path.
func (bl *builder) siteConst(h *traceHooks, at source.Span) mir.Operand {
	path := ""
	if bl.cfg.SitePath != nil {
		path = bl.cfg.SitePath(at.File)
	} else if f := bl.cfg.Sources.File(at.File); f != nil {
		path = f.Path
	}
	lc := bl.cfg.Sources.LineCol(source.Pos{File: at.File, Off: at.Start})
	file := bl.cfg.Consts.Get(bl.strConst([]byte(path), at).Const)
	v := types.ConstValue{Type: bl.tab.Named(h.site, nil), Align: 8, Bytes: make([]byte, 24)}
	copy(v.Bytes, file.Bytes)
	v.Relocs = append([]types.Reloc(nil), file.Relocs...)
	types.StoreUint(v.Bytes[16:], 4, uint64(lc.Line))
	types.StoreUint(v.Bytes[20:], 4, uint64(lc.Col))
	if err := v.Canonicalize(8); err != nil {
		source.ICEf(at, "lower: site constant: %v", err)
	}
	sv := bl.cfg.Consts.Intern(v)
	r := types.ConstValue{Type: h.siteRef, Align: 8, Bytes: make([]byte, 8), Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: sv}}}
	if err := r.Canonicalize(8); err != nil {
		source.ICEf(at, "lower: site reference: %v", err)
	}
	return mir.Const(h.siteRef, bl.cfg.Consts.Intern(r))
}
