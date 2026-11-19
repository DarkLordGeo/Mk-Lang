// Why: std.url must pass web-platform-tests' URL data, the data browsers test
// against.
// What: TestStdUrl_UrlTestData, TestStdUrl_Setters, TestStdUrl_IdnaCutForm and
// TestStdUrl_PercentEncoding.
// How: every case of urltestdata.json, setters_tests.json and
// percent-encoding.json through the driver in dev and release.

package std

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// urlEvidenceRead reads a file of Mk-evidence/whatwg/url ($MK_EVIDENCE,
// else next to the repository), or nil when there is none.
func urlEvidenceRead(name string) []byte {
	root := os.Getenv("MK_EVIDENCE")
	if root == "" {
		root = filepath.Join("..", "..", "Mk-evidence")
	}
	b, err := os.ReadFile(filepath.Join(root, "whatwg", "url", name))
	if err != nil {
		return nil
	}
	return b
}

// urlEvidence is urlEvidenceRead for a test; a missing copy skips it.
func urlEvidence(t *testing.T, name string) []byte {
	t.Helper()
	b := urlEvidenceRead(name)
	if b == nil {
		t.Skipf("Mk-evidence/whatwg/url/%s not found (set MK_EVIDENCE)", name)
	}
	return b
}

// urlNeedsIdna is the filter for builds without IDNA: text whose host part could
// need UTS 46, judged without the parser: a non-ASCII code point or "xn--"
// in any case anywhere in it once percent-decoded. A case is skipped only
// when this holds for its input or base and Mk answered IdnaUnsupported;
// IdnaUnsupported on any other case fails.
func urlNeedsIdna(s string) bool {
	d := pctDecodeModel([]byte(s))
	for _, c := range d {
		if c >= 0x80 {
			return true
		}
	}
	return strings.Contains(strings.ToLower(string(d)), "xn--")
}

// urlIdnaBuilt is whether this tree builds IDNA: std/url/idna.mk refuses
// every domain IDNA would process when it does not.
func urlIdnaBuilt(t *testing.T) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("url", "idna.mk"))
	if err != nil {
		t.Fatal(err)
	}
	return !strings.Contains(string(b), "fail IdnaUnsupported")
}

// urlCase is one object of urltestdata.json.
type urlCase struct {
	Input        string  `json:"input"`
	Base         *string `json:"base"`
	Href         string  `json:"href"`
	Origin       *string `json:"origin"`
	Protocol     string  `json:"protocol"`
	Username     string  `json:"username"`
	Password     string  `json:"password"`
	Host         string  `json:"host"`
	Hostname     string  `json:"hostname"`
	Port         string  `json:"port"`
	Pathname     string  `json:"pathname"`
	Search       string  `json:"search"`
	Hash         string  `json:"hash"`
	SearchParams *string `json:"searchParams"`
	Failure      bool    `json:"failure"`
}

// urlTestData is urltestdata.json's cases; comments are dropped.
func urlTestData(t *testing.T) []urlCase {
	t.Helper()
	var raw []json.RawMessage
	if err := json.Unmarshal(urlEvidence(t, "urltestdata.json"), &raw); err != nil {
		t.Fatal(err)
	}
	var out []urlCase
	for _, r := range raw {
		if len(r) > 0 && r[0] == '"' {
			continue
		}
		var c urlCase
		if err := json.Unmarshal(r, &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// urlRecord is the driver record that parses c.
func urlRecord(c urlCase) string {
	base := "-"
	if c.Base != nil {
		base = urlHex(*c.Base)
	}
	return "parse " + urlHex(c.Input) + " " + base
}

// urlCompare checks a shown URL against an expected one field by field,
// with the invariants every URL keeps: href parses back to itself and
// Format is href without the password.
func urlCompare(u urlShown, want urlCase) []string {
	var bad []string
	add := func(m string) {
		if m != "" {
			bad = append(bad, m)
		}
	}
	add(urlCheck("href", u.href, want.Href))
	if want.Origin != nil {
		add(urlCheck("origin", u.origin, *want.Origin))
	}
	add(urlCheck("protocol", u.scheme+":", want.Protocol))
	add(urlCheck("username", u.username, want.Username))
	add(urlCheck("password", u.password, want.Password))
	add(urlCheck("host", u.host(), want.Host))
	add(urlCheck("hostname", u.hostname, want.Hostname))
	add(urlCheck("port", u.portText(), want.Port))
	add(urlCheck("pathname", u.path, want.Pathname))
	add(urlCheck("search", u.search(), want.Search))
	add(urlCheck("hash", u.hash(), want.Hash))
	// Host()'s kind and payload (std-url_regress_test.go).
	add(urlCheck("host kind", u.kind, urlHostKindModel(u.scheme, u.hostname)))
	if !u.reparses {
		bad = append(bad, "href does not parse back to itself")
	}
	format := u.href
	if u.password != "" {
		format = strings.Replace(u.href, ":"+u.password+"@", "@", 1)
	}
	add(urlCheck("format", u.format, format))
	return bad
}

// TestStdUrl_UrlTestData: every case of urltestdata.json, in dev and
// release: a failure fails, and every other case gives every accessor the
// expected value, Host()'s kind and payload against urlHostKindModel
// (std-url_regress_test.go), and searchParams through ParseQuery and FormatQuery.
func TestStdUrl_UrlTestData(t *testing.T) {
	idna := urlIdnaBuilt(t)
	for _, mode := range urlModes {
		urlRunTestData(t, urlMain, mode, idna)
	}
}

// urlRunTestData runs urltestdata.json through b's driver in mode, idna
// saying whether b builds IDNA, and returns the counts it logs.
func urlRunTestData(t *testing.T, b urlBuild, mode string, idna bool) (passed, skipped, failed int) {
	t.Helper()
	cases := urlTestData(t)
	var records []string
	for _, c := range cases {
		records = append(records, urlRecord(c))
	}
	{
		answers := urlAskOf(t, b, mode, records)
		replaced := 0
		var qrecords []string
		var qwant []string
		for i, c := range cases {
			a := answers[i]
			name := c.Input
			if c.Base != nil {
				name += " against " + *c.Base
			}
			if strings.Contains(a, "IdnaUnsupported") && !idna && (urlNeedsIdna(c.Input) || c.Base != nil && urlNeedsIdna(*c.Base)) {
				skipped++
				continue
			}
			if !utf8.ValidString(c.Input) || c.Base != nil && !utf8.ValidString(*c.Base) {
				// A lone surrogate: JSON's \uD800 decodes to U+FFFD in Go,
				// so the case is not the file's; it is not run.
				skipped++
				continue
			}
			u, ok := urlRead(t, a)
			if c.Failure {
				if ok {
					t.Errorf("%s: %s: parsed to %q, want failure", mode, name, u.href)
					failed++
				} else {
					passed++
				}
				continue
			}
			if !ok {
				t.Errorf("%s: %s: %s, want %q", mode, name, a, c.Href)
				failed++
				continue
			}
			if bad := urlCompare(u, c); len(bad) > 0 {
				t.Errorf("%s: %s:\n    %s", mode, name, strings.Join(bad, "\n    "))
				failed++
				continue
			}
			passed++
			if c.SearchParams != nil {
				qrecords = append(qrecords, "pq "+urlHex(u.query))
				qwant = append(qwant, *c.SearchParams)
			}
		}
		// searchParams: the query parsed, then serialized again.
		qanswers := urlAskOf(t, b, mode, qrecords)
		var frecords []string
		for _, a := range qanswers {
			frecords = append(frecords, "fq "+urlPairsArg(a))
		}
		fanswers := urlAskOf(t, b, mode, frecords)
		for i, a := range fanswers {
			got := strings.TrimPrefix(a, "ok ")
			if urlUnhex(t, got) != qwant[i] {
				t.Errorf("%s: searchParams of %q: got %q, want %q", mode, urlUnhex(t, strings.TrimPrefix(qrecords[i], "pq ")), urlUnhex(t, got), qwant[i])
				failed++
			}
		}
		t.Logf("%s %s: urltestdata.json: %d cases, %d passed (%d holding U+FFFD), %d skipped by the cut form's IDNA filter, %d failed", b.key, mode, len(cases), passed, replaced, skipped, failed)
	}
	return passed, skipped, failed
}

// urlPairsArg turns a pq answer into an fq argument.
func urlPairsArg(answer string) string {
	f := strings.Fields(answer)
	if len(f) <= 1 {
		return "-"
	}
	return strings.Join(f[1:], ",")
}

// TestStdUrl_Setters: every case of setters_tests.json, in dev and
// release: the URL parsed from href, the part set, and each expected getter.
func TestStdUrl_Setters(t *testing.T) {
	idna := urlIdnaBuilt(t)
	for _, mode := range urlModes {
		urlRunSetters(t, urlMain, mode, idna)
	}
}

// urlRunSetters runs setters_tests.json through b's driver in mode, idna
// saying whether b builds IDNA, and returns the counts it logs.
func urlRunSetters(t *testing.T, b urlBuild, mode string, idna bool) (passed, skipped, failed int) {
	t.Helper()
	var file map[string]json.RawMessage
	if err := json.Unmarshal(urlEvidence(t, "setters_tests.json"), &file); err != nil {
		t.Fatal(err)
	}
	type setCase struct {
		Href     string            `json:"href"`
		NewValue string            `json:"new_value"`
		Expected map[string]string `json:"expected"`
	}
	type named struct {
		part string
		c    setCase
	}
	var cases []named
	parts := []string{"protocol", "username", "password", "host", "hostname", "port", "pathname", "search", "hash", "href"}
	for _, part := range parts {
		raw, ok := file[part]
		if !ok {
			continue
		}
		var cs []setCase
		if err := json.Unmarshal(raw, &cs); err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			cases = append(cases, named{part, c})
		}
	}
	var records []string
	for _, n := range cases {
		records = append(records, "set "+urlHex(n.c.Href)+" "+n.part+" "+urlHex(n.c.NewValue))
	}
	{
		answers := urlAskOf(t, b, mode, records)
		for i, n := range cases {
			a := answers[i]
			name := n.part + " = " + n.c.NewValue + " on " + n.c.Href
			if strings.Contains(a, "IdnaUnsupported") && !idna && (urlNeedsIdna(n.c.Href) || urlNeedsIdna(n.c.NewValue)) {
				skipped++
				continue
			}
			u, ok := urlRead(t, a)
			if !ok {
				t.Errorf("%s: %s: %s", mode, name, a)
				failed++
				continue
			}
			var bad []string
			for key, want := range n.c.Expected {
				var got string
				switch key {
				case "href":
					got = u.href
				case "protocol":
					got = u.scheme + ":"
				case "username":
					got = u.username
				case "password":
					got = u.password
				case "host":
					got = u.host()
				case "hostname":
					got = u.hostname
				case "port":
					got = u.portText()
				case "pathname":
					got = u.path
				case "search":
					got = u.search()
				case "hash":
					got = u.hash()
				case "origin":
					got = u.origin
				default:
					t.Fatalf("unknown getter %q", key)
				}
				if m := urlCheck(key, got, want); m != "" {
					bad = append(bad, m)
				}
			}
			// Host()'s kind and payload after the setter.
			if m := urlCheck("host kind", u.kind, urlHostKindModel(u.scheme, u.hostname)); m != "" {
				bad = append(bad, m)
			}
			if !u.reparses {
				bad = append(bad, "href does not parse back to itself")
			}
			if len(bad) > 0 {
				t.Errorf("%s: %s:\n    %s", mode, name, strings.Join(bad, "\n    "))
				failed++
				continue
			}
			passed++
		}
		t.Logf("%s %s: setters_tests.json: %d cases, %d passed, %d skipped by the cut form's IDNA filter, %d failed", b.key, mode, len(cases), passed, skipped, failed)
	}
	return passed, skipped, failed
}

// TestStdUrl_IdnaCutForm: std.url with IDNA cut (std/url/idna_*.mk
// deleted, idna.mk in its refusal form, urlCutSeam), in release: it
// builds, and urltestdata.json and setters_tests.json pass but for the
// cases the IDNA filter skips, 36 and 9.
func TestStdUrl_IdnaCutForm(t *testing.T) {
	_, skipped, failed := urlRunTestData(t, urlCutBuild, "release", false)
	if skipped != 36 || failed != 0 {
		t.Errorf("cut form: urltestdata.json %d skipped and %d failed, want 36 and 0", skipped, failed)
	}
	_, skipped, failed = urlRunSetters(t, urlCutBuild, "release", false)
	if skipped != 9 || failed != 0 {
		t.Errorf("cut form: setters_tests.json %d skipped and %d failed, want 9 and 0", skipped, failed)
	}
}

// urlPercentSkipped are percent-encoding.json's output keys for encodings
// std.encoding does not build (the CJK set), skipped
// by name; the keys were confirmed against the pinned copy (wpt 4561f06).
var urlPercentSkipped = map[string]bool{"big5": true, "euc-kr": true, "iso-2022-jp": true, "gb18030": true, "shift_jis": true}

// TestStdUrl_PercentEncoding: percent-encoding.json's outputs for utf-8
// and the single-byte encodings. utf-8 runs through the parser, as wpt
// does: the input as the query of a special URL. A single-byte encoding
// is percent-encode after encoding with the special-query set, composed in
// the driver from Encoding.Encode and std.url's encoder, since url.Parse is
// UTF-8 only.
func TestStdUrl_PercentEncoding(t *testing.T) {
	var raw []json.RawMessage
	if err := json.Unmarshal(urlEvidence(t, "percent-encoding.json"), &raw); err != nil {
		t.Fatal(err)
	}
	type pctCase struct {
		Input  string            `json:"input"`
		Output map[string]string `json:"output"`
	}
	var records, wants, names []string
	keys := map[string]bool{}
	for _, r := range raw {
		if len(r) > 0 && r[0] == '"' {
			continue
		}
		var c pctCase
		if err := json.Unmarshal(r, &c); err != nil {
			t.Fatal(err)
		}
		for enc, out := range c.Output {
			keys[enc] = true
			if urlPercentSkipped[enc] {
				continue
			}
			if enc == "utf-8" {
				records = append(records, "parse "+urlHex("https://doesnotmatter.invalid/?"+c.Input)+" -")
			} else {
				records = append(records, "pcte "+enc+" "+urlHex(c.Input))
			}
			wants = append(wants, out)
			names = append(names, enc+" "+c.Input)
		}
	}
	for k := range urlPercentSkipped {
		if !keys[k] {
			t.Errorf("skip list names %q, which the pinned file does not have", k)
		}
	}
	for k := range keys {
		if k != "utf-8" && k != "windows-1252" && k != "iso-8859-2" && !urlPercentSkipped[k] {
			t.Errorf("percent-encoding.json has key %q, neither run nor skipped", k)
		}
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, a := range answers {
			var got string
			if strings.HasPrefix(records[i], "parse ") {
				u, ok := urlRead(t, a)
				if !ok {
					t.Errorf("%s: %s: %s", mode, names[i], a)
					continue
				}
				got = u.query
			} else {
				if !strings.HasPrefix(a, "ok ") {
					t.Errorf("%s: %s: %s", mode, names[i], a)
					continue
				}
				got = urlUnhex(t, strings.TrimPrefix(a, "ok "))
			}
			if got != wants[i] {
				t.Errorf("%s: %s: got %q, want %q", mode, names[i], got, wants[i])
			}
		}
		t.Logf("%s: percent-encoding.json: %d outputs run, skipped by name: big5, euc-kr, gb18030, iso-2022-jp, shift_jis", mode, len(records))
	}
}
