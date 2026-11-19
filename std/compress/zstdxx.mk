// Why: a Zstandard frame's checksum is XXH64 over the decoded bytes, which
// the decoder hands out a piece at a time from a window that wraps, while
// hash.Xx64 is one-shot.
//
// What: ZxState with ZxNew, ZxUpdate and ZxDigest: streaming XXH64 with seed
// 0 (RFC 8878 section 3.1.1 keeps its low 32 bits).
//
// How: XXH64 as xxhash.h's XXH64_update and XXH64_digest compute it: four
// lanes over 32-byte stripes, a partial stripe kept in the state. The tests
// check it against hash.Xx64 over every split of the input. Part of the
// zstd decoder (zstd*.mk).

import std.raw

const ZxPrime1: u64 = 0x9E3779B185EBCA87
const ZxPrime2: u64 = 0xC2B2AE3D27D4EB4F
const ZxPrime3: u64 = 0x165667B19E3779F9
const ZxPrime4: u64 = 0x85EBCA77C2B2AE63
const ZxPrime5: u64 = 0x27D4EB2F165667C5

// ZxState is XXH64's state with seed 0: the four lane accumulators, the
// bytes absorbed, and the partial stripe tail[0..tn].
struct ZxState {
    v1: u64
    v2: u64
    v3: u64
    v4: u64
    total: u64
    tail: [32]u8
    tn: int
}

// ZxNew is the state over no input.
ZxNew(): ZxState {
    return ZxState(v1: ZxPrime1 +% ZxPrime2, v2: ZxPrime2, v3: 0, v4: 0 -% ZxPrime1, total: 0, tail: [0; 32], tn: 0)
}

// ZxRound is XXH64's round: rotl(acc + lane * P2, 31) * P1.
ZxRound(acc: u64, lane: u64): u64 {
    return raw.RotL64(acc +% lane *% ZxPrime2, 31) *% ZxPrime1
}

// ZxStripe absorbs the 32 bytes w[0..32] into the lanes.
ZxStripe(x: mut ZxState, w: []u8) {
    x.v1 = ZxRound(x.v1, Le64(w, 0))
    x.v2 = ZxRound(x.v2, Le64(w, 8))
    x.v3 = ZxRound(x.v3, Le64(w, 16))
    x.v4 = ZxRound(x.v4, Le64(w, 24))
}

// ZxUpdate absorbs data: the partial stripe first, then whole stripes
// straight from data, then the rest into the tail. O(n); allocates
// nothing.
ZxUpdate(x: mut ZxState, data: []u8) {
    n := data.Len()
    x.total = x.total +% Trunc[u64](n)
    var i := 0
    if x.tn > 0 {
        k := if 32 - x.tn > n { n } else { 32 - x.tn }
        tn := x.tn
        (mut x.tail[tn..tn + k]).CopyFrom(data[0..k])
        x.tn = tn + k
        i = k
        if x.tn < 32 {
            return
        }
        t := x.tail
        ZxStripe(x, t[..])
        x.tn = 0
    }
    for n - i >= 32 {
        ZxStripe(x, data[i..i + 32])
        i += 32
    }
    if i < n {
        (mut x.tail[0..n - i]).CopyFrom(data[i..n])
        x.tn = n - i
    }
}

// ZxDigest is XXH64 of everything absorbed; x is unchanged.
ZxDigest(x: &ZxState): u64 {
    var acc: u64 = 0
    if x.total >= 32 {
        acc = raw.RotL64(x.v1, 1) +% raw.RotL64(x.v2, 7) +% raw.RotL64(x.v3, 12) +% raw.RotL64(x.v4, 18)
        acc = (acc ^ ZxRound(0, x.v1)) *% ZxPrime1 +% ZxPrime4
        acc = (acc ^ ZxRound(0, x.v2)) *% ZxPrime1 +% ZxPrime4
        acc = (acc ^ ZxRound(0, x.v3)) *% ZxPrime1 +% ZxPrime4
        acc = (acc ^ ZxRound(0, x.v4)) *% ZxPrime1 +% ZxPrime4
    } else {
        acc = ZxPrime5
    }
    acc = acc +% x.total
    t := x.tail[0..x.tn]
    n := t.Len()
    var i := 0
    for n - i >= 8 {
        acc = acc ^ ZxRound(0, Le64(t, i))
        acc = raw.RotL64(acc, 27) *% ZxPrime1 +% ZxPrime4
        i += 8
    }
    if n - i >= 4 {
        acc = acc ^ (u64(Le32(t, i)) *% ZxPrime1)
        acc = raw.RotL64(acc, 23) *% ZxPrime2 +% ZxPrime3
        i += 4
    }
    for i < n {
        acc = acc ^ (u64(t[i]) *% ZxPrime5)
        acc = raw.RotL64(acc, 11) *% ZxPrime1
        i += 1
    }
    acc = (acc ^ (acc >> 33)) *% ZxPrime2
    acc = (acc ^ (acc >> 29)) *% ZxPrime3
    return acc ^ (acc >> 32)
}
