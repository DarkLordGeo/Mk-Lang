// Why: SHA-256 runs many times faster on the CPU's SHA instructions, and
// std's hash code should reach them without inline assembly.
//
// What: the SHA-256 intrinsics of std.raw (std/raw/sha256.mk):
// sha256Rounds4 and sha256Schedule4, registered in init.
//
// How: each is defined by FIPS 180-4 section 6.2.2:
//
//   - Sha256Rounds4(s, wk): four rounds over s = (f, e, b, a, h, g, d, c),
//     lanes from 0; x86_64 two sha256rnds2 (ABEF and CDGH are its operand
//     order, the second taking wk's high half), aarch64 sha256h and
//     sha256h2 between permutations to and from (a, b, c, d), (e, f, g, h).
//   - Sha256Schedule4(w0, w1, w2, w3): W[t .. t + 3]; x86_64 sha256msg1,
//     the add of W[t - 7 .. t - 4], sha256msg2; aarch64 sha256su0 and
//     sha256su1.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

func init() {
	registerTarget("std.raw.Sha256Rounds4", "([8]u32, [4]u32): [8]u32", (*fnGen).sha256Rounds4)
	registerTarget("std.raw.Sha256Schedule4", "([4]u32, [4]u32, [4]u32, [4]u32): [4]u32", (*fnGen).sha256Schedule4)
}

func (fg *fnGen) sha256Rounds4(t *mir.Terminator, args []value) {
	const key = "std.raw.Sha256Rounds4"
	m := fg.g.m
	v4, v8 := m.Vector(4, m.I(32)), m.Vector(8, m.I(32))
	s, wk := fg.loadArg(key, t, args, 0, v8), fg.loadArg(key, t, args, 1, v4)
	arm := fg.archOf() == types.ArchArm64
	need := []string{"sha"}
	if arm {
		need = []string{"sha2"}
	}
	r := fg.featureOp(helperName(key), need, v8, []llvm.Value{s, wk}, func(b *llvm.Block, p []llvm.Value) llvm.Value {
		abef := b.ShuffleVector(p[0], p[0], []int{0, 1, 2, 3})
		cdgh := b.ShuffleVector(p[0], p[0], []int{4, 5, 6, 7})
		if arm {
			abcd := b.ShuffleVector(abef, cdgh, []int{3, 2, 7, 6})
			efgh := b.ShuffleVector(abef, cdgh, []int{1, 0, 5, 4})
			na := b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha256h"), []llvm.Value{abcd, efgh, p[1]})
			ne := b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha256h2"), []llvm.Value{efgh, abcd, p[1]})
			return b.ShuffleVector(na, ne, []int{5, 4, 1, 0, 7, 6, 3, 2})
		}
		s1 := b.CallFunc(m.IntrinsicFn("x86.sha256rnds2"), []llvm.Value{cdgh, abef, p[1]})
		hi := b.ShuffleVector(p[1], p[1], []int{2, 3, 2, 3})
		s0 := b.CallFunc(m.IntrinsicFn("x86.sha256rnds2"), []llvm.Value{abef, s1, hi})
		return b.ShuffleVector(s0, s1, []int{0, 1, 2, 3, 4, 5, 6, 7})
	})
	fg.storeAt(t, r, 0)
	fg.targetDone(t)
}

func (fg *fnGen) sha256Schedule4(t *mir.Terminator, args []value) {
	const key = "std.raw.Sha256Schedule4"
	m := fg.g.m
	v4 := m.Vector(4, m.I(32))
	in := make([]llvm.Value, 4)
	for i := range in {
		in[i] = fg.loadArg(key, t, args, i, v4)
	}
	arm := fg.archOf() == types.ArchArm64
	need := []string{"sha"}
	if arm {
		need = []string{"sha2"}
	}
	r := fg.featureOp(helperName(key), need, v4, in, func(b *llvm.Block, p []llvm.Value) llvm.Value {
		if arm {
			x := b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha256su0"), []llvm.Value{p[0], p[1]})
			return b.CallFunc(m.IntrinsicFn("aarch64.crypto.sha256su1"), []llvm.Value{x, p[2], p[3]})
		}
		x := b.CallFunc(m.IntrinsicFn("x86.sha256msg1"), []llvm.Value{p[0], p[1]})
		w7 := b.ShuffleVector(p[2], p[3], []int{1, 2, 3, 4}) // W[t - 7 .. t - 4]
		x = b.Bin(llvm.Add, x, w7, 0)
		return b.CallFunc(m.IntrinsicFn("x86.sha256msg2"), []llvm.Value{x, p[3]})
	})
	fg.storeAt(t, r, 0)
	fg.targetDone(t)
}
