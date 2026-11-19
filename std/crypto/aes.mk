// Why: AES-GCM needs the AES block cipher on CPUs without AES
// instructions too, and a table-based AES leaks its key through the cache.
//
// What: the portable AES encryption used by aesgcm.mk: the key schedule
// AesSchedule and the four-block encryption AesEncrypt4, with the bitsliced
// steps AesSbox, AesShiftRows, AesMixColumns and AesOrtho.
//
// How: the AES block cipher of FIPS 197 (encryption only, 128- and 256-bit
// keys, and 192 for the key schedule's sake) by BearSSL's aes_ct64 (Thomas
// Pornin, src/symcipher/aes_ct64.c and aes_ct64_enc.c): four blocks bitsliced
// into eight 64-bit words, the S-box as Boyar and Peralta's 113-gate circuit
// ("A new combinational logic minimization technique with applications to
// cryptology", 2009), ShiftRows and MixColumns as masks and rotations.
// Constant time by construction: no table, no branch and no index depends on
// the key or the data; the key schedule uses the same S-box circuit.
//
// The round keys are kept in two forms: rk, FIPS 197 section 5.2's w as
// bytes, for std.simd.kernel's AES instructions, and sk, the bitsliced
// keys in BearSSL's compressed form (two words per round key, every
// fourth bit), expanded word by word as each round adds them.

// AesRcon is FIPS 197 section 5.2's Rcon[i] for i = 1 to 10, its top byte.
const AesRcon: [10]u8 = [0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36]

// AesSwap2 exchanges the odd bits of x with the even bits of y.
AesSwap2(x: mut u64, y: mut u64) {
    a := *x
    b := *y
    *x = (a & 0x5555555555555555) | ((b & 0x5555555555555555) << 1)
    *y = ((a & 0xAAAAAAAAAAAAAAAA) >> 1) | (b & 0xAAAAAAAAAAAAAAAA)
}

// AesSwap4 is AesSwap2 on pairs of bits.
AesSwap4(x: mut u64, y: mut u64) {
    a := *x
    b := *y
    *x = (a & 0x3333333333333333) | ((b & 0x3333333333333333) << 2)
    *y = ((a & 0xCCCCCCCCCCCCCCCC) >> 2) | (b & 0xCCCCCCCCCCCCCCCC)
}

// AesSwap8 is AesSwap2 on nibbles.
AesSwap8(x: mut u64, y: mut u64) {
    a := *x
    b := *y
    *x = (a & 0x0F0F0F0F0F0F0F0F) | ((b & 0x0F0F0F0F0F0F0F0F) << 4)
    *y = ((a & 0xF0F0F0F0F0F0F0F0) >> 4) | (b & 0xF0F0F0F0F0F0F0F0)
}

// AesOrtho moves between the interleaved and the bitsliced forms of q (an
// involution: br_aes_ct64_ortho).
AesOrtho(q: mut [8]u64) {
    var q0 := q[0]
    var q1 := q[1]
    var q2 := q[2]
    var q3 := q[3]
    var q4 := q[4]
    var q5 := q[5]
    var q6 := q[6]
    var q7 := q[7]
    AesSwap2(mut q0, mut q1)
    AesSwap2(mut q2, mut q3)
    AesSwap2(mut q4, mut q5)
    AesSwap2(mut q6, mut q7)
    AesSwap4(mut q0, mut q2)
    AesSwap4(mut q1, mut q3)
    AesSwap4(mut q4, mut q6)
    AesSwap4(mut q5, mut q7)
    AesSwap8(mut q0, mut q4)
    AesSwap8(mut q1, mut q5)
    AesSwap8(mut q2, mut q6)
    AesSwap8(mut q3, mut q7)
    *q = [q0, q1, q2, q3, q4, q5, q6, q7]
}

// AesSbox applies the S-box to every byte of the bitsliced state q: the
// top linear map, the GF(2^4)-based inversion and the bottom linear map
// of Boyar and Peralta's circuit (br_aes_ct64_bitslice_Sbox).
AesSbox(q: mut [8]u64) {
    x0 := q[7]
    x1 := q[6]
    x2 := q[5]
    x3 := q[4]
    x4 := q[3]
    x5 := q[2]
    x6 := q[1]
    x7 := q[0]

    y14 := x3 ^ x5
    y13 := x0 ^ x6
    y9 := x0 ^ x3
    y8 := x0 ^ x5
    t0 := x1 ^ x2
    y1 := t0 ^ x7
    y4 := y1 ^ x3
    y12 := y13 ^ y14
    y2 := y1 ^ x0
    y5 := y1 ^ x6
    y3 := y5 ^ y8
    t1 := x4 ^ y12
    y15 := t1 ^ x5
    y20 := t1 ^ x1
    y6 := y15 ^ x7
    y10 := y15 ^ t0
    y11 := y20 ^ y9
    y7 := x7 ^ y11
    y17 := y10 ^ y11
    y19 := y10 ^ y8
    y16 := t0 ^ y11
    y21 := y13 ^ y16
    y18 := x0 ^ y16

    t2 := y12 & y15
    t3 := y3 & y6
    t4 := t3 ^ t2
    t5 := y4 & x7
    t6 := t5 ^ t2
    t7 := y13 & y16
    t8 := y5 & y1
    t9 := t8 ^ t7
    t10 := y2 & y7
    t11 := t10 ^ t7
    t12 := y9 & y11
    t13 := y14 & y17
    t14 := t13 ^ t12
    t15 := y8 & y10
    t16 := t15 ^ t12
    t17 := t4 ^ t14
    t18 := t6 ^ t16
    t19 := t9 ^ t14
    t20 := t11 ^ t16
    t21 := t17 ^ y20
    t22 := t18 ^ y19
    t23 := t19 ^ y21
    t24 := t20 ^ y18

    t25 := t21 ^ t22
    t26 := t21 & t23
    t27 := t24 ^ t26
    t28 := t25 & t27
    t29 := t28 ^ t22
    t30 := t23 ^ t24
    t31 := t22 ^ t26
    t32 := t31 & t30
    t33 := t32 ^ t24
    t34 := t23 ^ t33
    t35 := t27 ^ t33
    t36 := t24 & t35
    t37 := t36 ^ t34
    t38 := t27 ^ t36
    t39 := t29 & t38
    t40 := t25 ^ t39

    t41 := t40 ^ t37
    t42 := t29 ^ t33
    t43 := t29 ^ t40
    t44 := t33 ^ t37
    t45 := t42 ^ t41
    z0 := t44 & y15
    z1 := t37 & y6
    z2 := t33 & x7
    z3 := t43 & y16
    z4 := t40 & y1
    z5 := t29 & y7
    z6 := t42 & y11
    z7 := t45 & y17
    z8 := t41 & y10
    z9 := t44 & y12
    z10 := t37 & y3
    z11 := t33 & y4
    z12 := t43 & y13
    z13 := t40 & y5
    z14 := t29 & y2
    z15 := t42 & y9
    z16 := t45 & y14
    z17 := t41 & y8

    t46 := z15 ^ z16
    t47 := z10 ^ z11
    t48 := z5 ^ z13
    t49 := z9 ^ z10
    t50 := z2 ^ z12
    t51 := z2 ^ z5
    t52 := z7 ^ z8
    t53 := z0 ^ z3
    t54 := z6 ^ z7
    t55 := z16 ^ z17
    t56 := z12 ^ t48
    t57 := t50 ^ t53
    t58 := z4 ^ t46
    t59 := z3 ^ t54
    t60 := t46 ^ t57
    t61 := z14 ^ t57
    t62 := t52 ^ t58
    t63 := t49 ^ t58
    t64 := z4 ^ t59
    t65 := t61 ^ t62
    t66 := z1 ^ t63
    s0 := t59 ^ t63
    s6 := t56 ^ ~t62
    s7 := t48 ^ ~t60
    t67 := t64 ^ t65
    s3 := t53 ^ t66
    s4 := t51 ^ t66
    s5 := t47 ^ t65
    s1 := t64 ^ ~s3
    s2 := t55 ^ ~t67

    *q = [s7, s6, s5, s4, s3, s2, s1, s0]
}

// AesShiftRows is FIPS 197's ShiftRows on each bitsliced word.
AesShiftRows(q: mut [8]u64) {
    for i in 0..8 {
        x := q[i]
        q[i] = (x & 0x000000000000FFFF) | ((x & 0x00000000FFF00000) >> 4) |
            ((x & 0x00000000000F0000) << 12) | ((x & 0x0000FF0000000000) >> 8) |
            ((x & 0x000000FF00000000) << 8) | ((x & 0xF000000000000000) >> 12) |
            ((x & 0x0FFF000000000000) << 4)
    }
}

// AesRotr32 rotates x by 32 bits.
AesRotr32(x: u64): u64 {
    return (x << 32) | (x >> 32)
}

// AesRotr16 rotates x right by 16 bits.
AesRotr16(x: u64): u64 {
    return (x >> 16) | (x << 48)
}

// AesMixColumns is FIPS 197's MixColumns on the bitsliced state.
AesMixColumns(q: mut [8]u64) {
    q0 := q[0]
    q1 := q[1]
    q2 := q[2]
    q3 := q[3]
    q4 := q[4]
    q5 := q[5]
    q6 := q[6]
    q7 := q[7]
    r0 := AesRotr16(q0)
    r1 := AesRotr16(q1)
    r2 := AesRotr16(q2)
    r3 := AesRotr16(q3)
    r4 := AesRotr16(q4)
    r5 := AesRotr16(q5)
    r6 := AesRotr16(q6)
    r7 := AesRotr16(q7)
    *q = [
        q7 ^ r7 ^ r0 ^ AesRotr32(q0 ^ r0),
        q0 ^ r0 ^ q7 ^ r7 ^ r1 ^ AesRotr32(q1 ^ r1),
        q1 ^ r1 ^ r2 ^ AesRotr32(q2 ^ r2),
        q2 ^ r2 ^ q7 ^ r7 ^ r3 ^ AesRotr32(q3 ^ r3),
        q3 ^ r3 ^ q7 ^ r7 ^ r4 ^ AesRotr32(q4 ^ r4),
        q4 ^ r4 ^ r5 ^ AesRotr32(q5 ^ r5),
        q5 ^ r5 ^ r6 ^ AesRotr32(q6 ^ r6),
        q6 ^ r6 ^ r7 ^ AesRotr32(q7 ^ r7),
    ]
}

// AesSpread turns the compressed key bits x (every fourth bit, shifted
// down to the lowest of each nibble) into whole nibbles: x * 15.
AesSpread(x: u64): u64 {
    return (x << 4) -% x
}

// AesAddRoundKey XORs round key u, read from the compressed keys sk and
// expanded (br_aes_ct64_skey_expand), into q.
AesAddRoundKey(q: mut [8]u64, sk: []u8, u: int) {
    c0 := Le64(sk, 16 * u)
    c1 := Le64(sk, 16 * u + 8)
    *q = [
        q[0] ^ AesSpread(c0 & 0x1111111111111111),
        q[1] ^ AesSpread((c0 & 0x2222222222222222) >> 1),
        q[2] ^ AesSpread((c0 & 0x4444444444444444) >> 2),
        q[3] ^ AesSpread((c0 & 0x8888888888888888) >> 3),
        q[4] ^ AesSpread(c1 & 0x1111111111111111),
        q[5] ^ AesSpread((c1 & 0x2222222222222222) >> 1),
        q[6] ^ AesSpread((c1 & 0x4444444444444444) >> 2),
        q[7] ^ AesSpread((c1 & 0x8888888888888888) >> 3),
    ]
}

// AesInterleaveIn spreads one block's four little-endian words into two
// interleaved words (br_aes_ct64_interleave_in).
AesInterleaveIn(w0: u32, w1: u32, w2: u32, w3: u32): (lo: u64, hi: u64) {
    var x0 := u64(w0)
    var x1 := u64(w1)
    var x2 := u64(w2)
    var x3 := u64(w3)
    x0 = (x0 | (x0 << 16)) & 0x0000FFFF0000FFFF
    x1 = (x1 | (x1 << 16)) & 0x0000FFFF0000FFFF
    x2 = (x2 | (x2 << 16)) & 0x0000FFFF0000FFFF
    x3 = (x3 | (x3 << 16)) & 0x0000FFFF0000FFFF
    x0 = (x0 | (x0 << 8)) & 0x00FF00FF00FF00FF
    x1 = (x1 | (x1 << 8)) & 0x00FF00FF00FF00FF
    x2 = (x2 | (x2 << 8)) & 0x00FF00FF00FF00FF
    x3 = (x3 | (x3 << 8)) & 0x00FF00FF00FF00FF
    return (lo: x0 | (x2 << 8), hi: x1 | (x3 << 8))
}

// AesInterleaveOut is AesInterleaveIn's inverse: the block's four words
// from q0 and q1, stored little-endian at out[at..at + 16].
AesInterleaveOut(q0: u64, q1: u64, out: mut []u8, at: int) {
    var x0 := q0 & 0x00FF00FF00FF00FF
    var x1 := q1 & 0x00FF00FF00FF00FF
    var x2 := (q0 >> 8) & 0x00FF00FF00FF00FF
    var x3 := (q1 >> 8) & 0x00FF00FF00FF00FF
    x0 = (x0 | (x0 >> 8)) & 0x0000FFFF0000FFFF
    x1 = (x1 | (x1 >> 8)) & 0x0000FFFF0000FFFF
    x2 = (x2 | (x2 >> 8)) & 0x0000FFFF0000FFFF
    x3 = (x3 | (x3 >> 8)) & 0x0000FFFF0000FFFF
    PutLe32(mut out[..], at, Trunc[u32](x0) | Trunc[u32](x0 >> 16))
    PutLe32(mut out[..], at + 4, Trunc[u32](x1) | Trunc[u32](x1 >> 16))
    PutLe32(mut out[..], at + 8, Trunc[u32](x2) | Trunc[u32](x2 >> 16))
    PutLe32(mut out[..], at + 12, Trunc[u32](x3) | Trunc[u32](x3 >> 16))
}

// AesSubWord is FIPS 197's SubWord on the four bytes of x, through the
// bitsliced S-box (one block of four, the others zero).
AesSubWord(x: u32): u32 {
    var q: [8]u64 = [u64(x), 0, 0, 0, 0, 0, 0, 0]
    AesOrtho(mut q)
    AesSbox(mut q)
    AesOrtho(mut q)
    return Trunc[u32](q[0])
}

// AesSchedule expands key, 16, 24 or 32 bytes, by FIPS 197 section 5.2
// into rk (w as bytes: 16 (nr + 1) of them) and sk (the compressed
// bitsliced round keys, two little-endian words per round key, as
// br_aes_ct64_keysched), and returns nr, 10, 12 or 14. The caller checks
// the key's length. Constant time: the S-box is the circuit, and every
// branch is on the word counter.
AesSchedule(key: []u8, rk: mut [240]u8, sk: mut [240]u8): int {
    nk := key.Len() / 4
    nr := nk + 6
    nkf := 4 * (nr + 1)
    var w: [60]u32 = [0; 60]
    for i in 0..nk {
        w[i] = Le32(key, 4 * i)
    }
    var tmp := w[nk - 1]
    var j := 0
    var k := 0
    for i in nk..nkf {
        if j == 0 {
            tmp = AesSubWord((tmp << 24) | (tmp >> 8)) ^ u32(AesRcon[k])
        } else if nk > 6 && j == 4 {
            tmp = AesSubWord(tmp)
        }
        tmp = tmp ^ w[i - nk]
        w[i] = tmp
        j += 1
        if j == nk {
            j = 0
            k += 1
        }
    }
    for i in 0..nkf {
        PutLe32(mut rk[..], 4 * i, w[i])
    }
    for r in 0..nr + 1 {
        b := AesInterleaveIn(w[4 * r], w[4 * r + 1], w[4 * r + 2], w[4 * r + 3])
        var q: [8]u64 = [b.lo, b.lo, b.lo, b.lo, b.hi, b.hi, b.hi, b.hi]
        AesOrtho(mut q)
        PutLe64(mut sk[..], 16 * r, (q[0] & 0x1111111111111111) | (q[1] & 0x2222222222222222) |
            (q[2] & 0x4444444444444444) | (q[3] & 0x8888888888888888))
        PutLe64(mut sk[..], 16 * r + 8, (q[4] & 0x1111111111111111) | (q[5] & 0x2222222222222222) |
            (q[6] & 0x4444444444444444) | (q[7] & 0x8888888888888888))
    }
    return nr
}

// AesEncrypt4 encrypts the four 16-byte blocks of src[0..64] into
// dst[0..64] under the compressed keys sk with nr rounds
// (br_aes_ct64_bitslice_encrypt between the interleave and the ortho).
AesEncrypt4(sk: []u8, nr: int, src: []u8, dst: mut []u8) {
    var q: [8]u64 = [0; 8]
    for i in 0..4 {
        b := AesInterleaveIn(Le32(src, 16 * i), Le32(src, 16 * i + 4), Le32(src, 16 * i + 8), Le32(src, 16 * i + 12))
        q[i] = b.lo
        q[i + 4] = b.hi
    }
    AesOrtho(mut q)
    AesAddRoundKey(mut q, sk, 0)
    for u in 1..nr {
        AesSbox(mut q)
        AesShiftRows(mut q)
        AesMixColumns(mut q)
        AesAddRoundKey(mut q, sk, u)
    }
    AesSbox(mut q)
    AesShiftRows(mut q)
    AesAddRoundKey(mut q, sk, nr)
    AesOrtho(mut q)
    for i in 0..4 {
        AesInterleaveOut(q[i], q[i + 4], dst, 16 * i)
    }
}
