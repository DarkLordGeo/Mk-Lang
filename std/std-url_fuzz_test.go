// Why: url.Parse, Url.Set and ParseQuery read untrusted text and must never
// panic, hang, or give a URL whose href does not parse back to itself.
// What: FuzzStdUrl_Parse, FuzzStdUrl_Set and FuzzStdUrl_ParseQuery through the
// release driver.
// How: seeds are urltestdata.json's and setters_tests.json's inputs; go test
// -fuzz FuzzStdUrl_<name> -parallel 2 explores (Windows only).
// Its fuzz targets read untrusted input; tools/gate/safety.go lists each untrusted-input target.

package std

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// urlFuzzOne runs one record through the release driver and returns its
// answer; a crash fails.
func urlFuzzOne(t *testing.T, record string) string {
	t.Helper()
	exe := urlExe(t, "release")
	r := runNative(t, exe, record+"\n")
	got := strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n")
	if r.code != 0 || strings.Contains(got, "\n") {
		t.Fatalf("%s\n exit %d, stderr %s\n answer %s", urlShortText(record), r.code, r.stderr, urlShortText(got))
	}
	return got
}

// urlFuzzCheck checks the invariants of a Show answer.
func urlFuzzCheck(t *testing.T, record, answer string) {
	t.Helper()
	if strings.HasPrefix(answer, "err ") {
		if strings.Contains(answer, "bad reparse") {
			t.Fatalf("%s: %s", urlShortText(record), answer)
		}
		return
	}
	u, ok := urlRead(t, answer)
	if !ok {
		t.Fatalf("%s: %s", urlShortText(record), urlShortText(answer))
	}
	for i := 0; i < len(u.href); i++ {
		if u.href[i] >= 0x80 {
			t.Fatalf("%s: href %q is not ASCII", urlShortText(record), u.href)
		}
	}
	if !u.reparses {
		t.Fatalf("%s: href %q does not parse back to itself", urlShortText(record), u.href)
	}
	format := u.href
	if u.password != "" {
		format = strings.Replace(u.href, ":"+u.password+"@", "@", 1)
	}
	if u.format != format {
		t.Fatalf("%s: {u} is %q, want %q", urlShortText(record), u.format, format)
	}
	if !strings.HasPrefix(u.href, u.scheme+":") {
		t.Fatalf("%s: href %q does not start with scheme %q", urlShortText(record), u.href, u.scheme)
	}
}

// urlFuzzBases are the bases a fuzzed input is parsed against.
var urlFuzzBases = []string{"", "http://example.org/foo/bar?q#f", "file:///C:/a/b", "sc://h/a/b", "about:blank", "https://user:pw@[::1]:8080/x/"}

func FuzzStdUrl_Parse(f *testing.F) {
	var raw []json.RawMessage
	if b := urlEvidenceRead("urltestdata.json"); b != nil {
		if err := json.Unmarshal(b, &raw); err == nil {
			for i, r := range raw {
				var c urlCase
				if json.Unmarshal(r, &c) == nil && c.Input != "" {
					f.Add(c.Input, uint8(i))
				}
			}
		}
	}
	f.Add("http://user:pass@EXAMPLE.com:80/a/./b/../c?d#e", uint8(0))
	f.Fuzz(func(t *testing.T, input string, base uint8) {
		if len(input) > 30000 || !utf8.ValidString(input) {
			return
		}
		b := urlFuzzBases[int(base)%len(urlFuzzBases)]
		arg := "-"
		if b != "" {
			arg = urlHex(b)
		}
		record := "parse " + urlHex(input) + " " + arg
		answer := urlFuzzOne(t, record)
		urlFuzzCheck(t, record, answer)
		// The input with a tab added parses to the same URL through
		// Serialize, the input as given perhaps through SerializeSame, with
		// or without a base (TestStdUrl_SameAsFull), and a BadUrl
		// offset past the tab moves by one.
		if tab, ok := urlTabbed(input); ok {
			if other := urlFuzzOne(t, "parse "+urlHex(tab)+" "+arg); !urlSameAnswersTab(answer, other, input) {
				t.Fatalf("%s:\n as given %s\n tabbed   %s", urlShortText(record), urlShortText(answer), urlShortText(other))
			}
		}
	})
}

// urlSetParts are the parts the setter fuzz target sets.
var urlSetParts = []string{"href", "protocol", "username", "password", "host", "hostname", "port", "pathname", "search", "hash"}

func FuzzStdUrl_Set(f *testing.F) {
	if b := urlEvidenceRead("setters_tests.json"); b != nil {
		var file map[string]json.RawMessage
		if json.Unmarshal(b, &file) == nil {
			for k, part := range urlSetParts {
				var cs []struct {
					Href     string `json:"href"`
					NewValue string `json:"new_value"`
				}
				if raw, ok := file[part]; ok && json.Unmarshal(raw, &cs) == nil {
					for _, c := range cs {
						f.Add(c.Href, c.NewValue, uint8(k))
					}
				}
			}
		}
	}
	f.Fuzz(func(t *testing.T, href, value string, part uint8) {
		if len(href)+len(value) > 30000 || !utf8.ValidString(href) || !utf8.ValidString(value) {
			return
		}
		record := "set " + urlHex(href) + " " + urlSetParts[int(part)%len(urlSetParts)] + " " + urlHex(value)
		answer := urlFuzzOne(t, record)
		if strings.HasPrefix(answer, "err href ") {
			return
		}
		urlFuzzCheck(t, record, answer)
	})
}

func FuzzStdUrl_ParseQuery(f *testing.F) {
	r := &urlRng{s: 7}
	for i := 0; i < 64; i++ {
		f.Add(urlRandomQuery(r, r.intn(12)))
	}
	f.Fuzz(func(t *testing.T, q string) {
		if len(q) > 30000 || !utf8.ValidString(q) {
			return
		}
		record := "pq " + urlHex(q)
		got := urlFuzzOne(t, record)
		if want := urlPairsAnswer(urlFormDecodeModel(q)); got != want {
			t.Fatalf("%q:\n got  %s\n want %s", q, got, want)
		}
	})
}
