// Why: the P-256 and P-384 code is layered, and a bug in a lower layer shows
// up far away; each layer is tested on its own against a model.
// What: differential tests of field inversion, the group law, scalar
// multiplication, EcSecret, ECDH and ECDSA, and RFC 6979 A.2.5's vectors.
// How: math/big models and crypto/elliptic, crypto/ecdh and crypto/ecdsa as
// oracles, through the EC driver; ECDSA signatures must equal Go's bytes.

package std

import (
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// cr2EcCurveOf is the Go curve of driver curve c.
func cr2EcCurveOf(c int) elliptic.Curve {
	if c == 0 {
		return elliptic.P256()
	}
	return elliptic.P384()
}

// cr2EcPoint is (x, y) uncompressed, or "" for the identity (nil x).
func cr2EcPoint(c elliptic.Curve, x, y *big.Int) string {
	if x == nil {
		return ""
	}
	n := (c.Params().BitSize + 7) / 8
	b := make([]byte, 1+2*n)
	b[0] = 4
	x.FillBytes(b[1 : 1+n])
	y.FillBytes(b[1+n:])
	return hex.EncodeToString(b)
}

// cr2EcAdd is the affine sum in the model; nil x is the identity.
func cr2EcAdd(c elliptic.Curve, x1, y1, x2, y2 *big.Int) (*big.Int, *big.Int) {
	p := c.Params().P
	if x1 == nil {
		return x2, y2
	}
	if x2 == nil {
		return x1, y1
	}
	var l *big.Int
	if x1.Cmp(x2) == 0 {
		if new(big.Int).Add(y1, y2).Mod(new(big.Int).Add(y1, y2), p).Sign() == 0 {
			return nil, nil
		}
		// (3x^2 - 3) / 2y
		num := new(big.Int).Mul(x1, x1)
		num.Mul(num, big.NewInt(3)).Sub(num, big.NewInt(3))
		den := new(big.Int).Lsh(y1, 1)
		l = num.Mul(num, den.ModInverse(den.Mod(den, p), p))
	} else {
		num := new(big.Int).Sub(y2, y1)
		den := new(big.Int).Sub(x2, x1)
		l = num.Mul(num, den.ModInverse(den.Mod(den, p), p))
	}
	l.Mod(l, p)
	x3 := new(big.Int).Mul(l, l)
	x3.Sub(x3, x1).Sub(x3, x2).Mod(x3, p)
	y3 := new(big.Int).Sub(x1, x3)
	y3.Mul(y3, l).Sub(y3, y1).Mod(y3, p)
	return x3, y3
}

// cr2EcRandPoint is k G for a pseudo-random k.
func cr2EcRandPoint(c elliptic.Curve, rnd *cryptoSplitmix) (*big.Int, *big.Int) {
	k := make([]byte, (c.Params().BitSize+7)/8)
	rnd.fill(k)
	return c.ScalarBaseMult(k)
}

// cr2EcMul is k (x, y) in the model, the identity as nil.
func cr2EcMul(c elliptic.Curve, x, y *big.Int, k []byte) (*big.Int, *big.Int) {
	if x == nil || new(big.Int).Mod(new(big.Int).SetBytes(k), c.Params().N).Sign() == 0 {
		return nil, nil
	}
	rx, ry := c.ScalarMult(x, y, k)
	if rx.Sign() == 0 && ry.Sign() == 0 {
		return nil, nil
	}
	return rx, ry
}

// TestSTDCRYPTO_EcFieldInverse: 1 / a in the four fields against
// ModInverse, with 0 (whose Fermat inverse is 0), 1, m - 1, m - 2 and
// random elements.
func TestSTDCRYPTO_EcFieldInverse(t *testing.T) {
	t.Parallel()
	mods := []*big.Int{elliptic.P256().Params().P, elliptic.P256().Params().N, elliptic.P384().Params().P, elliptic.P384().Params().N}
	rnd := cryptoSplitmix{s: 61}
	var lines, want []string
	for f, m := range mods {
		n := (m.BitLen() + 7) / 8
		vals := []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), new(big.Int).Sub(m, big.NewInt(1)), new(big.Int).Sub(m, big.NewInt(2))}
		for range 30 {
			b := make([]byte, n)
			rnd.fill(b)
			vals = append(vals, new(big.Int).Mod(new(big.Int).SetBytes(b), m))
		}
		for _, a := range vals {
			inv := new(big.Int)
			if a.Sign() != 0 {
				inv.ModInverse(a, m)
			}
			lines = append(lines, fmt.Sprintf("I %d %x", f, a.FillBytes(make([]byte, n))))
			want = append(want, fmt.Sprintf("I %x", inv.FillBytes(make([]byte, n))))
		}
	}
	cr2EcCheck(t, "inverse", lines, want)
}

// TestSTDCRYPTO_EcGroupLaw: p + q and 2p against the affine model on both
// curves, for random points, p + p, p + (-p), p + O, O + p, O + O, 2O and
// a point of y = 0's absence (no point of order 2 exists: the order is
// prime, so the formulas meet no exceptional pair).
func TestSTDCRYPTO_EcGroupLaw(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 62}
	var lines, want []string
	for c := range 2 {
		cv := cr2EcCurveOf(c)
		p := cv.Params().P
		add := func(x1, y1, x2, y2 *big.Int) {
			rx, ry := cr2EcAdd(cv, x1, y1, x2, y2)
			lines = append(lines, fmt.Sprintf("A %d %s %s", c, cr2EcPoint(cv, x1, y1), cr2EcPoint(cv, x2, y2)))
			want = append(want, "A "+cr2EcPoint(cv, rx, ry))
		}
		dbl := func(x, y *big.Int) {
			rx, ry := cr2EcAdd(cv, x, y, x, y)
			lines = append(lines, fmt.Sprintf("D %d %s", c, cr2EcPoint(cv, x, y)))
			want = append(want, "D "+cr2EcPoint(cv, rx, ry))
		}
		for range 20 {
			x1, y1 := cr2EcRandPoint(cv, &rnd)
			x2, y2 := cr2EcRandPoint(cv, &rnd)
			add(x1, y1, x2, y2)
			add(x1, y1, x1, y1)
			add(x1, y1, x1, new(big.Int).Sub(p, y1))
			add(x1, y1, nil, nil)
			add(nil, nil, x2, y2)
			dbl(x1, y1)
		}
		add(nil, nil, nil, nil)
		dbl(nil, nil)
		gx, gy := cv.Params().Gx, cv.Params().Gy
		add(gx, gy, gx, gy)
		dbl(gx, gy)
		add(gx, gy, gx, new(big.Int).Sub(p, gy))
	}
	cr2EcCheck(t, "group law", lines, want)
}

// TestSTDCRYPTO_EcScalarMult: the constant-time k p and the public u1 G +
// u2 q on both curves against crypto/elliptic, for random scalars and 0,
// 1, 2, n - 1, n, n + 1 and all-ones bytes.
func TestSTDCRYPTO_EcScalarMult(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 63}
	var lines, want []string
	for c := range 2 {
		cv := cr2EcCurveOf(c)
		size := (cv.Params().BitSize + 7) / 8
		n := cv.Params().N
		scalar := func(v *big.Int) []byte { return v.FillBytes(make([]byte, size)) }
		ks := [][]byte{scalar(big.NewInt(0)), scalar(big.NewInt(1)), scalar(big.NewInt(2)), scalar(new(big.Int).Sub(n, big.NewInt(1))), scalar(n), scalar(new(big.Int).Add(n, big.NewInt(1))), []byte(strings.Repeat("\xff", size))}
		for range 12 {
			k := make([]byte, size)
			rnd.fill(k)
			ks = append(ks, k)
		}
		for i, k := range ks {
			px, py := cr2EcRandPoint(cv, &rnd)
			rx, ry := cr2EcMul(cv, px, py, k)
			lines = append(lines, fmt.Sprintf("X %d %s %x", c, cr2EcPoint(cv, px, py), k))
			want = append(want, "X "+cr2EcPoint(cv, rx, ry))
			u := ks[(i*5+3)%len(ks)]
			ax, ay := cr2EcMul(cv, cv.Params().Gx, cv.Params().Gy, u)
			bx, by := cr2EcMul(cv, px, py, k)
			sx, sy := cr2EcAdd(cv, ax, ay, bx, by)
			lines = append(lines, fmt.Sprintf("Y %d %x %s %x", c, u, cr2EcPoint(cv, px, py), k))
			want = append(want, "Y "+cr2EcPoint(cv, sx, sy))
		}
		// k G through the window, and k q with q = -G.
		gx, gy := cv.Params().Gx, cv.Params().Gy
		for _, k := range ks {
			rx, ry := cr2EcMul(cv, gx, gy, k)
			lines = append(lines, fmt.Sprintf("X %d %s %x", c, cr2EcPoint(cv, gx, gy), k))
			want = append(want, "X "+cr2EcPoint(cv, rx, ry))
		}
	}
	cr2EcCheck(t, "scalar mult", lines, want)
}

// cr2EcPriv is Go's P-256 key of d.
func cr2EcPriv(t *testing.T, d []byte) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// cr2EcFixed is a DER signature as r || s of size bytes each.
func cr2EcFixed(t *testing.T, der []byte, size int) []byte {
	t.Helper()
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 2*size)
	sig.R.FillBytes(out[:size])
	sig.S.FillBytes(out[size:])
	return out
}

// TestSTDCRYPTO_EcKeysMatchGo: EcSecret.FromBytes and Public against
// crypto/ecdh, the refused scalars (0, n, n + 1, all ones, wrong lengths),
// and ECDH against crypto/ecdh, with peers off the curve, compressed, the
// identity's encoding and a short out refused, out untouched.
func TestSTDCRYPTO_EcKeysMatchGo(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 64}
	n := elliptic.P256().Params().N
	var lines, want []string
	for i := range 24 {
		d := make([]byte, 32)
		rnd.fill(d)
		if i == 0 {
			d = big.NewInt(1).FillBytes(make([]byte, 32))
		}
		if i == 1 {
			d = new(big.Int).Sub(n, big.NewInt(1)).FillBytes(make([]byte, 32))
		}
		k, err := ecdh.P256().NewPrivateKey(d)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("P %x", d))
		want = append(want, fmt.Sprintf("P %x", k.PublicKey().Bytes()))
		pd := make([]byte, 32)
		rnd.fill(pd)
		peer, _ := ecdh.P256().NewPrivateKey(pd)
		shared, err := k.ECDH(peer.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		pub := peer.PublicKey().Bytes()
		lines = append(lines, fmt.Sprintf("H %x %x 32", d, pub))
		want = append(want, fmt.Sprintf("H %x", shared))
		// A short or long out, a flipped coordinate bit (off the curve), a
		// compressed point, a truncated point.
		lines = append(lines, fmt.Sprintf("H %x %x 31", d, pub), fmt.Sprintf("H %x %x 33", d, pub))
		want = append(want, "H bad-length true", "H bad-length true")
		bad := append([]byte(nil), pub...)
		bad[1+i%64] ^= 1 << (i % 8)
		comp := append([]byte{2 + pub[64]&1}, pub[1:33]...)
		lines = append(lines, fmt.Sprintf("H %x %x 32", d, bad), fmt.Sprintf("H %x %x 32", d, comp), fmt.Sprintf("H %x %x 32", d, pub[:64]))
		want = append(want, "H bad-key true", "H bad-key true", "H bad-key true")
	}
	for _, d := range []string{"", "00", strings.Repeat("00", 32), fmt.Sprintf("%x", n.FillBytes(make([]byte, 32))), fmt.Sprintf("%x", new(big.Int).Add(n, big.NewInt(1)).FillBytes(make([]byte, 32))), strings.Repeat("ff", 32)} {
		lines = append(lines, "P "+d)
		if len(d) != 64 {
			want = append(want, "P bad-length")
		} else {
			want = append(want, "P bad-key")
		}
	}
	lines = append(lines, fmt.Sprintf("H %s 00 32", strings.Repeat("11", 32)), "R")
	want = append(want, "H bad-key true", "R bad-key bad-key bad-key bad-key bad-key")
	cr2EcCheck(t, "keys", lines, want)
}

// TestSTDCRYPTO_EcGenerate: Generate's scalar is in [1, n - 1] and its
// public point is Go's for that scalar, and two draws differ.
func TestSTDCRYPTO_EcGenerate(t *testing.T) {
	t.Parallel()
	lines := []string{"G", "G", "G", "G"}
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		seen := map[string]bool{}
		for _, g := range got {
			f := strings.Fields(g)
			if len(f) != 3 {
				t.Fatalf("%s: %q", v.name, g)
			}
			d, _ := hex.DecodeString(f[1])
			k, err := ecdh.P256().NewPrivateKey(d)
			if err != nil {
				t.Fatalf("%s: generated scalar %s refused by Go: %v", v.name, f[1], err)
			}
			if w := hex.EncodeToString(k.PublicKey().Bytes()); w != f[2] {
				t.Errorf("%s: public %s, Go %s", v.name, f[2], w)
			}
			if seen[f[1]] {
				t.Errorf("%s: scalar %s drawn twice", v.name, f[1])
			}
			seen[f[1]] = true
		}
	}
}

// TestSTDCRYPTO_EcdsaSignMatchesGo: RFC 6979 signatures of SHA-256,
// SHA-384 and SHA-512 digests, in both formats, equal Go's
// PrivateKey.Sign(nil, digest, hash) byte for byte; a digest of another
// length (20, 1 and 100 bytes) signs a signature Go verifies; an empty
// digest and a short out are BadLength with out untouched.
func TestSTDCRYPTO_EcdsaSignMatchesGo(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 65}
	var lines, want []string
	type other struct {
		d, digest []byte
		at        int
	}
	var others []other
	for i := range 24 {
		d := make([]byte, 32)
		rnd.fill(d)
		priv := cr2EcPriv(t, d)
		h := []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512}[i%3]
		digest := make([]byte, h.Size())
		rnd.fill(digest)
		der, err := priv.Sign(nil, digest, h)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, fmt.Sprintf("S %x %x der 80", d, digest), fmt.Sprintf("S %x %x fixed 70", d, digest))
		want = append(want, fmt.Sprintf("S %x true", der), fmt.Sprintf("S %x true", cr2EcFixed(t, der, 32)))
		lines = append(lines, fmt.Sprintf("S %x %x fixed 63", d, digest), fmt.Sprintf("S %x %x der %d", d, digest, len(der)-1), fmt.Sprintf("S %x  der 80", d))
		want = append(want, "S bad-length true", "S bad-length true", "S bad-length true")
		for _, n := range []int{20, 1, 100} {
			od := make([]byte, n)
			rnd.fill(od)
			others = append(others, other{d, od, len(lines)})
			lines = append(lines, fmt.Sprintf("S %x %x der 80", d, od))
			want = append(want, "")
		}
	}
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		for i := range lines {
			if want[i] != "" && got[i] != want[i] {
				t.Errorf("%s: %s\n got  %s\n want %s", v.name, lines[i], got[i], want[i])
			}
		}
		for _, o := range others {
			f := strings.Fields(got[o.at])
			sig, _ := hex.DecodeString(f[1])
			if !ecdsa.VerifyASN1(&cr2EcPriv(t, o.d).PublicKey, o.digest, sig) {
				t.Errorf("%s: Go does not verify %s", v.name, lines[o.at])
			}
		}
	}
}

// TestSTDCRYPTO_EcdsaVerifyMatchesGo: Go's signatures over both curves,
// both formats and digests of 32, 48 and 64 bytes verify; a flipped bit in
// the signature, the digest or the key, r or s of 0 or n, a DER with
// trailing bytes, a negative or padded INTEGER, and the wrong format do
// not.
func TestSTDCRYPTO_EcdsaVerifyMatchesGo(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 66}
	var lines, want []string
	for c := range 2 {
		cv := cr2EcCurveOf(c)
		size := (cv.Params().BitSize + 7) / 8
		n := cv.Params().N
		for i := range 18 {
			d := make([]byte, size)
			rnd.fill(d)
			priv, err := ecdsa.ParseRawPrivateKey(cv, d)
			if err != nil {
				t.Fatal(err)
			}
			pub, _ := priv.PublicKey.Bytes()
			digest := make([]byte, []int{32, 48, 64}[i%3])
			rnd.fill(digest)
			der, err := ecdsa.SignASN1(nil, priv, digest)
			if err != nil {
				t.Fatal(err)
			}
			fixed := cr2EcFixed(t, der, size)
			add := func(pub, digest []byte, f string, sig []byte, ok bool) {
				lines = append(lines, fmt.Sprintf("V %d %x %x %s %x", c, pub, digest, f, sig))
				want = append(want, fmt.Sprintf("V %v", ok))
			}
			add(pub, digest, "der", der, true)
			add(pub, digest, "fixed", fixed, true)
			add(pub, digest, "fixed", der, false)
			add(pub, digest, "der", fixed, false)
			add(pub, digest, "der", append(append([]byte(nil), der...), 0), false)
			bad := append([]byte(nil), fixed...)
			bad[i%len(bad)] ^= 1 << (i % 8)
			add(pub, digest, "fixed", bad, false)
			bd := append([]byte(nil), digest...)
			bd[i%len(bd)] ^= 1
			add(pub, bd, "der", der, false)
			bp := append([]byte(nil), pub...)
			bp[1+i%(2*size)] ^= 1
			add(bp, digest, "der", der, false)
			add(pub, nil, "der", der, false)
			zero := make([]byte, size)
			nb := n.FillBytes(make([]byte, size))
			add(pub, digest, "fixed", append(append([]byte(nil), zero...), fixed[size:]...), false)
			add(pub, digest, "fixed", append(append([]byte(nil), fixed[:size]...), zero...), false)
			add(pub, digest, "fixed", append(append([]byte(nil), nb...), fixed[size:]...), false)
			add(pub, digest, "fixed", append(append([]byte(nil), fixed[:size]...), nb...), false)
			// r + n has the same x mod n only when x >= n; it must fail as
			// a value out of range, not as an encoding.
			rn := new(big.Int).Add(new(big.Int).SetBytes(fixed[:size]), n)
			if rn.BitLen() <= 8*size {
				add(pub, digest, "fixed", append(rn.FillBytes(make([]byte, size)), fixed[size:]...), false)
			}
		}
	}
	cr2EcCheck(t, "verify", lines, want)
}

// TestSTDCRYPTO_EcdsaRfc6979: RFC 6979 A.2.5's P-256 vectors for
// "sample" and "test" with SHA-256, SHA-384 and SHA-512, in Fixed form.
func TestSTDCRYPTO_EcdsaRfc6979(t *testing.T) {
	t.Parallel()
	const x = "c9afa9d845ba75166b5c215767b1d6934e50c3db36e89b127b8a622b120f6721"
	vecs := []struct {
		msg, h, r, s string
	}{
		{"sample", "sha256", "efd48b2aacb6a8fd1140dd9cd45e81d69d2c877b56aaf991c34d0ea84eaf3716", "f7cb1c942d657c41d436c7a1b6e29f65f3e900dbb9aff4064dc4ab2f843acda8"},
		{"sample", "sha384", "0eafea039b20e9b42309fb1d89e213057cbf973dc0cfc8f129edddc800ef7719", "4861f0491e6998b9455193e34e7b0d284ddd7149a74b95b9261f13abde940954"},
		{"sample", "sha512", "8496a60b5e9b47c825488827e0495b0e3fa109ec4568fd3f8d1097678eb97f00", "2362ab1adbe2b8adf9cb9edab740ea6049c028114f2460f96554f61fae3302fe"},
		{"test", "sha256", "f1abb023518351cd71d881567b1ea663ed3efcf6c5132b354f28d3b0b7d38367", "019f4113742a2b14bd25926b49c649155f267e60d3814b4c0cc84250e46f0083"},
		{"test", "sha384", "83910e8b48bb0c74244ebdf7f07a1c5413d61472bd941ef3920e623fbccebeb6", "8ddbec54cf8cd5874883841d712142a56a8d0f218f5003cb0296b6b509619f2c"},
		{"test", "sha512", "461d93f31b6540894788fd206c07cfa0cc35f46fa3c91816fff1040ad1581a04", "39af9f15de0db8d97e72719c74820d304ce5226e32dedae67519e840d1194e55"},
	}
	var lines, want []string
	for _, v := range vecs {
		var d []byte
		switch v.h {
		case "sha256":
			s := sha256.Sum256([]byte(v.msg))
			d = s[:]
		case "sha384":
			s := sha512.Sum384([]byte(v.msg))
			d = s[:]
		default:
			s := sha512.Sum512([]byte(v.msg))
			d = s[:]
		}
		lines = append(lines, fmt.Sprintf("S %s %x fixed 64", x, d))
		want = append(want, "S "+v.r+v.s+" true")
	}
	cr2EcCheck(t, "RFC 6979", lines, want)
}
