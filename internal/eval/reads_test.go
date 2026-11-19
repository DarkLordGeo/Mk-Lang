// Why: the incremental cache reruns an evaluation only when something it read
// changed, so every read must be recorded (doc/compiler.md, "Records").
// What: tests of the read record: bodies, values, layouts (also from a cached
// compiled body) and Target fields, with the provider's hashes.
// How: evaluations over a test world; the tests compare their ReadSets.

package eval

import (
	"slices"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

func TestReads(t *testing.T) {
	w := newWorld(t)
	pt, ptT := w.structType("lib", "Pt", types.Int, types.Int)
	lim := w.constItem("lib", "Limit", types.Int, 0)
	w.lowerVals[lim] = Const{Value: w.intVal(types.Int, 7)}
	tgt := w.constItem("std.core", "Target", types.Int, 0)
	w.lowerVals[tgt] = Const{Value: w.intVal(types.Int, 1)}
	w.target = tgt
	g := w.fn("lib", "G", types.Int)
	x := w.body(g, types.Int)
	p := x.local(ptT)
	x.assign(0, lp(p), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: pt, Ops: x.b.AddOperands(imm(types.Int, 1), imm(types.Int, 2))})
	x.assign(0, lp(0), use(mir.Copy(x.b.Place(p, mir.Projection{Kind: mir.ProjField, A: 1, Type: types.Int}))))
	x.ret(0)
	x.done()
	mk := func(name string) types.ItemID {
		k := w.constItem("main", name, types.Int, 0)
		y := w.body(k, types.Int)
		a, b, c := y.local(types.Int), y.local(types.Int), y.local(types.Int)
		b1 := y.block()
		y.assign(0, lp(a), mir.Rvalue{Kind: mir.RvConstRef, Item: lim})
		y.assign(0, lp(b), mir.Rvalue{Kind: mir.RvConstRef, Item: tgt})
		y.call(0, g, lp(c), b1)
		y.assign(b1, lp(0), bin(mir.Add, cp(a), cp(c)))
		y.ret(b1)
		y.done()
		return k
	}
	k1, k2 := mk("K1"), mk("K2")
	e := w.evaluator(0)
	want := func(k types.ItemID) []types.Read {
		_, lh := w.Layout(ptT)
		rs := []types.Read{
			{Kind: types.ReadValue, Key: w.items.Get(lim).Key, Hash: w.ConstHash(w.intVal(types.Int, 7))},
			{Kind: types.ReadBody, Key: w.items.Get(g).Key, Hash: w.bodies[g].Hash},
			{Kind: types.ReadBody, Key: w.items.Get(k).Key, Hash: w.bodies[k].Hash},
			{Kind: types.ReadLayout, Key: w.items.Get(pt).Key, Name: "", Hash: lh},
		}
		for _, f := range []types.TargetField{types.TargetArch, types.TargetOS, types.TargetPointerBytes, types.TargetVectorBits} {
			rs = append(rs, types.Read{Kind: types.ReadTarget, Key: w.items.Get(tgt).Key, Name: f.String(), Hash: w.TargetHash(f)})
		}
		slices.SortFunc(rs, types.Read.Compare)
		return rs
	}
	for _, k := range []types.ItemID{k1, k2} { // K2 enters G's cached body
		r := e.Eval(constReq(w, k, types.StageSignatures))
		if r.Err != nil || w.intOf(r.Value) != 9 {
			t.Fatalf("%s: %+v", w.items.Get(k).Key, r)
		}
		if got := r.Reads.Sorted(); !slices.Equal(got, want(k)) {
			t.Errorf("%s reads:\n got %v\nwant %v", w.items.Get(k).Key, got, want(k))
		}
	}
	// A slot hit returns the stored reads; the result's set is a copy.
	r := e.Eval(constReq(w, k1, types.StageSignatures))
	r.Reads.Add(types.Read{Kind: types.ReadFile, Name: "x", Hash: source.Hash{}})
	if r2 := e.Eval(constReq(w, k1, types.StageSignatures)); !slices.Equal(r2.Reads.Sorted(), want(k1)) {
		t.Errorf("a caller's edit reached the stored reads: %v", r2.Reads.Sorted())
	}
}
