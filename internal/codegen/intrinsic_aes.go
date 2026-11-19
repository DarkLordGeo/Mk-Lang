// Why: AES and GHASH run many times faster on the CPU's own instructions,
// and std's crypto code should reach them without inline assembly.
//
// What: the AES and carry-less multiply intrinsics of std.raw
// (std/raw/aes.mk): aesRound (AesRound, AesLastRound) and clmul64
// (Clmul64), registered in init.
//
// How: each is defined by FIPS 197 (the Cipher's round and last round) or
// by the polynomial product, and lowers to one target instruction family:
//
//   - AesRound(state, key): SubBytes, ShiftRows, MixColumns, then
//     AddRoundKey; x86_64 aesenc, aarch64 aesmc(aese(state, 0)) ^ key.
//   - AesLastRound(state, key): the same without MixColumns; x86_64
//     aesenclast, aarch64 aese(state, 0) ^ key.
//   - Clmul64(a, b): the 128-bit carry-less product as (hi, lo); x86_64
//     pclmulqdq, aarch64 pmull.
//
// The 16 bytes of a state or key are FIPS 197's input order (byte r + 4c
// is row r of column c), which is the order both instruction sets load.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/types"
)

func init() {
	registerTarget("std.raw.AesRound", "([16]u8, [16]u8): [16]u8", func(fg *fnGen, t *mir.Terminator, args []value) {
		fg.aesRound(t, args, "std.raw.AesRound", true)
	})
	registerTarget("std.raw.AesLastRound", "([16]u8, [16]u8): [16]u8", func(fg *fnGen, t *mir.Terminator, args []value) {
		fg.aesRound(t, args, "std.raw.AesLastRound", false)
	})
	registerTarget("std.raw.Clmul64", "(u64, u64): (hi: u64, lo: u64)", (*fnGen).clmul64)
}

// aesRound lowers AesRound (mix) or AesLastRound.
func (fg *fnGen) aesRound(t *mir.Terminator, args []value, key string, mix bool) {
	m := fg.g.m
	var r llvm.Value
	if fg.archOf() == types.ArchArm64 {
		v := m.Vector(16, m.I(8))
		s, k := fg.loadArg(key, t, args, 0, v), fg.loadArg(key, t, args, 1, v)
		r = fg.featureOp(helperName(key), []string{"aes"}, v, []llvm.Value{s, k}, func(b *llvm.Block, p []llvm.Value) llvm.Value {
			x := b.CallFunc(m.IntrinsicFn("aarch64.crypto.aese"), []llvm.Value{p[0], m.ZeroInit(v)})
			if mix {
				x = b.CallFunc(m.IntrinsicFn("aarch64.crypto.aesmc"), []llvm.Value{x})
			}
			return b.Bin(llvm.Xor, x, p[1], 0)
		})
	} else {
		v := m.Vector(2, m.I(64))
		s, k := fg.loadArg(key, t, args, 0, v), fg.loadArg(key, t, args, 1, v)
		name := "x86.aesni.aesenclast"
		if mix {
			name = "x86.aesni.aesenc"
		}
		r = fg.featureOp(helperName(key), []string{"aes"}, v, []llvm.Value{s, k}, func(b *llvm.Block, p []llvm.Value) llvm.Value {
			return b.CallFunc(m.IntrinsicFn(name), []llvm.Value{p[0], p[1]})
		})
	}
	fg.storeAt(t, r, 0)
	fg.targetDone(t)
}

// clmul64 lowers Clmul64: the product as <2 x i64>, lane 0 the low half;
// the result record's fields are in name order (hi, lo), as Mul128's.
func (fg *fnGen) clmul64(t *mir.Terminator, args []value) {
	const key = "std.raw.Clmul64"
	g, m := fg.g, fg.g.m
	i64 := m.I(64)
	v := m.Vector(2, i64)
	a, b := fg.loadArg(key, t, args, 0, i64), fg.loadArg(key, t, args, 1, i64)
	var r llvm.Value
	if fg.archOf() == types.ArchArm64 {
		r = fg.featureOp(helperName(key), []string{"aes"}, v, []llvm.Value{a, b}, func(bl *llvm.Block, p []llvm.Value) llvm.Value {
			x := bl.CallFunc(m.IntrinsicFn("aarch64.neon.pmull64"), []llvm.Value{p[0], p[1]})
			return bl.Cast(llvm.BitCast, x, v)
		})
	} else {
		r = fg.featureOp(helperName(key), []string{"pclmul"}, v, []llvm.Value{a, b}, func(bl *llvm.Block, p []llvm.Value) llvm.Value {
			zero := m.ConstInt(i64, 0)
			va := bl.InsertElement(m.ZeroInit(v), p[0], zero)
			vb := bl.InsertElement(m.ZeroInit(v), p[1], zero)
			return bl.CallFunc(m.IntrinsicFn("x86.pclmulqdq"), []llvm.Value{va, vb, m.ConstInt(m.I(8), 0)})
		})
	}
	lo := fg.cur.ExtractElement(r, m.ConstInt(i64, 0))
	hi := fg.cur.ExtractElement(r, m.ConstInt(i64, 1))
	d, _ := fg.placeAddr(t.Dest)
	fs := g.layout(fg.b.PlaceType(t.Dest)).Fields // (hi, lo), records in name order
	fg.cur.Store(hi, fg.gep(d.v, fs[0].Offset), alignAt(d.align, fs[0].Offset))
	fg.cur.Store(lo, fg.gep(d.v, fs[1].Offset), alignAt(d.align, fs[1].Offset))
	fg.targetDone(t)
}
