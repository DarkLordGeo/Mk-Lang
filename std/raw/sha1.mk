// Why: SHA-1 (still needed by protocols such as the WebSocket handshake) is
// much faster on the CPU's SHA instructions.
//
// What: Sha1Rounds4, Sha1NextE and Sha1Schedule4.
//
// How: each is defined by FIPS 180-4 section 6.1.2, at a grain x86_64 (SHA)
// and aarch64 (FEAT_SHA1, which LLVM's sha2 enables) both implement: four
// rounds of one round group, the next group's e, and four words of the
// message schedule. Bodiless intrinsics: codegen lowers each to its target
// instructions and eval runs its definition.

/// Sha1Rounds4 is four rounds of FIPS 180-4 section 6.1.2 step 3 inside
/// round group g (rounds 20g to 20g + 19), with its f and K (0 Ch,
/// 1 Parity, 2 Maj, 3 Parity). Each [4]u32 has its oldest word in lane
/// 3: abcd holds d, c, b, a from lane 0, and a word group holds W[t + 3]
/// to W[t]. we is the four words with e added to W[t], lane 3. The result
/// is the new a to d in the same order; the e they leave is ROTL30 of
/// abcd's a, which Sha1NextE adds. g is a constant from 0 to 3 (E0706
/// otherwise). Safe in a function whose features include sha; elsewhere
/// the call needs unsafe (E1101) behind a simd.Cpu() test. On x86_64 the
/// function's features must also include sse2, inside unsafe too (E1106):
/// hosted targets always have it; on x86_64-freestanding the function's
/// cpu clause must name it, whatever the bin's cpu. O(1).
export Sha1Rounds4(abcd: [4]u32, we: [4]u32, const g: u8): [4]u32
    requires g < 4
    cpu "sha, sha2"
/// Sha1NextE is w with ROTL30 of abcd's a added to lane 3: given the state
/// before four rounds, the e they leave plus the next group's W[t]. It
/// needs the sha feature as Sha1Rounds4 does. O(1).
export Sha1NextE(abcd: [4]u32, w: [4]u32): [4]u32 cpu "sha, sha2"
/// Sha1Schedule4 is W[t] to W[t + 3] of FIPS 180-4 section 6.1.2 step 1,
/// from w0 = W[t - 16] .. W[t - 13], w1, w2 and w3 = W[t - 4] .. W[t - 1].
/// It needs the sha feature as Sha1Rounds4 does. O(1).
export Sha1Schedule4(w0: [4]u32, w1: [4]u32, w2: [4]u32, w3: [4]u32): [4]u32 cpu "sha, sha2"
