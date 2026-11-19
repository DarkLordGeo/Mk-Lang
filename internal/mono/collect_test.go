// Why: Collect decides which instances exist and what they are called; keys and
// symbols must be stable and complete for codegen and the cache.
// What: tests of roots, substitution, instance keys and symbols, const value
// parameters, interface calls, fn literals by content, and a benchmark.
// How: builds small worlds by hand or from source, collects them, and compares
// keys, symbols and bodies, also across different interning orders.

package mono

import (
	"encoding/hex"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

func keysOf(in *Instances) []string {
	var out []string
	for _, i := range in.List {
		out = append(out, i.Key.Text)
	}
	return out
}

func (w *world) inst(t *testing.T, in *Instances, name string) *Instance {
	t.Helper()
	for i := range in.List {
		if in.List[i].Name == name {
			return &in.List[i]
		}
	}
	var names []string
	for _, i := range in.List {
		names = append(names, i.Name)
	}
	t.Fatalf("no instance %q among %q", name, names)
	return nil
}

// TestRoots: collection starts at the roots (Main, export externs,
// tests) and reaches exactly what they call; generic items are never roots;
// an extern is an instance with no body named as written.
func TestRoots(t *testing.T) {
	w := newWorld()
	puts := w.extern("puts", []types.TypeID{types.Int}, types.Void)
	g := w.fn("G", 1, []types.TypeID{w.tab.Param(0)}, types.Void)
	w.body(g).ret()
	f := w.fn("F", 0, nil, types.Void)
	fb := w.body(f)
	v := fb.temp(types.Void)
	fb.call(g, []types.GenericArg{ty(types.Int)}, v, arg(mir.Imm(types.Int, 1)))
	fb.call(puts, nil, v, arg(mir.Imm(types.Int, 2)))
	fb.ret()
	w.leaf("Unused")
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	mb.call(f, nil, mb.temp(types.Void))
	mb.ret()
	exp := w.fn("Exp", 0, nil, types.Void)
	w.items.Get(exp).Decl.(*types.FuncDecl).Sig.Extern = true
	w.body(exp).ret()
	// A test block is a root of a test build.
	tid := w.items.AddItem(&types.Item{Module: w.mod, Key: types.TestKey("m", "adds"), Kind: types.KindTest, Decl: &types.TestDecl{Name: "adds"}})
	tb := mir.NewBody(tid, w.span(), types.Void)
	tb.AddBlock(mir.OriginBody, mir.NoDefer)
	tb.Blocks[0].Term = mir.Terminator{Kind: mir.TermReturn, Span: tb.AddSpan(w.span())}
	w.bodies[tid] = tb

	in, ds := w.collect(t, Options{}, mainRoot(main), Root{Kind: RootExport, Item: exp}, Root{Kind: RootTest, Item: tid})
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	gk := keyOf(w, g, []types.GenericArg{ty(types.Int)}).Text
	want := []string{"m#test(\"adds\")", "m.Exp", "m.F", gk, "m.Main", "m.puts"}
	if got := keysOf(in); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("instances %q, want %q", got, want)
	}
	if !strings.HasPrefix(gk, "m.G@") || len(gk) != len("m.G@")+32 {
		t.Fatalf("generic key %q", gk)
	}
	p := w.inst(t, in, "m.puts")
	if p.Body != nil || p.Symbol != "puts" {
		t.Fatalf("extern instance: body %v symbol %q", p.Body != nil, p.Symbol)
	}
	e := w.inst(t, in, "m.Exp")
	if !e.AddrTaken || e.Symbol != "Exp" {
		t.Fatalf("export extern: AddrTaken %v symbol %q", e.AddrTaken, e.Symbol)
	}
	if mi := w.inst(t, in, "m.Main"); mi.Symbol != "mk.m.Main" || mi.Body != w.bodies[main] || mi.Depth != 0 {
		t.Fatalf("Main: symbol %q, own body %v, depth %d", mi.Symbol, mi.Body == w.bodies[main], mi.Depth)
	}
	gi := w.inst(t, in, "m.G[int]")
	if gi.Depth != 1 || gi.Body.GenericCount != 0 || gi.Body.Locals[1].Type != types.Int || gi.Generics != 1 {
		t.Fatalf("G[int]: depth %d, generic count %d, param %s", gi.Depth, gi.Body.GenericCount, w.tab.TypeString(gi.Body.Locals[1].Type, nil))
	}
	if w.bodies[g].Locals[1].Type != w.tab.Param(0) {
		t.Fatal("the generic body was modified")
	}
	if gi.Ops != 1 || gi.Owner != w.mod || source.Hash(gi.Content).IsZero() {
		t.Fatalf("G[int]: ops %d owner %d content %x", gi.Ops, gi.Owner, gi.Content)
	}

	mustICE(t, "is generic", func() { Collect(w, []Root{mainRoot(g)}, Options{}) })
}

// TestMonoSubstitution: Subst replaces parameters at every depth (records,
// fn types and their flows, arrays with a const length, named arguments,
// error-set parameters) and keeps SizeOf; Instantiate substitutes a
// whole body, folds SizeOf[T]() and RvConstArg, and leaves the generic body
// alone.
func TestMonoSubstitution(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0, p1 := tab.Param(0), tab.Param(1)
	wrap := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "Wrap"), Name: w.in.Intern("Wrap"), Kind: types.KindStruct})
	errA := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "ErrA"), Name: w.in.Intern("ErrA"), Kind: types.KindError, Decl: &types.ErrorDecl{}})
	setA := tab.ErrorSet([]types.ErrorMember{{Item: errA}})
	four := intConst(w.consts, 4)
	args := []types.GenericArg{ty(types.I32), ty(tab.ErrorSetType(setA)), types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: four})}
	paramSet := tab.ErrorSet([]types.ErrorMember{{IsParam: true, Param: 1}})
	cases := []struct{ in, want types.TypeID }{
		{tab.Ref(tab.Slice(p0)), tab.Ref(tab.Slice(types.I32))},
		{tab.Record([]types.RecordField{{Name: w.in.Intern("a"), Type: p0}, {Name: w.in.Intern("b"), Type: tab.Optional(p0)}}),
			tab.Record([]types.RecordField{{Name: w.in.Intern("a"), Type: types.I32}, {Name: w.in.Intern("b"), Type: tab.Optional(types.I32)}})},
		{tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(p0)}}, Result: tab.Ref(p0), Errors: paramSet,
			Sources: []types.Source{{Param: 1, Path: []types.FieldStep{{Field: 0, Type: p0}}}}}),
			tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(types.I32)}}, Result: tab.Ref(types.I32), Errors: setA,
				Sources: []types.Source{{Param: 1, Path: []types.FieldStep{{Field: 0, Type: types.I32}}}}})},
		// A per-field clause's paths are substituted as Sources' are.
		{tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(p0)}}, Result: tab.Ref(p0),
			Sources: []types.Source{{Param: 1}}, Fields: []types.FieldSources{{Field: 0, Sources: []types.Source{{Param: 1, Behind: true, Path: []types.FieldStep{{Field: 0, Type: p0}}}}}}}),
			tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(types.I32)}}, Result: tab.Ref(types.I32),
				Sources: []types.Source{{Param: 1}}, Fields: []types.FieldSources{{Field: 0, Sources: []types.Source{{Param: 1, Behind: true, Path: []types.FieldStep{{Field: 0, Type: types.I32}}}}}}})},
		{tab.Array(types.ConstArg{Kind: types.CAParam, Param: 2}, p0), tab.Array(types.ConstArg{Kind: types.CAValue, Value: four}, types.I32)},
		{tab.Named(wrap, []types.GenericArg{ty(tab.MutSlice(p0))}), tab.Named(wrap, []types.GenericArg{ty(tab.MutSlice(types.I32))})},
		{tab.ErrorUnion(paramSet, p0), tab.ErrorUnion(setA, types.I32)},
		{tab.Array(types.ConstArg{Kind: types.CASizeOf, Type: p0}, types.U8), tab.Array(types.ConstArg{Kind: types.CASizeOf, Type: types.I32}, types.U8)},
		{types.Str, types.Str},
	}
	for i, c := range cases {
		if got := Subst(tab, c.in, args); got != c.want {
			t.Errorf("case %d: Subst(%s) = %s, want %s", i, tab.TypeString(c.in, nil), tab.TypeString(got, nil), tab.TypeString(c.want, nil))
		}
	}
	mustICE(t, "type parameter 5 of 3", func() { Subst(tab, tab.Param(5), args) })
	mustICE(t, "given a const argument", func() { Subst(tab, tab.Param(2), args) })
	mustICE(t, "given a type argument", func() { Subst(tab, tab.Array(types.ConstArg{Kind: types.CAParam, Param: 0}, p0), args) })
	_ = p1

	// Instantiate: Id[T, const N: int](x: T): int { _2: [SizeOf[T]]u8; _0 = N }.
	id := w.fn("Id", 2, []types.TypeID{p0}, types.Int)
	fd := w.items.Get(id).Decl.(*types.FuncDecl)
	fd.Sig.Generics[1].Const, fd.Sig.Generics[1].Type = true, types.Int
	b := w.body(id)
	buf := b.temp(tab.Array(types.ConstArg{Kind: types.CASizeOf, Type: p0}, types.U8))
	b.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvConstArg, Len: types.ConstArg{Kind: types.CAParam, Param: 1}, Type: types.Int})
	gb := b.ret()
	seven := intConst(w.consts, 7)
	iargs := []types.GenericArg{ty(types.I64), types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: seven})}
	var ib *mir.Body
	if ie := source.Guard("t", source.Span{}, func() { ib = Instantiate(w, id, iargs, Options{Validate: true}) }); ie != nil {
		t.Fatalf("Instantiate: %s", ie)
	}
	if ib == gb || ib.GenericCount != 0 || ib.Locals[1].Type != types.I64 {
		t.Fatalf("instance body: shared %v, generic count %d, param %s", ib == gb, ib.GenericCount, tab.TypeString(ib.Locals[1].Type, nil))
	}
	if got, want := ib.Locals[buf].Type, tab.Array(types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, 8)}, types.U8); got != want {
		t.Fatalf("SizeOf[i64] local %s, want %s", tab.TypeString(got, nil), tab.TypeString(want, nil))
	}
	rv := ib.Blocks[0].Stmts[0].Rvalue
	if rv.Kind != mir.RvUse || rv.A.Kind != mir.OpImm || rv.A.Bits != 7 {
		t.Fatalf("RvConstArg became %v %v %d", rv.Kind, rv.A.Kind, rv.A.Bits)
	}
	if gb.Locals[1].Type != p0 || gb.GenericCount != 2 || gb.Blocks[0].Stmts[0].Rvalue.Kind != mir.RvConstArg {
		t.Fatal("the generic body was modified")
	}
	// Collected from a caller, the table is closed over the bodies.
	main := w.fn("Main", 0, nil, types.Int)
	mb := w.body(main)
	mb.call(id, iargs, mir.ReturnLocal, arg(mir.Imm(types.I64, 1)))
	mb.ret()
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
}

// keyedProgram builds Main -> F -> G[int], G[?&bool] -> G2[[]T] in w, after
// interning noise types in the order given, so TypeIDs differ between runs.
func keyedProgram(w *world, noise func()) types.ItemID {
	noise()
	tab := w.tab
	g2 := w.fn("G2", 1, []types.TypeID{tab.Param(0)}, types.Void)
	w.body(g2).ret()
	g := w.fn("G", 1, []types.TypeID{tab.Param(0)}, types.Void)
	gb := w.body(g)
	gb.call(g2, []types.GenericArg{ty(tab.Slice(tab.Param(0)))}, gb.temp(types.Void), arg(mir.Imm(types.Int, 0)))
	gb.ret()
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	v := mb.temp(types.Void)
	mb.call(g, []types.GenericArg{ty(types.Int)}, v, arg(mir.Imm(types.Int, 0)))
	mb.call(g, []types.GenericArg{ty(tab.Optional(tab.Ref(types.Bool)))}, v, arg(mir.Imm(types.Int, 0)))
	mb.ret()
	return main
}

func summary(in *Instances) string {
	var b strings.Builder
	for _, i := range in.List {
		fmt.Fprintf(&b, "%s %x %s %s %x %d\n", i.Key.Text, i.Key.Hash, i.Symbol, i.Name, i.Content, i.Depth)
	}
	return b.String()
}

// TestKeyCanonical: keys, hashes, symbols and content hashes do not
// depend on interning order: two tables built in different orders.
func TestKeyCanonical(t *testing.T) {
	w1, w2 := newWorld(), newWorld()
	m1 := keyedProgram(w1, func() {})
	m2 := keyedProgram(w2, func() {
		w2.tab.Slice(w2.tab.Optional(w2.tab.Ref(types.Bool)))
		w2.tab.Slice(types.Int)
		w2.tab.Ref(types.Bool)
		w2.tab.Param(0)
		w2.leaf("Noise")
	})
	if w1.tab.Optional(w1.tab.Ref(types.Bool)) == w2.tab.Optional(w2.tab.Ref(types.Bool)) {
		t.Fatal("the noise did not move TypeIDs")
	}
	in1, _ := w1.collect(t, Options{}, mainRoot(m1))
	in2, _ := w2.collect(t, Options{}, mainRoot(m2))
	if s1, s2 := summary(in1), summary(in2); s1 != s2 {
		t.Fatalf("interning order changed the table:\n%s\nvs\n%s", s1, s2)
	}
	if len(in1.List) != 5 {
		t.Fatalf("want 5 instances, got:\n%s", summary(in1))
	}
}

// TestMonoDeterministic: the root order, GOMAXPROCS and repetition change
// nothing.
func TestMonoDeterministic(t *testing.T) {
	w := newWorld()
	main := keyedProgram(w, func() {})
	other := w.leaf("Other")
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(0))
	runtime.GOMAXPROCS(1)
	a, _ := w.collect(t, Options{}, mainRoot(main), Root{Kind: RootExport, Item: other})
	runtime.GOMAXPROCS(8)
	b, _ := w.collect(t, Options{}, Root{Kind: RootExport, Item: other}, mainRoot(main))
	if summary(a) != summary(b) {
		t.Fatalf("collection differs:\n%s\nvs\n%s", summary(a), summary(b))
	}
}

// TestInstanceSymbolMatchesIface: Key.Hash is iface's InstanceName over the
// item key and Args, and a generic instance's symbol suffix is its first 16
// hex digits; a fn literal is named by its content.
func TestInstanceSymbolMatchesIface(t *testing.T) {
	w := newWorld()
	main := keyedProgram(w, func() {})
	in, _ := w.collect(t, Options{}, mainRoot(main))
	w2, lmain := literalProgram(t, 3)
	in2, _ := w2.collect(t, Options{}, mainRoot(lmain))
	for _, c := range []struct {
		w  *world
		in *Instances
	}{{w, in}, {w2, in2}} {
		for _, i := range c.in.List {
			it := c.w.items.Get(i.Item)
			if it.Kind == types.KindLiteral {
				h := c.w.h.FnRef(types.FnRef{Item: i.Item, Args: i.Args})
				if source.Hash(i.Key.Hash) != h || i.Symbol != "mk.fn."+h.String()[:16] {
					t.Errorf("literal %s: hash %x symbol %s", i.Name, i.Key.Hash, i.Symbol)
				}
				continue
			}
			h := c.w.h.InstanceName(it.Key, i.Args)
			if source.Hash(i.Key.Hash) != h {
				t.Errorf("%s: Key.Hash %x, InstanceName %s", i.Name, i.Key.Hash, h)
			}
			want := "mk." + it.Key.String()
			if len(i.Args) > 0 {
				want += "." + fmt.Sprintf("%016x", h.Uint64())
			}
			if i.Symbol != want {
				t.Errorf("%s: symbol %s, want %s", i.Name, i.Symbol, want)
			}
		}
	}
}

// literalProgram: Count(x: &int, const keep: fn(&int): bool): bool calls
// keep(x); n functions F<i> each pass an identical literal F<i>#fn0
// (returning true) and F_odd passes a different one (returning false).
func literalProgram(t *testing.T, n int) (*world, types.ItemID) {
	t.Helper()
	w := newWorld()
	tab := w.tab
	refInt := tab.Ref(types.Int)
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: refInt}}, Result: types.Bool})
	count := w.fnWith("Count", 0, []types.TypeID{refInt, fnT}, []bool{false, true}, types.Bool)
	cb := w.body(count)
	cb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(cb.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(cb.b, 1)))
	cb.ret()
	caller := func(name string, result bool) types.ItemID {
		f := w.fn(name, 0, []types.TypeID{refInt}, types.Bool)
		lit := w.literal(f, 0, []types.TypeID{refInt}, types.Bool)
		lb := w.body(lit)
		bit := uint64(0)
		if result {
			bit = 1
		}
		lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, bit)})
		lb.ret()
		c := w.consts.Intern(types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
			Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: lit}}}})
		fb := w.body(f)
		fb.call(count, nil, mir.ReturnLocal, arg(copyOf(fb.b, 1)), arg(mir.Const(fnT, c)))
		fb.ret()
		return f
	}
	var fs []types.ItemID
	for i := 0; i < n; i++ {
		fs = append(fs, caller(fmt.Sprintf("F%03d", i), true))
	}
	fs = append(fs, caller("Fodd", false))
	main := w.fn("Main", 0, []types.TypeID{refInt}, types.Void)
	mb := w.body(main)
	v := mb.temp(types.Bool)
	for _, f := range fs {
		mb.call(f, nil, v, arg(copyOf(mb.b, 1)))
	}
	mb.ret()
	return w, main
}

// TestFnLiteralByContent: 300 identical comparator literals give one
// instance of Count and one literal instance; a different literal gives its
// own; Count's call through keep is direct.
func TestFnLiteralByContent(t *testing.T) {
	w, main := literalProgram(t, 300)
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	var counts, lits []*Instance
	for i := range in.List {
		it := w.items.Get(in.List[i].Item)
		switch {
		case it.Kind == types.KindLiteral:
			lits = append(lits, &in.List[i])
		case it.Key.String() == "m.Count":
			counts = append(counts, &in.List[i])
		}
	}
	closedOver(t, w, in)
	if len(counts) != 2 || len(lits) != 2 {
		t.Fatalf("want 2 Count instances and 2 literals, got %d and %d:\n%s", len(counts), len(lits), summary(in))
	}
	if len(in.List) != 2+2+301+1 {
		t.Fatalf("want 306 instances, got %d", len(in.List))
	}
	for _, c := range counts {
		if c.Generics != 0 || len(c.Args) != 1 || !c.Args[0].IsConst {
			t.Fatalf("Count instance args %v", c.Args)
		}
		term := c.Body.Blocks[0].Term
		if term.Callee.Kind != mir.CallDirect || w.items.Get(term.Callee.Fn.Item).Kind != types.KindLiteral {
			t.Fatalf("call through keep is %v", term.Callee.Kind)
		}
		if !strings.HasPrefix(c.Symbol, "mk.m.Count.") {
			t.Fatalf("Count symbol %s", c.Symbol)
		}
	}
	for _, l := range lits {
		if !l.AddrTaken {
			t.Fatalf("literal %s not address-taken", l.Name)
		}
	}
	// The kept literal is the first in key order among equal ones.
	for _, l := range lits {
		if k := w.items.Get(l.Item).Key.String(); k != "m.F000#fn0" && k != "m.Fodd#fn0" {
			t.Fatalf("literal instance body from %s", k)
		}
	}
}

// TestConstFnArg_GenericBody: in Outer[T], Keep[T] is copied from an RvFnRef
// temporary into Count's const parameter; mono traces it, keys Count on
// Keep[i32], and makes the call through keep direct.
func TestConstFnArg_GenericBody(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0 := tab.Param(0)
	keep := w.fn("Keep", 1, []types.TypeID{tab.Ref(p0)}, types.Bool)
	kb := w.body(keep)
	kb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
	kb.ret()
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(p0)}}, Result: types.Bool})
	count := w.fnWith("Count", 1, []types.TypeID{tab.Ref(p0), fnT}, []bool{false, true}, types.Bool)
	cb := w.body(count)
	cb.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(cb.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(cb.b, 1)))
	cb.ret()
	outer := w.fn("Outer", 1, []types.TypeID{tab.Ref(p0)}, types.Bool)
	ob := w.body(outer)
	tmp := ob.temp(fnT)
	ob.assign(tmp, mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: keep, Args: ob.b.AddGenericArgs(ty(p0))}, Type: fnT})
	ob.call(count, []types.GenericArg{ty(p0)}, mir.ReturnLocal, arg(copyOf(ob.b, 1)), arg(copyOf(ob.b, tmp)))
	ob.ret()
	closed := w.fn("Closed", 0, []types.TypeID{tab.Ref(types.I32)}, types.Bool)
	clb := w.body(closed)
	clb.call(outer, []types.GenericArg{ty(types.I32)}, mir.ReturnLocal, arg(copyOf(clb.b, 1)))
	clb.ret()
	in, ds := w.collect(t, Options{}, mainRoot(closed))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	var c *Instance
	for i := range in.List {
		if strings.HasPrefix(in.List[i].Name, "m.Count[") {
			c = &in.List[i]
		}
	}
	if c == nil || len(c.Args) != 2 || c.Generics != 1 || !c.Args[1].IsConst {
		t.Fatalf("Count instance missing or args wrong:\n%s", summary(in))
	}
	term := c.Body.Blocks[0].Term
	if term.Callee.Kind != mir.CallDirect || term.Callee.Fn.Item != keep {
		t.Fatalf("call through keep: %v", term.Callee.Kind)
	}
	if ga := c.Body.GenericArgs[term.Callee.Fn.Args.Start:term.Callee.Fn.Args.End()]; len(ga) != 1 || ga[0].Type != types.I32 {
		t.Fatalf("Keep's arguments %v", ga)
	}
	w.inst(t, in, "m.Keep[i32]")
	// The key differs from Count[i32] keyed on another function.
	other := keyOf(w, count, []types.GenericArg{ty(types.I32), c.Args[1]})
	if other.Text != c.Key.Text {
		t.Fatalf("key %s, recomputed %s", c.Key.Text, other.Text)
	}
	_ = hex.EncodeToString
}

// TestMonoInterfaceResolved: an interface call on a concrete Self becomes a
// direct call of the impl's method, which is collected.
func TestMonoInterfaceResolved(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0 := tab.Param(0)
	iface := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "Show"), Name: w.in.Intern("Show"), Kind: types.KindInterface, Decl: &types.InterfaceDecl{}})
	showInt := w.fn("ShowInt", 0, []types.TypeID{tab.Ref(types.Int)}, types.Void)
	w.body(showInt).ret()
	w.impls[implKey{iface, 0, types.Int}] = showInt
	gen := w.fn("Use", 1, []types.TypeID{tab.Ref(p0)}, types.Void)
	gb := w.body(gen)
	gb.callTerm(mir.Callee{Kind: mir.CallInterface, Iface: iface, Method: 0, SelfType: p0}, gb.temp(types.Void), mir.NoLocal, arg(copyOf(gb.b, 1)))
	gb.ret()
	main := w.fn("Main", 0, []types.TypeID{tab.Ref(types.Int)}, types.Void)
	mb := w.body(main)
	mb.call(gen, []types.GenericArg{ty(types.Int)}, mb.temp(types.Void), arg(copyOf(mb.b, 1)))
	mb.ret()
	in, _ := w.collect(t, Options{}, mainRoot(main))
	u := w.inst(t, in, "m.Use[int]")
	if c := u.Body.Blocks[0].Term.Callee; c.Kind != mir.CallDirect || c.Fn.Item != showInt {
		t.Fatalf("interface call became %v", c.Kind)
	}
	w.inst(t, in, "m.ShowInt")
	if w.bodies[gen].Blocks[0].Term.Callee.Kind != mir.CallInterface {
		t.Fatal("the generic body was modified")
	}
	// No impl is an ICE: check proved the bound.
	mb2 := w.fn("Main2", 0, []types.TypeID{tab.Ref(types.Bool)}, types.Void)
	b2 := w.body(mb2)
	b2.call(gen, []types.GenericArg{ty(types.Bool)}, b2.temp(types.Void), arg(copyOf(b2.b, 1)))
	b2.ret()
	mustICE(t, "no impl of m.Show", func() { Collect(w, []Root{mainRoot(mb2)}, Options{}) })
}

// TestMonoValidatesInstances: with Validate set, an instance body that is
// wrong once substituted is an ICE naming the instance; the
// same body passes at an argument that fits.
func TestMonoValidatesInstances(t *testing.T) {
	w := newWorld()
	g := w.fn("G", 1, nil, w.tab.Param(0))
	gb := w.body(g)
	gb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Int, 1)})
	gb.ret()
	ok := []types.GenericArg{ty(types.Int)}
	if ie := source.Guard("t", source.Span{}, func() { Instantiate(w, g, ok, Options{Validate: true}) }); ie != nil {
		t.Fatalf("G[int]: %s", ie)
	}
	mustICE(t, "instance m.G[bool]", func() { Instantiate(w, g, []types.GenericArg{ty(types.Bool)}, Options{Validate: true}) })
	mustICE(t, "open argument", func() { Instantiate(w, g, []types.GenericArg{ty(w.tab.Param(0))}, Options{}) })
}

// TestFoldRespectsFacts (symbol and body part): mono folds
// nothing, so instances whose arguments differ only in facts codegen
// attaches (a niche optional, an error set's members, a Cell) get distinct
// keys, symbols, bodies and content hashes.
func TestFoldRespectsFacts(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0 := tab.Param(0)
	first := w.fn("First", 1, []types.TypeID{tab.Slice(p0)}, types.Void)
	w.body(first).ret()
	cell := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "Cell"), Name: w.in.Intern("Cell"), Kind: types.KindStruct, Decl: &types.StructDecl{}})
	e1 := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "E1"), Name: w.in.Intern("E1"), Kind: types.KindError, Decl: &types.ErrorDecl{}})
	e2 := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "E2"), Name: w.in.Intern("E2"), Kind: types.KindError, Decl: &types.ErrorDecl{}})
	set1 := tab.ErrorSetType(tab.ErrorSet([]types.ErrorMember{{Item: e1}}))
	set2 := tab.ErrorSetType(tab.ErrorSet([]types.ErrorMember{{Item: e1}, {Item: e2}}))
	pairs := [][2]types.TypeID{
		{tab.Ref(types.Int), tab.Optional(tab.Ref(types.Int))},
		{set1, set2},
		{types.I64, tab.Named(cell, []types.GenericArg{ty(types.I64)})},
	}
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	v := mb.temp(types.Void)
	for _, p := range pairs {
		for _, a := range p {
			mb.call(first, []types.GenericArg{ty(a)}, v, arg(mir.Imm(types.Int, 0)))
		}
	}
	mb.ret()
	in, _ := w.collect(t, Options{}, mainRoot(main))
	for _, p := range pairs {
		x, y := keyOf(w, first, []types.GenericArg{ty(p[0])}), keyOf(w, first, []types.GenericArg{ty(p[1])})
		ix, ok1 := in.Lookup(x.Text)
		iy, ok2 := in.Lookup(y.Text)
		if !ok1 || !ok2 || x.Text == y.Text || ix.Symbol == iy.Symbol || ix.Body == iy.Body || ix.Content == iy.Content {
			t.Fatalf("%s and %s folded or missing", tab.TypeString(p[0], nil), tab.TypeString(p[1], nil))
		}
	}
}

// BenchmarkMonoCollect: instances per second on a generated program of
// 1,000 generic instantiations G[[i]int].
func BenchmarkMonoCollect(b *testing.B) {
	w := newWorld()
	tab := w.tab
	g := w.fn("G", 1, []types.TypeID{tab.Param(0)}, types.Void)
	gb := w.body(g)
	gb.assign(gb.temp(tab.Slice(tab.Param(0))), mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Int, 0)})
	gb.ret()
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	v := mb.temp(types.Void)
	for i := 0; i < 1000; i++ {
		a := tab.Array(types.ConstArg{Kind: types.CAValue, Value: intConst(w.consts, int64(i))}, types.Int)
		mb.call(g, []types.GenericArg{ty(a)}, v, arg(mir.Imm(types.Int, 0)))
	}
	mb.ret()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in, _ := Collect(w, []Root{mainRoot(main)}, Options{})
		if len(in.List) != 1001 {
			b.Fatalf("%d instances", len(in.List))
		}
	}
	b.ReportMetric(float64(1001*b.N)/b.Elapsed().Seconds(), "instances/s")
}

// TestConstParamForwarded: Count[T](x, const keep) forwards keep to
// Inner[T](x, const keep); in Count's instance the forwarded operand is the
// constant itself, so Inner is keyed on the same function and its call
// through keep is direct.
func TestConstParamForwarded(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0 := tab.Param(0)
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: tab.Ref(p0)}}, Result: types.Bool})
	keep := w.fn("Keep", 1, []types.TypeID{tab.Ref(p0)}, types.Bool)
	kb := w.body(keep)
	kb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
	kb.ret()
	inner := w.fnWith("Inner", 1, []types.TypeID{tab.Ref(p0), fnT}, []bool{false, true}, types.Bool)
	ib := w.body(inner)
	ib.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(ib.b, 2)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(ib.b, 1)))
	ib.ret()
	count := w.fnWith("Count", 1, []types.TypeID{tab.Ref(p0), fnT}, []bool{false, true}, types.Bool)
	cb := w.body(count)
	cb.call(inner, []types.GenericArg{ty(p0)}, mir.ReturnLocal, arg(copyOf(cb.b, 1)), arg(copyOf(cb.b, 2)))
	cb.ret()
	main := w.fn("Main", 0, []types.TypeID{tab.Ref(types.I32)}, types.Bool)
	mb := w.body(main)
	fnI := Subst(tab, fnT, []types.GenericArg{ty(types.I32)})
	c := w.consts.Intern(types.ConstValue{Type: fnI, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: keep, Args: []types.GenericArg{ty(types.I32)}}}}})
	mb.call(count, []types.GenericArg{ty(types.I32)}, mir.ReturnLocal, arg(copyOf(mb.b, 1)), arg(mir.Const(fnI, c)))
	mb.ret()
	in, ds := w.collect(t, Options{}, mainRoot(main))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	var got *Instance
	for i := range in.List {
		if strings.HasPrefix(in.List[i].Name, "m.Inner[") {
			got = &in.List[i]
		}
	}
	if got == nil || len(got.Args) != 2 || got.Args[1].Const.Value != c {
		t.Fatalf("Inner not keyed on Keep[i32]:\n%s", summary(in))
	}
	if term := got.Body.Blocks[0].Term; term.Callee.Kind != mir.CallDirect || term.Callee.Fn.Item != keep {
		t.Fatalf("Inner's call through keep: %v", term.Callee.Kind)
	}
	w.inst(t, in, "m.Keep[i32]")
}
