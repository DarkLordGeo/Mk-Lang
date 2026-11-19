// Why: a std.simd intrinsic named as a fn value must meet the same rules as
// a call of it: lane classes, the E1106 `needs` of a CPU feature, and its
// lane-count requires.
// What: TestFmaNeedsFeature, TestLanesConstInGeneric and
// BenchmarkCheckVecOps.
// How: checks fn values and calls over the std overlay and a fake evaluator
// that answers the lane-count clause (laneCountEval).

package check

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mk/internal/types"
)

// TestFmaNeedsFeature: Fma and the rounding methods named as fn
// values are judged as their calls are. Fma at a baseline without fma is
// E1106 `needs` on every x86_64 target; Floor is accepted at the hosted
// baseline (x86-64-v2 holds sse4.1) and is E1106 on x86_64-freestanding,
// whose baseline does not; inside a cpu clause that holds the feature both
// are accepted. The lane classes hold for values too: Floor over i32 lanes
// is E0701 `lanes`, over a T bounded only by Lane E0703. A value judges
// the method's lane-count requires as a call does, so the world has
// a fake evaluator answering it for N; the method-call forms are
// TestFeatureNeeds's, over the std overlay.
func TestFmaNeedsFeature(t *testing.T) {
	const fmaVal = `Use(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] {
	f := simd.Vec[f32, 4].Fma
	return f(v, v, v)
}
`
	const floorVal = `Use(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] {
	f := simd.Vec[f32, 4].Floor
	return f(v)
}
`
	const both = `Use(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] cpu "fma,sse4.1" {
	f := simd.Vec[f32, 4].Fma
	g := simd.Vec[f32, 4].Floor
	return g(f(v, v, v))
}
`
	const intLanes = `Use(v: simd.Vec[i32, 4]): simd.Vec[i32, 4] {
	f := simd.Vec[i32, 4].Floor
	return f(v)
}
`
	const generic = `Use[T: simd.Lane](v: simd.Vec[T, 4]): simd.Vec[T, 4] {
	f := simd.Vec[T, 4].Floor
	return f(v)
}
`
	const ownGenerics = `Use(v: simd.Vec[f32, 4]): simd.Vec[i32, 4] {
	f := simd.Vec[f32, 4].CastSat[i32]
	g := simd.Vec[f32, 4].RoundEven
	return f(g(v))
}
`
	for _, tc := range []struct {
		name   string
		target types.Target
		src    string
		want   string
	}{
		{"Fma value at the hosted baseline", types.X86_64Windows, fmaVal, "E1106@4:7"},
		{"Fma value at the linux baseline", types.X86_64Linux, fmaVal, "E1106@4:7"},
		{"Fma value at the freestanding baseline", types.X86_64Freestanding, fmaVal, "E1106@4:7"},
		{"Floor value at the hosted baseline", types.X86_64Windows, floorVal, ""},
		{"Floor value at the freestanding baseline", types.X86_64Freestanding, floorVal, "E1106@4:7"},
		{"both inside cpu fma,sse4.1, freestanding", types.X86_64Freestanding, both, ""},
		{"both inside cpu fma,sse4.1, hosted", types.X86_64Windows, both, ""},
		{"Floor value over i32 lanes", types.X86_64Windows, intLanes, "E0701@4:7"},
		{"Floor value over a Lane-bounded T", types.X86_64Windows, generic, "E0703@4:7"},
		{"Type[A].M[B] value and RoundEven, freestanding", types.X86_64Freestanding, ownGenerics, "E1106@5:7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := simdWorld(t)
			w.cfg.Target = tc.target
			laneCountEval(w)
			checkBody(t, w, tc.want, "import std.simd\n\n"+tc.src)
		})
	}
}

// laneCountEval installs a fake evaluator on w that answers a std.simd
// method's compile-time requires as the lane-count clause, `N == 2 || N ==
// 4 || ... || N == 64` over the generic argument N (slot 1), and true for a
// clause with no such argument.
func laneCountEval(w *world) {
	fe := w.withEval()
	fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
		if req.What != types.EvalContract {
			return types.EvalResult{}, false
		}
		v := uint64(1)
		if len(req.GenericArgs) > 1 && req.GenericArgs[1].IsConst {
			if n, ok := w.c.constInt(req.GenericArgs[1].Const.Value); ok && (n < 2 || n > 64 || n&(n-1) != 0) {
				v = 0
			}
		}
		return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
	}
}

// TestLanesConstInGeneric: simd.Lanes[T]() is the constant
// Target.vector_bits / (8 * SizeOf[T]()) for a concrete lane type, in a body
// (Body.Consts, read by lower) and as a const argument of a type (Vec[f32,
// simd.Lanes[f32]()] is Vec[f32, 4]); a bool is no lane (E0701). Of a type
// parameter it is a run-time value in a body, and as a const argument it is
// E0703 until types.ConstArg has a symbolic form (the rule that allows it
// waits for that contract kind).
func TestLanesConstInGeneric(t *testing.T) {
	t.Run("body constants", func(t *testing.T) {
		t.Parallel()
		w := simdWorld(t)
		b := checkBody(t, w, "", "import std.simd\n\nU8(): int {\n\treturn simd.Lanes[u8]()\n}\n\nF64(): int {\n\treturn simd.Lanes[f64]()\n}\n\nG[T: simd.Lane](): int {\n\treturn simd.Lanes[T]()\n}\n")
		for name, want := range map[string]int64{"U8": 16, "F64": 2} {
			body := w.c.Check(b.m, b.item(name))
			var got []int64
			for _, k := range body.Consts {
				if k == types.NoConst {
					continue
				}
				if v, ok := w.c.constInt(k); ok {
					got = append(got, v)
				}
			}
			if len(got) != 1 || got[0] != want {
				t.Errorf("%s: folded constants %v, want [%d]", name, got, want)
			}
			reads := false
			for _, r := range body.Reads.Sorted() {
				if r.Kind == types.ReadTarget && r.Name == "vector_bits" {
					reads = true
				}
			}
			if !reads {
				t.Errorf("%s: no read of Target.vector_bits", name)
			}
		}
		for _, k := range w.c.Check(b.m, b.item("G")).Consts {
			if k != types.NoConst {
				t.Errorf("G: simd.Lanes[T]() of a parameter folded to %s", w.tab.ConstString(k))
			}
		}
	})
	for _, tc := range []struct{ name, src, want string }{
		{"a const argument of a concrete lane", "V(v: simd.Vec[f32, simd.Lanes[f32]()]): simd.Vec[f32, 4] {\n\treturn v\n}\n", ""},
		{"the same type as Vec[f32, 4]", "V(v: simd.Vec[u16, simd.Lanes[u16]()]): simd.Vec[u16, 4] {\n\treturn v\n}\n", "E0301@4:9"},
		{"a const argument over a type parameter", "V[T: simd.Lane](v: simd.Vec[T, simd.Lanes[T]()]): int {\n\treturn 0\n}\n", "E0703@3:32"},
		{"bool is no lane, as a const argument", "V(v: [simd.Lanes[bool]()]u8): int {\n\treturn 0\n}\n", "E0701@3:18"},
		{"bool is no lane, in a body", "V(): int {\n\treturn simd.Lanes[bool]()\n}\n", "E0701@4:9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBody(t, simdWorld(t), tc.want, "import std.simd\n\n"+tc.src)
		})
	}
}

// vecOpsModule is a module importing std.simd with fns functions of 100
// vector expressions each: wrapping arithmetic, bitwise operators, shifts,
// compares and mask operators on Vec[u8, 16] and Vec[f32, 4] (operators
// judge no lane-count clause at a concrete N, so check needs no evaluator).
func vecOpsModule(fns int) string {
	var sb strings.Builder
	sb.WriteString("import std.simd\n\n")
	for f := 0; f < fns; f++ {
		fmt.Fprintf(&sb, "V%d(a: simd.Vec[u8, 16], b: simd.Vec[u8, 16], x: simd.Vec[f32, 4], k: u8): simd.Mask[u8, 16] {\n", f)
		sb.WriteString("\tvar c := a\n\tvar y := x\n\tvar m := a == b\n")
		for i := 0; i < 20; i++ {
			sb.WriteString("\tc = c +% b\n\tc = (c ^ a) & ~b\n\ty = y * x + y\n\tm = m | (c < b)\n\tc = c << k\n")
		}
		sb.WriteString("\treturn m\n}\n\n")
	}
	return sb.String()
}

// BenchmarkCheckVecOps: check over a generated
// module of 10,000 vector expressions (100 functions of 100), the module
// parsed and resolved outside the timer. It reports; it gates nothing.
// go test ./internal/check -run XXX -bench SY04_CheckVecOps
func BenchmarkCheckVecOps(b *testing.B) {
	src := vecOpsModule(100)
	vec := vecText(b)
	var total time.Duration
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		w := newWorld(b, true)
		if ds := w.add("std.simd", opt{std: true}, vec).diags(); len(ds) > 0 {
			b.Fatalf("std.simd: %s", dump(w, ds))
		}
		m := resolved(b, w, "m", src)
		b.StartTimer()
		start := time.Now()
		checkModule(w, m, 1)
		total += time.Since(start)
		if i == 0 {
			for _, id := range m.m.Units() {
				if ds := w.c.Check(m.m, id).Diags; len(ds) > 0 {
					b.Fatalf("diagnostics: %s", dump(w, ds))
				}
			}
		}
	}
	b.ReportMetric(10000*float64(b.N)/total.Seconds(), "exprs/s")
}
