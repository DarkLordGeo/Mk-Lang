// Why: x509.Parse reads untrusted bytes and Verify judges names, so both must
// never panic and must agree with a mature verifier.
// What: FuzzX509_Parse, FuzzX509_ParsePem, FuzzX509_Verify and
// TestX509_LargeInputsBounded.
// How: one release driver per fuzz worker (x509LiveAsk); Verify is
// differential, a chain built from the fuzzer's bytes judged by Go's
// crypto/x509 and a model.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"bufio"
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// x509Live is the release driver one fuzz worker keeps running.
var x509Live struct {
	sync.Mutex
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	stderr *bytes.Buffer
	dir    string
}

// x509LiveStop kills the running driver, if any.
func x509LiveStop() {
	if x509Live.cmd != nil {
		x509Live.in.Close()
		x509Live.cmd.Process.Kill()
		x509Live.cmd.Wait()
		x509Live.cmd = nil
	}
}

// x509LiveAsk sends one record to the worker's running driver and returns
// its answer; a crash, an exit or no answer within runTimeout fails (and
// the next call starts a new driver).
func x509LiveAsk(t *testing.T, record string) string {
	t.Helper()
	x509Live.Lock()
	defer x509Live.Unlock()
	if x509Live.cmd == nil {
		exe := x509Exe(t, "release")
		cmd := exec.Command(exe)
		cmd.Dir = filepath.Dir(exe)
		in, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		x509Live.stderr = &bytes.Buffer{}
		cmd.Stderr = x509Live.stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		dir, err := packageTempDir("mk-x509-live")
		if err != nil {
			t.Fatal(err)
		}
		x509Live.cmd, x509Live.in, x509Live.out, x509Live.dir = cmd, in, bufio.NewReaderSize(out, 1<<20), dir
	}
	if len(record) > 60000 {
		p := filepath.Join(x509Live.dir, "record.txt")
		if err := os.WriteFile(p, []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
		record = "f " + x509Hex([]byte(p))
	}
	type answer struct {
		line string
		err  error
	}
	done := make(chan answer, 1)
	if _, err := io.WriteString(x509Live.in, record+"\n"); err != nil {
		x509LiveStop()
		t.Fatalf("driver gone before %s: %v\nstderr: %s", encShort(record), err, x509Live.stderr)
	}
	out := x509Live.out
	go func() {
		l, err := out.ReadString('\n')
		done <- answer{l, err}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			x509LiveStop()
			t.Fatalf("driver died on %s: %v\nstderr: %s", encShort(record), a.err, x509Live.stderr)
		}
		return strings.TrimRight(a.line, "\r\n")
	case <-time.After(runTimeout):
		x509LiveStop()
		t.Fatalf("driver hung on %s", encShort(record))
	}
	return ""
}

// x509FuzzOne runs one record through the worker's release driver.
func x509FuzzOne(t *testing.T, record string) string {
	t.Helper()
	return x509LiveAsk(t, record)
}

// x509Seeds is every fixture certificate's DER.
func x509Seeds(f *testing.F) [][]byte {
	files, _ := filepath.Glob(filepath.Join(x509Dir, "*.pem"))
	var out [][]byte
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		for {
			var blk *pem.Block
			blk, b = pem.Decode(b)
			if blk == nil {
				break
			}
			out = append(out, blk.Bytes)
		}
	}
	return out
}

func FuzzX509_Parse(f *testing.F) {
	for _, d := range x509Seeds(f) {
		f.Add(d)
		f.Add(d[:len(d)/2])
	}
	// Ed25519 with NULL parameters reads as an unknown algorithm.
	f.Add(x509EdNull(f))
	// Inputs FuzzX509_Parse once stopped on (kept here, not under testdata):
	// a lone '0'; a subjectAltName DNS name holding
	// line breaks (one driver answer became several lines); a name holding
	// '|' (it shifted x509Head's fields).
	for _, h := range []string{
		"30",
		"308201e230820168a003020102020203eb300a06082a8648ce3d04030330313110300e060355040a13074d6b207465733a311d301b060355040313144d6b205465737420496e7465726d6564696174653020170d3030303130313030303030305a180f32323030303130313030303030305a3017311530130603550403130c6578616d706c652e746573743059301306072a86480a3d020106082a8648ce3d030107034200041701d303e7bb19947d5eda0afebcd0e4be9a3af11397a3e523156386c5d8f25126621e29542fc1d07c9bac0661aceedad02298c0d4aa4e26e6476d1c22e7e268a38187308184300e0603551d0f0101ff04040302078030130603551d25040c300a06082b06010505070301301f0603551d230418301680147c1de2a786fe744a9103c6eed71656127a85d10a303c0603551d1104353033820c6578616d706c652e74657374820b2a2e0a0a0a0a0a7465737487047f000001871000000000000000000000000000000001300a06082a8648ce3d040303036800306502304cd906de1ceff4f4818dea9bc130ce208bf48c91ac00c8397c845a3a889191e42e1015fece8f0f65c94a3346c7d66cad023100ce4efb72b7782133a0510f04c0cb7b0658933c148470a9211696b624092ef3b84e6eae1b63f261ac849f91360e57468a",
		"308201e230820168a003020102020203eb300a06082a8648ce3d04030330313110300e060355040a13074d6b207465733a311d301b0603bbfc0313144d6b205465737420496e7465726d6564696174653020170d3030303130313030303030305a180f32323030303130313030303030305a3017311530130603550403130c6578616d706c6551746573743059301306072a86480a3d020106082a8648ce3d030107034200041701d303e75519947d5e0000007fd0e4be9a05ffff05a3e523156386c5d8f25126621e29542fc1d07c9bac0661aceedad02298c0d4aa4e26e6476d1c22e7e268a38187308184300e0603551d0f0101ff04040302078030130603551d25040c300a06082b06010505070301301f0603551d230418301680147c1de2a786fe744a9103c6eed71656127a85d10a303c0603551d1104353033820c6578616d706c652e74657374820b2a2e77696c642e7c65737487047f000001871000000000000000000000000000000001300a06082a8648ce3d040303036800306502304cd906de1ceff4f4818dea9bc130ce208bf48c91ac00c8397c845a3a889191e42e1015fece8f0f65c94a3346c7d66cad023100ce4efb72b7782133a0510f04c0cb7b0658933c148470a9211696b624092ef3b84e6eae1b63f261ac849f91360e57418a",
	} {
		b, err := hex.DecodeString(h)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	// chain.pem's leaf with a constructed [1] before its
	// extensions (Go reads none of them), and with an extension whose OID
	// is not minimal (Go refuses it).
	raw, err := os.ReadFile(filepath.Join(x509Dir, "chain.pem"))
	if err != nil {
		f.Fatal(err)
	}
	blk, _ := pem.Decode(raw)
	p := x509Split(f, blk.Bytes)
	f.Add(p.insert(len(p.tbs)-1, []byte{0xA1, 0x00}).der())
	f.Add(p.withExts(append(p.exts(f), x509ExtEl([]byte{0x55, 0x1d, 0x80, 0x0f}, false, []byte{0x05, 0x00}))).der())
	// Found by the tightened target (09:03): a name constraint whose base
	// has a high tag number (ff 7f), which Go's cryptobyte refuses.
	if b, err := hex.DecodeString("308201dc30820183a003020102020203f4300a06082a8648ce3d04030230293110300e060355040a13074d6b2074657374311530130603550403130c4d6b205465737420526f6f743020170d3030303130313030303030305a180f32323030303130313030303030305a30303110300e060355040b13074d6b2074657374311c301a060355040313134d6b205465737420436f6e73747261696e65643059301306072a8648ce3d020106082a8648ce3d0301070342000457131c86d3363daaa380fc919abd9fd97b94aab7e6b0abf55bfafb3c30577e7ca23e39ffc82ec37dad5ea97e9c3a57ea7a88434435e8e7bccec2a5862e83d3a5a3819130818e300e060369690f0101ff040403020106300f0603551d130101ff040530030101ff301d0603551d0e04160414a83bdf3e844ae6a0c4aab37c7ab3f14c2dd37351301f0603551d23041830168014b485494333ecc97331af254539177b2c6ab3c162302b0603551d1e0101ff0421301fa01d300fff7f00006c6c6f7765642e74657374300a87087f000000ff000000300a06082a8648ce3d040302034700304402200facb63dafb4d92a67203b93487a8a355112eedd6fdfb81340ce4dda72b2a5f602206cbe855890d8fc74a70605eff5695c4f70dd8d828643f6ee7097a2563c94ae89"); err == nil {
		f.Add(b)
	} else {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, der []byte) {
		if len(der) > 16384 {
			return
		}
		got := x509FuzzOne(t, "p "+x509Hex(der))
		if strings.HasPrefix(got, "err ") {
			if !strings.HasPrefix(got, "err Malformed(offset: ") {
				t.Fatalf("Parse failed with %s", got)
			}
			return
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			// A certificate Go refuses is Malformed to std.x509
			// too, but for the refusals std.x509 makes later on purpose.
			if !x509LaterRefusal(err) {
				t.Fatalf("Go refuses (%v), std.x509 answers %.300s", err, got)
			}
			return
		}
		if x509Head(got) != x509Summary(c) {
			t.Fatalf("read differently:\n mk %s\n go %s", got, x509Summary(c))
		}
	})
}

// x509KeyRefusals are the refusals of Go's parsePublicKey (Go 1.27):
// std.x509 keeps the SubjectPublicKeyInfo and reads the key only when a
// signature is checked or PublicKey is asked (parse.mk), so a system
// bundle's odd key does not fail the parse.
var x509KeyRefusals = []string{
	"x509: RSA key missing NULL parameters",
	"x509: invalid RSA public key",
	"x509: invalid RSA modulus",
	"x509: invalid RSA public exponent",
	"x509: RSA modulus is not a positive number",
	"x509: RSA public exponent is not a positive number",
	"x509: invalid ECDSA parameters",
	"x509: unsupported elliptic curve",
	"x509: Ed25519 key encoded with illegal parameters",
	"x509: wrong Ed25519 public key size",
	"x509: ML-DSA key encoded with illegal parameters",
	"x509: unsupported ML-DSA parameters",
	"x509: X25519 key encoded with illegal parameters",
	"x509: invalid DSA public key",
	"x509: invalid DSA parameters",
	"x509: zero or negative DSA parameter",
	"x509: unknown public key algorithm",
}

// x509LaterRefusal is whether Go's refusal err is one std.x509 makes later
// or not at all, on purpose: the public key (read when used), whether
// parsePublicKey's own refusal or one of the key packages' it passes on
// unwrapped (every other refusal of ParseCertificate starts "x509: ",
// Go 1.27: the first fuzz run found "P256 point not on curve"), and a URI
// subjectAltName Go's url.Parse refuses (std.x509 parses no URL; under a
// URI constraint it refuses any URI whose host readers may read
// differently).
func x509LaterRefusal(err error) bool {
	m := err.Error()
	return slices.Contains(x509KeyRefusals, m) || !strings.HasPrefix(m, "x509: ") || strings.HasPrefix(m, "x509: cannot parse URI ")
}

func FuzzX509_ParsePem(f *testing.F) {
	for _, p := range []string{"chain.pem", "rsa-leaf.pem", "ed-leaf.pem"} {
		b, err := os.ReadFile(filepath.Join(x509Dir, p))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(b))
		f.Add(string(b[:len(b)/2]))
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 16384 {
			return
		}
		got := x509FuzzOne(t, "m "+x509Hex([]byte(text)))
		if !strings.HasPrefix(got, "ok ") && !strings.HasPrefix(got, "err Malformed(offset: ") {
			t.Fatalf("ParsePem answered %s", got)
		}
	})
}

// x509Bytes is a deterministic picker over the fuzzer's bytes: IntN(n)
// is the next byte modulo n, 0 once the bytes run out.
type x509Bytes struct {
	b []byte
}

func (r *x509Bytes) IntN(n int) int {
	if len(r.b) == 0 || n <= 1 {
		return 0
	}
	x := int(r.b[0])
	r.b = r.b[1:]
	return x % n
}

// x509FuzzKit is the root a fuzz worker signs its chains under.
var x509FuzzKit struct {
	sync.Once
	root *x509Made
}

// x509FixedSides are constraints the seeds of fixed defects need (gen's
// first byte 3 picks one by its second): two excluded URI domains and an
// excluded mailbox.
var x509FixedSides = []func(*x509.Certificate){
	func(c *x509.Certificate) { c.ExcludedURIDomains = []string{"bad.com"} },
	func(c *x509.Certificate) { c.ExcludedURIDomains = []string{"good.com"} },
	func(c *x509.Certificate) { c.ExcludedEmailAddresses = []string{"foo@example.com"} },
}

// x509RawNames reads a raw subjectAltName value into names, false when
// it is not a SEQUENCE of primitive context-specific names.
func x509RawNames(raw []byte) ([]x509Gn, bool) {
	var seq asn1.RawValue
	rest, err := asn1.Unmarshal(raw, &seq)
	if err != nil || len(rest) > 0 || seq.Tag != 16 || !seq.IsCompound {
		return nil, false
	}
	var out []x509Gn
	b := seq.Bytes
	for len(b) > 0 {
		var g asn1.RawValue
		b, err = asn1.Unmarshal(b, &g)
		if err != nil || g.Class != 2 || g.IsCompound || g.Tag > 8 {
			return nil, false
		}
		out = append(out, x509Gn{byte(g.Tag), g.Bytes})
	}
	return out, true
}

// x509PlainHost is whether every reader of URLs reads the same host in
// u: no control byte, space or '\' in it, "scheme://" first, at most one
// '@' before the host, a host of letters, digits, '-', '.' and '_' whose
// last label is not a number (the URL Standard reads that as IPv4), then
// only a digit port.
func x509PlainHost(u string) bool {
	if strings.ContainsFunc(u, func(r rune) bool { return r <= ' ' || r >= 0x7f || r == '\\' }) {
		return false
	}
	_, rest, ok := strings.Cut(u, "://")
	if !ok {
		return false
	}
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		rest = rest[:i]
	}
	if strings.Count(rest, "@") > 1 {
		return false
	}
	if i := strings.LastIndexByte(rest, '@'); i >= 0 {
		rest = rest[i+1:]
	}
	host, port, _ := strings.Cut(rest, ":")
	if host == "" || strings.Trim(port, "0123456789") != "" || strings.Trim(host, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._") != "" {
		return false
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	last := strings.ToLower(labels[len(labels)-1])
	return strings.Trim(last, "0123456789") != "" && !strings.HasPrefix(last, "0x")
}

// x509GoWritable is whether Go's CreateCertificate writes names (it
// parses what it made): an email or DNS name in ASCII, a URI of at least
// five ASCII bytes (x509Chain writes a placeholder for it), an IP address
// of 4 or 16 bytes not IPv4-mapped; at least one name.
func x509GoWritable(names []x509Gn) bool {
	if len(names) == 0 {
		return false
	}
	for _, g := range names {
		ascii := !slices.ContainsFunc(g.v, func(b byte) bool { return b >= 0x80 })
		switch g.tag {
		case 1, 2:
			if !ascii {
				return false
			}
		case 6:
			if !ascii || len(g.v) < 5 {
				return false
			}
		case 7:
			if len(g.v) != 4 && (len(g.v) != 16 || net.IP(g.v).To4() != nil) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func FuzzX509_Verify(f *testing.F) {
	uri := func(dns, u string) []byte {
		return x509RawSAN([]x509Gn{{2, []byte(dns)}, {6, []byte(u)}}).Value
	}
	mail := func(m string) []byte {
		return x509RawSAN([]x509Gn{{2, []byte("ok.test")}, {1, []byte(m)}}).Value
	}
	// Certificates of fixed defects (URI hosts, quoted mailboxes), then
	// generated ones.
	f.Add([]byte{3, 0}, uri("ok.test", "https://bad%2ecom/"), "")
	f.Add([]byte{3, 1}, uri("ok.test", `https://good.com\@bad.com/`), "ok.test")
	f.Add([]byte{3, 2}, mail(`"foo"@example.com`), "")
	f.Add([]byte{3, 2}, mail(`fo\o@example.com`), "")
	// Found 2026-11-13: under IP-only constraints, a URI whose '#' comes
	// before its first ':' has no scheme and no host for Go (refused);
	// UriNamed read one.
	f.Add([]byte("101"), []byte("0\x1d\x82\a0000000\x86\x1200#00://0000000000"), "")
	for i := 0; i < 16; i++ {
		f.Add([]byte{0, byte(i), byte(i * 7), byte(i * 13), 1, 2, 3, byte(i * 31), 5, 8, 13}, []byte(nil), "")
		f.Add([]byte{1, byte(i * 3), 9, byte(i), 4, 4, 4, 1, 0, 2}, []byte(nil), "a.b")
	}
	f.Fuzz(func(t *testing.T, gen []byte, rawSan []byte, host string) {
		// Verify's host is a str, UTF-8 by its type, as the driver reads it.
		if len(gen) > 4096 || len(rawSan) > 8192 || len(host) > 300 || strings.ContainsAny(host, " \n\r") || !utf8.ValidString(host) {
			return
		}
		x509FuzzKit.Do(func() {
			x509FuzzKit.root = x509Make(t, x509CA("Mk Fuzz Root"), x509P256(t), nil)
		})
		root := x509FuzzKit.root
		r := &x509Bytes{gen}
		mode := r.IntN(4)
		it := x509CA("Mk Fuzz Constrained")
		var p, e x509Nc
		if mode == 3 {
			x509FixedSides[r.IntN(len(x509FixedSides))](it)
			e.uri, e.email = it.ExcludedURIDomains, it.ExcludedEmailAddresses
		} else {
			p, e = x509GenSide(r), x509GenSide(r)
			it.PermittedDNSDomains, it.ExcludedDNSDomains = p.dns, e.dns
			it.PermittedIPRanges, it.ExcludedIPRanges = p.ip, e.ip
			it.PermittedEmailAddresses, it.ExcludedEmailAddresses = p.email, e.email
			it.PermittedURIDomains, it.ExcludedURIDomains = p.uri, e.uri
			if len(p.dns)+len(e.dns)+len(p.ip)+len(e.ip)+len(p.email)+len(e.email)+len(p.uri)+len(e.uri) == 0 {
				it.PermittedDNSDomains = []string{"x"}
				p.dns = it.PermittedDNSDomains
			}
		}
		var names []x509Gn
		generated := false
		if mode == 0 || len(rawSan) == 0 {
			names, generated = x509GenNames(r), true
		} else if n, ok := x509RawNames(rawSan); ok && x509GoWritable(n) {
			names = n
		} else {
			return
		}
		leaf, inter := x509Chain(t, root, it, names)
		chain := [][]byte{leaf, inter}
		got := x509FuzzOne(t, x509LimboRecord(x509Now, host, chain, [][]byte{root.der}))
		if got != "ok" && !strings.HasPrefix(got, "err ") {
			t.Fatalf("Verify answered %s", got)
		}
		mk := got == "ok"
		if generated && host == "" {
			if m := x509ModelAllows(p, e, names); mk != m {
				t.Fatalf("std.x509 %s, the model %v: permitted %+v excluded %+v names %q", got, m, p, e, names)
			}
		}
		_, err1 := x509.ParseCertificate(leaf)
		_, err2 := x509.ParseCertificate(inter)
		if err1 != nil || err2 != nil {
			// Go refuses at parse a URI it cannot parse; std.x509 reads a
			// URI only under a URI constraint, and must then have read a
			// host no reader of URLs reads otherwise.
			if mk && len(p.uri)+len(e.uri) > 0 {
				for _, g := range names {
					if g.tag == 6 && !x509PlainHost(string(g.v)) {
						t.Fatalf("std.x509 accepts a URI whose host readers may read differently, under a URI constraint: %q", g.v)
					}
				}
			}
			return
		}
		if len(p.email)+len(e.email)+len(p.uri)+len(e.uri) > 0 {
			// RFC 5280's exact-host reading of email and URI domains
			// (std.x509) against Go's DNS reading: judged by the model.
			return
		}
		goOk := x509GoVerify(chain, [][]byte{root.der}, host, x509Now) == nil
		if mk != goOk {
			t.Fatalf("std.x509 %s, Go ok=%v: permitted %+v excluded %+v names %q host %q", got, goOk, p, e, names, host)
		}
	})
}

// TestX509_LargeInputsBounded: a certificate with 2,000 subjectAltName
// names and a subject of 1,500 RDNs parses, prints its names and subject,
// and verifies against itself in linear time: 20 of each in one driver
// run stay under a generous bound on a loaded machine.
func TestX509_LargeInputsBounded(t *testing.T) {
	key := x509P256(t)
	tmpl := x509Leaf("big.test", nil, nil)
	for i := 0; i < 2000; i++ {
		tmpl.DNSNames = append(tmpl.DNSNames, fmt.Sprintf("n%d.big.test", i))
	}
	for i := 0; i < 1500; i++ {
		tmpl.Subject.ExtraNames = append(tmpl.Subject.ExtraNames, pkix.AttributeTypeAndValue{Type: asn1.ObjectIdentifier{2, 5, 4, 11}, Value: fmt.Sprintf("u%d", i)})
	}
	big := x509Make(t, tmpl, key, nil)
	var records []string
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		records = append(records, "p "+x509Hex(big.der), x509VerifyRecord(now, "n1999.big.test", [][]byte{big.der}, [][]byte{big.der}))
	}
	start := time.Now()
	got := x509Ask(t, "release", records)
	took := time.Since(start)
	for i := 0; i < len(got); i += 2 {
		if !strings.HasPrefix(got[i], "ok ") || got[i+1] != "ok" {
			t.Fatalf("record %d: %.200s / %s", i, got[i], got[i+1])
		}
	}
	if took > 20*time.Second {
		t.Errorf("20 large certificates took %s", took)
	}
	t.Logf("%d bytes a certificate; 20 parses with names and subject, 20 verifies: %s (release, loaded machine; not a measurement)", len(big.der), took)
}
