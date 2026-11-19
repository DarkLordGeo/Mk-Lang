// Why: std.x509's name check decides which host a certificate is good for.
// What: TestX509_IpMatchesNetip and TestX509_HostMatchesGo.
// How: the IP reader against netip.ParseAddr (zones aside), the host check
// against Certificate.VerifyHostname on edge and generated hosts.

package std

import (
	"crypto/x509"
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"
)

// x509IpWant is what FxIp must print for s.
func x509IpWant(s string) string {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "none"
	}
	b := a.As16()
	return "ok " + hex.EncodeToString(b[:])
}

// TestX509_IpMatchesNetip: IpOf reads what netip reads, on edge cases and
// generated strings.
func TestX509_IpMatchesNetip(t *testing.T) {
	cases := []string{"", "1.2.3.4", "255.255.255.255", "256.1.1.1", "01.2.3.4", "1.2.3", "1.2.3.4.5", "1..2.3", " 1.2.3.4",
		"::", "::1", "1::", "1::2", "1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7::", "::2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8:9", ":::", "1:::2",
		"::ffff:1.2.3.4", "::1.2.3.4", "1:2:3:4:5:6:1.2.3.4", "1:2:3:4:5:6:7:1.2.3.4", "fe80::1%eth0", "12345::", "g::",
		"0:0:0:0:0:0:0:1", "1:2::3:4:5::6", "2001:db8::", "[::1]", "1.2.3.4:80", "::1:", ":1::", "1:2:3:4:5:6::7:8"}
	r := &encRng{s: 7}
	alpha := "0123456789abcdefABCDEF:.%"
	for k := 0; k < 3000; k++ {
		n := 1 + r.intn(24)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteByte(alpha[r.intn(len(alpha))])
		}
		cases = append(cases, b.String())
	}
	var records []string
	for _, c := range cases {
		records = append(records, "i "+x509Hex([]byte(c)))
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		bad := 0
		for i, c := range cases {
			if want := x509IpWant(c); got[i] != want {
				t.Errorf("%s IpOf(%q): got %s, want %s", mode, c, got[i], want)
				if bad++; bad == 10 {
					t.FailNow()
				}
			}
		}
	}
}

// TestX509_HostMatchesGo: the host check agrees with Go's VerifyHostname
// on the fixtures' leaves.
func TestX509_HostMatchesGo(t *testing.T) {
	fx := x509Fixtures(t)
	hosts := []string{"example.test", "EXAMPLE.test", "example.test.", "example.test..", ".example.test", "x.example.test",
		"foo.wild.test", "FOO.Wild.Test", "a.b.wild.test", "wild.test", "*.wild.test", "foo.wild.test.", "_x.wild.test",
		"127.0.0.1", "[127.0.0.1]", "::1", "[::1]", "::ffff:127.0.0.1", "0:0::1", "127.0.0.2", "1.2.3.4", "",
		".", "..", "*", "-x.wild.test", "x-.wild.test", "é.wild.test", "a.allowed.test", "www.google.com", "WWW.GOOGLE.COM", "google.com"}
	var records, want []string
	for name, ders := range fx {
		d := ders[0]
		c, err := x509.ParseCertificate(d)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, h := range hosts {
			if h == "" {
				continue
			}
			records = append(records, "h "+x509Hex([]byte(h))+" "+x509Hex(d))
			if c.VerifyHostname(h) == nil {
				want = append(want, "ok true")
			} else {
				want = append(want, "ok false")
			}
		}
	}
	for _, mode := range x509Modes {
		got := x509Ask(t, mode, records)
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s %s: got %s, want %s", mode, records[i][:min(60, len(records[i]))], got[i], want[i])
			}
		}
	}
}
