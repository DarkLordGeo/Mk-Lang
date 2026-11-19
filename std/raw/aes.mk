// Why: AES-GCM is fast only on the CPU's AES and carry-less multiply
// instructions; std.crypto reaches them here.
//
// What: AesRound, AesLastRound and Clmul64.
//
// How: each is defined by its function in FIPS 197 or by the polynomial
// product, at a grain x86_64 (AES-NI, PCLMULQDQ) and aarch64 (FEAT_AES,
// which includes PMULL) both implement. Each is bodiless, so check marks it
// an intrinsic; codegen lowers it and eval runs its definition.

/// AesRound is one round of FIPS 197's Cipher: SubBytes, ShiftRows and
/// MixColumns of state, then AddRoundKey with key. state and key are 16
/// bytes in FIPS 197's input order: byte r + 4c is row r of column c.
/// Safe in a function whose features include aes; elsewhere the call
/// needs unsafe (E1101) behind a simd.Cpu() test. On x86_64 the
/// function's features must also include sse2, inside unsafe too
/// (E1106): hosted targets always have it; on x86_64-freestanding the
/// function's cpu clause must name it, whatever the bin's cpu. O(1).
export AesRound(state: [16]u8, key: [16]u8): [16]u8 cpu "aes"
/// AesLastRound is the last round of FIPS 197's Cipher: SubBytes and
/// ShiftRows of state, then AddRoundKey with key. It needs the aes
/// feature as AesRound does. O(1).
export AesLastRound(state: [16]u8, key: [16]u8): [16]u8 cpu "aes"
/// Clmul64 is the 128-bit carry-less product of a and b as polynomials over
/// GF(2), bit i the coefficient of x^i. It needs the pclmul and aes
/// features as AesRound needs aes. O(1).
export Clmul64(a: u64, b: u64): (hi: u64, lo: u64) cpu "pclmul, aes"
