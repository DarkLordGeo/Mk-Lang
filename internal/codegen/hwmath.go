// Why: std.math's rounding and fused multiply-add are written in Mk for
// every target, but a CPU that has the instruction should run the
// instruction, with exactly the same results.
//
// What: hwMathKeys (the std.math functions and their instructions) and
// hwMathCall, which emits the instruction in place of the call.
//
// How: a call of std.math's
// Floor, Ceil, RoundToZero, Round, RoundEven or Fma (and their F32 forms)
// from a function whose features hold the instruction is the instruction,
// inline, bit-identical to the software form the call would otherwise run:
// roundsd/roundss under sse4.1 (the hosted x86_64 baseline,
// x86-64-v2) and vfmadd under fma on x86_64, frint*/fmadd on aarch64,
// whose baseline has them. Bit-identity needs two things the instruction
// does not give by itself: a NaN result is math.Nan's bits (the software
// forms canonicalize NaNs), and Round (a half away from zero, which
// roundsd has no mode for) is trunc(x) plus one away from zero when
// |x - trunc(x)| >= 0.5, signed as x, which is exact since x - trunc(x)
// is. The std.math bodies stay the forms every other caller runs, the
// evaluator included (Fma's body itself runs the instruction where
// simd.Cpu() reports fma, and its software form elsewhere and in the
// evaluator); std/math/tests_hwmath.mk compares the two natively, and
// TestHardwareMathIR checks the inline IR.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

// hwMath is one std.math function's hardware form: its LLVM rounding
// family ("" for Round's own sequence, "fma" for Fma), its float width in
// bytes, and the x86_64 feature it needs.
type hwMath struct {
	op      string
	width   int
	feature string
}

var hwMathKeys = map[string]hwMath{
	"std.math.Floor": {"floor", 8, "sse4.1"}, "std.math.FloorF32": {"floor", 4, "sse4.1"},
	"std.math.Ceil": {"ceil", 8, "sse4.1"}, "std.math.CeilF32": {"ceil", 4, "sse4.1"},
	"std.math.RoundToZero": {"trunc", 8, "sse4.1"}, "std.math.RoundToZeroF32": {"trunc", 4, "sse4.1"},
	"std.math.RoundEven": {"roundeven", 8, "sse4.1"}, "std.math.RoundEvenF32": {"roundeven", 4, "sse4.1"},
	"std.math.Round": {"", 8, "sse4.1"}, "std.math.RoundF32": {"", 4, "sse4.1"},
	"std.math.Fma": {"fma", 8, "fma"}, "std.math.FmaF32": {"fma", 4, "fma"},
}

// hwMathCall lowers t inline when it calls one of hwMathKeys from a
// function whose features hold the instruction, reporting whether it did.
func (fg *fnGen) hwMathCall(t *mir.Terminator, args []value) bool {
	g := fg.g
	it := g.items.Get(t.Callee.Fn.Item)
	h, ok := hwMathKeys[it.Key.String()]
	if !ok {
		return false
	}
	switch g.cfg.Target.Arch {
	case types.ArchX64:
		if !fg.hasFeatures([]string{h.feature}) {
			return false
		}
	case types.ArchArm64:
	default:
		return false
	}
	m := g.m
	ft := m.Double()
	nan := m.ConstF64(types.CanonicalNaN64)
	if h.width == 4 {
		ft = m.Float()
		nan = m.ConstF32(types.CanonicalNaN32)
	}
	nargs := 1
	if h.op == "fma" {
		nargs = 3
	}
	if len(args) != nargs {
		return false
	}
	xs := make([]llvm.Value, nargs)
	for i := range xs {
		if args[i].kind != rScalar || args[i].v.Ty != ft {
			return false
		}
		xs[i] = args[i].v
	}
	var r llvm.Value
	switch h.op {
	case "":
		x := xs[0]
		tr := fg.cur.CallFunc(m.IntrinsicFn("trunc", ft), []llvm.Value{x})
		d := fg.cur.CallFunc(m.IntrinsicFn("fabs", ft), []llvm.Value{fg.cur.Bin(llvm.FSub, x, tr, 0)})
		half, one, zero := m.ConstF64(0x3fe0000000000000), m.ConstF64(0x3ff0000000000000), m.ConstF64(0)
		if h.width == 4 {
			half, one, zero = m.ConstF32(0x3f000000), m.ConstF32(0x3f800000), m.ConstF32(0)
		}
		up := fg.cur.Select(fg.cur.FCmp(llvm.FOGE, d, half), one, zero)
		r = fg.cur.Bin(llvm.FAdd, fg.cur.CallFunc(m.IntrinsicFn("fabs", ft), []llvm.Value{tr}), up, 0)
		r = fg.cur.CallFunc(m.IntrinsicFn("copysign", ft), []llvm.Value{r, x})
	default:
		r = fg.cur.CallFunc(m.IntrinsicFn(h.op, ft), xs)
	}
	r = fg.cur.Select(fg.cur.FCmp(llvm.FUNO, r, r), nan, r)
	destT := fg.b.PlaceType(t.Dest)
	d, _ := fg.placeAddr(t.Dest)
	fg.store(d, value{t: destT, kind: rScalar, v: r})
	fg.cur.Br(fg.blocks[t.Target])
	return true
}
