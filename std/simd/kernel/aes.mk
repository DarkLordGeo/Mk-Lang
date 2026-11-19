// Why: AES in software is slow and hard to make constant time; the CPU's AES
// round instructions are both fast and constant time, so std's ciphers call
// these kernels when the running CPU has them.
//
// What: AesEncryptBlocks and AesCtr, whole AES operations over std.raw's
// AES round intrinsics, each behind its simd.Cpu() guard, with the eight-block
// Encrypt8 and the CTR counter step.
//
// How: a kernel returns false, having written nothing, when the running CPU
// lacks aes (always on x86_64-freestanding, whose bins hold no vector
// instruction, and in the evaluator, where Cpu() is the baseline); the
// caller then runs its portable constant-time path, which is the reference.
// The round keys rk are FIPS 197 section 5.2's key schedule w[0 .. 4Nr+3]
// as bytes, 16 per round key: 176, 208 or 240 bytes for Nr = 10, 12 or 14.
// No key-assist intrinsic exists, so the caller expands the key. Eight
// blocks are in flight at once, as AES-NI's latency over its throughput
// asks (Intel's AES-NI white paper, section "parallelizing"); the round
// count is a const parameter, so each instance's rounds unroll. Each cpu
// clause names sse2 too: AES-NI works in xmm registers, so a call of the
// intrinsics needs it in context on x86_64 (E1106 needs; hosted targets
// have it, x86_64-freestanding does not).

import std.raw
import std.simd

// Load is the 16 bytes of s from i, one bounds check.
Load(s: []u8, i: int): [16]u8 {
    return simd.Vec[u8, 16].FromSlice(s, i).ToArray()
}

// Store writes a to dst from i, one bounds check.
Store(dst: mut []u8, i: int, a: [16]u8) {
    simd.Vec[u8, 16].FromArray(a).CopyTo(dst, i)
}

// Xor is a ^ b bytewise.
Xor(a: [16]u8, b: [16]u8): [16]u8 {
    return (simd.Vec[u8, 16].FromArray(a) ^ simd.Vec[u8, 16].FromArray(b)).ToArray()
}

// Rounds is Nr for a schedule of rk's length; it panics on any other.
Rounds(rk: []u8): int {
    n := rk.Len()
    if n != 176 && n != 208 && n != 240 {
        Panic("std.simd.kernel: an AES key schedule is 176, 208 or 240 bytes")
    }
    return n / 16 - 1
}

// Keys copies the NR + 1 round keys of rk.
Keys[const NR: int](rk: []u8): [15][16]u8 {
    var k: [15][16]u8 = [[0; 16]; 15]
    for r in 0..NR + 1 {
        k[r] = Load(rk, 16 * r)
    }
    return k
}

// Encrypt is FIPS 197's Cipher of block b.
Encrypt[const NR: int](k: &[15][16]u8, b: [16]u8): [16]u8 cpu "sse2, aes" {
    var s := Xor(b, k[0])
    for r in 1..NR {
        s = raw.AesRound(s, k[r])
    }
    return raw.AesLastRound(s, k[NR])
}

// Encrypt8 is Encrypt of eight blocks, interleaved round by round.
Encrypt8[const NR: int](k: &[15][16]u8, b: [8][16]u8): [8][16]u8 cpu "sse2, aes" {
    var s := b
    for j in 0..8 {
        s[j] = Xor(s[j], k[0])
    }
    for r in 1..NR {
        for j in 0..8 {
            s[j] = raw.AesRound(s[j], k[r])
        }
    }
    for j in 0..8 {
        s[j] = raw.AesLastRound(s[j], k[NR])
    }
    return s
}

// BlocksNi encrypts every whole block of src into dst.
BlocksNi[const NR: int](rk: []u8, src: []u8, dst: mut []u8) cpu "sse2, aes" {
    k := Keys[NR](rk)
    n := src.Len() / 16 * 16
    var i := 0
    for i + 128 <= n {
        var b: [8][16]u8 = [[0; 16]; 8]
        for j in 0..8 {
            b[j] = Load(src, i + 16 * j)
        }
        c := Encrypt8[NR](&k, b)
        for j in 0..8 {
            Store(dst, i + 16 * j, c[j])
        }
        i += 128
    }
    for i < n {
        Store(dst, i, Encrypt[NR](&k, Load(src, i)))
        i += 16
    }
}

/// AesEncryptBlocks encrypts every whole 16-byte block of src into dst
/// (FIPS 197's Cipher; ECB, the caller's mode decides the blocks) with the
/// round keys rk, and reports true; it reports false, writing nothing, when
/// the CPU lacks aes. Panics when rk is not 176, 208 or 240 bytes or dst
/// is shorter than src's whole blocks. O(n), eight blocks in flight;
/// allocates nothing.
export AesEncryptBlocks(rk: []u8, src: []u8, dst: mut []u8): bool {
    nr := Rounds(rk)
    if Target.os == Os.Freestanding {
        return false
    }
    if !simd.Cpu().Has("aes") {
        return false
    }
    unsafe "Cpu() reports aes, which BlocksNi's clause names" {
        if nr == 10 {
            BlocksNi[10](rk, src, dst)
        } else if nr == 12 {
            BlocksNi[12](rk, src, dst)
        } else {
            BlocksNi[14](rk, src, dst)
        }
    }
    return true
}

// Counter is c with its last four bytes the big-endian x (GCM's counter
// block, NIST SP 800-38D section 6.2).
Counter(c: [16]u8, x: u32): [16]u8 {
    var b := c
    b[12] = Cast[u8](x >> 24)
    b[13] = Cast[u8]((x >> 16) & 0xFF)
    b[14] = Cast[u8]((x >> 8) & 0xFF)
    b[15] = Cast[u8](x & 0xFF)
    return b
}

// CtrNi is AesCtr's body: eight counter blocks per step, then one, then a
// partial last block.
CtrNi[const NR: int](rk: []u8, counter: mut [16]u8, src: []u8, dst: mut []u8) cpu "sse2, aes" {
    k := Keys[NR](rk)
    c0 := *counter
    var x := (Cast[u32](c0[12]) << 24) | (Cast[u32](c0[13]) << 16) | (Cast[u32](c0[14]) << 8) | Cast[u32](c0[15])
    n := src.Len()
    var i := 0
    for i + 128 <= n {
        var b: [8][16]u8 = [[0; 16]; 8]
        for j in 0..8 {
            b[j] = Counter(c0, x +% Cast[u32](j))
        }
        ks := Encrypt8[NR](&k, b)
        for j in 0..8 {
            Store(dst, i + 16 * j, Xor(Load(src, i + 16 * j), ks[j]))
        }
        x +%= 8
        i += 128
    }
    for i + 16 <= n {
        Store(dst, i, Xor(Load(src, i), Encrypt[NR](&k, Counter(c0, x))))
        x +%= 1
        i += 16
    }
    if i < n {
        ks := Encrypt[NR](&k, Counter(c0, x))
        x +%= 1
        for j in 0..n - i {
            dst[i + j] = src[i + j] ^ ks[j]
        }
    }
    *counter = Counter(c0, x)
}

/// AesCtr sets dst to src xor the keystream of the AES encryptions (round
/// keys rk) of the counter blocks from counter, which it advances by one
/// per block, a partial last block included, with GCM's inc32 (NIST SP
/// 800-38D section 6.2: the last four bytes a big-endian u32 that wraps,
/// the first twelve unchanged), and reports true; it reports false, writing
/// nothing, when the CPU lacks aes. Panics when rk is not 176, 208 or 240
/// bytes or dst is shorter than src. O(n), eight blocks in flight;
/// allocates nothing.
export AesCtr(rk: []u8, counter: mut [16]u8, src: []u8, dst: mut []u8): bool {
    nr := Rounds(rk)
    if Target.os == Os.Freestanding {
        return false
    }
    if !simd.Cpu().Has("aes") {
        return false
    }
    unsafe "Cpu() reports aes, which CtrNi's clause names" {
        if nr == 10 {
            CtrNi[10](rk, counter, src, dst)
        } else if nr == 12 {
            CtrNi[12](rk, counter, src, dst)
        } else {
            CtrNi[14](rk, counter, src, dst)
        }
    }
    return true
}
