// Why: a second set of fixed std.x509 defects, each kept fixed by a test.
// What: TestX509_ tests: unique ids, OIDs and extensions, the
// 101st signature check, weak intermediates, name order and early times.
// How: certificates made at run time or spliced from a fixture, judged against
// Go's crypto/x509; where std.x509 is stricter, the test names its answer.

package std

import (
	"bytes"
	"crypto"
	"crypto/sha1"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// x509Body is the content of the one DER element der.
func x509Body(t testing.TB, der []byte) []byte {
	t.Helper()
	var rv asn1.RawValue
	rest, err := asn1.Unmarshal(der, &rv)
	if err != nil || len(rest) != 0 {
		t.Fatalf("not one element: %v (%d bytes after)", err, len(rest))
	}
	return rv.Bytes
}

// x509Elems is the elements of b, each whole.
func x509Elems(t testing.TB, b []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(b) > 0 {
		var rv asn1.RawValue
		rest, err := asn1.Unmarshal(b, &rv)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rv.FullBytes)
		b = rest
	}
	return out
}

func x509Cat(xs ...[]byte) []byte {
	var o []byte
	for _, x := range xs {
		o = append(o, x...)
	}
	return o
}

// x509Parts is a certificate split for splicing: the TBSCertificate's
// elements, the outer AlgorithmIdentifier and the signature BIT STRING.
type x509Parts struct {
	tbs      [][]byte
	alg, sig []byte
}

func x509Split(t testing.TB, der []byte) x509Parts {
	t.Helper()
	top := x509Elems(t, x509Body(t, der))
	return x509Parts{tbs: x509Elems(t, x509Body(t, top[0])), alg: top[1], sig: top[2]}
}

func (p x509Parts) clone() x509Parts {
	return x509Parts{tbs: slices.Clone(p.tbs), alg: p.alg, sig: p.sig}
}

// der is the certificate, every length written again.
func (p x509Parts) der() []byte {
	return x509TLV(0x30, x509Cat(x509TLV(0x30, x509Cat(p.tbs...)), p.alg, p.sig))
}

// at is the offset of TBS element i in der().
func (p x509Parts) at(i int) int {
	inner := len(x509Cat(p.tbs...))
	tbs := len(x509TLV(0x30, x509Cat(p.tbs...)))
	whole := tbs + len(p.alg) + len(p.sig)
	off := len(x509TLV(0x30, make([]byte, whole))) - whole + tbs - inner
	for k := 0; k < i; k++ {
		off += len(p.tbs[k])
	}
	return off
}

// insert is p with el before TBS element i.
func (p x509Parts) insert(i int, els ...[]byte) x509Parts {
	q := p.clone()
	q.tbs = slices.Insert(q.tbs, i, els...)
	return q
}

// exts is the extensions of p (its last TBS element is [3]).
func (p x509Parts) exts(t testing.TB) [][]byte {
	return x509Elems(t, x509Body(t, x509Body(t, p.tbs[len(p.tbs)-1])))
}

// withExts is p with exts as its extensions.
func (p x509Parts) withExts(exts [][]byte) x509Parts {
	q := p.clone()
	q.tbs[len(q.tbs)-1] = x509TLV(0xA3, x509TLV(0x30, x509Cat(exts...)))
	return q
}

// x509ExtEl is an Extension element.
func x509ExtEl(oid []byte, critical bool, value []byte) []byte {
	b := x509TLV(0x06, oid)
	if critical {
		b = append(b, 0x01, 0x01, 0xff)
	}
	return x509TLV(0x30, append(b, x509TLV(0x04, value)...))
}

// x509ExtID is the OID content of an Extension element.
func x509ExtID(t testing.TB, e []byte) []byte {
	return x509Body(t, x509Elems(t, x509Body(t, e))[0])
}

// x509ParseCase is one certificate for Parse: goRefuses is asserted
// against Go, so each case tests what it says; want, when set, is the
// prefix of std.x509's own answer, else Go judges.
type x509ParseCase struct {
	name      string
	der       []byte
	goRefuses bool
	want      string
}

// x509RunParse asks both builds and judges each case.
func x509RunParse(t *testing.T, cases []x509ParseCase) {
	t.Helper()
	var recs []string
	for _, c := range cases {
		recs = append(recs, "p "+x509Hex(c.der))
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		gc, err := x509.ParseCertificate(c.der)
		if (err != nil) != c.goRefuses {
			t.Errorf("%s: Go says %v, the case expects a refusal: %v", c.name, err, c.goRefuses)
		}
		switch {
		case c.want != "":
			if !strings.HasPrefix(got[i], c.want) {
				t.Errorf("%s: got %.200s, want %s", c.name, got[i], c.want)
			}
		case err != nil:
			if !strings.HasPrefix(got[i], "err Malformed(offset: ") {
				t.Errorf("%s: Go refuses (%v), std.x509 answers %.200s", c.name, err, got[i])
			}
		default:
			if x509Head(got[i]) != x509Summary(gc) {
				t.Errorf("%s: read differently:\n mk %s\n go %s", c.name, got[i], x509Summary(gc))
			}
		}
	}
}

// TestX509_UniqueIdsPrimitive: an issuerUniqueID [1] or
// subjectUniqueID [2] is a primitive BIT STRING (RFC 5280 4.1.2.8). A
// constructed one, which made std.x509 read extensions Go never reads,
// and a malformed BIT STRING are Malformed at the identifier's first byte;
// well-formed ones read as Go reads them.
func TestX509_UniqueIdsPrimitive(t *testing.T) {
	p := x509Split(t, x509Fixtures(t)["chain.pem"][0])
	i3 := len(p.tbs) - 1
	at := func(q x509Parts, i int) string { return fmt.Sprintf("err Malformed(offset: %d)", q.at(i)) }
	var cases []x509ParseCase
	for _, c := range []struct {
		name string
		els  [][]byte
		bad  int
	}{
		{"constructed [1] before [3] (the finding's)", [][]byte{{0xA1, 0x00}}, 0},
		{"constructed [2] before [3]", [][]byte{{0xA2, 0x00}}, 0},
		{"constructed [1] holding a BIT STRING", [][]byte{{0xA1, 0x04, 0x03, 0x02, 0x00, 0xAB}}, 0},
		{"[1] with 9 unused bits", [][]byte{{0x81, 0x01, 0x09}}, 0},
		{"[1] empty", [][]byte{{0x81, 0x00}}, 0},
		{"[1] with a set padding bit", [][]byte{{0x81, 0x02, 0x03, 0x01}}, 0},
		{"[2] of one byte with 1 unused bit", [][]byte{{0x82, 0x01, 0x01}}, 0},
		{"valid [1], then constructed [2]", [][]byte{{0x81, 0x02, 0x00, 0xAB}, {0xA2, 0x00}}, 1},
		{"[2] before [1]", [][]byte{{0x82, 0x02, 0x00, 0xAB}, {0x81, 0x02, 0x00, 0xCD}}, 1},
	} {
		q := p.insert(i3, c.els...)
		cases = append(cases, x509ParseCase{name: c.name, der: q.der(), want: at(q, i3+c.bad)})
	}
	ok := p.insert(i3, []byte{0x81, 0x02, 0x00, 0xAB}, []byte{0x82, 0x02, 0x04, 0xF0})
	cases = append(cases, x509ParseCase{name: "valid [1] and [2]", der: ok.der()})
	x509RunParse(t, cases)
}

// TestX509_OidsAndExtensionsAsGo: every OBJECT IDENTIFIER
// (extension IDs, key purposes, access methods, the signature's and the
// key's algorithm) is read as Go reads it, and the key identifiers, CRL
// distribution points, certificate policies, policy mappings, policy
// constraints, inhibitAnyPolicy and authority information access are
// validated as Go's parser.go validates them: each certificate Go refuses
// is Malformed, each it reads reads the same.
func TestX509_OidsAndExtensionsAsGo(t *testing.T) {
	fx := x509Fixtures(t)
	p := x509Split(t, fx["chain.pem"][0])
	exts := p.exts(t)
	null := []byte{0x05, 0x00}
	tlv := x509TLV
	add := func(es ...[]byte) []byte {
		return p.withExts(append(slices.Clone(exts), es...)).der()
	}
	swap := func(id []byte, e []byte) []byte {
		out := slices.Clone(exts)
		for i := range out {
			if bytes.Equal(x509ExtID(t, out[i]), id) {
				out[i] = e
				return p.withExts(out).der()
			}
		}
		t.Fatalf("no extension %x", id)
		return nil
	}
	eku, aki := []byte{0x55, 0x1d, 0x25}, []byte{0x55, 0x1d, 0x23}
	ski, crl, pol := []byte{0x55, 0x1d, 0x0e}, []byte{0x55, 0x1d, 0x1f}, []byte{0x55, 0x1d, 0x20}
	maps, pcon, inh := []byte{0x55, 0x1d, 0x21}, []byte{0x55, 0x1d, 0x24}, []byte{0x55, 0x1d, 0x36}
	aia := []byte{0x2b, 0x06, 0x01, 0x05, 0x05, 0x07, 0x01, 0x01}
	server := []byte{0x2b, 0x06, 0x01, 0x05, 0x05, 0x07, 0x03, 0x01}
	ocsp := []byte{0x2b, 0x06, 0x01, 0x05, 0x05, 0x07, 0x30, 0x01}
	oid := func(b []byte) []byte { return tlv(0x06, b) }
	seq := func(xs ...[]byte) []byte { return tlv(0x30, x509Cat(xs...)) }
	// 2.25.2^100: an arc past 2^31, which a certificate policy may have.
	big := append([]byte{0x69, 0x84}, append(bytes.Repeat([]byte{0x80}, 13), 0x00)...)
	policies := func(n int) []byte {
		var ps [][]byte
		for i := 0; i < n; i++ {
			ps = append(ps, seq(oid([]byte{0x2b, 0x06, 0x01, 0x04, 0x01, byte(i)})))
		}
		return seq(ps...)
	}
	cases := []x509ParseCase{
		{name: "extension OID 55 1d 80 0f (the finding's)", der: add(x509ExtEl([]byte{0x55, 0x1d, 0x80, 0x0f}, false, null)), goRefuses: true},
		{name: "extension OID 2b 06 81 (cut off)", der: add(x509ExtEl([]byte{0x2b, 0x06, 0x81}, false, null)), goRefuses: true},
		{name: "extension OID empty", der: add(x509ExtEl(nil, false, null)), goRefuses: true},
		{name: "extension OID with an arc of 2^31", der: add(x509ExtEl([]byte{0x2b, 0x06, 0x88, 0x80, 0x80, 0x80, 0x00}, false, null)), goRefuses: true},
		{name: "extension OID with an arc of 2^31 - 1", der: add(x509ExtEl([]byte{0x2b, 0x06, 0x87, 0xff, 0xff, 0xff, 0x7f}, false, null))},
		{name: "EKU holding 2b 06 81", der: swap(eku, x509ExtEl(eku, false, seq(oid(server), oid([]byte{0x2b, 0x06, 0x81})))), goRefuses: true},
		{name: "EKU holding 2b 06 80 01", der: swap(eku, x509ExtEl(eku, false, seq(oid(server), oid([]byte{0x2b, 0x06, 0x80, 0x01})))), goRefuses: true},
		{name: "EKU holding an unknown purpose", der: swap(eku, x509ExtEl(eku, false, seq(oid([]byte{0x2b, 0x06, 0x01, 0x04, 0x01, 0x7f}), oid(server))))},
		{name: "AIA access method cut off", der: add(x509ExtEl(aia, false, seq(seq(oid([]byte{0x2b, 0x06, 0x81}), tlv(0x86, []byte("http://ocsp.test")))))), goRefuses: true},
		{name: "AIA critical", der: add(x509ExtEl(aia, true, seq(seq(oid(ocsp), tlv(0x86, []byte("http://ocsp.test")))))), goRefuses: true},
		{name: "AIA valid", der: add(x509ExtEl(aia, false, seq(seq(oid(ocsp), tlv(0x86, []byte("http://ocsp.test"))))))},
		{name: "SKI NULL (the finding's)", der: add(x509ExtEl(ski, false, null)), goRefuses: true},
		{name: "SKI critical", der: add(x509ExtEl(ski, true, tlv(0x04, []byte{1, 2, 3, 4}))), goRefuses: true},
		{name: "SKI valid", der: add(x509ExtEl(ski, false, tlv(0x04, []byte{1, 2, 3, 4})))},
		{name: "AKI NULL", der: swap(aki, x509ExtEl(aki, false, null)), goRefuses: true},
		{name: "AKI critical", der: swap(aki, x509ExtEl(aki, true, seq(tlv(0x80, []byte{1, 2, 3, 4})))), goRefuses: true},
		{name: "AKI with a constructed [0]", der: swap(aki, x509ExtEl(aki, false, seq(tlv(0xA0, tlv(0x04, []byte{1, 2})))))},
		{name: "CRLDP NULL (the finding's)", der: add(x509ExtEl(crl, false, null)), goRefuses: true},
		{name: "CRLDP point NULL", der: add(x509ExtEl(crl, false, seq(null))), goRefuses: true},
		{name: "CRLDP nameRelativeToCRLIssuer", der: add(x509ExtEl(crl, false, seq(seq(tlv(0xA0, tlv(0xA1, seq(oid([]byte{0x55, 0x04, 0x03}), tlv(0x13, []byte("x"))))))))), goRefuses: true},
		{name: "CRLDP fullName URI", der: add(x509ExtEl(crl, false, seq(seq(tlv(0xA0, tlv(0xA0, tlv(0x86, []byte("http://crl.test/a.crl"))))))))},
		{name: "CRLDP primitive [0]", der: add(x509ExtEl(crl, false, seq(seq(tlv(0x80, []byte("x"))))))},
		{name: "CRLDP empty point", der: add(x509ExtEl(crl, false, seq(seq())))},
		{name: "policies NULL", der: add(x509ExtEl(pol, false, null)), goRefuses: true},
		{name: "policy OID 2b 06 80 01", der: add(x509ExtEl(pol, false, seq(seq(oid([]byte{0x2b, 0x06, 0x80, 0x01}))))), goRefuses: true},
		{name: "policy OID cut off", der: add(x509ExtEl(pol, false, seq(seq(oid([]byte{0x2b, 0x86}))))), goRefuses: true},
		{name: "policy OID empty", der: add(x509ExtEl(pol, false, seq(seq(oid(nil))))), goRefuses: true},
		{name: "policy not an OID", der: add(x509ExtEl(pol, false, seq(seq(null)))), goRefuses: true},
		{name: "policy twice", der: add(x509ExtEl(pol, false, seq(seq(oid([]byte{0x55, 0x1d, 0x20, 0x00})), seq(oid([]byte{0x55, 0x1d, 0x20, 0x00}))))), goRefuses: true},
		{name: "policy OID 2.25.2^100", der: add(x509ExtEl(pol, false, seq(seq(oid(big)))))},
		{name: "policy with a CPS qualifier", der: add(x509ExtEl(pol, false, seq(seq(oid([]byte{0x55, 0x1d, 0x20, 0x00}), seq(seq(oid([]byte{0x2b, 0x06, 0x01, 0x05, 0x05, 0x07, 0x02, 0x01}), tlv(0x16, []byte("http://cps.test"))))))))},
		{name: "64 policies", der: add(x509ExtEl(pol, false, policies(64)))},
		{name: "65 policies (std.x509's bound)", der: add(x509ExtEl(pol, false, policies(65))), want: "err Malformed(offset: "},
		{name: "policy mappings NULL", der: add(x509ExtEl(maps, false, null)), goRefuses: true},
		{name: "policy mapping of one OID", der: add(x509ExtEl(maps, false, seq(seq(oid([]byte{0x2b, 0x06, 0x01}))))), goRefuses: true},
		{name: "policy mapping", der: add(x509ExtEl(maps, false, seq(seq(oid([]byte{0x2b, 0x06, 0x01}), oid([]byte{0x2b, 0x06, 0x02})))))},
		{name: "policy constraints NULL", der: add(x509ExtEl(pcon, false, null)), goRefuses: true},
		{name: "policy constraints non-minimal [0]", der: add(x509ExtEl(pcon, false, seq(tlv(0x80, []byte{0x00, 0x01})))), goRefuses: true},
		{name: "policy constraints [1] of 9 bytes", der: add(x509ExtEl(pcon, false, seq(tlv(0x81, []byte{0x01, 0, 0, 0, 0, 0, 0, 0, 0})))), goRefuses: true},
		{name: "policy constraints", der: add(x509ExtEl(pcon, false, seq(tlv(0x80, []byte{0x00}), tlv(0x81, []byte{0x02}))))},
		{name: "policy constraints critical (unhandled: no policy processing)", der: add(x509ExtEl(pcon, true, seq(tlv(0x80, []byte{0x00}))))},
		{name: "inhibitAnyPolicy NULL", der: add(x509ExtEl(inh, false, null)), goRefuses: true},
		{name: "inhibitAnyPolicy non-minimal", der: add(x509ExtEl(inh, false, tlv(0x02, []byte{0x00, 0x01}))), goRefuses: true},
		{name: "inhibitAnyPolicy", der: add(x509ExtEl(inh, false, tlv(0x02, []byte{0x00})))},
	}
	// The signature's algorithm, in both places, and the key's.
	leaf := fx["chain.pem"][0]
	ecdsa384 := []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x04, 0x03, 0x03}
	ecKey := []byte{0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01}
	if bytes.Count(leaf, ecdsa384) != 2 || bytes.Count(leaf, ecKey) != 1 {
		t.Fatalf("chain.pem's leaf: %d signature OIDs, %d key OIDs", bytes.Count(leaf, ecdsa384), bytes.Count(leaf, ecKey))
	}
	cases = append(cases,
		x509ParseCase{name: "signature algorithm OID with an arc 80 03", der: bytes.ReplaceAll(leaf, ecdsa384, []byte{0x2a, 0x86, 0x48, 0xce, 0x3d, 0x80, 0x03, 0x03}), goRefuses: true},
		x509ParseCase{name: "key algorithm OID with an arc 80 01", der: bytes.Replace(leaf, ecKey, []byte{0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x80, 0x01}, 1), goRefuses: true},
	)
	// RSASSA-PSS parameters (pss-leaf: [0] hash, [1] MGF1, [2] salt).
	pp := x509Split(t, fx["pss-leaf.pem"][0])
	ai := x509Elems(t, x509Body(t, pp.alg))
	params := x509Elems(t, x509Body(t, ai[1]))
	if len(params) != 3 || !bytes.Equal(pp.tbs[2], pp.alg) {
		t.Fatalf("pss-leaf: %d parameters", len(params))
	}
	pss := func(f func(ps [][]byte) [][]byte) []byte {
		ps := f(slices.Clone(params))
		a := x509TLV(0x30, x509Cat(ai[0], x509TLV(0x30, x509Cat(ps...))))
		q := pp.clone()
		q.tbs[2], q.alg = a, a
		return q.der()
	}
	retag := func(k int, tag byte) func([][]byte) [][]byte {
		return func(ps [][]byte) [][]byte {
			ps[k] = append([]byte{tag}, ps[k][1:]...)
			return ps
		}
	}
	sha256Ai := seq(oid([]byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01}), null)
	cases = append(cases,
		x509ParseCase{name: "PSS as made", der: pss(func(ps [][]byte) [][]byte { return ps })},
		x509ParseCase{name: "PSS primitive [0] (the finding's)", der: pss(retag(0, 0x80))},
		x509ParseCase{name: "PSS primitive [1]", der: pss(retag(1, 0x81))},
		x509ParseCase{name: "PSS primitive [2]", der: pss(retag(2, 0x82))},
		x509ParseCase{name: "PSS constructed [3] of 1", der: pss(func(ps [][]byte) [][]byte { return append(ps, tlv(0xA3, []byte{0x02, 0x01, 0x01})) })},
		x509ParseCase{name: "PSS primitive [3] (Go skips it)", der: pss(func(ps [][]byte) [][]byte { return append(ps, tlv(0x83, []byte{0x02, 0x01, 0x01})) }), want: "err Malformed(offset: "},
		x509ParseCase{name: "PSS hash OID cut off (unknown to both)", der: pss(func(ps [][]byte) [][]byte {
			ps[0] = tlv(0xA0, seq(oid([]byte{0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x81}), null))
			return ps
		})},
		x509ParseCase{name: "PSS MGF1 OID cut off (unknown to both)", der: pss(func(ps [][]byte) [][]byte {
			ps[1] = tlv(0xA1, seq(oid([]byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x88}), sha256Ai))
			return ps
		})},
	)
	x509RunParse(t, cases)
}

// TestX509_TooComplexAt101: Verify gives up with TooComplex at
// the 101st signature check of one chain, as Go's
// maxChainSignatureChecks. Every wrong candidate is a valid CA with the
// real issuer's name and key identifier and a key of its own, so Go
// tries them in the chain's order too.
func TestX509_TooComplexAt101(t *testing.T) {
	root := x509Make(t, x509CA("Mk Budget Root"), x509P256(t), nil)
	kid := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	it := x509CA("Mk Budget Issuer")
	it.SubjectKeyId = kid
	issuer := x509Make(t, it, x509P256(t), root)
	leaf := x509Make(t, x509Leaf("budget.test", []string{"budget.test"}, nil), x509P256(t), issuer)
	var wrong [][]byte
	for i := 0; i < 101; i++ {
		w := x509CA("Mk Budget Issuer")
		w.SubjectKeyId = kid
		wrong = append(wrong, x509Make(t, w, x509P256(t), root).der)
	}
	chain := func(n int, real bool) [][]byte {
		c := append([][]byte{leaf.der}, wrong[:n]...)
		if real {
			c = append(c, issuer.der)
		}
		return c
	}
	cases := []struct {
		name  string
		chain [][]byte
		want  string
		limit bool // Go answers with its signature check limit
		goOk  bool
	}{
		{"101 wrong candidates", chain(101, false), "err TooComplex", true, false},
		{"100 wrong candidates (no 101st check)", chain(100, false), "err BadSignature", false, false},
		{"100 wrong, then the issuer", chain(100, true), "err TooComplex", true, false},
		{"99 wrong, then the issuer (the root's check is the 101st)", chain(99, true), "err TooComplex", true, false},
		{"98 wrong, then the issuer (exactly 100 checks)", chain(98, true), "ok", false, true},
	}
	var recs []string
	for _, c := range cases {
		recs = append(recs, x509VerifyRecord(x509Now, "budget.test", c.chain, [][]byte{root.der}))
		err := x509GoVerify(c.chain, [][]byte{root.der}, "budget.test", x509Now)
		if (err == nil) != c.goOk || (err != nil && strings.Contains(err.Error(), "signature check attempts limit") != c.limit) {
			t.Errorf("%s: Go says %v", c.name, err)
		}
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got[i], c.want)
		}
	}
}

// TestX509_PlatformRefusesWeakIntermediate: against the
// system's pool on Windows, an intermediate signed with SHA-1 on the
// platform's path is Unsupported (the platform's weak-signature bit),
// as with the program's own pool and as Go refuses it; a weak
// certificate the path does not check, and a self-issued SHA-1 root, are
// left to the rest of the platform's verdict.
func TestX509_PlatformRefusesWeakIntermediate(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the platform's path is Windows'")
	}
	sha1Rsa := []byte{0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x05}
	root := x509Make(t, x509CA("Mk Weak Root"), x509Rsa(t), nil)
	inter := x509Make(t, x509CA("Mk Weak Intermediate"), x509Rsa(t), root)
	weakInter := x509Resign(t, inter, root, sha1Rsa, crypto.SHA1)
	weakRoot := x509Resign(t, root, root, sha1Rsa, crypto.SHA1)
	leaf := x509Make(t, x509Leaf("weak.test", []string{"weak.test"}, nil), x509P256(t), inter)
	if err := x509GoVerify([][]byte{leaf.der, weakInter}, [][]byte{root.der}, "weak.test", x509Now); err == nil {
		t.Fatal("Go accepts a SHA-1 intermediate")
	}
	if err := x509GoVerify([][]byte{leaf.der, inter.der}, [][]byte{root.der}, "weak.test", x509Now); err != nil {
		t.Fatalf("Go refuses the control: %v", err)
	}
	cases := []struct{ rec, want string }{
		// The platform's own path flags the SHA-1 intermediate
		// (CERT_TRUST_HAS_WEAK_SIGNATURE), Unsupported before its
		// untrusted root is named.
		{x509CachedRecord(x509Now, "weak.test", [][]byte{leaf.der, weakInter, root.der}), "err Unsupported"},
		// Without its root no path checks its signature (a partial
		// chain), and a weak copy beside the SHA-256 one is not on the
		// path: refused only for the root no store holds.
		{x509CachedRecord(x509Now, "weak.test", [][]byte{leaf.der, weakInter}), "err UnknownAuthority"},
		{x509CachedRecord(x509Now, "weak.test", [][]byte{leaf.der, inter.der, weakInter, root.der}), "err UnknownAuthority"},
		{x509VerifyRecord(x509Now, "weak.test", [][]byte{leaf.der, weakInter}, [][]byte{root.der}), "err Unsupported"},
		{x509VerifyRecord(x509Now, "weak.test", [][]byte{leaf.der, inter.der}, [][]byte{root.der}), "ok"},
		// Controls: the test root is not trusted by this machine.
		{x509CachedRecord(x509Now, "weak.test", [][]byte{leaf.der, inter.der, weakRoot}), "err UnknownAuthority"},
		{x509CachedRecord(x509Now, "weak.test", [][]byte{leaf.der, inter.der}), "err UnknownAuthority"},
	}
	var recs []string
	for _, c := range cases {
		recs = append(recs, c.rec)
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("record %d: got %s, want %s", i, got[i], c.want)
		}
	}
}

// TestX509_PlatformTrustBits: the platform's path also reads the
// chain's trust bits, but only behind a passing SSL policy verdict, so a
// refusal keeps the kind the policy names (the kind the program's own pool
// gives): Go's recorded www.google.com chain with one signature byte of
// its leaf flipped is BadSignature, the chain long after its expiry is
// Expired, and the chain as recorded is ok. Windows, with GTS Root R1 in
// the ROOT store.
func TestX509_PlatformTrustBits(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the platform's path is Windows'")
	}
	held := false
	for _, d := range rt8StoreRoots(t) {
		if s := x509Sha1Hex(d); s == x509GtsRootR1 {
			held = true
		}
	}
	if !held {
		t.Skipf("this host's ROOT store does not hold GTS Root R1 (%s)", x509GtsRootR1)
	}
	chain := x509Fixtures(t)["google-chain.pem"][:2]
	bad := slices.Clone(chain[0])
	bad[len(bad)-1] ^= 0x01
	at := time.Unix(1677615892, 0)
	cases := []struct{ rec, want string }{
		{x509CachedRecord(at, "www.google.com", chain), "ok"},
		{x509CachedRecord(at, "www.google.com", [][]byte{bad, chain[1]}), "err BadSignature"},
		{x509CachedRecord(time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC), "www.google.com", chain), "err Expired"},
	}
	var recs []string
	for _, c := range cases {
		recs = append(recs, c.rec)
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("record %d: got %s, want %s", i, got[i], c.want)
		}
	}
}

// x509Sha1Hex is the SHA-1 thumbprint of der in lower-case hex.
func x509Sha1Hex(der []byte) string {
	s := sha1.Sum(der)
	return hex.EncodeToString(s[:])
}

// x509SubjectSeq is the "subject" lines of out, in order.
func x509SubjectSeq(out string) []string {
	var s []string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if v, ok := strings.CutPrefix(l, "subject "); ok {
			s = append(s, v)
		}
	}
	return s
}

// TestX509_SystemRootsNameOrderLinux: on x86_64-linux,
// SSL_CERT_DIR on tmpfs holding c.pem, a.pem and b.pem, made in that order
// (tmpfs lists them in creation order or its reverse, never sorted), gives
// the pool in name order, the order of Go's SystemCertPool. MK_LINUX=1
// only, under the WSL lock with -p 1.
func TestX509_SystemRootsNameOrderLinux(t *testing.T) {
	if !linuxRun() {
		t.Skip("MK_LINUX=1 runs the x86_64-linux leaf (under the WSL lock, -p 1)")
	}
	exe := linuxIo.build(t, map[string]string{"x509/tests_x509fx.mk": x509Fixture}, x509CancelSrc(t))
	dir := filepath.Dir(exe)
	gox := x509BuildGoRoots(t, dir)
	d := fmt.Sprintf("/dev/shm/mk-x509-order-%d", os.Getpid())
	env := []string{"SSL_CERT_FILE=/nonexistent/mk-x509", "SSL_CERT_DIR=" + d}
	mk := linuxIo.runMany(t, exe, []ioCall{
		{env: []string{"MK_X509_MKORDER=" + d}, watch: 60},
		{env: append([]string{"MK_X509_ROOTS=1"}, env...), watch: 60},
	})
	gor := linuxIo.runMany(t, gox, []ioCall{{env: env, watch: 60}})
	linuxIo.runMany(t, exe, []ioCall{{env: []string{"MK_X509_RMDIR=" + d}, watch: 60}})
	if mk[0].code != 0 || strings.TrimSpace(mk[0].stdout) != "order made" {
		t.Fatalf("making the directory: exit %d\n%s%s", mk[0].code, mk[0].stdout, mk[0].stderr)
	}
	if mk[1].code != 0 || gor[0].code != 0 {
		t.Fatalf("exit %d (mk), %d (go)\n%s%s", mk[1].code, gor[0].code, mk[1].stderr, gor[0].stderr)
	}
	fx := x509Fixtures(t)
	var want []string
	for _, f := range []string{"rsa-root.pem", "ed-root.pem", "root.pem"} {
		want = append(want, x509NameText(x509MustParse(t, fx[f][0]).RawSubject))
	}
	m, g := x509SubjectSeq(mk[1].stdout), x509SubjectSeq(gor[0].stdout)
	if !slices.Equal(g, want) {
		t.Errorf("Go's order %q, want %q", g, want)
	}
	if !slices.Equal(m, g) {
		t.Errorf("std.x509's order %q, Go's %q", m, g)
	}
}

// x509BuildGoRoots builds the Go oracle of the system's pool for linux in
// dir.
func x509BuildGoRoots(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "gox.go"), []byte(x509GoRoots), 0o644); err != nil {
		t.Fatal(err)
	}
	gox := filepath.Join(dir, "gox")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", gox, "gox.go")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build of the oracle for linux: %v\n%s", err, out)
	}
	return gox
}

// TestX509_EarlyTimesSaturate: a validity time before
// time.Time's range (1600, and year 0) saturates to its first instant,
// as Go's value saturated to that range; Parse never fails on it and the
// chain verifies at 2024.
func TestX509_EarlyTimesSaturate(t *testing.T) {
	root := x509Make(t, x509CA("Mk Early Root"), x509P256(t), nil)
	first := x509Unix(time.Unix(0, math.MinInt64))
	var recs []string
	var certs []*x509.Certificate
	for _, y := range []int{1600, 0} {
		lt := x509Leaf("early.test", []string{"early.test"}, nil)
		lt.NotBefore = time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		m := x509Make(t, lt, x509P256(t), root)
		stamp := fmt.Sprintf("%04d0101000000Z", y)
		if !bytes.Contains(m.der, []byte(stamp)) {
			t.Fatalf("year %d: no GeneralizedTime %s in the certificate", y, stamp)
		}
		if x509Unix(m.cert.NotBefore) != first {
			t.Fatalf("year %d: the model saturates to %d, want %d", y, x509Unix(m.cert.NotBefore), first)
		}
		certs = append(certs, m.cert)
		recs = append(recs, "p "+x509Hex(m.der), x509VerifyRecord(x509Now, "early.test", [][]byte{m.der}, [][]byte{root.der}))
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range certs {
		p, v := got[2*i], got[2*i+1]
		if x509Head(p) != x509Summary(c) {
			t.Errorf("certificate %d:\n mk %s\n go %s", i, p, x509Summary(c))
		}
		if f := strings.Split(p, "|"); len(f) < 4 || f[3] != fmt.Sprint(first) {
			t.Errorf("certificate %d: %.200s, want NotBefore %d", i, p, first)
		}
		if v != "ok" {
			t.Errorf("certificate %d: Verify %s, want ok", i, v)
		}
	}
}
