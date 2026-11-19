// Why: the strong-signature check must not hold a trusted root's own
// self-signature to the strong-signature policy.
// What: TestX509_StrongSignSha1Roots.
// How: every SHA-1 self-signed root of the user's ROOT store that Go trusts is
// given to the platform as a one-certificate chain; each must be trusted.

//go:build windows

package std

import (
	"crypto/x509"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestX509_StrongSignSha1Roots(t *testing.T) {
	var recs, names []string
	for _, der := range rt8StoreRoots(t) {
		c, err := x509.ParseCertificate(der)
		if err != nil || c.SignatureAlgorithm != x509.SHA1WithRSA {
			continue
		}
		if _, err := c.Verify(x509.VerifyOptions{CurrentTime: time.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			continue
		}
		recs = append(recs, fmt.Sprintf("b %d 1 %s", time.Now().Unix(), x509Hex(der)))
		names = append(names, c.Subject.CommonName+" / "+strings.Join(c.Subject.Organization, ","))
	}
	if len(recs) == 0 {
		t.Skip("no SHA-1 self-signed root of this store verifies under Go")
	}
	got := x509Answers(t, x509Modes, recs)
	for i := range recs {
		v, o := x509Bits(t, got[i])
		if v != 0 || o&x509TrustWeak != 0 {
			t.Errorf("%s: verdict %#x status %#x, want a trusted root with no weak bit", names[i], v, o)
		}
	}
	t.Logf("%d SHA-1 self-signed roots trusted, none weak", len(recs))
}
