// Why: SHA-1 runs many times faster on the CPU's SHA instructions, and
// std's hash code should reach them without inline assembly.
//
// What: the SHA-1 intrinsics of std.raw (std/raw/sha1.mk): sha1Rounds4,
// sha1NextE and sha1Schedule4, registered in init, with sha1K and sha1Rev.
//
// How: each is defined by FIPS 180-4 section 6.1.2, in x86_64's lane order
// (the oldest word, and a, in lane 3):
//
//   - Sha1Rounds4(abcd, we, g): four rounds of round group g; x86_64
//     sha1rnds4 with g its immediate (it adds K itself), aarch64 sha1c,
//     sha1p or sha1m with e 0 and K added to the words, between lane
//     reversals.
//   - Sha1NextE(abcd, w): w with ROTL30 of a added to lane 3; x86_64
//     sha1nexte, aarch64 sha1h and an add.
//   - Sha1Schedule4(w0, w1, w2, w3): W[t .. t + 3]; x86_64 sha1msg1, the
//     xor of W[t - 8 .. t - 5], sha1msg2; aarch64 sha1su0 and sha1su1.

package codegen

import (
	"fmt"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// sha1K are FIPS 180-4 section 4.2.1's constants, one per round group.
var sha1K = [4]uint64{0x5a827999, 0x6ed9eba1, 0x8f1bbcdc, 0xca62c1d6}

// sha1Rev reverses four lanes: x86_64's SHA-1 order to aarch64's.
var sha1Rev = []int{3, 2, 1, 0}

func init() {
	registerTarget("std.raw.Sha1Rounds4", "([4]u32, [4]u32, u8): [4]u32", (*fnGen).sha1Rounds4)
	registerTarget("std.raw.Sha1NextE", "([4]u32, [4]u32): [4]u32", (*fnGen).sha1NextE)
	registerTarget("std.raw.Sha1Schedule4", "([4]u32, [4]u32, [4]u32, [4]u32): [4]u32", (*fnGen).sha1Schedule4)
}

// sha1Need is the SHA-1 intrinsics' feature on fg's architecture.
func (fg *fnGen) sha1Need() (arm bool, need []string) {
	if fg.archOf() == types.ArchArm64 {
		return true, []string{"sha2"}
	}
	return false, []string{"sha"}
}

func (fg *fnGen) sha1Rounds4(t *mir.Terminator, args []value) {
	const key = "std.raw.Sha1Rounds4"
	m := fg.g.m
	v4 := m.Vector(4, m.I(32))
	g := fg.constArgBits(t, key, 2) & 0xff
	if g > 3 {
		// check's E0706 judged the requires clause at the call.
		source.ICEf(fg.spanOf(t.Span), "codegen: intrinsic %s with round group %d", key, g)
	}
	abcd, we := fg.loadArg(key, t, args, 0, v4), fg.loadArg(key, t, args, 1, v4)
	arm, need := fg.sha1Need()
	// One helper per round group: the group is the instruction's
	// immediate on x86_64 and picks the instruction on aarch64.
	name := fmt.Sprintf("%s.%d", helperName(key), g)
	r := fg.featureOp(name, need, v4, []llvm.Value{abcd, we}, func(b *llvm.Block, p []llvm.Value) llvm.Value {
		if arm {
			fn := [4]string{"aarch64.crypto.sha1c", "aarch64.crypto.sha1p", "aarch64.crypto.sha1m", "aarch64.crypto.sha1p"}[g]
			k := m.ConstInt(m.I(32), sha1K[g])
			wk := b.Bin(llvm.Add, b.ShuffleVector(p[1], p[1], sha1Rev), m.ConstVector(m.I(32), k, k, k, k), 0)
			x := b.CallFunc(m.IntrinsicFn(fn), []llvm.Value{b.ShuffleVector(p[0], p[0], sha1Rev), m.ConstInt(m.I(32), 0), wk})
			return b.ShuffleVector(x, x, sha1Rev)
		}
		return b.CallFunc(m.IntrinsicFn("x86.sha1rnds4"), []llvm.Value{p[0], p[1], m.ConstInt(m.I(8), g)})
	})
	fg.storeAt(t, r, 0)
	fg.targetDone(t)
}

func (fg *fnGen) sha1NextE(t *mir.Terminator, args []value) {
	const key = "std.raw.Sha1NextE"
	m := fg.g.m
	v4 := m.Vector(4, m.I(32))
	abcd, w := fg.loadArg(key, t, args, 0, v4), fg.loadArg(key, t, args, 1, v4)
	arm, need := fg.sha1Need()
	r := fg.featureOp(helperName(key), need, v4, []llvm.Value{abcd, w}, func(b *llvm.Block, p []llvm.Value) llvm.Value {
		if arm {
			lane := m.ConstInt(m.I(32), 3)
			e := b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha1h"), []llvm.Value{b.ExtractElement(p[0], lane)})
			return b.InsertElement(p[1], b.Bin(llvm.Add, b.ExtractElement(p[1], lane), e, 0), lane)
		}
		return b.CallFunc(m.IntrinsicFn("x86.sha1nexte"), []llvm.Value{p[0], p[1]})
	})
	fg.storeAt(t, r, 0)
	fg.targetDone(t)
}

func (fg *fnGen) sha1Schedule4(t *mir.Terminator, args []value) {
	const key = "std.raw.Sha1Schedule4"
	m := fg.g.m
	v4 := m.Vector(4, m.I(32))
	in := make([]llvm.Value, 4)
	for i := range in {
		in[i] = fg.loadArg(key, t, args, i, v4)
	}
	arm, need := fg.sha1Need()
	r := fg.featureOp(helperName(key), need, v4, in, func(b *llvm.Block, p []llvm.Value) llvm.Value {
		if arm {
			var w [4]llvm.Value
			for i := range w {
				w[i] = b.ShuffleVector(p[i], p[i], sha1Rev)
			}
			x := b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha1su0"), []llvm.Value{w[0], w[1], w[2]})
			x = b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha1su1"), []llvm.Value{x, w[3]})
			return b.ShuffleVector(x, x, sha1Rev)
		}
		x := b.CallFunc(m.IntrinsicFn("x86.sha1msg1"), []llvm.Value{p[0], p[1]})
		x = b.Bin(llvm.Xor, x, p[2], 0)
		return b.CallFunc(m.IntrinsicFn("x86.sha1msg2"), []llvm.Value{x, p[3]})
	})
	fg.storeAt(t, r, 0)
	fg.targetDone(t)
}
