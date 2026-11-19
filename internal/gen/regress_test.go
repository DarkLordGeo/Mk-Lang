// Why: gen's reflection, splicing and budgets had bugs on generic
// interfaces, error types, fn types and large outputs.
// What: regression tests of queries over generic interfaces and error types,
// the spelling of spliced types, the fuel bound on host token lists, the
// per-run budget and one trace per item.
// How: hand-built worlds (errorType, doubler, blockAdder) run through gen,
// checked for codes, origins and allocation counts.

package gen

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/types"
)

// errorType declares error mod.name(fields...) and its view, and returns
// it the way the stage-4 view types an error item: the set of that error.
func (w *world) errorType(mod, name string, exported bool, fields ...any) (types.ItemID, types.TypeID) {
	d := &types.ErrorDecl{}
	v := &DeclView{Kind: types.KindError, Exported: exported}
	for i := 0; i+1 < len(fields); i += 2 {
		fv := FieldView{Name: w.in.Intern(fields[i].(string)), Type: fields[i+1].(types.TypeID), Exported: true, Span: w.span()}
		d.Fields = append(d.Fields, types.FieldDecl{Name: fv.Name, Type: fv.Type})
		v.Fields = append(v.Fields, fv)
	}
	id := w.item(mod, name, types.KindError, d)
	w.items.Get(id).Exported = exported
	v.Item = id
	w.views[id] = v
	return id, w.tab.ErrorSetType(w.tab.ErrorSet([]types.ErrorMember{{Item: id}}))
}

// originsAt are the origin kinds of the TokenOrigins at the identifiers
// spelled word in out's text.
func originsAt(out *Output, word string) []OriginKind {
	var ks []OriginKind
	for _, o := range out.Origins {
		end := o.Offset
		for end < uint32(len(out.File.Text)) && isIdentByte(out.File.Text[end]) {
			end++
		}
		if string(out.File.Text[o.Offset:end]) == word {
			ks = append(ks, o.Kind)
		}
	}
	return ks
}

// TestGenericInterfaceArg: `gen lib.Fwd(Slice)` over
// `interface Slice[T]` is E0301 at the argument, as a bare `Pair` is, and the
// generator never runs, so Items(&self): []T never hands T to the host.
func TestGenericInterfaceArg(t *testing.T) {
	w := newWorld(t)
	slice := w.item("app", "Slice", types.KindInterface, &types.InterfaceDecl{})
	w.items.Get(slice).Exported = true
	tp := []types.GenericParam{{Name: w.in.Intern("T")}}
	w.views[slice] = &DeclView{Item: slice, Kind: types.KindInterface, Exported: true, Generics: tp, Methods: []MethodView{{Sig: types.Signature{
		Name: w.in.Intern("Items"), Receiver: types.ByShared, Generics: tp, OwnGenerics: 0,
		Params: []types.Param{{Name: w.in.Intern("self"), Type: w.tab.Ref(w.tab.SelfType())}},
		Result: w.tab.Slice(w.tab.Param(0)), Errors: types.EmptyErrors}}}}
	g := w.gen1("Fwd", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		ms, b := x.api(0, "Type:Methods", cp(tr))
		mt := w.named(w.known.Method)
		idx, m := x.local(types.Int), x.local(mt)
		x.assign(b, lp(idx), use(mir.Imm(types.Int, 0)))
		x.assign(b, lp(m), use(mir.Copy(x.b.Place(ms, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: mt}))))
		mr := x.ref(m, false)
		x.flush(b)
		s, b2 := x.api(b, "Method:Signature", cp(mr))
		return x.emit(b2, "S.F$0 {\n}", cp(s))
	})
	l := w.line(0, "lib.Fwd", g, Arg{Kind: ArgInterface, Item: slice, Span: w.span()})
	out := w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0301")
	if d := out.Diags[0]; d.At != l.Args[0].Span || !strings.Contains(d.Message, "app.Slice") || out.Lines[0].Ran || out.File != nil {
		t.Fatalf("generic interface: at %v (arg %v) %q ran=%v", d.At, l.Args[0].Span, d.Message, out.Lines[0].Ran)
	}
	// The same interface without type parameters binds and runs.
	w.views[slice].Generics = nil
	w.views[slice].Methods[0].Sig.Generics = nil
	w.views[slice].Methods[0].Sig.Result = w.tab.Slice(types.Int)
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Fwd", g, Arg{Kind: ArgInterface, Item: slice, Span: w.span()})})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "S.F(&self): []int {") {
		t.Fatalf("non-generic interface:\n%s", out.Text())
	}
}

// TestErrorFields: an error item, which the stage-4 view
// types as the set of that one error, reflects as its declaration: Fields in
// declaration order, the signature and fields reads, Exported from the item,
// FailOn at the error's declaration.
func TestErrorFields(t *testing.T) {
	w := newWorld(t)
	os, osT := w.errorType("app", "Os", false, "code", types.I64)
	g := w.gen1("Count", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		fs, b := x.api(0, "Type:Fields", cp(tr))
		n := x.local(types.Int)
		x.assign(b, lp(n), mir.Rvalue{Kind: mir.RvLen, Place: lp(fs)})
		exp, b2 := x.api(b, "Type:Exported", cp(tr))
		fr, b3 := x.fieldAt(b2, tr, 0)
		name, b4 := x.api(b3, "Field:Name", cp(fr))
		return x.emit(b4, "const N = $0\nconst E = $1\nconst $2 = 0\nvar e: $3 = x", cp(n), cp(exp), cp(name), cp(2))
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Count", g, typeArg(osT))})
	wantCodes(t, out)
	for _, want := range []string{"const N = 1\n", "const E = false\n", "const code = 0\n", "var e: app.Os = x", "// gen lib.Count#1 for Os"} {
		if !strings.Contains(out.Text(), want) {
			t.Fatalf("error reflection: no %q in\n%s", want, out.Text())
		}
	}
	rs := reads(out, 0)
	if !hasRead(rs, "signature app.Os ") || !hasRead(rs, "fields app.Os ") {
		t.Fatalf("reads %v", rs)
	}
	w.items.Get(os).Exported = true
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Count", g, typeArg(osT))})
	if wantCodes(t, out); !strings.Contains(out.Text(), "const E = true\n") {
		t.Fatalf("Exported does not follow the error's flag:\n%s", out.Text())
	}
	// FailOn(t, ..) reports at Os's declaration, not at the gen line.
	gf, x, tr, _ := w.genHead("lib", "Refuse")
	ty := x.local(w.typeT())
	x.assign(0, lp(ty), use(mir.Copy(x.deref(tr))))
	_, fb := x.api(0, "Gen:FailOn", cp(1), cp(ty), w.strConst("no errors here"))
	x.ret(fb)
	x.done()
	l := w.line(0, "lib.Refuse", gf, typeArg(osT))
	out = w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0903")
	if out.Diags[0].At != w.items.Get(os).Span {
		t.Fatalf("FailOn at %v, want Os's declaration %v (line %v)", out.Diags[0].At, w.items.Get(os).Span, l.Span)
	}
	// A set of several errors names no one declaration: Fields is E0901.
	_, badT := w.errorType("app", "Bad", true)
	two := w.tab.ErrorSetType(w.tab.Union(w.tab.Get(osT).Errors, w.tab.Get(badT).Errors))
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Count", g, typeArg(two))})
	wantCodes(t, out, "E0901")
}

// spliceFields runs a generator printing `F(<name>: <field type>, ...) {}`
// over struct main.S's fields, each type through Type.Name.
func spliceFields(t *testing.T, w *world, fields ...any) *Output {
	t.Helper()
	_, s := w.structType("main", "S", false, fields...)
	n := len(fields) / 2
	g := w.gen1("F", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		var ops []mir.Operand
		var tmpl []string
		blk := mir.BlockID(0)
		for i := range n {
			fr, b := x.fieldAt(blk, tr, i)
			ft, b2 := x.api(b, "Field:Type", cp(fr))
			ftr := x.ref(ft, false)
			x.flush(b2)
			name, b3 := x.api(b2, "Type:Name", cp(ftr))
			ops = append(ops, cp(name))
			tmpl = append(tmpl, fields[2*i].(string)+": $"+itoa(i))
			blk = b3
		}
		return x.emit(blk, "F("+strings.Join(tmpl, ", ")+") {\n}", ops...)
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.F", g, typeArg(s))})
	wantCodes(t, out)
	return out
}

// paramTypes are the parameter type nodes of the output's first function.
func paramTypes(t *testing.T, out *Output) map[string]ast.Type {
	t.Helper()
	if len(out.AST.Items) != 1 {
		t.Fatalf("%d items:\n%s", len(out.AST.Items), out.Text())
	}
	fd, ok := out.AST.Items[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("item %T:\n%s", out.AST.Items[0], out.Text())
	}
	m := map[string]ast.Type{}
	for _, p := range fd.Sig.Params {
		m[string(out.File.Text[p.Name.Span().Start:p.Name.Span().End])] = p.Type
	}
	return m
}

// TestFnTypeSplicePinned: a fn type's named types are pinned
// and qualified like any spliced type, and a fn type with flows names its
// parameters so `into` and `from` parse.
func TestFnTypeSplicePinned(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("lib", "User", true)
	fnT := func(ft types.FnType) types.TypeID {
		if ft.Errors == 0 {
			ft.Errors = types.EmptyErrors
		}
		if ft.Result == types.NoType {
			ft.Result = types.Void
		}
		return w.tab.Fn(ft)
	}
	_, badT := w.errorType("lib", "Bad", true)
	pred := fnT(types.FnType{Params: []types.FnParam{{Type: user}}, Result: types.Bool})
	into := fnT(types.FnType{Params: []types.FnParam{{Type: w.tab.Mut(user)}, {Type: w.tab.Ref(types.Int), Into: 1}}})
	from := fnT(types.FnType{Params: []types.FnParam{{Type: w.tab.Ref(user)}}, Result: w.tab.Ref(types.Int),
		Sources: []types.Source{{Param: 1}}})
	fails := fnT(types.FnType{Params: nil, Result: user, Errors: w.tab.Get(badT).Errors})
	out := spliceFields(t, w, "a", pred, "b", into, "c", from, "d", fails)
	for _, want := range []string{"a: fn(lib.User): bool", "b: (fn(p1: mut lib.User, p2: &int into p1))",
		"c: (fn(p1: &lib.User): &int from p1)", "d: fn(): lib.Bad!lib.User"} {
		if !strings.Contains(out.Text(), want) {
			t.Fatalf("fn type splice: no %q in\n%s", want, out.Text())
		}
	}
	if ks := originsAt(out, "User"); len(ks) != 4 || ks[0] != OriginPinnedItem {
		t.Fatalf("origins of User: %v\n%s", ks, out.Text())
	}
	for _, k := range originsAt(out, "lib") {
		if k != OriginPinnedModule {
			t.Fatalf("origin of lib: %v", k)
		}
	}
	ps := paramTypes(t, out)
	if _, ok := ps["a"].(*ast.FnType); !ok {
		t.Fatalf("a: %T", ps["a"])
	}
	for _, n := range []string{"b", "c"} {
		pt, ok := ps[n].(*ast.ParenType)
		if !ok {
			t.Fatalf("%s: %T", n, ps[n])
		}
		ft, ok := pt.Type.(*ast.FnType)
		if !ok || (n == "b" && (len(ft.Params) != 2 || ft.Params[1].Into == nil)) || (n == "c" && len(ft.Flows) != 1) {
			t.Fatalf("%s: %T %+v", n, pt.Type, ft)
		}
	}
}

// TestSpliceParenthesizes: a spliced type keeps the
// parentheses TypeString writes, so it parses back to the same type: an
// error union or a union of errors under a prefix, and mut ([]T).
func TestSpliceParenthesizes(t *testing.T) {
	w := newWorld(t)
	_, badT := w.errorType("lib", "Bad", true)
	_, worseT := w.errorType("lib", "Worse", true)
	eu := w.tab.ErrorUnion(w.tab.Get(badT).Errors, types.Int)
	set2 := w.tab.ErrorSetType(w.tab.Union(w.tab.Get(badT).Errors, w.tab.Get(worseT).Errors))
	out := spliceFields(t, w,
		"b", w.tab.Optional(eu), "c", w.tab.Mut(w.tab.Slice(types.U8)), "d", w.tab.Ref(eu),
		"e", w.tab.MutSlice(types.U8), "f", w.tab.Slice(eu), "g", w.tab.Optional(set2), "h", eu,
		"i", w.tab.Slice(w.tab.Slice(types.U8)), "j", w.tab.Slice(w.tab.Ref(types.Int)))
	// i and j: an element after a []-prefix prints tight, as in handwritten
	// code ([][]u8, []&int), not as an index or a binary &.
	for _, want := range []string{"b: ?(lib.Bad!int)", "c: mut ([]u8)", "d: &(lib.Bad!int)", "e: mut []u8",
		"f: [](lib.Bad!int)", "g: ?(lib.Bad|lib.Worse)", "h: lib.Bad!int", "i: [][]u8", "j: []&int"} {
		if !strings.Contains(out.Text(), want) {
			t.Fatalf("parenthesized splice: no %q in\n%s", want, out.Text())
		}
	}
	ps := paramTypes(t, out)
	paren := func(n string, outer ast.Type) ast.Type {
		var elem ast.Type
		switch x := outer.(type) {
		case *ast.OptionalType:
			elem = x.Elem
		case *ast.MutType:
			elem = x.Elem
		case *ast.RefType:
			elem = x.Elem
		case *ast.SliceType:
			elem = x.Elem
		default:
			t.Fatalf("%s: outer %T", n, outer)
		}
		p, ok := elem.(*ast.ParenType)
		if !ok {
			t.Fatalf("%s: operand %T, want a parenthesized type", n, elem)
		}
		return p.Type
	}
	for _, n := range []string{"b", "d", "f"} {
		if _, ok := paren(n, ps[n]).(*ast.ErrorType); !ok {
			t.Fatalf("%s: not an error union inside the parentheses", n)
		}
	}
	if _, ok := paren("c", ps["c"]).(*ast.SliceType); !ok {
		t.Fatal("c: mut ([]u8) is not a mut of a slice")
	}
	if _, ok := paren("g", ps["g"]).(*ast.UnionType); !ok {
		t.Fatal("g: not a union inside the parentheses")
	}
	if _, ok := ps["e"].(*ast.MutSliceType); !ok {
		t.Fatalf("e: %T, want mut []u8", ps["e"])
	}
}

// doubler is a generator building c = quote { x } and then k times
// c = c.Then(c), emitting nothing.
func (w *world) doubler(name string, k int) types.ItemID {
	return w.gen1(name, func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		c, b := x.quote(0, "x")
		for range k {
			b = x.then(b, c, c)
		}
		return b
	})
}

// TestTokBytes: tokBytes is at least what a tok takes, so chargeToks
// charges every byte the host keeps.
func TestTokBytes(t *testing.T) {
	if n := reflect.TypeOf(tok{}).Size(); n > tokBytes {
		t.Fatalf("a tok takes %d bytes, tokBytes says %d", n, tokBytes)
	}
}

// TestHostTokensCharged: the tokens Code.Then copies are
// charged, c(n·tokBytes) steps, so a doubling Code runs out of fuel (E0902)
// instead of out of memory.
func TestHostTokensCharged(t *testing.T) {
	w := newWorld(t)
	const k = 10
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.D", w.doubler("D", k), typeArg(types.Int))})
	wantCodes(t, out)
	if least := uint64(1<<k) * tokBytes / 16; out.Lines[0].Steps < least {
		t.Fatalf("%d doublings: %d steps, want at least %d (the last copy alone)", k, out.Lines[0].Steps, least)
	}
	// 64 doublings would be 2^64 tokens: fuel stops the run first.
	l := w.line(0, "lib.D64", w.doubler("D64", 64), typeArg(types.Int))
	l.FuelLit, l.FuelSpan = &ast.BasicLit{Kind: ast.TokInt, Raw: "1000000"}, w.span()
	out = w.context(1).Run(w.module("main"), []Line{l})
	wantCodes(t, out, "E0902")
	// And under the default fuel too.
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.D64", l.Generator, typeArg(types.Int))})
	wantCodes(t, out, "E0902")
}

// blockAdder is a generator calling b.Add(quote { x() }) n times.
func (w *world) blockAdder(name string, n int) types.ItemID {
	return w.gen1(name, func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		blk, b := x.api(0, "Gen:Block", cp(gr))
		code, b2 := x.quote(b, "x()")
		i, c := x.local(types.Int), x.local(types.Bool)
		x.assign(b2, lp(i), use(mir.Imm(types.Int, 0)))
		head, body, exit := x.block(), x.block(), x.block()
		x.gotoB(b2, head)
		x.assign(head, lp(c), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(i), B: mir.Imm(types.Int, uint64(n))})
		x.branch(head, cp(c), body, exit)
		next := x.add(body, blk, code)
		x.assign(next, lp(i), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(i), B: mir.Imm(types.Int, 1)})
		x.gotoB(next, head)
		return exit
	})
}

// addRun runs blockAdder(n) and returns the bytes the run allocated.
func addRun(t *testing.T, n int) (uint64, uint64) {
	t.Helper()
	w := newWorld(t)
	g := w.blockAdder("Add", n)
	c := w.context(1)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	out := c.Run(w.module("main"), []Line{w.line(0, "lib.Add", g, typeArg(types.Int))})
	runtime.ReadMemStats(&after)
	wantCodes(t, out)
	return after.TotalAlloc - before.TotalAlloc, out.Lines[0].Steps
}

// TestBlockAddLinear: n Block.Add calls allocate in
// proportion to n, not n²: four times the calls allocate well under eight
// times the bytes (a copy per Add gives sixteen).
func TestBlockAddLinear(t *testing.T) {
	small, ss := addRun(t, 5000)
	large, ls := addRun(t, 20000)
	t.Logf("n=5000: %d bytes, %d steps; n=20000: %d bytes, %d steps", small, ss, large, ls)
	if large > 8*small {
		t.Fatalf("Block.Add is not linear: 5000 Adds allocate %d bytes, 20000 allocate %d", small, large)
	}
}

// BenchmarkGenBlockAdd: one run of 40,000 Block.Add calls.
func BenchmarkGenBlockAdd(b *testing.B) {
	w := newWorld(b)
	g := w.blockAdder("Add", 40000)
	for b.Loop() {
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.Add", g, typeArg(types.Int))})
		if len(out.Diags) > 0 {
			b.Fatal(out.Diags[0].Message)
		}
	}
}

// TestContinuedSignatureOneItem: a function whose requires
// or from clause, and then its '{', sit on lines of their own is one item:
// one trace comment, one ItemOrigin, matched by OriginOf.
func TestContinuedSignatureOneItem(t *testing.T) {
	for _, c := range []struct {
		name, text, want string
		items            int
	}{
		{"requires", "Pos(x: int): int\nrequires x > 0\n{\nreturn x\n}", "Pos(x: int): int\n    requires x > 0\n{\n    return x\n}", 1},
		{"from", "Get(p: &int, q: &int): &int\nfrom p\n{\nreturn p\n}", "Get(p: &int, q: &int): &int\n    from p\n{", 1},
		{"store", "Put(p: mut []&int, q: &int)\n*q into p\n{\n}", "Put(p: mut []&int, q: &int)\n    *q into p\n{", 1},
		{"two", "const A = 1\nconst B = 2\nF() {\n}", "const B = 2", 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			g := w.gen1("C", func(x *bb, tr, gr mir.LocalID) mir.BlockID { return x.emit(0, c.text) })
			out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.C", g, typeArg(types.Int))})
			wantCodes(t, out)
			if n := strings.Count(out.Text(), "// gen lib.C#1"); n != c.items || len(out.Items) != c.items || len(out.AST.Items) != c.items {
				t.Fatalf("%d traces, %d item origins, %d parsed items:\n%s", n, len(out.Items), len(out.AST.Items), out.Text())
			}
			if !strings.Contains(out.Text(), c.want) {
				t.Fatalf("no %q in\n%s", c.want, out.Text())
			}
			if _, ok := out.OriginOf(out.AST.Items[0]); !ok {
				t.Fatalf("OriginOf(%T) not found: %+v", out.AST.Items[0], out.Items)
			}
		})
	}
}
