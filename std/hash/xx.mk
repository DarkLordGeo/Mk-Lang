// Why: non-cryptographic hashing of large inputs (checksums, deduplication,
// cache keys) wants the speed of xxHash and its exact output.
//
// What: Xx (XXH3-64, seed 0, the default secret) and Xx64 (XXH64, seeded).
// Xx64State (xx64state.mk) shares XXH64's merge and tail with Xx64.
//
// How: after the xxHash specification (Y. Collet, xxhash_spec.md, v0.8) and
// xxhash.h. All arithmetic is the algorithm's mod 2^64 (+%, *%). No
// allocation.

import std.raw

const XxPrime32A: u64 = 0x9E3779B1
const XxPrime32B: u64 = 0x85EBCA77
const XxPrime32C: u64 = 0xC2B2AE3D
const XxPrime1: u64 = 0x9E3779B185EBCA87
const XxPrime2: u64 = 0xC2B2AE3D27D4EB4F
const XxPrime3: u64 = 0x165667B19E3779F9
const XxPrime4: u64 = 0x85EBCA77C2B2AE63
const XxPrime5: u64 = 0x27D4EB2F165667C5

// XxSecret is XXH3's default secret (kSecret, 192 bytes).
const XxSecret: [192]u8 = [
    0xb8, 0xfe, 0x6c, 0x39, 0x23, 0xa4, 0x4b, 0xbe, 0x7c, 0x01, 0x81, 0x2c, 0xf7, 0x21, 0xad, 0x1c,
    0xde, 0xd4, 0x6d, 0xe9, 0x83, 0x90, 0x97, 0xdb, 0x72, 0x40, 0xa4, 0xa4, 0xb7, 0xb3, 0x67, 0x1f,
    0xcb, 0x79, 0xe6, 0x4e, 0xcc, 0xc0, 0xe5, 0x78, 0x82, 0x5a, 0xd0, 0x7d, 0xcc, 0xff, 0x72, 0x21,
    0xb8, 0x08, 0x46, 0x74, 0xf7, 0x43, 0x24, 0x8e, 0xe0, 0x35, 0x90, 0xe6, 0x81, 0x3a, 0x26, 0x4c,
    0x3c, 0x28, 0x52, 0xbb, 0x91, 0xc3, 0x00, 0xcb, 0x88, 0xd0, 0x65, 0x8b, 0x1b, 0x53, 0x2e, 0xa3,
    0x71, 0x64, 0x48, 0x97, 0xa2, 0x0d, 0xf9, 0x4e, 0x38, 0x19, 0xef, 0x46, 0xa9, 0xde, 0xac, 0xd8,
    0xa8, 0xfa, 0x76, 0x3f, 0xe3, 0x9c, 0x34, 0x3f, 0xf9, 0xdc, 0xbb, 0xc7, 0xc7, 0x0b, 0x4f, 0x1d,
    0x8a, 0x51, 0xe0, 0x4b, 0xcd, 0xb4, 0x59, 0x31, 0xc8, 0x9f, 0x7e, 0xc9, 0xd9, 0x78, 0x73, 0x64,
    0xea, 0xc5, 0xac, 0x83, 0x34, 0xd3, 0xeb, 0xc3, 0xc5, 0x81, 0xa0, 0xff, 0xfa, 0x13, 0x63, 0xeb,
    0x17, 0x0d, 0xdd, 0x51, 0xb7, 0xf0, 0xda, 0x49, 0xd3, 0x16, 0x55, 0x26, 0x29, 0xd4, 0x68, 0x9e,
    0x2b, 0x16, 0xbe, 0x58, 0x7d, 0x47, 0xa1, 0xfc, 0x8f, 0xf8, 0xb8, 0xd1, 0x7a, 0xd0, 0x31, 0xce,
    0x45, 0xcb, 0x3a, 0x8f, 0x95, 0x16, 0x04, 0x28, 0xaf, 0xd7, 0xfb, 0xca, 0xbb, 0x4b, 0x40, 0x7e,
]

// Xx64Round is XXH64's round: rotl(acc + lane * P2, 31) * P1.
Xx64Round(acc: u64, lane: u64): u64 {
    return raw.RotL64(acc +% lane *% XxPrime2, 31) *% XxPrime1
}

// Xx64Avalanche is XXH64's final mix (XXH3 uses it for lengths 0 to 3).
Xx64Avalanche(x: u64): u64 {
    var h := x
    h = (h ^ (h >> 33)) *% XxPrime2
    h = (h ^ (h >> 29)) *% XxPrime3
    return h ^ (h >> 32)
}

/// Returns XXH64 of data with seed. It is not cryptographic: an attacker
/// can find inputs that collide, so for keys an attacker chooses use Sip13.
/// O(n); allocates nothing.
export Xx64(data: []u8, seed: u64): u64 {
    n := data.Len()
    var i := 0
    var acc: u64 = 0
    if n >= 32 {
        var v1 := seed +% XxPrime1 +% XxPrime2
        var v2 := seed +% XxPrime2
        var v3 := seed
        var v4 := seed -% XxPrime1
        for n - i >= 32 {
            w := data[i..n][0..32]
            v1 = Xx64Round(v1, Le64(w, 0))
            v2 = Xx64Round(v2, Le64(w, 8))
            v3 = Xx64Round(v3, Le64(w, 16))
            v4 = Xx64Round(v4, Le64(w, 24))
            i += 32
        }
        acc = Xx64Merge(v1, v2, v3, v4)
    } else {
        acc = seed +% XxPrime5
    }
    return Xx64Finish(acc +% Trunc[u64](n), data[i..n])
}

// Xx64Merge folds XXH64's four lane accumulators into one, after the last
// whole stripe.
Xx64Merge(v1: u64, v2: u64, v3: u64, v4: u64): u64 {
    var acc := raw.RotL64(v1, 1) +% raw.RotL64(v2, 7) +% raw.RotL64(v3, 12) +% raw.RotL64(v4, 18)
    acc = (acc ^ Xx64Round(0, v1)) *% XxPrime1 +% XxPrime4
    acc = (acc ^ Xx64Round(0, v2)) *% XxPrime1 +% XxPrime4
    acc = (acc ^ Xx64Round(0, v3)) *% XxPrime1 +% XxPrime4
    return (acc ^ Xx64Round(0, v4)) *% XxPrime1 +% XxPrime4
}

// Xx64Finish runs XXH64's tail, the fewer than 32 bytes after the last
// whole stripe, by 8, 4 and 1 bytes into acc (which already holds the
// length), and the final mix.
Xx64Finish(acc0: u64, tail: []u8): u64 {
    n := tail.Len()
    var acc := acc0
    var i := 0
    for n - i >= 8 {
        acc = acc ^ Xx64Round(0, Le64(tail, i))
        acc = raw.RotL64(acc, 27) *% XxPrime1 +% XxPrime4
        i += 8
    }
    if n - i >= 4 {
        acc = acc ^ (u64(Le32(tail, i)) *% XxPrime1)
        acc = raw.RotL64(acc, 23) *% XxPrime2 +% XxPrime3
        i += 4
    }
    for i < n {
        acc = acc ^ (u64(tail[i]) *% XxPrime5)
        acc = raw.RotL64(acc, 11) *% XxPrime1
        i += 1
    }
    return Xx64Avalanche(acc)
}

// XxAvalanche is XXH3's final mix.
XxAvalanche(x: u64): u64 {
    h := (x ^ (x >> 37)) *% 0x165667919E3779F9
    return h ^ (h >> 32)
}

// XxWords is the secret's little-endian words at byte offsets 8m + r,
// m = 0 to 23, and 0 for a word that would pass the end. It runs in the
// evaluator only, for the four key tables below.
XxWords(r: int): [24]u64 {
    var w: [24]u64 = [0; 24]
    for m in 0..24 {
        s := 8 * m + r
        if s + 8 <= 192 {
            var x: u64 = 0
            for k in 0..8 {
                x = x | (u64(XxSecret[s + k]) << (8 * k))
            }
            w[m] = x
        }
    }
    return w
}

// The secret's words at offsets 8m (XxK0), 8m + 1 (XxK1, the last stripe
// at 121), 8m + 3 (XxK3, lengths 129 to 240 and the merge at 11) and
// 8m + 7 (XxK7, the last mix at 119), computed at compile time.
const XxK0: [24]u64 = XxWords(0)
const XxK1: [24]u64 = XxWords(1)
const XxK3: [24]u64 = XxWords(3)
const XxK7: [24]u64 = XxWords(7)

// XxMix16 is XXH3's mix16B of the 16 bytes of b at i against the secret
// words ka and kb.
XxMix16(b: []u8, i: int, ka: u64, kb: u64): u64 {
    return Fold(Le64(b, i) ^ ka, Le64(b, i + 8) ^ kb)
}

// ByteSwap64 reverses the bytes of x (one bswap).
ByteSwap64(x: u64): u64 {
    return (x << 56) | ((x & 0xFF00) << 40) | ((x & 0xFF0000) << 24) | ((x & 0xFF000000) << 8) |
        ((x >> 8) & 0xFF000000) | ((x >> 24) & 0xFF0000) | ((x >> 40) & 0xFF00) | (x >> 56)
}

/// Returns XXH3-64 of data with seed 0 and the default secret. It is not
/// cryptographic: for keys an attacker chooses use Sip13. Inputs of up to
/// 240 bytes take one of five fixed paths of at most 15 multiplies; longer
/// inputs are read in 64-byte stripes. The entry is small enough to inline,
/// so hashing a 9 to 16 byte key costs no call. O(n); allocates nothing.
export Xx(data: []u8): u64 {
    n := data.Len()
    if n > 8 {
        if n <= 16 {
            return Xx9To16(Le64(data, 0), Le64(data, n - 8), n)
        }
        return XxOver16(data)
    }
    return XxUpTo16(data)
}

// Xx9To16 is XXH3-64 for 9 to 16 bytes from the first and the last 8 (they
// overlap below 16).
Xx9To16(a: u64, b: u64, n: int): u64 {
    lo := a ^ (XxK0[3] ^ XxK0[4])
    hi := b ^ (XxK0[5] ^ XxK0[6])
    return XxAvalanche(Trunc[u64](n) +% ByteSwap64(lo) +% hi +% Fold(lo, hi))
}

// XxOver16 is XXH3-64 for lengths over 16.
XxOver16(data: []u8): u64 {
    n := data.Len()
    if n <= 128 {
        var acc := Trunc[u64](n) *% XxPrime1
        if n > 32 {
            if n > 64 {
                if n > 96 {
                    acc = acc +% XxMix16(data, 48, XxK0[12], XxK0[13])
                    acc = acc +% XxMix16(data, n - 64, XxK0[14], XxK0[15])
                }
                acc = acc +% XxMix16(data, 32, XxK0[8], XxK0[9])
                acc = acc +% XxMix16(data, n - 48, XxK0[10], XxK0[11])
            }
            acc = acc +% XxMix16(data, 16, XxK0[4], XxK0[5])
            acc = acc +% XxMix16(data, n - 32, XxK0[6], XxK0[7])
        }
        acc = acc +% XxMix16(data, 0, XxK0[0], XxK0[1])
        acc = acc +% XxMix16(data, n - 16, XxK0[2], XxK0[3])
        return XxAvalanche(acc)
    }
    if n <= 240 {
        var acc := Trunc[u64](n) *% XxPrime1
        for k in 0..8 {
            acc = acc +% XxMix16(data, 16 * k, XxK0[2 * k], XxK0[2 * k + 1])
        }
        acc = XxAvalanche(acc)
        rounds := n / 16
        for k in 8..rounds {
            acc = acc +% XxMix16(data, 16 * k, XxK3[2 * (k - 8)], XxK3[2 * (k - 8) + 1])
        }
        acc = acc +% XxMix16(data, n - 16, XxK7[14], XxK7[15])
        return XxAvalanche(acc)
    }
    return XxLong(data)
}

// XxUpTo16 is XXH3-64 for lengths 0 to 16.
XxUpTo16(data: []u8): u64 {
    n := data.Len()
    if n > 8 {
        return Xx9To16(Le64(data, 0), Le64(data, n - 8), n)
    }
    if n >= 4 {
        x := (u64(Le32(data, n - 4)) +% (u64(Le32(data, 0)) << 32)) ^ (XxK0[1] ^ XxK0[2])
        var h := x ^ raw.RotL64(x, 49) ^ raw.RotL64(x, 24)
        h = h *% 0x9FB21C651E98DF25
        h = h ^ ((h >> 35) +% Trunc[u64](n))
        h = h *% 0x9FB21C651E98DF25
        return h ^ (h >> 28)
    }
    if n > 0 {
        c := (u32(data[0]) << 16) | (u32(data[n >> 1]) << 24) | u32(data[n - 1]) | (Trunc[u32](n) << 8)
        flip := Trunc[u32](XxK0[0]) ^ Trunc[u32](XxK0[0] >> 32)
        return Xx64Avalanche(u64(c ^ flip))
    }
    return Xx64Avalanche(XxK0[7] ^ XxK0[8])
}

// XxAcc is XXH3's accumulate_512: the 64-byte stripe of b at i against the
// secret words key[m..m + 8].
XxAcc(acc: mut [8]u64, b: []u8, i: int, key: &[24]u64, m: int) {
    w := b[i..b.Len()][0..64]
    for k in 0..8 {
        v := Le64(w, 8 * k)
        x := v ^ key[m + k]
        acc[k ^ 1] = acc[k ^ 1] +% v
        acc[k] = acc[k] +% (x & 0xFFFFFFFF) *% (x >> 32)
    }
}

// XxLong is XXH3-64 for lengths over 240.
XxLong(data: []u8): u64 {
    n := data.Len()
    var acc: [8]u64 = [XxPrime32C, XxPrime1, XxPrime2, XxPrime3, XxPrime4, XxPrime32B, XxPrime5, XxPrime32A]
    blocks := (n - 1) / 1024
    for k in 0..blocks {
        blk := data[1024 * k..n][0..1024]
        for j in 0..16 {
            XxAcc(mut acc, blk, 64 * j, &XxK0, j)
        }
        for x in 0..8 {
            var a := acc[x]
            a = a ^ (a >> 47)
            a = a ^ XxK0[16 + x]
            acc[x] = a *% XxPrime32A
        }
    }
    stripes := (n - 1 - 1024 * blocks) / 64
    for j in 0..stripes {
        XxAcc(mut acc, data, 1024 * blocks + 64 * j, &XxK0, j)
    }
    XxAcc(mut acc, data, n - 64, &XxK1, 15)
    var r := Trunc[u64](n) *% XxPrime1
    for x in 0..4 {
        r = r +% Fold(acc[2 * x] ^ XxK3[1 + 2 * x], acc[2 * x + 1] ^ XxK3[2 + 2 * x])
    }
    return XxAvalanche(r)
}
