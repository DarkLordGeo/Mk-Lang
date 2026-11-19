// Why: mono resolves per instance what the generic body leaves open: type
// tests, onerr guards, contract calls in release builds, spawns and fields
// loops.
// What: TestTypeTestFolds, TestMonoOnerrResolved,
// TestMonoReleaseDropsContracts, TestMonoSpawnMarks and TestMonoFieldsUnroll.
// How: hand-built generic bodies are instantiated and their blocks and
// terminators compared with the expected shape.

package mono

import (
	"fmt"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// blocks adds n empty blocks to b and returns their IDs.
func blocks(b *mir.Body, n int) []mir.BlockID {
	out := make([]mir.BlockID, n)
	for i := range out {
		out[i] = b.AddBlock(mir.OriginBody, mir.NoDefer)
	}
	return out
}

func (w *world) stmt(b *mir.Body, blk mir.BlockID, place mir.Place, rv mir.Rvalue) {
	b.Blocks[blk].Stmts = append(b.Blocks[blk].Stmts, mir.Statement{Kind: mir.StAssign, Place: place, Rvalue: rv, Span: b.AddSpan(w.span())})
}

func (w *world) gotoTerm(b *mir.Body, from, to mir.BlockID) {
	b.Blocks[from].Term = mir.Terminator{Kind: mir.TermGoto, Span: b.AddSpan(w.span()), Target: to}
}

func (w *world) retTerm(b *mir.Body, blk mir.BlockID) {
	b.Blocks[blk].Term = mir.Terminator{Kind: mir.TermReturn, Span: b.AddSpan(w.span()), Exit: mir.ExitNormal}
}

// TestTypeTestFolds: `if T is i32 { narrowed } else { other }` folds per
// instance; the dead branch emits no code (its blocks are dropped and the
// rest renumbered in order), RvTypeIs is an immediate and CastNarrow a use.
func TestTypeTestFolds(t *testing.T) {
	w := newWorld()
	p0 := w.tab.Param(0)
	pick := w.fn("Pick", 1, []types.TypeID{p0}, types.Int)
	gb := mir.NewBody(pick, w.span(), types.Int)
	gb.GenericCount = 1
	gb.AddParam(p0, w.in.Intern("x"), 0)
	narrowed := gb.AddLocal(mir.Local{Type: types.I32, Kind: mir.LocalUser, Loop: mir.NoLoop})
	is := gb.AddLocal(mir.Local{Type: types.Bool, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	bb := blocks(gb, 4)
	w.stmt(gb, bb[0], gb.Place(is), mir.Rvalue{Kind: mir.RvTypeIs, Param: 0, Type: types.I32})
	gb.Blocks[bb[0]].Term = mir.Terminator{Kind: mir.TermTypeTest, Span: gb.AddSpan(w.span()), Param: 0, Type: types.I32, Yes: bb[1], No: bb[2]}
	w.stmt(gb, bb[1], gb.Place(narrowed), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastNarrow, A: mir.Copy(gb.Place(1)), Type: types.I32})
	w.stmt(gb, bb[1], gb.Place(mir.ReturnLocal), mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Int, 1)})
	w.gotoTerm(gb, bb[1], bb[3])
	w.stmt(gb, bb[2], gb.Place(mir.ReturnLocal), mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Int, 2)})
	w.gotoTerm(gb, bb[2], bb[3])
	w.retTerm(gb, bb[3])
	w.bodies[pick] = gb

	build := func(a types.TypeID) *mir.Body {
		var ib *mir.Body
		if ie := source.Guard("t", source.Span{}, func() { ib = Instantiate(w, pick, []types.GenericArg{ty(a)}, Options{Validate: true}) }); ie != nil {
			t.Fatalf("Pick[%s]: %s", w.tab.TypeString(a, nil), ie)
		}
		return ib
	}
	yes := build(types.I32)
	if len(yes.Blocks) != 3 || yes.Blocks[0].Term.Kind != mir.TermGoto || yes.Blocks[0].Term.Target != 1 || yes.Blocks[1].Term.Target != 2 {
		t.Fatalf("Pick[i32]:\n%s", (&mir.Printer{Types: w.tab, Items: w.items}).Print(yes))
	}
	if rv := yes.Blocks[0].Stmts[0].Rvalue; rv.Kind != mir.RvUse || rv.A.Bits != 1 {
		t.Fatalf("Pick[i32] typeis: %v", rv)
	}
	if rv := yes.Blocks[1].Stmts[0].Rvalue; rv.Kind != mir.RvUse || rv.A.Kind != mir.OpCopy {
		t.Fatalf("Pick[i32] narrow: %v", rv)
	}
	no := build(types.Bool)
	if len(no.Blocks) != 3 || no.Blocks[1].Stmts[0].Rvalue.A.Bits != 2 {
		t.Fatalf("Pick[bool]:\n%s", (&mir.Printer{Types: w.tab, Items: w.items}).Print(no))
	}
	if rv := no.Blocks[0].Stmts[0].Rvalue; rv.A.Bits != 0 {
		t.Fatalf("Pick[bool] typeis: %v", rv)
	}
	for _, blk := range no.Blocks {
		for _, st := range blk.Stmts {
			if st.Rvalue.Kind == mir.RvCast {
				t.Fatal("Pick[bool] kept the dead branch's narrowing")
			}
		}
	}
	if len(gb.Blocks) != 4 || gb.Blocks[0].Term.Kind != mir.TermTypeTest {
		t.Fatal("the generic body was modified")
	}
}

// TestMonoOnerrResolved: a guard becomes a goto to its onerr body or past
// it, as borrow resolved it, by the generic body's block number; a
// non-generic body with a guard is rewritten too.
func TestMonoOnerrResolved(t *testing.T) {
	for _, runs := range []bool{true, false} {
		w := newWorld()
		f := w.fn("Save", 0, []types.TypeID{types.Int}, types.Int)
		b := mir.NewBody(f, w.span(), types.Int)
		b.AddParam(types.Int, w.in.Intern("x"), 0)
		bb := blocks(b, 4)
		w.gotoTerm(b, bb[0], bb[1])
		b.Blocks[bb[1]].Term = mir.Terminator{Kind: mir.TermOnerrGuard, Span: b.AddSpan(w.span()), Value: 1, Yes: bb[2], No: bb[3]}
		w.stmt(b, bb[2], b.Place(mir.ReturnLocal), mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Int, 7)})
		w.gotoTerm(b, bb[2], bb[3])
		w.retTerm(b, bb[3])
		w.bodies[f] = b
		w.onerr[onerrKey{f, bb[1]}] = runs
		in, _ := w.collect(t, Options{}, mainRoot(f))
		ib := w.inst(t, in, "m.Save").Body
		if ib == b {
			t.Fatal("a body with a guard was not rewritten")
		}
		for _, blk := range ib.Blocks {
			if blk.Term.Kind == mir.TermOnerrGuard {
				t.Fatal("guard survived")
			}
		}
		want := 4
		if !runs {
			want = 3
		}
		if len(ib.Blocks) != want {
			t.Fatalf("onerr runs %v: %d blocks:\n%s", runs, len(ib.Blocks), (&mir.Printer{Types: w.tab, Items: w.items}).Print(ib))
		}
	}
}

// TestMonoReleaseDropsContracts: in release, a call of a contract predicate
// writes true to its destination and a trace hook call disappears; neither
// callee is collected. Dev keeps both.
func TestMonoReleaseDropsContracts(t *testing.T) {
	w := newWorld()
	f := w.fn("Div", 0, []types.TypeID{types.Int}, types.Int)
	pred := w.items.AddItem(&types.Item{Module: w.mod, Owner: f, Key: types.ContractKey(types.ItemKey("m", "Div"), "requires", 0), Kind: types.KindFunc})
	w.items.Get(pred).Decl = &types.FuncDecl{Sig: types.Signature{Item: pred, Params: []types.Param{{Type: w.tab.Ref(types.Int)}}, Result: types.Bool}}
	pb := w.body(pred)
	pb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
	pb.ret()
	fd := w.items.Get(f).Decl.(*types.FuncDecl)
	fd.Sig.Contracts = []types.Contract{{Kind: types.Requires, Body: pred}}
	rt := w.items.AddModule(&types.Module{Path: "std.rt"})
	trace := w.items.AddItem(&types.Item{Module: rt, Key: types.ItemKey("std.rt", "TraceFail"), Name: w.in.Intern("TraceFail"), Kind: types.KindFunc})
	w.items.Get(trace).Decl = &types.FuncDecl{Sig: types.Signature{Item: trace, Result: types.Void}}
	w.body(trace).ret()
	b := w.body(f)
	ok := b.temp(types.Bool)
	b.call(pred, nil, ok, arg(mir.Imm(types.Int, 0)))
	b.call(trace, nil, b.temp(types.Void))
	b.ret()

	rel, _ := w.collect(t, Options{Profile: types.Release}, mainRoot(f))
	if len(rel.List) != 1 {
		t.Fatalf("release collected %d instances:\n%s", len(rel.List), summary(rel))
	}
	ib := rel.List[0].Body
	calls := 0
	for _, blk := range ib.Blocks {
		if blk.Term.Kind == mir.TermCall {
			calls++
		}
	}
	if calls != 0 || ib.Blocks[0].Stmts[0].Rvalue.A.Bits != 1 || ib.Blocks[0].Stmts[0].Place.Local != ok {
		t.Fatalf("release body:\n%s", (&mir.Printer{Types: w.tab, Items: w.items}).Print(ib))
	}
	dev, _ := w.collect(t, Options{Profile: types.Dev}, mainRoot(f))
	if len(dev.List) != 3 || w.inst(t, dev, "m.Div").Body != w.bodies[f] {
		t.Fatalf("dev collected:\n%s", summary(dev))
	}
}

// TestMonoSpawnMarks: the callee of an s.go is Spawned; a plain callee is
// not; a function used as a value is AddrTaken.
func TestMonoSpawnMarks(t *testing.T) {
	w := newWorld()
	s := w.leaf("Task")
	p := w.leaf("Plain")
	v := w.leaf("Value")
	main := w.fn("Main", 0, nil, types.Void)
	mb := w.body(main)
	scope := mb.b.AddLocal(mir.Local{Type: types.Int, Kind: mir.LocalScope, Loop: mir.NoLoop})
	mb.callTerm(mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: s}}, mb.temp(types.Void), scope)
	mb.call(p, nil, mb.temp(types.Void))
	fnT := w.tab.Fn(types.FnType{Result: types.Void})
	mb.assign(mb.temp(fnT), mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: v}, Type: fnT})
	mb.ret()
	in, _ := w.collect(t, Options{}, mainRoot(main))
	if i := w.inst(t, in, "m.Task"); !i.Spawned || i.AddrTaken {
		t.Fatalf("Task: spawned %v addr %v", i.Spawned, i.AddrTaken)
	}
	if i := w.inst(t, in, "m.Plain"); i.Spawned || i.AddrTaken {
		t.Fatalf("Plain: spawned %v addr %v", i.Spawned, i.AddrTaken)
	}
	if i := w.inst(t, in, "m.Value"); !i.AddrTaken || i.Spawned {
		t.Fatalf("Value: spawned %v addr %v", i.Spawned, i.AddrTaken)
	}
}

// TestMonoFieldsUnroll: the shape of doc/reference.md's Dump example. A
// generic fields loop over T (hidden generic 1 for the current field) is
// unrolled per exported field of S, in declaration order: each copy has the
// field's name as a str const, its own value local typed &field, the field
// projection made static, and the interface call through the field's type made
// direct; the private field is skipped; a struct with no exported field skips
// the loop.
func TestMonoFieldsUnroll(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0, p1 := tab.Param(0), tab.Param(1)
	show := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "Show"), Name: w.in.Intern("Show"), Kind: types.KindInterface, Decl: &types.InterfaceDecl{}})
	showInt := w.fn("ShowInt", 0, []types.TypeID{tab.Ref(types.Int)}, types.Void)
	w.body(showInt).ret()
	showBool := w.fn("ShowBool", 0, []types.TypeID{tab.Ref(types.Bool)}, types.Void)
	w.body(showBool).ret()
	w.impls[implKey{show, 0, types.Int}] = showInt
	w.impls[implKey{show, 0, types.Bool}] = showBool
	field := func(name string, t types.TypeID, exp bool) types.FieldDecl {
		return types.FieldDecl{Name: w.in.Intern(name), Type: t, Exported: exp}
	}
	sItem := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "S"), Name: w.in.Intern("S"), Kind: types.KindStruct,
		Decl: &types.StructDecl{Fields: []types.FieldDecl{field("a", types.Int, true), field("hid", types.Int, false), field("b", types.Bool, true)}}})
	sType := tab.Named(sItem, nil)
	eItem := w.items.AddItem(&types.Item{Module: w.mod, Key: types.ItemKey("m", "E"), Name: w.in.Intern("E"), Kind: types.KindStruct,
		Decl: &types.StructDecl{Fields: []types.FieldDecl{field("x", types.Int, false)}}})

	dump := w.fn("Dump", 1, []types.TypeID{p0}, types.Void)
	gb := mir.NewBody(dump, w.span(), types.Void)
	gb.GenericCount = 2 // T, then the hidden field generic
	gb.AddParam(p0, w.in.Intern("x"), 0)
	name := gb.AddLocal(mir.Local{Type: types.Str, Kind: mir.LocalUser, Loop: mir.NoLoop})
	value := gb.AddLocal(mir.Local{Type: tab.Ref(p1), Kind: mir.LocalUser, Loop: mir.NoLoop})
	void := gb.AddLocal(mir.Local{Type: types.Void, Kind: mir.LocalTemp, Loop: mir.NoLoop})
	bb := blocks(gb, 6) // entry, header, body, latch, after call, exit
	gb.Loops = append(gb.Loops, mir.LoopInfo{Header: bb[1], Exit: bb[5], Holder: mir.NoLocal, FieldsOf: p0, FieldsParam: 1})
	w.gotoTerm(gb, bb[0], bb[1])
	w.gotoTerm(gb, bb[1], bb[2])
	w.stmt(gb, bb[2], gb.Place(name), mir.Rvalue{Kind: mir.RvFieldName, Field: mir.FieldRef{Loop: true, Index: 0}, Type: types.Str})
	fp := gb.Place(1, mir.Projection{Kind: mir.ProjField, FieldLoop: true, A: 0, Type: p1})
	w.stmt(gb, bb[2], gb.Place(value), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: fp, Type: tab.Ref(p1)})
	gb.Blocks[bb[2]].Term = mir.Terminator{Kind: mir.TermCall, Span: gb.AddSpan(w.span()), Spawn: mir.NoLocal,
		Callee: mir.Callee{Kind: mir.CallInterface, Iface: show, Method: 0, SelfType: p1},
		Args:   gb.AddArgs(arg(mir.Copy(gb.Place(value)))), Dest: gb.Place(void), Target: bb[4]}
	w.gotoTerm(gb, bb[3], bb[5])
	w.gotoTerm(gb, bb[4], bb[3])
	w.retTerm(gb, bb[5])
	w.bodies[dump] = gb
	if VisibleGenerics(gb) != 1 {
		t.Fatalf("visible generics %d", VisibleGenerics(gb))
	}

	build := func(a types.TypeID) *mir.Body {
		var ib *mir.Body
		if ie := source.Guard("t", source.Span{}, func() { ib = Instantiate(w, dump, []types.GenericArg{ty(a)}, Options{Validate: true}) }); ie != nil {
			t.Fatalf("Dump[%s]: %s", tab.TypeString(a, nil), ie)
		}
		return ib
	}
	ib := build(sType)
	pr := (&mir.Printer{Types: tab, Items: w.items}).Print(ib)
	if len(ib.Blocks) != 10 {
		t.Fatalf("want 10 blocks (entry, 2 copies of 4, exit):\n%s", pr)
	}
	var names []string
	var callees []types.ItemID
	var fields []uint32
	for _, blk := range ib.Blocks {
		for _, st := range blk.Stmts {
			if st.Rvalue.Kind == mir.RvUse && st.Rvalue.A.Kind == mir.OpConst {
				names = append(names, tab.ConstString(st.Rvalue.A.Const))
			}
			if st.Rvalue.Kind == mir.RvRef {
				pj := ib.Proj(st.Rvalue.Place)
				if len(pj) != 1 || pj[0].FieldLoop {
					t.Fatalf("field projection %v", pj)
				}
				fields = append(fields, pj[0].A)
				if want := tab.Ref(pj[0].Type); ib.Locals[st.Place.Local].Type != want {
					t.Fatalf("value local %s, want %s", tab.TypeString(ib.Locals[st.Place.Local].Type, nil), tab.TypeString(want, nil))
				}
			}
		}
		if blk.Term.Kind == mir.TermCall {
			if blk.Term.Callee.Kind != mir.CallDirect {
				t.Fatalf("interface call survived:\n%s", pr)
			}
			callees = append(callees, blk.Term.Callee.Fn.Item)
		}
	}
	if fmt.Sprint(names) != `["a" "b"]` || fmt.Sprint(fields) != "[0 2]" || len(callees) != 2 || callees[0] != showInt || callees[1] != showBool {
		t.Fatalf("names %v fields %v callees %v:\n%s", names, fields, callees, pr)
	}
	for i, l := range ib.Locals {
		if !closed(tab, l.Type) {
			t.Fatalf("local _%d keeps %s", i, tab.TypeString(l.Type, nil))
		}
	}
	// No exported field: the loop is skipped.
	eb := build(tab.Named(eItem, nil))
	if len(eb.Blocks) > 4 {
		t.Fatalf("Dump[E] keeps the loop:\n%s", (&mir.Printer{Types: tab, Items: w.items}).Print(eb))
	}
	if gb.Loops[0].FieldsOf != p0 || gb.Blocks[bb[2]].Term.Callee.Kind != mir.CallInterface {
		t.Fatal("the generic body was modified")
	}
}
