// Why: Mk links no C runtime, so what one would provide (memcpy and its
// kin, the process entry, the task switch, stack probes, weak hook
// defaults) is emitted by the compiler itself, once per build, in the
// support pack.
//
// What: emitSupport, memFunctions (memcpy, memmove, memset, memcmp, bcmp),
// targetRoutines and asmFunc (module-assembly routines per target), entry
// (the entry stub), initContext, freeHooks and yieldHook, and the frame
// layout of a task context (ctxFrame, frameOf).
//
// How: the memory functions are LLVM IR using general registers only,
// compiled at -O2 in every profile; the target routines are module
// assembly with their unwind directives. The per-target assembly lives in
// support_x64win.go, support_x64linux.go, support_aarch64.go and
// support_nutshell.go.

package codegen

import (
	"io"
	"strings"

	"mk/internal/llvm"
	"mk/internal/source"
	"mk/internal/types"
)

// Support routine symbols. They are external with default visibility in
// every profile; the std intrinsics that reach them are lowered to calls of
// these names.
const (
	symMemcpy  = "memcpy"
	symMemmove = "memmove"
	symMemset  = "memset"
	symMemcmp  = "memcmp"
	symBcmp    = "bcmp"
	symChkstk  = "__chkstk"
	symFltused = "_fltused"
	symStart   = "mk_start"
	// symMachOMain is the macOS entry dyld calls through LC_MAIN, _main
	// in the object (Mach-O prefixes '_').
	symMachOMain   = "main"
	symELFStart    = "_start"
	symSwitch      = "mk_switch"
	symTaskStart   = "mk_task_start"
	symClone       = "mk_clone"
	symSigreturn   = "mk_sigreturn"
	symCallSystem  = "mk_call_system"
	symInitContext = "mk.support.InitContext"
	// symTaskStartHook is the std function the entry stub calls:
	// std.task.Start(sp: *u8): never.
	symTaskStartHook = "mk.std.task.Start"
	// The freestanding hooks a program may define with export extern;
	// the support pack defines each as
	// a weak default on x86_64-freestanding only (freeHooks).
	symPanicHook = "MkPanicHook"
	symPageAlloc = "MkPageAlloc"
	symPageFree  = "MkPageFree"
	// symThreadYield is the yield hook of std.atomic's Spin: a weak
	// default on every target (yieldHook), which hosted
	// std.rt's export extern replaces.
	symThreadYield = "MkThreadYield"
	// symRepMovsb and symRepStosb name memcpy's and memset's System V
	// x86_64 paths from ermsMin bytes, the keys of ermsInline.
	symRepMovsb = "mk_rep_movsb"
	symRepStosb = "mk_rep_stosb"
)

// Frame layout of a task context: the bytes mk_switch leaves at
// Context[0] and the offsets InitContext writes. frame is the distance from
// the 16-aligned top to ctx[0]; the others are offsets from ctx[0].
type ctxFrame struct {
	frame uint64
	entry uint64 // r12 / x19
	arg   uint64 // r13 / x20
	ret   uint64 // the return address slot (x30 on aarch64)
	mxcsr int64  // -1 when the target has none
	fpcw  int64
	fpcr  int64
}

func frameOf(t types.Target) ctxFrame {
	switch {
	case t.Arch == types.ArchArm64:
		return ctxFrame{frame: 176, entry: 0, arg: 8, ret: 88, mxcsr: -1, fpcw: -1, fpcr: 160}
	case t.OS == types.OSWindows:
		// 240 bytes mk_switch pops, then 16 zero bytes: the return
		// address an unwinder reads above mk_task_start's frame is 0, the
		// end of a walk, as Linux's .cfi_undefined.
		return ctxFrame{frame: 256, entry: 192, arg: 184, ret: 232, mxcsr: 160, fpcw: 164, fpcr: -1}
	default:
		return ctxFrame{frame: 64, entry: 32, arg: 24, ret: 56, mxcsr: 0, fpcw: 4, fpcr: -1}
	}
}

// support builds the support pack's module.
type support struct {
	m   *llvm.Module
	cfg *Config
	t   types.Target
	asm strings.Builder
	// defs are the functions defined in IR and asmNames those defined in
	// module assembly, in creation order: FuncTable's support rows.
	defs     []*llvm.Function
	asmNames []string
	// traps is whether the unit traps a call (trapform.go unitTraps):
	// mk_trap_call is emitted only then.
	traps bool
}

func emitSupport(u *Unit, env Env, cfg *Config, w io.Writer) error {
	var werr error
	err := guard("support pack", source.Span{}, func() {
		s := &support{m: llvm.NewModule("mk.support", cfg.triple(), cfg.DataLayout), cfg: cfg, t: cfg.Target}
		if env != nil {
			s.traps = trapUnit(u, env, cfg)
		}
		s.memFunctions()
		s.tiHelpers()
		s.targetRoutines()
		if s.t.OS == types.OSFreestanding {
			s.freeHooks() // no tasks, no entry stub: the program's MkEntry
		} else {
			s.initContext()
		}
		s.entry()
		if env != nil {
			s.intrinsicBodies(u, env)
		}
		if cfg.Library {
			s.libEntries() // exports.go
		} else if env != nil {
			s.imageBaseStub(u, env) // exports.go
		}
		s.yieldHook() // last: s.defs feeds the function table's support rows
		if s.asm.Len() > 0 {
			s.m.ModuleAsm(strings.TrimRight(s.asm.String(), "\n"))
		}
		_, werr = s.m.WriteTo(w)
	})
	if err != nil {
		return err
	}
	return werr
}

// fnAttrs are the attributes of every support function written in IR; in a
// general-regs-only bin they hold its features (groAttrs). On
// Windows they hold uwtable: clang's memset saves rsi and rdi and
// its memcmp rsi, callee-saved on Win64, so the system's unwinder needs
// their entries to restore them for a C source's __except around a fault
// inside one; the fault walk passes a memory function's row (rowMem) at
// frame 0 on to its caller (std/sys/windows/fault.mk, FaultOwner).
//
// Every hosted target's support
// functions carry uwtable, so a walk (the backtrace, a debugger) unwinds
// through them on Linux too; freestanding keeps none (no unwinder, and a
// kernel may not want .eh_frame). They also carry the frame-pointer and
// size attributes a Mk function does under --frame-pointers and --profile
// size (fpSizeAttrs).
func (s *support) fnAttrs() llvm.Attrs {
	a := llvm.Attrs{}.Flag("nounwind")
	if s.t.OS != types.OSFreestanding {
		a = a.Flag("uwtable")
	}
	return fpSizeAttrs(groAttrs(noRedZone(cpuAttrs(a, s.cfg.CPU), s.t), s.cfg), s.cfg)
}

// noRedZone adds noredzone on x86_64-freestanding: an interrupt pushes its
// frame below the interrupted code's stack
// pointer, so no function may keep data there. clang's -mno-red-zone does
// not reach IR input (clang 21.1.8), so every
// definition carries the attribute.
func noRedZone(a llvm.Attrs, t types.Target) llvm.Attrs {
	if t.OS == types.OSFreestanding {
		return a.Flag("noredzone")
	}
	return a
}

// freeHooks emits the weak defaults of the freestanding hooks, default
// visibility so a program's export extern definition wins
// at link time: MkPanicHook(info) traps (llvm.trap, ud2 on x86_64);
// MkPageAlloc(size, align) returns null, so a Heap then fails with
// OutOfMemory; MkPageFree(p, size, align) does nothing.
func (s *support) freeHooks() {
	m := s.m
	ptr, i64 := m.Ptr(), m.I(64)
	weak := func(name string, ret llvm.Type, params []llvm.Type, attrs llvm.Attrs) *llvm.Function {
		pa := make([]llvm.Attrs, len(params))
		for i := range params {
			pa[i] = llvm.Attrs{}.Flag("noundef")
		}
		f := m.AddFunction(name, llvm.FuncSig{Linkage: llvm.Weak, DSOLocal: true, Ret: ret,
			Params: params, ParamAttrs: pa, FnAttrs: attrs})
		s.defs = append(s.defs, f)
		return f
	}
	f := weak(symPanicHook, m.Void(), []llvm.Type{ptr}, s.fnAttrs().Flag("noreturn").Flag("cold"))
	b := f.NewBlock("entry")
	b.CallFunc(m.IntrinsicFn("trap"), nil)
	b.Unreachable()
	f = weak(symPageAlloc, ptr, []llvm.Type{i64, i64}, s.fnAttrs())
	b = f.NewBlock("entry")
	null := m.Null()
	b.Ret(&null)
	f = weak(symPageFree, m.Void(), []llvm.Type{ptr, i64, i64}, s.fnAttrs())
	b = f.NewBlock("entry")
	b.Ret(nil)
}

// yieldHook emits the weak default of MkThreadYield, the yield hook that
// std.atomic's Spin calls after 64 failed tries, through std.core's
// SpinLock, with no import (as the other weak hooks): one spin-wait
// hint (x86_64 pause, aarch64 yield) and nothing else, on every target.
// Hosted std.rt exports the real one (SwitchToThread on Windows,
// sched_yield on Linux), which wins at link time over this weak definition;
// a freestanding program only pauses unless it exports its own, since the
// support pack cannot know whether a thread scheduler exists there.
func (s *support) yieldHook() {
	m := s.m
	f := m.AddFunction(symThreadYield, llvm.FuncSig{Linkage: llvm.Weak, DSOLocal: true, Ret: m.Void(), FnAttrs: s.fnAttrs()})
	s.defs = append(s.defs, f)
	b := f.NewBlock("entry")
	hint := "pause"
	if s.t.Arch == types.ArchArm64 {
		hint = "yield"
	}
	b.InlineAsm(m.FuncType(m.Void(), nil, false), hint, "~{memory}", true, nil)
	b.Ret(nil)
}

// define adds an external, default-visibility support function.
func (s *support) define(name string, ret llvm.Type, params []llvm.Type, attrs llvm.Attrs) *llvm.Function {
	pa := make([]llvm.Attrs, len(params))
	for i, p := range params {
		if p.IsPtr() || p.IsInt() {
			pa[i] = llvm.Attrs{}.Flag("noundef")
		}
	}
	f := s.m.AddFunction(name, llvm.FuncSig{Linkage: llvm.External, DSOLocal: true, Ret: ret,
		Params: params, ParamAttrs: pa, FnAttrs: attrs})
	s.defs = append(s.defs, f)
	return f
}

// counter is an i64 loop variable in memory; clang -O2 promotes it.
type counter struct {
	slot llvm.Value
	i64  llvm.Type
}

func newCounter(b *llvm.Block, m *llvm.Module, init llvm.Value) counter {
	c := counter{slot: b.Alloca(m.I(64), 8), i64: m.I(64)}
	b.Store(init, c.slot, 8)
	return c
}

func (c counter) get(b *llvm.Block) llvm.Value    { return b.Load(c.i64, c.slot, 8) }
func (c counter) set(b *llvm.Block, v llvm.Value) { b.Store(v, c.slot, 8) }

// ermsMin is the length from which memcpy and memset use rep movsb and rep
// stosb on System V x86_64: the default of glibc's
// x86_rep_movsb_threshold and x86_rep_stosb_threshold for a 16-byte vector
// size (glibc's sysdeps/x86/dl-tunables.list and cpu-features.c), not a
// measurement of Mk's functions. On a CPU with ERMS (cpuid leaf 7, EBX bit
// 9) the microcode copies in wide steps where the general-register loop
// moves 8 bytes at a time (general-regs-only keeps the loop off vector
// registers); on
// one without, the instructions are slower but still correct.
//
// Win64 has neither. There rdi and rsi are callee-saved, so the string
// instructions need code that saves and restores them, and no form of it
// is right yet. An inline asm in memcpy made the optimized memcpy push both
// at entry on every call (LLVM does not shrink-wrap under Windows CFI). A
// module-asm routine that parks them in the volatile r9 and r10 never moves
// rsp, but it has no unwind data, so the unwinder takes it for a leaf that
// changed no callee-saved register: a C source's __except around a memcpy
// that faults inside it resumes with the routine's rdi and rsi. A routine
// that pushes them under .seh_pushreg has an unwind
// entry and starts at a function-table row; FaultOwner
// (std/sys/windows/fault.mk) passes a memory function's row (rowMem) at
// frame 0 on to its caller, so that form, with
// such a row, is possible but not built or measured. The IR support functions, where clang
// -O2 makes one save a callee-saved register (memset pushes rsi and rdi,
// memcmp rsi), have unwind entries on Win64 (fnAttrs).
const ermsMin = 2048

// ermsInline are the System V inline asm strings and constraints of the
// two operations over memcpy's (d, s, n) and memset's (d, c, n) parameters
// in order: tied outputs for the registers the instruction changes, c's
// low byte in al. On System V rdi, rsi and rcx are volatile.
var ermsInline = map[string][2]string{
	symRepMovsb: {"cld\n\trep movsb", "={di},={si},={cx},0,1,2,~{memory},~{dirflag},~{fpsr},~{flags}"},
	symRepStosb: {"cld\n\trep stosb", "={di},={cx},0,{ax},1,~{memory},~{dirflag},~{fpsr},~{flags}"},
}

// memFunctions emits memcpy, memmove, memset, memcmp and bcmp as IR
// marked "no-builtins", so LLVM cannot turn their loops back into calls of
// themselves, and keeps them in llvm.used.
func (s *support) memFunctions() {
	m := s.m
	ptr, i64, i32, i8 := m.Ptr(), m.I(64), m.I(32), m.I(8)
	attrs := s.fnAttrs().Str("no-builtins", "")
	c8 := m.ConstInt(i64, 8)
	c1 := m.ConstInt(i64, 1)
	zero := m.ConstInt(i64, 0)

	// at is the address p+k for a constant k, or p+n-k when fromEnd.
	at := func(b *llvm.Block, p, n llvm.Value, k int64, fromEnd bool) llvm.Value {
		if !fromEnd {
			if k == 0 {
				return p
			}
			return b.GEPBytes(p, m.ConstInt(i64, uint64(k)), false)
		}
		return b.GEPBytes(p, b.Bin(llvm.Sub, n, m.ConstInt(i64, uint64(k)), 0), false)
	}
	// moveWords copies the w-byte words at offsets offs of src to d, all
	// loads before any store, so it is right for overlapping ranges.
	type off struct {
		k       int64
		fromEnd bool
	}
	moveWords := func(b *llvm.Block, d, src, n llvm.Value, t llvm.Type, offs []off) {
		vs := make([]llvm.Value, len(offs))
		for i, o := range offs {
			vs[i] = b.Load(t, at(b, src, n, o.k, o.fromEnd), 1)
		}
		for i, o := range offs {
			b.Store(vs[i], at(b, d, n, o.k, o.fromEnd), 1)
		}
	}
	// sizeClasses copies n <= 64 bytes in its size class: a head and
	// an overlapping tail of one word size each, all loaded before any
	// store, so the branches depend on the class, not on n's exact value,
	// and the copy is right for overlapping ranges (memmove). General
	// registers only (i64 at most), as general-regs-only needs. n > 64
	// branches to large.
	sizeClasses := func(f *llvm.Function, from *llvm.Block, d, src, n llvm.Value, pfx string, done, large *llvm.Block) {
		cmp := func(b *llvm.Block, max int64, t, e *llvm.Block) {
			b.CondBr(b.ICmp(llvm.IULE, n, m.ConstInt(i64, uint64(max))), t, e)
		}
		upTo16, upTo32, upTo64 := f.NewBlock(pfx+"le16"), f.NewBlock(pfx+"le32"), f.NewBlock(pfx+"le64")
		over16, over32 := f.NewBlock(pfx+"gt16"), f.NewBlock(pfx+"gt32")
		cmp(from, 16, upTo16, over16)
		cmp(over16, 32, upTo32, over32)
		cmp(over32, 64, upTo64, large)
		w8, lt8, w4, lt4, w1 := f.NewBlock(pfx+"w8"), f.NewBlock(pfx+"lt8"), f.NewBlock(pfx+"w4"), f.NewBlock(pfx+"lt4"), f.NewBlock(pfx+"w1")
		upTo16.CondBr(upTo16.ICmp(llvm.IUGE, n, c8), w8, lt8)
		moveWords(w8, d, src, n, i64, []off{{0, false}, {8, true}})
		w8.Br(done)
		lt8.CondBr(lt8.ICmp(llvm.IUGE, n, m.ConstInt(i64, 4)), w4, lt4)
		moveWords(w4, d, src, n, i32, []off{{0, false}, {4, true}})
		w4.Br(done)
		lt4.CondBr(lt4.ICmp(llvm.IEQ, n, zero), done, w1)
		// 1 to 3 bytes: the first, the middle and the last.
		h := w1.Bin(llvm.LShr, n, c1, 0)
		a, b2, c := w1.Load(i8, src, 1), w1.Load(i8, w1.GEPBytes(src, h, false), 1), w1.Load(i8, at(w1, src, n, 1, true), 1)
		w1.Store(a, d, 1)
		w1.Store(b2, w1.GEPBytes(d, h, false), 1)
		w1.Store(c, at(w1, d, n, 1, true), 1)
		w1.Br(done)
		moveWords(upTo32, d, src, n, i64, []off{{0, false}, {8, false}, {16, true}, {8, true}})
		upTo32.Br(done)
		moveWords(upTo64, d, src, n, i64, []off{{0, false}, {8, false}, {16, false}, {24, false}, {32, true}, {24, true}, {16, true}, {8, true}})
		upTo64.Br(done)
	}
	// copyForward copies n > 64 bytes from s to d forward in 32-byte
	// blocks, then the last 32 bytes, loaded before the loop, as one
	// overlapping block: one loop exit per call, not a word loop and a byte
	// loop of varying trip counts. Right for d <= s overlapping too:
	// each block is loaded before it is stored, a store never reaches a
	// later block's source, and the tail is read before any store.
	copyForward := func(f *llvm.Function, from *llvm.Block, d, src, n llvm.Value, pfx string, done *llvm.Block) {
		tail := []off{{32, true}, {24, true}, {16, true}, {8, true}}
		tv := make([]llvm.Value, len(tail))
		for i, o := range tail {
			tv[i] = from.Load(i64, at(from, src, n, o.k, o.fromEnd), 1)
		}
		i := newCounter(from, m, zero)
		w := f.NewBlock(pfx + "w")
		wb := f.NewBlock(pfx + "wb")
		t := f.NewBlock(pfx + "t")
		from.Br(w)
		iv := i.get(w)
		left := w.Bin(llvm.Sub, n, iv, 0)
		w.CondBr(w.ICmp(llvm.IUGT, left, m.ConstInt(i64, 32)), wb, t)
		iv = i.get(wb)
		ps, pd := wb.GEPBytes(src, iv, false), wb.GEPBytes(d, iv, false)
		var vs [4]llvm.Value
		for k := range vs {
			vs[k] = wb.Load(i64, at(wb, ps, n, int64(8*k), false), 1)
		}
		for k := range vs {
			wb.Store(vs[k], at(wb, pd, n, int64(8*k), false), 1)
		}
		i.set(wb, wb.Bin(llvm.Add, iv, m.ConstInt(i64, 32), 0))
		wb.Br(w)
		for k, o := range tail {
			t.Store(tv[k], at(t, d, n, o.k, o.fromEnd), 1)
		}
		t.Br(done)
	}

	// copyBackward is copyForward's mirror for memmove's d > s overlap:
	// 32-byte blocks from the end, then the first 32 bytes,
	// loaded before the loop, as one overlapping block. A block is loaded
	// before it is stored, a store lands above every block still to be
	// read (d > s, blocks are 32 bytes apart), and the head is read before
	// any store.
	copyBackward := func(f *llvm.Function, from *llvm.Block, d, src, n llvm.Value, pfx string, done *llvm.Block) {
		var hv [4]llvm.Value
		for k := range hv {
			hv[k] = from.Load(i64, at(from, src, n, int64(8*k), false), 1)
		}
		j := newCounter(from, m, n)
		w := f.NewBlock(pfx + "w")
		wb := f.NewBlock(pfx + "wb")
		t := f.NewBlock(pfx + "t")
		from.Br(w)
		jv := j.get(w)
		w.CondBr(w.ICmp(llvm.IUGT, jv, m.ConstInt(i64, 32)), wb, t)
		jv = wb.Bin(llvm.Sub, j.get(wb), m.ConstInt(i64, 32), 0)
		ps, pd := wb.GEPBytes(src, jv, false), wb.GEPBytes(d, jv, false)
		var vs [4]llvm.Value
		for k := range vs {
			vs[k] = wb.Load(i64, at(wb, ps, n, int64(8*k), false), 1)
		}
		for k := range vs {
			wb.Store(vs[k], at(wb, pd, n, int64(8*k), false), 1)
		}
		j.set(wb, jv)
		wb.Br(w)
		for k := range hv {
			t.Store(hv[k], at(t, d, n, int64(8*k), false), 1)
		}
		t.Br(done)
	}

	// ermsFrom ends b, on System V x86_64, with a branch to a block that
	// does f's whole job with ermsInline's string instruction and returns d
	// when n >= ermsMin, and otherwise to a new block, which it returns for
	// the general-register code. On aarch64 and on Win64 (see ermsMin) it
	// adds nothing and returns b.
	ermsFrom := func(f *llvm.Function, b *llvm.Block, n llvm.Value, sym string, params []llvm.Type) *llvm.Block {
		if s.t.Arch != types.ArchX64 || s.t.OS == types.OSWindows {
			return b
		}
		rep, words := f.NewBlock("erms"), f.NewBlock("words")
		b.CondBr(b.ICmp(llvm.IUGE, n, m.ConstInt(i64, ermsMin)), rep, words)
		args := make([]llvm.Value, len(params))
		for i := range params {
			args[i] = f.Param(i)
		}
		in := ermsInline[sym]
		outs := []llvm.Type{ptr, ptr, i64}
		if sym == symRepStosb {
			outs = []llvm.Type{ptr, i64}
		}
		rep.InlineAsm(m.FuncType(m.Struct(false, outs...), params, false), in[0], in[1], true, args)
		d := f.Param(0)
		rep.Ret(&d)
		return words
	}

	// memcpy(d, s, n): the size classes up to 64 bytes, two more up to 256
	// (memcpyGroups), then 32-byte blocks and an overlapping tail, or rep
	// movsb from ermsMin bytes on System V x86_64.
	f := s.define(symMemcpy, ptr, []llvm.Type{ptr, ptr, i64}, attrs)
	e := f.NewBlock("entry")
	ret := f.NewBlock("ret")
	big := f.NewBlock("big")
	sizeClasses(f, e, f.Param(0), f.Param(1), f.Param(2), "", ret, big)
	// memcpyGroups: 65 to 128 bytes as the first and the last 64, 129 to
	// 256 as the first 128 and the last 128, each 64-byte group of eight
	// words loaded and then stored, so the branches depend on the class and
	// not on n, as in sizeClasses: the 32-byte loop below them runs n / 32
	// times, a trip count that follows n and is mispredicted on every call
	// when lengths vary (textset, 0 to 299 bytes). A group's eight words are
	// at most four vector registers where the target has them (all volatile
	// on Win64, so no xmm6 or xmm7 to save) and eight general registers
	// under general-regs-only, as sizeClasses' 64-byte class has. memcpy
	// only: a group is stored before the next is loaded, so overlapping
	// ranges, which memcpy's callers never pass (memmove takes those), would
	// read bytes already written; d == s, the overlap llvm.memcpy allows,
	// stores each word where it was read, unchanged.
	{
		group := func(b *llvm.Block, from int64, fromEnd bool) {
			g := make([]off, 0, 8)
			for k := int64(0); k < 64; k += 8 {
				if fromEnd {
					g = append(g, off{from - k, true})
				} else {
					g = append(g, off{from + k, false})
				}
			}
			moveWords(b, f.Param(0), f.Param(1), f.Param(2), i64, g)
		}
		le128, gt128, le256 := f.NewBlock("le128"), f.NewBlock("gt128"), f.NewBlock("le256")
		gt256 := f.NewBlock("gt256")
		big.CondBr(big.ICmp(llvm.IULE, f.Param(2), m.ConstInt(i64, 128)), le128, gt128)
		group(le128, 0, false)
		group(le128, 64, true)
		le128.Br(ret)
		gt128.CondBr(gt128.ICmp(llvm.IULE, f.Param(2), m.ConstInt(i64, 256)), le256, gt256)
		group(le256, 0, false)
		group(le256, 64, false)
		group(le256, 128, true)
		group(le256, 64, true)
		le256.Br(ret)
		big = gt256
	}
	big = ermsFrom(f, big, f.Param(2), symRepMovsb, []llvm.Type{ptr, ptr, i64})
	copyForward(f, big, f.Param(0), f.Param(1), f.Param(2), "", ret)
	d0 := f.Param(0)
	ret.Ret(&d0)
	memcpy := f

	// memmove(d, s, n): the size classes up to 64 bytes (overlap-safe),
	// then forward when d <= s or the ranges do not overlap, else backward
	// from the end, both by 32-byte blocks.
	f = s.define(symMemmove, ptr, []llvm.Type{ptr, ptr, i64}, attrs)
	e = f.NewBlock("entry")
	big = f.NewBlock("big")
	fwd := f.NewBlock("fwd")
	bwd := f.NewBlock("bwd")
	ret = f.NewBlock("ret")
	sizeClasses(f, e, f.Param(0), f.Param(1), f.Param(2), "s", ret, big)
	di := big.Cast(llvm.PtrToInt, f.Param(0), i64)
	si := big.Cast(llvm.PtrToInt, f.Param(1), i64)
	le := big.ICmp(llvm.IULE, di, si)
	send := big.Bin(llvm.Add, si, f.Param(2), 0)
	apart := big.ICmp(llvm.IUGE, di, send)
	big.CondBr(big.Bin(llvm.Or, le, apart, 0), fwd, bwd)
	copyForward(f, fwd, f.Param(0), f.Param(1), f.Param(2), "f", ret)
	copyBackward(f, bwd, f.Param(0), f.Param(1), f.Param(2), "b", ret)
	d0 = f.Param(0)
	ret.Ret(&d0)
	memmove := f

	// memset(d, c, n): c splatted to an i64, or rep stosb from ermsMin
	// bytes on System V x86_64.
	f = s.define(symMemset, ptr, []llvm.Type{ptr, i32, i64}, attrs)
	e = f.NewBlock("entry")
	ret = f.NewBlock("ret")
	{
		cb := e.Cast(llvm.Trunc, f.Param(1), i8)
		cw := e.Bin(llvm.Mul, e.Cast(llvm.ZExt, cb, i64), m.ConstInt(i64, 0x0101010101010101), 0)
		i := newCounter(e, m, zero)
		w := f.NewBlock("w")
		wb := f.NewBlock("wb")
		t := f.NewBlock("t")
		tb := f.NewBlock("tb")
		ermsFrom(f, e, f.Param(2), symRepStosb, []llvm.Type{ptr, i32, i64}).Br(w)
		iv := i.get(w)
		w.CondBr(w.ICmp(llvm.IUGE, w.Bin(llvm.Sub, f.Param(2), iv, 0), c8), wb, t)
		iv = i.get(wb)
		wb.Store(cw, wb.GEPBytes(f.Param(0), iv, false), 1)
		i.set(wb, wb.Bin(llvm.Add, iv, c8, 0))
		wb.Br(w)
		iv = i.get(t)
		t.CondBr(t.ICmp(llvm.IULT, iv, f.Param(2)), tb, ret)
		iv = i.get(tb)
		tb.Store(cb, tb.GEPBytes(f.Param(0), iv, false), 1)
		i.set(tb, tb.Bin(llvm.Add, iv, c1, 0))
		tb.Br(t)
	}
	d0 = f.Param(0)
	ret.Ret(&d0)
	memset := f

	// compareWords emits the word loop of memcmp and bcmp: while 8 bytes
	// are left it compares a word of each, branching to differ (with both
	// words) at the first unequal pair; then the tail bytes from the same
	// counter. byteDiffer gets the two zero-extended bytes. Unaligned i64
	// loads are what glibc and musl do on x86_64 and AArch64.
	compareWords := func(f *llvm.Function, e *llvm.Block, pfx string,
		wordDiffer func(b *llvm.Block, a, c llvm.Value), byteDiffer func(b *llvm.Block, a, c llvm.Value)) {
		i := newCounter(e, m, zero)
		w := f.NewBlock(pfx + "w")
		wb := f.NewBlock(pfx + "wb")
		wn := f.NewBlock(pfx + "wn")
		wd := f.NewBlock(pfx + "wd")
		t := f.NewBlock(pfx + "t")
		tb := f.NewBlock(pfx + "tb")
		tn := f.NewBlock(pfx + "tn")
		td := f.NewBlock(pfx + "td")
		same := f.NewBlock(pfx + "same")
		// The first byte first: most calls on unrelated strings end there,
		// and the branch on n >= 8 that follows depends on the data, so on
		// random lengths it is mispredicted often (sortstr, 1 to 16 bytes:
		// 1.157 times the byte loop's time without this test, measured with
		// tools/bench).
		b0 := f.NewBlock(pfx + "b0")
		d0 := f.NewBlock(pfx + "d0")
		e.CondBr(e.ICmp(llvm.IEQ, f.Param(2), zero), same, b0)
		a0 := b0.Cast(llvm.ZExt, b0.Load(i8, f.Param(0), 1), i32)
		c0 := b0.Cast(llvm.ZExt, b0.Load(i8, f.Param(1), 1), i32)
		b0.CondBr(b0.ICmp(llvm.INE, a0, c0), d0, w)
		byteDiffer(d0, a0, c0)
		w.CondBr(w.ICmp(llvm.IUGE, w.Bin(llvm.Sub, f.Param(2), i.get(w), 0), c8), wb, t)
		iv := i.get(wb)
		aw := wb.Load(i64, wb.GEPBytes(f.Param(0), iv, false), 1)
		bw := wb.Load(i64, wb.GEPBytes(f.Param(1), iv, false), 1)
		wb.CondBr(wb.ICmp(llvm.INE, aw, bw), wd, wn)
		wordDiffer(wd, aw, bw)
		i.set(wn, wn.Bin(llvm.Add, i.get(wn), c8, 0))
		wn.Br(w)
		t.CondBr(t.ICmp(llvm.IULT, i.get(t), f.Param(2)), tb, same)
		iv = i.get(tb)
		av := tb.Cast(llvm.ZExt, tb.Load(i8, tb.GEPBytes(f.Param(0), iv, false), 1), i32)
		bv := tb.Cast(llvm.ZExt, tb.Load(i8, tb.GEPBytes(f.Param(1), iv, false), 1), i32)
		tb.CondBr(tb.ICmp(llvm.INE, av, bv), td, tn)
		byteDiffer(td, av, bv)
		i.set(tn, tn.Bin(llvm.Add, i.get(tn), c1, 0))
		tn.Br(t)
		z := m.ConstInt(i32, 0)
		same.Ret(&z)
	}

	// memcmp(a, b, n): the difference of the first differing bytes, as i32.
	// In a differing word the first differing byte is the lowest set byte
	// of a ^ b (every supported target is little-endian).
	f = s.define(symMemcmp, i32, []llvm.Type{ptr, ptr, i64}, attrs)
	e = f.NewBlock("entry")
	memcmp := f
	compareWords(f, e, "", func(b *llvm.Block, aw, bw llvm.Value) {
		x := b.Bin(llvm.Xor, aw, bw, 0)
		tz := b.CallFunc(m.IntrinsicFn("cttz", i64), []llvm.Value{x, m.ConstInt(m.I(1), 1)})
		sh := b.Bin(llvm.And, tz, m.ConstInt(i64, ^uint64(7)), 0)
		ab := b.Cast(llvm.ZExt, b.Cast(llvm.Trunc, b.Bin(llvm.LShr, aw, sh, 0), i8), i32)
		bb := b.Cast(llvm.ZExt, b.Cast(llvm.Trunc, b.Bin(llvm.LShr, bw, sh, 0), i8), i32)
		d := b.Bin(llvm.Sub, ab, bb, 0)
		b.Ret(&d)
	}, func(b *llvm.Block, av, bv llvm.Value) {
		d := b.Bin(llvm.Sub, av, bv, 0)
		b.Ret(&d)
	})

	// bcmp(a, b, n): 0 when equal, else 1 (LLVM emits bcmp for an
	// equality-only memcmp on Linux): the word loop with no byte search.
	f = s.define(symBcmp, i32, []llvm.Type{ptr, ptr, i64}, attrs)
	e = f.NewBlock("entry")
	one := func(b *llvm.Block, _, _ llvm.Value) {
		r := m.ConstInt(i32, 1)
		b.Ret(&r)
	}
	compareWords(f, e, "", one, one)

	m.Used(m.FuncRef(memcpy), m.FuncRef(memmove), m.FuncRef(memset), m.FuncRef(memcmp), m.FuncRef(f))
}

// asmName is a symbol as the assembler spells it: Mach-O prefixes '_'.
func (s *support) asmName(name string) string {
	if s.t.OS == types.OSMacos {
		return "_" + name
	}
	return name
}

// asmFunc appends one global function in module assembly, with the symbol
// directives of the target's object format; body lines are tab-indented
// instructions, local labels or the routine's own unwind directives. On
// x86_64 it brackets the routine with its format's unwind procedure:
// .seh_proc and .seh_endproc on
// COFF, so .pdata and .xdata hold the body's .seh_ codes; .cfi_startproc
// and .cfi_endproc on ELF, so .eh_frame holds its .cfi_ rules. Each x86_64
// body states its prologue in those directives; TestSupportUnwind
// checks that every routine is covered and that its rules match its
// pushes. aarch64's routines have none yet.
func (s *support) asmFunc(name, body string) {
	n := s.asmName(name)
	s.asmNames = append(s.asmNames, name)
	a := &s.asm
	a.WriteString("\t.text\n\t.globl " + n + "\n")
	switch {
	case s.t.OS == types.OSWindows:
		a.WriteString("\t.def " + n + ";\n\t.scl 2;\n\t.type 32;\n\t.endef\n\t.p2align 4\n")
	case s.t.OS == types.OSMacos && s.t.Arch == types.ArchArm64:
		a.WriteString("\t.p2align 2\n")
	case s.t.OS == types.OSMacos:
		a.WriteString("\t.p2align 4\n")
	case s.t.Arch == types.ArchArm64:
		a.WriteString("\t.type " + n + ",%function\n\t.p2align 2\n")
	default:
		a.WriteString("\t.type " + n + ",@function\n\t.p2align 4\n")
	}
	a.WriteString(n + ":\n")
	end := ""
	if s.t.Arch == types.ArchX64 {
		switch s.t.OS {
		case types.OSWindows:
			a.WriteString("\t.seh_proc " + n + "\n")
			end = "\t.seh_endproc\n"
		case types.OSLinux, types.OSNutshell, types.OSMacos:
			a.WriteString("\t.cfi_startproc\n")
			end = "\t.cfi_endproc\n"
		}
	}
	for _, line := range strings.Split(strings.Trim(body, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, ":") {
			a.WriteString(line + "\n")
		} else {
			a.WriteString("\t" + line + "\n")
		}
	}
	a.WriteString(end)
	if s.t.OS == types.OSLinux || s.t.OS == types.OSNutshell {
		a.WriteString("\t.size " + n + ", .-" + n + "\n")
	}
}

// targetRoutines emits the support pack's module-assembly routines for the
// target.
func (s *support) targetRoutines() {
	switch {
	case s.t.Arch == types.ArchArm64:
		s.asmFunc(symSwitch, arm64Switch)
		s.asmFunc(symTaskStart, arm64TaskStart)
		s.asmFunc(symCallSystem, arm64CallSystem)
		if s.t.OS == types.OSLinux { // macOS: dyld calls main (entry)
			if !s.cfg.Library { // a library has no process entry: a C host's crt1 has _start
				s.asmFunc(symELFStart, arm64ELFStart)
			}
			s.asmFunc(symClone, arm64Clone)
			s.asmFunc(symSigreturn, arm64Sigreturn)
		}
	case s.t.OS == types.OSWindows:
		s.asmFunc(symChkstk, win64Chkstk)
		s.asmFunc(symChkstkAligned, win64ChkstkAligned)
		s.asmFunc(symSwitch, win64Switch)
		s.asmFunc(symTaskStart, win64TaskStart)
		s.asmFunc(symCallSystem, win64CallSystem)
		if s.traps {
			s.asmFunc(symTrapCall, win64TrapCall) // trapform.go
		}
		s.m.AddGlobal(symFltused, llvm.GlobalAttrs{Linkage: llvm.External, DSOLocal: true,
			Type: s.m.I(32), Init: s.m.ConstInt(s.m.I(32), 0), Align: 4})
	case s.t.OS == types.OSFreestanding:
		// None: no process entry, no tasks to switch, no threads,
		// no signals, no system stack; the program's MkEntry is the entry
		// and ld.lld needs no __chkstk on ELF.
	case s.t.OS == types.OSMacos:
		// System V's switch, task start and system-stack call; dyld
		// calls main (entry), threads come from pthread_create and the
		// kernel's signal trampoline is libSystem's, so there is no
		// _start, mk_clone or mk_sigreturn.
		s.asmFunc(symSwitch, sysvSwitch)
		s.asmFunc(symTaskStart, sysvTaskStart)
		s.asmFunc(symCallSystem, sysvCallSystem)
	case s.t.OS == types.OSNutshell:
		// Linux's System V routines with Nutshell's own _start, and
		// no mk_clone or mk_sigreturn (support_nutshell.go).
		s.asmFunc(symELFStart, nutshellELFStart)
		s.asmFunc(symSwitch, sysvSwitch)
		s.asmFunc(symTaskStart, sysvTaskStart)
		s.asmFunc(symCallSystem, sysvCallSystem)
	default:
		if !s.cfg.Library { // a library has no process entry: a C host's crt1 has _start
			s.asmFunc(symELFStart, sysvELFStart)
		}
		s.asmFunc(symSwitch, sysvSwitch)
		s.asmFunc(symTaskStart, sysvTaskStart)
		s.asmFunc(symClone, sysvClone)
		s.asmFunc(symSigreturn, sysvSigreturn)
		s.asmFunc(symCallSystem, sysvCallSystem)
	}
}

// initContext emits mk.support.InitContext(ctx, top, entry, arg), the body
// of the intrinsic std.task.InitContext: below top rounded down to 16
// it writes the frame mk_switch pops, zero but for the entry function, its
// argument, the return into mk_task_start and the default MXCSR, x87 control
// word or FPCR, and stores the resulting stack pointer in ctx[0].
func (s *support) initContext() {
	m := s.m
	ptr, i64 := m.Ptr(), m.I(64)
	fr := frameOf(s.t)
	f := s.define(symInitContext, m.Void(), []llvm.Type{ptr, ptr, ptr, ptr}, s.fnAttrs())
	b := f.NewBlock("entry")
	top := b.Cast(llvm.PtrToInt, f.Param(1), i64)
	low := b.Bin(llvm.And, top, m.ConstInt(i64, 15), 0)
	back := b.Bin(llvm.Sub, m.ConstInt(i64, 0), b.Bin(llvm.Add, low, m.ConstInt(i64, fr.frame), 0), 0)
	sp := b.GEPBytes(f.Param(1), back, false)
	for off := uint64(0); off < fr.frame; off += 8 {
		b.Store(m.ConstInt(i64, 0), b.GEPBytes(sp, m.ConstInt(i64, off), true), 8)
	}
	at := func(off uint64) llvm.Value { return b.GEPBytes(sp, m.ConstInt(i64, off), true) }
	b.Store(f.Param(2), at(fr.entry), 8)
	b.Store(f.Param(3), at(fr.arg), 8)
	ts := m.Declare(symTaskStart, llvm.FuncSig{Linkage: llvm.External, Ret: m.Void()})
	b.Store(m.FuncRef(ts), at(fr.ret), 8)
	if fr.mxcsr >= 0 {
		b.Store(m.ConstInt(m.I(32), 0x1F80), at(uint64(fr.mxcsr)), 4)
		b.Store(m.ConstInt(m.I(16), 0x037F), at(uint64(fr.fpcw)), 2)
	}
	b.Store(sp, f.Param(0), 8)
	b.Ret(nil)
}

// machoMain emits main(argc, argv, envp, apple), the macOS entry dyld
// calls through LC_MAIN: it stores its four arguments in a block of four
// words on the main thread's stack and calls std.task.Start with the
// block's address, the sp std/sys/macos reads argv and envp from. Start
// never returns, so the block lives as long as the process.
func (s *support) machoMain() {
	m := s.m
	hook := m.Declare(symTaskStartHook, llvm.FuncSig{Linkage: llvm.External, Ret: m.Void(),
		Params: []llvm.Type{m.Ptr()}, ParamAttrs: []llvm.Attrs{llvm.Attrs{}.Flag("noundef")},
		FnAttrs: llvm.Attrs{}.Flag("noreturn").Flag("nounwind")})
	attrs := s.fnAttrs().Flag("noreturn").Str("probe-stack", "inline-asm").Str("stack-probe-size", "4096")
	i64 := m.I(64)
	f := s.define(symMachOMain, m.I(32), []llvm.Type{m.I(32), m.Ptr(), m.Ptr(), m.Ptr()}, attrs)
	b := f.NewBlock("entry")
	block := b.Alloca(m.Array(4, i64), 8)
	b.Store(b.Cast(llvm.ZExt, f.Param(0), i64), block, 8)
	for i := 1; i <= 3; i++ {
		b.Store(f.Param(i), b.GEPBytes(block, m.ConstInt(i64, uint64(8*i)), false), 8)
	}
	b.CallFunc(hook, []llvm.Value{block})
	b.Unreachable()
}

// entry emits the entry stub: Windows mk_start(), linked with
// -entry:mk_start; Linux and Nutshell mk_start(sp), called by _start with
// the initial stack pointer (on Nutshell, the address of the two start
// words _start pushed). Both call std.task.Start and never return.
func (s *support) entry() {
	if s.t.OS == types.OSMacos {
		if !s.cfg.Library {
			s.machoMain()
		}
		return
	}
	if s.t.OS == types.OSFreestanding {
		return // the program's export extern MkEntry is the entry
	}
	if s.cfg.Library {
		return // a library's runtime starts through its exports
	}
	m := s.m
	hook := m.Declare(symTaskStartHook, llvm.FuncSig{Linkage: llvm.External, Ret: m.Void(),
		Params: []llvm.Type{m.Ptr()}, ParamAttrs: []llvm.Attrs{llvm.Attrs{}.Flag("noundef")},
		FnAttrs: llvm.Attrs{}.Flag("noreturn").Flag("nounwind")})
	attrs := s.fnAttrs().Flag("noreturn")
	if s.t.OS == types.OSLinux || s.t.OS == types.OSNutshell {
		attrs = attrs.Str("probe-stack", "inline-asm").Str("stack-probe-size", "4096")
	}
	var f *llvm.Function
	var sp llvm.Value
	if s.t.OS == types.OSWindows {
		f = s.define(symStart, m.Void(), nil, attrs)
		sp = m.Null()
	} else {
		f = s.define(symStart, m.Void(), []llvm.Type{m.Ptr()}, attrs)
		sp = f.Param(0)
	}
	b := f.NewBlock("entry")
	b.CallFunc(hook, []llvm.Value{sp})
	b.Unreachable()
}
