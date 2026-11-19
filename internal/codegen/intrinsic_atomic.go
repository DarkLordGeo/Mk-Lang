// Why: std.raw's atomics take their memory order as an argument, but LLVM
// needs the ordering fixed in each instruction.
//
// What: atomicOrder (an order argument as an LLVM ordering), constArgBits,
// constBool, the ordUse kinds and rmwOps.
//
// How: the order is a const argument, an immediate or a const after mono's
// substitution, mapped to LLVM's orderings (C++20 [atomics.order]: relaxed
// is LLVM's monotonic). An order the operation cannot have is an ICE.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// What an order is for: each operation admits a subset of the five
// (C++20 [atomics.types.operations], [atomics.fences]).
type ordUse uint8

const (
	ordLoad    ordUse = iota // relaxed, acquire, seq_cst, unordered
	ordStore                 // relaxed, release, seq_cst, unordered
	ordRMW                   // any
	ordCasFail               // relaxed, acquire, seq_cst
	ordFence                 // acquire, release, acq_rel, seq_cst
)

// rmwOps maps the ordered read-modify-write keys to their atomicrmw.
var rmwOps = map[string]llvm.RMWOp{
	"std.raw.Atomic:SwapOrd": llvm.RMWXchg, "std.raw.Atomic:AddOrd": llvm.RMWAdd,
	"std.raw.Atomic:SubOrd": llvm.RMWSub, "std.raw.Atomic:AndOrd": llvm.RMWAnd,
	"std.raw.Atomic:OrOrd": llvm.RMWOr, "std.raw.Atomic:XorOrd": llvm.RMWXor,
}

// constArgBits is the value of const argument i of call t: an immediate,
// or the little-endian bytes of a const of at most 8 bytes.
func (fg *fnGen) constArgBits(t *mir.Terminator, key string, i int) uint64 {
	if i >= int(t.Args.Len) {
		source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s has no argument %d", key, i)
	}
	op := fg.b.Args[t.Args.Start+uint32(i)].Operand
	switch op.Kind {
	case mir.OpImm:
		return op.Bits
	case mir.OpConst:
		cv := fg.g.env.Consts().Get(op.Const)
		if n := len(cv.Bytes); n >= 1 && n <= 8 {
			return types.LoadUint(cv.Bytes, n)
		}
	}
	source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s argument %d is no constant", key, i)
	return 0
}

// constBool is const bool argument i of call t.
func (fg *fnGen) constBool(t *mir.Terminator, key string, i int) bool {
	return fg.constArgBits(t, key, i)&0xff != 0
}

// atomicOrder is const order argument i of call t as an LLVM ordering, for
// use u; 5 (Unordered, a load or a store order only) is lowered as
// monotonic, the same instruction on x86_64 and aarch64 and a stronger
// order, because LLVM's loop idiom turns a counted loop of unordered
// accesses into a call to __llvm_memcpy_element_unordered_atomic_<n>,
// which no Mk link provides (AtomicCopy and AtomicFill are lowered the
// same way). An order
// the use cannot have is an ICE: std.raw's and std.atomic's requires
// (Order.CanLoad, CanStore, CanFence) rejected it at the call (E0706), and
// a caller that forwards its own const order restates the clause, which is
// checked at its calls in turn. What is left to reach
// here is std.atomic's own same-module code, whose clause check folds no
// method call, and std.atomic passing its own order on to std.raw
// (check's stdForwardsRawOrder).
func (fg *fnGen) atomicOrder(t *mir.Terminator, key string, i int, u ordUse) llvm.Ordering {
	o := fg.constArgBits(t, key, i) & 0xff
	if o == 5 && (u == ordLoad || u == ordStore) {
		return llvm.Monotonic // a load or store order only
	}
	ok := o <= 4
	switch u {
	case ordLoad, ordCasFail:
		ok = ok && o != 2 && o != 3
	case ordStore:
		ok = ok && o != 1 && o != 3
	case ordFence:
		ok = ok && o != 0
	}
	if !ok {
		source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s with order %d", key, o)
	}
	return llvm.Monotonic + llvm.Ordering(o) // 0 relaxed is monotonic, then acquire to seq_cst
}
