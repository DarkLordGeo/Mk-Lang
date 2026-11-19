// Why: Mk's AES and carry-less multiply intrinsics lower to target
// intrinsics, and each needs its exact LLVM signature.
//
// What: the table entries for x86_64 (AES-NI, PCLMULQDQ) and aarch64
// (FEAT_AES, which includes PMULL).
//
// How: names and operand orders follow LLVM 21's IntrinsicsX86.td and
// IntrinsicsAArch64.td.

package llvm

func init() {
	// x86_64: aesenc and aesenclast take the state and the round key.
	intrinsics["x86.aesni.aesenc"] = fixedSig(v2i64, v2i64, v2i64)
	intrinsics["x86.aesni.aesenclast"] = fixedSig(v2i64, v2i64, v2i64)
	// pclmulqdq (a, b, imm8): imm8 bit 0 picks a's quadword, bit 4 b's.
	intrinsics["x86.pclmulqdq"] = intrinsicSig{0, func(m *Module, _ []Type) (Type, []Type, []bool) {
		return v2i64(m), []Type{v2i64(m), v2i64(m), m.I(8)}, []bool{false, false, true}
	}}
	// aarch64: aese (state, key) is AddRoundKey, then SubBytes and
	// ShiftRows; aesmc is MixColumns; pmull64 is the 128-bit product.
	intrinsics["aarch64.crypto.aese"] = fixedSig(v16i8, v16i8, v16i8)
	intrinsics["aarch64.crypto.aesmc"] = fixedSig(v16i8, v16i8)
	intrinsics["aarch64.neon.pmull64"] = intrinsicSig{0, func(m *Module, _ []Type) (Type, []Type, []bool) {
		return v16i8(m), []Type{m.I(64), m.I(64)}, nil
	}}
}
