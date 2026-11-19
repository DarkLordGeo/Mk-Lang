// Why: Win64 has no red zone and LLVM does not shrink-wrap under Win64 unwind
// information, so a function whose only call is a cold one keeps a prologue
// and an epilogue on its hot path. Trapping that call instead makes such a
// function (std.dyn's adapters, a key check and its Mismatch) a frameless leaf.
//
// What: the trapped call on x86_64-windows: trapTarget, trapsIn, trapCallee,
// trapCallable and trapCall pick and emit it; unitTraps and trapUnit say
// whether a unit has one; trapFormFacts hashes that into pack keys.
//
// How: a cold call of a Mk function that takes no parameter and never returns
// becomes prefetchnta <callee>(%rip) then ud1l 0x7f(%eax), and the runtime's
// vectored handler (std/sys/windows/fault.mk, TrapResume) reads the callee
// from the 7 bytes before the faulting pc and resumes into mk_trap_call, which
// makes the call. The details follow the imports.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// The form, two instructions LLVM emits from intrinsics, no inline asm:
//
//	prefetchnta <callee>(%rip)   0F 18 05 rel32   llvm.prefetch(callee, 0, 0, 1) nomerge
//	ud1l 0x7f(%eax), %eax        67 0F B9 40 7F   llvm.ubsantrap(127) nomerge
//
// A prefetch never faults and the ud1 always raises #UD
// (EXCEPTION_ILLEGAL_INSTRUCTION) at its first byte; the prefetch's rel32
// is relative to its end, the ud1's address, so the handler reads the
// callee from the 7 bytes before the faulting pc. The record lives in the
// instruction stream, so it survives inlining (each inlined copy carries
// its own), ThinLTO import (no inline asm: a function holding inline asm
// is not importable from a pack whose private FuncTable rows are in
// llvm.compiler.used), function sections, /opt:ref and /opt:icf (equal
// bytes and relocations, equal callee), with no table, section or bounds
// symbol. nomerge on both calls keeps LLVM from hoisting a prefetch two
// cold blocks share into their branch and from merging two traps.
//
// The handler takes the pair only when the callee is the entry of a Mk
// function (a kind-0 FuncTable row); any other illegal instruction goes on
// as it would without the handler. It resumes the thread at mk_trap_call
// (support_x64win.go, a support row of kind 2; std.sys.TrapEntry, an
// intrinsic, is its address) with r10 the callee and r11 the trap's address
// plus one; mk_trap_call writes a machine frame (.seh_pushframe) holding r11
// and the faulting rsp and calls r10, so RtlVirtualUnwind walks from the
// callee through mk_trap_call back to the function that trapped, and the
// panic report's backtrace (std/sys/windows/backtrace.mk) skips
// mk_trap_call's frame and steps over a frameless leaf as the system unwinds
// one. The callee's report, site, code and exit status are the call's; the
// backtrace keeps every frame the call's had and may show one more, the
// callee's own, which LLVM can inline into a call but not into a trap. A
// debugger sees a first-chance illegal-instruction exception before the
// callee runs; the call does not return, so its thread pays the exception
// once.
//
// Where: only in a function whose definition carries alwaysinline
// (smallfn.go's two marks, which LLVM inlines at every call whatever its
// cost), so the cheaper cold path changes no decision to inline that
// function itself (a call costs LLVM's inliner about 25 more than the two
// intrinsics); its callers, which inline it, do get that much cheaper. The
// same form for every failing check (prefetcht2 <site> before ud1l <code>,
// resumed into std.rt.PanicAt) is not used: in every function it makes LLVM
// inline check-heavy callees the PanicAt calls keep out of line (std.rt.Put
// into the panic report's writers), which grows the release hello past its
// size budget, and even limited to alwaysinline functions the callers it
// makes cheaper and the handler's code grow it.
//
// Which builds: the driver sets Config.TrapForm for x86_64-windows
// executables built optimized (release, checked, size), without --debug and
// not general-regs-only (internal/driver trapform.go). Dev builds, eval,
// every other target (Linux shrink-wraps already), libraries (their handler
// is installed late and removed at unload) and freestanding code keep the
// call. A unit with no trapped call (unitTraps) has no mk_trap_call,
// TrapEntry is null in it and TrapResume folds to false, so such a program
// (hello) carries none of this.

// trapCallCode is the ubsantrap immediate of a trapped call.
const trapCallCode = 0x7F

// symTrapCall is where the vectored handler resumes a trapped call: the
// support pack's module-assembly routine win64TrapCall (support_x64win.go),
// in a unit with a trapped call only (unitTraps).
const symTrapCall = "mk_trap_call"

// trapTarget reports whether the build may trap calls at all.
func (g *packGen) trapTarget() bool {
	return g.cfg.TrapForm && g.cfg.Target.OS == types.OSWindows && g.cfg.Target.Arch == types.ArchX64
}

// trapsIn reports whether Funcs[fi]'s calls may take the trapped form: a
// function whose definition carries alwaysinline.
func (g *packGen) trapsIn(fi int) bool {
	return g.trapTarget() && fi >= 0 && (g.smallFn(fi) || g.crossSole(fi))
}

// trapCallee reports whether a direct call of Funcs[j] with the lowered
// signature ls can be trapped: a Mk function with a body, not an extern or
// naked one, that returns never and takes no lowered parameter.
func (g *packGen) trapCallee(j int, ls *lsig) bool {
	if ls.result != types.Never || len(ls.ltypes) != 0 || ls.variadic {
		return false
	}
	f := &g.u.Funcs[j]
	if f.Body == nil || g.nakedFn(f) {
		return false
	}
	fd, ok := g.items.Get(f.Item).Decl.(*types.FuncDecl)
	return ok && !fd.Sig.Extern
}

// trapCallable reports whether the current function's direct call of
// Funcs[fi] with the lowered signature ls takes the trapped form.
func (fg *fnGen) trapCallable(fi int, ls *lsig) bool {
	return fg.g.trapsIn(fg.fi) && fg.g.trapCallee(fi, ls)
}

// trapCall ends the current block with the trapped form of a call of fn.
// A trap in a unit unitTraps says has none would have no mk_trap_call to
// resume at, so it is an internal error.
func (fg *fnGen) trapCall(fn *llvm.Function) {
	if !fg.g.unitTraps() {
		source.ICEf(fg.spanOf(fg.span), "codegen: a trapped call of %s in a unit with no trapped call", fn.Name())
	}
	m := fg.g.m
	i32 := m.I(32)
	// Both calls nomerge: SimplifyCFG hoists a prefetch two cold blocks
	// begin with into their branch, which would part a pair from its ud1
	// (TestTrapForm_PairsIntact), and BranchFolding merges equal traps.
	pf := m.IntrinsicFn("prefetch", m.Ptr())
	fg.cur.Call(pf.Type(), pf.Ref(), []llvm.Value{fn.Ref(), m.ConstInt(i32, 0), m.ConstInt(i32, 0), m.ConstInt(i32, 1)},
		nil, llvm.Attrs{}, llvm.Attrs{}.Flag("nomerge"))
	ut := m.IntrinsicFn("ubsantrap")
	fg.cur.Call(ut.Type(), ut.Ref(), []llvm.Value{m.ConstInt(m.I(8), trapCallCode)}, nil, llvm.Attrs{},
		llvm.Attrs{}.Flag("nomerge").Flag("noreturn").Flag("nounwind").Flag("cold"))
	fg.cur.Unreachable()
}

// unitTraps reports whether some function of the unit traps a call: then
// the support pack defines mk_trap_call and std.sys.TrapEntry is its
// address. A unit-wide read of the members' marks and their direct
// callees, so PackFacts hashes it into every pack's key; computed once per
// Unit (idxMu guards the memo; two packs computing it at once agree).
func (g *packGen) unitTraps() bool {
	if !g.trapTarget() {
		return false
	}
	u := g.u
	u.idxMu.Lock()
	if u.trapsN == len(u.Funcs)+1 {
		v := u.traps
		u.idxMu.Unlock()
		return v
	}
	u.idxMu.Unlock()
	v := false
	for fi := range u.Funcs {
		f := &u.Funcs[fi]
		if f.Body == nil || !g.trapsIn(fi) {
			continue
		}
		cg := &fnGen{g: g, f: f, fi: fi, b: f.Body}
		for bi := range f.Body.Blocks {
			t := &f.Body.Blocks[bi].Term
			if !isCall(t) || t.Callee.Kind != mir.CallDirect || t.Spawn != mir.NoLocal {
				continue
			}
			if fd, ok := g.items.Get(t.Callee.Fn.Item).Decl.(*types.FuncDecl); ok && fd.Intrinsic {
				continue
			}
			if j := cg.callIndex(t); g.trapCallee(j, g.funcSigOf(j)) {
				v = true
				break
			}
		}
		if v {
			break
		}
	}
	u.idxMu.Lock()
	u.traps, u.trapsN = v, len(u.Funcs)+1
	u.idxMu.Unlock()
	return v
}

// trapUnit is unitTraps outside a pack's emission (the support pack,
// PackFacts), over a scratch module for the lowered signatures.
func trapUnit(u *Unit, env Env, cfg *Config) bool {
	if !cfg.TrapForm {
		return false
	}
	return newPackGen(u, 0, env, cfg, llvm.NewModule("mk.trapform", cfg.triple(), cfg.DataLayout)).unitTraps()
}

// trapFormFacts hashes the setting, and whether the unit traps a call,
// into a pack key when it is on.
func trapFormFacts(u *Unit, env Env, cfg Config, h *source.Hasher) {
	if cfg.TrapForm {
		h.String("trapform")
		h.Bool(trapUnit(u, env, &cfg))
	}
}
