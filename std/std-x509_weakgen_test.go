// Why: the strong-signature tests need weak chains whose bytes never change.
// What: TestX509_WeakFixtureStable, which makes weak-chain.pem again and
// compares; -x509weakgen writes it.
// How: RSA keys from a SHA-256 counter stream per key name, fixed serials and
// validity, and RSA PKCS#1 v1.5 signatures, which have no randomness.

package std

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

var x509WeakGen = flag.Bool("x509weakgen", false, "write std/x509/testdata/weak-chain.pem again")

// x509WeakFile is the fixture's name under x509Dir.
const x509WeakFile = "weak-chain.pem"

// The fixture's certificates, by their index in the file.
const (
	weakLeaf = iota
	weakInter
	weakInterSha1
	weakInterMd5
	weakRoot
	weakOldRoot
	weakCross
	weakCount
)

// x509DetStream is SHA-256(seed || counter) block after block: a
// deterministic byte stream for key generation.
type x509DetStream struct {
	seed []byte
	n    uint64
	buf  []byte
}

func (s *x509DetStream) Read(p []byte) (int, error) {
	for i := range p {
		if len(s.buf) == 0 {
			var c [8]byte
			binary.BigEndian.PutUint64(c[:], s.n)
			s.n++
			h := sha256.Sum256(append(append([]byte{}, s.seed...), c[:]...))
			s.buf = h[:]
		}
		p[i] = s.buf[0]
		s.buf = s.buf[1:]
	}
	return len(p), nil
}

// x509DetPrime is the first prime p >= a 1024-bit odd candidate from s,
// its top two bits set (so two make a 2048-bit modulus), with p - 1
// coprime to 65537.
func x509DetPrime(s *x509DetStream) *big.Int {
	b := make([]byte, 128)
	s.Read(b)
	b[0] |= 0xC0
	b[127] |= 1
	p := new(big.Int).SetBytes(b)
	e := big.NewInt(65537)
	one := big.NewInt(1)
	for {
		if p.ProbablyPrime(32) {
			pm := new(big.Int).Sub(p, one)
			if new(big.Int).GCD(nil, nil, pm, e).Cmp(one) == 0 {
				return p
			}
		}
		p.Add(p, big.NewInt(2))
	}
}

// x509DetRsa is the 2048-bit RSA key named name, the same on every run.
func x509DetRsa(t *testing.T, name string) *rsa.PrivateKey {
	t.Helper()
	s := &x509DetStream{seed: []byte("mk x509 weak fixture " + name)}
	p := x509DetPrime(s)
	q := x509DetPrime(s)
	for p.Cmp(q) == 0 {
		q = x509DetPrime(s)
	}
	n := new(big.Int).Mul(p, q)
	one := big.NewInt(1)
	phi := new(big.Int).Mul(new(big.Int).Sub(p, one), new(big.Int).Sub(q, one))
	d := new(big.Int).ModInverse(big.NewInt(65537), phi)
	k := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: n, E: 65537}, D: d, Primes: []*big.Int{p, q}}
	if err := k.Validate(); err != nil {
		t.Fatalf("key %s: %v", name, err)
	}
	k.Precompute()
	if k.N.BitLen() != 2048 {
		t.Fatalf("key %s: %d bits", name, k.N.BitLen())
	}
	return k
}

// x509DetKeyId is RFC 7093's method 1 key identifier of key: the leftmost
// 160 bits of the SHA-256 of its subjectPublicKey (the PKCS#1 key), set
// explicitly so the fixture does not depend on how a Go version derives
// one.
func x509DetKeyId(key *rsa.PrivateKey) []byte {
	s := sha256.Sum256(x509.MarshalPKCS1PublicKey(&key.PublicKey))
	return s[:20]
}

// x509DetMake signs tmpl (serial number serial) by parent, itself when
// parent is nil, with sha256WithRSAEncryption; tmpl's key identifiers are
// set here, the authority's from parent.
func x509DetMake(t *testing.T, tmpl *x509.Certificate, serial int64, key *rsa.PrivateKey, parent *x509Made) *x509Made {
	t.Helper()
	tmpl.SerialNumber = big.NewInt(serial)
	tmpl.NotBefore, tmpl.NotAfter = x509From, x509To
	tmpl.SignatureAlgorithm = x509.SHA256WithRSA
	tmpl.SubjectKeyId = x509DetKeyId(key)
	tmpl.AuthorityKeyId = nil
	if parent != nil {
		tmpl.AuthorityKeyId = x509DetKeyId(parent.key.(*rsa.PrivateKey))
	}
	signer, issuer := crypto.Signer(key), tmpl
	if parent != nil {
		signer, issuer = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(&x509DetStream{seed: []byte("mk x509 weak fixture signing")}, tmpl, issuer, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &x509Made{c, der, key}
}

// x509WeakChain makes the fixture's certificates, in the file's order.
func x509WeakChain(t *testing.T) [][]byte {
	t.Helper()
	sha1Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x05}
	md5Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x04}
	ca := func(name string) *x509.Certificate {
		return &x509.Certificate{
			Subject:               pkix.Name{CommonName: name, Organization: []string{"Mk test"}},
			IsCA:                  true,
			BasicConstraintsValid: true,
			MaxPathLen:            -1,
			KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		}
	}
	root := x509DetMake(t, ca("Mk Weak Fixture Root"), 7141, x509DetRsa(t, "root"), nil)
	old := x509DetMake(t, ca("Mk Weak Fixture Old Root"), 7142, x509DetRsa(t, "old root"), nil)
	interKey := x509DetRsa(t, "intermediate")
	inter := x509DetMake(t, ca("Mk Weak Fixture Intermediate"), 7143, interKey, root)
	cross := x509DetMake(t, ca("Mk Weak Fixture Intermediate"), 7144, interKey, old)
	leaf := x509DetMake(t, x509Leaf("weak.test", []string{"weak.test"}, nil), 7145, x509DetRsa(t, "leaf"), inter)
	out := make([][]byte, weakCount)
	out[weakLeaf] = leaf.der
	out[weakInter] = inter.der
	out[weakInterSha1] = x509Resign(t, inter, root, sha1Rsa, crypto.SHA1)
	out[weakInterMd5] = x509Resign(t, inter, root, md5Rsa, crypto.MD5)
	out[weakRoot] = root.der
	out[weakOldRoot] = old.der
	out[weakCross] = x509Resign(t, cross, old, sha1Rsa, crypto.SHA1)
	return out
}

// x509WeakPem is ders as the fixture file's text.
func x509WeakPem(ders [][]byte) []byte {
	var b bytes.Buffer
	for _, d := range ders {
		pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: d})
	}
	return b.Bytes()
}

// TestX509_WeakFixtureStable: the generator makes the checked-in file's
// bytes exactly (twice, so it is deterministic), and Go reads the
// intermediates' signatures as the algorithms they claim. The file holds,
// in order: the leaf (weak.test), the intermediate signed with
// sha256WithRSAEncryption, the same intermediate signed with
// sha1WithRSAEncryption and with md5WithRSAEncryption, the root, a second
// root, and the intermediate cross-signed by that second root with SHA-1.
// Write it again with go test ./std -run TestX509_WeakFixtureStable
// -x509weakgen.
func TestX509_WeakFixtureStable(t *testing.T) {
	a := x509WeakPem(x509WeakChain(t))
	path := filepath.Join(x509Dir, x509WeakFile)
	if *x509WeakGen {
		if err := os.WriteFile(path, a, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if b := x509WeakPem(x509WeakChain(t)); !bytes.Equal(a, b) {
		t.Fatal("two runs of the generator differ")
	}
	have, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n")), a) {
		t.Fatalf("%s differs from the generator's output; run with -x509weakgen and review the change", path)
	}
	ders := x509Fixtures(t)[x509WeakFile]
	if len(ders) != weakCount {
		t.Fatalf("%d certificates, want %d", len(ders), weakCount)
	}
	want := map[int]x509.SignatureAlgorithm{weakLeaf: x509.SHA256WithRSA, weakInter: x509.SHA256WithRSA, weakInterSha1: x509.SHA1WithRSA,
		weakInterMd5: x509.MD5WithRSA, weakRoot: x509.SHA256WithRSA, weakOldRoot: x509.SHA256WithRSA, weakCross: x509.SHA1WithRSA}
	for i, d := range ders {
		c, err := x509.ParseCertificate(d)
		if err != nil {
			t.Fatalf("certificate %d: %v", i, err)
		}
		if c.SignatureAlgorithm != want[i] {
			t.Errorf("certificate %d: %v, want %v", i, c.SignatureAlgorithm, want[i])
		}
	}
	// The SHA-256 chain verifies under the root in Go; the SHA-1 and MD5
	// intermediates do not.
	if err := x509GoVerify([][]byte{ders[weakLeaf], ders[weakInter]}, [][]byte{ders[weakRoot]}, "weak.test", x509Now); err != nil {
		t.Errorf("Go refuses the SHA-256 chain: %v", err)
	}
	for _, i := range []int{weakInterSha1, weakInterMd5} {
		if err := x509GoVerify([][]byte{ders[weakLeaf], ders[i]}, [][]byte{ders[weakRoot]}, "weak.test", x509Now); err == nil {
			t.Errorf("Go accepts intermediate %d", i)
		}
	}
}
