// Why: the key fuzz target must also reach P-384, fuzzed ECDSA public keys and
// RSA under SHA-384 and SHA-512.
// What: ops 7 to 9 of FuzzSTDCRYPTO_Keys and their Go answers.
// How: answers held to crypto/ecdsa and crypto/rsa; the RSA key comes from
// primes searched from a fixed seed, so a crasher replays.

package std

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"
	"math/big"
	"sync"
)

// cr2FzMoreKeys are the fixed P-384 key and the fixed RSA private key.
var cr2FzMoreKeys = sync.OnceValue(func() struct {
	p384 *ecdsa.PrivateKey
	rsa  *rsa.PrivateKey
} {
	d := cr2Rand(384, 48)
	d[0] &= 0x7f
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P384(), d)
	if err != nil {
		panic(err)
	}
	prime := func(seed uint64) *big.Int {
		b := cr2Rand(seed, 128)
		b[0] |= 0xc0
		b[127] |= 1
		p := new(big.Int).SetBytes(b)
		for !p.ProbablyPrime(20) {
			p.Add(p, big.NewInt(2))
		}
		return p
	}
	p, q := prime(1001), prime(1002)
	n := new(big.Int).Mul(p, q)
	e := big.NewInt(65537)
	phi := new(big.Int).Mul(new(big.Int).Sub(p, big.NewInt(1)), new(big.Int).Sub(q, big.NewInt(1)))
	dd := new(big.Int).ModInverse(e, phi)
	if dd == nil {
		panic("cr2FzMoreKeys: e not invertible")
	}
	rk := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: n, E: 65537}, D: dd, Primes: []*big.Int{p, q}}
	rk.Precompute()
	if err := rk.Validate(); err != nil {
		panic(err)
	}
	return struct {
		p384 *ecdsa.PrivateKey
		rsa  *rsa.PrivateKey
	}{k, rk}
})

// cr2FzHash is the driver's bits and Go's hash for selector i.
func cr2FzHash(i byte) (int, crypto.Hash) {
	switch i % 3 {
	case 0:
		return 256, crypto.SHA256
	case 1:
		return 384, crypto.SHA384
	}
	return 512, crypto.SHA512
}

// cr2FzCaseMore decodes ops 7 to 9 of cr2FzCase.
//
//	7: b[0] bit 7 the format, b[0] % 80 the digest's length; the rest a
//	   signature checked under the fixed P-384 key.
//	8: b[0] bit 0 the curve, bit 1 the format, (b[0] >> 2) % 70 the
//	   digest's length; then the public key (65 or 97 bytes, as many as
//	   there are), then the signature.
//	9: b[0] bit 0 the scheme (PSS when set), bits 1 and 2 the hash, bit 3
//	   set: (b[0] >> 4) * 5 the digest's length instead of the hash's; the
//	   rest a signature checked under the fixed RSA key.
func cr2FzCaseMore(op byte, b []byte) (string, func(got string) string) {
	sel := byte(0)
	if len(b) > 0 {
		sel, b = b[0], b[1:]
	}
	ks := cr2FzMoreKeys()
	want := func(w string) func(string) string {
		return func(got string) string {
			if got != w {
				return w
			}
			return ""
		}
	}
	switch op {
	case 7:
		fixed := sel&0x80 != 0
		f := map[bool]string{false: "der", true: "fixed"}[fixed]
		digest := cr2Rand(2, int(sel)%80)
		pub, _ := ks.p384.PublicKey.Bytes()
		return fmt.Sprintf("V 1 %x %x %s %x", pub, digest, f, b),
			want(fmt.Sprintf("V %v", cr2GoEcdsa(elliptic.P384(), pub, digest, b, fixed)))
	case 8:
		c, curve, plen := 0, elliptic.P256(), 65
		if sel&1 != 0 {
			c, curve, plen = 1, elliptic.P384(), 97
		}
		fixed := sel&2 != 0
		f := map[bool]string{false: "der", true: "fixed"}[fixed]
		digest := cr2Rand(3, int(sel>>2)%70)
		plen = min(plen, len(b))
		pub, sig := b[:plen], b[plen:]
		return fmt.Sprintf("V %d %x %x %s %x", c, pub, digest, f, sig),
			want(fmt.Sprintf("V %v", cr2GoEcdsa(curve, pub, digest, sig, fixed)))
	}
	pss := sel&1 != 0
	bits, h := cr2FzHash(sel >> 1 & 3)
	dl := h.Size()
	if sel&8 != 0 {
		dl = int(sel>>4) * 5
	}
	digest := cr2Rand(4, dl)
	pk := &ks.rsa.PublicKey
	kind := "p"
	var err error
	if pss {
		kind = "s"
		err = rsa.VerifyPSS(pk, h, digest, b, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	} else {
		err = rsa.VerifyPKCS1v15(pk, h, digest, b)
	}
	return fmt.Sprintf("W %s %d %x 65537 %x %x", kind, bits, pk.N.Bytes(), digest, b),
		want(fmt.Sprintf("W %v", err == nil))
}

// cr2FzMoreSeeds are valid inputs of ops 7 to 9: Go's signatures under
// the fixed keys, each in the form its op decodes.
func cr2FzMoreSeeds() [][]byte {
	ks := cr2FzMoreKeys()
	var seeds [][]byte
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	// Op 7: digest lengths 48 and 64, both formats.
	for _, dl := range []int{48, 64} {
		digest := cr2Rand(2, dl)
		sig, _ := ecdsa.SignASN1(nil, ks.p384, digest)
		seeds = append(seeds, cat([]byte{7, byte(dl)}, sig), cat([]byte{7, byte(dl) | 0x80}, cr2EcFixedOf(sig, 48)))
	}
	// Op 8: P-256 under the fixed key, P-384 under its fixed key, a
	// 32-byte and a 48-byte digest (sel >> 2 is the length).
	p256pub, _ := cr2FzKey.ec.PublicKey.Bytes()
	p384pub, _ := ks.p384.PublicKey.Bytes()
	d32, d48 := cr2Rand(3, 32), cr2Rand(3, 48)
	s1, _ := ecdsa.SignASN1(nil, cr2FzKey.ec, d32)
	s2, _ := ecdsa.SignASN1(nil, ks.p384, d48)
	seeds = append(seeds,
		cat([]byte{8, 32 << 2}, p256pub, s1),
		cat([]byte{8, 32<<2 | 2}, p256pub, cr2EcFixedOf(s1, 32)),
		cat([]byte{8, 48<<2 | 1}, p384pub, s2),
		cat([]byte{8, 48<<2 | 3}, p384pub, cr2EcFixedOf(s2, 48)))
	// Op 9: each hash, both schemes.
	for i := byte(0); i < 3; i++ {
		_, h := cr2FzHash(i)
		digest := cr2Rand(4, h.Size())
		p1, err := rsa.SignPKCS1v15(nil, ks.rsa, h, digest)
		if err != nil {
			panic(err)
		}
		ps, err := rsa.SignPSS(cr2FzRandReader{}, ks.rsa, h, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		if err != nil {
			panic(err)
		}
		seeds = append(seeds, cat([]byte{9, i << 1}, p1), cat([]byte{9, i<<1 | 1}, ps))
	}
	return seeds
}

// cr2FzRandReader is a deterministic byte source for PSS salts (Go may
// ignore it and draw its own; either way the seed is a valid signature).
type cr2FzRandReader struct{}

func (cr2FzRandReader) Read(p []byte) (int, error) {
	copy(p, cr2Rand(5, len(p)))
	return len(p), nil
}
