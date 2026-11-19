// Why: RSA verification reads untrusted signatures and keys in TLS and X.509.
// What: TestSTDCRYPTO_RsaMatchesGo, TestSTDCRYPTO_RsaPublicOpMatchesBig
// and TestSTDCRYPTO_WycheproofRsa.
// How: Go signs with PKCS#1 v1.5 and PSS at 2048 to 4096 bits; each signature
// is verified, then refused for every tamper; Wycheproof's vectors run too.

package std

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"math/big"
	mrand "math/rand/v2"
	"strings"
	"testing"
)

// cr2RsaHash is the driver's hash bits and Go's hash for i.
func cr2RsaHash(i int) (int, crypto.Hash) {
	switch i % 3 {
	case 0:
		return 256, crypto.SHA256
	case 1:
		return 384, crypto.SHA384
	}
	return 512, crypto.SHA512
}

// TestSTDCRYPTO_RsaMatchesGo: Go's signatures verify, and fail for a
// flipped bit of the signature or the digest, another hash, a digest of
// the wrong length, a signature one byte short or long, a signature equal
// to or above n, and (PSS) a salt of another length.
func TestSTDCRYPTO_RsaMatchesGo(t *testing.T) {
	t.Parallel()
	rnd := cryptoSplitmix{s: 71}
	var lines, want []string
	for ki, bits := range []int{2048, 2049, 3072, 4096} {
		priv, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Fatal(err)
		}
		n := priv.N.Bytes()
		e := priv.E
		k := len(n)
		for i := range 6 {
			hb, h := cr2RsaHash(i + ki)
			digest := make([]byte, h.Size())
			rnd.fill(digest)
			p1, err := rsa.SignPKCS1v15(nil, priv, h, digest)
			if err != nil {
				t.Fatal(err)
			}
			ps, err := rsa.SignPSS(rand.Reader, priv, h, digest, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			if err != nil {
				t.Fatal(err)
			}
			short, err := rsa.SignPSS(rand.Reader, priv, h, digest, &rsa.PSSOptions{SaltLength: 20})
			if err != nil {
				t.Fatal(err)
			}
			add := func(kind string, hb int, digest, sig []byte, ok bool) {
				lines = append(lines, fmt.Sprintf("W %s %d %x %d %x %x", kind, hb, n, e, digest, sig))
				want = append(want, fmt.Sprintf("W %v", ok))
			}
			flip := func(b []byte, at int) []byte {
				c := append([]byte(nil), b...)
				c[at%len(c)] ^= 1 << (at % 8)
				return c
			}
			other, _ := cr2RsaHash(i + ki + 1)
			for _, kind := range []string{"p", "s"} {
				sig := p1
				if kind == "s" {
					sig = ps
				}
				add(kind, hb, digest, sig, true)
				add(kind, hb, digest, flip(sig, 7*i+3), false)
				add(kind, hb, flip(digest, i), sig, false)
				add(kind, other, digest, sig, false)
				add(kind, hb, digest[:len(digest)-1], sig, false)
				add(kind, hb, digest, sig[1:], false)
				add(kind, hb, digest, append([]byte{0}, sig...), false)
				add(kind, hb, digest, n, false)
				add(kind, hb, digest, new(big.Int).Add(priv.N, big.NewInt(1)).FillBytes(make([]byte, k)), false)
			}
			add("s", hb, digest, short, h.Size() == 20)
			add("p", hb, digest, ps, false)
			add("s", hb, digest, p1, false)
		}
		// A modulus with a leading zero byte is the same key.
		lines = append(lines, fmt.Sprintf("K 00%x %d", n, e))
		want = append(want, "K ok")
	}
	// New's bounds: the modulus's bit length, parity and the exponent.
	mod := func(bits int, odd bool) string {
		b := make([]byte, (bits+7)/8)
		rnd.fill(b)
		v := new(big.Int).SetBytes(b)
		v.SetBit(v, bits-1, 1)
		for i := bits; i < 8*len(b); i++ {
			v.SetBit(v, i, 0)
		}
		v.SetBit(v, 0, 0)
		if odd {
			v.SetBit(v, 0, 1)
		}
		return hex.EncodeToString(v.Bytes())
	}
	for _, c := range []struct {
		bits int
		odd  bool
		e    uint64
		ok   bool
	}{
		{2048, true, 65537, true}, {2047, true, 65537, false}, {2049, true, 65537, true},
		{8192, true, 65537, true}, {8193, true, 65537, false}, {4096, false, 65537, false},
		{2048, true, 3, true}, {2048, true, 1, false}, {2048, true, 2, false}, {2048, true, 65536, false},
		{2048, true, 0xFFFFFFFF, true}, {2048, true, 0x100000001, false}, {2048, true, 0, false},
	} {
		lines = append(lines, fmt.Sprintf("K %s %d", mod(c.bits, c.odd), c.e))
		if c.ok {
			want = append(want, "K ok")
		} else {
			want = append(want, "K bad-key")
		}
	}
	lines = append(lines, "K  65537", "K 0000 65537")
	want = append(want, "K bad-key", "K bad-key")
	cr2EcCheck(t, "rsa", lines, want)
}

// TestSTDCRYPTO_RsaPublicOpMatchesBig: RsaPublicOp's s^e mod n against
// math/big's Exp, for odd moduli at and around the limb boundaries (2048,
// 2049, 2111, 2112, 2113, 4095, 4097 and 8192 bits; random, with the top
// bit set, and n = 2^b - 1), every exponent class the key accepts (3, 17,
// 65537, 2^31 + 1 and 2^32 - 1, and a random odd one), and s of 0, 1, 2,
// n - 1, n - 2 and random below n; then s = n, s above n, and s one byte
// short or long, each false.
func TestSTDCRYPTO_RsaPublicOpMatchesBig(t *testing.T) {
	t.Parallel()
	rng := mrand.New(mrand.NewPCG(7, 2026))
	pad := func(x *big.Int, k int) string {
		b := make([]byte, k)
		x.FillBytes(b)
		return hex.EncodeToString(b)
	}
	var lines, want []string
	for _, bits := range []int{2048, 2049, 2111, 2112, 2113, 4095, 4097, 8192} {
		var mods []*big.Int
		for i := 0; i < 2; i++ {
			n := cr2Below(rng, new(big.Int).Lsh(big.NewInt(1), uint(bits)))
			n.SetBit(n, bits-1, 1)
			n.SetBit(n, 0, 1)
			mods = append(mods, n)
		}
		allOnes := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(bits)), big.NewInt(1))
		mods = append(mods, allOnes)
		for _, n := range mods {
			k := (bits + 7) / 8
			es := []uint64{3, 17, 65537, 1<<31 + 1, 1<<32 - 1, uint64(rng.Uint32()) | 1<<31 | 1}
			nm1 := new(big.Int).Sub(n, big.NewInt(1))
			nm2 := new(big.Int).Sub(n, big.NewInt(2))
			ss := []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), nm1, nm2,
				cr2Below(rng, n), cr2Below(rng, n)}
			for _, e := range es {
				for _, s := range ss {
					r := new(big.Int).Exp(s, new(big.Int).SetUint64(e), n)
					lines = append(lines, fmt.Sprintf("E %s %d %s", pad(n, k), e, pad(s, k)))
					want = append(want, "E "+pad(r, k))
				}
			}
			above := new(big.Int).Add(n, big.NewInt(1))
			short := pad(nm1, k)[2:]
			if bits%8 != 0 {
				above = new(big.Int).Lsh(big.NewInt(1), uint(8*k)-1) // over n, still k bytes
			}
			for _, s := range []string{pad(n, k), short, "00" + pad(nm1, k)} {
				lines = append(lines, fmt.Sprintf("E %s 65537 %s", pad(n, k), s))
				want = append(want, "E false")
			}
			if above.BitLen() <= 8*k {
				lines = append(lines, fmt.Sprintf("E %s 65537 %s", pad(n, k), pad(above, k)))
				want = append(want, "E false")
			}
		}
	}
	cr2EcCheck(t, "rsa public op", lines, want)
}

type cr2WpRsa struct {
	TestGroups []struct {
		PublicKey struct {
			Modulus        string `json:"modulus"`
			PublicExponent string `json:"publicExponent"`
		} `json:"publicKey"`
		Sha    string `json:"sha"`
		MgfSha string `json:"mgfSha"`
		SLen   int    `json:"sLen"`
		Tests  []struct {
			TcID   int    `json:"tcId"`
			Msg    string `json:"msg"`
			Sig    string `json:"sig"`
			Result string `json:"result"`
		} `json:"tests"`
	} `json:"testGroups"`
}

// TestSTDCRYPTO_WycheproofRsa: every vector of the RSA files whose
// message hash is SHA-256, SHA-384 or SHA-512; the others (SHA-1,
// SHA-224, SHA-512/224, SHA-512/256) are skipped by name. A PSS group
// whose MGF1 hash differs from the message hash or whose salt is not the
// digest's length states verdicts for parameters the API does not take
// (it fixes both), so there, as for "acceptable" vectors, the answer must
// be Go's with the API's parameters (crypto/rsa, PSSSaltLengthEqualsHash):
// false for every one of those groups' signatures but those made, or
// modified, with the salt the API takes (tcId 69 of sha256_mgf1_0).
func TestSTDCRYPTO_WycheproofRsa(t *testing.T) {
	t.Parallel()
	files := []string{
		"rsa_signature_2048_sha256_test.json", "rsa_signature_2048_sha512_test.json",
		"rsa_signature_3072_sha256_test.json", "rsa_signature_3072_sha512_test.json",
		"rsa_signature_4096_sha256_test.json", "rsa_signature_4096_sha512_test.json",
		"rsa_signature_8192_sha256_test.json", "rsa_signature_8192_sha512_test.json",
		"rsa_pss_2048_sha256_mgf1_0_test.json", "rsa_pss_2048_sha256_mgf1_32_test.json",
		"rsa_pss_3072_sha256_mgf1_32_test.json", "rsa_pss_4096_sha256_mgf1_32_test.json",
		"rsa_pss_4096_sha512_mgf1_32_test.json", "rsa_pss_4096_sha512_mgf1_64_test.json",
		"rsa_pss_2048_sha512_mgf1sha256_32_params_test.json", "rsa_pss_misc_test.json",
	}
	for _, name := range files {
		var wp cr2WpRsa
		cr2Wycheproof(t, name, &wp)
		pss := strings.HasPrefix(name, "rsa_pss")
		kind := "p"
		if pss {
			kind = "s"
		}
		var lines, want []string
		var ids []int
		skipped := 0
		for _, g := range wp.TestGroups {
			var hb int
			var h crypto.Hash
			switch g.Sha {
			case "SHA-256":
				hb, h = 256, crypto.SHA256
			case "SHA-384":
				hb, h = 384, crypto.SHA384
			case "SHA-512":
				hb, h = 512, crypto.SHA512
			default:
				skipped += len(g.Tests)
				continue
			}
			nb, _ := hex.DecodeString(g.PublicKey.Modulus)
			eb, _ := hex.DecodeString(g.PublicKey.PublicExponent)
			e := new(big.Int).SetBytes(eb)
			pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(e.Int64())}
			params := !pss || (g.MgfSha == g.Sha && g.SLen == h.Size())
			for _, tc := range g.Tests {
				msg, _ := hex.DecodeString(tc.Msg)
				sig, _ := hex.DecodeString(tc.Sig)
				hh := h.New()
				hh.Write(msg)
				digest := hh.Sum(nil)
				ok := tc.Result == "valid"
				if tc.Result == "acceptable" || !params {
					if pss {
						ok = rsa.VerifyPSS(pub, h, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) == nil
					} else {
						ok = rsa.VerifyPKCS1v15(pub, h, digest, sig) == nil
					}
				}
				lines = append(lines, fmt.Sprintf("W %s %d %x %s %x %x", kind, hb, nb, e, digest, sig))
				want = append(want, fmt.Sprintf("W %v", ok))
				ids = append(ids, tc.TcID)
			}
		}
		for _, v := range cr2EcVariants {
			got := cr2EcAsk(t, v, lines)
			bad := 0
			for i := range lines {
				if got[i] != want[i] {
					t.Errorf("%s %s tcId %d: %s, want %s", name, v.name, ids[i], got[i], want[i])
					if bad++; bad == 10 {
						t.Fatalf("%s %s: stopping after 10", name, v.name)
					}
				}
			}
		}
		t.Logf("%s: %d run, %d skipped (a hash the API does not take)", name, len(lines), skipped)
	}
}

// cr2Below is a pseudo-random number below m from rng (its slight bias
// does not matter to a test).
func cr2Below(rng *mrand.Rand, m *big.Int) *big.Int {
	b := make([]byte, (m.BitLen()+7)/8+8)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return new(big.Int).Mod(new(big.Int).SetBytes(b), m)
}
