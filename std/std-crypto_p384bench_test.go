// Why: std.crypto has no P-384 secrets, so the P-384 verify bench row needs
// keys, digests and signatures written into each column's source.
// What: TestSTDCRYPTO_P384VerifyRowTables, generator and check.
// How: splitmix64 seed 1 secrets and digests, signed by Go under SHA-384
// (RFC 6979); MK_CR2_BENCHGEN=1 writes the tables, otherwise it checks them.

package std

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	_ "crypto/sha512"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cr2P384Begin = "// BEGIN generated tables (std/std-crypto_p384bench_test.go)\n"
const cr2P384End = "// END generated tables\n"

// cr2P384Tables is the row's 16 public keys (uncompressed SEC 1), digests
// and DER signatures, in hex.
func cr2P384Tables(t *testing.T) (pubs, digests, sigs []string) {
	t.Helper()
	st := uint64(1)
	fill := func(b []byte) {
		for i := 0; i < len(b); {
			st += 0x9e3779b97f4a7c15
			z := st
			z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
			z = (z ^ z>>27) * 0x94d049bb133111eb
			x := z ^ z>>31
			for k := 0; k < 8 && i < len(b); k++ {
				b[i] = byte(x >> (8 * k))
				i++
			}
		}
	}
	const keys = 16
	secrets := make([]byte, 48*keys)
	fill(secrets)
	ds := make([]byte, 48*keys)
	fill(ds)
	for i := range keys {
		secrets[48*i] &= 0x7f
		k, err := ecdsa.ParseRawPrivateKey(elliptic.P384(), secrets[48*i:48*i+48])
		if err != nil {
			t.Fatal(err)
		}
		pub, err := k.PublicKey.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		d := ds[48*i : 48*i+48]
		der, err := k.Sign(nil, d, crypto.SHA384)
		if err != nil {
			t.Fatal(err)
		}
		if !ecdsa.VerifyASN1(&k.PublicKey, d, der) {
			t.Fatalf("key %d: Go does not verify its own signature", i)
		}
		pubs = append(pubs, fmt.Sprintf("%x", pub))
		digests = append(digests, fmt.Sprintf("%x", d))
		sigs = append(sigs, fmt.Sprintf("%x", der))
	}
	return pubs, digests, sigs
}

// cr2P384Block renders the tables in one column's syntax.
func cr2P384Block(lang string, pubs, digests, sigs []string) string {
	var b strings.Builder
	b.WriteString(cr2P384Begin)
	list := func(name string, xs []string) {
		switch lang {
		case "mk":
			fmt.Fprintf(&b, "const %s: []str = [\n", name)
			for _, x := range xs {
				fmt.Fprintf(&b, "    %q,\n", x)
			}
			b.WriteString("]\n")
		case "go":
			fmt.Fprintf(&b, "var %s = []string{\n", name)
			for _, x := range xs {
				fmt.Fprintf(&b, "\t%q,\n", x)
			}
			b.WriteString("}\n")
		case "zig":
			fmt.Fprintf(&b, "const %s = [_][]const u8{\n", name)
			for _, x := range xs {
				fmt.Fprintf(&b, "    %q,\n", x)
			}
			b.WriteString("};\n")
		}
	}
	names := map[string][3]string{
		"mk":  {"Pubs", "Digests", "Sigs"},
		"go":  {"pubHex", "digestHex", "sigHex"},
		"zig": {"pub_hex", "digest_hex", "sig_hex"},
	}[lang]
	list(names[0], pubs)
	list(names[1], digests)
	list(names[2], sigs)
	b.WriteString(cr2P384End)
	return b.String()
}

// TestSTDCRYPTO_P384VerifyRowTables: the P-384 verify row's keys,
// digests and DER signatures are tables written into each column's source
// from splitmix64 seed 1 (48-byte secrets, the first byte masked to 7 bits,
// then 48-byte digests) and Go's RFC 6979 signing under SHA-384. Without
// MK_CR2_BENCHGEN=1 the test checks that each source holds exactly these
// tables and that Go verifies every signature; with it, it writes them:
//
//	MK_CR2_BENCHGEN=1 go test ./std -run TestSTDCRYPTO_P384VerifyRowTables
func TestSTDCRYPTO_P384VerifyRowTables(t *testing.T) {
	pubs, digests, sigs := cr2P384Tables(t)
	row := filepath.Join("..", "tests", "bench-lanes", "std-crypto2", "p384-verify")
	write := os.Getenv("MK_CR2_BENCHGEN") == "1"
	for lang, file := range map[string]string{
		"mk":  filepath.Join(row, "mk", "crypto-p384-verify.mk"),
		"go":  filepath.Join(row, "testdata", "crypto-p384-verify.go"),
		"zig": filepath.Join(row, "crypto-p384-verify.zig"),
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		src := strings.ReplaceAll(string(b), "\r\n", "\n")
		i := strings.Index(src, cr2P384Begin)
		j := strings.Index(src, cr2P384End)
		if i < 0 || j < i {
			t.Fatalf("%s: no generated-table markers", file)
		}
		want := cr2P384Block(lang, pubs, digests, sigs)
		whole := src[:i] + want + src[j+len(cr2P384End):]
		if lang == "go" {
			// The Go baseline is checked by the gate's gofmt step, so the
			// file holds the generated block as gofmt writes it.
			f, err := format.Source([]byte(whole))
			if err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			whole = string(f)
		}
		if whole == src {
			continue
		}
		if !write {
			t.Errorf("%s: the tables differ from the generator's (MK_CR2_BENCHGEN=1 writes them)", file)
			continue
		}
		if err := os.WriteFile(file, []byte(whole), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", file)
	}
}
