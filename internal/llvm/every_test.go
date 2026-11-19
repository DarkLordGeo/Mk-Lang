// Why: every IR form codegen emits must print exactly and be accepted by clang.
// What: buildEvery, the one module that uses every builder method and every
// attribute, linkage and metadata kind codegen emits, and TestLLVMPrintGolden.
// How: TestLLVMPrintGolden compares its Windows form with
// testdata/every.ll.golden; TestLLVMSyntaxSmoke compiles it for every smoke
// target.

package llvm

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.golden")

// target is one smoke-test target: the triple and data layout printed into
// the module, and what selects the target-specific assembly.
type target struct {
	name, triple, layout string
	arch                 string // "x86_64" or "aarch64"
	os                   string // "windows", "linux" or "darwin"
}

// goldenTarget is the target of the golden file: clang 21.1.8's triple and
// data layout for x86_64-pc-windows-msvc, as printed by clang
// --target=x86_64-pc-windows-msvc -S -emit-llvm -x ir on a module without a
// triple.
var goldenTarget = target{"x86_64-windows", "x86_64-pc-windows-msvc19.33.0",
	"e-m:w-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128", "x86_64", "windows"}

// buildEvery builds the every-construct module for tg.
func buildEvery(tg target) *Module {
	m := NewModule("every.mk", tg.triple, tg.layout)
	coff := tg.os == "windows"
	i1, i8, i16, i32, i64 := m.I(1), m.I(8), m.I(16), m.I(32), m.I(64)
	p, f32, f64, void := m.Ptr(), m.Float(), m.Double(), m.Void()
	none := Attrs{}
	nu := none.Flag("noundef")
	cpu := "x86-64"
	if tg.arch == "aarch64" {
		cpu = "generic"
	}
	fnBase := none.Flag("nounwind").Flag("uwtable").Str("target-cpu", cpu)
	if !coff {
		fnBase = fnBase.Str("probe-stack", "inline-asm").Str("stack-probe-size", "4096")
	}
	cross := FuncSig{Visibility: Hidden} // referenced from another pack
	if coff {
		cross = FuncSig{DSOLocal: true}
	}

	// Module assembly.
	switch {
	case tg.os == "windows":
		m.ModuleAsm(".text\n.globl __chkstk\n__chkstk:\n" +
			"  pushq %rcx\n  pushq %rax\n  cmpq $0x1000, %rax\n  leaq 24(%rsp), %rcx\n  jb 1f\n" +
			"2:\n  subq $0x1000, %rcx\n  orq $0, (%rcx)\n  subq $0x1000, %rax\n  cmpq $0x1000, %rax\n  ja 2b\n" +
			"1:\n  subq %rax, %rcx\n  orq $0, (%rcx)\n  popq %rax\n  popq %rcx\n  retq\n")
	case tg.arch == "x86_64":
		m.ModuleAsm(".text\n.globl _start\n_start:\n  xorl %ebp, %ebp\n  movq %rsp, %rdi\n  andq $-16, %rsp\n" +
			"  callq mk_start\n  ud2\n.globl mk_sigreturn\nmk_sigreturn:\n  movl $15, %eax\n  syscall\n")
	case tg.os == "darwin":
		m.ModuleAsm(".text\n.globl _mk_sigreturn\n.p2align 2\n_mk_sigreturn:\n  mov x8, #139\n  svc #0\n  ret\n")
	default:
		m.ModuleAsm(".text\n.globl _start\n_start:\n  mov x29, #0\n  mov x0, sp\n  and sp, x0, #-16\n" +
			"  bl mk_start\n  brk #1\n.globl mk_sigreturn\nmk_sigreturn:\n  mov x8, #139\n  svc #0\n")
	}
	switch tg.os { // quoted strings in module asm exercise the escapes
	case "windows":
		m.ModuleAsm(".section .rdata,\"dr\"\n.ascii \"mk\\n\"\n.text")
	case "linux":
		m.ModuleAsm(".section .rodata,\"a\",@progbits\n.ascii \"mk\\n\"\n.text")
	}

	// Globals.
	str := m.Bytes([]byte("hi \"mk\"\\\n\x00\xff"))
	lit := m.AddGlobal("mk.s.00112233aabbccdd", GlobalAttrs{Linkage: Private, Constant: true, UnnamedAddr: true,
		Align: 1, Type: str.Ty, Init: str})
	litRef := m.GlobalRef(lit)
	row := m.ConstStruct(true, m.ConstInt(i32, 7), m.Bytes([]byte{0, 0, 0, 0}), litRef, m.ConstInt(i64, 9))
	m.AddGlobal("mk.c.0123456789abcdef", GlobalAttrs{Linkage: Private, Constant: true, UnnamedAddr: true, Align: 8,
		Type: row.Ty, Init: row})
	site := m.ConstStruct(false, litRef, m.ConstInt(i64, 8), m.ConstInt(i32, 3), m.ConstInt(i32, 5))
	siteG := m.AddGlobal("mk.pos.mk.every.F.0", GlobalAttrs{Linkage: Private, Constant: true, UnnamedAddr: true,
		Align: 8, Type: site.Ty, Init: site})
	varG := m.AddGlobal("mk.every.counter", GlobalAttrs{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal,
		Align: 8, Type: m.Array(16, i8), Init: m.ZeroInit(m.Array(16, i8))})
	m.AddGlobal("mk.every.local", GlobalAttrs{Linkage: Internal, Align: 4, Type: i32, Init: m.ConstSInt(i32, -3)})
	if coff {
		m.AddGlobal("_fltused", GlobalAttrs{DSOLocal: true, Type: i32, Init: m.ConstInt(i32, 0)})
	}
	m.AddGlobal("mk.every.pgo", GlobalAttrs{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal, Align: 8,
		Type: m.Array(3, i64), Init: m.ZeroInit(m.Array(3, i64))})
	ext := m.AddGlobal("mk.other.Table", GlobalAttrs{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal,
		Type: m.Array(4, i8), Align: 1})
	sect := ".data.mk"
	if tg.os == "darwin" {
		sect = "__DATA,__mk"
	}
	m.AddGlobal("mk.every.sect", GlobalAttrs{Type: i64, Init: m.ConstInt(i64, 1), Section: sect, Align: 8})
	floats := m.ConstStruct(false, m.ConstF32(0x3DCCCCCD), m.ConstF32(0x7FC00000), m.ConstF32(0x7F800000),
		m.ConstF32(0xFF800000), m.ConstF32(0x80000000), m.ConstF32(0x7F800001), m.ConstF32(0x00000001), m.ConstF64(0x3FB999999999999A), m.ConstF64(0x7FF8000000000000))
	m.AddGlobal("mk.c.floats", GlobalAttrs{Linkage: Private, Constant: true, UnnamedAddr: true, Type: floats.Ty, Init: floats})
	tbl := m.ConstArray(m.Struct(false, i32, p, i64),
		m.ConstStruct(false, m.ConstInt(i32, 1), m.ConstGEP(litRef, 1), m.ConstInt(i64, 2)),
		m.ConstStruct(false, m.ConstInt(i32, 2), m.Null(), m.ConstInt(i64, 0)))
	m.AddGlobal("mk.support.errnames", GlobalAttrs{Linkage: Private, Constant: true, UnnamedAddr: true, Align: 8,
		Type: tbl.Ty, Init: tbl})
	misc := m.ConstStruct(false, m.ConstIntToPtr(8), m.Undef(i32), m.Poison(i64), m.ZeroInit(f64),
		m.ConstInt(i1, 1), m.ConstInt(m.I(128), ^uint64(0)), m.ZeroInit(m.Vector(2, f32)))
	m.AddGlobal("mk.c.misc", GlobalAttrs{Linkage: Internal, Constant: true, Type: misc.Ty, Init: misc})
	empty := m.ConstStruct(false, m.ConstStruct(true), m.ConstStruct(false), m.ConstArray(i8))
	m.AddGlobal("mk.c.empty", GlobalAttrs{Linkage: Private, Constant: true, UnnamedAddr: true, Type: empty.Ty, Init: empty})
	selfRef := m.AddGlobal("mk.every.self", GlobalAttrs{Linkage: Internal, Type: p, Init: m.Null()})
	selfRef.SetInit(m.GlobalRef(selfRef))
	odr := m.AddGlobal("mk.every.odr", GlobalAttrs{Linkage: LinkOnceODR, Visibility: cross.Visibility,
		DSOLocal: cross.DSOLocal, Constant: true, Type: i64, Init: m.ConstInt(i64, 42), Align: 8})

	// Aliases.
	m.AddAlias("mk.every.counter.alias", External, cross.Visibility, m.Array(16, i8), m.GlobalRef(varG))
	m.AddAlias("mk.every.tail", Internal, Default, i8, m.ConstGEP(m.GlobalRef(varG), 8))

	// Declarations (deduplicated), with codegen's parameter attribute rows.
	panicSig := FuncSig{Ret: void, Params: []Type{i32, p},
		ParamAttrs: []Attrs{nu, nu.Flag("nonnull").Int("align", 8).Int("dereferenceable", 24)},
		FnAttrs:    none.Flag("cold").Flag("noreturn").Flag("nounwind")}
	panicFn := m.Declare("mk.std.rt.Panic", panicSig)
	if m.Declare("mk.std.rt.Panic", panicSig) != panicFn {
		panic("Declare did not deduplicate")
	}
	printf := m.Declare("mk_printf", FuncSig{Ret: i32, RetAttrs: nu, Params: []Type{p},
		ParamAttrs: []Attrs{nu.Flag("nonnull")}, Variadic: true, FnAttrs: none.Flag("nounwind")})
	small := m.Declare("mk.other.Small", FuncSig{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal,
		Ret: i8, RetAttrs: nu.Flag("zeroext").Range(i8, 0, 3), Params: []Type{i8, i16, i1},
		ParamAttrs: []Attrs{nu.Flag("signext").Range(i8, -3, 5), nu.Flag("zeroext").Range(i16, 0, 1000), nu.Flag("zeroext")}, FnAttrs: fnBase})
	agg := m.Struct(false, i64, f64)
	coerced := m.Declare("mk.other.Coerced", FuncSig{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal,
		Ret: agg, Params: []Type{i64, f64, m.Vector(2, f32)}, ParamAttrs: []Attrs{nu, nu, nu}, FnAttrs: fnBase})

	// Return attribute goldens: GetX(p: mut Point): mut int and First(xs:
	// &List[int]): &int.
	retRef := nu.Flag("nonnull").Int("align", 8).Int("dereferenceable", 8)
	getX := m.AddFunction("mk.every.GetX", FuncSig{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal,
		Ret: p, RetAttrs: retRef, Params: []Type{p},
		ParamAttrs: []Attrs{nu.Flag("nonnull").Flag("noalias").Int("align", 8).Int("dereferenceable", 16)},
		FnAttrs:    fnBase})
	{
		b := getX.NewBlock("b0")
		x := b.GEPBytes(getX.Param(0), m.ConstInt(i64, 8), true)
		b.Ret(&x)
	}
	first := m.AddFunction("mk.every.First", FuncSig{Ret: p, RetAttrs: retRef, Params: []Type{p},
		ParamAttrs: []Attrs{nu.Flag("nonnull").Flag("noalias").Flag("readonly").Int("align", 8).Int("dereferenceable", 24)},
		FnAttrs:    fnBase})
	{
		b := first.NewBlock("b0")
		d := b.Load(p, first.Param(0), 8, m.meta.NonNull(), m.meta.NoUndef(), m.meta.Align(8))
		b.Ret(&d)
	}

	// Parameter rows: slices, str, raw pointers, sret, indirect, byval.
	indirect := nu.Flag("nonnull").Flag("noalias").Int("align", 8).Int("dereferenceable", 40)
	if tg.os == "linux" && tg.arch == "x86_64" {
		indirect = none.Typed("byval", m.Array(40, i8)).Int("align", 8)
	}
	rows := m.AddFunction("mk.every.Rows", FuncSig{Linkage: Internal,
		Params: []Type{p, p, i64, p, i64, p, i64, p, p, p}, Ret: void,
		ParamAttrs: []Attrs{
			none.Typed("sret", m.Array(24, i8)).Flag("noalias").Flag("nonnull").Int("align", 8),
			nu.Flag("nonnull").Flag("noalias").Flag("readonly"), nu, // []int (Freeze)
			nu.Flag("nonnull").Flag("noalias"), nu, // mut []int
			nu.Flag("nonnull").Flag("noalias").Flag("readonly"), nu, // str
			nu,       // *T
			nu,       // ?&T
			indirect, // indirect aggregate
		},
		FnAttrs: fnBase.Flag("inlinehint")})
	{
		b := rows.NewBlock("b0")
		b.Ret(nil)
	}

	// The body: every instruction kind, with line tables.
	file := m.meta.DIFile("every.mk", ".")
	cu := m.meta.DICompileUnit(file, false)
	m.meta.DebugFlags(coff)
	sp := m.meta.DISubprogram("F", "mk.every.F", file, 3, cu, false)
	fsig := FuncSig{Visibility: cross.Visibility, DSOLocal: cross.DSOLocal, Ret: i64, RetAttrs: nu,
		Params: []Type{i64, i32, p, f64, f32, i1}, ParamAttrs: []Attrs{nu, nu, nu.Flag("nonnull"), nu, nu, nu.Flag("zeroext")},
		FnAttrs: fnBase.Flag("hot")}
	fn := m.AddFunction("mk.every.F", fsig)
	fn.SetDbg(sp)
	fn.SetProf(m.meta.EntryCount(1000))
	loc := func(line, col int) MD { return m.meta.DILocation(line, col, sp) }
	entry := fn.NewBlock("entry")
	b0 := fn.NewBlock("b0")
	b1 := fn.NewBlock("b1")
	b2 := fn.NewBlock("b2")
	b3 := fn.NewBlock("b3")
	exit := fn.NewBlock("exit")
	trap := fn.NewBlock("trap.0") // created last, filled first
	trap.SetDebugLoc(loc(9, 2))
	trap.Call(panicFn.Type(), panicFn.Ref(), []Value{m.ConstInt(i32, 1), m.GlobalRef(siteG)},
		[]Attrs{nu, nu.Flag("nonnull").Int("align", 8).Int("dereferenceable", 24)}, none,
		none.Flag("cold").Flag("noreturn").Flag("nounwind"))
	trap.Unreachable()

	x, y, ptr, d, s, flag := fn.Param(0), fn.Param(1), fn.Param(2), fn.Param(3), fn.Param(4), fn.Param(5)
	entry.SetDebugLoc(loc(3, 1))
	slot := entry.Alloca(m.Array(16, i8), 8)
	tmp := entry.Alloca(i64, 8)
	bslot := entry.Alloca(i8, 1)
	entry.Store(x, tmp, 8)
	entry.Store(entry.Cast(ZExt, flag, i8), bslot, 1)
	entry.Br(b0)

	b0.SetDebugLoc(loc(4, 5))
	bv := b0.Load(i8, bslot, 1, Range(m.meta.Range(i8, []int64{0, 2})), m.meta.NoUndef())
	_ = b0.Cast(Trunc, bv, i1)
	ch := b0.Load(i32, ptr, 4, Range(m.meta.Range(i32, []int64{0, 55296, 57344, 1114112})), m.meta.NoUndef())
	tag := b0.Load(i8, ptr, 1, Range(m.meta.Range(i8, RangePairs(8, []uint64{0, 1, 200}))), m.meta.NoUndef())
	field := b0.GEPBytes(slot, m.ConstInt(i64, 8), true)
	raw := b0.GEPBytes(ptr, x, false)
	b0.Store(m.ConstInt(i64, 5), field, 8)
	// GEP flags of LLVM 21, the oldest LLVM Mk supports: inbounds nuw, nusw
	// nuw, nuw.
	b0.Store(m.ConstInt(i64, 6), b0.GEP8(slot, m.ConstInt(i64, 8), GEPInbounds|GEPNUW), 8)
	b0.Store(m.ConstInt(i8, 7), b0.GEP8(ptr, x, GEPNUSW|GEPNUW), 1)
	b0.Store(m.ConstInt(i8, 8), b0.GEP8(ptr, x, GEPNUW), 1)
	ld := b0.Load(i64, raw, 1, Dbg(loc(4, 9)))
	acc := ld
	for _, op := range []BinOp{Add, Sub, Mul, SDiv, UDiv, SRem, URem, Shl, LShr, AShr, And, Or, Xor} {
		acc = b0.Bin(op, acc, x, 0)
	}
	acc = b0.Bin(Add, acc, x, NUW|NSW)
	acc = b0.Bin(Sub, acc, x, NSW)
	acc = b0.Bin(Mul, acc, x, NUW)
	acc = b0.Bin(Shl, acc, m.ConstInt(i64, 1), NUW)
	acc = b0.Bin(SDiv, acc, m.ConstInt(i64, 2), Exact)
	acc = b0.Bin(LShr, acc, m.ConstInt(i64, 1), Exact)
	fv := d
	for _, op := range []BinOp{FAdd, FSub, FMul, FDiv} {
		fv = b0.Bin(op, fv, d, 0)
	}
	fv = b0.Bin(FNeg, fv, Value{}, 0)
	for pr := IEQ; pr <= ISLE; pr++ {
		b0.ICmp(pr, acc, x)
	}
	b0.ICmp(IEQ, ptr, m.Null())
	for pr := FFalse; pr <= FTrue; pr++ {
		b0.FCmp(pr, fv, d)
	}
	t8 := b0.Cast(Trunc, acc, i8)
	b0.Cast(ZExt, t8, i32)
	b0.Cast(SExt, y, i64)
	b0.Cast(FPTrunc, d, f32)
	b0.Cast(FPExt, s, f64)
	b0.Cast(FPToUI, d, i32)
	b0.Cast(FPToSI, d, i64)
	b0.Cast(UIToFP, y, f64)
	b0.Cast(SIToFP, x, f32)
	pi := b0.Cast(PtrToInt, ptr, i64)
	b0.Cast(IntToPtr, pi, p)
	b0.Cast(BitCast, d, i64)
	sel := b0.Select(flag, acc, x)
	pair := b0.InsertValue(m.Undef(agg), sel, 0)
	pair = b0.InsertValue(pair, fv, 1)
	b0.ExtractValue(pair, 1)
	nested := b0.InsertValue(m.ZeroInit(m.Struct(false, agg, m.Array(2, i32))), y, 1, 1)
	b0.ExtractValue(nested, 0, 0)
	_, _ = ch, tag
	ov := b0.Call(m.IntrinsicFn("sadd.with.overflow", i64).Type(), m.Intrinsic("sadd.with.overflow", i64),
		[]Value{acc, x}, nil, none, none)
	sum := b0.ExtractValue(ov, 0)
	of := b0.ExtractValue(ov, 1)
	b0.CondBr(of, trap, b1, Prof(m.meta.BranchWeights(1, 1048575)))

	b1.SetDebugLoc(loc(5, 3))
	ints := []string{"uadd.with.overflow", "ssub.with.overflow", "usub.with.overflow", "smul.with.overflow",
		"umul.with.overflow", "sadd.sat", "uadd.sat", "ssub.sat", "usub.sat", "smax", "smin", "umax", "umin",
		"fshl", "fshr", "ctpop", "bswap", "bitreverse", "ctlz", "cttz", "abs", "expect"}
	for _, name := range ints {
		f := m.IntrinsicFn(name, i64)
		args := []Value{sum, x, x}[:f.Type().Len()]
		if f.Type().Len() == 2 && f.Type().Elem(1) == i1 {
			args = []Value{sum, m.ConstInt(i1, 0)}
		}
		b1.CallFunc(f, args)
	}
	for _, name := range []string{"fabs", "sqrt", "floor", "ceil", "trunc", "rint", "nearbyint", "round",
		"roundeven", "copysign", "minnum", "maxnum", "minimum", "maximum", "fma"} {
		f := m.IntrinsicFn(name, f64)
		b1.CallFunc(f, []Value{d, d, d}[:f.Type().Len()])
		if name == "fabs" || name == "sqrt" {
			b1.CallFunc(m.IntrinsicFn(name, f32), []Value{s})
		}
	}
	b1.CallFunc(m.IntrinsicFn("memcpy", p, p, i64), []Value{slot, ptr, m.ConstInt(i64, 16), m.ConstInt(i1, 0)})
	b1.CallFunc(m.IntrinsicFn("memmove", p, p, i64), []Value{slot, ptr, x, m.ConstInt(i1, 0)})
	b1.CallFunc(m.IntrinsicFn("llvm.memset", p, i64), []Value{slot, m.ConstInt(i8, 0), m.ConstInt(i64, 16), m.ConstInt(i1, 0)})
	b1.CallFunc(m.IntrinsicFn("assume", nil...), []Value{m.ConstInt(i1, 1)})
	b1.CallFunc(m.IntrinsicFn("ctpop", m.Vector(4, i32)), []Value{m.ZeroInit(m.Vector(4, i32))})
	// Atomics at seq_cst and the other orderings (raw.Atomic).
	ap := b1.GEPBytes(slot, m.ConstInt(i64, 0), true)
	for op := RMWXchg; op <= RMWUMin; op++ {
		b1.AtomicRMW(op, ap, x, SeqCst, 8)
	}
	b1.AtomicRMW(RMWFAdd, ap, d, Monotonic, 8)
	b1.AtomicRMW(RMWFSub, ap, d, AcqRel, 8)
	cx := b1.CmpXchg(ap, x, sum, SeqCst, SeqCst, 8)
	b1.ExtractValue(cx, 1)
	b1.CmpXchg(ap, x, sum, AcqRel, Acquire, 8)
	al := b1.AtomicLoad(i64, ap, SeqCst, 8)
	b1.AtomicLoad(i64, ap, Acquire, 8)
	b1.AtomicLoad(i64, ap, Unordered, 8)
	b1.AtomicStore(al, ap, SeqCst, 8)
	b1.AtomicStore(al, ap, Release, 8)
	b1.AtomicStore(al, ap, Monotonic, 8)
	b1.Fence(SeqCst)
	b1.Fence(Acquire)
	b1.Fence(Release)
	b1.Fence(AcqRel)
	// Inline assembly.
	switch {
	case tg.arch == "x86_64" && tg.os == "linux":
		sc := m.FuncType(i64, []Type{i64, i64, i64, i64, i64, i64, i64}, false)
		b1.InlineAsm(sc, "syscall", "={rax},{rax},{rdi},{rsi},{rdx},{r10},{r8},{r9},~{rcx},~{r11},~{memory}",
			true, []Value{m.ConstInt(i64, 39), x, x, x, x, x, x})
		b1.InlineAsm(m.FuncType(i64, nil, false), "movq %rsp, $0", "=r", true, nil)
	case tg.arch == "x86_64":
		b1.InlineAsm(m.FuncType(p, nil, false), "movq %gs:0x30, $0", "=r", false, nil)
		b1.InlineAsm(m.FuncType(i64, nil, false), "movq %rsp, $0", "=r", true, nil)
	default:
		sc := m.FuncType(i64, []Type{i64, i64, i64, i64, i64, i64, i64}, false)
		b1.InlineAsm(sc, "svc #0", "={x0},{x8},{x0},{x1},{x2},{x3},{x4},{x5},~{memory}",
			true, []Value{m.ConstInt(i64, 172), x, x, x, x, x, x})
		b1.InlineAsm(m.FuncType(i64, nil, false), "mov $0, sp", "=r", true, nil)
	}
	b1.InlineAsm(m.FuncType(void, nil, false), "", "~{memory}", true, nil)
	// Calls: direct with the callee's attributes, variadic, through a pointer.
	b1.CallFunc(getX, []Value{slot})
	b1.CallFunc(small, []Value{t8, m.ConstSInt(i16, -1), flag})
	cv := b1.CallFunc(coerced, []Value{x, d, m.ZeroInit(m.Vector(2, f32))})
	b1.ExtractValue(cv, 0)
	b1.Call(printf.Type(), printf.Ref(), []Value{litRef, x, d}, []Attrs{nu.Flag("nonnull"), nu, nu}, nu, none)
	fp := b1.Load(p, ptr, 8, m.meta.NoUndef())
	b1.Call(m.FuncType(i64, []Type{i64}, false), fp, []Value{x}, []Attrs{nu}, nu, none.Flag("nounwind"))
	sret := b1.Alloca(m.Array(24, i8), 8)
	b1.CallFunc(rows, []Value{sret, ptr, x, ptr, x, litRef, m.ConstInt(i64, 3), ptr, m.Null(), slot})
	b1.Load(i8, m.GlobalRef(ext), 1)
	b1.Load(i64, m.GlobalRef(odr), 8)
	b1.Switch(y, b2, []Case{{m.ConstInt(i32, 0), b3}, {m.ConstInt(i32, 1), b3}, {m.ConstSInt(i32, -7), exit}},
		Prof(m.meta.BranchWeights(5, 10, 0, 1)))

	b2.SetDebugLoc(loc(6, 1))
	b2.Switch(y, exit, nil)

	b3.SetDebugLoc(loc(7, 1))
	b3.CondBr(flag, exit, exit, Prof(m.meta.BranchWeights(0xFFFFFFFF, 0)))

	exit.SetDebugLoc(loc(8, 1))
	exit.Ret(&sum)

	// Vector operations (ABI coercion values), and a second, optimized
	// subprogram whose linkage name equals its name.
	vec := m.AddFunction("vec", FuncSig{Ret: m.Vector(4, i32), Params: []Type{m.Vector(4, i32), m.Vector(2, f64)},
		FnAttrs: fnBase})
	vec.SetDbg(m.meta.DISubprogram("vec", "vec", file, 20, cu, true))
	{
		b := vec.NewBlock("b0")
		b.SetDebugLoc(m.meta.DILocation(21, 1, vec.dbg))
		a, v2 := vec.Param(0), vec.Param(1)
		sum4 := b.Bin(Add, a, a, NSW)
		c := b.ICmp(ISLT, sum4, a)
		b.FCmp(FOLT, v2, b.Bin(FMul, v2, v2, 0))
		r := b.Select(c, sum4, a)
		b.Ret(&r)
	}

	// Named metadata with every operand kind.
	note := m.meta.DistinctTuple(MDStr("mk"), MDVal(vec.Ref()), MDNull(), MDRef(file), MDRef(MD{}),
		MDVal(m.ConstInt(i64, 1)))
	m.meta.Named("mk.notes", note, m.meta.Tuple(MDStr("second")))

	// A function with no debug info, external linkage, noreturn (the entry stub).
	start := m.AddFunction("mk_start", FuncSig{Ret: void, FnAttrs: none.Flag("noreturn").Flag("nounwind")})
	{
		b := start.NewBlock("b0")
		b.Call(panicFn.Type(), panicFn.Ref(), []Value{m.ConstInt(i32, 0), m.GlobalRef(siteG)}, nil, none, none)
		b.Unreachable()
	}
	// A memory function of the support pack ("no-builtins", llvm.used).
	memset := m.AddFunction("memset", FuncSig{Ret: p, Params: []Type{p, i32, i64},
		FnAttrs: none.Flag("nounwind").Str("no-builtins", "").Flag("noinline")})
	{
		b := memset.NewBlock("b0")
		r := memset.Param(0)
		b.Ret(&r)
	}
	pv := m.AddFunction("mk.every.priv", FuncSig{Linkage: Private, Ret: void, FnAttrs: fnBase.Flag("cold")})
	pv.NewBlock("b0").Ret(nil)
	odrF := m.AddFunction("mk.every.odrf", FuncSig{Linkage: LinkOnceODR, Visibility: cross.Visibility,
		DSOLocal: cross.DSOLocal, Ret: void, FnAttrs: fnBase})
	odrF.NewBlock("b0").Ret(nil)
	m.Used(memset.Ref(), pv.Ref())
	return m
}

func TestLLVMPrintGolden(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if _, err := buildEvery(goldenTarget).WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "every.ll.golden")
	if *update {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/llvm -update)", err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("printed module differs from %s; rerun with -update and read the diff", path)
	}
}
