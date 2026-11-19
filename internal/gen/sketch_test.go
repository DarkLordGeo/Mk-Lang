// Why: the generator API must be enough to write std's library generators,
// and their output must parse.
// What: deriveHash, deriveFormat and dynOf, hand-built MIR run over the fake
// World by TestSketchesHandBuilt (mkderive_test.go has the Mk versions).
// How: each generator walks the type's fields or methods and prints Code,
// compared with goldens. dyn.Of names its types with Gen.Ident of a str
// constant, since these std-free tests lack std's formatting.

package gen

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// loopv is the state of one lowered for loop over a slice.
type loopv struct {
	i, n       mir.LocalID
	head, exit mir.BlockID
}

// each lowers `for e in xs` from blk: in the returned body block e is a
// copy of element i and er is &e; next ends an iteration; the loop leaves
// through l.exit.
func (x *bb) each(blk mir.BlockID, xs mir.LocalID, et types.TypeID) (l loopv, er mir.LocalID, body mir.BlockID) {
	l = loopv{i: x.local(types.Int), n: x.local(types.Int)}
	c := x.local(types.Bool)
	x.assign(blk, lp(l.n), mir.Rvalue{Kind: mir.RvLen, Place: lp(xs)})
	x.assign(blk, lp(l.i), use(mir.Imm(types.Int, 0)))
	l.head, body, l.exit = x.block(), x.block(), x.block()
	x.gotoB(blk, l.head)
	x.assign(l.head, lp(c), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(l.i), B: cp(l.n)})
	x.branch(l.head, cp(c), body, l.exit)
	e := x.local(et)
	x.assign(body, lp(e), use(mir.Copy(x.b.Place(xs, mir.Projection{Kind: mir.ProjIndex, A: uint32(l.i), Type: et}))))
	er = x.ref(e, false)
	x.flush(body)
	return l, er, body
}

func (x *bb) next(blk mir.BlockID, l loopv) {
	x.assign(blk, lp(l.i), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(l.i), B: mir.Imm(types.Int, 1)})
	x.gotoB(blk, l.head)
}

// add is block.Add(c).
func (x *bb) add(blk mir.BlockID, block, c mir.LocalID) mir.BlockID {
	br := x.ref(block, true)
	x.flush(blk)
	_, next := x.api(blk, "Block:Add", cp(br), cp(c))
	return next
}

// then is c = c.Then(d).
func (x *bb) then(blk mir.BlockID, c, d mir.LocalID) mir.BlockID {
	r, next := x.api(blk, "Code:Then", cp(c), cp(d))
	x.assign(next, lp(c), use(cp(r)))
	return next
}

// genHead starts a generator (g: mut Gen, t: Type) of module mod with &t and
// &*g in locals.
func (w *world) genHead(mod, name string) (types.ItemID, *bb, mir.LocalID, mir.LocalID) {
	g := w.generator(mod, name, w.typeT())
	x := w.body(g)
	tr := x.ref(2, false)
	gr := x.refPlace(x.deref(1), w.named(w.known.Gen), false)
	x.flush(0)
	return g, x, tr, gr
}

// deriveHash is derive.Hash: one self.<f>.Hash(h) per field.
func (w *world) deriveHash() types.ItemID {
	g, x, tr, gr := w.genHead("derive", "Hash")
	body, b := x.api(0, "Gen:Block", cp(gr))
	fs, b := x.api(b, "Type:Fields", cp(tr))
	l, fr, lb := x.each(b, fs, w.named(w.known.Field))
	name, lb := x.api(lb, "Field:Name", cp(fr))
	code, lb := x.quote(lb, "\nself.$0.Hash(h)\n", cp(name))
	x.next(x.add(lb, body, code), l)
	tn, e := x.api(l.exit, "Type:Name", cp(tr))
	q, e := x.quote(e, "\nimpl Hash for $0 {\nHash(&self, h: mut Hasher) {\n$1\n}\n}\n", cp(tn), cp(body))
	_, e = x.api(e, "Gen:Emit", cp(1), cp(q))
	x.ret(e)
	x.done()
	return g
}

// deriveFormat is derive.Format: Name(a: .., b: ..), a separator
// before every field but the first.
func (w *world) deriveFormat() types.ItemID {
	g, x, tr, gr := w.genHead("derive", "Format")
	body, b := x.api(0, "Gen:Block", cp(gr))
	tt, b := x.api(b, "Type:Text", cp(tr))
	ts, b := x.api(b, "Gen:Str", cp(gr), cp(tt))
	open, b := x.quote(b, "\ntry w.Str($0)\ntry w.Str(\"(\")\n", cp(ts))
	b = x.add(b, body, open)
	fs, b := x.api(b, "Type:Fields", cp(tr))
	l, fr, lb := x.each(b, fs, w.named(w.known.Field))
	gt := x.local(types.Bool)
	x.assign(lb, lp(gt), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Gt, A: cp(l.i), B: mir.Imm(types.Int, 0)})
	sep, join := x.block(), x.block()
	x.branch(lb, cp(gt), sep, join)
	comma, sb := x.quote(sep, "\ntry w.Str(\", \")\n")
	x.gotoB(x.add(sb, body, comma), join)
	fname, jb := x.api(join, "Field:Name", cp(fr))
	fstr, jb := x.api(jb, "Gen:Str", cp(gr), cp(fname))
	part, jb := x.quote(jb, "\ntry w.Str($0)\ntry w.Str(\": \")\ntry self.$1.Format(w)\n", cp(fstr), cp(fname))
	x.next(x.add(jb, body, part), l)
	closing, e := x.quote(l.exit, "\ntry w.Str(\")\")\n")
	e = x.add(e, body, closing)
	tn, e := x.api(e, "Type:Name", cp(tr))
	q, e := x.quote(e, "\nimpl Format for $0 {\nFormat(&self, w: mut Writer): WriteError!void {\n$1\n}\n}\n", cp(tn), cp(body))
	_, e = x.api(e, "Gen:Emit", cp(1), cp(q))
	x.ret(e)
	x.done()
	return g
}

// dynOf is dyn.Of(I): FailOn for a non-interface and for a method
// that takes self by value, is generic or declares flows; else a struct of
// a data pointer and a table of function pointers, and one forwarding
// method per interface method, built from Name, Receiver, Params (joined
// with Code.Then) and Result.
func (w *world) dynOf() types.ItemID {
	g, x, tr, gr := w.genHead("dyn", "Of")
	tyT, codeT := w.typeT(), w.named(w.known.Code)
	failOn := func(blk mir.BlockID, msg string) {
		ty := x.local(tyT)
		x.assign(blk, lp(ty), use(mir.Copy(x.deref(tr))))
		_, fb := x.api(blk, "Gen:FailOn", cp(1), cp(ty), w.strConst(msg))
		x.ret(fb)
	}
	disc := func(blk mir.BlockID, e mir.LocalID, op mir.BinOp, v uint64) mir.LocalID {
		d, c := x.local(types.U64), x.local(types.Bool)
		x.assign(blk, lp(d), mir.Rvalue{Kind: mir.RvDiscriminant, Place: lp(e)})
		x.assign(blk, lp(c), mir.Rvalue{Kind: mir.RvBinary, Bin: op, A: cp(d), B: mir.Imm(types.U64, v)})
		return c
	}
	k, b := x.api(0, "Type:Kind", cp(tr))
	notI := disc(b, k, mir.Ne, tkInterface)
	bad, ok := x.block(), x.block()
	x.branch(b, cp(notI), bad, ok)
	failOn(bad, "dyn.Of needs an interface")

	mt := w.named(w.known.Method)
	ms, b := x.api(ok, "Type:Methods", cp(tr))
	l, mr, lb := x.each(b, ms, mt)
	rv, lb := x.api(lb, "Method:Receiver", cp(mr))
	byValue := disc(lb, rv, mir.Eq, 1)
	refuse, n1, n2, n3 := x.block(), x.block(), x.block(), x.block()
	x.branch(lb, cp(byValue), refuse, n1)
	gn, n1b := x.api(n1, "Method:Generic", cp(mr))
	x.branch(n1b, cp(gn), refuse, n2)
	fl, n2b := x.api(n2, "Method:HasFlows", cp(mr))
	x.branch(n2b, cp(fl), refuse, n3)
	x.next(n3, l)
	failOn(refuse, "dyn.Of: a method takes self by value, is generic or declares flows")

	name, e := x.api(l.exit, "Gen:Ident", cp(gr), w.strConst("StoreDyn"))
	table, e := x.api(e, "Gen:Ident", cp(gr), w.strConst("StoreDynTable"))
	slots, e := x.api(e, "Gen:Block", cp(gr))
	methods, e := x.api(e, "Gen:Block", cp(gr))
	l2, mr2, lb2 := x.each(e, ms, mt)
	mn, lb2 := x.api(lb2, "Method:Name", cp(mr2))
	rv2, lb2 := x.api(lb2, "Method:Receiver", cp(mr2))
	isMut := disc(lb2, rv2, mir.Eq, 3)
	sig := x.local(codeT)
	mutB, refB, joinB := x.block(), x.block(), x.block()
	x.branch(lb2, cp(isMut), mutB, refB)
	for _, r := range []struct {
		blk  mir.BlockID
		text string
	}{{mutB, "mut self"}, {refB, "&self"}} {
		c, cb := x.quote(r.blk, r.text)
		x.assign(cb, lp(sig), use(cp(c)))
		x.gotoB(cb, joinB)
	}
	tys, jb := x.quote(joinB, "*u8")
	args, jb := x.quote(jb, "self.data")
	ps, jb := x.api(jb, "Method:Params", cp(mr2))
	l3, pr, lb3 := x.each(jb, ps, w.named(w.known.Param))
	pn, lb3 := x.api(lb3, "Param:Name", cp(pr))
	pt, lb3 := x.api(lb3, "Param:Type", cp(pr))
	q1, lb3 := x.quote(lb3, ", $0: $1", cp(pn), cp(pt))
	lb3 = x.then(lb3, sig, q1)
	q2, lb3 := x.quote(lb3, ", $0", cp(pt))
	lb3 = x.then(lb3, tys, q2)
	q3, lb3 := x.quote(lb3, ", $0", cp(pn))
	x.next(x.then(lb3, args, q3), l3)
	res, rb := x.api(l3.exit, "Method:Result", cp(mr2))
	slot, rb := x.quote(rb, "\n$0: fn($1): $2\n", cp(mn), cp(tys), cp(res))
	rb = x.add(rb, slots, slot)
	fwd, rb := x.quote(rb, "\n$0.$1($2): $3 {\nreturn self.vt.$1($4)\n}\n", cp(name), cp(mn), cp(sig), cp(res), cp(args))
	x.next(x.add(rb, methods, fwd), l2)

	q, eb := x.quote(l2.exit, "\nexport struct $0 {\ndata: *u8\nvt: &$1\n}\n\nexport struct $1 {\n$2\n}\n\n$3\n",
		cp(name), cp(table), cp(slots), cp(methods))
	_, eb = x.api(eb, "Gen:Emit", cp(1), cp(q))
	x.ret(eb)
	x.done()
	return g
}

// TestSketchesHandBuilt: derive.Hash and derive.Format over struct
// User { name: str, age: int } and dyn.Of over interface Store { Get(&self,
// k: int): str; Put(mut self, v: []u8) } print their goldens and parse
// with no diagnostic; dyn.Of refuses a struct and a by-value method with
// E0903 at the type's declaration.
func TestSketchesHandBuilt(t *testing.T) {
	w := newWorld(t)
	_, user := w.structType("main", "User", false, "name", types.Str, "age", types.Int)
	store := w.item("app", "Store", types.KindInterface, &types.InterfaceDecl{})
	w.items.Get(store).Exported = true
	method := func(name string, recv types.Receiver, res types.TypeID, params ...types.Param) MethodView {
		self := w.tab.Ref(w.tab.SelfType())
		switch recv {
		case types.ByMut:
			self = w.tab.Mut(w.tab.SelfType())
		case types.ByValue:
			self = w.tab.SelfType()
		}
		s := types.Signature{Name: w.in.Intern(name), Receiver: recv, Result: res,
			Params: append([]types.Param{{Name: w.in.Intern("self"), Type: self}}, params...)}
		return MethodView{Sig: s}
	}
	w.views[store] = &DeclView{Item: store, Kind: types.KindInterface, Exported: true, Methods: []MethodView{
		method("Get", types.ByShared, types.Str, types.Param{Name: w.in.Intern("k"), Type: types.Int}),
		method("Put", types.ByMut, types.Void, types.Param{Name: w.in.Intern("v"), Type: w.tab.Slice(types.U8)}),
	}}
	dyn := w.dynOf()
	for _, c := range []struct {
		golden string
		gen    types.ItemID
		path   string
		arg    Arg
	}{
		{"derive_hash.golden", w.deriveHash(), "derive.Hash", typeArg(user)},
		{"derive_format.golden", w.deriveFormat(), "derive.Format", typeArg(user)},
		{"dyn_of.golden", dyn, "dyn.Of", Arg{Kind: ArgInterface, Item: store}},
	} {
		t.Run(c.path, func(t *testing.T) {
			out := w.context(1).Run(w.module("main"), []Line{w.line(0, c.path, c.gen, c.arg)})
			noDiags(t, out)
			if out.AST == nil || len(out.AST.Items) == 0 {
				t.Fatalf("no parsed items:\n%s", out.Text())
			}
			golden(t, c.golden, out.Text())
		})
	}
	// dyn.Of's refusals: E0903 at the type's declaration, output discarded.
	byValue := w.item("app", "Sink", types.KindInterface, &types.InterfaceDecl{})
	w.views[byValue] = &DeclView{Item: byValue, Kind: types.KindInterface,
		Methods: []MethodView{method("Close", types.ByValue, types.Void)}}
	for _, c := range []struct {
		arg  Arg
		at   types.ItemID
		want string
	}{
		{typeArg(user), w.tab.Get(user).Item, "needs an interface"},
		{Arg{Kind: ArgInterface, Item: byValue}, byValue, "by value"},
	} {
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "dyn.Of", dyn, c.arg)})
		wantCodes(t, out, "E0903")
		if d := out.Diags[0]; d.At != w.items.Get(c.at).Span || !strings.Contains(d.Message, c.want) {
			t.Fatalf("E0903 at %v (%s), want %v", d.At, d.Message, w.items.Get(c.at).Span)
		}
		if out.File != nil {
			t.Fatalf("a refused run's output was kept:\n%s", out.Text())
		}
	}
}

// TestArgsElemExportedConst: the API calls the sketches do not
// make: Type.Args (const arguments left out), Type.Elem (and E0901 for a
// type with no element), Field.Exported, Param.Const, and E0901 for
// Method.Params of a generic method.
func TestArgsElemExportedConst(t *testing.T) {
	w := newWorld(t)
	pid, _ := w.structType("app", "Pair", true, "A", w.tab.Param(0))
	w.views[pid].Generics = []types.GenericParam{{Name: w.in.Intern("T")}}
	pair := w.tab.Named(pid, []types.GenericArg{types.TypeArg(types.Str)})
	g := w.gen1("AE", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		as, b := x.api(0, "Type:Args", cp(tr))
		n := x.local(types.Int)
		x.assign(b, lp(n), mir.Rvalue{Kind: mir.RvLen, Place: lp(as)})
		idx, a0 := x.local(types.Int), x.local(w.typeT())
		x.assign(b, lp(idx), use(mir.Imm(types.Int, 0)))
		x.assign(b, lp(a0), use(mir.Copy(x.b.Place(as, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: w.typeT()}))))
		return x.emit(b, "const N = $0\nvar a: $1 = x", cp(n), cp(a0))
	})
	out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.AE", g, typeArg(pair))})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const N = 1") || !strings.Contains(out.Text(), "var a: str = x") {
		t.Fatalf("Type.Args:\n%s", out.Text())
	}
	elem := w.gen1("E", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		e, b := x.api(0, "Type:Elem", cp(tr))
		return x.emit(b, "var e: $0 = x", cp(e))
	})
	for _, c := range []struct {
		of   types.TypeID
		want string
	}{
		{w.tab.Slice(types.U8), "var e: u8 = x"},
		{w.tab.Optional(w.tab.Ref(types.Int)), "var e: &int = x"},
		{w.tab.Array(types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 4)}, types.Bool), "var e: bool = x"},
		{types.Int, ""},
	} {
		out := w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.E", elem, typeArg(c.of))})
		if c.want == "" {
			wantCodes(t, out, "E0901")
			if !strings.Contains(out.Diags[0].Message, "has no element") {
				t.Fatalf("Type.Elem of int: %s", out.Diags[0].Message)
			}
			continue
		}
		wantCodes(t, out)
		if !strings.Contains(out.Text(), c.want) {
			t.Fatalf("Type.Elem of %s:\n%s", w.tab.TypeString(c.of, nil), out.Text())
		}
	}
	// Field.Exported over the line's own module: every field, each flag.
	_, s := w.structType("main", "S", false, "Pub", types.Int, "own", types.Int)
	fe := w.gen1("FE", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		f0, b := x.fieldAt(0, tr, 0)
		e0, b := x.api(b, "Field:Exported", cp(f0))
		f1, b := x.fieldAt(b, tr, 1)
		e1, b := x.api(b, "Field:Exported", cp(f1))
		return x.emit(b, "const X = [$0, $1]", cp(e0), cp(e1))
	})
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.FE", fe, typeArg(s))})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const X = [true, false]") {
		t.Fatalf("Field.Exported:\n%s", out.Text())
	}
	// Param.Const, and Method.Params of a generic method.
	iface := w.item("app", "Sized", types.KindInterface, &types.InterfaceDecl{})
	self := types.Param{Name: w.in.Intern("self"), Type: w.tab.Ref(w.tab.SelfType())}
	sigN := types.Signature{Name: w.in.Intern("Take"), Receiver: types.ByShared, Result: types.Void,
		Params: []types.Param{self, {Name: w.in.Intern("n"), Type: types.Int, Const: true}, {Name: w.in.Intern("m"), Type: types.Int}}}
	sigG := types.Signature{Name: w.in.Intern("Map"), Receiver: types.ByShared, Result: types.Void, OwnGenerics: 1,
		Params: []types.Param{self}}
	w.views[iface] = &DeclView{Item: iface, Kind: types.KindInterface, Methods: []MethodView{{Sig: sigN}, {Sig: sigG}}}
	method := func(x *bb, blk mir.BlockID, tr mir.LocalID, i int) (mir.LocalID, mir.BlockID) {
		ms, b := x.api(blk, "Type:Methods", cp(tr))
		mt := w.named(w.known.Method)
		idx, m := x.local(types.Int), x.local(mt)
		x.assign(b, lp(idx), use(mir.Imm(types.Int, uint64(i))))
		x.assign(b, lp(m), use(mir.Copy(x.b.Place(ms, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: mt}))))
		mr := x.ref(m, false)
		x.flush(b)
		return mr, b
	}
	pc := w.gen1("PC", func(x *bb, tr, gr mir.LocalID) mir.BlockID {
		mr, b := method(x, 0, tr, 0)
		ps, b := x.api(b, "Method:Params", cp(mr))
		pt := w.named(w.known.Param)
		var cs []mir.Operand
		for i := range 2 {
			idx, p := x.local(types.Int), x.local(pt)
			x.assign(b, lp(idx), use(mir.Imm(types.Int, uint64(i))))
			x.assign(b, lp(p), use(mir.Copy(x.b.Place(ps, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: pt}))))
			pr := x.ref(p, false)
			x.flush(b)
			c, nb := x.api(b, "Param:Const", cp(pr))
			cs, b = append(cs, cp(c)), nb
		}
		return x.emit(b, "const C = [$0, $1]", cs...)
	})
	out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.PC", pc, Arg{Kind: ArgInterface, Item: iface})})
	wantCodes(t, out)
	if !strings.Contains(out.Text(), "const C = [true, false]") {
		t.Fatalf("Param.Const:\n%s", out.Text())
	}
	for _, q := range []string{"Method:Params", "Method:Result"} {
		gp := w.gen1("G"+q[7:], func(x *bb, tr, gr mir.LocalID) mir.BlockID {
			mr, b := method(x, 0, tr, 1)
			gn, b := x.api(b, "Method:Generic", cp(mr))
			_, b = x.api(b, q, cp(mr))
			return x.emit(b, "const G = $0", cp(gn))
		})
		out = w.context(1).Run(w.module("main"), []Line{w.line(0, "lib.G", gp, Arg{Kind: ArgInterface, Item: iface})})
		wantCodes(t, out, "E0901")
		if !strings.Contains(out.Diags[0].Message, "test Method.Generic first") {
			t.Fatalf("%s of a generic method: %s", q, out.Diags[0].Message)
		}
	}
}
