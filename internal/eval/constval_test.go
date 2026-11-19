// Why: a compile-time result becomes static data, and its bytes, relocations
// and identity must come out canonical so equal values are equal consts.
// What: tests of const values out and in: the Table example, the canonical
// value form, a reference to a const keeping its identity, frame memory
// copied to fresh statics, cyclic values and float consts.
// How: hand-lowered bodies run through the Machine; the tests read the
// resulting ConstValue's Bytes and Relocs directly.

package eval

import (
	"math"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// squares declares Squares(): [4]i32 in module mod, hand-lowered.
func (w *world) squares(mod string) types.ItemID {
	arr := w.array(4, types.I32)
	f := w.fn(mod, "Squares", arr)
	x := w.body(f, arr)
	t, i, cnd, sq := x.local(arr), x.local(types.Int), x.local(types.Bool), x.local(types.Int)
	head, body, exit := x.block(), x.block(), x.block()
	x.assign(0, lp(t), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.I32, 0), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 4)}})
	x.assign(0, lp(i), use(imm(types.Int, 0)))
	x.gotoB(0, head)
	x.assign(head, lp(cnd), bin(mir.Lt, cp(i), imm(types.Int, 4)))
	x.branch(head, cp(cnd), body, exit)
	x.assign(body, lp(sq), bin(mir.Mul, cp(i), cp(i)))
	x.assign(body, x.b.Place(t, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: types.I32}),
		mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastChecked, A: cp(sq), Type: types.I32})
	x.assign(body, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
	x.gotoB(body, head)
	x.assign(exit, lp(0), use(cp(t)))
	x.ret(exit)
	x.done()
	return f
}

func TestTableSquares(t *testing.T) {
	w := newWorld(t)
	sq := w.squares("main")
	arr := w.array(4, types.I32)
	table := w.constItem("main", "Table", arr, 0)
	x := w.body(table, arr)
	b1 := x.block()
	x.call(0, sq, lp(0), b1)
	x.ret(b1)
	x.done()
	r := w.evaluator(0).Eval(constReq(w, table, types.StageBodies))
	if r.Err != nil || r.Value == types.NoConst {
		t.Fatalf("Table: %+v", r)
	}
	v := w.consts.Get(r.Value)
	want := []byte{0, 0, 0, 0, 1, 0, 0, 0, 4, 0, 0, 0, 9, 0, 0, 0}
	if v.Type != arr || string(v.Bytes) != string(want) || len(v.Relocs) != 0 || v.Align != 4 {
		t.Errorf("Table = %+v, want [0, 1, 4, 9] as [4]i32", v)
	}
	// At StageSignatures the same const enters a same-module body: E0908.
	r = w.evaluator(0).Eval(constReq(w, table, types.StageSignatures))
	if r.Err == nil || r.Err.Kind != types.EvalStage || len(r.Err.Items) != 2 || r.Err.Items[0] != table || r.Err.Items[1] != sq {
		t.Errorf("at stage 5: %+v, want E0908 [Table, Squares]", r.Err)
	}
}

func TestCanonicalValue(t *testing.T) {
	w := newWorld(t)
	p, pt := w.structType("lib", "Pad", types.U8, types.F64, types.Bool, types.I16)
	_, et := w.enumType("lib", "E", []types.TypeID{types.I64, types.I64}, []types.TypeID{types.U8})
	opt := w.tab.Optional(types.I64)
	rec := w.tab.Record([]types.RecordField{{Name: w.in.Intern("x"), Type: pt}, {Name: w.in.Intern("e"), Type: et}, {Name: w.in.Intern("o"), Type: opt}})
	nan := w.consts.Intern(types.ConstValue{Type: types.F64, Align: 8, Bytes: le64(0xfff8000000000abc)})
	f := w.fn("lib", "Mk", rec)
	x := w.body(f, rec)
	sv, ev, ov := x.local(pt), x.local(et), x.local(opt)
	x.assign(0, lp(sv), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: p,
		Ops: x.b.AddOperands(imm(types.U8, 7), mir.Const(types.F64, nan), imm(types.Bool, 1), imm(types.I16, -2))})
	x.assign(0, lp(ev), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggEnum, Item: w.tab.Get(et).Item, Variant: 0,
		Ops: x.b.AddOperands(imm(types.I64, -1), imm(types.I64, -1))})
	x.assign(0, lp(ev), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggEnum, Item: w.tab.Get(et).Item, Variant: 1,
		Ops: x.b.AddOperands(imm(types.U8, 5))})
	x.assign(0, lp(ov), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggSome, Ops: x.b.AddOperands(imm(types.I64, -1))})
	x.assign(0, lp(ov), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggNone})
	x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggRecord, Type: rec, Ops: x.b.AddOperands(cp(ev), cp(ov), cp(sv))})
	x.ret(0)
	x.done()
	e := w.evaluator(0)
	r := w.call(e, f, 0)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	v := w.consts.Get(r.Value)
	l, _ := w.Layout(rec)
	// Record fields sorted by name: e, o, x.
	eOff, oOff, xOff := l.Fields[0].Offset, l.Fields[1].Offset, l.Fields[2].Offset
	pl, _ := w.Layout(pt)
	el, _ := w.Layout(et)
	ol, _ := w.Layout(opt)
	check := func(name string, off, n uint64, want []byte) {
		t.Helper()
		if got := v.Bytes[off : off+n]; string(got) != string(want) {
			t.Errorf("%s: % x, want % x", name, got, want)
		}
	}
	check("struct u8", xOff, 1, []byte{7})
	check("struct padding", xOff+1, pl.Fields[1].Offset-1, make([]byte, pl.Fields[1].Offset-1))
	check("canonical NaN (C2)", xOff+pl.Fields[1].Offset, 8, le64(types.CanonicalNaN64))
	check("bool (C3)", xOff+pl.Fields[2].Offset, 1, []byte{1})
	check("enum tag", eOff, 1, []byte{1})
	check("enum inactive bytes (C1)", eOff+el.Variants[1].Fields[0].Offset+1, el.Shape.Size-el.Variants[1].Fields[0].Offset-1,
		make([]byte, el.Shape.Size-el.Variants[1].Fields[0].Offset-1))
	check("none: some byte 0 and value bytes zero (C1, C5)", oOff, ol.Shape.Size, make([]byte, ol.Shape.Size))
	if r2 := w.call(e, f, 0); r2.Value != r.Value {
		t.Errorf("equal values interned twice: %d and %d", r.Value, r2.Value)
	}
}

func le64(x uint64) []byte {
	b := make([]byte, 8)
	types.StoreUint(b, 8, x)
	return b
}

func TestRefToConstKeepsIdentity(t *testing.T) {
	w := newWorld(t)
	limit := w.constItem("main", "Limit", types.Int, 0)
	x := w.body(limit, types.Int)
	x.assign(0, lp(0), use(imm(types.Int, 10)))
	x.ret(0)
	x.done()
	ref := w.tab.Ref(types.Int)
	rc := w.constItem("main", "R", ref, 0)
	y := w.body(rc, ref)
	y.assign(0, lp(0), mir.Rvalue{Kind: mir.RvGlobalRef, Item: limit})
	y.ret(0)
	y.done()
	e := w.evaluator(0)
	lv := e.Eval(constReq(w, limit, types.StageBodies))
	r := e.Eval(constReq(w, rc, types.StageBodies))
	if r.Err != nil || lv.Err != nil {
		t.Fatalf("%+v %+v", lv.Err, r.Err)
	}
	v := w.consts.Get(r.Value)
	if len(v.Relocs) != 1 || v.Relocs[0].Kind != types.RelocData || v.Relocs[0].Data != lv.Value || string(v.Bytes) != string(make([]byte, 8)) {
		t.Errorf("&Limit = %+v, want a RelocData to Limit's value %d with a zero word (C4)", v, lv.Value)
	}
	// A str const keeps its data const.
	s := w.strConst("hello")
	sc := w.constItem("main", "S", types.Str, 0)
	z := w.body(sc, types.Str)
	z.assign(0, lp(0), use(mir.Const(types.Str, s)))
	z.ret(0)
	z.done()
	if r := e.Eval(constReq(w, sc, types.StageBodies)); r.Value != s {
		t.Errorf("str const = %d, want the literal's %d", r.Value, s)
	}
}

func TestFrameSliceBecomesStatic(t *testing.T) {
	w := newWorld(t)
	arr := w.array(5, types.I64)
	sl := w.tab.Slice(types.I64)
	f := w.fn("lib", "Mid", sl)
	x := w.body(f, sl)
	a := x.local(arr)
	x.assign(0, lp(a), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggArray, Type: arr,
		Ops: x.b.AddOperands(imm(types.I64, 1), imm(types.I64, 2), imm(types.I64, 3), imm(types.I64, 4), imm(types.I64, 5))})
	x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvSlice, Place: lp(a), HasLo: true, A: imm(types.Int, 1), HasHi: true, B: imm(types.Int, 4)})
	x.ret(0)
	x.done()
	before := w.tab.Len()
	r := w.call(w.evaluator(0), f, 0)
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	v := w.consts.Get(r.Value)
	if len(v.Relocs) != 1 || types.LoadUint(v.Bytes[8:], 8) != 3 {
		t.Fatalf("slice value %+v", v)
	}
	p := w.consts.Get(v.Relocs[0].Data)
	if pt := w.tab.Get(p.Type); pt.Kind != types.KArray || pt.Elem != types.I64 || w.intOf(pt.Len.Value) != 3 {
		t.Errorf("pointee type %s, want [3]i64 (C7)", w.tab.TypeString(p.Type, nil))
	}
	if types.LoadUint(p.Bytes, 8) != 2 || types.LoadUint(p.Bytes[16:], 8) != 4 {
		t.Errorf("pointee bytes % x", p.Bytes)
	}
	if grew := w.tab.Len() - before; grew != 1 {
		t.Errorf("the evaluation interned %d types, want only [3]i64 (EG-08)", grew)
	}
	// An empty slice has a zero data word relocated to the [0]i64 const, as
	// the empty piece of a format string needs: never a null pointer.
	g := w.fn("lib", "Empty", sl)
	y := w.body(g, sl)
	b := y.local(arr)
	y.assign(0, lp(b), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.I64, 0), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 5)}})
	y.assign(0, lp(0), mir.Rvalue{Kind: mir.RvSlice, Place: lp(b), HasLo: true, A: imm(types.Int, 2), HasHi: true, B: imm(types.Int, 2)})
	y.ret(0)
	y.done()
	v = w.consts.Get(w.call(w.evaluator(0), g, 0).Value)
	if len(v.Relocs) != 1 || v.Relocs[0].Kind != types.RelocData || string(v.Bytes) != string(make([]byte, 16)) {
		t.Fatalf("empty slice %+v", v)
	}
	if p := w.consts.Get(v.Relocs[0].Data); len(p.Bytes) != 0 || w.tab.TypeString(p.Type, nil) != "[0]i64" {
		t.Errorf("empty slice pointee %s % x, want [0]i64 of no bytes", w.tab.TypeString(p.Type, nil), p.Bytes)
	}
}

func TestConstOut_Cycle(t *testing.T) {
	w := newWorld(t)
	// struct Node { next: ?&Node }; the result points at itself.
	node := w.item("lib", "Node", types.KindStruct, &types.StructDecl{})
	nt := w.tab.Named(node, nil)
	ot := w.tab.Optional(w.tab.Ref(nt))
	w.items.Get(node).Decl.(*types.StructDecl).Fields = []types.FieldDecl{{Name: w.in.Intern("next"), Type: ot}}
	f := w.fn("lib", "Loop", nt)
	x := w.body(f, nt)
	r0, o := x.local(w.tab.Ref(nt)), x.local(ot)
	x.assign(0, lp(r0), mir.Rvalue{Kind: mir.RvRef, Place: lp(0)})
	x.assign(0, lp(o), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggSome, Ops: x.b.AddOperands(cp(r0))})
	x.assign(0, x.b.Place(0, mir.Projection{Kind: mir.ProjField, A: 0, Type: ot}), use(cp(o)))
	x.ret(0)
	x.done()
	r := w.call(w.evaluator(0), f, 0)
	if r.Err == nil || r.Err.Kind != types.EvalRefused || r.Err.Refusal != types.RefuseRawAccess || r.Err.Message != "cyclic value" {
		t.Errorf("got %+v, want raw-access cyclic value", r.Err)
	}
}

func TestFloatConstsOut(t *testing.T) {
	w := newWorld(t)
	f := w.fn("lib", "Q", types.F32)
	x := w.body(f, types.F32)
	one := w.consts.Intern(types.ConstValue{Type: types.F32, Align: 4, Bytes: []byte{0, 0, 0x80, 0x3f}})
	zero := w.consts.Intern(types.ConstValue{Type: types.F32, Align: 4, Bytes: make([]byte, 4)})
	x.assign(0, lp(0), bin(mir.Sub, mir.Const(types.F32, one), mir.Const(types.F32, zero)))
	x.ret(0)
	x.done()
	r := w.call(w.evaluator(0), f, 0)
	if r.Err != nil || math.Float32frombits(uint32(types.LoadUint(w.consts.Get(r.Value).Bytes, 4))) != 1 {
		t.Errorf("1 - 0 = %+v", r)
	}
}
