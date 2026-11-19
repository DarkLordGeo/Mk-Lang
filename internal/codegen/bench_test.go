// Why: EmitPack's throughput bounds how fast a large pack compiles.
// What: BenchmarkEmitPack, MIR operations per second of EmitPack on a pack of
// about 20,000 operations, and TestBenchUnitValid for its input.
// How: benchUnit chains generated functions of checked arithmetic, compares,
// branches, calls and aggregates until the pack reaches the operation count.

package codegen

import (
	"fmt"
	"io"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// benchUnit is one pack of functions K0..Kn-1, each
//
//	Kn(a: int, b: int): int {
//	    x1 := a + b; x2 := x1 * 3; x3 := x2 / b
//	    if x3 < a { x4 = x3 - a } else { x4 = x3 ^ b }
//	    p := Pair(x4, a); x5 := p.f0 + p.f1
//	    return K(n-1)(x5, b)            (n > 0; K0 returns x5)
//	}
//
// until the pack holds at least ops MIR operations; it returns the unit and
// the exact count.
func benchUnit(e *testEnv, ops int) (*Unit, int) {
	pair := e.structT("Pair", types.Int, types.Int)
	var bodies []*mir.Body
	total := 0
	for n := 0; total < ops; n++ {
		f := e.body(e.fn(fmt.Sprintf("K%d", n), []types.TypeID{types.Int, types.Int}, types.Int, 0))
		x1, x2, x3, x4, x5 := f.local(types.Int), f.local(types.Int), f.local(types.Int), f.local(types.Int), f.local(types.Int)
		c, p := f.local(types.Bool), f.local(pair)
		f.assign(place(x1), bin(mir.Add, mir.Copy(place(1)), mir.Copy(place(2))), f.at(2, 5))
		f.assign(place(x2), bin(mir.Mul, mir.Copy(place(x1)), mir.Imm(types.Int, 3)), f.at(2, 20))
		f.assign(place(x3), bin(mir.Div, mir.Copy(place(x2)), mir.Copy(place(2))), f.at(2, 35))
		f.assign(place(c), bin(mir.Lt, mir.Copy(place(x3)), mir.Copy(place(1))), f.at(3, 5))
		yes, no, join := f.newBlock(), f.newBlock(), f.newBlock()
		f.term(mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(place(c)), Cases: f.b.AddCases(
			mir.SwitchCase{Value: 1, Target: yes}, mir.SwitchCase{Value: 0, Target: no}), Default: mir.NoBlock, Span: f.at(3, 5)})
		f.cur = yes
		f.assign(place(x4), bin(mir.Sub, mir.Copy(place(x3)), mir.Copy(place(1))), f.at(3, 20))
		f.term(mir.Terminator{Kind: mir.TermGoto, Target: join, Span: f.at(3, 20)})
		f.cur = no
		f.assign(place(x4), bin(mir.BitXor, mir.Copy(place(x3)), mir.Copy(place(2))), f.at(3, 40))
		f.term(mir.Terminator{Kind: mir.TermGoto, Target: join, Span: f.at(3, 40)})
		f.cur = join
		f.assign(place(p), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: e.tt.Get(pair).Item,
			Ops: f.b.AddOperands(mir.Copy(place(x4)), mir.Copy(place(1)))}, f.at(4, 5))
		f0 := f.b.Place(p, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int})
		f1 := f.b.Place(p, mir.Projection{Kind: mir.ProjField, A: 1, Type: types.Int})
		f.assign(place(x5), bin(mir.Add, mir.Copy(f0), mir.Copy(f1)), f.at(4, 20))
		if n > 0 {
			f.callTo(direct(bodies[n-1].Item), place(0), mir.NoLocal, f.at(5, 5), mir.Copy(place(x5)), mir.Copy(place(2)))
		} else {
			f.assign(place(0), use(mir.Copy(place(x5))), f.at(5, 5))
		}
		f.ret(f.at(5, 5))
		for _, blk := range f.b.Blocks {
			total += len(blk.Stmts) + 1
		}
		bodies = append(bodies, f.b)
	}
	return unitOf(e, bodies), total
}

// BenchmarkEmitPack: go test ./internal/codegen -run XXX -bench EmitPack.
func BenchmarkEmitPack(b *testing.B) {
	for _, prof := range []types.Profile{types.Dev, types.Release} {
		b.Run(map[types.Profile]string{types.Dev: "dev", types.Release: "release"}[prof], func(b *testing.B) {
			e := newTestEnv()
			u, ops := benchUnit(e, 20000)
			cfg := Config{Target: types.X86_64Windows, Profile: prof, CPU: "x86-64-v2"}
			if _, err := EmitPack(u, 0, e, cfg, io.Discard); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := EmitPack(u, 0, e, cfg, io.Discard); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(ops)*float64(b.N)/b.Elapsed().Seconds(), "ops/s")
			b.ReportMetric(float64(ops), "ops/pack")
		})
	}
}

// TestBenchUnitValid: the benchmark's pack validates, emits and compiles,
// so the number measures a real pack.
func TestBenchUnitValid(t *testing.T) {
	e := newTestEnv()
	u, ops := benchUnit(e, 2000)
	if ops < 2000 {
		t.Fatalf("%d operations", ops)
	}
	ll := emit(t, e, u, types.X86_64Windows, types.Dev)
	clangC(t, types.X86_64Windows, t.TempDir(), "pack", ll, "-O0")
}
