// Why: on Windows x509.Verify hands the chain to the platform, which must be
// tested without touching the network.
// What: the TestX509_Platform* tests (wrong name, unknown authority, a recorded
// chain), TestX509_StoreRootsNamesMatchGo and TestX509_CacheMarkRefusals.
// How: the platform cases go through FxVerifyCached, which asks
// CertGetCertificateChain for cache-only retrieval with no root auto-update;
// the recorded chain is Go's own chain to GTS Root R1.

//go:build windows

package std

import (
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

// x509GtsRootR1 is GTS Root R1's SHA-1 thumbprint.
const x509GtsRootR1 = "e58c1cc4913b38634be9106ee3ad8e6b9dd9814a"

// x509PlatformRecord is an "s" record: chain against SystemRoots.
func x509PlatformRecord(now time.Time, host string, chain [][]byte) string {
	r := x509VerifyRecord(now, host, chain, nil)
	return "s" + r[1:len(r)-2]
}

// x509CachedRecord is a "c" record: chain against SystemRoots, the
// platform's retrieval from its cache only.
func x509CachedRecord(now time.Time, host string, chain [][]byte) string {
	return "c" + x509PlatformRecord(now, host, chain)[1:]
}

// TestX509_PlatformWrongName: a wrong host is refused by std.x509's own
// check before the platform is asked.
func TestX509_PlatformWrongName(t *testing.T) {
	fx := x509Fixtures(t)
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := []string{
		x509PlatformRecord(now, "wrong.test", fx["chain.pem"]),
		x509PlatformRecord(now, "client.test", fx["client-leaf.pem"]),
	}
	want := []string{"err NameMismatch", "err BadUsage"}
	got := x509Ask(t, "release", rec)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("case %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

// TestX509_PlatformUnknownAuthority: the checked-in test root is not in
// the host's store, so the platform refuses the chain.
func TestX509_PlatformUnknownAuthority(t *testing.T) {
	fx := x509Fixtures(t)
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	got := x509Ask(t, "release", []string{x509CachedRecord(now, "example.test", fx["chain.pem"])})
	if got[0] != "err UnknownAuthority" {
		t.Errorf("got %s, want err UnknownAuthority", got[0])
	}
}

// TestX509_PlatformRecordedChain: Go's recorded www.google.com chain at its
// recording time is trusted by the platform when this host holds GTS
// Root R1. The chain is Go's own (crypto/x509 verify_test.go: googleLeaf,
// gtsIntermediate and gtsRoot, BSD licence), verified there through the
// platform at Unix time 1677615892.
func TestX509_PlatformRecordedChain(t *testing.T) {
	held := false
	for _, d := range rt8StoreRoots(t) {
		s := sha1.Sum(d)
		if hex.EncodeToString(s[:]) == x509GtsRootR1 {
			held = true
		}
	}
	if !held {
		t.Skipf("this host's ROOT store does not hold GTS Root R1 (%s)", x509GtsRootR1)
	}
	fx := x509Fixtures(t)
	chain := fx["google-chain.pem"]
	got := x509Ask(t, "release", []string{x509CachedRecord(time.Unix(1677615892, 0), "www.google.com", chain[:2])})
	if got[0] != "ok" {
		t.Errorf("got %s, want ok", got[0])
	}
}

// x509GoName is Go's own text of a Name: pkix.RDNSequence.String.
func x509GoName(raw []byte) string {
	var r pkix.RDNSequence
	if rest, err := asn1.Unmarshal(raw, &r); err != nil || len(rest) > 0 {
		return "<bad name>"
	}
	return r.String()
}

// x509UnnamedText is whether the Name raw holds a text value of an
// attribute outside Go's nine short names.
func x509UnnamedText(raw []byte) bool {
	var r pkix.RDNSequence
	if _, err := asn1.Unmarshal(raw, &r); err != nil {
		return false
	}
	for _, rdn := range r {
		for _, tv := range rdn {
			if _, ok := x509ShortNames[tv.Type.String()]; ok {
				continue
			}
			if _, ok := tv.Value.(string); ok {
				return true
			}
		}
	}
	return false
}

// TestX509_StoreRootsNamesMatchGo: Subject and Issuer of every certificate
// of this user's ROOT store, which Go parses, read as Go's
// pkix.RDNSequence.String writes them (real names: emailAddress,
// organizationIdentifier and other attributes with no short name).
func TestX509_StoreRootsNamesMatchGo(t *testing.T) {
	var recs []string
	var want []string
	unnamed := 0
	for _, d := range rt8StoreRoots(t) {
		c, err := x509.ParseCertificate(d)
		if err != nil {
			continue
		}
		recs = append(recs, "p "+x509Hex(d))
		want = append(want, x509GoName(c.RawSubject)+"|"+x509GoName(c.RawIssuer))
		if x509UnnamedText(c.RawSubject) || x509UnnamedText(c.RawIssuer) {
			unnamed++
		}
	}
	t.Logf("%d roots carry a text attribute with no short name (the rule names.mk's AttrText changed for)", unnamed)
	if len(recs) == 0 {
		t.Skip("this user's ROOT store holds no certificate Go parses")
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, recs)
		bad := 0
		for i := range want {
			parts := strings.SplitN(strings.TrimPrefix(got[i], "ok "), "|", 3)
			if len(parts) < 2 || parts[0]+"|"+parts[1] != want[i] {
				bad++
				if bad <= 5 {
					t.Errorf("%s: root %d: got %q, want %q", mode, i, got[i], want[i])
				}
			}
		}
		t.Logf("%s: %d roots, %d differ", mode, len(want), bad)
	}
}

// TestX509_CacheMarkRefusals: the cache-only mark is reachable only
// through rt.CertVerifyCached. rt.CertVerify given a marked request with a
// valid record answers Other, as before the mark existed; CertVerifyCached
// given the record without the mark answers Other; given both, the
// platform answers (err None, its verdict in value).
func TestX509_CacheMarkRefusals(t *testing.T) {
	fx := x509Fixtures(t)
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, []string{"r " + x509Hex(fx["chain.pem"][0])})
		if got[0] != "ok Other Other None" {
			t.Errorf("%s: got %s, want ok Other Other None", mode, got[0])
		}
	}
}
