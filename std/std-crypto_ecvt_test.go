// Why: ECDSA verification uses variable-time Jacobian arithmetic (the Vt
// functions of ec_<curve>.mk), whose exceptional cases are taken by branches
// and must each be right; only public data may reach it.
// What: TestSTDCRYPTO_EcVtFormulas, _EcVtMulAddEdges, _EcVerifyMatchesOld,
// _WycheproofLargeX and _VtOnlyFromVerify, with the fixtures cr2EcVt*.
// How: each formula and the double multiplication against crypto/elliptic on
// their exceptional inputs, verification against Go and against the
// projective verification with complete formulas (FxOldEcdsaVerify), and a
// scan of std/crypto's sources for who names a Vt function.

package std

import (
	"crypto/elliptic"
	"fmt"
	"maps"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The Vt functions use Jacobian coordinates by dbl-2001-b, madd-2007-bl and
// add-2007-bl, with the point at infinity and equal or opposite points taken
// by branches, and check R's x against r as X = r Z^2 (or (r + n) Z^2 when
// r + n is below p). The signatures are random valid and invalid ones and
// constructed ones: Q = G and Q = -G with u1 = u2, u1 = 0, an x(R) of n or
// more, which needs the second comparison, and an r that only a comparison
// without the r < p - n guard accepts.

// cr2EcVtCurveFixture is, per curve, the fixture's Vt access and the projective
// verification: __C__ the base field's prefix, __S__ the scalar field's.
const cr2EcVtCurveFixture = `
// FxVtLift__C__ is the point b (SEC 1 uncompressed) in Jacobian
// coordinates with Z = k + 1, so a formula sees Z other than 1; the point
// at infinity for an empty b.
FxVtLift__C__(b: []u8, k: int): __C__VtPt {
    if b.Len() == 0 {
        return __C__VtInf
    }
    a := __C__PtFromBytes(b) else {
        return __C__VtInf
    }
    var z := __C__One
    for _ in 0..k {
        z = __C__Add(&z, &__C__One)
    }
    z2 := __C__Square(&z)
    z3 := __C__Mul(&z2, &z)
    return __C__VtPt(x: __C__Mul(&a.x, &z2), y: __C__Mul(&a.y, &z3), z: z)
}

// FxVtBytes__C__ writes p's uncompressed encoding to out and returns its
// length, 0 for the point at infinity.
FxVtBytes__C__(p: &__C__VtPt, out: mut []u8): int {
    if __C__VtIsInf(p) {
        return 0
    }
    zi := __C__Invert(&p.z)
    zi2 := __C__Square(&zi)
    zi3 := __C__Mul(&zi2, &zi)
    x := __C__Mul(&p.x, &zi2)
    y := __C__Mul(&p.y, &zi3)
    out[0] = 4
    __C__Encode(&x, mut out[1..__B1__])
    __C__Encode(&y, mut out[__B1__..__E__])
    return __E__
}

// FxVtOp__C__ is op: 0 VtAdd(p with Z = 2, q with Z = 3), 1 VtAddAffine(p
// with Z = 2, q affine), 2 VtDouble(p with Z = 2).
FxVtOp__C__(op: int, p: []u8, q: []u8, out: mut []u8): int {
    a := FxVtLift__C__(p, 1)
    if op == 0 {
        b := FxVtLift__C__(q, 2)
        r := __C__VtAdd(&a, &b)
        return FxVtBytes__C__(&r, out)
    }
    if op == 1 {
        qp := __C__PtFromBytes(q) else {
            return 0
        }
        qa := __C__VtAffine(x: qp.x, y: qp.y)
        r := __C__VtAddAffine(&a, &qa)
        return FxVtBytes__C__(&r, out)
    }
    r := __C__VtDouble(&a)
    return FxVtBytes__C__(&r, out)
}

// FxVtMulAdd__C__ is VtMulAdd(u1, q, u2), q not the identity.
FxVtMulAdd__C__(u1: []u8, q: []u8, u2: []u8, out: mut []u8): int {
    qp := __C__PtFromBytes(q) else {
        return 0
    }
    qa := __C__VtAffine(x: qp.x, y: qp.y)
    r := __C__VtMulAdd(u1, &qa, u2)
    return FxVtBytes__C__(&r, out)
}

// FxOld__C__GOddAt, FxOld__C__AddDigit, FxOld__C__MulAdd and
// FxOld__C__Verify are verification with the projective complete formulas
// and one inversion, for the differential test.
FxOld__C__GOddAt(j: int): __C__Pt {
    var x: [__L__]u64 = [0; __L__]
    var y: [__L__]u64 = [0; __L__]
    b := 2 * __L__ * j
    for w in 0..__L__ {
        x[w] = __C__GOdd[b + w]
        y[w] = __C__GOdd[b + __L__ + w]
    }
    return __C__Pt(x: x, y: y, z: __C__One)
}

FxOld__C__AddDigit(p: &__C__Pt, t: &__C__Pt, d: int): __C__Pt {
    if d > 0 {
        return __C__PtAdd(p, t)
    }
    n := __C__Pt(x: t.x, y: __C__Opp(&t.y), z: t.z)
    return __C__PtAdd(p, &n)
}

FxOld__C__MulAdd(u1: []u8, q: &__C__Pt, u2: []u8): __C__Pt {
    var n1: [__N1__]int = [0; __N1__]
    var n2: [__N1__]int = [0; __N1__]
    __C__Naf(u1, 6, mut n1)
    __C__Naf(u2, 5, mut n2)
    var tq: [8]__C__Pt = [__C__Identity; 8]
    tq[0] = *q
    q2 := __C__PtDouble(q)
    for i in 1..8 {
        tq[i] = __C__PtAdd(&tq[i - 1], &q2)
    }
    var p := __C__Identity
    var started := false
    var i := __N1__ - 1
    for i >= 0 {
        if started {
            p = __C__PtDouble(&p)
        }
        a := n1[i]
        if a != 0 {
            ma := if a > 0 { a } else { -a }
            g := FxOld__C__GOddAt(ma / 2)
            p = FxOld__C__AddDigit(&p, &g, a)
            started = true
        }
        b := n2[i]
        if b != 0 {
            mb := if b > 0 { b } else { -b }
            p = FxOld__C__AddDigit(&p, &tq[mb / 2], b)
            started = true
        }
        i -= 1
    }
    return p
}

FxOld__C__Verify(q: []u8, digest: []u8, r: []u8, s: []u8): bool {
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
    rp := FxOld__C__MulAdd(b1[..], &qp, b2[..])
    var x: [__B__]u8 = [0; __B__]
    if !__C__PtXBytes(&rp, mut x[..]) {
        return false
    }
    v := __S__Reduce(x[..])
    return __C__Equal(&v, &rs)
}
`

// cr2EcVtCommon is the fixture's dispatch over the two curves.
const cr2EcVtCommon = `
// FxVtOp is FxVtOp<curve> on curve c (0 P-256, 1 P-384).
export FxVtOp(c: int, op: int, p: []u8, q: []u8, out: mut []u8): int {
    if c == 0 {
        return FxVtOpP256(op, p, q, out)
    }
    return FxVtOpP384(op, p, q, out)
}

// FxVtMulAdd is FxVtMulAdd<curve> on curve c.
FxVtMulAdd(c: int, u1: []u8, q: []u8, u2: []u8, out: mut []u8): int {
    if c == 0 {
        return FxVtMulAddP256(u1, q, u2, out)
    }
    return FxVtMulAddP384(u1, q, u2, out)
}

// FxOldEcdsaVerify is EcdsaVerify through the projective verification.
export FxOldEcdsaVerify(public: []u8, c: int, digest: []u8, format: SigFormat, sig: []u8): bool {
    if c == 0 {
        var r: [32]u8 = [0; 32]
        var s: [32]u8 = [0; 32]
        return SigDecode(format, sig, mut r[..], mut s[..]) && FxOldP256Verify(public, digest, r[..], s[..])
    }
    var r: [48]u8 = [0; 48]
    var s: [48]u8 = [0; 48]
    return SigDecode(format, sig, mut r[..], mut s[..]) && FxOldP384Verify(public, digest, r[..], s[..])
}
`

// cr2EcVtFixture is the Vt part of the EC driver's fixture.
func cr2EcVtFixture() string {
	var b strings.Builder
	for _, c := range cr2EcCurves {
		bytes := 8 * c.limbs
		b.WriteString(strings.NewReplacer("__C__", c.c, "__S__", c.s, "__L__", fmt.Sprint(c.limbs), "__B1__", fmt.Sprint(bytes+1),
			"__B__", fmt.Sprint(bytes), "__E__", fmt.Sprint(2*bytes+1), "__N1__", fmt.Sprint(8*bytes+1)).Replace(cr2EcVtCurveFixture))
	}
	b.WriteString(cr2EcVtCommon)
	return b.String()
}

// cr2EcFixtureAll is the EC driver's whole fixture.
var cr2EcFixtureAll = cr2EcFixture + cr2EcVtFixture()

// cr2EcNeg is -(x, y), nil for the identity.
func cr2EcNeg(c elliptic.Curve, x, y *big.Int) (*big.Int, *big.Int) {
	if x == nil {
		return nil, nil
	}
	return x, new(big.Int).Sub(c.Params().P, y)
}

// TestSTDCRYPTO_EcVtFormulas: VtAdd, VtAddAffine and VtDouble on both
// curves against crypto/elliptic, with the left operand's Z = 2 (and
// VtAdd's right one's Z = 3), on each case the formulas do not take and a
// branch does: the point at infinity on either side, P + P and P + (-P),
// and on generic pairs.
func TestSTDCRYPTO_EcVtFormulas(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 0x7654}
	var lines, want []string
	for c := range 2 {
		cv := cr2EcCurveOf(c)
		pt := func(x, y *big.Int) string { return cr2EcPoint(cv, x, y) }
		add := func(op int, x1, y1, x2, y2 *big.Int) {
			sx, sy := cr2EcAdd(cv, x1, y1, x2, y2)
			lines = append(lines, fmt.Sprintf("T %d %d %s %s", c, op, pt(x1, y1), pt(x2, y2)))
			want = append(want, "T "+pt(sx, sy))
		}
		dbl := func(x, y *big.Int) {
			sx, sy := cr2EcAdd(cv, x, y, x, y)
			lines = append(lines, fmt.Sprintf("T %d 2 %s ", c, pt(x, y)))
			want = append(want, "T "+pt(sx, sy))
		}
		for range 4 {
			px, py := cr2EcRandPoint(cv, &rnd)
			qx, qy := cr2EcRandPoint(cv, &rnd)
			nx, ny := cr2EcNeg(cv, px, py)
			// VtAdd: infinity left, right and both, P + P, P - P, generic.
			add(0, nil, nil, qx, qy)
			add(0, px, py, nil, nil)
			add(0, nil, nil, nil, nil)
			add(0, px, py, px, py)
			add(0, px, py, nx, ny)
			add(0, px, py, qx, qy)
			// VtAddAffine: infinity left, P + P, P - P, generic.
			add(1, nil, nil, qx, qy)
			add(1, px, py, px, py)
			add(1, px, py, nx, ny)
			add(1, px, py, qx, qy)
			// VtDouble: infinity, a point.
			dbl(nil, nil)
			dbl(px, py)
		}
		// G + G and G - G.
		gx, gy := cv.Params().Gx, cv.Params().Gy
		ngx, ngy := cr2EcNeg(cv, gx, gy)
		add(0, gx, gy, gx, gy)
		add(1, gx, gy, ngx, ngy)
	}
	cr2EcCheck(t, "Vt formulas", lines, want)
}

// TestSTDCRYPTO_EcVtMulAddEdges: VtMulAdd's loop driven into each
// exceptional case, against crypto/elliptic: q = G and q = -G with u1 =
// u2 (the second addition meets P + P or P - P), q = ((n + 1) / 2) G and
// ((n - 1) / 2) G with u1 = 1 and u2 = 2 (2 u2 q is G or -G when G is
// added), u1 = 0, u2 = 0 (which EcdsaVerify never passes, since r and s
// are nonzero mod a prime) and both, and u1 = n - 1 with q = G and u2 = 1.
func TestSTDCRYPTO_EcVtMulAddEdges(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 0x7655}
	var lines, want []string
	for c := range 2 {
		cv := cr2EcCurveOf(c)
		size := (cv.Params().BitSize + 7) / 8
		n := cv.Params().N
		sc := func(v *big.Int) []byte { return new(big.Int).Mod(v, n).FillBytes(make([]byte, size)) }
		gx, gy := cv.Params().Gx, cv.Params().Gy
		ngx, ngy := cr2EcNeg(cv, gx, gy)
		y := func(u1 []byte, qx, qy *big.Int, u2 []byte) {
			ax, ay := cr2EcMul(cv, gx, gy, u1)
			bx, by := cr2EcMul(cv, qx, qy, u2)
			sx, sy := cr2EcAdd(cv, ax, ay, bx, by)
			lines = append(lines, fmt.Sprintf("Y %d %x %s %x", c, u1, cr2EcPoint(cv, qx, qy), u2))
			want = append(want, "Y "+cr2EcPoint(cv, sx, sy))
		}
		one, two := sc(big.NewInt(1)), sc(big.NewInt(2))
		y(one, gx, gy, one)
		y(one, ngx, ngy, one)
		half := new(big.Int).Rsh(new(big.Int).Add(n, big.NewInt(1)), 1)
		hx, hy := cv.ScalarBaseMult(half.Bytes())
		y(one, hx, hy, two)
		half2 := new(big.Int).Rsh(new(big.Int).Sub(n, big.NewInt(1)), 1)
		h2x, h2y := cv.ScalarBaseMult(half2.Bytes())
		y(one, h2x, h2y, two)
		y(sc(new(big.Int).Sub(n, big.NewInt(1))), gx, gy, one)
		for range 4 {
			k := make([]byte, size)
			rnd.fill(k)
			k = sc(new(big.Int).SetBytes(k))
			y(k, gx, gy, k)
			y(k, ngx, ngy, k)
			qx, qy := cr2EcRandPoint(cv, &rnd)
			zero := make([]byte, size)
			y(zero, qx, qy, k)
			y(k, qx, qy, zero)
			y(zero, qx, qy, zero)
		}
	}
	cr2EcCheck(t, "VtMulAdd edges", lines, want)
}

// cr2EcSignWith is the fixed-form signature (r, s) of digest under d with
// r given (s = (e + r d) / k): with r = x(k G) mod n a genuine signature,
// with another r one whose R = u1 G + u2 Q is still k G.
func cr2EcSignWith(cv elliptic.Curve, d, k, r *big.Int, digest []byte) []byte {
	n := cv.Params().N
	size := (cv.Params().BitSize + 7) / 8
	e := new(big.Int).SetBytes(digest)
	if len(digest) > size {
		e.SetBytes(digest[:size])
	}
	e.Mod(e, n)
	s := new(big.Int).Mul(r, d)
	s.Add(s, e).Mul(s, new(big.Int).ModInverse(k, n)).Mod(s, n)
	out := make([]byte, 2*size)
	r.FillBytes(out[:size])
	s.FillBytes(out[size:])
	return out
}

// cr2EcLargeX is a signature whose R has x(R) of n or more, so r = x(R) -
// n and X = r Z^2 fails: the check needs X = (r + n) Z^2. Wycheproof's
// construction: R a point with x in [n, p), u1 and u2 at random, Q =
// u2^-1 (R - u1 G), s = r / u2 and e = u1 s. It returns the public point,
// the digest (e) and the signature, or nil when no such R was found.
func cr2EcLargeX(cv elliptic.Curve, rnd *cryptoSplitmix) (pub, digest, sig []byte) {
	prm := cv.Params()
	p, n := prm.P, prm.N
	size := (prm.BitSize + 7) / 8
	span := new(big.Int).Sub(p, n)
	for range 200 {
		b := make([]byte, size)
		rnd.fill(b)
		x := new(big.Int).Mod(new(big.Int).SetBytes(b), span)
		x.Add(x, n)
		rhs := new(big.Int).Mul(x, x)
		rhs.Mul(rhs, x).Sub(rhs, new(big.Int).Mul(big.NewInt(3), x)).Add(rhs, prm.B).Mod(rhs, p)
		yv := new(big.Int).ModSqrt(rhs, p)
		if yv == nil || new(big.Int).Sub(x, n).Sign() == 0 {
			continue
		}
		rnd.fill(b)
		u1 := new(big.Int).Mod(new(big.Int).SetBytes(b), n)
		rnd.fill(b)
		u2 := new(big.Int).Mod(new(big.Int).SetBytes(b), n)
		if u1.Sign() == 0 || u2.Sign() == 0 {
			continue
		}
		ax, ay := cv.ScalarBaseMult(u1.Bytes())
		bx, by := cr2EcAdd(cv, x, yv, ax, new(big.Int).Sub(p, ay))
		if bx == nil {
			continue
		}
		u2i := new(big.Int).ModInverse(u2, n)
		qx, qy := cv.ScalarMult(bx, by, u2i.Bytes())
		r := new(big.Int).Sub(x, n)
		s := new(big.Int).Mul(r, u2i)
		s.Mod(s, n)
		e := new(big.Int).Mul(u1, s)
		e.Mod(e, n)
		pub, _ = hexBytes(cr2EcPoint(cv, qx, qy))
		digest = e.FillBytes(make([]byte, size))
		sig = make([]byte, 2*size)
		r.FillBytes(sig[:size])
		s.FillBytes(sig[size:])
		return pub, digest, sig
	}
	return nil, nil, nil
}

// hexBytes decodes s.
func hexBytes(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := range out {
		var v byte
		for _, ch := range s[2*i : 2*i+2] {
			v <<= 4
			switch {
			case ch >= '0' && ch <= '9':
				v |= byte(ch - '0')
			case ch >= 'a' && ch <= 'f':
				v |= byte(ch-'a') + 10
			}
		}
		out[i] = v
	}
	return out, nil
}

// TestSTDCRYPTO_EcVerifyMatchesOld: EcdsaVerify (the Vt functions) and
// the projective verification (FxOldEcdsaVerify) give Go's verdict on every
// signature, in dev and release: Go's signatures and their mutations
// (r or s off by one, s replaced by n - s, random r and s, a flipped
// digest or key bit), and the constructed cases of the file comment.
func TestSTDCRYPTO_EcVerifyMatchesOld(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 0x7656}
	var lines, want []string
	needSecond := 0
	for c := range 2 {
		cv := cr2EcCurveOf(c)
		prm := cv.Params()
		p, n := prm.P, prm.N
		size := (prm.BitSize + 7) / 8
		scalar := func() *big.Int {
			for {
				b := make([]byte, size)
				rnd.fill(b)
				v := new(big.Int).Mod(new(big.Int).SetBytes(b), n)
				if v.Sign() != 0 {
					return v
				}
			}
		}
		pubOf := func(d *big.Int) []byte {
			x, y := cv.ScalarBaseMult(d.FillBytes(make([]byte, size)))
			b, _ := hexBytes(cr2EcPoint(cv, x, y))
			return b
		}
		// ask adds the vector with Go's verdict, which must be expect
		// when expect is not nil.
		ask := func(what string, pub, digest, sig []byte, expect *bool) {
			ok := cr2GoEcdsa(cv, pub, digest, sig, true)
			if expect != nil && ok != *expect {
				t.Fatalf("%s (curve %d): Go says %v, the construction %v", what, c, ok, *expect)
			}
			lines = append(lines, fmt.Sprintf("V %d %x %x fixed %x", c, pub, digest, sig), fmt.Sprintf("O %d %x %x fixed %x", c, pub, digest, sig))
			want = append(want, fmt.Sprintf("V %v", ok), fmt.Sprintf("O %v", ok))
		}
		yes, no := true, false
		for range 30 {
			d := scalar()
			pub := pubOf(d)
			digest := make([]byte, size)
			rnd.fill(digest)
			k := scalar()
			rx, _ := cv.ScalarBaseMult(k.FillBytes(make([]byte, size)))
			r := new(big.Int).Mod(rx, n)
			sig := cr2EcSignWith(cv, d, k, r, digest)
			ask("signature", pub, digest, sig, &yes)
			sv := new(big.Int).SetBytes(sig[size:])
			flip := func(rv, sv *big.Int) []byte {
				out := make([]byte, 2*size)
				new(big.Int).Mod(rv, n).FillBytes(out[:size])
				new(big.Int).Mod(sv, n).FillBytes(out[size:])
				return out
			}
			ask("s = n - s", pub, digest, flip(r, new(big.Int).Sub(n, sv)), &yes)
			ask("r + 1", pub, digest, flip(new(big.Int).Add(r, big.NewInt(1)), sv), nil)
			ask("s + 1", pub, digest, flip(r, new(big.Int).Add(sv, big.NewInt(1))), nil)
			ask("random r and s", pub, digest, flip(scalar(), scalar()), nil)
			bd := append([]byte(nil), digest...)
			bd[0] ^= 0x80
			ask("digest bit", pub, bd, sig, &no)
			// An r that a check without the r < p - n guard accepts:
			// r = x(k G) + p - n, so (r + n) mod p = x(k G).
			if r.Cmp(rx) == 0 {
				bad := new(big.Int).Add(rx, new(big.Int).Sub(p, n))
				if bad.Cmp(n) < 0 {
					ask("r = x + p - n", pub, digest, cr2EcSignWith(cv, d, k, bad, digest), &no)
				}
			}
		}
		// Q = G (d = 1) with u1 = u2: e = r, s = 2 r / k, valid; Q = -G (d
		// = n - 1) with e = r: R is the point at infinity.
		for range 4 {
			k := scalar()
			rx, _ := cv.ScalarBaseMult(k.FillBytes(make([]byte, size)))
			r := new(big.Int).Mod(rx, n)
			digest := r.FillBytes(make([]byte, size))
			ask("Q = G, u1 = u2", pubOf(big.NewInt(1)), digest, cr2EcSignWith(cv, big.NewInt(1), k, r, digest), &yes)
			s := scalar()
			sig := make([]byte, 2*size)
			r.FillBytes(sig[:size])
			s.FillBytes(sig[size:])
			ask("Q = -G, u1 = u2", pubOf(new(big.Int).Sub(n, big.NewInt(1))), digest, sig, &no)
			// u1 = 0: a digest of zero and of n.
			d := scalar()
			for _, dg := range [][]byte{make([]byte, size), n.FillBytes(make([]byte, size))} {
				ask("u1 = 0", pubOf(d), dg, cr2EcSignWith(cv, d, k, r, dg), &yes)
			}
		}
		// x(R) of n or more: the second comparison.
		for range 3 {
			pub, digest, sig := cr2EcLargeX(cv, &rnd)
			if pub == nil {
				t.Fatalf("curve %d: no point with x in [n, p) found", c)
			}
			ask("x(R) >= n", pub, digest, sig, &yes)
			needSecond++
			bad := append([]byte(nil), sig...)
			bad[2*size-1] ^= 1
			ask("x(R) >= n, s flipped", pub, digest, bad, &no)
		}
	}
	cr2EcCheck(t, "verify against old", lines, want)
	t.Logf("%d vectors, %d of them with x(R) >= n", len(lines)/2, needSecond)
}

// TestSTDCRYPTO_WycheproofLargeX counts the valid Wycheproof ECDSA vectors
// whose R has x(R) of n or more (the ones X = r Z^2 alone would refuse),
// per curve; TestSTDCRYPTO_WycheproofEcdsa runs them. Each curve's files
// must hold at least one.
func TestSTDCRYPTO_WycheproofLargeX(t *testing.T) {
	counts := map[string]int{}
	for _, name := range []string{"ecdsa_secp256r1_sha256_p1363_test.json", "ecdsa_secp384r1_sha384_p1363_test.json"} {
		var wp cr2WpEcdsa
		cr2Wycheproof(t, name, &wp)
		for _, g := range wp.TestGroups {
			cv := elliptic.P256()
			if g.PublicKey.Curve == "secp384r1" {
				cv = elliptic.P384()
			}
			prm := cv.Params()
			size := (prm.BitSize + 7) / 8
			pub, _ := hexBytes(g.PublicKey.Uncompressed)
			qx, qy := new(big.Int).SetBytes(pub[1:1+size]), new(big.Int).SetBytes(pub[1+size:])
			for _, tc := range g.Tests {
				if tc.Result != "valid" {
					continue
				}
				sig, _ := hexBytes(tc.Sig)
				msg, _ := hexBytes(tc.Msg)
				digest := cr2WpDigest(t, g.Sha, msg)
				r, s := new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])
				e := new(big.Int).SetBytes(digest)
				if len(digest) > size {
					e.SetBytes(digest[:size])
				}
				w := new(big.Int).ModInverse(s, prm.N)
				u1 := new(big.Int).Mul(e, w)
				u1.Mod(u1, prm.N)
				u2 := new(big.Int).Mul(r, w)
				u2.Mod(u2, prm.N)
				ax, ay := cv.ScalarBaseMult(u1.Bytes())
				bx, by := cv.ScalarMult(qx, qy, u2.Bytes())
				x, _ := cr2EcAdd(cv, ax, ay, bx, by)
				if x != nil && x.Cmp(prm.N) >= 0 {
					counts[prm.Name]++
				}
			}
		}
	}
	for _, c := range []string{"P-256", "P-384"} {
		if counts[c] == 0 {
			t.Errorf("%s: no valid Wycheproof vector has x(R) >= n", c)
		}
	}
	t.Logf("valid vectors with x(R) >= n: %v", counts)
}

// cr2VtMarker matches a name of verification's variable-time arithmetic.
var cr2VtMarker = regexp.MustCompile(`\b(P256|P384)Vt[A-Za-z0-9_]*`)

// cr2VtItem matches a top-level item of an Mk file: a function (a method
// with its type), a struct or a const, with its name.
var cr2VtItem = regexp.MustCompile(`^(?:export )?(?:(?:struct|const|enum|interface|union) )?([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\b`)

// cr2VtAllowed matches the items that may name a Vt function or type: the
// Vt items and <curve>EcdsaVerify; cr2VtCore matches <curve>EcdsaVerify.
var (
	cr2VtAllowed = regexp.MustCompile(`^(P256|P384)(Vt[A-Za-z0-9_]*|EcdsaVerify)$`)
	cr2VtCore    = regexp.MustCompile(`^(P256|P384)EcdsaVerify$`)
)

// cr2VtScan returns each use of a Vt name in files (name to text) outside
// a Vt item and outside <curve>EcdsaVerify, and each call of
// <curve>EcdsaVerify outside EcdsaVerify, as file:line: item: text.
func cr2VtScan(files map[string]string) []string {
	verify := regexp.MustCompile(`\b(P256|P384)EcdsaVerify\(`)
	var bad []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		item := ""
		for i, l := range strings.Split(strings.ReplaceAll(files[name], "\r\n", "\n"), "\n") {
			code := l
			if j := strings.Index(code, "//"); j >= 0 {
				code = code[:j]
			}
			if code != "" && code[0] != ' ' && code[0] != '\t' && code[0] != '}' {
				if m := cr2VtItem.FindStringSubmatch(code); m != nil && m[1] != "import" {
					item = m[1]
				}
			}
			if cr2VtMarker.MatchString(code) && !cr2VtAllowed.MatchString(item) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s: %s", name, i+1, item, strings.TrimSpace(l)))
			}
			if verify.MatchString(code) && item != "EcdsaVerify" && !cr2VtCore.MatchString(item) {
				bad = append(bad, fmt.Sprintf("%s:%d: %s calls EcdsaVerify's core: %s", name, i+1, item, strings.TrimSpace(l)))
			}
		}
	}
	return bad
}

// TestSTDCRYPTO_VtOnlyFromVerify: in std/crypto's sources (tests_ files
// aside), a Vt name appears only inside a Vt item or <curve>EcdsaVerify,
// and <curve>EcdsaVerify is called only from EcdsaVerify, whose inputs are
// public (the signature, the digest and the public key); so no secret
// reaches the variable-time arithmetic. The scanner itself is checked on a
// planted call from EcdhShared and on a clean file.
func TestSTDCRYPTO_VtOnlyFromVerify(t *testing.T) {
	planted := map[string]string{"x.mk": `// A file.
EcdhShared(curve: Curve, secret: &EcSecret, peer: []u8, out: mut []u8): Error!void {
    p := P256VtDouble(&P256VtInf)
}

P256VtOk(p: &P256VtPt): P256VtPt {
    return P256VtDouble(p)
}

Sign(d: []u8): bool {
    return P256EcdsaVerify(d, d, d, d)
}
`}
	got := cr2VtScan(planted)
	if len(got) != 2 || !strings.Contains(got[0], "EcdhShared") || !strings.Contains(got[1], "Sign calls") {
		t.Fatalf("scanner on the planted file: %q, want the call from EcdhShared and the one from Sign", got)
	}
	if got := cr2VtScan(map[string]string{"y.mk": "P256VtA(p: &P256VtPt): P256VtPt {\n    return P256VtB(p)\n}\n\nP256EcdsaVerify(q: []u8): bool {\n    return P256VtXIs(&P256VtInf, q)\n}\n\nexport EcdsaVerify(q: []u8): bool {\n    return P256EcdsaVerify(q)\n}\n"}); len(got) != 0 {
		t.Fatalf("scanner on a clean file: %q, want nothing", got)
	}
	files := map[string]string{}
	ents, err := os.ReadDir("crypto")
	if err != nil {
		t.Fatal(err)
	}
	uses := 0
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mk") || strings.HasPrefix(e.Name(), "tests_") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("crypto", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
		uses += len(cr2VtMarker.FindAllString(string(b), -1))
	}
	if uses == 0 {
		t.Fatal("no Vt name in std/crypto: the scan reads nothing")
	}
	bad := cr2VtScan(files)
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("a Vt name or EcdsaVerify's core outside verification: %s", b)
	}
	t.Logf("%d uses of a Vt name read in %d files", uses, len(files))
}
