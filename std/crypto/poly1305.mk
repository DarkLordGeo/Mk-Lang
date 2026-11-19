// Why: Poly1305 is the authenticator of ChaCha20-Poly1305.
//
// What: Poly with PolyNew, PolyBlocks, PolyPadded and PolySum, and the
// one-shot PolyMac.
//
// How: the Poly1305 one-time authenticator of RFC 8439 section 2.5, after Go's
// x/crypto/internal/poly1305/sum_generic.go (Zig's poly1305.zig is the same
// method): the accumulator h below 2^131 in three u64 (h0, h1, and h2 of at
// most 7), r clamped into two u64 whose top four bits are clear, 64x64
// products by raw.Mul128, carries by raw.AddCarry64 and raw.SubBorrow64
// (an adc or sbb chain, no branch in any build), the partial
// reduction c 2^130 = 5c after each block, and the final h - p kept or not
// by mask. Constant time: no branch or index depends on the key, the
// message or h; the only branches are on lengths.

import std.raw

// Poly is a Poly1305 computation: the accumulator, r and s.
struct Poly {
    h0: u64
    h1: u64
    h2: u64
    r0: u64
    r1: u64
    s0: u64
    s1: u64
}

// Add64 is x + y + c (c is 0 or 1) and its carry out (Go's
// math/bits.Add64), by raw.AddCarry64.
Add64(x: u64, y: u64, c: u64): (c: u64, s: u64) {
    r := raw.AddCarry64(x, y, c)
    return (c: r.carry, s: r.sum)
}

// Sub64 is x - y - b (b is 0 or 1) and its borrow out (math/bits.Sub64),
// by raw.SubBorrow64.
Sub64(x: u64, y: u64, b: u64): (b: u64, d: u64) {
    r := raw.SubBorrow64(x, y, b)
    return (b: r.borrow, d: r.diff)
}

// PolyNew starts a computation under the 32-byte one-time key r || s
// (RFC 8439 2.5: r clamped with 0x0ffffffc0ffffffc0ffffffc0fffffff).
PolyNew(key: []u8): Poly {
    return Poly(h0: 0, h1: 0, h2: 0, r0: Le64(key, 0) & 0x0FFFFFFC0FFFFFFF, r1: Le64(key, 8) & 0x0FFFFFFC0FFFFFFC,
        s0: Le64(key, 16), s1: Le64(key, 24))
}

// PolyBlocks absorbs every whole 16-byte block of data, each with hibit
// (1 for a full block, so the block is m + 2^128) added above it;
// data.Len() % 16 trailing bytes are ignored. O(data.Len()).
PolyBlocks(p: mut Poly, data: []u8, hibit: u64) {
    n := data.Len()
    var h0 := p.h0
    var h1 := p.h1
    var h2 := p.h2
    r0 := p.r0
    r1 := p.r1
    var i := 0
    for n - i >= 16 {
        b := data[i..n][0..16]
        a0 := Add64(h0, Le64(b, 0), 0)
        a1 := Add64(h1, Le64(b, 8), a0.c)
        h0 = a0.s
        h1 = a1.s
        h2 = h2 +% a1.c +% hibit
        // h is at most 7 * 2^128 and r below 2^124, so h2 * r fits 64 bits
        // and the product has four limbs.
        h0r0 := MulW(h0, r0)
        h1r0 := MulW(h1, r0)
        h0r1 := MulW(h0, r1)
        h1r1 := MulW(h1, r1)
        h2r0 := h2 *% r0
        h2r1 := h2 *% r1
        m1 := Add64(h1r0.lo, h0r1.lo, 0)
        m1hi := h1r0.hi +% h0r1.hi +% m1.c
        m2 := Add64(h1r1.lo, h2r0, 0)
        m2hi := h1r1.hi +% m2.c
        t1 := Add64(m1.s, h0r0.hi, 0)
        t2 := Add64(m2.s, m1hi, t1.c)
        t3 := h2r1 +% m2hi +% t2.c
        // Split at 2^130: h = t mod 2^130, cc = 4c. Add cc, then cc / 4.
        h0 = h0r0.lo
        h1 = t1.s
        h2 = t2.s & 3
        cc0 := t2.s & 0xFFFFFFFFFFFFFFFC
        cc1 := t3
        b0 := Add64(h0, cc0, 0)
        b1 := Add64(h1, cc1, b0.c)
        h2 = h2 +% b1.c
        d0 := Add64(b0.s, (cc0 >> 2) | (cc1 << 62), 0)
        d1 := Add64(b1.s, cc1 >> 2, d0.c)
        h0 = d0.s
        h1 = d1.s
        h2 = h2 +% d1.c
        i += 16
    }
    p.h0 = h0
    p.h1 = h1
    p.h2 = h2
}

// PolyPadded absorbs data zero-padded to a multiple of 16 bytes, every
// block full (RFC 8439 2.8's pad16).
PolyPadded(p: mut Poly, data: []u8) {
    n := data.Len()
    PolyBlocks(p, data, 1)
    r := n % 16
    if r != 0 {
        var b: [16]u8 = [0; 16]
        (mut b[0..r]).CopyFrom(data[n - r..n])
        PolyBlocks(p, b[..], 1)
    }
}

// PolySum is the tag: h fully reduced (h - p kept when it does not
// borrow), plus s, mod 2^128, little-endian.
PolySum(p: &Poly): [16]u8 {
    a := Sub64(p.h0, 0xFFFFFFFFFFFFFFFB, 0)
    b := Sub64(p.h1, 0xFFFFFFFFFFFFFFFF, a.b)
    c := Sub64(p.h2, 3, b.b)
    m := 0 -% c.b
    h0 := (p.h0 & m) | (a.d & ~m)
    h1 := (p.h1 & m) | (b.d & ~m)
    x := Add64(h0, p.s0, 0)
    y := Add64(h1, p.s1, x.c)
    var out: [16]u8 = [0; 16]
    PutLe64(mut out[..], 0, x.s)
    PutLe64(mut out[..], 8, y.s)
    return out
}

// PolyMac is Poly1305 of msg under the one-time key (RFC 8439 2.5.1): a
// last partial block gets a 1 byte after it and no 2^128.
PolyMac(key: []u8, msg: []u8): [16]u8 {
    var p := PolyNew(key)
    n := msg.Len()
    PolyBlocks(mut p, msg, 1)
    r := n % 16
    if r != 0 {
        var b: [16]u8 = [0; 16]
        (mut b[0..r]).CopyFrom(msg[n - r..n])
        b[r] = 1
        PolyBlocks(mut p, b[..], 0)
    }
    return PolySum(&p)
}
