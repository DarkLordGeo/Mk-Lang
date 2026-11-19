// Why: P-256 and P-384 share all their group code but for constants; one
// template keeps them in step, as Go's nistec does.
// What: TestSTDCRYPTO_EcGeneration, which writes (MK_ECGEN=1) or checks
// std/crypto/ec_p256.mk and ec_p384.mk from one Mk template.
// How: constants computed with math/big; complete formulas for a = -3,
// fixed-window and comb multiplication; verification by Straus over NAFs
// in Jacobian coordinates with variable-time formulas (the Vt functions).

package std

import (
	"crypto/elliptic"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cr2EcCurve is one curve: its Mk prefixes, limb count and parameters.
type cr2EcCurve struct {
	name, c, s, file string
	limbs            int
	params           *elliptic.CurveParams
	comb             bool // a precomputed comb for k G (P-256: signing and keys)
}

var cr2EcCurves = []cr2EcCurve{
	{"P-256", "P256", "P256n", "ec_p256.mk", 4, elliptic.P256().Params(), true},
	{"P-384", "P384", "P384n", "ec_p384.mk", 6, elliptic.P384().Params(), false},
}

// cr2EcLimbs is x as an Mk array literal of little-endian 64-bit limbs.
func cr2EcLimbs(x *big.Int, limbs int) string {
	var parts []string
	m := new(big.Int).Set(x)
	mask := new(big.Int).SetUint64(^uint64(0))
	for range limbs {
		parts = append(parts, fmt.Sprintf("0x%016x", new(big.Int).And(m, mask).Uint64()))
		m.Rsh(m, 64)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// cr2EcBytes is x as an Mk array literal of n big-endian bytes, 12 a line.
func cr2EcBytes(x *big.Int, n int) string {
	b := x.FillBytes(make([]byte, n))
	var lines []string
	for i := 0; i < n; i += 12 {
		var parts []string
		for _, v := range b[i:min(i+12, n)] {
			parts = append(parts, fmt.Sprintf("0x%02x", v))
		}
		lines = append(lines, "    "+strings.Join(parts, ", "))
	}
	return "[\n" + strings.Join(lines, ",\n") + ",\n]"
}

// cr2EcMont is x in the Montgomery domain of m with R = 2^(64 limbs).
func cr2EcMont(x, m *big.Int, limbs int) *big.Int {
	r := new(big.Int).Lsh(big.NewInt(1), uint(64*limbs))
	return new(big.Int).Mod(new(big.Int).Mul(x, r), m)
}

// cr2EcLimbsOf is the generated body that reads B big-endian bytes of b
// into little-endian limbs.
func cr2EcLimbsOf(limbs int) string {
	var parts []string
	for i := range limbs {
		parts = append(parts, fmt.Sprintf("Be64(b, %d)", 8*(limbs-1-i)))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// cr2EcPutLimbs is the generated body that writes limbs big-endian.
func cr2EcPutLimbs(limbs int) string {
	var lines []string
	for i := range limbs {
		lines = append(lines, fmt.Sprintf("    PutBe64(mut out[..], %d, v[%d])", 8*(limbs-1-i), i))
	}
	return strings.Join(lines, "\n")
}

// cr2EcField is the template of one field's conversions and inversion:
// __F__ is its fiat prefix, __M__ its modulus constant.
const cr2EcField = `
// __F__Limbs is the __B__ big-endian bytes of b as little-endian limbs.
__F__Limbs(b: []u8): [__L__]u64 {
    return __LIMBSOF__
}

// __F__Decode is the element whose big-endian encoding is b
// (__B__ bytes), in the Montgomery domain, or none when it is not below
// the modulus. Constant time but for the answer.
__F__Decode(b: []u8): ?[__L__]u64 {
    a := __F__Limbs(b)
    if __C__Below(&a, &__M__) == 0 {
        return none
    }
    return some(__F__ToMontgomery(&a))
}

// __F__Reduce is the element b mod the modulus, b __B__ big-endian bytes
// below twice the modulus (a coordinate or a digest reduced by the order):
// one subtraction kept by mask, so constant time.
__F__Reduce(b: []u8): [__L__]u64 {
    a := __F__Limbs(b)
    var d: [__L__]u64 = [0; __L__]
    var borrow: u64 = 0
    for i in 0..__L__ {
        w := FiatSub64(a[i], __M__[i], borrow)
        d[i] = w.d
        borrow = w.b
    }
    r := __F__Selectznz(borrow, &d, &a)
    return __F__ToMontgomery(&r)
}

// __F__Encode writes a (Montgomery domain) as __B__ big-endian bytes at the
// front of out.
__F__Encode(a: &[__L__]u64, out: mut []u8) {
    v := __F__FromMontgomery(a)
__PUTLIMBS__
}

// __F__IsZero is 1 when a is zero, else 0, by mask arithmetic.
__F__IsZero(a: &[__L__]u64): u64 {
    n := __F__Nonzero(a)
    return ((n | (0 -% n)) >> 63) ^ 1
}

// __F__Invert is 1 / a, a^(m - 2) by Fermat's little theorem (0 for 0),
// over a fixed 4-bit window of the public exponent: a table of a^0 to
// a^15, then per nibble four squarings and one product, the same sequence
// of operations for every a, so constant time. 15 + __B__ * 2 products
// and __B__ * 8 squarings.
__F__Invert(a: &[__L__]u64): [__L__]u64 {
    one := __F__SetOne()
    var t: [16][__L__]u64 = [one; 16]
    t[1] = *a
    for i in 2..16 {
        t[i] = __F__Mul(&t[i - 1], a)
    }
    var r := one
    for i in 0..__B__ {
        e := __F__InvExp[i]
        for _ in 0..4 {
            r = __F__Square(&r)
        }
        r = __F__Mul(&r, &t[int(e >> 4)])
        for _ in 0..4 {
            r = __F__Square(&r)
        }
        r = __F__Mul(&r, &t[int(e & 15)])
    }
    return r
}
`

// cr2EcGroup is the template of one curve's group and ECDSA verification
// core: __C__ is the base field's prefix, __S__ the scalar field's.
const cr2EcGroup = `
// __C__Pt is a point of __NAME__ in projective coordinates (X : Y : Z),
// x = X / Z and y = Y / Z, each in the Montgomery domain; the identity is
// (0 : 1 : 0). The complete formulas take every pair of points, the
// identity and equal points included, with no branch.
struct __C__Pt {
    x: [__L__]u64
    y: [__L__]u64
    z: [__L__]u64
}

const __C__Identity: __C__Pt = __C__Pt(x: [0; __L__], y: __C__One, z: [0; __L__])

const __C__Generator: __C__Pt = __C__Pt(x: __C__Gx, y: __C__Gy, z: __C__One)

// __C__Below is 1 when a < m, else 0: the borrow out of a - m, constant
// time. Every caller branches on the answer, so the borrows are
// FiatSubBorrow's formula (fiat.mk says why not FiatSub64's).
__C__Below(a: &[__L__]u64, m: &[__L__]u64): u64 {
    var borrow: u64 = 0
    for i in 0..__L__ {
        borrow = FiatSubBorrow(a[i], m[i], borrow)
    }
    return borrow
}

// __C__Equal reports whether two elements in the Montgomery domain are
// equal (each is fully reduced, so their limbs are). Public data only.
__C__Equal(a: &[__L__]u64, b: &[__L__]u64): bool {
    var d: u64 = 0
    for i in 0..__L__ {
        d = d | (a[i] ^ b[i])
    }
    return d == 0
}

// __C__PtAdd is p + q: Renes, Costello and Batina 2016, algorithm 4 (a =
// -3), 12 products, 2 by b, and 29 additions; complete, so no case of p
// and q needs a branch.
__C__PtAdd(p: &__C__Pt, q: &__C__Pt): __C__Pt {
    b := __C__B
    var t0 := __C__Mul(&p.x, &q.x)
    var t1 := __C__Mul(&p.y, &q.y)
    var t2 := __C__Mul(&p.z, &q.z)
    var t3 := __C__Add(&p.x, &p.y)
    var t4 := __C__Add(&q.x, &q.y)
    t3 = __C__Mul(&t3, &t4)
    t4 = __C__Add(&t0, &t1)
    t3 = __C__Sub(&t3, &t4)
    t4 = __C__Add(&p.y, &p.z)
    var x3 := __C__Add(&q.y, &q.z)
    t4 = __C__Mul(&t4, &x3)
    x3 = __C__Add(&t1, &t2)
    t4 = __C__Sub(&t4, &x3)
    x3 = __C__Add(&p.x, &p.z)
    var y3 := __C__Add(&q.x, &q.z)
    x3 = __C__Mul(&x3, &y3)
    y3 = __C__Add(&t0, &t2)
    y3 = __C__Sub(&x3, &y3)
    var z3 := __C__Mul(&b, &t2)
    x3 = __C__Sub(&y3, &z3)
    z3 = __C__Add(&x3, &x3)
    x3 = __C__Add(&x3, &z3)
    z3 = __C__Sub(&t1, &x3)
    x3 = __C__Add(&t1, &x3)
    y3 = __C__Mul(&b, &y3)
    t1 = __C__Add(&t2, &t2)
    t2 = __C__Add(&t1, &t2)
    y3 = __C__Sub(&y3, &t2)
    y3 = __C__Sub(&y3, &t0)
    t1 = __C__Add(&y3, &y3)
    y3 = __C__Add(&t1, &y3)
    t1 = __C__Add(&t0, &t0)
    t0 = __C__Add(&t1, &t0)
    t0 = __C__Sub(&t0, &t2)
    t1 = __C__Mul(&t4, &y3)
    t2 = __C__Mul(&t0, &y3)
    y3 = __C__Mul(&x3, &z3)
    y3 = __C__Add(&y3, &t2)
    x3 = __C__Mul(&t3, &x3)
    x3 = __C__Sub(&x3, &t1)
    z3 = __C__Mul(&t4, &z3)
    t1 = __C__Mul(&t3, &t0)
    z3 = __C__Add(&z3, &t1)
    return __C__Pt(x: x3, y: y3, z: z3)
}

// __C__PtDouble is 2p: Renes, Costello and Batina 2016, algorithm 6 (a =
// -3), 8 products, 3 squarings, 2 by b; complete.
__C__PtDouble(p: &__C__Pt): __C__Pt {
    b := __C__B
    var t0 := __C__Square(&p.x)
    var t1 := __C__Square(&p.y)
    var t2 := __C__Square(&p.z)
    var t3 := __C__Mul(&p.x, &p.y)
    t3 = __C__Add(&t3, &t3)
    var z3 := __C__Mul(&p.x, &p.z)
    z3 = __C__Add(&z3, &z3)
    var y3 := __C__Mul(&b, &t2)
    y3 = __C__Sub(&y3, &z3)
    var x3 := __C__Add(&y3, &y3)
    y3 = __C__Add(&x3, &y3)
    x3 = __C__Sub(&t1, &y3)
    y3 = __C__Add(&t1, &y3)
    y3 = __C__Mul(&x3, &y3)
    x3 = __C__Mul(&x3, &t3)
    t3 = __C__Add(&t2, &t2)
    t2 = __C__Add(&t2, &t3)
    z3 = __C__Mul(&b, &z3)
    z3 = __C__Sub(&z3, &t2)
    z3 = __C__Sub(&z3, &t0)
    t3 = __C__Add(&z3, &z3)
    z3 = __C__Add(&z3, &t3)
    t3 = __C__Add(&t0, &t0)
    t0 = __C__Add(&t3, &t0)
    t0 = __C__Sub(&t0, &t2)
    t0 = __C__Mul(&t0, &z3)
    y3 = __C__Add(&y3, &t0)
    t0 = __C__Mul(&p.y, &p.z)
    t0 = __C__Add(&t0, &t0)
    z3 = __C__Mul(&t0, &z3)
    x3 = __C__Sub(&x3, &z3)
    z3 = __C__Mul(&t0, &t1)
    z3 = __C__Add(&z3, &z3)
    z3 = __C__Add(&z3, &z3)
    return __C__Pt(x: x3, y: y3, z: z3)
}

// __C__PtSelect is a when c is 0 and b when c is 1, by mask.
__C__PtSelect(c: u64, a: &__C__Pt, b: &__C__Pt): __C__Pt {
    return __C__Pt(x: __C__Selectznz(c, &a.x, &b.x), y: __C__Selectznz(c, &a.y, &b.y), z: __C__Selectznz(c, &a.z, &b.z))
}

// __C__PtTable is 0q, 1q, ..., 15q: 7 doublings and 7 additions.
__C__PtTable(q: &__C__Pt): [16]__C__Pt {
    var t: [16]__C__Pt = [__C__Identity; 16]
    t[1] = *q
    for i in 1..8 {
        t[2 * i] = __C__PtDouble(&t[i])
        t[2 * i + 1] = __C__PtAdd(&t[2 * i], q)
    }
    return t
}

// __C__PtLookup is t[n] for a secret n below 16: every entry is read and
// one kept by mask (equality by arithmetic, not a comparison).
__C__PtLookup(t: &[16]__C__Pt, n: u64): __C__Pt {
    var r := t[0]
    var i: u64 = 1
    for k in 1..16 {
        r = __C__PtSelect(((i ^ n) -% 1) >> 63, &r, &t[k])
        i += 1
    }
    return r
}

// __C__PtMul is k q for k as big-endian bytes, constant time: a fixed
// 4-bit window, per nibble four doublings and one complete addition of a
// masked table entry, the same sequence for every k and q.
__C__PtMul(q: &__C__Pt, k: []u8): __C__Pt {
    t := __C__PtTable(q)
    var p := __C__Identity
    for i in 0..k.Len() {
        for _ in 0..4 {
            p = __C__PtDouble(&p)
        }
        p = __C__PtAdd(&p, &__C__PtLookup(&t, u64(k[i] >> 4)))
        for _ in 0..4 {
            p = __C__PtDouble(&p)
        }
        p = __C__PtAdd(&p, &__C__PtLookup(&t, u64(k[i] & 15)))
    }
    return p
}

// __C__Naf writes the width-w NAF of k (__B__ big-endian bytes, w from 2
// to 7) to d, least significant digit first: each digit 0 or odd with
// absolute value below 2^(w - 1), and of any w consecutive digits at most
// one nonzero (Solinas 2000; Hankerson, Menezes and Vanstone, algorithm
// 3.35). Public data, variable time.
__C__Naf(k: []u8, w: int, d: mut [__N1__]int) {
    var x: [__L1__]u64 = [0; __L1__]
    for i in 0..__L__ {
        x[i] = Be64(k, __B__ - 8 * (i + 1))
    }
    full := u64(1) << Cast[u64](w)
    half := full >> 1
    for i in 0..__N1__ {
        var di := 0
        if x[0] & 1 == 1 {
            m := x[0] & (full - 1)
            if m >= half {
                // A negative digit, m - 2^w: x += 2^w - m clears the low w bits.
                di = Cast[int](m) - Cast[int](full)
                var c := full - m
                for j in 0..__L1__ {
                    s := x[j] +% c
                    c = if s < c { 1 } else { 0 }
                    x[j] = s
                }
            } else {
                di = Cast[int](m)
                x[0] = x[0] - m
            }
        }
        d[i] = di
        for j in 0..__L__ {
            x[j] = (x[j] >> 1) | (x[j + 1] << 63)
        }
        x[__L__] = x[__L__] >> 1
    }
}

// __C__VtPt is a point of __NAME__ in Jacobian coordinates (X : Y : Z),
// x = X / Z^2 and y = Y / Z^3, in the Montgomery domain; Z = 0 is the
// point at infinity. It, __C__VtAffine and every function named __C__Vt
// are verification's alone: public data, variable time, branches on the
// points. Only __C__EcdsaVerify and the __C__Vt functions name them
// (TestSTDCRYPTO_VtOnlyFromVerify reads the sources), and no secret
// path's build holds one, out of line or inlined
// (TestSTDCRYPTO_EcConstantTimeDisassembly). Secrets take __C__Pt and
// its complete formulas.
struct __C__VtPt {
    x: [__L__]u64
    y: [__L__]u64
    z: [__L__]u64
}

// __C__VtAffine is an affine point (x, y), in the Montgomery domain, never
// the point at infinity.
struct __C__VtAffine {
    x: [__L__]u64
    y: [__L__]u64
}

const __C__VtInf: __C__VtPt = __C__VtPt(x: __C__One, y: __C__One, z: [0; __L__])

// __C__VtIsInf reports whether p is the point at infinity.
__C__VtIsInf(p: &__C__VtPt): bool {
    return __C__IsZero(&p.z) == 1
}

// __C__VtDouble is 2 p: dbl-2001-b for a = -3 (Bernstein and Lange's
// Explicit-Formulas Database), 3 products and 5 squarings. The point at
// infinity doubles to itself (Z3 = (Y + Z)^2 - Y^2 - Z^2 = 2 Y Z = 0), and a
// curve of prime order has no point with y = 0, so no case needs a test.
__C__VtDouble(p: &__C__VtPt): __C__VtPt {
    delta := __C__Square(&p.z)
    gamma := __C__Square(&p.y)
    beta := __C__Mul(&p.x, &gamma)
    t0 := __C__Sub(&p.x, &delta)
    t1 := __C__Add(&p.x, &delta)
    a1 := __C__Mul(&t0, &t1)
    a2 := __C__Add(&a1, &a1)
    alpha := __C__Add(&a2, &a1)
    b2 := __C__Add(&beta, &beta)
    b4 := __C__Add(&b2, &b2)
    b8 := __C__Add(&b4, &b4)
    var x3 := __C__Square(&alpha)
    x3 = __C__Sub(&x3, &b8)
    var z3 := __C__Add(&p.y, &p.z)
    z3 = __C__Square(&z3)
    z3 = __C__Sub(&z3, &gamma)
    z3 = __C__Sub(&z3, &delta)
    g1 := __C__Square(&gamma)
    g2 := __C__Add(&g1, &g1)
    g4 := __C__Add(&g2, &g2)
    g8 := __C__Add(&g4, &g4)
    var y3 := __C__Sub(&b4, &x3)
    y3 = __C__Mul(&alpha, &y3)
    y3 = __C__Sub(&y3, &g8)
    return __C__VtPt(x: x3, y: y3, z: z3)
}

// __C__VtAddAffine is p + q for an affine q: madd-2007-bl, 7 products and
// 4 squarings, after the cases the formula does not take: p at infinity
// gives q, and when x(p) = x(q) (H = 0) p + q is 2 p if y(p) = y(q) and the
// point at infinity if not (p = -q).
__C__VtAddAffine(p: &__C__VtPt, q: &__C__VtAffine): __C__VtPt {
    if __C__VtIsInf(p) {
        return __C__VtPt(x: q.x, y: q.y, z: __C__One)
    }
    z1z1 := __C__Square(&p.z)
    u2 := __C__Mul(&q.x, &z1z1)
    var s2 := __C__Mul(&q.y, &p.z)
    s2 = __C__Mul(&s2, &z1z1)
    h := __C__Sub(&u2, &p.x)
    d := __C__Sub(&s2, &p.y)
    if __C__IsZero(&h) == 1 {
        if __C__IsZero(&d) == 1 {
            return __C__VtDouble(p)
        }
        return __C__VtInf
    }
    hh := __C__Square(&h)
    i2 := __C__Add(&hh, &hh)
    i := __C__Add(&i2, &i2)
    j := __C__Mul(&h, &i)
    r := __C__Add(&d, &d)
    v := __C__Mul(&p.x, &i)
    var x3 := __C__Square(&r)
    x3 = __C__Sub(&x3, &j)
    x3 = __C__Sub(&x3, &v)
    x3 = __C__Sub(&x3, &v)
    var y3 := __C__Sub(&v, &x3)
    y3 = __C__Mul(&r, &y3)
    yj := __C__Mul(&p.y, &j)
    yj2 := __C__Add(&yj, &yj)
    y3 = __C__Sub(&y3, &yj2)
    var z3 := __C__Add(&p.z, &h)
    z3 = __C__Square(&z3)
    z3 = __C__Sub(&z3, &z1z1)
    z3 = __C__Sub(&z3, &hh)
    return __C__VtPt(x: x3, y: y3, z: z3)
}

// __C__VtAdd is p + q: add-2007-bl, 11 products and 5 squarings, after
// the cases the formula does not take: either point at infinity gives the
// other, and when the x coordinates agree (H = 0) p + q is 2 p or the
// point at infinity, as in __C__VtAddAffine.
__C__VtAdd(p: &__C__VtPt, q: &__C__VtPt): __C__VtPt {
    if __C__VtIsInf(p) {
        return *q
    }
    if __C__VtIsInf(q) {
        return *p
    }
    z1z1 := __C__Square(&p.z)
    z2z2 := __C__Square(&q.z)
    u1 := __C__Mul(&p.x, &z2z2)
    u2 := __C__Mul(&q.x, &z1z1)
    var s1 := __C__Mul(&p.y, &q.z)
    s1 = __C__Mul(&s1, &z2z2)
    var s2 := __C__Mul(&q.y, &p.z)
    s2 = __C__Mul(&s2, &z1z1)
    h := __C__Sub(&u2, &u1)
    d := __C__Sub(&s2, &s1)
    if __C__IsZero(&h) == 1 {
        if __C__IsZero(&d) == 1 {
            return __C__VtDouble(p)
        }
        return __C__VtInf
    }
    h2 := __C__Add(&h, &h)
    i := __C__Square(&h2)
    j := __C__Mul(&h, &i)
    r := __C__Add(&d, &d)
    v := __C__Mul(&u1, &i)
    var x3 := __C__Square(&r)
    x3 = __C__Sub(&x3, &j)
    x3 = __C__Sub(&x3, &v)
    x3 = __C__Sub(&x3, &v)
    var y3 := __C__Sub(&v, &x3)
    y3 = __C__Mul(&r, &y3)
    sj := __C__Mul(&s1, &j)
    sj2 := __C__Add(&sj, &sj)
    y3 = __C__Sub(&y3, &sj2)
    var z3 := __C__Add(&p.z, &q.z)
    z3 = __C__Square(&z3)
    z3 = __C__Sub(&z3, &z1z1)
    z3 = __C__Sub(&z3, &z2z2)
    z3 = __C__Mul(&z3, &h)
    return __C__VtPt(x: x3, y: y3, z: z3)
}

// __C__VtNeg is -p.
__C__VtNeg(p: &__C__VtPt): __C__VtPt {
    return __C__VtPt(x: p.x, y: __C__Opp(&p.y), z: p.z)
}

// __C__VtGOdd is (2 j + 1) G from __C__GOdd, negated when neg.
__C__VtGOdd(j: int, neg: bool): __C__VtAffine {
    var x: [__L__]u64 = [0; __L__]
    var y: [__L__]u64 = [0; __L__]
    b := 2 * __L__ * j
    for w in 0..__L__ {
        x[w] = __C__GOdd[b + w]
        y[w] = __C__GOdd[b + __L__ + w]
    }
    if neg {
        y = __C__Opp(&y)
    }
    return __C__VtAffine(x: x, y: y)
}

// __C__VtMulAdd is u1 G + u2 q for public scalars (big-endian, __B__
// bytes each) and a public q, as ECDSA verification needs: Straus' shared
// doublings over width-6 and width-5 NAFs (Möller 2001's interleaving, as
// BoringSSL and Go's generic path verify), G's odd multiples affine from
// __C__GOdd and added by __C__VtAddAffine, q's 8 made per call in Jacobian
// coordinates and added by __C__VtAdd, the leading doublings of the
// point at infinity skipped. Variable time; public data only.
__C__VtMulAdd(u1: []u8, q: &__C__VtAffine, u2: []u8): __C__VtPt {
    var n1: [__N1__]int = [0; __N1__]
    var n2: [__N1__]int = [0; __N1__]
    __C__Naf(u1, 6, mut n1)
    __C__Naf(u2, 5, mut n2)
    var tq: [8]__C__VtPt = [__C__VtInf; 8]
    tq[0] = __C__VtPt(x: q.x, y: q.y, z: __C__One)
    q2 := __C__VtDouble(&tq[0])
    for i in 1..8 {
        tq[i] = __C__VtAdd(&tq[i - 1], &q2)
    }
    var p := __C__VtInf
    var started := false
    var i := __N1__ - 1
    for i >= 0 {
        if started {
            p = __C__VtDouble(&p)
        }
        a := n1[i]
        if a != 0 {
            ma := if a > 0 { a } else { -a }
            g := __C__VtGOdd(ma / 2, a < 0)
            p = __C__VtAddAffine(&p, &g)
            started = true
        }
        b := n2[i]
        if b != 0 {
            if b > 0 {
                p = __C__VtAdd(&p, &tq[b / 2])
            } else {
                t := __C__VtNeg(&tq[-b / 2])
                p = __C__VtAdd(&p, &t)
            }
            started = true
        }
        i -= 1
    }
    return p
}

// __C__VtXIs reports whether p is not the point at infinity and its
// affine x, reduced mod n, is r (__B__ big-endian bytes, below n), with no
// inversion: x = X / Z^2, so x = r exactly when X = r Z^2. x is below p and
// n < p < 2 n, so x mod n = r also when x = r + n, which is below p only
// for r below p - n (__C__PMinusN); r + n is not formed otherwise, so the
// sum cannot wrap.
__C__VtXIs(p: &__C__VtPt, r: []u8): bool {
    if __C__VtIsInf(p) {
        return false
    }
    z2 := __C__Square(&p.z)
    a := __C__Limbs(r)
    ra := __C__ToMontgomery(&a)
    t := __C__Mul(&ra, &z2)
    if __C__Equal(&t, &p.x) {
        return true
    }
    if __C__Below(&a, &__C__PMinusN) == 0 {
        return false
    }
    var b: [__L__]u64 = [0; __L__]
    var c: u64 = 0
    for i in 0..__L__ {
        w := FiatAdd64(a[i], __S__N[i], c)
        b[i] = w.s
        c = w.c
    }
    rb := __C__ToMontgomery(&b)
    t2 := __C__Mul(&rb, &z2)
    return __C__Equal(&t2, &p.x)
}

// __C__PtFromBytes is the point SEC 1 2.3.4 encodes in b uncompressed
// (0x04, x, y; __E__ bytes), or none when b is any other length or form
// (the identity's and compressed encodings included), a coordinate is not
// below p, or the point is not on the curve, y^2 = x^3 - 3x + b. Public
// data.
__C__PtFromBytes(b: []u8): ?__C__Pt {
    if b.Len() != __E__ || b[0] != 4 {
        return none
    }
    x := __C__Decode(b[1..__B1__]) else {
        return none
    }
    y := __C__Decode(b[__B1__..__E__]) else {
        return none
    }
    cb := __C__B
    var rhs := __C__Square(&x)
    rhs = __C__Mul(&rhs, &x)
    var x3 := __C__Add(&x, &x)
    x3 = __C__Add(&x3, &x)
    rhs = __C__Sub(&rhs, &x3)
    rhs = __C__Add(&rhs, &cb)
    lhs := __C__Square(&y)
    if !__C__Equal(&lhs, &rhs) {
        return none
    }
    return some(__C__Pt(x: x, y: y, z: __C__One))
}

// __C__PtBytes writes p's uncompressed SEC 1 encoding (__E__ bytes) at the
// front of out and reports true, or writes nothing and reports false for
// the identity. Constant time but for that answer.
__C__PtBytes(p: &__C__Pt, out: mut []u8): bool {
    if __C__IsZero(&p.z) == 1 {
        return false
    }
    zi := __C__Invert(&p.z)
    x := __C__Mul(&p.x, &zi)
    y := __C__Mul(&p.y, &zi)
    out[0] = 4
    __C__Encode(&x, mut out[1..__B1__])
    __C__Encode(&y, mut out[__B1__..__E__])
    return true
}

// __C__PtXBytes writes p's affine x (__B__ big-endian bytes) at the front
// of out and reports true, or writes nothing and reports false for the
// identity. Constant time but for that answer.
__C__PtXBytes(p: &__C__Pt, out: mut []u8): bool {
    if __C__IsZero(&p.z) == 1 {
        return false
    }
    zi := __C__Invert(&p.z)
    x := __C__Mul(&p.x, &zi)
    __C__Encode(&x, mut out[0..__B__])
    return true
}

// __S__Digest is the digest as a scalar, FIPS 186-5 6.4.1's bits2int
// reduced by the order: its leftmost __B__ bytes (all of it, left-padded
// with zeros, when shorter), mod n. Constant time.
__S__Digest(digest: []u8): [__L__]u64 {
    var e: [__B__]u8 = [0; __B__]
    if digest.Len() >= __B__ {
        (mut e[..]).CopyFrom(digest[0..__B__])
    } else {
        (mut e[__B__ - digest.Len()..__B__]).CopyFrom(digest)
    }
    return __S__Reduce(e[..])
}

// __C__EcdsaVerify reports whether r and s (__B__ big-endian bytes each)
// are an ECDSA signature of digest under the public point q (SEC 1
// uncompressed), FIPS 186-5 6.4.2: false for an empty digest, a q not on
// the curve, r or s not in [1, n - 1], or R = u1 G + u2 Q the identity.
// Public data, variable time: the __C__Vt functions' Jacobian formulas
// and one inversion (of s, in the scalar field).
__C__EcdsaVerify(q: []u8, digest: []u8, r: []u8, s: []u8): bool {
    if digest.Len() == 0 {
        return false
    }
    qp := __C__PtFromBytes(q) else {
        return false
    }
    rs := __S__Decode(r) else {
        return false
    }
    ss := __S__Decode(s) else {
        return false
    }
    if __S__IsZero(&rs) == 1 || __S__IsZero(&ss) == 1 {
        return false
    }
    e := __S__Digest(digest)
    w := __S__Invert(&ss)
    u1 := __S__Mul(&e, &w)
    u2 := __S__Mul(&rs, &w)
    var b1: [__B__]u8 = [0; __B__]
    var b2: [__B__]u8 = [0; __B__]
    __S__Encode(&u1, mut b1[..])
    __S__Encode(&u2, mut b2[..])
    qa := __C__VtAffine(x: qp.x, y: qp.y)
    rp := __C__VtMulAdd(b1[..], &qa, b2[..])
    return __C__VtXIs(&rp, r)
}
`

// cr2EcGenerate is the Mk file for c.
func cr2EcGenerate(c cr2EcCurve) string {
	p, n, l := c.params.P, c.params.N, c.limbs
	bytes := 8 * l
	two := big.NewInt(2)
	var out strings.Builder
	fmt.Fprintf(&out, "// Code generated by std/std-crypto_ecgen_test.go. DO NOT EDIT.\n")
	fmt.Fprintf(&out, "// The %s group (SEC 2, FIPS 186-5: y^2 = x^3 - 3x + b over fiat_%s.mk's field, of prime\n", c.name, strings.ToLower(c.c))
	fmt.Fprintf(&out, "// order n, fiat_%s.mk's field): Montgomery-domain constants, conversions, Fermat inversion,\n", strings.ToLower(c.s))
	if c.comb {
		fmt.Fprintf(&out, "// the complete group law, constant-time multiplication and a comb for k G, public-data double\n")
	} else {
		fmt.Fprintf(&out, "// the complete group law, constant-time multiplication, public-data double\n")
	}
	fmt.Fprintf(&out, "// multiplication in Jacobian coordinates (the Vt functions, which only verification calls),\n")
	fmt.Fprintf(&out, "// SEC 1 point encoding and ECDSA verification's core.\n")
	consts := []struct{ name, doc, val string }{
		{c.c + "One", "1 in the Montgomery domain of p (R mod p).", cr2EcLimbs(cr2EcMont(big.NewInt(1), p, l), l)},
		{c.c + "B", "the curve's b in the Montgomery domain.", cr2EcLimbs(cr2EcMont(c.params.B, p, l), l)},
		{c.c + "Gx", "the base point's x in the Montgomery domain.", cr2EcLimbs(cr2EcMont(c.params.Gx, p, l), l)},
		{c.c + "Gy", "the base point's y in the Montgomery domain.", cr2EcLimbs(cr2EcMont(c.params.Gy, p, l), l)},
		{c.c + "P", "the field's modulus p.", cr2EcLimbs(p, l)},
		{c.s + "N", "the group's order n.", cr2EcLimbs(n, l)},
		{c.c + "PMinusN", "p - n: verification's bound, below which r + n is below p (VtXIs).", cr2EcLimbs(new(big.Int).Sub(p, n), l)},
	}
	for _, k := range consts {
		fmt.Fprintf(&out, "\n// %s is %s\nconst %s: [%d]u64 = %s\n", k.name, k.doc, k.name, l, k.val)
	}
	fmt.Fprintf(&out, "\n// %sInvExp is p - 2, big-endian: Fermat's exponent of inversion.\nconst %sInvExp: [%d]u8 = %s\n", c.c, c.c, bytes, cr2EcBytes(new(big.Int).Sub(p, two), bytes))
	fmt.Fprintf(&out, "\n// %sInvExp is n - 2, big-endian.\nconst %sInvExp: [%d]u8 = %s\n", c.s, c.s, bytes, cr2EcBytes(new(big.Int).Sub(n, two), bytes))
	common := strings.NewReplacer(
		"__NAME__", c.name, "__C__", c.c, "__S__", c.s,
		"__LIMBSOF__", cr2EcLimbsOf(l), "__PUTLIMBS__", cr2EcPutLimbs(l),
		"__L__", fmt.Sprint(l), "__B1__", fmt.Sprint(bytes+1), "__B__", fmt.Sprint(bytes),
		"__E__", fmt.Sprint(2*bytes+1),
		"__N1__", fmt.Sprint(8*bytes+1), "__L1__", fmt.Sprint(l+1),
	)
	for _, f := range []struct{ prefix, mod string }{{c.c, c.c + "P"}, {c.s, c.s + "N"}} {
		out.WriteString(common.Replace(strings.NewReplacer("__F__", f.prefix, "__M__", f.mod).Replace(cr2EcField)))
	}
	out.WriteString(common.Replace(cr2EcGroup))
	fmt.Fprintf(&out, "\n// %sGOdd is G, 3 G, ..., 31 G, affine and in the Montgomery domain, x's\n", c.c)
	fmt.Fprintf(&out, "// %d limbs then y's: verification's width-6 NAF table. %d bytes.\n", l, 16*2*l*8)
	fmt.Fprintf(&out, "const %sGOdd: [%d]u64 = %s\n", c.c, 16*2*l, cr2EcOddTable(c))
	if c.comb {
		fmt.Fprintf(&out, "\n// %sComb is the base multiplication's two tables (%sPtBaseMul), affine\n", c.c, c.c)
		fmt.Fprintf(&out, "// and in the Montgomery domain: entry 15 t + j - 1 (j from 1 to 15) is\n")
		fmt.Fprintf(&out, "// 2^(32 t) (j0 + j1 2^64 + j2 2^128 + j3 2^192) G for j's bits j0 to j3,\n")
		fmt.Fprintf(&out, "// x's %d limbs then y's. %d bytes.\n", l, 2*15*2*l*8)
		fmt.Fprintf(&out, "const %sComb: [%d]u64 = %s\n", c.c, 2*15*2*l, cr2EcCombTable(c))
		out.WriteString(common.Replace(cr2EcComb))
	}
	return out.String()
}

// cr2EcCombTable is the comb's table as an Mk array literal, one
// coordinate a line.
func cr2EcCombTable(c cr2EcCurve) string {
	p, l := c.params.P, c.limbs
	var lines []string
	for t := 0; t < 2; t++ {
		for j := 1; j < 16; j++ {
			k := new(big.Int)
			for b := 0; b < 4; b++ {
				if j>>b&1 == 1 {
					k.SetBit(k, 64*b+32*t, 1)
				}
			}
			x, y := c.params.ScalarBaseMult(k.Bytes())
			for _, v := range []*big.Int{x, y} {
				lim := cr2EcLimbs(cr2EcMont(v, p, l), l)
				lines = append(lines, "    "+lim[1:len(lim)-1])
			}
		}
	}
	return "[\n" + strings.Join(lines, ",\n") + ",\n]"
}

// cr2EcOddTable is G, 3 G, ..., 31 G as an Mk array literal, one
// coordinate a line.
func cr2EcOddTable(c cr2EcCurve) string {
	p, l := c.params.P, c.limbs
	var lines []string
	for j := 0; j < 16; j++ {
		x, y := c.params.ScalarBaseMult(big.NewInt(int64(2*j + 1)).Bytes())
		for _, v := range []*big.Int{x, y} {
			lim := cr2EcLimbs(cr2EcMont(v, p, l), l)
			lines = append(lines, "    "+lim[1:len(lim)-1])
		}
	}
	return "[\n" + strings.Join(lines, ",\n") + ",\n]"
}

// cr2EcComb is the template of the comb base multiplication (P-256).
const cr2EcComb = `
// __C__CombBits is the window of comb position i (0 to 63) of k, __B__
// big-endian bytes: bits i, i + 64, i + 128 and i + 192 of k as bits 0
// to 3. The bytes read depend on i only.
__C__CombBits(k: []u8, i: int): u64 {
    var n: u64 = 0
    for t in 0..4 {
        j := i + 64 * t
        n = n | (((u64(k[k.Len() - 1 - j / 8]) >> Cast[u64](j % 8)) & 1) << Cast[u64](t))
    }
    return n
}

// __C__CombLookup is entry n (0 to 15, secret) of comb table t (0 or 1)
// as a projective point, the identity for 0: every entry is read and one
// kept by mask (equality by arithmetic, not a comparison).
__C__CombLookup(t: int, n: u64): __C__Pt {
    var x: [__L__]u64 = [0; __L__]
    var y := __C__One
    var z: [__L__]u64 = [0; __L__]
    var i: u64 = 1
    for e in 0..15 {
        m := ((i ^ n) -% 1) >> 63
        b := 2 * __L__ * (15 * t + e)
        var ex: [__L__]u64 = [0; __L__]
        var ey: [__L__]u64 = [0; __L__]
        for w in 0..__L__ {
            ex[w] = __C__Comb[b + w]
            ey[w] = __C__Comb[b + __L__ + w]
        }
        x = __C__Selectznz(m, &x, &ex)
        y = __C__Selectznz(m, &y, &ey)
        z = __C__Selectznz(m, &z, &__C__One)
        i += 1
    }
    return __C__Pt(x: x, y: y, z: z)
}

// __C__PtBaseMul is k G for k as __B__ big-endian bytes, constant time:
// Lim and Lee's comb over two precomputed tables (__C__Comb; the shape
// of BoringSSL's and fiat-crypto's P-256 base multiplication), 31
// doublings and 64 complete additions of masked table entries, the same
// sequence for every k.
__C__PtBaseMul(k: []u8): __C__Pt {
    var p := __C__Identity
    var i := 31
    for i >= 0 {
        if i != 31 {
            p = __C__PtDouble(&p)
        }
        p = __C__PtAdd(&p, &__C__CombLookup(0, __C__CombBits(k, i)))
        p = __C__PtAdd(&p, &__C__CombLookup(1, __C__CombBits(k, i + 32)))
        i -= 1
    }
    return p
}
`

// TestSTDCRYPTO_EcGeneration: each std/crypto/ec_<curve>.mk is the
// generator's output (MK_ECGEN=1 writes them):
//
//	MK_ECGEN=1 go test ./std -run TestSTDCRYPTO_EcGeneration
func TestSTDCRYPTO_EcGeneration(t *testing.T) {
	for _, c := range cr2EcCurves {
		want := cr2EcGenerate(c)
		path := filepath.Join("crypto", c.file)
		if os.Getenv("MK_ECGEN") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with MK_ECGEN=1)", path, err)
		}
		if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("%s differs from the generator's output: run with MK_ECGEN=1 and read the diff", path)
		}
	}
}
