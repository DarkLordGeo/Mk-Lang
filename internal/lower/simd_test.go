// Why: operators on std.simd vectors are intrinsic calls, not Mk code, and
// lane counts are constants once types are known.
// What: TestOperatorIsIntrinsicCall,
// TestCompoundAssignIsIntrinsicCall, TestLanesLowersToConstant,
// BenchmarkEvalVecOps.
// How: an operator on a std.simd Vec or Mask lowers to a direct call of the
// intrinsic check recorded, over the declaration file
// internal/check/testdata/simd/vec.mk added as std.simd.

package lower

import (
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"mk/internal/eval"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// TestOperatorIsIntrinsicCall: `a +% b`, `<<`, `~`, `==`, `!` and the
// float operators become calls of std.simd.Vec:AddWrap and the rest with
// the instance's arguments, Validate on; no RvBinary or RvUnary is left on
// a vector.
func TestOperatorIsIntrinsicCall(t *testing.T) {
	vec, err := os.ReadFile("../check/testdata/simd/vec.mk")
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, true)
	w.add("std.simd", opt{std: true}, string(vec)).wantClean(t)
	b := w.add("m", opt{}, `import std.simd

Ints(a: simd.Vec[u8, 16], k: u8): simd.Mask[u8, 16] {
	c := (a +% a) & ~a
	return !((c << k) == a)
}

Floats(f: simd.Vec[f64, 2]): simd.Vec[f64, 2] {
	return -f * f / f + f - f
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	ints, floats := l.print("Ints"), l.print("Floats")
	for _, want := range []string{"std.simd.Vec:AddWrap[u8, 16]", "std.simd.Vec:And[u8, 16]", "std.simd.Vec:Not[u8, 16]",
		"std.simd.Vec:Shl[u8, 16, u8]", "std.simd.Vec:Eq[u8, 16]", "std.simd.Mask:Not[u8, 16]"} {
		if !strings.Contains(ints, want) {
			t.Errorf("Ints lacks a call of %s:\n%s", want, ints)
		}
	}
	for _, want := range []string{"Vec:Neg[f64, 2]", "Vec:Mul[f64, 2]", "Vec:Div[f64, 2]", "Vec:Add[f64, 2]", "Vec:Sub[f64, 2]"} {
		if !strings.Contains(floats, want) {
			t.Errorf("Floats lacks a call of %s:\n%s", want, floats)
		}
	}
}

// TestCompoundAssignIsIntrinsicCall: `x +%= w` on a vector is
// x = Vec:AddWrap[T, N](copy x, w), and a shift's amount type is the third
// generic argument.
func TestCompoundAssignIsIntrinsicCall(t *testing.T) {
	vec, err := os.ReadFile("../check/testdata/simd/vec.mk")
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, true)
	w.add("std.simd", opt{std: true}, string(vec)).wantClean(t)
	b := w.add("m", opt{}, `import std.simd

Acc(a: simd.Vec[u8, 16], k: u8): simd.Vec[u8, 16] {
	var x := a
	x +%= a
	x <<= k
	x >>= k
	return x
}
`)
	b.wantClean(t)
	got := lowerModule(t, w, b).print("Acc")
	for _, want := range []string{"call std.simd.Vec:AddWrap[u8, 16](copy _3, copy _1)", "call std.simd.Vec:Shl[u8, 16, u8](copy _3, copy _2)", "call std.simd.Vec:Shr[u8, 16, u8](copy _3, copy _2)"} {
		if !strings.Contains(got, want) {
			t.Errorf("Acc lacks %s:\n%s", want, got)
		}
	}
}

// TestLanesLowersToConstant: simd.Lanes[T]() of a concrete lane type
// is the constant check folded, not a call; of a type parameter it stays a
// call of the intrinsic, answered per instance.
func TestLanesLowersToConstant(t *testing.T) {
	vec, err := os.ReadFile("../check/testdata/simd/vec.mk")
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, true)
	w.add("std.simd", opt{std: true}, string(vec)).wantClean(t)
	b := w.add("m", opt{}, `import std.simd

F32(): int {
	return simd.Lanes[f32]()
}

G[T: simd.Lane](): int {
	return simd.Lanes[T]()
}
`)
	b.wantClean(t)
	l := lowerModule(t, w, b)
	if f := l.print("F32"); strings.Contains(f, "Lanes") || !strings.Contains(f, "int 4") {
		t.Errorf("F32 is not the constant 4:\n%s", f)
	}
	if g := l.print("G"); !strings.Contains(g, "std.simd.Lanes[$0]") && !strings.Contains(g, "std.simd.Lanes[T]") {
		t.Errorf("G does not call simd.Lanes:\n%s", g)
	}
}

// benchProg serves eval the lowered bodies of several modules, by item,
// and check's layouts.
type benchProg struct {
	w      *world
	bodies map[types.ItemID]*mir.Body
}

func (p *benchProg) Body(fn types.ItemID, args []types.GenericArg) eval.Body {
	if b, ok := p.bodies[fn]; ok {
		return eval.Body{MIR: b, State: eval.BodyReady}
	}
	return eval.Body{State: eval.BodyMissing}
}
func (p *benchProg) Layout(t types.TypeID) (*types.Layout, source.Hash) {
	l := p.w.c.Layout(t)
	return &l, source.Hash{}
}
func (p *benchProg) LayoutName(args []types.GenericArg) string  { return "" }
func (p *benchProg) Const(item types.ItemID) eval.Const         { return eval.Const{} }
func (p *benchProg) ConstHash(c types.ConstID) source.Hash      { return source.Hash{} }
func (p *benchProg) TargetHash(f types.TargetField) source.Hash { return source.Hash{} }

// lowerInto lowers every unit of b into p (check diagnostics fail tb).
func (p *benchProg) lowerInto(tb testing.TB, b *built) map[string]types.ItemID {
	tb.Helper()
	keys := map[string]types.ItemID{}
	cfg := p.w.lowerCfg()
	for _, id := range b.m.Units() {
		body := p.w.c.Check(b.m, id)
		if len(body.Diags) > 0 {
			tb.Fatalf("%s: check diagnostics: %s", p.w.items.Get(id).Key, dump(p.w, body.Diags))
		}
		p.bodies[id] = Lower(cfg, FromCheck(cfg, p.w.c, body))
		keys[b.key(id)] = id
	}
	return keys
}

// laneClauseHolds is check's evaluator in BenchmarkEvalVecOps: every
// compile-time requires (std.simd's lane-count clause) is true, and any
// other request is refused.
type laneClauseHolds struct{ consts *types.ConstTable }

func (l laneClauseHolds) Eval(req *types.EvalRequest) types.EvalResult {
	if req.What != types.EvalContract {
		return types.EvalResult{Err: &types.EvalError{Kind: types.EvalRefused}}
	}
	return types.EvalResult{Value: l.consts.Intern(types.ConstValue{Type: types.Bool, Align: 1, Bytes: []byte{1}})}
}

// BenchmarkEvalVecOps: the evaluator over the
// reference's CountByte loop, 10,240 vector iterations (40 passes over
// 4,096 bytes, 256 loads, compares and CountSets each), lowered from source
// and run through the test-only edge lower -> eval. The intrinsics
// are called through fn values; each call is one intrinsic step of eval.
// Naming them judges their lane-count requires as a call does, and
// this harness has no mono to give check a real evaluator for it, so check
// gets laneClauseHolds, which answers each such clause true (16 lanes holds
// it). It reports; it gates nothing.
// go test ./internal/lower -run XXX -bench SY12_EvalVecOps
func BenchmarkEvalVecOps(b *testing.B) {
	vec, err := os.ReadFile("../check/testdata/simd/vec.mk")
	if err != nil {
		b.Fatal(err)
	}
	w := newWorld(b, true)
	w.cfg.Eval = laneClauseHolds{w.consts}
	p := &benchProg{w: w, bodies: map[types.ItemID]*mir.Body{}}
	ev := eval.New(eval.Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Program: p, Known: *w.cfg.Known})
	if ds := w.add("std.simd", opt{std: true}, string(vec)).diags(); len(ds) > 0 {
		b.Fatalf("std.simd: %s", dump(w, ds))
	}
	m := w.add("m", opt{}, `import std.simd

CountByte(xs: []u8, b: u8): int {
	splat := simd.Vec[u8, 16].Splat
	load := simd.Vec[u8, 16].FromSlice
	count := simd.Mask[u8, 16].CountSet
	var n := 0
	var i := 0
	needle := splat(b)
	for i + 16 <= xs.Len() {
		v := load(xs, i)
		n += count(v == needle)
		i += 16
	}
	return n
}

H(): int {
	var buf: [4096]u8 = [0; 4096]
	for j in 0..4096 {
		buf[j] = Trunc[u8](j *% 7)
	}
	var total := 0
	for r in 0..40 {
		total += CountByte(buf[..], Trunc[u8](r))
	}
	return total
}
`)
	if ds := m.diags(); len(ds) > 0 {
		b.Fatalf("diagnostics: %s", dump(w, ds))
	}
	h := p.lowerInto(b, m)["H"]
	req := &types.EvalRequest{What: types.EvalCall, Item: h, Fuel: 1 << 30, Stage: types.StageBodies, Module: m.id}
	r := ev.Eval(req)
	if r.Err != nil {
		b.Fatalf("evaluation: %+v", r.Err)
	}
	if got := binary.LittleEndian.Uint64(w.consts.Get(r.Value).Bytes); got != 4096*40/256 {
		b.Fatalf("H() = %d, want %d", got, 4096*40/256)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if r := ev.Eval(req); r.Err != nil {
			b.Fatal(r.Err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/10240, "ns/vector-iteration")
}
