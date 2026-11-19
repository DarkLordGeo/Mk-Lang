// Why: IDNA processing reads hostile domains and must never panic, hang or
// break its invariants.
// What: TestStdUrl_IdnaAdversarial and FuzzStdUrl_DomainToAscii over
// url.DomainToAscii, strict and lax, and url.DomainToUnicode.
// How: a lax result is ASCII, the lax parser is idempotent and reads back
// DomainToUnicode's result; seeds from toascii.json and IdnaTestV2.json.
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// urlIdnaBig is one adversarial domain: prefix, unit repeated count times,
// suffix, through fn ("dta1", "dta0" or "dtu"), and the answer's prefix
// it must have.
type urlIdnaBig struct {
	name, fn, prefix, unit, suffix string
	count                          int
	want                           string
}

func urlIdnaBigCases() []urlIdnaBig {
	long := strings.Repeat("a", 64)
	return []urlIdnaBig{
		// U+FDFA maps to 18 scalars: the map's largest growth.
		{"U+FDFA x 100000, lax", "dta0", "", "ﷺ", "", 100000, "err BadHost"},
		{"U+FDFA x 100000, unicode", "dtu", "", "ﷺ", "", 100000, "ok 300000 "},
		// One label of 200,000 Arabic letters: Bidi, Punycode both ways.
		{"alef x 200000, lax", "dta0", "", "ا", ".com", 200000, "ok "},
		{"alef x 200000, strict", "dta1", "", "ا", ".com", 200000, "err BadHost"},
		// Joiners between transparent runs: CheckJoiners stays linear.
		{"beh, fathatan runs and ZWNJ", "dta0", "ب", strings.Repeat("ً", 500) + "‌" + strings.Repeat("ً", 500) + "ب", "", 200, "ok "},
		{"ZWNJ x 100000 after beh", "dta0", "ب", "‌", "", 100000, "err BadHost"},
		{"ZWJ after virama x 50000", "dta0", "", "क्‍", "", 50000, "ok "},
		// A mark run NFC sorts: std.text's counting sort.
		{"u and 100000 marks", "dta0", "u", "̣́", "", 50000, "ok "},
		{"leading mark", "dta0", "", "́", "", 100000, "err BadHost"},
		// 200,000 labels.
		{"200000 labels", "dta0", "", "ü.", "x", 200000, "ok "},
		{"200000 empty labels, strict", "dta1", "", ".", "ü", 200000, "err BadHost"},
		// Punycode labels: long, overflowing, and many.
		{"xn-- and 100000 digits, unicode", "dtu", "xn--", "9", "", 100000, "ok 100004 786e2d2d39393939"},
		{"xn--ls8h x 100000, unicode", "dtu", "", "xn--ls8h.", "", 100000, "ok 500000 "},
		{"xn--ls8h x 100000, strict", "dta1", "", "xn--ls8h.", "a", 100000, "err BadHost"},
		{"long labels, strict", "dta1", "", long + "ü.", "", 1000, "err BadHost"},
		{"ASCII 1 MB, lax", "dta0", "", "ABCDEFGH", "", 125000, "ok 1000000 6162636465666768"},
	}
}

// TestStdUrl_IdnaAdversarial: each hostile domain, release, within its
// time bound.
func TestStdUrl_IdnaAdversarial(t *testing.T) {
	cases := urlIdnaBigCases()
	var records []string
	for _, c := range cases {
		records = append(records, strings.Join([]string{"big", c.fn, urlHex(c.prefix), urlHex(c.unit), urlItoa(c.count), urlHex(c.suffix)}, " "))
	}
	urlExeOf(t, urlIdnaBuild, "release")
	for i, c := range cases {
		start := time.Now()
		answer := urlIdnaAsk(t, "release", records[i:i+1])[0]
		if !strings.HasPrefix(answer, c.want) {
			t.Errorf("%s: got %s, want %s...", c.name, urlShortText(answer), c.want)
		}
		t.Logf("%s: %v", c.name, time.Since(start))
	}
}

func urlItoa(n int) string {
	var b []byte
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// urlIdnaFuzzOne runs records through the release IDNA driver; a crash
// fails.
func urlIdnaFuzzOne(t *testing.T, records ...string) []string {
	t.Helper()
	exe := urlExeOf(t, urlIdnaBuild, "release")
	r := runNative(t, exe, strings.Join(records, "\n")+"\n")
	got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
	if r.code != 0 || len(got) != len(records) {
		t.Fatalf("%s\n exit %d, stderr %s\n answer %s", urlShortText(strings.Join(records, " | ")), r.code, r.stderr, urlShortText(r.stdout))
	}
	return got
}

func FuzzStdUrl_DomainToAscii(f *testing.F) {
	// Every toascii.json input and every eighth of IdnaTestV2.json's (a
	// seed costs up to five driver runs, and go test runs every seed).
	for k, name := range []string{"toascii.json", "IdnaTestV2.json"} {
		var raw []json.RawMessage
		if b := urlEvidenceRead(name); b != nil && json.Unmarshal(b, &raw) == nil {
			for i, r := range raw {
				var c urlHostCase
				if json.Unmarshal(r, &c) == nil && (k == 0 || i%8 == 0) {
					f.Add(c.Input)
				}
			}
		}
	}
	f.Add("xn--fa-hia.de")
	f.Fuzz(func(t *testing.T, domain string) {
		if len(domain) > 20000 || !utf8.ValidString(domain) {
			return
		}
		h := urlHex(domain)
		a := urlIdnaFuzzOne(t, "dta "+h+" 0", "dta "+h+" 1", "dtu "+h)
		for _, x := range a {
			if !strings.HasPrefix(x, "ok ") && x != "err BadHost" {
				t.Fatalf("%q: %s", domain, urlShortText(x))
			}
		}
		lax, ok := strings.CutPrefix(a[0], "ok ")
		if !ok {
			return
		}
		r := urlUnhex(t, lax)
		for i := 0; i < len(r); i++ {
			if r[i] >= 0x80 {
				t.Fatalf("%q: lax result %q is not ASCII", domain, r)
			}
		}
		// The lax parser is idempotent, and reads back its result's
		// Unicode form.
		b := urlIdnaFuzzOne(t, "dta "+lax+" 0", "dtu "+lax)
		if b[0] != a[0] {
			t.Fatalf("%q: lax result %q parses to %s", domain, r, urlShortText(b[0]))
		}
		u := urlUnhex(t, strings.TrimPrefix(b[1], "ok "))
		if c := urlIdnaFuzzOne(t, "dta "+urlHex(u)+" 0"); c[0] != a[0] {
			t.Fatalf("%q: lax result %q, its Unicode form %q parses to %s", domain, r, u, urlShortText(c[0]))
		}
	})
}
