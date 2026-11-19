// Why: Ed25519 signs and verifies with point arithmetic on edwards25519,
// and signing needs a constant-time fixed-base multiply.
//
// What: the point forms Pt, P1, P2, Cached and Affine, addition and
// doubling, PtBytes and PtFromBytes, PtBaseMult and PtDoubleMult.
//
// How: the group edwards25519 (-x^2 + y^2 = 1 + d x^2 y^2 over GF(2^255 -
// 19), RFC 8032 5.1) in the extended coordinates of Hisil, Wong, Carter and
// Dawson 2008 (the a = -1 formulas of RFC 8032 5.1.4), with Go's
// representations (crypto/internal/fips140/edwards25519): Pt (X:Y:Z:T), the
// completed P1 ((X:Z), (Y:T)) an addition or doubling produces, P2 (X:Y:Z)
// for chains of doublings, and the cached forms (Y+X, Y-X, Z, 2dT) and (y+x,
// y-x, 2dxy) that additions read. The fixed-base multiply is ref10's signed
// radix-16 comb over tables.mk's BaseComb; the verification multiply is
// Straus with width-5 and width-8 NAFs (Go's VarTimeDoubleScalarBaseMult).
// Constant time: PtBaseMult's digits come from the secret scalar by
// arithmetic only, it runs a fixed 64 additions and 4 doublings, and each
// table read scans all 8 entries of its row and keeps one by mask; the sign
// is applied by mask. PtDoubleMult and PtFromBytes are variable time and run
// only on public data (a signature, a public key, a message digest), as Go's.

struct Pt {
    x: Fe
    y: Fe
    z: Fe
    t: Fe
}

struct P1 {
    x: Fe
    y: Fe
    z: Fe
    t: Fe
}

struct P2 {
    x: Fe
    y: Fe
    z: Fe
}

struct Cached {
    ypx: Fe
    ymx: Fe
    z: Fe
    t2d: Fe
}

struct Affine {
    ypx: Fe
    ymx: Fe
    t2d: Fe
}

const PtIdentity: Pt = Pt(x: FeZero, y: FeOne, z: FeOne, t: FeZero)
const P2Identity: P2 = P2(x: FeZero, y: FeOne, z: FeOne)
const AffineIdentity: Affine = Affine(ypx: FeOne, ymx: FeOne, t2d: FeZero)

PtFromP1(p: &P1): Pt {
    return Pt(x: FeMul(p.x, p.t), y: FeMul(p.y, p.z), z: FeMul(p.z, p.t), t: FeMul(p.x, p.y))
}

P2FromP1(p: &P1): P2 {
    return P2(x: FeMul(p.x, p.t), y: FeMul(p.y, p.z), z: FeMul(p.z, p.t))
}

PtFromP2(p: &P2): Pt {
    return Pt(x: FeMul(p.x, p.z), y: FeMul(p.y, p.z), z: FeSquare(p.z), t: FeMul(p.x, p.y))
}

CachedOf(p: &Pt): Cached {
    return Cached(ypx: FeAdd(p.y, p.x), ymx: FeSub(p.y, p.x), z: p.z, t2d: FeMul(p.t, D2))
}

// P1Add is p + q (add-2008-hwcd-3: 9 multiplications).
P1Add(p: &Pt, q: &Cached): P1 {
    pp := FeMul(FeAdd(p.y, p.x), q.ypx)
    mm := FeMul(FeSub(p.y, p.x), q.ymx)
    tt := FeMul(p.t, q.t2d)
    zz := FeMul(p.z, q.z)
    zz2 := FeAdd(zz, zz)
    return P1(x: FeSub(pp, mm), y: FeAdd(pp, mm), z: FeAdd(zz2, tt), t: FeSub(zz2, tt))
}

// P1Sub is p - q: q's Y+X and Y-X swap and its 2dT changes sign.
P1Sub(p: &Pt, q: &Cached): P1 {
    pp := FeMul(FeAdd(p.y, p.x), q.ymx)
    mm := FeMul(FeSub(p.y, p.x), q.ypx)
    tt := FeMul(p.t, q.t2d)
    zz := FeMul(p.z, q.z)
    zz2 := FeAdd(zz, zz)
    return P1(x: FeSub(pp, mm), y: FeAdd(pp, mm), z: FeSub(zz2, tt), t: FeAdd(zz2, tt))
}

// P1AddAffine is p + q for q with Z = 1 (madd: 7 multiplications).
P1AddAffine(p: &Pt, q: &Affine): P1 {
    pp := FeMul(FeAdd(p.y, p.x), q.ypx)
    mm := FeMul(FeSub(p.y, p.x), q.ymx)
    tt := FeMul(p.t, q.t2d)
    z2 := FeAdd(p.z, p.z)
    return P1(x: FeSub(pp, mm), y: FeAdd(pp, mm), z: FeAdd(z2, tt), t: FeSub(z2, tt))
}

P1SubAffine(p: &Pt, q: &Affine): P1 {
    pp := FeMul(FeAdd(p.y, p.x), q.ymx)
    mm := FeMul(FeSub(p.y, p.x), q.ypx)
    tt := FeMul(p.t, q.t2d)
    z2 := FeAdd(p.z, p.z)
    return P1(x: FeSub(pp, mm), y: FeAdd(pp, mm), z: FeSub(z2, tt), t: FeAdd(z2, tt))
}

// P1Double is 2p (dbl-2008-hwcd: 4 squarings).
P1Double(p: &P2): P1 {
    xx := FeSquare(p.x)
    yy := FeSquare(p.y)
    zz := FeSquare(p.z)
    zz2 := FeAdd(zz, zz)
    s := FeSquare(FeAdd(p.x, p.y))
    y := FeAdd(yy, xx)
    z := FeSub(yy, xx)
    return P1(x: FeSub(s, y), y: y, z: z, t: FeSub(zz2, z))
}

PtNeg(p: &Pt): Pt {
    return Pt(x: FeNeg(p.x), y: p.y, z: p.z, t: FeNeg(p.t))
}

// PtBytes is RFC 8032 5.1.2's encoding: y canonical, the sign of x in bit
// 255.
PtBytes(p: &Pt): [32]u8 {
    zi := FeInvert(p.z)
    x := FeMul(p.x, zi)
    var out := FeBytes(FeMul(p.y, zi))
    out[31] = out[31] | Trunc[u8](FeIsNegative(x) << 7)
    return out
}

// PtFromBytes decodes as Go's Point.SetBytes: y is the 255 low bits taken
// mod p (a non-canonical y is accepted), x = sqrt((y^2 - 1) / (d y^2 + 1))
// with bit 255 choosing its sign (x = 0 with the bit set is accepted), and
// none when there is no square root. Variable time on public data.
PtFromBytes(b: &[32]u8): ?Pt {
    y := FeFromBytes(b)
    y2 := FeSquare(y)
    u := FeSub(y2, FeOne)
    v := FeAdd(FeMul(y2, D), FeOne)
    r := FeSqrtRatio(u, v)
    if r.ok == 0 {
        return none
    }
    x := FeSelect(FeNeg(r.r), r.r, 0 -% (u64(b[31]) >> 7))
    return some(Pt(x: x, y: y, z: FeOne, t: FeMul(x, y)))
}

AffineSelect(a: &Affine, b: &Affine, m: u64): Affine {
    return Affine(ypx: FeSelect(a.ypx, b.ypx, m), ymx: FeSelect(a.ymx, b.ymx, m), t2d: FeSelect(a.t2d, b.t2d, m))
}

// CombEntry is entry k of BaseComb (tables.mk) and NafEntry entry k of
// BaseNaf. Each indexes the const directly, so a read touches one entry
// of the global and never copies the 30,720-byte table.
CombEntry(k: int): Affine {
    i := 15 * k
    return Affine(ypx: Fe(l0: BaseComb[i], l1: BaseComb[i + 1], l2: BaseComb[i + 2], l3: BaseComb[i + 3], l4: BaseComb[i + 4]),
        ymx: Fe(l0: BaseComb[i + 5], l1: BaseComb[i + 6], l2: BaseComb[i + 7], l3: BaseComb[i + 8], l4: BaseComb[i + 9]),
        t2d: Fe(l0: BaseComb[i + 10], l1: BaseComb[i + 11], l2: BaseComb[i + 12], l3: BaseComb[i + 13], l4: BaseComb[i + 14]))
}

NafEntry(k: int): Affine {
    i := 15 * k
    return Affine(ypx: Fe(l0: BaseNaf[i], l1: BaseNaf[i + 1], l2: BaseNaf[i + 2], l3: BaseNaf[i + 3], l4: BaseNaf[i + 4]),
        ymx: Fe(l0: BaseNaf[i + 5], l1: BaseNaf[i + 6], l2: BaseNaf[i + 7], l3: BaseNaf[i + 8], l4: BaseNaf[i + 9]),
        t2d: Fe(l0: BaseNaf[i + 10], l1: BaseNaf[i + 11], l2: BaseNaf[i + 12], l3: BaseNaf[i + 13], l4: BaseNaf[i + 14]))
}

// CombAt is d 16^(2 row) B for d in [-8, 8], read in constant time: all
// eight entries of the row are loaded, the one at |d| kept by mask (none
// for d = 0, which leaves the identity), then negated by mask when d < 0.
CombAt(row: int, d: i64): Affine {
    neg := Trunc[u64](d) >> 63
    abs := (Trunc[u64](d) ^ (0 -% neg)) +% neg
    var r := AffineIdentity
    for j in 0..8 {
        e := CombEntry(8 * row + j)
        // m is all ones exactly when abs = j + 1 (both below 2^63).
        m := 0 -% (((abs ^ Trunc[u64](j + 1)) -% 1) >> 63)
        r = AffineSelect(&e, &r, m)
    }
    mn := 0 -% neg
    return Affine(ypx: FeSelect(r.ymx, r.ypx, mn), ymx: FeSelect(r.ypx, r.ymx, mn), t2d: FeSelect(FeNeg(r.t2d), r.t2d, mn))
}

// PtBaseMult is [s]B for s < 2^255 (a clamped or a reduced scalar): s in
// 64 signed radix-16 digits in [-8, 8), s = sum e[i] 16^i, as
// sum over odd i plus 16 times the sum over even i, each term read from
// row i / 2 of the comb (ref10's ge_scalarmult_base).
PtBaseMult(s: &[32]u8): Pt {
    var e: [64]i64 = [0; 64]
    for i in 0..32 {
        e[2 * i] = i64(s[i] & 15)
        e[2 * i + 1] = i64(s[i] >> 4)
    }
    for i in 0..63 {
        c := (e[i] +% 8) >> 4
        e[i] = e[i] -% (c << 4)
        e[i + 1] = e[i + 1] +% c
    }
    var h := PtIdentity
    for i in 0..32 {
        q := CombAt(i, e[2 * i + 1])
        r := P1AddAffine(&h, &q)
        h = PtFromP1(&r)
    }
    var p2 := P2(x: h.x, y: h.y, z: h.z)
    var r := P1Double(&p2)
    p2 = P2FromP1(&r)
    r = P1Double(&p2)
    p2 = P2FromP1(&r)
    r = P1Double(&p2)
    p2 = P2FromP1(&r)
    r = P1Double(&p2)
    h = PtFromP1(&r)
    for i in 0..32 {
        q := CombAt(i, e[2 * i])
        r2 := P1AddAffine(&h, &q)
        h = PtFromP1(&r2)
    }
    return h
}

// Naf is s (below 2^255) in width-w non-adjacent form, 2 <= w <= 8: every
// non-zero digit odd and in (-2^(w-1), 2^(w-1)), and any w consecutive
// digits with at most one non-zero (curve25519-dalek's non_adjacent_form,
// as Go ports it). Variable time: s is public.
Naf(s: &[32]u8, w: int): [256]i64 {
    var naf: [256]i64 = [0; 256]
    var d: [5]u64 = [0; 5]
    for i in 0..4 {
        d[i] = Le64(s[..], 8 * i)
    }
    width := Trunc[u64](1) << w
    mask := width - 1
    var pos := 0
    var carry: u64 = 0
    for pos < 256 {
        q := pos >> 6
        b := pos & 63
        buf: u64 = if b < 64 - w { d[q] >> b } else { (d[q] >> b) | (d[q + 1] << (64 - b)) }
        win := carry + (buf & mask)
        if win & 1 == 0 {
            pos += 1
            continue
        }
        if win < width / 2 {
            carry = 0
            naf[pos] = Trunc[i64](win)
        } else {
            carry = 1
            naf[pos] = Trunc[i64](win) - Trunc[i64](width)
        }
        pos += w
    }
    return naf
}

// PtDoubleMult is [a]A + [b]B, by Straus: one chain of doublings from the
// top non-zero digit, adding the odd multiples of A (built here, width 5)
// and of B (BaseNaf, width 8) that the NAF digits name. Variable time:
// only for Verify, whose inputs are public.
PtDoubleMult(a: &[32]u8, pa: &Pt, b: &[32]u8): Pt {
    an := Naf(a, 5)
    bn := Naf(b, 8)
    var at: [8]Cached = [CachedOf(pa); 8]
    r0 := P1Add(pa, &at[0])
    a2 := PtFromP1(&r0)
    for i in 0..7 {
        r := P1Add(&a2, &at[i])
        n := PtFromP1(&r)
        at[i + 1] = CachedOf(&n)
    }
    var i := 255
    for i >= 0 && an[i] == 0 && bn[i] == 0 {
        i -= 1
    }
    var p2 := P2Identity
    for i >= 0 {
        var r := P1Double(&p2)
        x := an[i]
        if x > 0 {
            h := PtFromP1(&r)
            r = P1Add(&h, &at[Trunc[int](x) / 2])
        } else if x < 0 {
            h := PtFromP1(&r)
            r = P1Sub(&h, &at[Trunc[int](-x) / 2])
        }
        y := bn[i]
        if y > 0 {
            h := PtFromP1(&r)
            q := NafEntry(Trunc[int](y) / 2)
            r = P1AddAffine(&h, &q)
        } else if y < 0 {
            h := PtFromP1(&r)
            q := NafEntry(Trunc[int](-y) / 2)
            r = P1SubAffine(&h, &q)
        }
        p2 = P2FromP1(&r)
        i -= 1
    }
    return PtFromP2(&p2)
}
