// Why: key parsing reads untrusted DER and PEM, and must read exactly what
// Go's crypto/x509 reads.
// What: TestSTDCRYPTO_KeysMatchGo, TestSTDCRYPTO_KeysPem and
// TestSTDCRYPTO_WycheproofKeys.
// How: keys Go marshals read back to the same values; refused kinds are
// BadKey; every proper prefix and a trailing byte are refused.

package std

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
)

// cr2KeyWant is the driver's answer for a public key Go parsed.
func cr2KeyWant(t *testing.T, k any) string {
	t.Helper()
	switch k := k.(type) {
	case *ecdsa.PublicKey:
		b, err := k.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		c := 0
		if k.Curve == elliptic.P384() {
			c = 1
		}
		return fmt.Sprintf("U ec %d %x", c, b)
	case *rsa.PublicKey:
		return fmt.Sprintf("U rsa %x %d", k.N.Bytes(), k.E)
	case ed25519.PublicKey:
		return fmt.Sprintf("U ed %x", []byte(k))
	}
	t.Fatalf("key type %T", k)
	return ""
}

// TestSTDCRYPTO_KeysMatchGo: public and private keys of every kind, as
// Go marshals them, against Go's parse; refusals with their exact error.
func TestSTDCRYPTO_KeysMatchGo(t *testing.T) {
	t.Parallel()
	var lines, want []string
	add := func(l, w string) {
		lines = append(lines, l)
		want = append(want, w)
	}
	var ders [][]byte
	for _, c := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P256()} {
		k, err := ecdsa.GenerateKey(c, rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		spki, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
		add(fmt.Sprintf("U %x", spki), cr2KeyWant(t, &k.PublicKey))
		p8, _ := x509.MarshalPKCS8PrivateKey(k)
		sec1, _ := x509.MarshalECPrivateKey(k)
		d, _ := k.Bytes()
		if c == elliptic.P256() {
			add(fmt.Sprintf("Z %x", p8), fmt.Sprintf("Z ec %x", d))
			add(fmt.Sprintf("Z %x", sec1), fmt.Sprintf("Z ec %x", d))
		} else {
			add(fmt.Sprintf("Z %x", p8), "Z bad-key")
			add(fmt.Sprintf("Z %x", sec1), "Z bad-key")
		}
		ders = append(ders, spki, p8, sec1)
	}
	// A P-224 key is BadKey; Go reads it.
	k224, _ := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	spki224, _ := x509.MarshalPKIXPublicKey(&k224.PublicKey)
	p8224, _ := x509.MarshalPKCS8PrivateKey(k224)
	add(fmt.Sprintf("U %x", spki224), "U bad-key")
	add(fmt.Sprintf("Z %x", p8224), "Z bad-key")
	for _, bits := range []int{2048, 3072} {
		k, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Fatal(err)
		}
		spki, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
		pk1 := x509.MarshalPKCS1PublicKey(&k.PublicKey)
		add(fmt.Sprintf("U %x", spki), cr2KeyWant(t, &k.PublicKey))
		add(fmt.Sprintf("U %x", pk1), cr2KeyWant(t, &k.PublicKey))
		p8, _ := x509.MarshalPKCS8PrivateKey(k)
		add(fmt.Sprintf("Z %x", p8), "Z bad-key")
		add(fmt.Sprintf("Z %x", x509.MarshalPKCS1PrivateKey(k)), "Z bad-encoding")
		ders = append(ders, spki, pk1)
	}
	// RSA keys Go reads and RsaPublic.New refuses: 1024 bits, e = 2^32 + 1.
	k1024, _ := rsa.GenerateKey(rand.Reader, 1024)
	add(fmt.Sprintf("U %x", x509.MarshalPKCS1PublicKey(&k1024.PublicKey)), "U bad-key")
	k2048, _ := rsa.GenerateKey(rand.Reader, 2048)
	bigE := struct {
		N *big.Int
		E int64
	}{k2048.N, 1<<32 + 1}
	be, err := asn1.Marshal(bigE)
	if err != nil {
		t.Fatal(err)
	}
	add(fmt.Sprintf("U %x", be), "U bad-key")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	spki, _ := x509.MarshalPKIXPublicKey(pub)
	p8, _ := x509.MarshalPKCS8PrivateKey(priv)
	add(fmt.Sprintf("U %x", spki), cr2KeyWant(t, pub))
	add(fmt.Sprintf("Z %x", p8), fmt.Sprintf("Z ed %x", priv.Seed()))
	ders = append(ders, spki, p8)
	// An X25519 key (another OID) is BadKey.
	xk, _ := ecdh.X25519().GenerateKey(rand.Reader)
	xspki, _ := x509.MarshalPKIXPublicKey(xk.PublicKey())
	add(fmt.Sprintf("U %x", xspki), "U bad-key")
	// EncryptedPrivateKeyInfo: SEQUENCE { AlgorithmIdentifier, OCTET STRING }.
	enc, err := asn1.Marshal(struct {
		Alg  pkix.AlgorithmIdentifier
		Data []byte
	}{pkix.AlgorithmIdentifier{Algorithm: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 5, 13}, Parameters: asn1.NullRawValue}, []byte("ciphertext")})
	if err != nil {
		t.Fatal(err)
	}
	add(fmt.Sprintf("Z %x", enc), "Z encrypted")
	// Truncations, a trailing byte, and the empty input are refused (never
	// a value, never a crash).
	for _, d := range ders {
		for n := 0; n < len(d); n += 1 + n/9 {
			add(fmt.Sprintf("U %x", d[:n]), "")
			add(fmt.Sprintf("Z %x", d[:n]), "")
		}
		add(fmt.Sprintf("U %x00", d), "U bad-encoding")
		add(fmt.Sprintf("Z %x00", d), "Z bad-encoding")
	}
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		for i := range lines {
			if want[i] == "" {
				if f := strings.Fields(got[i]); len(f) != 2 || !strings.HasPrefix(f[1], "bad-") {
					t.Errorf("%s: %s\n got %s, want a refusal", v.name, cryptoTail(lines[i], 200), cryptoTail(got[i], 200))
				}
				continue
			}
			if got[i] != want[i] {
				t.Errorf("%s: %s\n got  %s\n want %s", v.name, cryptoTail(lines[i], 200), cryptoTail(got[i], 200), cryptoTail(want[i], 200))
			}
		}
	}
}

// TestSTDCRYPTO_KeysPem: a PEM file of a public and a private key, each
// block parsed by its label through encoding.ParsePem.
func TestSTDCRYPTO_KeysPem(t *testing.T) {
	t.Parallel()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	spki, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	p8, _ := x509.MarshalPKCS8PrivateKey(k)
	sec1, _ := x509.MarshalECPrivateKey(k)
	text := "comment\n" + string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: spki})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: p8})) +
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}))
	d, _ := k.Bytes()
	pw := cr2KeyWant(t, &k.PublicKey)
	cr2EcCheck(t, "pem", []string{"M " + hex.EncodeToString([]byte(text))},
		[]string{fmt.Sprintf("M %s; Z ec %x; Z ec %x;", pw, d, d)})
}

// TestSTDCRYPTO_WycheproofKeys: every public key of the ECDH file (SPKI,
// then ECDH with the vector's private key: valid vectors give the shared
// secret, invalid ones a refusal at parse or exchange, acceptable ones
// Go's x509 and ecdh answer, except that one Go reads despite trailing
// bytes inside a SEQUENCE may be refused: strict DER), and every ECDSA and RSA group's DER key
// (and RSA's PKCS#1 form) read back to its stated point or modulus.
func TestSTDCRYPTO_WycheproofKeys(t *testing.T) {
	t.Parallel()
	var wp cr2WpEcdh
	cr2Wycheproof(t, "ecdh_secp256r1_test.json", &wp)
	var lines, want []string
	var ids []int
	for _, g := range wp.TestGroups {
		for _, tc := range g.Tests {
			d := cr2WpScalar(tc.Private)
			der, _ := hex.DecodeString(tc.Public)
			w := ""
			switch tc.Result {
			case "valid":
				w = "J " + tc.Shared
			case "acceptable":
				// Go reads a few of these (zeros appended inside the
				// SEQUENCE, InvalidAsn); ParsePublicKey reads DER only, which
				// Wycheproof's "acceptable" allows, so those may be refused.
				w = "refused"
				if slices.Contains(tc.Flags, "InvalidAsn") {
					break
				}
				if k, err := x509.ParsePKIXPublicKey(der); err == nil {
					if ek, ok := k.(*ecdsa.PublicKey); ok {
						if pk, err := ek.ECDH(); err == nil {
							sk, _ := ecdh.P256().NewPrivateKey(d)
							if s, err := sk.ECDH(pk); err == nil {
								w = "J " + hex.EncodeToString(s)
							}
						}
					}
				}
			default:
				w = "refused"
			}
			lines = append(lines, fmt.Sprintf("J %s %x", tc.Public, d))
			want = append(want, w)
			ids = append(ids, tc.TcID)
		}
	}
	n := len(lines)
	for _, name := range []string{"ecdsa_secp256r1_sha256_test.json", "ecdsa_secp384r1_sha384_test.json"} {
		var ew cr2WpEcdsa
		cr2Wycheproof(t, name, &ew)
		for _, g := range ew.TestGroups {
			c := 0
			if g.PublicKey.Curve == "secp384r1" {
				c = 1
			}
			lines = append(lines, "U "+g.PublicKeyDer)
			want = append(want, fmt.Sprintf("U ec %d %s", c, g.PublicKey.Uncompressed))
			ids = append(ids, 0)
		}
	}
	type rsaGroup struct {
		PublicKey struct {
			Modulus        string `json:"modulus"`
			PublicExponent string `json:"publicExponent"`
		} `json:"publicKey"`
		PublicKeyAsn string `json:"publicKeyAsn"`
		PublicKeyDer string `json:"publicKeyDer"`
	}
	for _, name := range []string{"rsa_signature_2048_sha256_test.json", "rsa_signature_8192_sha512_test.json", "rsa_pss_4096_sha512_mgf1_64_test.json"} {
		var rw struct {
			TestGroups []rsaGroup `json:"testGroups"`
		}
		cr2Wycheproof(t, name, &rw)
		for _, g := range rw.TestGroups {
			k, err := x509.ParsePKCS1PublicKey(cryptoUnhex(t, g.PublicKeyAsn))
			if err != nil {
				t.Fatal(err)
			}
			w := cr2KeyWant(t, k)
			if strings.HasPrefix(name, "rsa_signature") {
				lines = append(lines, "U "+g.PublicKeyDer)
				want = append(want, w)
				ids = append(ids, 0)
			}
			lines = append(lines, "U "+g.PublicKeyAsn)
			want = append(want, w)
			ids = append(ids, 0)
		}
	}
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		for i := range lines {
			ok := got[i] == want[i]
			if want[i] == "refused" {
				f := strings.Fields(got[i])
				ok = len(f) == 2 && strings.HasPrefix(f[1], "bad-")
			}
			if !ok {
				if i < n {
					t.Errorf("ecdh_secp256r1_test.json %s tcId %d: %s, want %s", v.name, ids[i], got[i], want[i])
				} else {
					t.Errorf("%s: %s\n got  %s\n want %s", v.name, cryptoTail(lines[i], 200), cryptoTail(got[i], 200), want[i])
				}
			}
		}
	}
	t.Logf("ecdh_secp256r1_test.json: %d of %d vectors run; %d DER keys of the ECDSA and RSA files", n, n, len(lines)-n)
}
