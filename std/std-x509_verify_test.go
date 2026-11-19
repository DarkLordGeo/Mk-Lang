// Why: x509.Verify decides trust and must agree with a mature verifier.
// What: TestX509_VerifyFixtures and TestX509_Limbo.
// How: named cases over the checked-in chains judged against Go's Verify, and
// the pinned x509-limbo suite filtered as Go filters it, every difference
// named.

package std

import (
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// x509GoVerify is Go's verdict on chain and roots for host at now.
func x509GoVerify(chain, roots [][]byte, host string, now time.Time) error {
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return err
	}
	inter, pool := x509.NewCertPool(), x509.NewCertPool()
	for _, d := range chain[1:] {
		if c, err := x509.ParseCertificate(d); err == nil {
			inter.AddCert(c)
		}
	}
	for _, d := range roots {
		if c, err := x509.ParseCertificate(d); err == nil {
			pool.AddCert(c)
		}
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter, Roots: pool, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

// TestX509_VerifyFixtures: the named cases.
func TestX509_VerifyFixtures(t *testing.T) {
	fx := x509Fixtures(t)
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	chain, root := fx["chain.pem"], fx["root.pem"]
	rroot, eroot := fx["rsa-root.pem"], fx["ed-root.pem"]
	tampered := slices.Clone(chain[0])
	tampered[len(tampered)-1] ^= 1
	type tc struct {
		name        string
		chain       [][]byte
		roots       [][]byte
		host        string
		now         time.Time
		want        string // "ok" or the error's name
		goDisagrees string // why Go's verdict differs, "" when it agrees
	}
	cases := []tc{
		{"chain", chain, root, "example.test", now, "ok", ""},
		{"wildcard", chain, root, "foo.wild.test", now, "ok", ""},
		{"wildcard two labels", chain, root, "a.b.wild.test", now, "NameMismatch", ""},
		{"wildcard bare parent", chain, root, "wild.test", now, "NameMismatch", ""},
		{"upper case", chain, root, "EXAMPLE.TEST", now, "ok", ""},
		{"trailing dot", chain, root, "example.test.", now, "ok", ""},
		{"ipv4", chain, root, "127.0.0.1", now, "ok", ""},
		{"ipv6 bracketed", chain, root, "[::1]", now, "ok", ""},
		{"ipv6 bare", chain, root, "::1", now, "ok", ""},
		{"ipv6 long form", chain, root, "0:0:0:0:0:0:0:1", now, "ok", ""},
		{"ip not named", chain, root, "127.0.0.2", now, "NameMismatch", ""},
		{"other name", chain, root, "other.test", now, "NameMismatch", ""},
		{"no host", chain, root, "", now, "ok", ""},
		{"no roots", chain, nil, "example.test", now, "UnknownAuthority", ""},
		{"missing intermediate", chain[:1], root, "example.test", now, "UnknownAuthority", ""},
		{"wrong root", chain, rroot, "example.test", now, "UnknownAuthority", ""},
		{"before validity", chain, root, "example.test", time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), "Expired", ""},
		{"after validity", chain, root, "example.test", time.Date(2250, 1, 1, 0, 0, 0, 0, time.UTC), "Expired", ""},
		{"tampered", [][]byte{tampered, chain[1]}, root, "example.test", now, "BadSignature", ""},
		{"rsa pkcs1", fx["rsa-leaf.pem"], rroot, "rsa.test", now, "ok", ""},
		{"rsa pss", fx["pss-leaf.pem"], rroot, "pss.test", now, "ok", ""},
		{"sha1 refused", fx["sha1-leaf.pem"], rroot, "rsa.test", now, "Unsupported", ""},
		{"md5 refused", fx["md5-leaf.pem"], rroot, "rsa.test", now, "Unsupported", ""},
		{"ed25519", fx["ed-leaf.pem"], eroot, "ed.test", now, "ok", ""},
		{"leaf in pool", fx["ed-leaf.pem"], fx["ed-leaf.pem"], "ed.test", now, "ok", ""},
		{"client auth only", fx["client-leaf.pem"], root, "client.test", now, "BadUsage", ""},
		{"issuer not a ca", fx["noca-chain.pem"], root, "noca.test", now, "BadUsage", ""},
		{"inside name constraints", fx["nc-good-chain.pem"], root, "a.allowed.test", now, "ok", ""},
		{"outside name constraints", fx["nc-bad-chain.pem"], root, "bad.test", now, "BadUsage", ""},
		{"no expiry", fx["forever-leaf.pem"], root, "forever.test", now, "ok", ""},
		{"no expiry near the end of Time", fx["forever-leaf.pem"], fx["forever-leaf.pem"], "forever.test", time.Date(2262, 1, 1, 0, 0, 0, 0, time.UTC), "ok", ""},
	}
	var records []string
	for _, c := range cases {
		records = append(records, x509VerifyRecord(c.now, c.host, c.chain, c.roots))
		goErr := x509GoVerify(c.chain, c.roots, c.host, c.now)
		if (goErr == nil) != (c.want == "ok") && c.goDisagrees == "" {
			t.Errorf("%s: Go says %v, the case wants %s", c.name, goErr, c.want)
		}
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		for i, c := range cases {
			want := c.want
			if want != "ok" {
				want = "err " + want
			}
			if got[i] != want {
				t.Errorf("%s %s: got %s, want %s", mode, c.name, got[i], want)
			}
		}
	}
}

// x509Limbo is the part of a limbo.json case the test reads.
type x509Limbo struct {
	ID               string   `json:"id"`
	Features         []string `json:"features"`
	ValidationKind   string   `json:"validation_kind"`
	TrustedCerts     []string `json:"trusted_certs"`
	Intermediates    []string `json:"untrusted_intermediates"`
	PeerCertificate  string   `json:"peer_certificate"`
	ValidationTime   *string  `json:"validation_time"`
	ExtendedKeyUsage []string `json:"extended_key_usage"`
	KeyUsage         []string `json:"key_usage"`
	ExpectedResult   string   `json:"expected_result"`
	ExpectedPeerName *struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"expected_peer_name"`
}

// x509LimboKinds pins std.x509's answer on cases whose outcome alone does
// not show the rule they exercise.
var x509LimboKinds = map[string]string{
	// 100 intermediates sharing one subject, each with a key of its
	// own, fail by the limit of 100 signature checks, not by
	// running out of candidates.
	"pathological::pathological-chain-same-subject-distinct-key": "err TooComplex",
}

// x509LimboAllowed are the cases where std.x509 and the suite disagree,
// each with its reason. Most are Go's own allowances (x509limbo_test.go of
// Go 1.27), where std.x509 follows Go.
var x509LimboAllowed = map[string]string{
	// std.x509 is stricter than the suite, by its own limits or RFC 8446.
	"rfc5280::ca-as-leaf":                         "the leaf's key usage lacks digitalSignature, which a TLS 1.3 server's key needs (RFC 8446 4.4.2.2)",
	"pathlen::validation-ignores-pathlen-in-leaf": "the leaf's key usage lacks digitalSignature, which a TLS 1.3 server's key needs (RFC 8446 4.4.2.2)",
	"rfc9881::ml-dsa-44":                          "ML-DSA (RFC 9881) is not in std.crypto",
	"rfc9881::ml-dsa-65":                          "ML-DSA (RFC 9881) is not in std.crypto",
	"rfc9881::ml-dsa-87":                          "ML-DSA (RFC 9881) is not in std.crypto",
	"pathlen::self-issued-certs-pathlen":          "path length counts every intermediate, as Go and mozilla::pkix count it",
	"rfc5280::nc::nc-forbids-othername-noop":      "a critical name constraints extension with a form std.x509 does not check is refused (RFC 5280 4.2), as Go refuses it",
	// Name constraints are checked with sorted, pruned sets, as
	// Go 1.27 checks them: Go allows these two the same way.
	"pathological::nc-dos-1": "standards compliant; the suite expects the rejection of a quadratic check's DoS limit, and std.x509's check is log-linear (as Go's)",
	"pathological::nc-dos-2": "standards compliant; the suite expects the rejection of a quadratic check's DoS limit, and std.x509's check is log-linear (as Go's)",
	// Go's allowances (x509limbo_test.go of Go 1.27), where std.x509 does
	// what Go does.
	"rfc5280::san::noncritical-with-empty-subject":              "Go: TODO(#79741); the subject's emptiness is not checked",
	"webpki::san::san-critical-with-nonempty-subject":           "Go: TODO(#79741); the subject's emptiness is not checked",
	"rfc5280::ca-empty-subject":                                 "Go: TODO(#79744); the subject's emptiness is not checked",
	"rfc5280::san::underscore-dns":                              "Go: underscores are allowed in DNS names (#75835)",
	"webpki::forbidden-dsa-leaf":                                "the leaf's own key is not used by Verify; no CABF key-strength policy (as Go)",
	"webpki::forbidden-p192-leaf":                               "the leaf's own key is not used by Verify; no CABF key-strength policy (as Go)",
	"webpki::forbidden-weak-rsa-in-leaf":                        "the leaf's own key is not used by Verify; no CABF key-strength policy (as Go)",
	"webpki::forbidden-rsa-not-divisible-by-8-in-root":          "no CABF key-strength policy beyond std.crypto's 2048 to 8192 bits (as Go)",
	"webpki::forbidden-rsa-key-not-divisible-by-8-in-leaf":      "the leaf's own key is not used by Verify; no CABF key-strength policy (as Go)",
	"webpki::san::public-suffix-wildcard-san":                   "no public suffix list in std (as Go)",
	"webpki::san::public-suffix-multi-label-wildcard-san":       "no public suffix list in std (as Go)",
	"webpki::san::public-suffix-private-namespace-wildcard-san": "no public suffix list in std (as Go)",
	"rfc5280::root-non-critical-basic-constraints":              "a trust anchor is taken as given (RFC 5280 6.1.1 (d)), as Go takes it",
	"rfc5280::root-missing-basic-constraints":                   "a trust anchor is taken as given (RFC 5280 6.1.1 (d)), as Go takes it",
	"rfc5280::root-inconsistent-ca-extensions":                  "a trust anchor is taken as given (RFC 5280 6.1.1 (d)), as Go takes it",
	"rfc5280::leaf-ku-keycertsign":                              "the leaf's key usage is checked for digitalSignature only (as Go ignores the rest)",
	"webpki::ee-basicconstraints-ca":                            "a CA certificate may be the leaf (as Go)",
	"rfc5280::nc::invalid-dnsname-leading-period":               "a leading period in a DNS constraint is taken, as Go takes it",
	"rfc5280::aki::cross-signed-root-missing-aki":               "the key identifiers are not load-bearing (as Go)",
	"rfc5280::aki::leaf-missing-aki":                            "the key identifiers are not load-bearing (as Go)",
	"rfc5280::aki::intermediate-missing-aki":                    "the key identifiers are not load-bearing (as Go)",
	"rfc5280::ski::root-missing-ski":                            "the key identifiers are not load-bearing (as Go)",
	"rfc5280::ski::intermediate-missing-ski":                    "the key identifiers are not load-bearing (as Go)",
	"webpki::aki::root-with-aki-missing-keyidentifier":          "no CABF rule on a root's key identifiers (as Go)",
	"webpki::aki::root-with-aki-authoritycertissuer":            "no CABF rule on a root's key identifiers (as Go)",
	"webpki::aki::root-with-aki-authoritycertserialnumber":      "no CABF rule on a root's key identifiers (as Go)",
	"webpki::aki::root-with-aki-all-fields":                     "no CABF rule on a root's key identifiers (as Go)",
	"webpki::aki::root-with-aki-ski-mismatch":                   "no CABF rule on a root's key identifiers (as Go)",
	"webpki::eku::ee-critical-eku":                              "a critical extended key usage is taken (as Go)",
	"rfc5280::nc::permitted-dns-match-noncritical":              "a non-critical name constraints extension is applied (as Go)",
	"rfc5280::pc::ica-noncritical-pc":                           "policy constraints are not processed",
	"rfc5280::serial::too-long":                                 "a long serial is taken (as Go: private PKIs)",
	"rfc5280::serial::zero":                                     "a zero serial is taken (as Go; RFC 5280 asks users to handle it)",
	"webpki::eku::ee-anyeku":                                    "anyExtendedKeyUsage serves serverAuth (as Go)",
	"webpki::eku::ee-without-eku":                               "no extended key usage serves any (as Go)",
	"webpki::eku::root-has-eku":                                 "a root's extended key usage is a downward constraint (as Go)",
	"webpki::cn::case-mismatch":                                 "the Common Name is ignored",
	"webpki::cn::ipv4-hex-mismatch":                             "the Common Name is ignored",
	"webpki::cn::ipv4-leading-zeros-mismatch":                   "the Common Name is ignored",
	"webpki::cn::ipv6-non-rfc5952-mismatch":                     "the Common Name is ignored",
	"webpki::cn::ipv6-uncompressed-mismatch":                    "the Common Name is ignored",
	"webpki::cn::ipv6-uppercase-mismatch":                       "the Common Name is ignored",
	"webpki::cn::not-in-san":                                    "the Common Name is ignored",
	"webpki::cn::punycode-not-in-san":                           "the Common Name is ignored",
	"webpki::cn::utf8-vs-punycode-mismatch":                     "the Common Name is ignored",
}

// x509Evidence is the Mk-evidence directory beside the repository.
func x509Evidence() string {
	if d := os.Getenv("MK_EVIDENCE"); d != "" {
		return d
	}
	return filepath.Join("..", "..", "Mk-evidence")
}

// x509Der is the DER of every CERTIFICATE block of text.
func x509Der(text string) [][]byte {
	var out [][]byte
	b := []byte(text)
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return out
		}
		if blk.Type == "CERTIFICATE" {
			out = append(out, blk.Bytes)
		}
	}
}

// TestX509_Limbo runs the pinned x509-limbo suite and records the pass
// rate; a disagreement not in x509LimboAllowed fails.
func TestX509_Limbo(t *testing.T) {
	path := filepath.Join(x509Evidence(), "x509", "limbo.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the limbo suite is not in Mk-evidence/x509 (set MK_EVIDENCE): %v", err)
	}
	var suite struct {
		Testcases []x509Limbo `json:"testcases"`
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	skipped := map[string]int{}
	var run []x509Limbo
	var records []string
	for _, c := range suite.Testcases {
		why := ""
		switch {
		case c.ValidationKind != "SERVER":
			why = "client validation (std.x509 checks a server's chain)"
		case slices.Contains(c.Features, "has-crl"):
			why = "revocation (not checked)"
		case slices.Contains(c.Features, "max-chain-depth"):
			why = "a chain depth limit of the caller's"
		case slices.Contains(c.Features, "name-constraint-dn"):
			why = "directoryName constraints"
		case len(c.ExtendedKeyUsage) > 0 && !slices.Equal(c.ExtendedKeyUsage, []string{"serverAuth"}):
			why = "an extended key usage other than serverAuth"
		case len(c.KeyUsage) > 0 && !slices.Contains(c.KeyUsage, "digitalSignature"):
			why = "a key usage other than digitalSignature"
		}
		if why != "" {
			skipped[why]++
			continue
		}
		peer := x509Der(c.PeerCertificate)
		if len(peer) != 1 {
			skipped["peer certificate not one PEM block"]++
			continue
		}
		var inter, roots [][]byte
		for _, s := range c.Intermediates {
			inter = append(inter, x509Der(s)...)
		}
		for _, s := range c.TrustedCerts {
			roots = append(roots, x509Der(s)...)
		}
		now := time.Now()
		if c.ValidationTime != nil {
			if now, err = time.Parse(time.RFC3339, *c.ValidationTime); err != nil {
				t.Fatalf("%s: %v", c.ID, err)
			}
		}
		host := ""
		if c.ExpectedPeerName != nil {
			host = c.ExpectedPeerName.Value
		}
		run = append(run, c)
		records = append(records, x509LimboRecord(now, host, append(peer, inter...), roots))
	}
	got := x509Ask(t, "release", records)
	pass, allowed := 0, 0
	var bad []string
	ran := map[string]bool{}
	for i, c := range run {
		ran[c.ID] = true
		if want, yes := x509LimboKinds[c.ID]; yes && got[i] != want {
			bad = append(bad, c.ID+": got "+got[i]+", want "+want)
		}
		ok := got[i] == "ok"
		if ok == (c.ExpectedResult == "SUCCESS") {
			pass++
			if _, yes := x509LimboAllowed[c.ID]; yes {
				bad = append(bad, c.ID+": allowed, but now agrees with the suite; remove it from x509LimboAllowed")
			}
			continue
		}
		if _, yes := x509LimboAllowed[c.ID]; yes {
			allowed++
			continue
		}
		bad = append(bad, c.ID+": expected "+c.ExpectedResult+", got "+got[i])
	}
	t.Logf("limbo: %d cases, %d run, %d agree, %d allowed, %d disagree; skipped %v", len(suite.Testcases), len(run), pass, allowed, len(bad), skipped)
	for id := range x509LimboKinds {
		if !ran[id] {
			bad = append(bad, id+": its error kind is pinned, but it did not run")
		}
	}
	for id := range x509LimboAllowed {
		if !ran[id] {
			bad = append(bad, id+": allowed, but not run; remove it from x509LimboAllowed")
		}
	}
	slices.Sort(bad)
	for _, b := range bad {
		t.Error(b)
	}
}

// x509LimboRecord is a "v" record whose certificates the driver may fail
// to parse: those of the pool and the intermediates are dropped by the
// driver's Certs as Go's AppendCertsFromPEM drops them.
func x509LimboRecord(now time.Time, host string, chain, roots [][]byte) string {
	return strings.Replace(x509VerifyRecord(now, host, chain, roots), "v ", "l ", 1)
}
