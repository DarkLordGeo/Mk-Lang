// Why: big-integer and crypto code adds and subtracts long chains of 64-bit
// limbs, and each step must be one add-with-carry, with no branch.
//
// What: the carry intrinsics of std.raw (std/raw/carry.mk), AddCarry64 and
// SubBorrow64, registered in init and lowered by carryOp.
//
// How: AddCarry64(x, y, c) is two llvm.uadd.with.overflow.i64 calls, x + y
// then that sum + c, with carry the zero-extended or of their flags, and
// SubBorrow64 the same over llvm.usub.with.overflow.i64. When c is the
// previous call's carry, LLVM's backend folds each pair into one adc (sbb
// on x86_64, adcs and sbcs on aarch64), so a limb chain whose results feed
// arithmetic has no branch and no compare in any build. A branch on the last
// flag is another matter: LLVM may fold the chain into a comparison of the
// operands and split the branch into one jump per limb, and a select on it
// is a cmov or a branch at its choice (std/crypto/crypto.mk's rules).

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
)

func init() {
	registerTarget("std.raw.AddCarry64", "(u64, u64, u64): (sum: u64, carry: u64)", func(fg *fnGen, t *mir.Terminator, args []value) {
		fg.carryOp(t, args, "std.raw.AddCarry64", "uadd.with.overflow", "sum", "carry")
	})
	registerTarget("std.raw.SubBorrow64", "(u64, u64, u64): (diff: u64, borrow: u64)", func(fg *fnGen, t *mir.Terminator, args []value) {
		fg.carryOp(t, args, "std.raw.SubBorrow64", "usub.with.overflow", "diff", "borrow")
	})
}

// carryOp lowers one carry intrinsic: op over x and y, then over that
// result and the carry in; the result record gets the value in its field
// named val and the or of the two flags in its field named flag (records
// are laid out in name order, so the fields are found by name).
func (fg *fnGen) carryOp(t *mir.Terminator, args []value, key, op, val, flag string) {
	g, m := fg.g, fg.g.m
	i64 := m.I(64)
	x, y, c := fg.loadArg(key, t, args, 0, i64), fg.loadArg(key, t, args, 1, i64), fg.loadArg(key, t, args, 2, i64)
	f := m.IntrinsicFn(op, i64)
	r1 := fg.cur.CallFunc(f, []llvm.Value{x, y})
	r2 := fg.cur.CallFunc(f, []llvm.Value{fg.cur.ExtractValue(r1, 0), c})
	out := map[string]llvm.Value{
		val:  fg.cur.ExtractValue(r2, 0),
		flag: fg.cur.Cast(llvm.ZExt, fg.cur.Bin(llvm.Or, fg.cur.ExtractValue(r1, 1), fg.cur.ExtractValue(r2, 1), 0), i64),
	}
	dt := fg.b.PlaceType(t.Dest)
	fs := g.layout(dt).Fields
	for i, fd := range g.tt.Get(dt).Fields {
		v, ok := out[g.tt.Interner().Str(fd.Name)]
		if !ok || i >= len(fs) {
			source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s returns %s, its lowering writes (%s, %s)", key, g.tt.TypeString(dt, nil), val, flag)
		}
		fg.storeAt(t, v, fs[i].Offset)
	}
	fg.targetDone(t)
}
