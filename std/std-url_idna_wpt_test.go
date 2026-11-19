// Why: std.url's IDNA must pass the published conformance data, through the
// URL API and directly.
// What: TestStdUrl_ToAscii, TestStdUrl_IdnaTestV2Wpt and
// TestStdUrl_IdnaTestV2.
// How: wpt's toascii.json and IdnaTestV2.json through url.Parse and Url.Set;
// Unicode's IdnaTestV2.txt through DomainToAscii and DomainToUnicode.

package std

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// urlHostCase is one object of toascii.json or IdnaTestV2.json; Output nil
// is failure.
type urlHostCase struct {
	Comment string  `json:"comment"`
	Input   string  `json:"input"`
	Output  *string `json:"output"`
}

// urlHostCases reads one of the two files; comments are dropped.
func urlHostCases(t *testing.T, name string) []urlHostCase {
	t.Helper()
	var raw []json.RawMessage
	if err := json.Unmarshal(urlEvidence(t, name), &raw); err != nil {
		t.Fatal(err)
	}
	var out []urlHostCase
	for _, r := range raw {
		if len(r) > 0 && r[0] == '"' {
			continue
		}
		var c urlHostCase
		if err := json.Unmarshal(r, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// urlHostParseCheck is wpt's check of new URL("https://" + input + "/x").
func urlHostParseCheck(t *testing.T, c urlHostCase, answer string) string {
	t.Helper()
	u, ok := urlRead(t, answer)
	if c.Output == nil {
		if ok {
			return fmt.Sprintf("parsed to %q, want failure", u.href)
		}
		return ""
	}
	if !ok {
		return fmt.Sprintf("%s, want host %q", answer, *c.Output)
	}
	var bad []string
	for _, m := range []string{
		urlCheck("host", u.host(), *c.Output),
		urlCheck("hostname", u.hostname, *c.Output),
		urlCheck("pathname", u.path, "/x"),
		urlCheck("href", u.href, "https://"+*c.Output+"/x"),
	} {
		if m != "" {
			bad = append(bad, m)
		}
	}
	return strings.Join(bad, "; ")
}

// TestStdUrl_ToAscii: every case of toascii.json, in dev and release, as
// toascii.window.js runs it for URL: parsed inside "https://" and "/x",
// and set as host and as hostname on "https://x/x" (a refused value leaves
// "x").
func TestStdUrl_ToAscii(t *testing.T) {
	cases := urlHostCases(t, "toascii.json")
	var records []string
	for _, c := range cases {
		records = append(records,
			"parse "+urlHex("https://"+c.Input+"/x")+" -",
			"set "+urlHex("https://x/x")+" host "+urlHex(c.Input),
			"set "+urlHex("https://x/x")+" hostname "+urlHex(c.Input))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		failed := 0
		for i, c := range cases {
			if msg := urlHostParseCheck(t, c, answers[3*i]); msg != "" {
				t.Errorf("%s: %q: %s", mode, c.Input, msg)
				failed++
			}
			for k, part := range []string{"host", "hostname"} {
				u, ok := urlRead(t, answers[3*i+1+k])
				if !ok {
					t.Errorf("%s: %q as %s: %s", mode, c.Input, part, answers[3*i+1+k])
					failed++
					continue
				}
				got := u.hostname
				if part == "host" {
					got = u.host()
				}
				want := "x"
				if c.Output != nil {
					want = *c.Output
				}
				if got != want {
					t.Errorf("%s: %q as %s: got %q, want %q", mode, c.Input, part, got, want)
					failed++
				}
			}
		}
		t.Logf("%s: toascii.json: %d cases, %d checks, %d failed", mode, len(cases), 3*len(cases), failed)
	}
}

// TestStdUrl_IdnaTestV2Wpt: every case of wpt's IdnaTestV2.json (UTS 46's
// IdnaTestV2.txt as the URL Standard reads it), in dev and release, as
// IdnaTestV2.any.js runs it; an empty input is skipped there, so here.
func TestStdUrl_IdnaTestV2Wpt(t *testing.T) {
	cases := urlHostCases(t, "IdnaTestV2.json")
	var run []urlHostCase
	var records []string
	for _, c := range cases {
		if c.Input == "" {
			continue
		}
		run = append(run, c)
		records = append(records, "parse "+urlHex("https://"+c.Input+"/x")+" -")
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		failed := 0
		for i, c := range run {
			if msg := urlHostParseCheck(t, c, answers[i]); msg != "" {
				t.Errorf("%s: %q (%s): %s", mode, c.Input, c.Comment, msg)
				failed++
			}
		}
		t.Logf("%s: IdnaTestV2.json: %d cases, %d run (%d empty inputs skipped), %d failed", mode, len(cases), len(run), len(cases)-len(run), failed)
	}
}

// urlIdnaLine is one test line of IdnaTestV2.txt, its columns resolved by
// the file's blank-means rules.
type urlIdnaLine struct {
	line          int
	source        string
	toUnicode     string
	unicodeStatus []string
	toAsciiN      string
	asciiStatus   []string
}

// urlIdnaUnescape is a column with \uXXXX and \x{XXXX} replaced; ok is false
// for a surrogate, which a str cannot hold.
func urlIdnaUnescape(s string) (string, bool) {
	var b strings.Builder
	ok := true
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+5 < len(s) && s[i+1] == 'u' {
			v, err := strconv.ParseUint(s[i+2:i+6], 16, 32)
			if err == nil {
				if v >= 0xD800 && v <= 0xDFFF {
					ok = false
				}
				b.WriteRune(rune(v))
				i += 5
				continue
			}
		}
		if s[i] == '\\' && i+2 < len(s) && s[i+1] == 'x' && s[i+2] == '{' {
			if end := strings.IndexByte(s[i:], '}'); end > 0 {
				v, err := strconv.ParseUint(s[i+3:i+end], 16, 32)
				if err == nil {
					if v >= 0xD800 && v <= 0xDFFF {
						ok = false
					}
					b.WriteRune(rune(v))
					i += end
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return b.String(), ok
}

// urlIdnaStatus is a status column as its codes; "" is blank.
func urlIdnaStatus(s string) ([]string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	var codes []string
	for _, c := range strings.Split(s, ",") {
		if c = strings.TrimSpace(c); c != "" {
			codes = append(codes, c)
		}
	}
	return codes, true
}

// urlIdnaValue is a string column: blank is def, "" is the empty string.
func urlIdnaValue(s, def string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, true
	}
	if s == `""` {
		return "", true
	}
	return urlIdnaUnescape(s)
}

// urlIdnaLines reads IdnaTestV2.txt; lines with a surrogate are returned
// separately, since no str holds one.
func urlIdnaLines(t *testing.T) (lines []urlIdnaLine, surrogate []int, version string) {
	t.Helper()
	path := urlIdnaFile("IdnaTestV2.txt")
	if path == "" {
		t.Skip("Mk-evidence/unicode/idna/IdnaTestV2.txt not found (set MK_EVIDENCE)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	n := 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "# Version: "); ok {
			version = strings.TrimSpace(v)
		}
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, ";")
		if len(f) < 7 {
			t.Fatalf("IdnaTestV2.txt:%d: %d columns", n, len(f))
		}
		src, ok1 := urlIdnaValue(f[0], "")
		uni, ok2 := urlIdnaValue(f[1], src)
		ascii, ok3 := urlIdnaValue(f[3], uni)
		ok := ok1 && ok2 && ok3
		us, _ := urlIdnaStatus(f[2])
		as, explicit := urlIdnaStatus(f[4])
		if !explicit {
			as = us
		}
		if !ok {
			surrogate = append(surrogate, n)
			continue
		}
		lines = append(lines, urlIdnaLine{line: n, source: src, toUnicode: uni, unicodeStatus: us, toAsciiN: ascii, asciiStatus: as})
	}
	return lines, surrogate, version
}

// urlIdnaErrs is whether codes holds a code outside ignored.
func urlIdnaErrs(codes []string, ignored ...string) bool {
	for _, c := range codes {
		skip := false
		for _, g := range ignored {
			if c == g {
				skip = true
			}
		}
		if !skip {
			return true
		}
	}
	return false
}

// urlIdnaXnLabel is whether a label of s starts "xn--": converted from
// Punycode, it fails validity criterion 4 when CheckHyphens is false,
// which the file's V2 and V3 (ignored without CheckHyphens) do not cover.
func urlIdnaXnLabel(s string) bool {
	for _, l := range strings.Split(s, ".") {
		if strings.HasPrefix(l, "xn--") {
			return true
		}
	}
	return false
}

// The status codes each setting ignores (IdnaTestV2.txt's conformance
// section). X4_2 marks an empty label in toUnicode, which the file
// introduced in place of A4_2: it belongs to VerifyDnsLength, which
// ToUnicode does not take, so it is ignored in every ToUnicode check.
var (
	urlIdnaIgnoreStrictUnicode = []string{"X4_2"}
	urlIdnaIgnoreLaxUnicode    = []string{"X4_2", "V2", "V3", "U1"}
	urlIdnaIgnoreLaxAscii      = []string{"X4_2", "V2", "V3", "U1", "A4_1", "A4_2"}
)

// TestStdUrl_IdnaTestV2: every line of IdnaTestV2.txt but the two with an
// unpaired surrogate (a str holds scalar values only; the file allows
// skipping them), in dev and release, five ways: DomainToAscii strict
// against toAsciiN (every flag on, the file's own settings); UTS 46's
// processing with every flag on against toUnicode; the processing and
// ToASCII with the URL Standard's lax flags (CheckHyphens,
// UseSTD3ASCIIRules and VerifyDnsLength off), the file's codes for those
// flags ignored; and DomainToUnicode, which returns its input on an error.
// Transitional processing is off, so toAsciiT is not run.
func TestStdUrl_IdnaTestV2(t *testing.T) {
	lines, surrogate, version := urlIdnaLines(t)
	if len(surrogate) != 2 {
		t.Errorf("IdnaTestV2.txt %s: %d lines with a surrogate (%v), the test expects 2", version, len(surrogate), surrogate)
	}
	var records []string
	for _, l := range lines {
		h := urlHex(l.source)
		records = append(records, "dta "+h+" 1", "u46 "+h+" 1", "u46 "+h+" 0", "u46a "+h+" 0", "dtu "+h)
	}
	for _, mode := range urlModes {
		answers := urlIdnaAsk(t, mode, records)
		failed := 0
		fail := func(l urlIdnaLine, what, got, want string) {
			failed++
			if failed <= 60 {
				t.Errorf("%s: IdnaTestV2.txt:%d %q: %s: got %s, want %s", mode, l.line, l.source, what, got, want)
			}
		}
		for i, l := range lines {
			a := answers[5*i : 5*i+5]
			// DomainToAscii, strict.
			want := "err BadHost"
			if !urlIdnaErrs(l.asciiStatus) {
				want = "ok " + urlHex(l.toAsciiN)
			}
			if a[0] != want {
				fail(l, "DomainToAscii strict", a[0], want)
			}
			// The processing, strict and lax.
			lax := urlIdnaErrs(l.unicodeStatus, urlIdnaIgnoreLaxUnicode...) || urlIdnaXnLabel(l.toUnicode)
			for k, errs := range []bool{urlIdnaErrs(l.unicodeStatus, urlIdnaIgnoreStrictUnicode...), lax} {
				want := fmt.Sprintf("ok %v %s", !errs, urlHex(l.toUnicode))
				if a[1+k] != want {
					fail(l, fmt.Sprintf("Uts46 strict=%v", k == 0), a[1+k], want)
				}
			}
			// ToASCII, lax.
			want = "ok none"
			if !urlIdnaErrs(l.asciiStatus, urlIdnaIgnoreLaxAscii...) && !urlIdnaXnLabel(l.toUnicode) {
				want = "ok " + urlHex(l.toAsciiN)
			}
			if a[3] != want {
				fail(l, "Uts46ToAscii lax", a[3], want)
			}
			// DomainToUnicode.
			want = "ok " + urlHex(l.source)
			if !lax {
				want = "ok " + urlHex(l.toUnicode)
			}
			if a[4] != want {
				fail(l, "DomainToUnicode", a[4], want)
			}
		}
		t.Logf("%s: IdnaTestV2.txt %s: %d lines run five ways (%d with a surrogate skipped), %d checks failed", mode, version, len(lines), len(surrogate), failed)
	}
}
