// Why: Mk's SHA-256 intrinsics lower to target intrinsics, and each needs
// its exact LLVM signature.
//
// What: the table entries for x86_64 (SHA) and aarch64 (FEAT_SHA256).
//
// How: names and operand orders follow LLVM 21's IntrinsicsX86.td and
// IntrinsicsAArch64.td.

package llvm

func init() {
	// x86_64: sha256rnds2 (CDGH, ABEF, two W+K words in the low lanes);
	// sha256msg1 (W0..3, W4..7); sha256msg2 (the partial sums, W12..15).
	intrinsics["x86.sha256rnds2"] = fixedSig(v4i32, v4i32, v4i32, v4i32)
	intrinsics["x86.sha256msg1"] = fixedSig(v4i32, v4i32, v4i32)
	intrinsics["x86.sha256msg2"] = fixedSig(v4i32, v4i32, v4i32)
	// aarch64: sha256h (ABCD, EFGH, four W+K words) is the new ABCD,
	// sha256h2 (EFGH, the old ABCD, the same words) the new EFGH;
	// sha256su0 (W0..3, W4..7) and sha256su1 (its result, W8..11, W12..15).
	intrinsics["aarch64.crypto.sha256h"] = fixedSig(v4i32, v4i32, v4i32, v4i32)
	intrinsics["aarch64.crypto.sha256h2"] = fixedSig(v4i32, v4i32, v4i32, v4i32)
	intrinsics["aarch64.crypto.sha256su0"] = fixedSig(v4i32, v4i32, v4i32)
	intrinsics["aarch64.crypto.sha256su1"] = fixedSig(v4i32, v4i32, v4i32, v4i32)
}
