// Why: the std-tls loopback benchmark needs a certificate chain made by a
// program in the repository and checked in, not one made by hand.
// What: TestStdTls_BenchPki, the generator of
// tests/bench-lanes/std-tls/loopback's P-256 root, "localhost" leaf and PKCS #8
// key, valid 2000 to 2200.
// How: with MK_TLS_GEN_BENCH=1 it prints the three PEM blocks to paste into the
// row's Mk and Go sources; without it, it checks that the checked-in chain
// parses and chains.

package std

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestStdTls_BenchPki(t *testing.T) {
	if os.Getenv("MK_TLS_GEN_BENCH") == "1" {
		rk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		nb := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
		na := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
		rt := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Mk bench root"}, NotBefore: nb, NotAfter: na,
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		rd, err := x509.CreateCertificate(rand.Reader, rt, rt, &rk.PublicKey, rk)
		if err != nil {
			t.Fatal(err)
		}
		root, _ := x509.ParseCertificate(rd)
		lk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		lt := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: nb, NotAfter: na,
			DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		ld, err := x509.CreateCertificate(rand.Reader, lt, root, &lk.PublicKey, rk)
		if err != nil {
			t.Fatal(err)
		}
		kd, _ := x509.MarshalPKCS8PrivateKey(lk)
		fmt.Print(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ld})))
		fmt.Print(string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd})))
		fmt.Print(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rd})))
		return
	}
	b, err := os.ReadFile(filepath.Join("..", "tests", "bench-lanes", "std-tls", "loopback", "mk", "tls-loopback.mk"))
	if err != nil {
		t.Fatal(err)
	}
	get := func(name string) []byte {
		m := regexp.MustCompile(`(?m)^const ` + name + `Pem: str = "(.*)"$`).FindSubmatch(b)
		if m == nil {
			t.Fatalf("tls-loopback.mk has no %sPem", name)
		}
		blk, _ := pem.Decode([]byte(strings.ReplaceAll(string(m[1]), `\n`, "\n")))
		if blk == nil {
			t.Fatalf("%sPem is not PEM", name)
		}
		return blk.Bytes
	}
	leaf, err := x509.ParseCertificate(get("Leaf"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(get("Root"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParsePKCS8PrivateKey(get("Key")); err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	for _, at := range []time.Time{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2199, 12, 31, 0, 0, 0, 0, time.UTC)} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "localhost", CurrentTime: at}); err != nil {
			t.Fatalf("the bench leaf does not chain at %v: %v", at, err)
		}
	}
}
