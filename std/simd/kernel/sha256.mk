// Why: SHA-256 is under TLS, certificates and content hashes, and the CPU's
// SHA instructions run it several times faster than portable code.
//
// What: Sha256Blocks, the SHA-256 kernel over std.raw's SHA-256 intrinsics
// behind its simd.Cpu() guard, with ShaNi and the round constants K256.
//
// How: Sha256Blocks returns false, having written nothing, when the running
// CPU lacks sha on x86_64 or sha2 on aarch64 (always on x86_64-freestanding
// and in the evaluator, where Cpu() is the baseline); the caller then runs
// its portable path, which is the reference. The shape is Intel's SHA
// extensions sample (Gulley et al., "Intel SHA Extensions", 2013): the
// state is held as (f, e, b, a) and (h, g, d, c) across the block, four
// rounds per Sha256Rounds4 with W + K added lanewise, and the schedule four
// words at a time from the last sixteen. ShaNi's clause names sse2 too: the
// SHA extensions work in xmm registers (E1106 needs on x86_64; aarch64
// ignores the name).

import std.raw
import std.simd

// K256 are FIPS 180-4 section 4.2.2's constants, four per round group.
const K256: [16][4]u32 = [
    [0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5], [0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5],
    [0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3], [0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174],
    [0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc], [0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da],
    [0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7], [0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967],
    [0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13], [0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85],
    [0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3], [0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070],
    [0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5], [0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3],
    [0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208], [0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2],
]

// BeWords is the four big-endian words of data from i.
BeWords(data: []u8, i: int): [4]u32 {
    v := simd.Vec[u8, 16].FromSlice(data, i)
    return v.Shuffle(v, [3, 2, 1, 0, 7, 6, 5, 4, 11, 10, 9, 8, 15, 14, 13, 12]).Reinterpret[u32, 4]().ToArray()
}

// AddWords is a + b lanewise, wrapping.
AddWords(a: [4]u32, b: [4]u32): [4]u32 {
    return (simd.Vec[u32, 4].FromArray(a) +% simd.Vec[u32, 4].FromArray(b)).ToArray()
}

// AddState is a + b lanewise, wrapping.
AddState(a: [8]u32, b: [8]u32): [8]u32 {
    return (simd.Vec[u32, 8].FromArray(a) +% simd.Vec[u32, 8].FromArray(b)).ToArray()
}

// ShaNi is Sha256Blocks' body.
ShaNi(h: mut [8]u32, data: []u8) cpu "sse2, sha, sha2" {
    var s: [8]u32 = [h[5], h[4], h[1], h[0], h[7], h[6], h[3], h[2]]
    n := data.Len() / 64 * 64
    var i := 0
    for i < n {
        save := s
        // w0 .. w3 are the last sixteen schedule words, oldest first; each
        // step shifts them down (the last four steps need no new words),
        // so no word is indexed at run time and all four stay in
        // registers.
        var w0 := BeWords(data, i)
        var w1 := BeWords(data, i + 16)
        var w2 := BeWords(data, i + 32)
        var w3 := BeWords(data, i + 48)
        for q in 0..16 {
            s = raw.Sha256Rounds4(s, AddWords(w0, K256[q]))
            w4 := if q < 12 { raw.Sha256Schedule4(w0, w1, w2, w3) } else { w3 }
            w0 = w1
            w1 = w2
            w2 = w3
            w3 = w4
        }
        s = AddState(s, save)
        i += 64
    }
    h[0] = s[3]
    h[1] = s[2]
    h[2] = s[7]
    h[3] = s[6]
    h[4] = s[1]
    h[5] = s[0]
    h[6] = s[5]
    h[7] = s[4]
}

/// Sha256Blocks compresses every whole 64-byte block of data into h, in
/// order (FIPS 180-4 section 6.2.2; h is H0 .. H7), and reports true; it
/// reports false, writing nothing, when the CPU lacks sha (sha2 on
/// aarch64). data.Len() % 64
/// trailing bytes are ignored. O(n); allocates nothing.
export Sha256Blocks(h: mut [8]u32, data: []u8): bool {
    if Target.os == Os.Freestanding {
        return false
    }
    if !simd.Cpu().Has("sha") && !simd.Cpu().Has("sha2") {
        return false
    }
    unsafe "Cpu() reports sha or sha2, the feature ShaNi's clause names on this architecture" {
        ShaNi(h, data)
    }
    return true
}
