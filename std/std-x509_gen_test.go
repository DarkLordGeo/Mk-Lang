// Why: the std.x509 test chains must come from a program in the repository, not
// from hand-edited files.
// What: TestX509_GenFixtures, which writes std/x509/testdata's chains, valid
// 2000 to 2200; run with go test ./std -run TestX509_GenFixtures -x509gen.
// How: Go's crypto/x509 makes them. ECDSA signatures are randomized, so the
// bytes change each run and no test pins them. No chain names an authority
// information access or CRL point, so a platform check never reaches the
// network.

package std

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var x509Gen = flag.Bool("x509gen", false, "write std/x509/testdata's chains again")

// x509Dir is where the fixtures live.
const x509Dir = "x509/testdata"

var (
	x509From = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	x509To   = time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
)

// x509Made is one certificate the generator made, with its key.
type x509Made struct {
	cert *x509.Certificate
	der  []byte
	key  crypto.Signer
}

var x509Serial int64 = 1000

// x509Make signs tmpl by parent (itself when parent is nil) and returns it.
func x509Make(t *testing.T, tmpl *x509.Certificate, key crypto.Signer, parent *x509Made) *x509Made {
	t.Helper()
	x509Serial++
	tmpl.SerialNumber = big.NewInt(x509Serial)
	if tmpl.NotBefore.IsZero() {
		tmpl.NotBefore = x509From
	}
	if tmpl.NotAfter.IsZero() {
		tmpl.NotAfter = x509To
	}
	signer, issuer := key, tmpl
	if parent != nil {
		signer, issuer = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, issuer, key.Public(), signer)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &x509Made{c, der, key}
}

func x509P256(t *testing.T) crypto.Signer {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func x509P384(t *testing.T) crypto.Signer {
	k, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func x509Rsa(t *testing.T) crypto.Signer {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func x509Ed(t *testing.T) crypto.Signer {
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func x509CA(name string) *x509.Certificate {
	return &x509.Certificate{
		Subject:               pkix.Name{CommonName: name, Organization: []string{"Mk test"}},
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            -1,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

func x509Leaf(name string, dns []string, ips []string) *x509.Certificate {
	c := &x509.Certificate{
		Subject:     pkix.Name{CommonName: name},
		DNSNames:    dns,
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, ip := range ips {
		c.IPAddresses = append(c.IPAddresses, net.ParseIP(ip))
	}
	return c
}

// x509Resign rewrites a certificate signed by sha256WithRSAEncryption to
// one signed by the RSA algorithm whose OID (of the same length) is oid,
// hashing with h, so the refusal of SHA-1 and MD5 can be tested on a
// signature that is otherwise right (Go makes neither any more).
func x509Resign(t *testing.T, m *x509Made, issuer *x509Made, oid []byte, h crypto.Hash) []byte {
	t.Helper()
	sha256Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x0b}
	tbs := bytes.Replace(m.cert.RawTBSCertificate, sha256Rsa, oid, 1)
	hh := h.New()
	hh.Write(tbs)
	sig, err := rsa.SignPKCS1v15(nil, issuer.key.(*rsa.PrivateKey), h, hh.Sum(nil))
	if err != nil {
		t.Fatal(err)
	}
	// Certificate ::= SEQUENCE { tbs, AlgorithmIdentifier, BIT STRING }.
	ai := append([]byte{0x30, 0x0d, 0x06, 0x09}, oid...)
	ai = append(ai, 0x05, 0x00)
	bits := append([]byte{0}, sig...)
	body := append(append(append([]byte{}, tbs...), ai...), x509DerTLV(0x03, bits)...)
	return x509DerTLV(0x30, body)
}

// x509DerTLV is the DER element of tag and content.
func x509DerTLV(tag byte, content []byte) []byte {
	n := len(content)
	switch {
	case n < 128:
		return append([]byte{tag, byte(n)}, content...)
	case n < 256:
		return append([]byte{tag, 0x81, byte(n)}, content...)
	default:
		return append([]byte{tag, 0x82, byte(n >> 8), byte(n)}, content...)
	}
}

func x509Write(t *testing.T, name string, ders ...[]byte) {
	t.Helper()
	var b bytes.Buffer
	for _, d := range ders {
		pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: d})
	}
	if err := os.WriteFile(filepath.Join(x509Dir, name), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestX509_GenFixtures writes the checked-in chains (only with -x509gen).
func TestX509_GenFixtures(t *testing.T) {
	if !*x509Gen {
		t.Skip("writes std/x509/testdata only with -x509gen")
	}
	if err := os.MkdirAll(x509Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := x509Make(t, x509CA("Mk Test Root"), x509P256(t), nil)
	it := x509CA("Mk Test Intermediate")
	it.MaxPathLen, it.MaxPathLenZero = 0, true
	inter := x509Make(t, it, x509P384(t), root)
	leaf := x509Make(t, x509Leaf("example.test", []string{"example.test", "*.wild.test"}, []string{"127.0.0.1", "::1"}), x509P256(t), inter)
	x509Write(t, "root.pem", root.der)
	x509Write(t, "chain.pem", leaf.der, inter.der)

	rroot := x509Make(t, x509CA("Mk Test RSA Root"), x509Rsa(t), nil)
	rleaf := x509Make(t, x509Leaf("rsa.test", []string{"rsa.test"}, nil), x509Rsa(t), rroot)
	pt := x509Leaf("pss.test", []string{"pss.test"}, nil)
	pt.SignatureAlgorithm = x509.SHA256WithRSAPSS
	pleaf := x509Make(t, pt, x509P256(t), rroot)
	x509Write(t, "rsa-root.pem", rroot.der)
	x509Write(t, "rsa-leaf.pem", rleaf.der)
	x509Write(t, "pss-leaf.pem", pleaf.der)
	sha1Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x05}
	md5Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x04}
	x509Write(t, "sha1-leaf.pem", x509Resign(t, rleaf, rroot, sha1Rsa, crypto.SHA1))
	x509Write(t, "md5-leaf.pem", x509Resign(t, rleaf, rroot, md5Rsa, crypto.MD5))

	eroot := x509Make(t, x509CA("Mk Test Ed25519 Root"), x509Ed(t), nil)
	eleaf := x509Make(t, x509Leaf("ed.test", []string{"ed.test"}, nil), x509Ed(t), eroot)
	x509Write(t, "ed-root.pem", eroot.der)
	x509Write(t, "ed-leaf.pem", eleaf.der)

	ct := x509Leaf("client.test", []string{"client.test"}, nil)
	ct.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	x509Write(t, "client-leaf.pem", x509Make(t, ct, x509P256(t), root).der)

	nt := x509CA("Mk Test Not A CA")
	nt.IsCA, nt.KeyUsage = false, x509.KeyUsageDigitalSignature
	noca := x509Make(t, nt, x509P256(t), root)
	x509Write(t, "noca-chain.pem", x509Make(t, x509Leaf("noca.test", []string{"noca.test"}, nil), x509P256(t), noca).der, noca.der)

	ncT := x509CA("Mk Test Constrained")
	ncT.PermittedDNSDomains = []string{".allowed.test"}
	ncT.PermittedDNSDomainsCritical = true
	_, ipnet, _ := net.ParseCIDR("127.0.0.0/8")
	ncT.PermittedIPRanges = []*net.IPNet{ipnet}
	nc := x509Make(t, ncT, x509P256(t), root)
	x509Write(t, "nc-good-chain.pem", x509Make(t, x509Leaf("a.allowed.test", []string{"a.allowed.test"}, []string{"127.0.0.2"}), x509P256(t), nc).der, nc.der)
	x509Write(t, "nc-bad-chain.pem", x509Make(t, x509Leaf("bad.test", []string{"bad.test"}, nil), x509P256(t), nc).der, nc.der)

	ft := x509Leaf("forever.test", []string{"forever.test"}, nil)
	ft.NotAfter = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	ft.NotBefore = time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC)
	x509Write(t, "forever-leaf.pem", x509Make(t, ft, x509P256(t), root).der)
}
