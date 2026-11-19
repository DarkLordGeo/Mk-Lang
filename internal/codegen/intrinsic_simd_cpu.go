// Why: code that dispatches on CPU features needs a cheap run-time feature
// test and per-function target features, and kernel code needs volatile
// device-register access; all three are intrinsics only codegen can emit.
//
// What: the CPU half of the std.simd intrinsics: cpu (simd.Cpu()), its cache
// and cold detection (cpuCache, cpuInitFnn, cpuDetect), cpuNames, cpuBits
// and targetFeatures (the cpu clause's "target-features"); and
// mmioIntrinsic (std.mmio). The vector intrinsics are intrinsic_simd.go's.
//
// How: the feature bits are read once by cpuid and xgetbv in a cold
// out-of-line function and cached in three words per pack, published with
// release and read with acquire.

package codegen

import (
	"sort"
	"strings"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

// cpu is simd.Cpu(): the feature bits, read once and
// cached. The pack's private cache mk.cpu.cache is three words: the two
// bit words and a ready word, written after them with release order and
// read with acquire, so a call after the first is two loads; two racing
// first calls compute and store the same bits. The detection runs out of
// line in the pack's cold mk.cpu.detect (cpuInitFnn), so a dispatcher's
// hot path is the ready load, a branch and the two loads, and its frame
// keeps none of cpuid's registers (with the detection inline,
// std.simd.IndexByte saved six registers and xmm6 on every call).
func (s *simdCall) cpu() {
	tg := s.g.cfg.Target
	d, _ := s.dest()
	b := s.fg.cur
	if tg.Arch != types.ArchX64 {
		w := cpuBits(tg.Arch, types.CPUBaseline(tg))
		b.Store(s.i64(w[0]), d.v, alignAt(d.align, 0))
		b.Store(s.i64(w[1]), s.fg.gep(d.v, 8), alignAt(d.align, 8))
		s.done()
		return
	}
	i64 := s.m.I(64)
	cache := s.g.cpuCache()
	ready := b.AtomicLoad(i64, s.cpuWord(cache, 2), llvm.Acquire, 8)
	miss, join := s.fg.newBlock(), s.fg.newBlock()
	b.CondBr(b.ICmp(llvm.INE, ready, s.i64(0)), join, miss, s.fg.trapWeights(false))
	detect := s.g.cpuInitFnn(s.fg)
	miss.Call(detect.Type(), detect.Ref(), nil, nil, llvm.Attrs{}, llvm.Attrs{}.Flag("nounwind"))
	miss.Br(join)
	s.fg.cur = join
	h0 := join.AtomicLoad(i64, s.cpuWord(cache, 0), llvm.Monotonic, 8)
	h1 := join.AtomicLoad(i64, s.cpuWord(cache, 1), llvm.Monotonic, 8)
	join.Store(h0, d.v, alignAt(d.align, 0))
	join.Store(h1, s.fg.gep(d.v, 8), alignAt(d.align, 8))
	s.done()
}

// cpuWord is word k of the cache, in the current block.
func (s *simdCall) cpuWord(cache llvm.Value, k uint64) llvm.Value {
	if k == 0 {
		return cache
	}
	return s.fg.cur.GEPBytes(cache, s.i64(8*k), true)
}

// cpuInitFnn is the pack's private mk.cpu.detect(): cpuDetect's reads,
// stored to the cache's two bit words and then its ready word (release).
// noinline and cold: it runs once per pack and process, and keeping it out
// of line keeps its registers out of every caller's frame.
func (g *packGen) cpuInitFnn(caller *fnGen) *llvm.Function {
	if g.cpuInitFn != nil {
		return g.cpuInitFn
	}
	m := g.m
	fn := m.AddFunction("mk.cpu.detect", llvm.FuncSig{Linkage: llvm.Internal, Ret: m.Void(),
		FnAttrs: g.fnAttrs(types.Void).Flag("noinline").Flag("cold")})
	g.cpuInitFn = fn
	g.extraFns = append(g.extraFns, fn)
	tg := &fnGen{g: g, f: caller.f, fi: -1, b: caller.b, fn: fn, span: caller.span}
	tg.entry = fn.NewBlock("entry")
	body := tg.newBlock()
	tg.cur = body
	ts := &simdCall{fg: tg, g: g, m: m}
	w := ts.cpuDetect()
	cache := g.cpuCache()
	ts.fg.cur.AtomicStore(w[0], ts.cpuWord(cache, 0), llvm.Monotonic, 8)
	ts.fg.cur.AtomicStore(w[1], ts.cpuWord(cache, 1), llvm.Monotonic, 8)
	ts.fg.cur.AtomicStore(ts.i64(1), ts.cpuWord(cache, 2), llvm.Release, 8)
	ts.fg.cur.Ret(nil)
	tg.entry.Br(body)
	return fn
}

// cpuCache is the pack's private [3 x i64] cache of Cpu() (cpu).
func (g *packGen) cpuCache() llvm.Value {
	if g.cpuCacheV.IsZero() {
		t := g.m.Array(3, g.m.I(64))
		g.cpuCacheV = g.m.GlobalRef(g.m.AddGlobal("mk.cpu.cache", llvm.GlobalAttrs{Linkage: llvm.Private, Align: 8, Type: t, Init: g.m.ZeroInit(t)}))
	}
	return g.cpuCacheV
}

// cpuDetect reads the features in the current block: cpuid leaf 1 (ECX,
// EDX), leaf 7 subleaf 0 (EBX, ECX) and leaf 0x80000001 (ECX), and
// xgetbv(0) for the state the OS saves, mapped to the bits of
// types.CPUFeatures' x86_64 rows.
func (s *simdCall) cpuDetect() [2]llvm.Value {
	b := s.fg.cur
	i32 := s.m.I(32)
	regs := s.m.Struct(false, i32, i32, i32, i32)
	cpuid := func(leaf, sub uint64) [4]llvm.Value {
		ft := s.m.FuncType(regs, []llvm.Type{i32, i32}, false)
		r := s.fg.cur.InlineAsm(ft, "cpuid", "={ax},={bx},={cx},={dx},{ax},{cx}", false,
			[]llvm.Value{s.m.ConstInt(i32, leaf), s.m.ConstInt(i32, sub)})
		var out [4]llvm.Value
		for k := range out {
			out[k] = s.fg.cur.ExtractValue(r, k)
		}
		return out
	}
	l1 := cpuid(1, 0)
	l7 := cpuid(7, 0)
	lx := cpuid(0x80000001, 0)
	ecx1, edx1, ebx7, ecx7, ecxX := l1[2], l1[3], l7[1], l7[2], lx[2]
	b = s.fg.cur
	osx := b.ICmp(llvm.INE, b.Bin(llvm.And, ecx1, s.m.ConstInt(i32, 1<<27), 0), s.m.ConstInt(i32, 0))
	// xgetbv(0) only when OSXSAVE is set (it faults otherwise): XCR0 is
	// zero through the other edge.
	slot := s.fg.tempBytes(4, 4)
	b.Store(s.m.ConstInt(i32, 0), slot.v, 4)
	xb, join := s.fg.newBlock(), s.fg.newBlock()
	b.CondBr(osx, xb, join)
	ft := s.m.FuncType(s.m.Struct(false, i32, i32), []llvm.Type{i32}, false)
	xr := xb.InlineAsm(ft, "xgetbv", "={ax},={dx},{cx}", false, []llvm.Value{s.m.ConstInt(i32, 0)})
	xb.Store(xb.ExtractValue(xr, 0), slot.v, 4)
	xb.Br(join)
	s.fg.cur = join
	b = join
	xcr := b.Load(i32, slot.v, 4)
	bit := func(r llvm.Value, k uint) llvm.Value {
		return b.Cast(llvm.ZExt, b.ICmp(llvm.INE, b.Bin(llvm.And, r, s.m.ConstInt(i32, 1<<k), 0), s.m.ConstInt(i32, 0)), s.m.I(64))
	}
	ymm := b.ICmp(llvm.IEQ, b.Bin(llvm.And, xcr, s.m.ConstInt(i32, 6), 0), s.m.ConstInt(i32, 6))
	zmm := b.Bin(llvm.And, ymm, b.ICmp(llvm.IEQ, b.Bin(llvm.And, xcr, s.m.ConstInt(i32, 0xe0), 0), s.m.ConstInt(i32, 0xe0)), 0)
	gate := func(v, on llvm.Value) llvm.Value {
		return b.Bin(llvm.And, v, b.Cast(llvm.ZExt, on, s.m.I(64)), 0)
	}
	// Each feature's source bit (Intel SDM vol. 2A, CPUID): name -> value.
	src := map[string]llvm.Value{
		"sse": bit(edx1, 25), "sse2": bit(edx1, 26), "sse3": bit(ecx1, 0), "ssse3": bit(ecx1, 9),
		"sse4.1": bit(ecx1, 19), "sse4.2": bit(ecx1, 20), "popcnt": bit(ecx1, 23), "cx16": bit(ecx1, 13),
		"sahf": bit(ecxX, 0),
		"avx":  gate(bit(ecx1, 28), ymm), "avx2": gate(bit(ebx7, 5), ymm),
		"bmi": bit(ebx7, 3), "bmi2": bit(ebx7, 8), "f16c": gate(bit(ecx1, 29), ymm), "fma": gate(bit(ecx1, 12), ymm),
		"lzcnt": bit(ecxX, 5), "movbe": bit(ecx1, 22), "xsave": bit(ecx1, 26), "pclmul": bit(ecx1, 1),
		"aes": bit(ecx1, 25), "sha": bit(ebx7, 29), "gfni": bit(ecx7, 8), "vpclmulqdq": gate(bit(ecx7, 10), ymm),
		"avx512f": gate(bit(ebx7, 16), zmm), "avx512bw": gate(bit(ebx7, 30), zmm), "avx512cd": gate(bit(ebx7, 28), zmm),
		"avx512dq": gate(bit(ebx7, 17), zmm), "avx512vl": gate(bit(ebx7, 31), zmm), "avx512vbmi": gate(bit(ecx7, 1), zmm),
	}
	var w [2]llvm.Value
	w[0], w[1] = s.i64(0), s.i64(0)
	k := 0
	for _, row := range types.CPUFeatures() {
		if row.Arch != types.ArchX64 {
			continue
		}
		if v, ok := src[row.Name]; ok && row.LLVM != "" {
			sh := b.Bin(llvm.Shl, v, s.i64(uint64(k%64)), 0)
			w[k/64] = b.Bin(llvm.Or, w[k/64], sh, 0)
		}
		k++
	}
	return w
}

// constStr is the str const argument i (Of's and Has's names).
func (s *simdCall) constStr(i int) string {
	op := s.fg.b.Args[s.t.Args.Start+uint32(i)].Operand
	if op.Kind != mir.OpConst {
		s.ice("argument %d is no constant str", i)
	}
	cv := s.g.env.Consts().Get(op.Const)
	if len(cv.Bytes) < 16 {
		s.ice("argument %d is no str", i)
	}
	n := types.LoadUint(cv.Bytes[8:], 8)
	if n == 0 || len(cv.Relocs) == 0 {
		return ""
	}
	data := s.g.env.Consts().Get(cv.Relocs[0].Data).Bytes
	if uint64(len(data)) < n {
		s.ice("argument %d: str of %d bytes over %d", i, n, len(data))
	}
	return string(data[:n])
}

// cpuNames splits a cpu clause's comma list (eval's cpuNames).
func cpuNames(list string) []string {
	var out []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// cpuBits is eval's cpuBits (eval/intrinsic_simd.go): bit k of the pair is
// the k-th row of arch; a level name has no bit of its own.
func cpuBits(arch types.Arch, names []string) [2]uint64 {
	var w [2]uint64
	k := 0
	for _, row := range types.CPUFeatures() {
		if row.Arch != arch {
			continue
		}
		if row.LLVM != "" {
			for _, n := range names {
				if n == row.Name {
					w[k/64] |= 1 << (k % 64)
				}
			}
		}
		k++
	}
	return w
}

// targetFeatures is the "target-features" attribute of a function with the
// feature set set: the LLVM spellings of
// its names for the target's architecture, sorted, each with its +; "" for
// an empty set. The build's target-cpu stays, and LLVM adds these features
// to it; LLVM refuses to inline a callee whose features are not a subset of
// the caller's, which is the behaviour wanted.
func (g *packGen) targetFeatures(set []string) string {
	if len(set) == 0 {
		return ""
	}
	var out []string
	evex := false
	for _, n := range set {
		if row, ok := types.CPUFeatureByName(g.cfg.Target.Arch, n); ok && row.LLVM != "" {
			out = append(out, row.LLVM)
			evex = evex || strings.HasPrefix(row.LLVM, "+avx512")
		}
	}
	// LLVM 18 to 21 split the 512-bit registers off the AVX-512 features:
	// on a base CPU without them (x86-64-v2, the hosted baseline) a function
	// with +avx512bw but no +evex512 has only 256-bit EVEX, and a
	// Vec[u8, 64] compare dies in instruction selection ("Cannot select
	// v32i1 setcc"). A cpu clause naming an AVX-512 feature means the
	// 512-bit form, so the clause brings +evex512 with it.
	if evex {
		out = append(out, "+evex512")
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// mmioIntrinsic lowers std.mmio: Load and
// Reg.Read are one load volatile of T's LLVM type, Store and Reg.Write one
// store volatile, each with align AlignOf[T] and no load facts; Fence is
// fence seq_cst; Reg.New is inttoptr of the address.
func (fg *fnGen) mmioIntrinsic(key string, t *mir.Terminator, args []value) {
	s := &simdCall{fg: fg, g: fg.g, m: fg.g.m, t: t, key: key, args: args,
		ga: fg.b.GenericArgs[t.Callee.Fn.Args.Start:t.Callee.Fn.Args.End()]}
	b := fg.cur
	access := func() (*repr, uint32) {
		r := s.g.reprOf(s.typeArg(0))
		if r.kind != rScalar || r.class == sBool || r.class == sFloat {
			s.ice("volatile access of %s (check's E0701)", s.g.tt.TypeString(s.typeArg(0), nil))
		}
		return r, max(r.align, 1)
	}
	// regPtr is the register's address: Reg's field p, read through &self.
	regPtr := func() llvm.Value {
		return b.Load(s.m.Ptr(), args[0].v, 8)
	}
	switch strings.TrimPrefix(key, "std.mmio.") {
	case "Load":
		r, al := access()
		s.ret(b.LoadVolatile(r.mem, args[0].v, al))
	case "Store":
		_, al := access()
		b.StoreVolatile(args[1].v, args[0].v, al)
		s.done()
	case "Fence":
		b.Fence(llvm.SeqCst)
		s.done()
	case "Reg:New":
		p := b.Cast(llvm.IntToPtr, args[0].v, s.m.Ptr())
		d, _ := s.dest()
		b.Store(p, d.v, alignAt(d.align, 0))
		s.done()
	case "Reg:Read":
		r, al := access()
		s.ret(b.LoadVolatile(r.mem, regPtr(), al))
	case "Reg:Write":
		_, al := access()
		b.StoreVolatile(args[1].v, regPtr(), al)
		s.done()
	default:
		s.ice("no lowering")
	}
}
