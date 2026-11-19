// Why: Wycheproof's vectors find the edge cases of ECDSA and ECDH that random
// tests miss.
// What: TestSTDCRYPTO_WycheproofEcdsa and TestSTDCRYPTO_WycheproofEcdhPoint.
// How: every P-256 and P-384 vector, DER and fixed form; valid must pass,
// invalid must fail, and acceptable must give Go's answer.

package std

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

type cr2WpEcdsa struct {
	TestGroups []struct {
		PublicKey struct {
			Curve        string `json:"curve"`
			Uncompressed string `json:"uncompressed"`
		} `json:"publicKey"`
		PublicKeyDer string `json:"publicKeyDer"`
		Sha          string `json:"sha"`
		Tests        []struct {
			TcID   int    `json:"tcId"`
			Msg    string `json:"msg"`
			Sig    string `json:"sig"`
			Result string `json:"result"`
		} `json:"tests"`
	} `json:"testGroups"`
}

// cr2WpDigest is the Wycheproof message hashed by sha.
func cr2WpDigest(t *testing.T, sha string, msg []byte) []byte {
	t.Helper()
	switch sha {
	case "SHA-256":
		s := sha256.Sum256(msg)
		return s[:]
	case "SHA-384":
		s := sha512.Sum384(msg)
		return s[:]
	case "SHA-512":
		s := sha512.Sum512(msg)
		return s[:]
	}
	t.Fatalf("hash %s", sha)
	return nil
}

// cr2GoEcdsa is Go's verdict on a signature: DER through VerifyASN1, P1363
// as r || s of the field length through Verify.
func cr2GoEcdsa(c elliptic.Curve, pub, digest, sig []byte, fixed bool) bool {
	k, err := ecdsa.ParseUncompressedPublicKey(c, pub)
	if err != nil {
		return false
	}
	if !fixed {
		return ecdsa.VerifyASN1(k, digest, sig)
	}
	n := (c.Params().BitSize + 7) / 8
	if len(sig) != 2*n {
		return false
	}
	return ecdsa.Verify(k, digest, new(big.Int).SetBytes(sig[:n]), new(big.Int).SetBytes(sig[n:]))
}

// TestSTDCRYPTO_WycheproofEcdsa: every vector of the P-256 and P-384
// ECDSA files the pinned copy holds, DER and P1363.
func TestSTDCRYPTO_WycheproofEcdsa(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"ecdsa_secp256r1_sha256_test.json", "ecdsa_secp256r1_sha512_test.json",
		"ecdsa_secp256r1_sha256_p1363_test.json", "ecdsa_secp256r1_sha512_p1363_test.json",
		"ecdsa_secp384r1_sha256_test.json", "ecdsa_secp384r1_sha384_test.json", "ecdsa_secp384r1_sha512_test.json",
		"ecdsa_secp384r1_sha384_p1363_test.json", "ecdsa_secp384r1_sha512_p1363_test.json",
	} {
		var wp cr2WpEcdsa
		cr2Wycheproof(t, name, &wp)
		fixed := strings.Contains(name, "p1363")
		f := "der"
		if fixed {
			f = "fixed"
		}
		var lines, want []string
		var ids []int
		counts := map[string]int{}
		for _, g := range wp.TestGroups {
			c, cv := 0, elliptic.P256()
			if g.PublicKey.Curve == "secp384r1" {
				c, cv = 1, elliptic.P384()
			}
			pub, _ := hex.DecodeString(g.PublicKey.Uncompressed)
			for _, tc := range g.Tests {
				msg, _ := hex.DecodeString(tc.Msg)
				sig, _ := hex.DecodeString(tc.Sig)
				digest := cr2WpDigest(t, g.Sha, msg)
				ok := tc.Result == "valid"
				if tc.Result == "acceptable" {
					ok = cr2GoEcdsa(cv, pub, digest, sig, fixed)
				}
				counts[tc.Result]++
				lines = append(lines, fmt.Sprintf("V %d %x %x %s %x", c, pub, digest, f, sig))
				want = append(want, fmt.Sprintf("V %v", ok))
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
		t.Logf("%s: %d of %d vectors run (valid %d, invalid %d, acceptable %d)", name, len(lines), len(lines), counts["valid"], counts["invalid"], counts["acceptable"])
	}
}

type cr2WpEcdh struct {
	TestGroups []struct {
		Curve string `json:"curve"`
		Tests []struct {
			TcID    int      `json:"tcId"`
			Public  string   `json:"public"`
			Private string   `json:"private"`
			Shared  string   `json:"shared"`
			Result  string   `json:"result"`
			Flags   []string `json:"flags"`
		} `json:"tests"`
	} `json:"testGroups"`
}

// cr2WpScalar is a Wycheproof private key (big-endian, possibly with a
// leading zero byte or shorter) as 32 bytes, or nil when it does not fit.
func cr2WpScalar(s string) []byte {
	b, _ := hex.DecodeString(s)
	v := new(big.Int).SetBytes(b)
	if v.BitLen() > 256 {
		return nil
	}
	return v.FillBytes(make([]byte, 32))
}

// TestSTDCRYPTO_WycheproofEcdhPoint: every vector of
// ecdh_secp256r1_ecpoint_test.json (the peer as a raw SEC 1 point).
// Compressed peers are "acceptable" there and refused by EcdhShared, as
// Go's crypto/ecdh refuses them.
func TestSTDCRYPTO_WycheproofEcdhPoint(t *testing.T) {
	t.Parallel()
	const name = "ecdh_secp256r1_ecpoint_test.json"
	var wp cr2WpEcdh
	cr2Wycheproof(t, name, &wp)
	var lines, want []string
	var ids []int
	counts := map[string]int{}
	for _, g := range wp.TestGroups {
		for _, tc := range g.Tests {
			d := cr2WpScalar(tc.Private)
			if d == nil {
				t.Fatalf("tcId %d: private key wider than 256 bits", tc.TcID)
			}
			pub, _ := hex.DecodeString(tc.Public)
			var w string
			switch tc.Result {
			case "valid":
				w = "H " + tc.Shared
			case "invalid":
				w = "H bad-key true"
			default:
				w = "H bad-key true"
				k, err1 := ecdh.P256().NewPrivateKey(d)
				p, err2 := ecdh.P256().NewPublicKey(pub)
				if err1 == nil && err2 == nil {
					if s, err := k.ECDH(p); err == nil {
						w = "H " + hex.EncodeToString(s)
					}
				}
			}
			counts[tc.Result]++
			lines = append(lines, fmt.Sprintf("H %x %x 32", d, pub))
			want = append(want, w)
			ids = append(ids, tc.TcID)
		}
	}
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		for i := range lines {
			if got[i] != want[i] {
				t.Errorf("%s %s tcId %d: %s, want %s", name, v.name, ids[i], got[i], want[i])
			}
		}
	}
	t.Logf("%s: %d of %d vectors run (valid %d, invalid %d, acceptable %d)", name, len(lines), len(lines), counts["valid"], counts["invalid"], counts["acceptable"])
}
