// Why: Mk has no 128-bit type, but LLVM makes i128 divisions itself: under
// a profile, loop unroll expands a trip count that SCEV computed in i128
// (sieve.mk's `for j <= n { j += i }`, whose `n + 1` may not fit i64). Mk
// links no compiler-rt, so without these routines the link fails with an
// undefined __udivti3.
//
// What: tiHelpers and tiHelper: __udivti3, __umodti3, __divti3 and
// __modti3, the calls LLVM's x86_64 and aarch64 back ends make for an i128
// udiv, urem, sdiv and srem.
//
// How: each body uses only operations every 64-bit back end lowers inline,
// and each public name is a weak alias of an internal body, so a user's
// definition wins.

package codegen

import (
	"strings"

	"mk/internal/llvm"
	"mk/internal/types"
)

// The 128-bit division routines, compiler-rt's names and contracts.
const (
	symUdivti3 = "__udivti3"
	symUmodti3 = "__umodti3"
	symDivti3  = "__divti3"
	symModti3  = "__modti3"
)

// tiBody is the internal body's name of routine name: mk.support.udivti3
// for __udivti3.
func tiBody(name string) string { return "mk.support." + strings.TrimPrefix(name, "__") }

// tiHelpers emits the four routines. Each is written with i128 add, sub,
// shifts, compares, ctlz and an i64 udiv or urem only, which every 64-bit
// back end lowers inline; an i128 udiv, urem, sdiv or srem in their bodies
// would call the routine itself. Their contracts are compiler-rt's:
//
//   - __udivti3(a, b) is a / b and __umodti3(a, b) is a % b, unsigned;
//   - __divti3(a, b) is a / b rounded toward zero and __modti3(a, b) is
//     a % b with a's sign, two's complement; MIN / -1 wraps to MIN and
//     MIN % -1 is 0, where LLVM's sdiv and srem are undefined;
//   - b == 0 traps (llvm.trap: ud2 on x86_64, brk on aarch64), where
//     LLVM's division by zero is undefined and compiler-rt's faults.
//
// The calling convention is the one LLVM's libcalls use: on System V and
// AAPCS64 each i128 in a register pair and the result in rax:rdx or x0:x1
// (the IR's i128 parameters and return); on Win64 both operands by
// reference and the result in xmm0 as <2 x i64> (LLVM's LowerWin64_i128OP),
// so there the IR takes two pointers and returns the vector. A Win64 bin
// is never general-regs-only, so xmm0 is always there.
//
// A user's definition wins: each body is an internal function,
// mk.support.udivti3 and so on, and the public name is a weak alias of it
// (a weak external on COFF, STB_WEAK on ELF; on Mach-O, which LLVM gives
// no external weak alias, the routine is itself a weak definition, named
// as the public name). So a C source, an --object
// or an archive member the link pulls (for any name) that defines
// __udivti3 itself replaces the support pack's with no duplicate symbol,
// on lld-link and ld.lld; the support pack's fills in only when no object
// of the link defines the name. As for any name an object defines, the
// link does not pull an archive member for these names alone. A weak
// function carrying its own comdat would be made global on COFF (LLVM
// drops the weak external), so the comdat is the internal body's. Each
// body has its own function-table row, in that comdat on COFF and
// !associated on ELF, so the linker discards it with the body
// (funcRowIn), and it names the support pack's code even when a user's
// definition took the name; none is a row in the support pack's own
// array, which FuncTable references and would keep. So a link that discards unreferenced sections (every
// release link, and a Windows dev link without --debug) keeps none of them
// in a program that divides no i128: its executable is the same bytes as
// before. A link that keeps every section (a Linux dev link, which has no
// --gc-sections; a Windows link with --debug in dev, /opt:noref) holds all
// four.
func (s *support) tiHelpers() {
	s.tiHelper(symUdivti3, false, false)
	s.tiHelper(symUmodti3, false, true)
	s.tiHelper(symDivti3, true, false)
	s.tiHelper(symModti3, true, true)
}

// tiHelper emits one routine: signed or unsigned, the remainder or the
// quotient. The unsigned core is restoring division: past the zero test,
// a fast path when both magnitudes fit in 64 bits (an i64 udiv, the case a
// widened trip count gives), the quotient 0 when b > a, and otherwise
// sh = ctlz(b) - ctlz(a) (0 to 127), b shifted left by sh, and sh + 1
// steps that each shift the quotient left, subtract the shifted divisor
// when the remainder is at least it (setting the quotient's low bit), and
// shift the divisor right. The signed forms divide the magnitudes
// ((x ^ s) - s with s = x >> 127, so |MIN| is 2^127) and give the result
// the sign of a ^ b (quotient) or of a (remainder).
func (s *support) tiHelper(name string, signed, rem bool) {
	m := s.m
	i128, i64, ptr := m.I(128), m.I(64), m.Ptr()
	win := s.t.OS == types.OSWindows
	ret, params := i128, []llvm.Type{i128, i128}
	if win {
		ret, params = m.Vector(2, i64), []llvm.Type{ptr, ptr}
	}
	pa := make([]llvm.Attrs, len(params))
	for i := range params {
		pa[i] = llvm.Attrs{}.Flag("noundef")
	}
	var f *llvm.Function
	if s.t.OS == types.OSMacos {
		// Mach-O has no comdats, so nothing to keep the weak external
		// from, and LLVM writes a weak alias there as a non-external
		// .weak_reference: the routine is a weak definition itself
		// (.weak_definition), and its row is not discardable (Mach-O's
		// rows never are, functable.go).
		f = m.AddFunction(name, llvm.FuncSig{Linkage: llvm.Weak, DSOLocal: true, Ret: ret,
			Params: params, ParamAttrs: pa, FnAttrs: s.fnAttrs()})
		funcRowIn(m, s.t, f, m.Null(), rowOther)
	} else {
		f = m.AddFunction(tiBody(name), llvm.FuncSig{Linkage: llvm.Internal, Ret: ret,
			Params: params, ParamAttrs: pa, FnAttrs: s.fnAttrs()})
		funcRowIn(m, s.t, f, m.Null(), rowOther)
		m.AddAlias(name, llvm.Weak, llvm.Default, m.FuncType(ret, params, false), m.FuncRef(f))
	}

	e := f.NewBlock("entry")
	q, r, dv, k := e.Alloca(i128, 16), e.Alloca(i128, 16), e.Alloca(i128, 16), e.Alloca(i64, 8)
	a, b := f.Param(0), f.Param(1)
	if win {
		a, b = e.Load(i128, a, 8), e.Load(i128, b, 8)
	}
	zero, one := m.ConstInt(i128, 0), m.ConstInt(i128, 1)
	n, d := a, b
	var sa, sb llvm.Value
	if signed {
		c127 := m.ConstInt(i128, 127)
		sa, sb = e.Bin(llvm.AShr, a, c127, 0), e.Bin(llvm.AShr, b, c127, 0)
		n = e.Bin(llvm.Sub, e.Bin(llvm.Xor, a, sa, 0), sa, 0)
		d = e.Bin(llvm.Sub, e.Bin(llvm.Xor, b, sb, 0), sb, 0)
	}
	byZero, ok := f.NewBlock("zero"), f.NewBlock("ok")
	fast, slow, long := f.NewBlock("fast"), f.NewBlock("slow"), f.NewBlock("long")
	loop, done := f.NewBlock("loop"), f.NewBlock("done")
	e.CondBr(e.ICmp(llvm.IEQ, d, zero), byZero, ok)
	byZero.CallFunc(m.IntrinsicFn("trap"), nil)
	byZero.Unreachable()

	hi := ok.Bin(llvm.LShr, ok.Bin(llvm.Or, n, d, 0), m.ConstInt(i128, 64), 0)
	ok.CondBr(ok.ICmp(llvm.IEQ, hi, zero), fast, slow)

	n64, d64 := fast.Cast(llvm.Trunc, n, i64), fast.Cast(llvm.Trunc, d, i64)
	fast.Store(fast.Cast(llvm.ZExt, fast.Bin(llvm.UDiv, n64, d64, 0), i128), q, 16)
	fast.Store(fast.Cast(llvm.ZExt, fast.Bin(llvm.URem, n64, d64, 0), i128), r, 16)
	fast.Br(done)

	slow.Store(zero, q, 16)
	slow.Store(n, r, 16)
	slow.CondBr(slow.ICmp(llvm.IUGT, d, n), done, long)

	ctlz := m.IntrinsicFn("ctlz", i128)
	no := m.ConstInt(m.I(1), 0)
	sh := long.Bin(llvm.Sub, long.CallFunc(ctlz, []llvm.Value{d, no}), long.CallFunc(ctlz, []llvm.Value{n, no}), 0)
	long.Store(long.Bin(llvm.Shl, d, sh, 0), dv, 16)
	long.Store(long.Cast(llvm.Trunc, sh, i64), k, 8)
	long.Br(loop)

	rv, dd, qv := loop.Load(i128, r, 16), loop.Load(i128, dv, 16), loop.Load(i128, q, 16)
	ge := loop.ICmp(llvm.IUGE, rv, dd)
	loop.Store(loop.Select(ge, loop.Bin(llvm.Sub, rv, dd, 0), rv), r, 16)
	loop.Store(loop.Bin(llvm.Or, loop.Bin(llvm.Shl, qv, one, 0), loop.Cast(llvm.ZExt, ge, i128), 0), q, 16)
	loop.Store(loop.Bin(llvm.LShr, dd, one, 0), dv, 16)
	kv := loop.Load(i64, k, 8)
	loop.Store(loop.Bin(llvm.Sub, kv, m.ConstInt(i64, 1), 0), k, 8)
	loop.CondBr(loop.ICmp(llvm.IEQ, kv, m.ConstInt(i64, 0)), done, loop)

	res := done.Load(i128, q, 16)
	if rem {
		res = done.Load(i128, r, 16)
	}
	if signed {
		sign := sa
		if !rem {
			sign = done.Bin(llvm.Xor, sa, sb, 0)
		}
		res = done.Bin(llvm.Sub, done.Bin(llvm.Xor, res, sign, 0), sign, 0)
	}
	if win {
		res = done.Cast(llvm.BitCast, res, ret)
	}
	done.Ret(&res)
}
