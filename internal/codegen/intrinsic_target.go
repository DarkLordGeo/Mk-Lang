// Why: std.raw's crypto intrinsics lower to one target instruction family
// and so need a cpu feature, which the calling function may not have.
//
// What: the registry (targetIntrinsics, registerTarget, targetCall), the
// feature test hasFeatures, featureOp (inline or through a helper), and
// the argument and result helpers the per-family files use.
//
// How: each unit (AES and carry-less multiply; SHA-256; SHA-1) registers its
// keys from its own file, so removing a unit is deleting its files.
//
// A call is expanded inline when the calling function's features (the
// target's baseline and its cpu clause) hold the intrinsic's; a call
// from any other function (one inside `unsafe`, whose reason names the
// Cpu() test) calls a private helper compiled with those features, which
// LLVM inlines wherever the caller's features allow and which never puts
// an instruction the caller's features lack into its body (instruction
// selection would refuse it).

package codegen

import (
	"slices"
	"strings"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// targetLowering lowers one target intrinsic call.
type targetLowering func(fg *fnGen, t *mir.Terminator, args []value)

// targetIntrinsics are the registered keys and their lowerings.
var targetIntrinsics = map[string]targetLowering{}

// registerTarget adds key, with its intrinsicSigs form sig, to the keys
// codegen implements. Called from init only.
func registerTarget(key, sig string, lower targetLowering) {
	targetIntrinsics[key] = lower
	intrinsicKeys = append(intrinsicKeys, key)
	intrinsicSigs[key] = sig
}

// targetCall lowers t when its key is a registered target intrinsic.
func (fg *fnGen) targetCall(key string, t *mir.Terminator, args []value) bool {
	lower, ok := targetIntrinsics[key]
	if ok {
		lower(fg, t, args)
	}
	return ok
}

// hasFeatures reports whether fg's function is compiled with every
// feature of need (Mk names): its baseline and its cpu set (cpuOf), as
// featuresOf gives them to LLVM. In a general-regs-only bin a function
// whose set lacks sse2 is compiled with none of them (groFeatures), so it
// has none (an aesenc expanded in a `cpu "aes"` function there crashed
// LLVM's backend).
func (fg *fnGen) hasFeatures(need []string) bool {
	g := fg.g
	have := map[string]bool{}
	for _, n := range types.CPUBaseline(g.cfg.Target) {
		have[n] = true
	}
	if fg.fi >= 0 {
		for _, n := range g.cpuOf(fg.fi) {
			have[n] = true
		}
	}
	if g.cfg.GeneralRegsOnly && !have["sse2"] {
		return false
	}
	set, _ := types.CPUClosure(g.cfg.Target.Arch, need)
	for _, n := range set {
		if !have[n] {
			return false
		}
	}
	return true
}

// featureOp is body over in, expanded in the current block when fg's
// function has need's features, else a call of the private helper
// mk.intr.<name> (one per pack) whose body is body over its inputs,
// compiled with need's features. The helper takes every input and gives
// its result through memory (pointers to the caller's temporaries): a
// vector passed in registers between functions of different features
// would not sit where both expect (a 256-bit value is one ymm register
// with AVX and two xmm without), which is why Mk passes a Vec by hidden
// pointer too.
func (fg *fnGen) featureOp(name string, need []string, ret llvm.Type, in []llvm.Value, body func(b *llvm.Block, p []llvm.Value) llvm.Value) llvm.Value {
	if fg.hasFeatures(need) {
		return body(fg.cur, in)
	}
	g, m := fg.g, fg.g.m
	if g.cfg.GeneralRegsOnly && fg.fi >= 0 && !slices.Contains(g.cpuOf(fg.fi), "sse2") {
		// check's E1106 `needs: sse2` (internal/check/raw_sse.go) refuses
		// the call; a helper here would put SSE code in a function the
		// bin promises none.
		source.ICEf(fg.spanOf(0), "codegen: %s called in a general-regs-only function without sse2", name)
	}
	ptr := m.Ptr()
	sym := "mk.intr." + name
	fn, ok := g.targetHelpers[sym]
	if !ok {
		params := make([]llvm.Type, len(in)+1)
		for i := range params {
			params[i] = ptr
		}
		set, _ := types.CPUClosure(g.cfg.Target.Arch, need)
		tf := g.targetFeatures(set)
		if g.cfg.GeneralRegsOnly {
			// Reached only from a function whose set holds sse2 (above):
			// compiled as that function is (featuresOf), never with the
			// x87 or MMX it lacks.
			sse, _ := types.CPUClosure(g.cfg.Target.Arch, append([]string{"sse2"}, need...))
			tf = "-x87,-mmx," + g.targetFeatures(sse)
		}
		attrs := withFeatures(g.fnAttrs(types.Void), tf).Flag("inlinehint")
		fn = m.AddFunction(sym, llvm.FuncSig{Linkage: llvm.Internal, Ret: m.Void(), Params: params, FnAttrs: attrs})
		b := fn.NewBlock("entry")
		ps := make([]llvm.Value, len(in))
		for i, v := range in {
			ps[i] = b.Load(v.Ty, fn.Param(i), 16)
		}
		b.Store(body(b, ps), fn.Param(len(in)), 16)
		b.Ret(nil)
		if g.targetHelpers == nil {
			g.targetHelpers = map[string]*llvm.Function{}
		}
		g.targetHelpers[sym] = fn
		g.extraFns = append(g.extraFns, fn)
	}
	args := make([]llvm.Value, len(in)+1)
	for i, v := range in {
		slot := fg.tempBytes(typeBytes(v.Ty), 16)
		fg.cur.Store(v, slot.v, 16)
		args[i] = slot.v
	}
	out := fg.tempBytes(typeBytes(ret), 16)
	args[len(in)] = out.v
	fg.cur.CallFunc(fn, args)
	return fg.cur.Load(ret, out.v, 16)
}

// typeBytes is the size of an integer or a vector of integers.
func typeBytes(t llvm.Type) uint64 {
	if t.IsVector() {
		return t.Len() * uint64(t.Elem(0).Bits()) / 8
	}
	return uint64(t.Bits()) / 8
}

// loadArg loads argument i, an array in memory (or a scalar of type ty),
// as ty.
func (fg *fnGen) loadArg(key string, t *mir.Terminator, args []value, i int, ty llvm.Type) llvm.Value {
	if i >= len(args) {
		source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s has %d arguments, its lowering reads %d", key, len(args), i+1)
	}
	v := args[i]
	if v.kind == rScalar {
		if v.v.Ty != ty {
			source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s argument %d is %s, its lowering reads %s", key, i, v.v.Ty, ty)
		}
		return v.v
	}
	bits := uint64(ty.Bits())
	if ty.IsVector() {
		bits = ty.Len() * uint64(ty.Elem(0).Bits())
	}
	if size, _ := fg.g.sizeAlign(v.t); size*8 != bits {
		source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s argument %d is %d bytes, its lowering reads %s", key, i, size, ty)
	}
	a := fg.addrOf(v)
	return fg.cur.Load(ty, a.v, a.align)
}

// storeAt stores x, a vector, to Dest at byte offset off; the caller
// continues with targetDone after the last store.
func (fg *fnGen) storeAt(t *mir.Terminator, x llvm.Value, off uint64) {
	d, _ := fg.placeAddr(t.Dest)
	fg.cur.Store(x, fg.gep(d.v, off), alignAt(d.align, off))
}

// targetDone continues at t's Target.
func (fg *fnGen) targetDone(t *mir.Terminator) { fg.cur.Br(fg.blocks[t.Target]) }

// archOf is the build's architecture.
func (fg *fnGen) archOf() types.Arch { return fg.g.cfg.Target.Arch }

// helperName is a target helper's name part for key ("std.raw.AesRound"
// gives "raw.AesRound").
func helperName(key string) string { return strings.TrimPrefix(key, "std.") }
