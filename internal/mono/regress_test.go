// Why: instance bodies must name their callees exactly as the table keys
// them, or codegen's exact lookup fails.
// What: tests of canonical literal references, folded
// SizeOf arguments, const parameters fed every form check accepts, fn values
// as const parameters, and E0708's count per build root.
// How: hand-built programs collected with Validate, then the table and bodies
// compared.

package mono

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// twoLiteralProgram: Count(x: &int, const keep: fn(&int): bool) calls keep;
// F1 and F2 each take their own literal (F1#fn0, F2#fn0, equal bodies) as a
// fn value (RvFnRef) and pass it as a const to Count. Main calls F2 then F1;
// F1 and F2 are one level, built in key order, so F1's literal is met first.
func twoLiteralProgram(w *world) (main, f1, f2 types.ItemID) {
	tab := w.tab
	refInt := tab.Ref(types.Int)
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: refInt}}, Result: types.Bool})
	count := w.fnWith("Count", 0, []types.TypeID{refInt, fnT}, []bool{false, true}, types.Bool)
	cb := w.body(count)
	cb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(cb.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(cb.b, 1)))
	cb.ret()
	caller := func(name string) types.ItemID {
		f := w.fn(name, 0, []types.TypeID{refInt}, types.Bool)
		lit := w.literal(f, 0, []types.TypeID{refInt}, types.Bool)
		lb := w.body(lit)
		lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
		lb.ret()
		c := w.consts.Intern(types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
			Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: lit}}}})
		fb := w.body(f)
		v := fb.temp(fnT)
		fb.assign(v, mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: lit}, Type: fnT})
		fb.call(count, nil, mir.ReturnLocal, arg(copyOf(fb.b, 1)), arg(mir.Const(fnT, c)))
		fb.ret()
		return f
	}
	f1, f2 = caller("F1"), caller("F2")
	main = w.fn("Main", 0, []types.TypeID{refInt}, types.Void)
	mb := w.body(main)
	r := mb.temp(types.Bool)
	mb.call(f2, nil, r, arg(copyOf(mb.b, 1)))
	mb.call(f1, nil, r, arg(copyOf(mb.b, 1)))
	mb.ret()
	return main, f1, f2
}

// TestLiteralReferencesCanonical: two functions with identical fn
// literals give one literal instance, and both callers' RvFnRef and RelocFn
// name that instance's item, so codegen's exact lookup finds it. The
// kept literal is the first met (F1#fn0); F1's body needs no copy, F2's is
// copied and its own body is left alone.
func TestLiteralReferencesCanonical(t *testing.T) {
	w := newWorld()
	main, f1, f2 := twoLiteralProgram(w)
	before := w.bodies[f2]
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	var lits []*Instance
	for i := range in.List {
		if w.items.Get(in.List[i].Item).Kind == types.KindLiteral {
			lits = append(lits, &in.List[i])
		}
	}
	if len(lits) != 1 {
		t.Fatalf("want one literal instance, got %d:\n%s", len(lits), summary(in))
	}
	if k := w.items.Get(lits[0].Item).Key.String(); k != "m.F1#fn0" {
		t.Fatalf("kept literal %s, want the first one met, m.F1#fn0", k)
	}
	fi := w.inst(t, in, "m.F2")
	if fi.Body == before {
		t.Fatal("F2's instance body is its own body: its literal reference was not rewritten")
	}
	if before.Blocks[0].Stmts[0].Rvalue.Fn.Item == lits[0].Item {
		t.Fatal("F2's own body was modified")
	}
	if w.inst(t, in, "m.F1").Body != w.bodies[f1] {
		t.Fatal("F1's body needed no rewrite, but was copied")
	}
	// The canonical lookup finds the instance by either literal.
	for _, f := range []types.ItemID{f1, f2} {
		lit := w.bodies[f].Blocks[0].Stmts[0].Rvalue.Fn.Item
		if i, ok := in.Find(lit, nil); !ok || in.List[i].Item != lits[0].Item {
			t.Fatalf("Find(%s) = %d, %v", w.items.Get(lit).Key, i, ok)
		}
	}
}

// TestSizeOfArgumentFolded: a non-generic Main passes SizeOf[i32]()
// as Len's const generic argument and declares a [SizeOf[i32]()]u8 local; the
// instance body carries the folded CAValue, equal to Len's instance Args.
func TestSizeOfArgumentFolded(t *testing.T) {
	w := newWorld()
	tab := w.tab
	ln := w.fn("Len", 1, nil, types.Int)
	fd := w.items.Get(ln).Decl.(*types.FuncDecl)
	fd.Sig.Generics[0].Const, fd.Sig.Generics[0].Type = true, types.Int
	lb := w.body(ln)
	lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvConstArg, Len: types.ConstArg{Kind: types.CAParam, Param: 0}, Type: types.Int})
	lb.ret()
	main := w.fn("Main", 0, nil, types.Int)
	mb := w.body(main)
	sz := types.ConstArg{Kind: types.CASizeOf, Type: types.I32}
	buf := mb.temp(tab.Array(sz, types.U8))
	mb.call(ln, []types.GenericArg{types.ConstGenericArg(sz)}, mir.ReturnLocal)
	gb := mb.ret()
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	mi := w.inst(t, in, "m.Main")
	if mi.Body == gb {
		t.Fatal("Main's instance body is its own body, with CASizeOf left in it")
	}
	four := types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 4)}
	if got := mi.Body.Locals[buf].Type; got != tab.Array(four, types.U8) {
		t.Fatalf("buf is %s", tab.TypeString(got, nil))
	}
	if gb.GenericArgs[0].Const.Kind != types.CASizeOf {
		t.Fatal("Main's own body was modified")
	}
}

// constParamProgram: Twice(const k: int): int returns k. Get[const n: int]
// passes n on (lower's `_t = RvConstArg(n); Twice(copy _t)`). ByName passes
// the named const K (lower's `_t = RvConstRef K; Twice(copy _t)`). Main
// calls Get[3], ByName and Twice(3).
func constParamProgram(w *world) (main, twice types.ItemID) {
	twice = w.fnWith("Twice", 0, []types.TypeID{types.Int}, []bool{true}, types.Int)
	tb := w.body(twice)
	tb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: copyOf(tb.b, 1)})
	tb.ret()
	get := w.fn("Get", 1, nil, types.Int)
	fd := w.items.Get(get).Decl.(*types.FuncDecl)
	fd.Sig.Generics[0].Const, fd.Sig.Generics[0].Type = true, types.Int
	gb := w.body(get)
	gt := gb.temp(types.Int)
	gb.assign(gt, mir.Rvalue{Kind: mir.RvConstArg, Len: types.ConstArg{Kind: types.CAParam, Param: 0}, Type: types.Int})
	gb.call(twice, nil, mir.ReturnLocal, arg(copyOf(gb.b, gt)))
	gb.ret()
	k := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "K"), Name: w.in.Intern("K"), Kind: types.KindConst,
		Decl: &types.ConstDecl{Type: types.Int, Value: intConst(w.consts, 3), HasValue: true}})
	byName := w.fn("ByName", 0, nil, types.Int)
	bb := w.body(byName)
	bt := bb.temp(types.Int)
	bb.assign(bt, mir.Rvalue{Kind: mir.RvConstRef, Item: k, Type: types.Int})
	bb.call(twice, nil, mir.ReturnLocal, arg(copyOf(bb.b, bt)))
	bb.ret()
	main = w.fn("Main", 0, nil, types.Int)
	mb := w.body(main)
	v := mb.temp(types.Int)
	three := types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 3)}
	mb.call(get, []types.GenericArg{types.ConstGenericArg(three)}, v)
	mb.call(byName, nil, v)
	mb.call(twice, nil, v, arg(mir.Imm(types.Int, 3)))
	mb.ret()
	return main, twice
}

// TestConstParamFromConstArgAndNamedConst: a const value parameter fed
// by a forwarded const generic parameter, or by a named const, keys the
// callee on the value (no ICE), the same instance as Twice(3), and the
// caller's argument becomes a constant operand.
func TestConstParamFromConstArgAndNamedConst(t *testing.T) {
	w := newWorld()
	main, twice := constParamProgram(w)
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	var n int
	for i := range in.List {
		if in.List[i].Item == twice {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one Twice instance, got %d:\n%s", n, summary(in))
	}
	want := keyOf(w, twice, []types.GenericArg{types.ConstGenericArg(types.ConstArg{Kind: types.CAValue,
		Value: (&collector{env: w}).immConst(types.Int, 3)})})
	if _, ok := in.Lookup(want.Text); !ok {
		t.Fatalf("no instance keyed as Twice(3):\n%s", summary(in))
	}
}

// TestConstParamFnValueIsICE: a function with a const value parameter
// used as a fn value is check's error to report; if one reaches mono, the
// backstop is an ICE naming the item, never a miscompile.
func TestConstParamFnValueIsICE(t *testing.T) {
	w := newWorld()
	twice := w.fnWith("Twice", 0, []types.TypeID{types.Int}, []bool{true}, types.Int)
	tb := w.body(twice)
	tb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: copyOf(tb.b, 1)})
	tb.ret()
	fnT := w.tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.Int}}, Result: types.Int})
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	f := mb.temp(fnT)
	mb.assign(f, mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: twice}, Type: fnT})
	mb.ret()
	mustICE(t, "m.Twice has const parameters and is used as a value", func() { Collect(w, []Root{mainRoot(main)}, Options{}) })
}

// TestCountPerBuildRoot: the count limit is per build root. With
// MaxCount 10, two roots each reaching 6 distinct instances (7 with the
// root) stay under it; a third root reaching 11 is refused, with one E0708
// for that root only.
func TestCountPerBuildRoot(t *testing.T) {
	w := newWorld()
	g := w.fn("G", 1, nil, types.Void)
	w.body(g).ret()
	root := func(name string, ts []types.TypeID) types.ItemID {
		f := w.fn(name, 0, nil, types.Void)
		fb := w.body(f)
		v := fb.temp(types.Void)
		for _, a := range ts {
			fb.call(g, []types.GenericArg{ty(a)}, v)
		}
		fb.ret()
		return f
	}
	tab := w.tab
	r1 := root("R1", []types.TypeID{types.I8, types.I16, types.I32, types.I64, types.U8, types.U16})
	r2 := root("R2", []types.TypeID{types.U32, types.U64, types.Bool, types.Char, types.Str, types.Int})
	var many []types.TypeID
	for i := 0; i < 10; i++ {
		many = append(many, tab.Array(types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, int64(i+1))}, types.U8))
	}
	r3 := root("R3", many)
	in, ds := w.collect(t, Options{MaxCount: 10}, mainRoot(r1), Root{Kind: RootExport, Item: r2})
	if len(ds) != 0 {
		t.Fatalf("two roots of 7 instances each under MaxCount 10: %v", ds)
	}
	if len(in.List) != 14 {
		t.Fatalf("want 14 instances, got %d", len(in.List))
	}
	in, ds = w.collect(t, Options{MaxCount: 10}, mainRoot(r1), Root{Kind: RootExport, Item: r2}, Root{Kind: RootExport, Item: r3})
	if len(ds) != 1 || ds[0].Code != diag.E0708 {
		t.Fatalf("want one E0708 for R3, got %v", ds)
	}
	if chain, _ := ds[0].FactValue("chain"); len(chain.List) != 2 || chain.List[0].Str != "m.R3" {
		t.Fatalf("chain %v", chain.List)
	}
	// R1 and R2 are complete; R3 is built, its instances are not.
	if len(in.List) != 15 {
		t.Fatalf("want 15 instances, got %d:\n%s", len(in.List), summary(in))
	}
}

// TestNamedConstLiteralCanonical: `const Keep: fn(&int): bool = fn(x)
// { true }` holds a literal equal to F1's. G reads Keep twice: as a value
// (RvConstRef into a local it calls through) and as Count's const argument
// (lower's `_t = RvConstRef Keep; Count(x, copy _t)`). Both reads become the
// canonical value naming the kept literal, the table is closed over the
// bodies, and the bodies validate (w.collect runs mir.Validate).
func TestNamedConstLiteralCanonical(t *testing.T) {
	w := newWorld()
	tab := w.tab
	refInt := tab.Ref(types.Int)
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: refInt}}, Result: types.Bool})
	main0, _, _ := twoLiteralProgram(w)
	count, _ := w.items.ByKey(types.ItemKey("m", "Count"))
	keepID := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "Keep"), Name: w.in.Intern("Keep"), Kind: types.KindConst})
	lit := w.literal(keepID, 0, []types.TypeID{refInt}, types.Bool)
	lb := w.body(lit)
	lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
	lb.ret()
	kv := w.consts.Intern(types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: lit}}}})
	w.items.Get(keepID).Decl = &types.ConstDecl{Type: fnT, Value: kv, HasValue: true}
	g := w.fn("G", 0, []types.TypeID{refInt}, types.Bool)
	gb := w.body(g)
	f := gb.temp(fnT)
	gb.assign(f, mir.Rvalue{Kind: mir.RvConstRef, Item: keepID, Type: fnT})
	r := gb.temp(types.Bool)
	gb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(gb.b, f)}, r, mir.NoLocal, arg(copyOf(gb.b, 1)))
	tmp := gb.temp(fnT)
	gb.assign(tmp, mir.Rvalue{Kind: mir.RvConstRef, Item: keepID, Type: fnT})
	gb.call(count, nil, mir.ReturnLocal, arg(copyOf(gb.b, 1)), arg(copyOf(gb.b, tmp)))
	gb.ret()
	// Main2 -> Main -> F1, F2 and Main2 -> H -> G: F1, F2 and G are one
	// level, built in key order, so F1's literal is met first.
	h := w.fn("H", 0, []types.TypeID{refInt}, types.Bool)
	hb := w.body(h)
	hb.call(g, nil, mir.ReturnLocal, arg(copyOf(hb.b, 1)))
	hb.ret()
	main := w.fn("Main2", 0, []types.TypeID{refInt}, types.Void)
	mb := w.body(main)
	v := mb.temp(types.Void)
	mb.call(main0, nil, v, arg(copyOf(mb.b, 1)))
	mb.call(h, nil, mb.temp(types.Bool), arg(copyOf(mb.b, 1)))
	mb.ret()

	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	var lits []*Instance
	for i := range in.List {
		if w.items.Get(in.List[i].Item).Kind == types.KindLiteral {
			lits = append(lits, &in.List[i])
		}
	}
	if len(lits) != 1 || w.items.Get(lits[0].Item).Key.String() != "m.F1#fn0" {
		t.Fatalf("want the one literal m.F1#fn0:\n%s", summary(in))
	}
	gi := w.inst(t, in, "m.G")
	if gi.Body == w.bodies[g] {
		t.Fatal("G's body was not rewritten")
	}
	names := func(c types.ConstID) bool {
		rs := w.consts.Get(c).Relocs
		return len(rs) == 1 && rs[0].Kind == types.RelocFn && rs[0].Fn.Item == lits[0].Item
	}
	for _, blk := range gi.Body.Blocks {
		for _, st := range blk.Stmts {
			switch rv := st.Rvalue; rv.Kind {
			case mir.RvConstRef:
				t.Fatalf("an RvConstRef of Keep survives in G")
			case mir.RvUse:
				if rv.A.Kind != mir.OpConst || !names(rv.A.Const) || rv.A.Type != fnT {
					t.Fatalf("G's read of Keep is %v of type %s", rv.A.Kind, tab.TypeString(rv.A.Type, nil))
				}
			}
		}
		if blk.Term.Kind == mir.TermCall && blk.Term.Callee.Kind == mir.CallDirect && blk.Term.Callee.Fn.Item == count {
			o := gi.Body.Args[blk.Term.Args.Start+1].Operand
			if o.Kind != mir.OpConst || !names(o.Const) {
				t.Fatalf("Count's const argument in G is %v", o.Kind)
			}
		}
	}
	if w.bodies[g].Blocks[0].Stmts[0].Rvalue.Kind != mir.RvConstRef {
		t.Fatal("G's own body was modified")
	}
}
