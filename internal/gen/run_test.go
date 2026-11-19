// Why: binding and running gen lines is where user generators meet the
// compiler, and each misuse has its own error code.
// What: tests of binding and running gen lines, the host's reflection and
// reads, E0301, E0304, E0901 to E0903, E0905, E0906, E0908, fresh names and
// determinism, with the gen1, emit and fieldAt MIR builders.
// How: hand-built generators run over a hand-built world; wantCodes compares
// the codes of the Output.

package gen

import (
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
	"mk/internal/eval"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// gen1 builds a generator of module lib taking (g, t: Type) whose body is
// build's, with &t and &*g ready in locals tr and gr.
func (w *world) gen1(name string, build func(x *bb, tr, gr mir.LocalID) mir.BlockID) types.ItemID {
	g := w.generator("lib", name, w.typeT())
	x := w.body(g)
	tr := x.ref(2, false)
	gr := x.refPlace(x.deref(1), w.named(w.known.Gen), false)
	x.flush(0)
	x.ret(build(x, tr, gr))
	x.done()
	return g
}

// emit ends blk with g.Emit(quote(text, splices...)).
func (x *bb) emit(blk mir.BlockID, text string, splices ...mir.Operand) mir.BlockID {
	q, b := x.quote(blk, text, splices...)
	_, b2 := x.api(b, "Gen:Emit", cp(1), cp(q))
	return b2
}

// field0 is t.Fields()[i] behind a reference.
func (x *bb) fieldAt(blk mir.BlockID, tr mir.LocalID, i int) (mir.LocalID, mir.BlockID) {
	fs, b := x.api(blk, "Type:Fields", cp(tr))
	ft := x.w.named(x.w.known.Field)
	idx, f := x.local(types.Int), x.local(ft)
	x.assign(b, lp(idx), use(mir.Imm(types.Int, uint64(i))))
	x.assign(b, lp(f), use(mir.Copy(x.b.Place(fs, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: ft}))))
	fr := x.ref(f, false)
	x.flush(b)
	return fr, b
}

func codes(out *Output) []string {
	var cs []string
	for _, d := range out.Diags {
		cs = append(cs, d.Code.String())
	}
	return cs
}

func wantCodes(t *testing.T, out *Output, want ...string) {
	t.Helper()
	if got := codes(out); strings.Join(got, ",") != strings.Join(want, ",") {
		for _, d := range out.Diags {
			t.Logf("%s at %v: %s %v", d.Code, d.At, d.Message, d.Facts)
		}
		t.Fatalf("codes %v, want %v", got, want)
	}
}

func TestE0301_GeneratorSignature(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	noG := w.fn("lib", "NoG", types.Void, w.typeT())
	ret := w.fn("lib", "Ret", types.Int, w.genMut(), w.typeT())
	st, _ := w.structType("lib", "St", true)
	gen := w.generator("lib", "Generic", w.typeT())
	w.items.Get(gen).Decl.(*types.FuncDecl).Sig.Generics = []types.GenericParam{{Name: w.in.Intern("T")}}
	for _, g := range []types.ItemID{noG, ret, st, gen} {
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.X", g, typeArg(user))})
		wantCodes(t, out, "E0301")
		if _, ok := out.Diags[0].FactValue("expected"); !ok {
			t.Errorf("%s: no expected fact", w.items.Get(g).Key)
		}
		if out.Lines[0].Ran {
			t.Errorf("%s ran", w.items.Get(g).Key)
		}
	}
}

func TestE0304_GenArgCount(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.generator("lib", "G", w.typeT())
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.G", g, typeArg(user), typeArg(user))})
	wantCodes(t, out, "E0304")
	if _, ok := out.Diags[0].FactValue("signature"); !ok {
		t.Fatal("no signature fact")
	}
}

func TestE0301_GenArgForms(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.gen1("N", func(x *bb, tr, gr mir.LocalID) mir.BlockID { return 0 })
	gi := w.generator("lib", "I", types.I8)
	x := w.body(gi)
	x.ret(x.emit(0, "const K = $0", cp(2)))
	x.done()
	lit := func(kind ast.TokenKind, raw string, neg bool) Arg {
		return Arg{Kind: ArgLiteral, Lit: &ast.BasicLit{Kind: kind, Raw: raw}, Neg: neg, Span: w.span()}
	}
	for _, c := range []struct {
		gen  types.ItemID
		arg  Arg
		want string
	}{
		{g, Arg{Kind: ArgOther, Span: w.span()}, "E0301"},
		{g, lit(ast.TokInt, "3", false), "E0301"},
		{gi, typeArg(user), "E0301"},
		{gi, lit(ast.TokInt, "128", false), "E0301"},
		{gi, lit(ast.TokStr, `"s"`, false), "E0301"},
		{gi, lit(ast.TokInt, "128", true), ""},
		{gi, lit(ast.TokInt, "0x7f", false), ""},
	} {
		l := w.line(0, "lib.X", c.gen, c.arg)
		out := w.context(1).Run(w.module("main"), []Line{l})
		if c.want == "" {
			wantCodes(t, out)
			continue
		}
		wantCodes(t, out, c.want)
		if out.Diags[0].At != c.arg.Span && c.arg.Span != (source.Span{}) {
			t.Errorf("%+v: at %v, want the argument", c.arg, out.Diags[0].At)
		}
	}
	// The folded literal reaches the generator: -128 as i8.
	l := w.line(0, "lib.I", gi, lit(ast.TokInt, "128", true))
	w2out := w.context(1).Run(w.module("main"), []Line{l})
	if !strings.Contains(w2out.Text(), "const K = -128") {
		t.Fatalf("literal argument:\n%s", w2out.Text())
	}
	if d := w.items.Get(l.Item).Decl.(*types.GenDecl); d.Generator != gi || len(d.Args) != 1 || d.Args[0].Kind != types.GenLiteral || d.Fuel != types.DefaultFuel {
		t.Fatalf("GenDecl %+v", d)
	}
}

func TestE0908_GeneratorSameModule(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("main", "User", false)
	g := w.generator("main", "Local", w.typeT())
	l := w.line(0, "Local", g, typeArg(user))
	out := w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0908")
	if out.Diags[0].At != l.Span {
		t.Fatalf("at %v", out.Diags[0].At)
	}
}

func TestE0908_FuelSameModuleConst(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.gen1("G", func(x *bb, tr, gr mir.LocalID) mir.BlockID { return 0 })
	k := w.item("main", "F", types.KindConst, &types.ConstDecl{Type: types.U64})
	l := w.line(0, "lib.G", g, typeArg(user))
	l.FuelItem, l.FuelSpan = k, w.span()
	out := w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0908")
	if out.Diags[0].At != l.FuelSpan {
		t.Fatalf("at %v, want the fuel operand", out.Diags[0].At)
	}
	l2 := w.line(1, "lib.G", g, typeArg(user))
	l2.FuelOther, l2.FuelSpan = true, w.span()
	out = w.context(1).Run(w.module("main"), []Line{l2})
	wantCodes(t, out, "E0705")
	// A literal fuel, and a lower module's const, bind.
	lk := w.item("lib", "Fuel", types.KindConst, &types.ConstDecl{Type: types.U64, HasValue: true, Value: w.intVal(types.U64, 77)})
	l3 := w.line(2, "lib.G", g, typeArg(user))
	l3.FuelItem, l3.FuelSpan = lk, w.span()
	out = w.context(1).Run(w.module("main"), []Line{l3})
	wantCodes(t, out)
	if d := w.items.Get(l3.Item).Decl.(*types.GenDecl); d.Fuel != 77 {
		t.Fatalf("fuel %d, want 77", d.Fuel)
	}
}

// loopGen loops forever.
func (w *world) loopGen() types.ItemID {
	return w.gen1("Loop", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		h := x.block()
		x.gotoB(0, h)
		x.gotoB(h, h)
		return x.block()
	})
}

func TestE0902_GeneratorFuel(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.loopGen()
	l := w.line(0, "lib.Loop", g, typeArg(user))
	l.FuelLit, l.FuelSpan = &ast.BasicLit{Kind: ast.TokInt, Raw: "100"}, w.span()
	out := w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0902")
	if out.Diags[0].At != l.Span || out.Lines[0].Steps > 100 {
		t.Fatalf("at %v, steps %d", out.Diags[0].At, out.Lines[0].Steps)
	}
}

func TestE0901_GeneratorPanic(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	for _, c := range []struct{ api, arg string }{{"Gen:Ident", "1x"}, {"Gen:Ident", "if"}, {"Gen:Fresh", "a b"}} {
		g := w.gen1("P"+c.arg, func(x *bb, tr, gr mir.LocalID) mir.BlockID {
			g := gr
			if c.api == "Gen:Fresh" {
				g = 1
			}
			_, b := x.api(0, c.api, cp(g), w.strConst(c.arg))
			return b
		})
		l := w.line(0, "lib.P", g, typeArg(user))
		out := w.context(1).Run(w.module("main"), []Line{l})
		wantCodes(t, out, "E0901")
		if f, _ := out.Diags[0].FactValue("panic"); f.Str == "" || out.Diags[0].At != l.Span {
			t.Fatalf("%s(%q): %+v", c.api, c.arg, out.Diags[0])
		}
	}
}

func TestE0903_FailAtField(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true, "Name", types.Str)
	fieldSpan := w.views[w.tab.Get(user).Item].Fields[0].Span
	g := w.gen1("F", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fr, b := x.fieldAt(0, tr, 0)
		f := x.local(w.named(w.known.Field))
		x.assign(b, lp(f), use(mir.Copy(x.deref(fr))))
		_, b2 := x.api(b, "Gen:FailAt", cp(1), cp(f), w.strConst("bad field"))
		_, b3 := x.api(b2, "Gen:Fail", cp(1), w.strConst("bad line"))
		return x.emit(b3, "const K = 1")
	})
	l := w.line(0, "lib.F", g, typeArg(user))
	out := w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0903", "E0903")
	if out.Diags[0].At != fieldSpan || out.Diags[0].Message != "bad field" || out.Diags[1].At != l.Span {
		t.Fatalf("E0903s at %v %v", out.Diags[0].At, out.Diags[1].At)
	}
	if out.File != nil {
		t.Fatalf("a failed run's output was kept:\n%s", out.Text())
	}
}

// reads returns the line's reads as "kind key name" strings.
func reads(out *Output, i int) []string {
	var rs []string
	for _, r := range out.Lines[i].Reads.Sorted() {
		rs = append(rs, fmt.Sprintf("%s %s %s", r.Kind, r.Key, r.Name))
	}
	return rs
}

func hasRead(rs []string, want string) bool {
	for _, r := range rs {
		if r == want {
			return true
		}
	}
	return false
}

// TestAllFieldsViewHash: Fields() in User's module records "all", from
// another module "", and the two hash differently.
func TestAllFieldsViewHash(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("main", "User", true, "Name", types.Str, "age", types.Int)
	_, other := w.structType("app", "Acc", true, "Id", types.Int, "secret", types.Int)
	g := w.gen1("Count", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fs, b := x.api(0, "Type:Fields", cp(tr))
		n := x.local(types.Int)
		x.assign(b, lp(n), mir.Rvalue{Kind: mir.RvLen, Place: lp(fs)})
		return x.emit(b, "const N = $0", cp(n))
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Count", g, typeArg(user)), w.line(1, "lib.Count", g, typeArg(other))})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const N = 2\n") || !strings.Contains(out.Text(), "const N = 1\n") {
		t.Fatalf("field counts: own module sees private fields, others do not:\n%s", out.Text())
	}
	if !hasRead(reads(out, 0), "fields main.User all") || !hasRead(reads(out, 1), "fields app.Acc ") {
		t.Fatalf("reads %v / %v", reads(out, 0), reads(out, 1))
	}
	ua := w.Hash(w.tab.Get(user).Item, types.ReadFields, "all")
	ue := w.Hash(w.tab.Get(user).Item, types.ReadFields, "")
	w.version["main.User|fields|all"]++
	if ua == ue || w.Hash(w.tab.Get(user).Item, types.ReadFields, "all") == ua || w.Hash(w.tab.Get(user).Item, types.ReadFields, "") != ue {
		t.Fatal("a private-field edit must change the all view's hash only")
	}
	for _, r := range out.Lines[0].Reads.Sorted() {
		if r.Kind == types.ReadFields && r.Hash != ua {
			t.Fatal("recorded hash is not World.Hash's")
		}
	}
}

// TestKindQueryRecorded: identity queries record ReadSignature; an
// Opaque type's records ReadNoItem; reads survive a failing run.
func TestKindQueryRecorded(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("app", "User", true, "Id", types.Int)
	for _, q := range []string{"Type:Text", "Type:Exported", "Type:Name", "Type:Kind"} {
		g := w.gen1("Q"+strings.ReplaceAll(q, ":", ""), func(x *bb, tr, gr mir.LocalID) mir.BlockID {
			_, b := x.api(0, q, cp(tr))
			return b
		})
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Q", g, typeArg(user))})
		if !hasRead(reads(out, 0), "signature app.User ") {
			t.Errorf("%s: reads %v", q, reads(out, 0))
		}
	}
	_, s := w.structType("main", "S", false, "a", "GenA")
	g := w.gen1("O", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fr, b := x.fieldAt(0, tr, 0)
		ft, b2 := x.api(b, "Field:Type", cp(fr))
		ftr := x.ref(ft, false)
		x.flush(b2)
		txt, b3 := x.api(b2, "Type:Text", cp(ftr))
		return x.emit(b3, "const $0 = 1", cp(txt))
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.O", g, typeArg(s))})
	wantCodes(t, out)
	if !hasRead(reads(out, 0), "no-item main GenA") || !strings.Contains(out.Text(), "const GenA = 1") {
		t.Fatalf("opaque: reads %v\n%s", reads(out, 0), out.Text())
	}
}

// TestInstanceFieldsSubstituted: Pair[int]'s field a: T is int.
func TestInstanceFieldsSubstituted(t *testing.T) {
	w := newWorld(t)
	pid, _ := w.structType("app", "Pair", true, "A", w.tab.Param(0))
	w.views[pid].Generics = []types.GenericParam{{Name: w.in.Intern("T")}}
	pair := w.tab.Named(pid, []types.GenericArg{types.TypeArg(types.Int)})
	g := w.gen1("H", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fr, b := x.fieldAt(0, tr, 0)
		ft, b2 := x.api(b, "Field:Type", cp(fr))
		name, b3 := x.api(b2, "Field:Name", cp(fr))
		return x.emit(b3, "var $0: $1 = 0\nvar p: $2 = x", cp(name), cp(ft), cp(2))
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.H", g, typeArg(pair))})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "var A: int = 0") || !strings.Contains(out.Text(), "var p: app.Pair[int]") || strings.Contains(out.Text(), "T") {
		t.Fatalf("substitution:\n%s", out.Text())
	}
	// A bare Pair is not a type: E0301 at the argument, the line does not run.
	// The stage-4 translation gives it as KNamed with no arguments, alone or
	// inside another type.
	for _, bare := range []types.TypeID{w.tab.Named(pid, nil), w.tab.Slice(w.tab.Named(pid, nil)),
		w.tab.Named(pid, []types.GenericArg{types.TypeArg(w.tab.Optional(w.tab.Named(pid, nil)))})} {
		l := w.line(0, "lib.H", g, typeArg(bare))
		l.Args[0].Span = w.span()
		out := w.context(1).Run(w.module("main"), []Line{l})
		wantCodes(t, out, "E0301")
		if d := out.Diags[0]; d.At != l.Args[0].Span || !strings.Contains(d.Message, "app.Pair") || out.Lines[0].Ran || out.File != nil {
			t.Fatalf("bare %s: %v %q ran=%v", w.tab.TypeString(bare, nil), d.At, d.Message, out.Lines[0].Ran)
		}
	}
}

// TestOpaqueTypesNotSame and TestE0906_GeneratedFieldType.
func TestOpaqueTypesNotSame(t *testing.T) {
	w := newWorld(t)
	_, s := w.structType("main", "S", false, "a", "GenA", "b", "GenB", "c", "GenA", "d", types.Int)
	same := func(i, j int) string {
		g := w.gen1(fmt.Sprintf("Same%d%d", i, j), func(x *bb, tr, gr mir.LocalID) mir.BlockID {
			fi, b := x.fieldAt(0, tr, i)
			ti, b2 := x.api(b, "Field:Type", cp(fi))
			fj, b3 := x.fieldAt(b2, tr, j)
			tj, b4 := x.api(b3, "Field:Type", cp(fj))
			tir := x.ref(ti, false)
			x.flush(b4)
			r, b5 := x.api(b4, "Type:Same", cp(tir), cp(tj))
			return x.emit(b5, "const K = $0", cp(r))
		})
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Same", g, typeArg(s))})
		wantCodes(t, out)
		return out.Text()
	}
	if !strings.Contains(same(0, 1), "K = false") || !strings.Contains(same(0, 2), "K = true") || !strings.Contains(same(0, 3), "K = false") {
		t.Fatal("Same over Opaque types")
	}
	// Fields() of an Opaque type is a tentative E0906, kept by Finish only
	// when the name turns out to be generated by this module.
	g := w.gen1("Deep", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fr, b := x.fieldAt(0, tr, 0)
		ft, b2 := x.api(b, "Field:Type", cp(fr))
		ftr := x.ref(ft, false)
		x.flush(b2)
		_, b3 := x.api(b2, "Type:Fields", cp(ftr))
		return b3
	})
	c := w.context(1)
	out := c.Run(w.module("main"), []Line{w.line(0, "lib.Deep", g, typeArg(s))})
	wantCodes(t, out)
	genA := w.item("main", "GenA", types.KindStruct, &types.StructDecl{})
	if ds := c.Finish(out, func(source.Symbol) types.ItemID { return genA }); len(ds) != 0 {
		t.Fatalf("E0906 for a handwritten item: %v", ds)
	}
	out = c.Run(w.module("main"), []Line{w.line(0, "lib.Deep", g, typeArg(s))})
	w.items.Get(genA).Origin = &types.GenOrigin{}
	ds := c.Finish(out, func(n source.Symbol) types.ItemID {
		if w.in.Str(n) == "GenA" {
			return genA
		}
		return types.NoItem
	})
	if len(ds) != 1 || ds[0].Code != diag.E0906 {
		t.Fatalf("Finish: %v", ds)
	}
	if f, _ := ds[0].FactValue("input"); f.Str != "GenA" {
		t.Fatalf("facts %v", ds[0].Facts)
	}
}

// TestE0906_GenArgGenerated: a deferred argument holds the line back, and
// becomes E0906 at the argument when it names a generated item.
func TestE0906_GenArgGenerated(t *testing.T) {
	w := newWorld(t)
	g := w.gen1("G", func(x *bb, tr, gr mir.LocalID) mir.BlockID { return x.emit(0, "const K = 1") })
	a := Arg{Kind: ArgDeferred, Name: w.in.Intern("Later"), Span: w.span()}
	c := w.context(1)
	out := c.Run(w.module("main"), []Line{w.line(0, "lib.G", g, a)})
	wantCodes(t, out)
	if out.Lines[0].Ran || out.File != nil {
		t.Fatal("a deferred line ran")
	}
	later := w.item("main", "Later", types.KindStruct, &types.StructDecl{})
	w.items.Get(later).Origin = &types.GenOrigin{}
	ds := c.Finish(out, func(source.Symbol) types.ItemID { return later })
	if len(ds) != 1 || ds[0].Code != diag.E0906 || ds[0].At != a.Span {
		t.Fatalf("Finish: %v", ds)
	}
	out = c.Run(w.module("main"), []Line{w.line(1, "lib.G", g, a)})
	if ds := c.Finish(out, func(source.Symbol) types.ItemID { return types.NoItem }); len(ds) != 0 {
		t.Fatal("E0906 for a name that resolves nowhere (resolve's E0201)")
	}
}

// TestFreshNames and TestTwoGeneratorsNoCollision.
func TestFreshNames(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.gen1("F", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		a, b := x.api(0, "Gen:Fresh", cp(1), w.strConst("Tmp"))
		c, b2 := x.api(b, "Gen:Fresh", cp(1), w.strConst("tmp"))
		return x.emit(b2, "const $0 = 1\nconst $1 = 2", cp(a), cp(c))
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(3, "lib.F", g, typeArg(user)), w.line(4, "lib.F", g, typeArg(user))})
	wantCodes(t, out)
	for _, n := range []string{"TmpG3N1", "tmp_g3_2", "TmpG4N1", "tmp_g4_2"} {
		if !strings.Contains(out.Text(), "const "+n+" =") {
			t.Fatalf("missing %s:\n%s", n, out.Text())
		}
	}
}

// TestGenDeterminism: 8 lines, Workers and GOMAXPROCS 1 and 8: identical text, origins and
// diagnostics.
func TestGenDeterminism(t *testing.T) {
	// keyOrder lists the lines as resolve.Info.Gens does, by key text
	// ("#gen(10)" before "#gen(2)"); Run orders them by Index itself.
	run := func(workers int, keyOrder bool) (string, string, string) {
		defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(workers))
		w := newWorld(t)
		eq := w.deriveEq()
		bad := w.gen1("Bad", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
			_, b := x.api(0, "Gen:Fail", cp(1), w.strConst("no"))
			return b
		})
		var lines []Line
		for i := range 12 {
			_, ty := w.structType("main", fmt.Sprintf("T%d", i), false, "a", types.Int, "b", types.Str)
			g := eq
			if i%3 == 2 {
				g = bad
			}
			lines = append(lines, w.line(i, "derive.Eq", g, typeArg(ty)))
		}
		if keyOrder {
			slices.SortFunc(lines, func(a, b Line) int { return strings.Compare(strconv.Itoa(a.Index), strconv.Itoa(b.Index)) })
		}
		out := w.context(workers).Run(w.module("main"), lines)
		var ls []string
		for _, l := range out.Lines {
			ls = append(ls, fmt.Sprint(l.Ran, l.Steps))
		}
		return out.Text(), fmt.Sprint(out.Origins, out.Items, ls), fmt.Sprint(codes(out))
	}
	a1, b1, c1 := run(1, false)
	for i := range 5 {
		a8, b8, c8 := run(8, i == 4)
		if a1 != a8 || b1 != b8 || c1 != c8 {
			t.Fatalf("output depends on Workers, schedule or line order (key order %v)", i == 4)
		}
	}
	if strings.Count(a1, "impl Eq for") != 8 || strings.Index(a1, "for T1 ") > strings.Index(a1, "for T10 ") {
		t.Fatalf("output:\n%s", a1)
	}
}

// TestE0905_Wrap and TestE0704_GeneratedImplAtGenLine.
func TestE0905_Wrap(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("main", "User", false, "a", types.Int)
	eq := w.deriveEq()
	l := w.line(0, "derive.Eq", eq, typeArg(user))
	out := w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out)
	at := out.Items[0].Output
	at.Start += 5
	at.End = at.Start + 2
	inner := diag.New(diag.E0301, at, "inner").In(diag.PhaseTypes)
	d := Wrap(inner, out)
	if d.Code != diag.E0905 || d.At != l.Span || d.Phase != diag.PhaseTypes {
		t.Fatalf("Wrap: %+v", d)
	}
	f, _ := d.FactValue("inner")
	if len(f.List) != 3 {
		t.Fatalf("inner fact %+v", f)
	}
	if o, _ := d.FactValue("output"); o.At != at {
		t.Fatalf("output fact %+v", o)
	}
	if e := Wrap(diag.New(diag.E0704, at, "orphan"), out); e.Code != diag.E0704 || e.At != l.Span {
		t.Fatalf("E0704: %+v", e)
	}
	elsewhere := diag.New(diag.E0301, w.span(), "x")
	if Wrap(elsewhere, out) != elsewhere {
		t.Fatal("a handwritten diagnostic was wrapped")
	}
}

// TestAPI: the interface and enum queries of the generator API against
// the fake World (Kind, Receiver and Arena: TestKindReceiverArena).
func TestAPI(t *testing.T) {
	w := newWorld(t)
	iface := w.item("app", "Store", types.KindInterface, &types.InterfaceDecl{})
	w.items.Get(iface).Exported = true
	sig := func(name string, recv types.Receiver, params []types.Param, res types.TypeID) types.Signature {
		s := types.Signature{Name: w.in.Intern(name), Receiver: recv, Result: res}
		rt := w.tab.Ref(w.tab.SelfType())
		if recv == types.ByMut {
			rt = w.tab.Mut(w.tab.SelfType())
		}
		s.Params = append([]types.Param{{Name: w.in.Intern("self"), Type: rt}}, params...)
		return s
	}
	w.views[iface] = &DeclView{Item: iface, Kind: types.KindInterface, Exported: true, Methods: []MethodView{
		{Sig: sig("Get", types.ByShared, []types.Param{{Name: w.in.Intern("k"), Type: types.Int}}, types.Str)},
		{Sig: sig("Put", types.ByMut, []types.Param{{Name: w.in.Intern("v"), Type: w.tab.Slice(types.U8)}}, types.Void)},
	}}
	g := w.gen1("Fwd", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		ms, b := x.api(0, "Type:Methods", cp(tr))
		mt := w.named(w.known.Method)
		var sigs []mir.Operand
		blk := b
		for i := range 2 {
			idx, m := x.local(types.Int), x.local(mt)
			x.assign(blk, lp(idx), use(mir.Imm(types.Int, uint64(i))))
			x.assign(blk, lp(m), use(mir.Copy(x.b.Place(ms, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: mt}))))
			mr := x.ref(m, false)
			x.flush(blk)
			name, b2 := x.api(blk, "Method:Name", cp(mr))
			s, b3 := x.api(b2, "Method:Signature", cp(mr))
			fl, b4 := x.api(b3, "Method:HasFlows", cp(mr))
			sigs = append(sigs, cp(name), cp(s), cp(fl))
			blk = b4
		}
		return x.emit(blk, "impl $6 for X {\n$0$1 {\nflows($2)\n}\n$3$4 {\nflows($5)\n}\n}", append(sigs, cp(2))...)
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Fwd", g, Arg{Kind: ArgInterface, Item: iface})})
	wantCodes(t, out)
	want := "impl app.Store for X {\n    Get(&self, k: int): str {\n        flows(false)\n    }\n    Put(mut self, v: []u8) {\n        flows(false)\n    }\n}"
	if !strings.Contains(out.Text(), want) || !strings.Contains(out.Text(), "// gen lib.Fwd#1 for Store") {
		t.Fatalf("interface forwarding:\n%s\nwant\n%s", out.Text(), want)
	}
	if !hasRead(reads(out, 0), "signature app.Store ") {
		t.Fatalf("reads %v", reads(out, 0))
	}
	// Enum variants and their payload fields, in declaration order.
	eid := w.item("app", "Shape", types.KindEnum, &types.EnumDecl{})
	w.views[eid] = &DeclView{Item: eid, Kind: types.KindEnum, Variants: []VariantView{
		{Name: w.in.Intern("Circle"), Fields: []FieldView{{Name: w.in.Intern("r"), Type: types.F64, Exported: true}}},
		{Name: w.in.Intern("Dot")},
	}}
	ge := w.gen1("Vs", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		vs, b := x.api(0, "Type:Variants", cp(tr))
		vt := w.named(w.known.Variant)
		idx, v := x.local(types.Int), x.local(vt)
		x.assign(b, lp(idx), use(mir.Imm(types.Int, 0)))
		x.assign(b, lp(v), use(mir.Copy(x.b.Place(vs, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: vt}))))
		vr := x.ref(v, false)
		x.flush(b)
		name, b2 := x.api(b, "Variant:Name", cp(vr))
		fs, b3 := x.api(b2, "Variant:Fields", cp(vr))
		n := x.local(types.Int)
		x.assign(b3, lp(n), mir.Rvalue{Kind: mir.RvLen, Place: lp(fs)})
		return x.emit(b3, "const $0 = $1", cp(name), cp(n))
	})
	out = w.context(1).Run(w.module("main"), []Line{w.line(1, "lib.Vs", ge, typeArg(w.tab.Named(eid, nil)))})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const Circle = 1") || !hasRead(reads(out, 0), "fields app.Shape ") {
		t.Fatalf("variants:\n%s\n%v", out.Text(), reads(out, 0))
	}
}

// TestInterfaceArgThatIsAStruct: an interface argument naming a struct
// answers as a struct, so Methods() fails the run (E0901) instead of giving
// an empty list, and the identity read is recorded.
func TestInterfaceArgThatIsAStruct(t *testing.T) {
	w := newWorld(t)
	sid, _ := w.structType("app", "NotI", true)
	g := w.gen1("M", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		_, b := x.api(0, "Type:Methods", cp(tr))
		return b
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.M", g, Arg{Kind: ArgInterface, Item: sid})})
	wantCodes(t, out, "E0901")
	if !hasRead(reads(out, 0), "signature app.NotI ") {
		t.Fatalf("reads %v", reads(out, 0))
	}
}

// Aliases: cases the tests above cover, under the names their codes suggest.
func TestE0906_GeneratedFieldType(t *testing.T)      { TestOpaqueTypesNotSame(t) }
func TestE0906_DroppedWhenNotGenerated(t *testing.T) { TestOpaqueTypesNotSame(t) }
func TestE0704_GeneratedImplAtGenLine(t *testing.T)  { TestE0905_Wrap(t) }
func TestTwoGeneratorsNoCollision(t *testing.T)      { TestFreshNames(t) }
func TestFieldOrder(t *testing.T)                    { TestDeriveEqShape(t) }
func TestPrivateFieldsOwnModuleOnly(t *testing.T)    { TestAllFieldsViewHash(t) }
func TestE0905_GenInOutput(t *testing.T)             { TestE0905_ParseErrorInOutput(t) }

// arenaFn declares std.core's MakeEvalArena(): &Arena as a static reference
// to a const Arena, and makes it the evaluator's Config.EvalArena.
func (w *world) arenaFn() {
	at := w.named(w.known.Arena)
	b := make([]byte, 4)
	types.StoreUint(b, 4, 7)
	c := w.item("std.core", "TheArena", types.KindConst, &types.ConstDecl{Type: at, HasValue: true,
		Value: w.consts.Intern(types.ConstValue{Type: at, Align: 4, Bytes: b})})
	mk := w.fn("std.core", "MakeEvalArena", w.tab.Ref(at))
	x := w.body(mk)
	x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvGlobalRef, Item: c})
	x.ret(0)
	x.done()
	w.evalArena = mk
}

// TestKindReceiverArena: Type.Kind's and Method.Receiver's enum
// results by their discriminants (std/core/core_gen.mk's declaration
// order), and Gen.Arena giving the run's one arena.
func TestKindReceiverArena(t *testing.T) {
	w := newWorld(t)
	_, st := w.structType("app", "S", true)
	eid := w.item("app", "E", types.KindEnum, &types.EnumDecl{})
	w.views[eid] = &DeclView{Item: eid, Kind: types.KindEnum}
	iface := w.item("app", "I", types.KindInterface, &types.InterfaceDecl{})
	w.views[iface] = &DeclView{Item: iface, Kind: types.KindInterface}
	kind := w.gen1("K", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		k, b := x.api(0, "Type:Kind", cp(tr))
		d := x.local(types.U64)
		x.assign(b, lp(d), mir.Rvalue{Kind: mir.RvDiscriminant, Place: lp(k)})
		return x.emit(b, "const K = $0", cp(d))
	})
	cases := []struct {
		arg  Arg
		want uint64
	}{
		{typeArg(st), tkStruct},
		{typeArg(w.tab.Named(eid, nil)), tkEnum},
		{Arg{Kind: ArgInterface, Item: iface}, tkInterface},
		{typeArg(types.Int), tkScalar},
		{typeArg(w.tab.Ref(types.Int)), tkRef},
		{typeArg(w.tab.Slice(types.U8)), tkSlice},
		{typeArg(w.tab.Optional(types.Int)), tkOptional},
	}
	var lines []Line
	for i, c := range cases {
		lines = append(lines, w.line(i, "lib.K", kind, c.arg))
	}
	out := w.context(1).Run(w.module("main"), lines)
	wantCodes(t, out)
	var got []string
	for _, l := range strings.Split(out.Text(), "\n") {
		if strings.HasPrefix(l, "const K = ") {
			got = append(got, strings.TrimPrefix(l, "const K = "))
		}
	}
	for i, c := range cases {
		if i >= len(got) || got[i] != fmt.Sprint(c.want) {
			t.Fatalf("Kind discriminants %v, case %d wants %d:\n%s", got, i, c.want, out.Text())
		}
	}
	// Receiver: Static Value Ref Mut.
	sig := func(name string, recv types.Receiver) MethodView {
		s := types.Signature{Name: w.in.Intern(name), Receiver: recv, Result: types.Void}
		switch recv {
		case types.ByValue:
			s.Params = []types.Param{{Name: w.in.Intern("self"), Type: w.tab.SelfType()}}
		case types.ByShared:
			s.Params = []types.Param{{Name: w.in.Intern("self"), Type: w.tab.Ref(w.tab.SelfType())}}
		case types.ByMut:
			s.Params = []types.Param{{Name: w.in.Intern("self"), Type: w.tab.Mut(w.tab.SelfType())}}
		}
		return MethodView{Sig: s}
	}
	w.views[iface].Methods = []MethodView{sig("New", types.NoReceiver), sig("Take", types.ByValue),
		sig("Get", types.ByShared), sig("Put", types.ByMut)}
	recv := w.gen1("R", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		ms, blk := x.api(0, "Type:Methods", cp(tr))
		mt := w.named(w.known.Method)
		var ds []mir.Operand
		for i := range 4 {
			idx, m := x.local(types.Int), x.local(mt)
			x.assign(blk, lp(idx), use(mir.Imm(types.Int, uint64(i))))
			x.assign(blk, lp(m), use(mir.Copy(x.b.Place(ms, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: mt}))))
			mr := x.ref(m, false)
			x.flush(blk)
			r, b := x.api(blk, "Method:Receiver", cp(mr))
			d := x.local(types.U64)
			x.assign(b, lp(d), mir.Rvalue{Kind: mir.RvDiscriminant, Place: lp(r)})
			ds = append(ds, cp(d))
			blk = b
		}
		return x.emit(blk, "const R = [$0, $1, $2, $3]", ds...)
	})
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.R", recv, Arg{Kind: ArgInterface, Item: iface})})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const R = [0, 1, 2, 3]") {
		t.Fatalf("Receiver discriminants:\n%s", out.Text())
	}
	// Arena: two calls give one arena, the one EvalArena() gives.
	w.arenaFn()
	aref := w.tab.Ref(w.named(w.known.Arena))
	arena := w.gen1("A", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		a1, b := x.api(0, "Gen:Arena", cp(gr))
		a2, b2 := x.api(b, "Gen:Arena", cp(gr))
		a3, b3 := x.local(aref), x.block()
		x.term(b2, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinEvalArena},
			Dest: lp(a3), Target: b3, Spawn: mir.NoLocal})
		e1, e2 := x.local(types.Bool), x.local(types.Bool)
		x.assign(b3, lp(e1), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Eq, A: cp(a1), B: cp(a2)})
		x.assign(b3, lp(e2), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Eq, A: cp(a1), B: cp(a3)})
		return x.emit(b3, "const A = [$0, $1]", cp(e1), cp(e2))
	})
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.A", arena, typeArg(st))})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const A = [true, true]") {
		t.Fatalf("Gen.Arena:\n%s", out.Text())
	}
}

// TestE0908_SizeOfSameModuleInGenerator: a generator of lib that reads the
// layout of a type of the line's own module (SizeOf of main.Local, here
// written into lib's MIR by hand: Mk source cannot name it) stops with E0908
// at the gen line, items ending in the type; SizeOf of an imported type is
// allowed.
func TestE0908_SizeOfSameModuleInGenerator(t *testing.T) {
	w := newWorld(t)
	_, local := w.structType("main", "Local", false, "a", types.Int)
	_, shared := w.structType("lib", "Shared", true, "a", types.Int, "b", types.Int)
	var l Line
	size := func(name string, of types.TypeID) *Output {
		g := w.gen1(name, func(x *bb, tr, gr mir.LocalID) mir.BlockID {
			n := x.local(types.Int)
			x.assign(0, lp(n), mir.Rvalue{Kind: mir.RvSizeOf, Type: of})
			return x.emit(0, "const N = $0", cp(n))
		})
		l = w.line(0, "lib."+name, g, typeArg(local))
		return w.context(1).Run(w.module("main"), []Line{l})
	}
	out := size("Local", local)
	wantCodes(t, out, "E0908")
	if out.Diags[0].At != l.Span {
		t.Fatalf("E0908 at %v", out.Diags[0].At)
	}
	items, _ := out.Diags[0].FactValue("items")
	if n := len(items.List); n == 0 || items.List[n-1].Str != "main.Local" {
		t.Fatalf("items %+v", items)
	}
	if out.File != nil {
		t.Fatalf("a stopped run's output was kept:\n%s", out.Text())
	}
	out = size("Imported", shared)
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const N = 16") {
		t.Fatalf("SizeOf of an imported type:\n%s", out.Text())
	}
}

// TestLineResult_Poisoned: a line whose fuel const or generator body is
// poison emits nothing, gives no diagnostic of its own (the cause has one)
// and says so in LineResult.Poisoned, which the driver reads to keep the
// module out of the cache (gen calls the Evaluator itself, not the
// driver's Eval shim). A line that runs clean is not Poisoned.
func TestLineResult_Poisoned(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	g := w.gen1("G", func(x *bb, tr, gr mir.LocalID) mir.BlockID { return 0 })

	clean := w.line(0, "lib.G", g, typeArg(user))
	out := w.context(1).Run(w.module("main"), []Line{clean})
	wantCodes(t, out)
	if r := out.Lines[0]; !r.Ran || r.Poisoned {
		t.Fatalf("clean line: Ran %v, Poisoned %v", r.Ran, r.Poisoned)
	}

	// A lower module's const with no value is poison (its module failed).
	k := w.item("lib", "Bad", types.KindConst, &types.ConstDecl{Type: types.U64})
	fl := w.line(1, "lib.G", g, typeArg(user))
	fl.FuelItem, fl.FuelSpan = k, w.span()
	out = w.context(1).Run(w.module("main"), []Line{fl})
	wantCodes(t, out)
	if r := out.Lines[0]; r.Ran || !r.Poisoned || out.AST != nil {
		t.Fatalf("poisoned fuel: Ran %v, Poisoned %v, AST %v", r.Ran, r.Poisoned, out.AST != nil)
	}

	// The generator's body is poison (its module failed after it was
	// lowered): the run returns poison.
	w.bodies[g].State = eval.BodyPoisoned
	pl := w.line(2, "lib.G", g, typeArg(user))
	out = w.context(1).Run(w.module("main"), []Line{pl})
	wantCodes(t, out)
	if r := out.Lines[0]; !r.Ran || !r.Poisoned || out.AST != nil {
		t.Fatalf("poisoned body: Ran %v, Poisoned %v, AST %v", r.Ran, r.Poisoned, out.AST != nil)
	}
}
