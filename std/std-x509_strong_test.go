// Why: on Windows std.x509 refuses SHA-1 and MD5 signatures on the platform's
// path whatever the machine's settings say.
// What: TestX509_StrongSignBits and TestX509_Md2Vectors (an MD2 model, which Go
// lacks).
// How: weak-chain.pem's chains go to the platform with CERT_STRONG_SIGN_PARA
// and cache-only retrieval; CERT_TRUST_HAS_WEAK_SIGNATURE must map to
// Unsupported.

//go:build windows

package std

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

// The chain status bits (wincrypt.h) these tests read.
const (
	x509TrustUntrustedRoot = 0x20
	x509TrustWeak          = 0x00100000
)

// x509BitsRecord is a "b" record: chain to the platform at now, its raw
// verdict and status.
func x509BitsRecord(chain [][]byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "b %d %d", x509Now.Unix(), len(chain))
	for _, c := range chain {
		b.WriteString(" " + x509Hex(c))
	}
	return b.String()
}

// x509Bits parses an "ok <verdict> <status>" answer.
func x509Bits(t *testing.T, s string) (verdict, status uint64) {
	t.Helper()
	f := strings.Fields(s)
	if len(f) != 3 || f[0] != "ok" {
		t.Fatalf("answer %q", s)
	}
	v, err1 := strconv.ParseUint(f[1], 16, 64)
	o, err2 := strconv.ParseUint(f[2], 16, 64)
	if err1 != nil || err2 != nil {
		t.Fatalf("answer %q", s)
	}
	return v, o
}

// x509Md2S is MD2's substitution table, made from the digits of pi as RFC
// 1319 made it (Go has no MD2); TestX509_Md2Vectors checks it against the
// RFC's test suite.
func x509Md2S() [256]byte {
	// pi's first 1,000 digits by Machin's formula, in fixed point.
	unity := new(big.Int).Exp(big.NewInt(10), big.NewInt(1010), nil)
	atan := func(x int64) *big.Int {
		sum := new(big.Int)
		xx := big.NewInt(x * x)
		term := new(big.Int).Div(unity, big.NewInt(x))
		for k := int64(0); term.Sign() != 0; k++ {
			v := new(big.Int).Div(term, big.NewInt(2*k+1))
			if k%2 == 0 {
				sum.Add(sum, v)
			} else {
				sum.Sub(sum, v)
			}
			term.Div(term, xx)
		}
		return sum
	}
	pi := new(big.Int).Sub(new(big.Int).Mul(big.NewInt(16), atan(5)), new(big.Int).Mul(big.NewInt(4), atan(239))).String()
	at := 0
	next := func() int { v := int(pi[at] - '0'); at++; return v }
	// rand(n) is a digit-drawn number below n, drawn again when it would
	// bias the result (the construction RFC 1319's table follows).
	var rnd func(n int) int
	rnd = func(n int) int {
		x, y := next(), 10
		if n > 10 {
			x, y = x*10+next(), 100
		}
		if n > 100 {
			x, y = x*10+next(), 1000
		}
		if x < n*(y/n) {
			return x % n
		}
		return rnd(n)
	}
	var s [256]byte
	for i := range s {
		s[i] = byte(i)
	}
	for i := 2; i <= 256; i++ {
		j := rnd(i)
		s[j], s[i-1] = s[i-1], s[j]
	}
	return s
}

// x509Md2 is RFC 1319's MD2 of msg.
func x509Md2(msg []byte) []byte {
	s := x509Md2S()
	pad := 16 - len(msg)%16
	m := append(append([]byte{}, msg...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	var c [16]byte
	var l byte
	for i := 0; i < len(m)/16; i++ {
		for j := 0; j < 16; j++ {
			c[j] ^= s[m[i*16+j]^l]
			l = c[j]
		}
	}
	m = append(m, c[:]...)
	var x [48]byte
	for i := 0; i < len(m)/16; i++ {
		for j := 0; j < 16; j++ {
			x[16+j] = m[i*16+j]
			x[32+j] = x[16+j] ^ x[j]
		}
		var v byte
		for j := 0; j < 18; j++ {
			for k := 0; k < 48; k++ {
				x[k] ^= s[v]
				v = x[k]
			}
			v = byte(int(v) + j)
		}
	}
	return append([]byte{}, x[:16]...)
}

func TestX509_Md2Vectors(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", "8350e5a3e24c153df2275c9f80692773"},
		{"a", "32ec01ec4a6dac72c0ab96fb34c0b5d1"},
		{"abc", "da853b0d3f88d99b30283a69e6ded6bb"},
		{"message digest", "ab4f496bfb2a530b219ff33031fe06b0"},
		{"abcdefghijklmnopqrstuvwxyz", "4e8ddff3650292ab5a4108c3aa47940b"},
	} {
		if got := hex.EncodeToString(x509Md2([]byte(c.in))); got != c.want {
			t.Errorf("MD2(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

// x509ResignMd2 is der, a certificate signed by sha256WithRSAEncryption,
// signed again by key with md2WithRSAEncryption (PKCS#1 v1.5 over MD2's
// DigestInfo, RFC 8017 9.2 note 1).
func x509ResignMd2(t *testing.T, der []byte, key *rsa.PrivateKey) []byte {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	sha256Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0b}
	md2Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x02}
	tbs := bytes.Replace(c.RawTBSCertificate, sha256Rsa, md2Rsa, 1)
	info := append([]byte{0x30, 0x20, 0x30, 0x0c, 0x06, 0x08, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x02, 0x02, 0x05, 0x00, 0x04, 0x10}, x509Md2(tbs)...)
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.Hash(0), info)
	if err != nil {
		t.Fatal(err)
	}
	ai := append([]byte{0x30, 0x0d, 0x06, 0x09}, md2Rsa...)
	ai = append(ai, 0x05, 0x00)
	body := append(append(append([]byte{}, tbs...), ai...), x509DerTLV(0x03, append([]byte{0}, sig...))...)
	return x509DerTLV(0x30, body)
}

// TestX509_StrongSignBits: the platform's own path flags every
// algorithm std.x509 calls weak (SHA-1 with RSA or ECDSA, MD5, MD2) on an
// intermediate or the leaf, beside the untrusted root's bit, and not the
// SHA-256 control, a weak cross-signature the path does not use, a weak
// intermediate whose issuer it cannot find (no path checks it), or a
// root's own signature; ECDSA and RSA-PSS with SHA-2 stay clear.
func TestX509_StrongSignBits(t *testing.T) {
	all := x509Fixtures(t)
	fx := all[x509WeakFile]
	if len(fx) != weakCount {
		t.Fatalf("%s: %d certificates", x509WeakFile, len(fx))
	}
	ec := all["chain.pem"]
	// The two other algorithms std.x509 calls weak (algo.mk's AlgWeak),
	// made at run time: ECDSA with SHA-1 (Go still signs it), and MD2
	// with RSA, the fixture's intermediate signed again by the fixture's
	// root key (the generator makes the same key again).
	eroot := x509Make(t, x509CA("Mk Strong Ecdsa Root"), x509P256(t), nil)
	et := x509CA("Mk Strong Ecdsa Sha1 Intermediate")
	et.SignatureAlgorithm = x509.ECDSAWithSHA1
	einter := x509Make(t, et, x509P256(t), eroot)
	eleaf := x509Make(t, x509Leaf("weak.test", []string{"weak.test"}, nil), x509P256(t), einter)
	md2 := x509ResignMd2(t, fx[weakInter], x509DetRsa(t, "root"))
	edLeaf := x509Make(t, x509Leaf("weak.test", []string{"weak.test"}, nil), x509Ed(t), eroot)
	cases := []struct {
		name  string
		chain [][]byte
		weak  bool
	}{
		{"sha256 control", [][]byte{fx[weakLeaf], fx[weakInter], fx[weakRoot]}, false},
		{"sha1 intermediate", [][]byte{fx[weakLeaf], fx[weakInterSha1], fx[weakRoot]}, true},
		{"md5 intermediate", [][]byte{fx[weakLeaf], fx[weakInterMd5], fx[weakRoot]}, true},
		{"ecdsa-sha1 intermediate", [][]byte{eleaf.der, einter.der, eroot.der}, true},
		{"md2 intermediate", [][]byte{fx[weakLeaf], md2, fx[weakRoot]}, true},
		{"sha1 leaf", [][]byte{all["sha1-leaf.pem"][0], all["rsa-root.pem"][0]}, true},
		{"md5 leaf", [][]byte{all["md5-leaf.pem"][0], all["rsa-root.pem"][0]}, true},
		// A partial chain: no path checks the intermediate's signature
		// (its issuer is unknown), so it is not flagged; the chain is
		// refused all the same.
		{"sha1 intermediate, its root not sent", [][]byte{fx[weakLeaf], fx[weakInterSha1]}, false},
		{"unused sha1 cross-signature", [][]byte{fx[weakLeaf], fx[weakInter], fx[weakRoot], fx[weakCross]}, false},
		{"only the sha1 cross-signature", [][]byte{fx[weakLeaf], fx[weakCross], fx[weakOldRoot]}, true},
		// Controls that strong signatures must keep: ECDSA P-256 and
		// P-384 with SHA-256 and SHA-384, RSA-PSS with SHA-256.
		{"ecdsa chain", [][]byte{ec[0], ec[1], all["root.pem"][0]}, false},
		{"rsa-pss leaf", [][]byte{all["pss-leaf.pem"][0], all["rsa-root.pem"][0]}, false},
		// The end certificate's own key is not held to OS_1's key rules
		// (CERT_CHAIN_STRONG_SIGN_DISABLE_END_CHECK_FLAG): an Ed25519 leaf key under an ECDSA root is not weak.
		{"ed25519 leaf key", [][]byte{edLeaf.der, eroot.der}, false},
	}
	// An RSA-1024 leaf key (Go makes one only with rsa1024min=0): not
	// weak to the platform either. x509.Verify does not hold the leaf's
	// key to a size on either path, as at v1.2.0; Certificate.PublicKey
	// refuses an RSA key under 2048 bits, and tls.Client calls it.
	t.Setenv("GODEBUG", "rsa1024min=0")
	if k, err := rsa.GenerateKey(rand.Reader, 1024); err == nil {
		rroot := x509Make(t, x509CA("Mk Strong Rsa Root"), x509Rsa(t), nil)
		l := x509Make(t, x509Leaf("weak.test", []string{"weak.test"}, nil), k, rroot)
		cases = append(cases, struct {
			name  string
			chain [][]byte
			weak  bool
		}{"rsa-1024 leaf key", [][]byte{l.der, rroot.der}, false})
	} else {
		t.Logf("no RSA-1024 key: %v", err)
	}
	var recs []string
	for _, c := range cases {
		recs = append(recs, x509BitsRecord(c.chain))
	}
	// The same chains through Verify against the system's pool: the
	// weak bit is Unsupported, else the untrusted root UnknownAuthority.
	for _, c := range cases {
		recs = append(recs, x509CachedRecord(x509Now, "", c.chain))
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		want := "err UnknownAuthority"
		if c.weak {
			want = "err Unsupported"
		}
		if v := got[len(cases)+i]; v != want {
			t.Errorf("%s through Verify: %s, want %s", c.name, v, want)
		}
	}
	for i, c := range cases {
		v, o := x509Bits(t, got[i])
		t.Logf("%s: verdict %#x status %#x", c.name, v, o)
		if (o&x509TrustWeak != 0) != c.weak {
			t.Errorf("%s: status %#x, weak bit want %v", c.name, o, c.weak)
		}
		if v == 0 {
			t.Errorf("%s: the policy passed a chain to a root in no store", c.name)
		}
	}
}
