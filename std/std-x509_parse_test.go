// Why: x509.Parse must read certificates as a mature parser does and fail only
// with Malformed.
// What: TestX509_ParseFixturesMatchGo, TestX509_ParsePemCounts and
// TestX509_PublicKeyKinds.
// How: fixtures and generated certificates parsed by Mk and by Go's crypto/x509
// must agree field by field; mutated DER must give ok or Malformed.

package std

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// x509Fixtures reads every checked-in fixture's certificates.
func x509Fixtures(t *testing.T) map[string][][]byte {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(x509Dir, "*.pem"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures under %s: %v", x509Dir, err)
	}
	out := map[string][][]byte{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for {
			var blk *pem.Block
			blk, b = pem.Decode(b)
			if blk == nil {
				break
			}
			out[filepath.Base(f)] = append(out[filepath.Base(f)], blk.Bytes)
		}
	}
	return out
}

// x509Ext is the value of the extension with OID oid, or nil.
func x509Ext(c *x509.Certificate, oid asn1.ObjectIdentifier) []byte {
	for _, e := range c.Extensions {
		if e.Id.Equal(oid) {
			return e.Value
		}
	}
	return nil
}

var (
	x509OidKU  = asn1.ObjectIdentifier{2, 5, 29, 15}
	x509OidSAN = asn1.ObjectIdentifier{2, 5, 29, 17}
	x509OidNC  = asn1.ObjectIdentifier{2, 5, 29, 30}
	x509OidEKU = asn1.ObjectIdentifier{2, 5, 29, 37}
)

// x509ShortNames are RFC 4514's short names std.x509 writes.
var x509ShortNames = map[string]string{
	"2.5.4.3": "CN", "2.5.4.5": "SERIALNUMBER", "2.5.4.6": "C", "2.5.4.7": "L", "2.5.4.8": "ST",
	"2.5.4.9": "STREET", "2.5.4.10": "O", "2.5.4.11": "OU", "2.5.4.17": "POSTALCODE",
}

// x509TextTags are the string types Go's asn1 reads as a string in a
// Name (UTF8String, PrintableString, IA5String, NumericString, T61String,
// BMPString): pkix.RDNSequence.String writes them as text under any
// attribute type (Go 1.27).
var x509TextTags = map[int]bool{12: true, 19: true, 22: true, 18: true, 20: true, 30: true}

// x509NameText is the Go model of Subject and Issuer: RFC 4514 over the
// raw Name, the RDNs last first, as std/x509/names.mk documents.
func x509NameText(raw []byte) string {
	var rdns []asn1.RawValue
	if _, err := asn1.Unmarshal(raw, &rdns); err != nil {
		return "<bad name>"
	}
	var parts []string
	for i := len(rdns) - 1; i >= 0; i-- {
		rest := rdns[i].Bytes
		var atvs []string
		for len(rest) > 0 {
			var atv struct {
				Type  asn1.ObjectIdentifier
				Value asn1.RawValue
			}
			var err error
			rest, err = asn1.Unmarshal(rest, &atv)
			if err != nil {
				return "<bad atv>"
			}
			short, ok := x509ShortNames[atv.Type.String()]
			if !ok {
				short = atv.Type.String()
			}
			if atv.Value.Class != 0 || atv.Value.IsCompound || !x509TextTags[atv.Value.Tag] {
				atvs = append(atvs, atv.Type.String()+"=#"+x509Hex(atv.Value.FullBytes))
				continue
			}
			v := string(atv.Value.Bytes)
			if atv.Value.Tag == 30 {
				b := atv.Value.Bytes
				if n := len(b); n >= 2 && b[n-1] == 0 && b[n-2] == 0 {
					b = b[:n-2]
				}
				u := make([]uint16, len(b)/2)
				for k := range u {
					u[k] = uint16(b[2*k])<<8 | uint16(b[2*k+1])
				}
				v = string(utf16.Decode(u))
			}
			var esc strings.Builder
			for k, c := range v {
				e := strings.ContainsRune(",+\"\\<>;", c) || (c == ' ' && (k == 0 || k == len(v)-1)) || (c == '#' && k == 0)
				if e {
					esc.WriteByte('\\')
				}
				esc.WriteRune(c)
			}
			atvs = append(atvs, short+"="+esc.String())
		}
		parts = append(parts, strings.Join(atvs, "+"))
	}
	return strings.Join(parts, ",")
}

// x509SanNames is the model of Names: the SAN's DNS names and IP
// addresses in order.
func x509SanNames(c *x509.Certificate) string {
	v := x509Ext(c, x509OidSAN)
	if v == nil {
		return ""
	}
	var seq asn1.RawValue
	asn1.Unmarshal(v, &seq)
	rest := seq.Bytes
	var out []string
	for len(rest) > 0 {
		var g asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &g)
		if err != nil {
			break
		}
		if g.Class != 2 || g.IsCompound {
			continue
		}
		switch g.Tag {
		case 2:
			out = append(out, string(g.Bytes))
		case 7:
			out = append(out, net.IP(g.Bytes).String())
		}
	}
	return strings.Join(out, ",")
}

// x509Unix is Time.Unix of t saturated to Time's range.
func x509Unix(t time.Time) int64 {
	minT := time.Unix(0, math.MinInt64)
	maxT := time.Unix(0, math.MaxInt64)
	if t.Before(minT) {
		t = minT
	}
	if t.After(maxT) {
		t = maxT
	}
	ns := t.UnixNano()
	s := ns / 1e9
	if ns%1e9 < 0 {
		s--
	}
	return s
}

// x509AlgCode is std.x509's SigAlg code of a Go algorithm.
func x509AlgCode(a x509.SignatureAlgorithm) int {
	switch a {
	case x509.SHA256WithRSA:
		return 1
	case x509.SHA384WithRSA:
		return 2
	case x509.SHA512WithRSA:
		return 3
	case x509.SHA256WithRSAPSS:
		return 4
	case x509.SHA384WithRSAPSS:
		return 5
	case x509.SHA512WithRSAPSS:
		return 6
	case x509.ECDSAWithSHA256:
		return 7
	case x509.ECDSAWithSHA384:
		return 8
	case x509.ECDSAWithSHA512:
		return 9
	case x509.PureEd25519:
		return 10
	case x509.SHA1WithRSA, x509.MD5WithRSA, x509.MD2WithRSA, x509.ECDSAWithSHA1:
		return 11
	}
	return 0
}

// x509Summary is what FxSummary must print for c, its first nine fields.
func x509Summary(c *x509.Certificate) string {
	var flags uint32
	if c.BasicConstraintsValid {
		flags |= 1
	}
	if c.IsCA {
		flags |= 2
	}
	if x509Ext(c, x509OidKU) != nil {
		flags |= 4
	}
	if x509Ext(c, x509OidEKU) != nil {
		flags |= 8
	}
	for _, u := range c.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			flags |= 16
		}
		if u == x509.ExtKeyUsageAny {
			flags |= 32
		}
	}
	if len(c.UnhandledCriticalExtensions) > 0 {
		flags |= 64
	}
	for _, e := range c.Extensions {
		// std.x509 does no policy processing: a critical policy
		// mappings, policy constraints or inhibitAnyPolicy extension stays
		// unhandled, as in Go before 1.24.
		if e.Critical && len(e.Id) == 4 && e.Id[0] == 2 && e.Id[1] == 5 && e.Id[2] == 29 && (e.Id[3] == 33 || e.Id[3] == 36 || e.Id[3] == 54) {
			flags |= 64
		}
	}
	if x509Ext(c, x509OidSAN) != nil {
		flags |= 128
	}
	if x509Ext(c, x509OidNC) != nil {
		flags |= 256
	}
	pl := -1
	if c.BasicConstraintsValid {
		pl = c.MaxPathLen
	}
	return x509OneLine(fmt.Sprintf("ok %s|%s|%s|%d|%d|%d|%d|%d|%d", x509NameText(c.RawSubject), x509NameText(c.RawIssuer), x509SanNames(c),
		x509Unix(c.NotBefore), x509Unix(c.NotAfter), flags, pl, int(c.KeyUsage), x509AlgCode(c.SignatureAlgorithm)))
}

// x509OneLine writes each line break of s as the driver's OneLine does
// (a name may hold one: an IA5 subjectAltName, a UTF8String attribute).
func x509OneLine(s string) string {
	return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(s)
}

// x509Head is a FxSummary answer without its last two '|' fields (the
// subjectAltName and name constraints lengths, which Go's summary does
// not have): the first nine fields counted from the end, since a name may
// hold a '|'.
func x509Head(s string) string {
	f := strings.Split(s, "|")
	if len(f) >= 11 {
		f = f[:len(f)-2]
	}
	return strings.Join(f, "|")
}

// TestX509_ParseFixturesMatchGo: every fixture certificate reads as Go
// reads it, in both build modes.
func TestX509_ParseFixturesMatchGo(t *testing.T) {
	fx := x509Fixtures(t)
	var records, want, names []string
	for name, ders := range fx {
		for i, d := range ders {
			c, err := x509.ParseCertificate(d)
			if err != nil {
				t.Fatalf("%s[%d]: Go: %v", name, i, err)
			}
			records = append(records, "p "+x509Hex(d))
			want = append(want, x509Summary(c))
			names = append(names, fmt.Sprintf("%s[%d]", name, i))
		}
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		for i := range got {
			if x509Head(got[i]) != want[i] {
				t.Errorf("%s %s:\n got  %s\n want %s", mode, names[i], got[i], want[i])
			}
		}
	}
}

// TestX509_ParsePemCounts: ParsePem takes every CERTIFICATE block, skips
// other labels and the text around, and fails on a broken block, where the
// lenient reader the system bundle uses skips it.
func TestX509_ParsePemCounts(t *testing.T) {
	fx := x509Fixtures(t)
	chain := fx["chain.pem"]
	var both strings.Builder
	both.WriteString("explanatory text\n")
	for _, d := range chain {
		pem.Encode(&both, &pem.Block{Type: "CERTIFICATE", Bytes: d})
	}
	pem.Encode(&both, &pem.Block{Type: "PRIVATE KEY", Bytes: []byte{1, 2, 3}})
	broken := both.String() + "-----BEGIN CERTIFICATE-----\nAAA*\n-----END CERTIFICATE-----\n"
	notCert := both.String() + "-----BEGIN CERTIFICATE-----\nAAEC\n-----END CERTIFICATE-----\n"
	cases := []struct{ in, strict, lenient string }{
		{both.String(), "ok 2", "ok 2"},
		{"", "ok 0", "ok 0"},
		{"no blocks here", "ok 0", "ok 0"},
		{broken, "err Malformed", "ok 2"},
		{notCert, "err Malformed", "ok 2"},
		{"-----BEGIN CERTIFICATE-----\nAAAA\n", "err Malformed", "ok 0"},
	}
	var records []string
	for _, c := range cases {
		records = append(records, "m "+x509Hex([]byte(c.in)), "w "+x509Hex([]byte(c.in)))
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		for i, c := range cases {
			if !strings.HasPrefix(got[2*i], c.strict) {
				t.Errorf("%s case %d ParsePem: got %s, want %s", mode, i, got[2*i], c.strict)
			}
			if got[2*i+1] != c.lenient {
				t.Errorf("%s case %d lenient: got %s, want %s", mode, i, got[2*i+1], c.lenient)
			}
		}
	}
}

// TestX509_PublicKeyKinds: PublicKey gives each fixture leaf's key, of the
// kind Go reads.
func TestX509_PublicKeyKinds(t *testing.T) {
	fx := x509Fixtures(t)
	cases := []struct{ file, want string }{
		{"chain.pem", "ok ec 65"},
		{"rsa-leaf.pem", "ok rsa"},
		{"ed-leaf.pem", "ok ed25519"},
		{"pss-leaf.pem", "ok ec 65"},
	}
	var records []string
	for _, c := range cases {
		records = append(records, "k "+x509Hex(fx[c.file][0]))
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		for i, c := range cases {
			if got[i] != c.want {
				t.Errorf("%s %s: got %s, want %s", mode, c.file, got[i], c.want)
			}
		}
	}
}
