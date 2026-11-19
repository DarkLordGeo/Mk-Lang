// Why: SHA-256 is much faster on the CPU's SHA instructions;
// std.crypto reaches them here.
//
// What: Sha256Rounds4 and Sha256Schedule4.
//
// How: each is defined by FIPS 180-4 section 6.2.2, at a grain x86_64
// (SHA) and aarch64 (FEAT_SHA256) both implement: four rounds, and four
// words of the message schedule. Bodiless intrinsics: codegen lowers each
// to its target instructions and eval runs its definition.

/// Sha256Rounds4 is four rounds of FIPS 180-4 section 6.2.2 step 3 over the
/// working variables in s, wk holding W[t] + K[t] for the four rounds in
/// order. s holds f, e, b, a, then h, g, d, c (x86_64's SHA256RNDS2 order;
/// aarch64 permutes), and so does the result. Safe in a function whose
/// features include sha; elsewhere the call needs unsafe (E1101) behind a
/// simd.Cpu() test. On x86_64 the function's features must also include
/// sse2, inside unsafe too (E1106): hosted targets always have it; on
/// x86_64-freestanding the function's cpu clause must name it, whatever
/// the bin's cpu. O(1).
export Sha256Rounds4(s: [8]u32, wk: [4]u32): [8]u32 cpu "sha, sha2"
/// Sha256Schedule4 is W[t] to W[t + 3] of FIPS 180-4 section 6.2.2 step 1,
/// from w0 = W[t - 16] .. W[t - 13], w1, w2 and w3 = W[t - 4] .. W[t - 1].
/// It needs the sha feature as Sha256Rounds4 does. O(1).
export Sha256Schedule4(w0: [4]u32, w1: [4]u32, w2: [4]u32, w3: [4]u32): [4]u32 cpu "sha, sha2"
