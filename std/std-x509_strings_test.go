// Why: Parse checks each name attribute's string type (StringValid in
// std/x509/parse.mk) and must accept exactly what Go accepts.
// What: TestX509_StringSetsMatchGo.
// How: every byte value is put into a PrintableString, IA5String, NumericString
// and UTF8String name and parsed by Mk and by Go's ParseCertificate.

package std

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"strings"
	"testing"
)

// TestX509_StringSetsMatchGo puts every byte value into the root's issuer
// common name, a PrintableString, and into the same name retagged as
// IA5String, NumericString and UTF8String, and requires Parse to take the
// certificate exactly when Go's ParseCertificate does (the signature is not
// checked by either).
func TestX509_StringSetsMatchGo(t *testing.T) {
	root := x509Fixtures(t)["root.pem"][0]
	cn := []byte{0x13, 0x0c, 'M', 'k', ' ', 'T', 'e', 's', 't', ' ', 'R', 'o', 'o', 't'}
	at := bytes.Index(root, cn)
	if at < 0 {
		t.Fatal("root.pem's issuer common name not found")
	}
	var inputs [][]byte
	var what []string
	for _, tag := range []byte{0x13, 0x16, 0x12, 0x0c} {
		for b := range 256 {
			for _, pos := range []int{2, 7, 13} {
				d := bytes.Clone(root)
				d[at] = tag
				d[at+pos] = byte(b)
				inputs = append(inputs, d)
				what = append(what, fmt.Sprintf("tag %#x byte %#x at %d", tag, b, pos))
			}
		}
	}
	// "k" answers with the key's kind only: a name holding a line break
	// cannot break the driver's one answer per line.
	records := make([]string, len(inputs))
	for i, in := range inputs {
		records[i] = "k " + x509Hex(in)
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		bad := 0
		for i, g := range got {
			_, err := x509.ParseCertificate(inputs[i])
			if (err == nil) != strings.HasPrefix(g, "ok ") {
				bad++
				if bad <= 10 {
					t.Errorf("%s: %s: Go %v, Mk %s", mode, what[i], err, g)
				}
			}
		}
		t.Logf("%s: %d certificates, %d differ from Go", mode, len(inputs), bad)
	}
}
