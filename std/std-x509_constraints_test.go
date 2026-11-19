// Why: each fixed std.x509 defect gets a test that keeps it fixed.
// What: TestX509_ tests: name constraints, URI hosts, mailboxes,
// Ed25519 parameters, long chains, weak signatures and read caps.
// How: chains made at run time and judged against Go's crypto/x509; name
// constraints also against x509ModelAllows, a naive quadratic model.

package std

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

var x509Now = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// x509TLV is the DER element of tag and content, any length.
func x509TLV(tag byte, content []byte) []byte {
	n := len(content)
	if n < 128 {
		return append([]byte{tag, byte(n)}, content...)
	}
	var l []byte
	for m := n; m > 0; m >>= 8 {
		l = append([]byte{byte(m)}, l...)
	}
	out := append([]byte{tag, 0x80 | byte(len(l))}, l...)
	return append(out, content...)
}

// x509Gn is one GeneralName: its context tag (1 email, 2 DNS, 6 URI, 7
// IP) and its raw content.
type x509Gn struct {
	tag byte
	v   []byte
}

// x509RawSAN is a subjectAltName extension holding names as given, so a
// value Go would not write (a URI with a '\', a quoted mailbox) is in it.
func x509RawSAN(names []x509Gn) pkix.Extension {
	var body []byte
	for _, g := range names {
		body = append(body, x509TLV(0x80|g.tag, g.v)...)
	}
	return pkix.Extension{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: x509TLV(0x30, body)}
}

// x509Answers asks the driver and returns the answers; mode "" asks both
// builds and fails unless they agree.
func x509Answers(t *testing.T, modes []string, records []string) []string {
	t.Helper()
	var first []string
	for _, m := range modes {
		got := x509Ask(t, m, records)
		if first == nil {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] {
				t.Errorf("record %d: %s says %s, %s says %s", i, modes[0], first[i], m, got[i])
			}
		}
	}
	return first
}

// x509Chain is root -> inter -> leaf at run time: inter carries the
// template's constraints, leaf the raw names. Go's CreateCertificate
// parses what it made, so a URI it would refuse is written as a
// placeholder of the same length, then put in the TBSCertificate, which
// the intermediate's key signs again.
func x509Chain(t *testing.T, root *x509Made, inter *x509.Certificate, names []x509Gn) (leafDer, interDer []byte) {
	t.Helper()
	in := x509Make(t, inter, x509P256(t), root)
	lt := x509Leaf("leaf.test", nil, nil)
	held := slices.Clone(names)
	var swaps [][2][]byte
	for i, g := range held {
		if g.tag == 6 && len(g.v) >= 5 {
			ph := []byte("u://" + strings.Repeat("q", len(g.v)-4))
			if !bytes.Equal(ph, g.v) {
				swaps = append(swaps, [2][]byte{ph, g.v})
				held[i] = x509Gn{6, ph}
			}
		}
	}
	lt.ExtraExtensions = []pkix.Extension{x509RawSAN(held)}
	leaf := x509Make(t, lt, x509P256(t), in)
	if len(swaps) == 0 {
		return leaf.der, in.der
	}
	var cert struct {
		TBS asn1.RawValue
		Alg asn1.RawValue
		Sig asn1.BitString
	}
	if _, err := asn1.Unmarshal(leaf.der, &cert); err != nil {
		t.Fatal(err)
	}
	tbs := slices.Clone(cert.TBS.FullBytes)
	for _, sw := range swaps {
		tbs = bytes.Replace(tbs, sw[0], sw[1], 1)
	}
	h := sha256.Sum256(tbs)
	sig, err := ecdsa.SignASN1(crand.Reader, in.key.(*ecdsa.PrivateKey), h[:])
	if err != nil {
		t.Fatal(err)
	}
	body := append(append(tbs, cert.Alg.FullBytes...), x509TLV(0x03, append([]byte{0}, sig...))...)
	return x509TLV(0x30, body), in.der
}

// TestX509_UntrustedConstraintsCostNothing: a self-signed
// intermediate the peer sends, excluding 40,000 IP ranges, over a leaf
// with 4,000 DNS names it signed, and the pool an unrelated root. The
// constraints are never walked (they bind only a path a root closes):
// the answer is UnknownAuthority, and BadSignature with the leaf's last
// byte flipped. Ten records stay far under the bound, and under
// MK_TIMING=1 each stays under 100 ms.
func TestX509_UntrustedConstraintsCostNothing(t *testing.T) {
	root := x509Make(t, x509CA("Mk Unrelated Root"), x509P256(t), nil)
	build := func(ranges, names int) (leaf, flipped, forged []byte) {
		ft := x509CA("Forged Inter")
		ft.ExcludedDNSDomains = []string{"zzz.invalid"}
		for i := 0; i < ranges; i++ {
			ft.ExcludedIPRanges = append(ft.ExcludedIPRanges, &net.IPNet{IP: net.IPv4(10, byte(i>>16), byte(i>>8), byte(i)).To4(), Mask: net.CIDRMask(32, 32)})
		}
		f := x509Make(t, ft, x509P256(t), nil)
		lt := x509Leaf("h0.test", nil, nil)
		for i := 0; i < names; i++ {
			lt.DNSNames = append(lt.DNSNames, fmt.Sprintf("h%d.test", i))
		}
		l := x509Make(t, lt, x509P256(t), f)
		fl := slices.Clone(l.der)
		fl[len(fl)-1] ^= 1
		return l.der, fl, f.der
	}
	pool := [][]byte{root.der}
	// Small, in both builds.
	leaf, flipped, forged := build(500, 50)
	recs := []string{
		x509LimboRecord(x509Now, "h0.test", [][]byte{leaf, forged}, pool),
		x509LimboRecord(x509Now, "h0.test", [][]byte{flipped, forged}, pool),
	}
	got := x509Answers(t, x509Modes, recs)
	if got[0] != "err UnknownAuthority" || got[1] != "err BadSignature" {
		t.Errorf("small: got %q, want UnknownAuthority and BadSignature", got)
	}
	for _, c := range [][][]byte{{leaf, forged}, {flipped, forged}} {
		if x509GoVerify(c, pool, "h0.test", x509Now) == nil {
			t.Errorf("Go accepts the forged chain")
		}
	}
	// The full size, release only, ten records.
	leaf, flipped, forged = build(40000, 4000)
	t.Logf("leaf %d bytes, forged intermediate %d bytes", len(leaf), len(forged))
	recs = nil
	for i := 0; i < 5; i++ {
		recs = append(recs, x509LimboRecord(x509Now, "h0.test", [][]byte{leaf, forged}, pool), x509LimboRecord(x509Now, "h0.test", [][]byte{flipped, forged}, pool))
	}
	x509Exe(t, "release")
	start := time.Now()
	got = x509Ask(t, "release", recs)
	took := time.Since(start)
	for i, g := range got {
		want := "err UnknownAuthority"
		if i%2 == 1 {
			want = "err BadSignature"
		}
		if g != want {
			t.Errorf("record %d: got %s, want %s", i, g, want)
		}
	}
	t.Logf("10 records (hex decode, parse of a 480 KB intermediate, Verify): %s (release, loaded machine; not a measurement)", took)
	if took > 15*time.Second {
		t.Errorf("10 records took %s: the constraints are walked again", took)
	}
	if os.Getenv("MK_TIMING") == "1" && took > time.Second {
		t.Errorf("10 records took %s, want under 1 s (100 ms each)", took)
	}
}

// TestX509_ManySubtreesAgreeWithGo: legitimately signed chains
// whose intermediate permits 1,100 and 10,000 DNS subtrees, over leaves
// with 1,000 and 10,000 names, verify as Go verifies them (ok); the
// quadratic scan answered TooComplex. A name outside them is still
// BadUsage.
func TestX509_ManySubtreesAgreeWithGo(t *testing.T) {
	root := x509Make(t, x509CA("Mk Test Root 6"), x509P256(t), nil)
	type tc struct {
		subtrees, names int
		extra           string
		want            string
	}
	cases := []tc{{1100, 1000, "", "ok"}, {10000, 10000, "", "ok"}, {1100, 1000, "outside.test", "err BadUsage"}}
	var recs []string
	for _, c := range cases {
		it := x509CA("Mk Constrained 6")
		for i := 0; i < c.subtrees; i++ {
			it.PermittedDNSDomains = append(it.PermittedDNSDomains, fmt.Sprintf("d%05d.test", i))
		}
		in := x509Make(t, it, x509P256(t), root)
		last := fmt.Sprintf("d%05d.test", c.subtrees-1)
		lt := x509Leaf("h0."+last, nil, nil)
		for i := 0; i < c.names; i++ {
			lt.DNSNames = append(lt.DNSNames, fmt.Sprintf("h%d.%s", i, last))
		}
		if c.extra != "" {
			lt.DNSNames = append(lt.DNSNames, c.extra)
		}
		leaf := x509Make(t, lt, x509P256(t), in)
		chain := [][]byte{leaf.der, in.der}
		if (x509GoVerify(chain, [][]byte{root.der}, "h0."+last, x509Now) == nil) != (c.want == "ok") {
			t.Errorf("%d x %d: Go disagrees with %s", c.subtrees, c.names, c.want)
		}
		recs = append(recs, x509LimboRecord(x509Now, "h0."+last, chain, [][]byte{root.der}))
	}
	start := time.Now()
	got := x509Answers(t, []string{"release"}, recs)
	t.Logf("3 records: %s (release, loaded machine; not a measurement)", time.Since(start))
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("%d subtrees x %d names %q: got %s, want %s", c.subtrees, c.names, c.extra, got[i], c.want)
		}
	}
}

// x509NcCase is one constrained chain of the table tests: the
// intermediate's constraints, the leaf's names, and std.x509's verdict.
type x509NcCase struct {
	name  string
	inter func(*x509.Certificate)
	names []x509Gn
	want  string
	// stricter is why std.x509 refuses what Go accepts, "" when it agrees.
	stricter string
}

// x509RunNc builds every case, checks the want against Go (std.x509 must
// refuse whatever Go refuses, and wants ok only where Go does), and runs
// both builds.
func x509RunNc(t *testing.T, cases []x509NcCase) {
	t.Helper()
	root := x509Make(t, x509CA("Mk Test Root Nc"), x509P256(t), nil)
	var recs []string
	for _, c := range cases {
		it := x509CA("Mk Constrained Nc")
		c.inter(it)
		names := append([]x509Gn{{2, []byte("ok.test")}}, c.names...)
		leaf, inter := x509Chain(t, root, it, names)
		chain := [][]byte{leaf, inter}
		goErr := x509GoVerify(chain, [][]byte{root.der}, "ok.test", x509Now)
		if (goErr == nil) != (c.want == "ok") && !(c.stricter != "" && goErr == nil) {
			t.Errorf("%s: Go says %v, the case wants %s", c.name, goErr, c.want)
		}
		recs = append(recs, x509LimboRecord(x509Now, "ok.test", chain, [][]byte{root.der}))
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		want := c.want
		if want != "ok" {
			want = "err " + want
		}
		if got[i] != want {
			t.Errorf("%s: got %s, want %s", c.name, got[i], want)
		}
	}
}

func x509ExURI(d ...string) func(*x509.Certificate) {
	return func(c *x509.Certificate) { c.ExcludedURIDomains = d }
}

func x509ExMail(d ...string) func(*x509.Certificate) {
	return func(c *x509.Certificate) { c.ExcludedEmailAddresses = d }
}

// TestX509_UriHostsReadAlike: a URI SAN under an excluded URI
// subtree is matched only when every URL reader reads the same host; a
// percent-encoded host, a '\' in the userinfo, a tab, a second '@', a bad
// port or a missing "//" is refused (Go refuses each at parse or at the
// check).
func TestX509_UriHostsReadAlike(t *testing.T) {
	uri := func(s string) []x509Gn { return []x509Gn{{6, []byte(s)}} }
	x509RunNc(t, []x509NcCase{
		{"percent-encoded host", x509ExURI("bad.com"), uri("https://bad%2ecom/"), "BadUsage", ""},
		{"backslash userinfo", x509ExURI("good.com"), uri(`https://good.com\@bad.com/`), "BadUsage", ""},
		{"excluded host", x509ExURI("bad.com"), uri("https://bad.com/"), "BadUsage", ""},
		{"excluded host upper case", x509ExURI("bad.com"), uri("https://BAD.com/x"), "BadUsage", ""},
		{"other host", x509ExURI("bad.com"), uri("https://ok.com/"), "ok", ""},
		{"userinfo and port", x509ExURI("bad.com"), uri("https://user:pw@ok.com:443/x?y#z"), "ok", ""},
		{"excluded behind userinfo", x509ExURI("bad.com"), uri("https://ok.com@bad.com/"), "BadUsage", ""},
		{"bad port", x509ExURI("bad.com"), uri("https://ok.com:44a/"), "BadUsage", ""},
		{"one slash", x509ExURI("bad.com"), uri("https:/bad.com/"), "BadUsage", ""},
		{"two at signs", x509ExURI("bad.com"), uri("https://a@b@bad.com/"), "BadUsage", ""},
		{"tab", x509ExURI("bad.com"), uri("https://ok.com\t/"), "BadUsage", ""},
		{"no authority", x509ExURI("bad.com"), uri("urn:bad.com"), "BadUsage", ""},
		{"ip literal", x509ExURI("bad.com"), uri("https://[::1]/"), "BadUsage", ""},
		{"ipv4 host", x509ExURI("bad.com"), uri("https://1.2.3.4/"), "BadUsage", ""},
		{"subdomain under a dotted constraint", x509ExURI(".bad.com"), uri("https://a.bad.com/"), "BadUsage", ""},
		{name: "numeric last label", inter: x509ExURI("bad.com"), names: uri("https://0x7f.1/"), want: "BadUsage", stricter: "the URL Standard reads 0x7f.1 as IPv4 127.0.0.1 where Go reads a name"},
		{name: "decimal host", inter: x509ExURI("bad.com"), names: uri("https://2130706433/"), want: "BadUsage", stricter: "the URL Standard reads 2130706433 as IPv4 127.0.0.1 where Go reads a name"},
	})
}

// TestX509_QuotedMailboxes: an exact excluded mailbox catches the
// same mailbox quoted or escaped, as Go's RFC 2821 reading does; the
// local part stays case-sensitive and the domain does not; a name Go
// cannot read as a mailbox is refused.
func TestX509_QuotedMailboxes(t *testing.T) {
	mail := func(s string) []x509Gn { return []x509Gn{{1, []byte(s)}} }
	ex := x509ExMail("foo@example.com")
	x509RunNc(t, []x509NcCase{
		{"quoted", ex, mail(`"foo"@example.com`), "BadUsage", ""},
		{"escaped", ex, mail(`fo\o@example.com`), "BadUsage", ""},
		{"quoted with an escape", ex, mail(`"fo\o"@example.com`), "BadUsage", ""},
		{"plain", ex, mail("foo@example.com"), "BadUsage", ""},
		{"domain upper case", ex, mail("foo@EXAMPLE.com"), "BadUsage", ""},
		{"local upper case", ex, mail("Foo@example.com"), "ok", ""},
		{"other local", ex, mail("bar@example.com"), "ok", ""},
		{"two at signs", ex, mail("a@b@example.com"), "BadUsage", ""},
		{"leading dot", ex, mail(".foo@example.com"), "BadUsage", ""},
		{"double dot", ex, mail("f..oo@example.com"), "BadUsage", ""},
		{"quoted mailbox constraint", x509ExMail(`"foo"@example.com`), mail("foo@example.com"), "BadUsage", ""},
		{"domain constraint", x509ExMail("example.com"), mail(`"x y"@example.com`), "BadUsage", ""},
		{"subdomain constraint, exact domain", x509ExMail(".example.com"), mail("foo@example.com"), "ok", ""},
	})
}

// x509EdNullChain is an Ed25519 root, a leaf it signs (ed.test), and that
// leaf with both AlgorithmIdentifiers given NULL parameters, its TBS
// signed again by the root's key.
func x509EdNullChain(t testing.TB) (bad, good, root []byte) {
	t.Helper()
	rpub, rkey, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	lpub, _, err := ed25519.GenerateKey(crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rt := x509CA("Mk Ed Null Root")
	rt.SerialNumber, rt.NotBefore, rt.NotAfter = big.NewInt(5001), x509From, x509To
	root, err = x509.CreateCertificate(crand.Reader, rt, rt, rpub, rkey)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := x509.ParseCertificate(root)
	if err != nil {
		t.Fatal(err)
	}
	lt := x509Leaf("ed.test", []string{"ed.test"}, nil)
	lt.SerialNumber, lt.NotBefore, lt.NotAfter = big.NewInt(5002), x509From, x509To
	good, err = x509.CreateCertificate(crand.Reader, lt, rc, lpub, rkey)
	if err != nil {
		t.Fatal(err)
	}
	lc, err := x509.ParseCertificate(good)
	if err != nil {
		t.Fatal(err)
	}
	bare := []byte{0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70}
	withNull := []byte{0x30, 0x07, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x05, 0x00}
	var tbs asn1.RawValue
	if _, err := asn1.Unmarshal(lc.RawTBSCertificate, &tbs); err != nil {
		t.Fatal(err)
	}
	// The signature field comes before the subject's key, so the first
	// match is it.
	newTbs := x509TLV(0x30, bytes.Replace(tbs.Bytes, bare, withNull, 1))
	sig := ed25519.Sign(rkey, newTbs)
	bad = x509TLV(0x30, append(append(append([]byte{}, newTbs...), withNull...), x509TLV(0x03, append([]byte{0}, sig...))...))
	return bad, good, root
}

// x509EdNull is x509EdNullChain's refused leaf, a fuzz seed.
func x509EdNull(t testing.TB) []byte {
	bad, _, _ := x509EdNullChain(t)
	return bad
}

// TestX509_Ed25519ParamsRefused: an Ed25519 signature whose
// AlgorithmIdentifier carries NULL parameters (RFC 8410 3: MUST be
// absent), re-signed so the signature itself is right, is Unsupported, as
// Go refuses it, and Parse reads its algorithm as unknown, as Go does; the
// same chain without the parameters verifies.
func TestX509_Ed25519ParamsRefused(t *testing.T) {
	der, good, root := x509EdNullChain(t)
	if err := x509GoVerify([][]byte{der}, [][]byte{root}, "ed.test", x509Now); err == nil {
		t.Fatal("Go accepts Ed25519 with NULL parameters")
	}
	if err := x509GoVerify([][]byte{good}, [][]byte{root}, "ed.test", x509Now); err != nil {
		t.Fatalf("Go refuses the control: %v", err)
	}
	c := x509MustParse(t, der)
	got := x509Answers(t, x509Modes, []string{
		x509VerifyRecord(x509Now, "ed.test", [][]byte{der}, [][]byte{root}),
		x509VerifyRecord(x509Now, "ed.test", [][]byte{good}, [][]byte{root}),
		"p " + x509Hex(der),
	})
	if got[0] != "err Unsupported" || got[1] != "ok" {
		t.Errorf("got %q, want [err Unsupported ok]", got[:2])
	}
	if x509Head(got[2]) != x509Summary(c) {
		t.Errorf("parse:\n mk %s\n go %s", got[2], x509Summary(c))
	}
}

// TestX509_LongChains: a root, 15 and then 40 intermediates and a
// leaf (17 and 42 certificates) verify, as Go verifies them; the 16
// certificate limit was the platform's, not the path search's.
func TestX509_LongChains(t *testing.T) {
	var recs []string
	for _, n := range []int{14, 15, 40} {
		root := x509Make(t, x509CA("Mk Deep Root"), x509P256(t), nil)
		up := root
		var inters [][]byte
		for i := 0; i < n; i++ {
			it := x509CA(fmt.Sprintf("Mk Deep %d", i))
			up = x509Make(t, it, x509P256(t), up)
			inters = append([][]byte{up.der}, inters...)
		}
		leaf := x509Make(t, x509Leaf("deep.test", []string{"deep.test"}, nil), x509P256(t), up)
		chain := append([][]byte{leaf.der}, inters...)
		if err := x509GoVerify(chain, [][]byte{root.der}, "deep.test", x509Now); err != nil {
			t.Fatalf("%d intermediates: Go says %v", n, err)
		}
		recs = append(recs, x509VerifyRecord(x509Now, "deep.test", chain, [][]byte{root.der}))
	}
	got := x509Answers(t, x509Modes, recs)
	for i, g := range got {
		if g != "ok" {
			t.Errorf("chain %d: got %s, want ok", i, g)
		}
	}
}

// TestX509_PlatformRefusesWeak: against the system's pool on
// Windows, a leaf signed with SHA-1 or MD5 is Unsupported before the
// platform is asked, as with the program's own pool.
func TestX509_PlatformRefusesWeak(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the platform's path is Windows'")
	}
	fx := x509Fixtures(t)
	var recs []string
	for _, f := range []string{"sha1-leaf.pem", "md5-leaf.pem"} {
		recs = append(recs, x509CachedRecord(x509Now, "rsa.test", fx[f]), x509VerifyRecord(x509Now, "rsa.test", fx[f], fx["rsa-root.pem"]))
	}
	got := x509Answers(t, x509Modes, recs)
	for i, g := range got {
		if g != "err Unsupported" {
			t.Errorf("record %d: got %s, want err Unsupported", i, g)
		}
	}
}

// TestX509_UniqueKeepsFirst: the system pool's certificates are
// kept once each, in the order first seen.
func TestX509_UniqueKeepsFirst(t *testing.T) {
	fx := x509Fixtures(t)
	r, s := fx["root.pem"][0], fx["rsa-root.pem"][0]
	var b bytes.Buffer
	for _, d := range [][]byte{r, r, s, r, s} {
		pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: d})
	}
	got := x509Answers(t, x509Modes, []string{"u " + x509Hex(b.Bytes()), "u " + x509Hex([]byte(""))})
	want := "ok " + x509NameText(x509MustParse(t, r).RawSubject) + "|" + x509NameText(x509MustParse(t, s).RawSubject)
	if got[0] != want || got[1] != "ok " {
		t.Errorf("got %q, want %q and \"ok \"", got, want)
	}
}

func x509MustParse(t *testing.T, der []byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestX509_ReadCapped: the system pool's file reader reads
// through one handle and gives up past its limit, on the size it reads
// first and on the bytes it reads.
func TestX509_ReadCapped(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.pem")
	empty := filepath.Join(dir, "empty.pem")
	big := filepath.Join(dir, "big.pem")
	if err := os.WriteFile(small, bytes.Repeat([]byte("x"), 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, bytes.Repeat([]byte("y"), 3_000_000), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := func(p string, limit int) string { return fmt.Sprintf("R %s %d", x509Hex([]byte(p)), limit) }
	cases := []struct {
		rec, want string
	}{
		{rec(small, 50), "ok none"},
		{rec(small, 99), "ok none"},
		{rec(small, 100), "ok 100"},
		{rec(small, 1000), "ok 100"},
		{rec(empty, 10), "ok 0"},
		{rec(empty, 0), "ok 0"},
		{rec(filepath.Join(dir, "missing.pem"), 10), "ok none"},
		{rec(dir, 10), "ok none"},
		{rec(big, 4_000_000), "ok 3000000"},
		{rec(big, 2_999_999), "ok none"},
	}
	var recs []string
	for _, c := range cases {
		recs = append(recs, c.rec)
	}
	got := x509Answers(t, x509Modes, recs)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("%s: got %s, want %s", c.rec[:min(len(c.rec), 60)], got[i], c.want)
		}
	}
}

// x509Pick is a deterministic choice for the generated cases.
type x509Pick interface {
	IntN(n int) int
}

var (
	x509Labels = []string{"a", "b", "x", "ab", "B"}
	x509Locals = []string{"foo", `"foo"`, `fo\o`, "Foo", "bar", `"b ar"`}
)

func x509Domain(r x509Pick) string {
	n := 1 + r.IntN(3)
	var l []string
	for i := 0; i < n; i++ {
		l = append(l, x509Labels[r.IntN(len(x509Labels))])
	}
	return strings.Join(l, ".")
}

// x509Nc is the generated constraints of one side.
type x509Nc struct {
	dns, email, uri []string
	ip              []*net.IPNet
}

func x509GenSide(r x509Pick) x509Nc {
	var s x509Nc
	for i := r.IntN(4); i > 0; i-- {
		d := x509Domain(r)
		switch r.IntN(6) {
		case 0:
			d = "." + d
		case 1:
			if r.IntN(4) == 0 {
				d = ""
			}
		}
		s.dns = append(s.dns, d)
	}
	for i := r.IntN(3); i > 0; i-- {
		if r.IntN(2) == 0 {
			bits := 8 + r.IntN(25)
			ip := net.IPv4(10, byte(r.IntN(4)), byte(r.IntN(4)), byte(r.IntN(4))).To4()
			s.ip = append(s.ip, &net.IPNet{IP: ip.Mask(net.CIDRMask(bits, 32)), Mask: net.CIDRMask(bits, 32)})
		} else {
			bits := 8 + r.IntN(121)
			ip := net.ParseIP(fmt.Sprintf("fd00::%x:%x", r.IntN(4), r.IntN(4)))
			s.ip = append(s.ip, &net.IPNet{IP: ip.Mask(net.CIDRMask(bits, 128)), Mask: net.CIDRMask(bits, 128)})
		}
	}
	for i := r.IntN(3); i > 0; i-- {
		switch r.IntN(3) {
		case 0:
			s.email = append(s.email, x509Locals[r.IntN(len(x509Locals))]+"@"+x509Domain(r))
		case 1:
			s.email = append(s.email, "."+x509Domain(r))
		default:
			s.email = append(s.email, x509Domain(r))
		}
	}
	for i := r.IntN(3); i > 0; i-- {
		d := x509Domain(r)
		if r.IntN(3) == 0 {
			d = "." + d
		}
		s.uri = append(s.uri, d)
	}
	return s
}

func x509GenNames(r x509Pick) []x509Gn {
	var out []x509Gn
	for i := 1 + r.IntN(4); i > 0; i-- {
		switch r.IntN(5) {
		case 0:
			d := x509Domain(r)
			if r.IntN(4) == 0 {
				d = "*." + d
			}
			out = append(out, x509Gn{2, []byte(d)})
		case 1:
			if r.IntN(2) == 0 {
				out = append(out, x509Gn{7, []byte{10, byte(r.IntN(4)), byte(r.IntN(4)), byte(r.IntN(4))}})
			} else {
				out = append(out, x509Gn{7, net.ParseIP(fmt.Sprintf("fd00::%x:%x", r.IntN(4), r.IntN(4))).To16()})
			}
		case 2:
			out = append(out, x509Gn{1, []byte(x509Locals[r.IntN(len(x509Locals))] + "@" + x509Domain(r))})
		case 3:
			out = append(out, x509Gn{6, []byte("https://" + x509Domain(r) + "/p")})
		default:
			out = append(out, x509Gn{2, []byte(x509Domain(r))})
		}
	}
	return out
}

// x509Mailbox is Go's parseRFC2821Mailbox (crypto/x509 verify.go, Go
// 1.27), for the model: the key (unquoted local part, '@', lower-case
// domain) and the domain.
func x509Mailbox(in string) (key, domain string, ok bool) {
	if len(in) == 0 {
		return "", "", false
	}
	var local []byte
	if in[0] == '"' {
		in = in[1:]
	quoted:
		for {
			if len(in) == 0 {
				return "", "", false
			}
			c := in[0]
			in = in[1:]
			switch {
			case c == '"':
				break quoted
			case c == '\\':
				if len(in) == 0 {
					return "", "", false
				}
				if in[0] == 11 || in[0] == 12 || (1 <= in[0] && in[0] <= 9) || (14 <= in[0] && in[0] <= 127) {
					local = append(local, in[0])
					in = in[1:]
				} else {
					return "", "", false
				}
			case c == 11 || c == 12 || c == 32 || c == 33 || c == 127 || (1 <= c && c <= 8) || (14 <= c && c <= 31) || (35 <= c && c <= 91) || (93 <= c && c <= 126):
				local = append(local, c)
			default:
				return "", "", false
			}
		}
	} else {
	atom:
		for len(in) > 0 {
			c := in[0]
			switch {
			case c == '\\':
				in = in[1:]
				if len(in) == 0 {
					return "", "", false
				}
				local = append(local, in[0])
				in = in[1:]
			case ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || strings.IndexByte("!#$%&'*+-/=?^_`{|}~.", c) >= 0:
				local = append(local, c)
				in = in[1:]
			default:
				break atom
			}
		}
		if len(local) == 0 || local[0] == '.' || local[len(local)-1] == '.' || bytes.Contains(local, []byte("..")) {
			return "", "", false
		}
	}
	if len(in) == 0 || in[0] != '@' {
		return "", "", false
	}
	in = in[1:]
	if !x509DomainValid(in) || strings.Contains(in, "@") {
		return "", "", false
	}
	return string(local) + "@" + strings.ToLower(in), in, true
}

// x509DomainValid is Go's domainNameValid(s, false).
func x509DomainValid(s string) bool {
	if s == "" {
		return true
	}
	if s[len(s)-1] == '.' {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if l == "" {
			return false
		}
		for i := 0; i < len(l); i++ {
			if l[i] < 33 || l[i] > 126 {
				return false
			}
		}
	}
	return true
}

// x509Suffix is Go's dnsHasSuffix for constraint c over name n.
func x509Suffix(c, n string) bool {
	if c == "" {
		return true
	}
	if len(c) > len(n) || !strings.EqualFold(n[len(n)-len(c):], c) {
		return false
	}
	return c[0] == '.' || len(n) == len(c) || n[len(n)-len(c)-1] == '.'
}

// x509HostIn is RFC 5280's reading of an email or URI host constraint.
func x509HostIn(c, h string) bool {
	if c == "" {
		return true
	}
	if c[0] == '.' {
		return len(h) > len(c) && x509Suffix(c, h)
	}
	return strings.EqualFold(c, h)
}

func x509First(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[i:]
	}
	return ""
}

// x509ModelAllows is std.x509's documented reading of name constraints,
// as a quadratic scan: whether every name is allowed by p and not
// excluded by e.
func x509ModelAllows(p, e x509Nc, names []x509Gn) bool {
	for _, g := range names {
		v := string(g.v)
		switch g.tag {
		case 2:
			if !x509DomainValid(v) {
				return false
			}
			if len(p.dns) > 0 && !slices.ContainsFunc(p.dns, func(c string) bool { return x509Suffix(c, v) }) {
				return false
			}
			if slices.ContainsFunc(e.dns, func(c string) bool {
				return x509Suffix(c, v) || (v != "" && v[0] == '*' && x509First(c) != "" && strings.EqualFold(x509First(c), x509First(v)))
			}) {
				return false
			}
		case 7:
			ip := net.IP(g.v)
			if len(p.ip) > 0 && !slices.ContainsFunc(p.ip, func(n *net.IPNet) bool { return len(n.IP) == len(ip) && n.Contains(ip) }) {
				return false
			}
			if slices.ContainsFunc(e.ip, func(n *net.IPNet) bool { return len(n.IP) == len(ip) && n.Contains(ip) }) {
				return false
			}
		case 1:
			key, dom, ok := x509Mailbox(v)
			if !ok {
				return false
			}
			match := func(c string) bool {
				if strings.Contains(c, "@") {
					ck, _, ok := x509Mailbox(c)
					return ok && ck == key
				}
				return x509HostIn(c, dom)
			}
			if len(p.email) > 0 && !slices.ContainsFunc(p.email, match) {
				return false
			}
			if slices.ContainsFunc(e.email, match) {
				return false
			}
		case 6:
			h := strings.TrimSuffix(strings.TrimPrefix(v, "https://"), "/p")
			if len(p.uri) > 0 && !slices.ContainsFunc(p.uri, func(c string) bool { return x509HostIn(c, h) }) {
				return false
			}
			if slices.ContainsFunc(e.uri, func(c string) bool { return x509HostIn(c, h) }) {
				return false
			}
		}
	}
	return true
}

// x509GenCase is one generated chain and its expected verdicts: the
// model's, and Go's when Go reads it the same way (no email or URI
// constraint, where std.x509 keeps RFC 5280's exact-host reading).
func x509GenCase(t *testing.T, r x509Pick, root *x509Made) (rec string, model bool, goSame bool, goOk bool, desc string) {
	p, e := x509GenSide(r), x509GenSide(r)
	it := x509CA("Mk Gen Constrained")
	it.PermittedDNSDomains, it.ExcludedDNSDomains = p.dns, e.dns
	it.PermittedIPRanges, it.ExcludedIPRanges = p.ip, e.ip
	it.PermittedEmailAddresses, it.ExcludedEmailAddresses = p.email, e.email
	it.PermittedURIDomains, it.ExcludedURIDomains = p.uri, e.uri
	if len(p.dns)+len(e.dns)+len(p.ip)+len(e.ip)+len(p.email)+len(e.email)+len(p.uri)+len(e.uri) == 0 {
		it.PermittedDNSDomains = []string{"x"}
		p.dns = it.PermittedDNSDomains
	}
	names := x509GenNames(r)
	leaf, inter := x509Chain(t, root, it, names)
	chain := [][]byte{leaf, inter}
	goErr := x509GoVerify(chain, [][]byte{root.der}, "", x509Now)
	model = x509ModelAllows(p, e, names)
	goSame = len(p.email)+len(e.email)+len(p.uri)+len(e.uri) == 0
	desc = fmt.Sprintf("permitted %+v excluded %+v names %q", p, e, names)
	return x509LimboRecord(x509Now, "", chain, [][]byte{root.der}), model, goSame, goErr == nil, desc
}

// TestX509_ConstraintsMatchModel: 600 generated chains (DNS, IP,
// email and URI constraints on both sides, wildcards, quoted mailboxes,
// empty and leading-dot bases) answer as the quadratic model does, and as
// Go does wherever Go reads the forms as std.x509 does.
func TestX509_ConstraintsMatchModel(t *testing.T) {
	root := x509Make(t, x509CA("Mk Gen Root"), x509P256(t), nil)
	r := rand.New(rand.NewPCG(6, 509))
	type gen struct {
		model, goSame, goOk bool
		desc                string
	}
	var recs []string
	var gens []gen
	for i := 0; i < 600; i++ {
		rec, m, s, g, d := x509GenCase(t, r, root)
		recs = append(recs, rec)
		gens = append(gens, gen{m, s, g, d})
		if s && m != g {
			t.Errorf("case %d: the model says %v and Go %v: %s", i, m, g, d)
		}
	}
	got := x509Answers(t, x509Modes, recs)
	oks := 0
	for i, g := range gens {
		mk := got[i] == "ok"
		if mk {
			oks++
		}
		if mk != g.model || (!mk && got[i] != "err BadUsage") {
			t.Errorf("case %d: std.x509 %s, the model %v: %s", i, got[i], g.model, g.desc)
		}
	}
	t.Logf("%d cases, %d allowed", len(gens), oks)
}
