// Why: Mk's SHA-1 intrinsics lower to target intrinsics, and each needs its
// exact LLVM signature.
//
// What: the table entries for x86_64 (SHA) and aarch64 (FEAT_SHA1, which
// LLVM's sha2 enables).
//
// How: names and operand orders follow LLVM 21's IntrinsicsX86.td and
// IntrinsicsAArch64.td.

package llvm

func i32(m *Module) Type { return m.I(32) }

func init() {
	// x86_64: sha1rnds4 (ABCD, W0+E..W3, imm8 the round group);
	// sha1nexte (the old ABCD, W4..7); sha1msg1 (W0..3, W4..7); sha1msg2
	// (the partial words, W12..15). Each with the oldest word in lane 3.
	intrinsics["x86.sha1rnds4"] = intrinsicSig{0, func(m *Module, _ []Type) (Type, []Type, []bool) {
		return v4i32(m), []Type{v4i32(m), v4i32(m), m.I(8)}, []bool{false, false, true}
	}}
	intrinsics["x86.sha1nexte"] = fixedSig(v4i32, v4i32, v4i32)
	intrinsics["x86.sha1msg1"] = fixedSig(v4i32, v4i32, v4i32)
	intrinsics["x86.sha1msg2"] = fixedSig(v4i32, v4i32, v4i32)
	// aarch64: sha1c, sha1p and sha1m (ABCD, E, four W+K words) are four
	// rounds of Ch, Parity and Maj; sha1h is ROTL30; sha1su0 (W0..3,
	// W4..7, W8..11) and sha1su1 (its result, W12..15). Lane 0 the oldest.
	for _, n := range []string{"aarch64.crypto.sha1c", "aarch64.crypto.sha1p", "aarch64.crypto.sha1m"} {
		intrinsics[n] = fixedSig(v4i32, v4i32, i32, v4i32)
	}
	intrinsics["aarch64.crypto.sha1h"] = fixedSig(i32, i32)
	intrinsics["aarch64.crypto.sha1su0"] = fixedSig(v4i32, v4i32, v4i32, v4i32)
	intrinsics["aarch64.crypto.sha1su1"] = fixedSig(v4i32, v4i32, v4i32)
}
