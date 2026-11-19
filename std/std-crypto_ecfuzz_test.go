// Why: every curve and RSA entry point that reads peer bytes must survive any
// input and agree with Go.
// What: FuzzSTDCRYPTO_Keys over ParsePublicKey, ParsePrivateKey, EcdsaVerify,
// EcdhShared, RsaPublic.New, RsaVerifyPkcs1 and RsaVerifyPss.
// How: each input becomes one EC driver command run by the dev driver (every
// check on); a key Mk reads must be one Go reads to the same value.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// cr2EcSrv is the dev EC driver kept running across fuzz inputs.
var cr2EcSrv struct {
	sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	errb *bytes.Buffer
}

// cr2EcServe sends one line to the running dev EC driver and returns its
// answer; a dead driver fails the input with its stderr.
func cr2EcServe(t *testing.T, line string) string {
	t.Helper()
	cr2EcSrv.Lock()
	defer cr2EcSrv.Unlock()
	if cr2EcSrv.cmd == nil {
		_ = cr2EcAsk(t, cr2EcVariants[0], []string{"K 00 3"})
		cr2EcExes.Lock()
		exe := cr2EcExes.m[cr2EcVariants[0].name]
		cr2EcExes.Unlock()
		cmd := exec.Command(exe)
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var errb bytes.Buffer
		cmd.Stderr = &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cr2EcSrv.cmd, cr2EcSrv.in, cr2EcSrv.out, cr2EcSrv.errb = cmd, in, bufio.NewReader(out), &errb
	}
	if _, err := io.WriteString(cr2EcSrv.in, line+"\n"); err != nil {
		t.Fatalf("EC driver: write: %v", err)
	}
	l, err := cr2EcSrv.out.ReadString('\n')
	if err != nil {
		cr2EcSrv.in.Close()
		cr2EcSrv.cmd.Wait()
		stderr := cr2EcSrv.errb.String()
		cr2EcSrv.cmd = nil
		t.Fatalf("EC driver died: %v\nstderr:\n%s\ncommand: %.300s", err, stderr, line)
	}
	return strings.TrimRight(l, "\r\n")
}

// cr2FzRsaModulus is Wycheproof's rsa_signature_2048_sha256 modulus.
const cr2FzRsaModulus = "a2b451a07d0aa5f96e455671513550514a8a5b462ebef717094fa1fee82224e637f9746d3f7cafd31878d80325b6ef5a1700f65903b469429e89d6eac8845097b5ab393189db92512ed8a7711a1253facd20f79c15e8247f3d3e42e46e48c98e254a2fe9765313a03eff8f17e1a029397a1fa26a8dce26f490ed81299615d9814c22da610428e09c7d9658594266f5c021d0fceca08d945a12be82de4d1ece6b4c03145b5d3495d4ed5411eb878daf05fd7afc3e09ada0f1126422f590975a1969816f48698bcbba1b4d9cae79d460d8f9f85e7975005d9bc22c4e5ac0f7c1a45d12569a62807d3b9a02e5a530e773066f453d1f5b4c2e9cf7820283f742b9d5"

// cr2FzKey is the fixed P-256 key and the fixed RSA key the verify
// commands use.
var cr2FzKey = func() struct {
	ec  *ecdsa.PrivateKey
	rsa *rsa.PublicKey
} {
	d, _ := hex.DecodeString("c9afa9d845ba75166b5c215767b1d6934e50c3db36e89b127b8a622b120f6721")
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		panic(err)
	}
	// Wycheproof's rsa_signature_2048_sha256 key (e = 65537), fixed so a
	// crasher replays.
	n, ok := new(big.Int).SetString(cr2FzRsaModulus, 16)
	if !ok {
		panic("cr2FzRsaModulus")
	}
	return struct {
		ec  *ecdsa.PrivateKey
		rsa *rsa.PublicKey
	}{k, &rsa.PublicKey{N: n, E: 65537}}
}()

// cr2FzGoPub is Go's reading of a public key, in the driver's form, or ""
// when Go refuses it or it is a kind the API has no form for.
func cr2FzGoPub(der []byte) string {
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		k, err = x509.ParsePKCS1PublicKey(der)
		if err != nil {
			return ""
		}
	}
	switch k := k.(type) {
	case *ecdsa.PublicKey:
		b, err := k.Bytes()
		if err != nil || (k.Curve != elliptic.P256() && k.Curve != elliptic.P384()) {
			return ""
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
	return ""
}

// cr2FzGoPriv is Go's reading of a private key in the driver's form, or
// "".
func cr2FzGoPriv(der []byte) string {
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		k, err = x509.ParseECPrivateKey(der)
		if err != nil {
			return ""
		}
	}
	switch k := k.(type) {
	case *ecdsa.PrivateKey:
		if k.Curve != elliptic.P256() {
			return ""
		}
		b, _ := k.Bytes()
		return fmt.Sprintf("Z ec %x", b)
	case ed25519.PrivateKey:
		return fmt.Sprintf("Z ed %x", k.Seed())
	}
	return ""
}

// cr2FzCase is one fuzz input: the driver command and a check of its
// answer.
func cr2FzCase(data []byte) (string, func(got string) string) {
	if len(data) == 0 {
		data = []byte{0}
	}
	op, b := data[0]%10, data[1:]
	if op >= 7 {
		return cr2FzCaseMore(op, b)
	}
	x := hex.EncodeToString
	pub, _ := cr2FzKey.ec.PublicKey.Bytes()
	digest := cr2Rand(1, 32)
	switch op {
	case 0:
		return "U " + x(b), func(got string) string {
			if strings.HasPrefix(got, "U bad-") {
				return ""
			}
			if w := cr2FzGoPub(b); w != got {
				return "Go reads " + w
			}
			return ""
		}
	case 1:
		return "Z " + x(b), func(got string) string {
			if strings.HasPrefix(got, "Z bad-") || got == "Z encrypted" {
				return ""
			}
			if w := cr2FzGoPriv(b); w != got {
				return "Go reads " + w
			}
			return ""
		}
	case 2, 3:
		f := "der"
		if op == 3 {
			f = "fixed"
		}
		return fmt.Sprintf("V 0 %x %x %s %x", pub, digest, f, b), func(got string) string {
			if w := fmt.Sprintf("V %v", cr2GoEcdsa(elliptic.P256(), pub, digest, b, op == 3)); w != got {
				return w
			}
			return ""
		}
	case 4:
		d := cr2Rand(uint64(len(b))+7, 32)
		d[0] &= 0x7f
		return fmt.Sprintf("H %x %x 32", d, b), func(got string) string {
			w := "H bad-key true"
			k, _ := ecdh.P256().NewPrivateKey(d)
			if p, err := ecdh.P256().NewPublicKey(b); err == nil {
				if s, err := k.ECDH(p); err == nil {
					w = "H " + x(s)
				}
			}
			if w != got {
				return w
			}
			return ""
		}
	case 5:
		kind := "p"
		if len(b) > 0 && b[0]&1 == 1 {
			kind = "s"
		}
		return fmt.Sprintf("W %s 256 %x 65537 %x %x", kind, cr2FzKey.rsa.N.Bytes(), digest, b), func(got string) string {
			var err error
			if kind == "p" {
				err = rsa.VerifyPKCS1v15(cr2FzKey.rsa, crypto.SHA256, digest, b)
			} else {
				err = rsa.VerifyPSS(cr2FzKey.rsa, crypto.SHA256, digest, b, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
			}
			if w := fmt.Sprintf("W %v", err == nil); w != got {
				return w
			}
			return ""
		}
	}
	e := uint64(65537)
	if len(b) >= 4 {
		e = uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
		b = b[4:]
	}
	return fmt.Sprintf("K %x %d", b, e), func(got string) string {
		n := new(big.Int).SetBytes(b)
		ok := n.BitLen() >= 2048 && n.BitLen() <= 8192 && n.Bit(0) == 1 && e&1 == 1 && e >= 3
		w := "K bad-key"
		if ok {
			w = "K ok"
		}
		if w != got {
			return w
		}
		return ""
	}
}

// cr2FzSeeds are seeds for every command: Wycheproof's ECDH SPKI keys,
// keys Go marshals, and signatures of the fixed keys.
func cr2FzSeeds(t testing.TB) [][]byte {
	var seeds [][]byte
	add := func(op byte, b []byte) { seeds = append(seeds, append([]byte{op}, b...)) }
	k := cr2FzKey.ec
	spki, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	p8, _ := x509.MarshalPKCS8PrivateKey(k)
	sec1, _ := x509.MarshalECPrivateKey(k)
	add(0, spki)
	add(0, x509.MarshalPKCS1PublicKey(cr2FzKey.rsa))
	rspki, _ := x509.MarshalPKIXPublicKey(cr2FzKey.rsa)
	add(0, rspki)
	add(1, p8)
	add(1, sec1)
	// Scalars with leading zero bytes and with a non-zero pad byte.
	padded, _ := cr2PaddedKeys(t, k)
	for _, der := range padded {
		add(1, der)
	}
	edk := ed25519.NewKeyFromSeed(make([]byte, 32))
	ep8, _ := x509.MarshalPKCS8PrivateKey(edk)
	add(1, ep8)
	pub, _ := k.PublicKey.Bytes()
	sig, _ := ecdsa.SignASN1(nil, k, cr2Rand(1, 32))
	add(2, sig)
	add(3, cr2EcFixedOf(sig, 32))
	add(4, pub)
	// The first run's crasher: a SEC 1 key whose [1] held no BIT STRING.
	seeds = append(seeds, []byte([]byte("\x010w\x02\x01\x01\x04 1227A2C991 ug\xb1֓NP\xc3\xdb6\xe8\x9b\x12{\x8ab+\x12\x0fg!\xa0\n\x06\b*\x86H\xce=\x03\x01\a\xa1D\x03C000000000000000000000000000000000000000000000000000000000000000000")))
	add(5, sig)
	add(6, append([]byte{0, 1, 0, 1}, cr2FzKey.rsa.N.Bytes()...))
	seeds = append(seeds, cr2FzMoreSeeds()...)
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	var wp cr2WpEcdh
	if b, err := os.ReadFile(filepath.Join(root, "wycheproof", "testvectors_v1", "ecdh_secp256r1_test.json")); err == nil && json.Unmarshal(b, &wp) == nil {
		for i, g := range wp.TestGroups {
			for j, tc := range g.Tests {
				if (i+j)%5 == 0 {
					b, _ := hex.DecodeString(tc.Public)
					add(0, b)
				}
			}
		}
	}
	return seeds
}

// cr2EcFixedOf is a DER signature as r || s of size bytes each.
func cr2EcFixedOf(der []byte, size int) []byte {
	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		return der
	}
	out := make([]byte, 2*size)
	sig.R.FillBytes(out[:size])
	sig.S.FillBytes(out[size:])
	return out
}

// FuzzSTDCRYPTO_Keys: one command per input against Go. Seeded from
// Wycheproof's ECDH SPKI keys and ECDSA signatures and from keys Go marshals:
//
//	go test ./std -run '^$' -fuzz FuzzSTDCRYPTO_Keys -fuzztime 5m -parallel 2
func FuzzSTDCRYPTO_Keys(f *testing.F) {
	for _, s := range cr2FzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		line, check := cr2FzCase(data)
		got := cr2EcServe(t, line)
		if w := check(got); w != "" {
			t.Fatalf("%.300s\n got  %.300s\n want %.300s", line, got, w)
		}
	})
}

// TestSTDCRYPTO_KeysFuzzSeeds: every seed of FuzzSTDCRYPTO_Keys holds
// on dev and release without -fuzz.
func TestSTDCRYPTO_KeysFuzzSeeds(t *testing.T) {
	t.Parallel()
	var lines []string
	var checks []func(string) string
	for _, s := range cr2FzSeeds(t) {
		l, c := cr2FzCase(s)
		lines = append(lines, l)
		checks = append(checks, c)
	}
	for _, v := range cr2EcVariants {
		got := cr2EcAsk(t, v, lines)
		for i := range lines {
			if w := checks[i](got[i]); w != "" {
				t.Errorf("%s: %.200s\n got  %.200s\n want %.200s", v.name, lines[i], got[i], w)
			}
		}
	}
	t.Logf("%d seeds", len(lines))
}
