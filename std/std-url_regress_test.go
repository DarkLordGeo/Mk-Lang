// Why: parser bugs that were found and fixed need tests that keep them fixed.
// What: the TestStdUrl_ regression tests: BadUrl offsets past removed tabs,
// ParseQuery, Url.Host()'s kind, base reads, Set(Href) failing as Parse does.
// How: each case against a Go model or Go's net/url.ParseQuery, through the
// std-url driver.

package std

import (
	"encoding/hex"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestStdUrl_OffsetPastRemovedTabs: a tab or newline removed
// directly before the failing byte does not pull BadUrl's offset onto it.
func TestStdUrl_OffsetPastRemovedTabs(t *testing.T) {
	cases := []struct{ input, want string }{
		{"http://h:\tx/", "err BadUrl(offset: 10)"},
		{"http://h:\t\nx/", "err BadUrl(offset: 11)"},
		{"http://h:\t\tx/", "err BadUrl(offset: 11)"},
		{"http://user@\t/", "err BadUrl(offset: 13)"},
		{" \thttp://h:\r\n\tx/", "err BadUrl(offset: 14)"},
		{"http://h:99999\t/", "err BadUrl(offset: 15)"},
		{"http://h:99\t999/", "err BadUrl(offset: 15)"},
		{"\t\nnoscheme", "err BadUrl(offset: 2)"},
		{"no\tscheme", "err BadUrl(offset: 0)"},
	}
	var records []string
	for _, c := range cases {
		records = append(records, "parse "+urlHex(c.input)+" -")
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, c := range cases {
			if answers[i] != c.want {
				t.Errorf("%s: %q: got %s, want %s", mode, c.input, answers[i], c.want)
			}
		}
	}
}

// urlBadOffset is the offset of a "err BadUrl(offset: N)" answer.
var urlBadOffset = regexp.MustCompile(`^err BadUrl\(offset: (\d+)\)$`)

// urlSameAnswersTab is whether the answers for s (x) and for urlTabbed(s)
// (y) agree: the same URL, the same error, and a BadUrl offset at or past
// the tab one byte later in y, unless step 1 trims the tab.
func urlSameAnswersTab(x, y, s string) bool {
	m := urlBadOffset.FindStringSubmatch(x)
	if m == nil {
		return x == y
	}
	o, _ := strconv.Atoi(m[1])
	k := -1
	for i, r := range s {
		if r > 0x20 {
			k = i + len(string(r))
			break
		}
	}
	trimmed := strings.TrimRight(s[k:], "\x00\x01\x02\x03\x04\x05\x06\x07\x08\t\n\x0b\x0c\r\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f ") == ""
	if o >= k && !trimmed {
		o++
	}
	return y == fmt.Sprintf("err BadUrl(offset: %d)", o)
}

// urlSpecial is the URL Standard's special schemes.
var urlSpecial = map[string]bool{"ftp": true, "file": true, "http": true, "https": true, "ws": true, "wss": true}

// urlHostKindModel is what the driver prints for Url.Host() of a URL with
// scheme and hostname: E: for no or the empty host; 6: and the 16
// bytes for a bracketed IPv6 address; for a special scheme, 4: and the
// value of a dotted IPv4 address, else D: and the domain; for any other
// scheme O: and the opaque host.
func urlHostKindModel(scheme, hostname string) string {
	switch {
	case hostname == "":
		return "E:"
	case strings.HasPrefix(hostname, "["):
		ip, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(hostname, "["), "]"))
		if err != nil || !ip.Is6() {
			return "bad IPv6 hostname " + hostname
		}
		b := ip.As16()
		return "6:" + hex.EncodeToString(b[:])
	case urlSpecial[scheme]:
		if ip, err := netip.ParseAddr(hostname); err == nil && ip.Is4() {
			b := ip.As4()
			return "4:" + strconv.FormatUint(uint64(b[0])<<24|uint64(b[1])<<16|uint64(b[2])<<8|uint64(b[3]), 10)
		}
		return "D:" + urlHex(hostname)
	}
	return "O:" + urlHex(hostname)
}

// TestStdUrl_HostKind: Url.Host()'s kind and payload for each kind,
// from Parse and through Set (whose record round-trips the IPv6 pieces),
// against urlHostKindModel and against the value written out. Every passing
// case of TestStdUrl_UrlTestData and TestStdUrl_Setters checks the model
// too.
func TestStdUrl_HostKind(t *testing.T) {
	cases := []struct{ record, want string }{
		{"parse " + urlHex("http://[1:2:3:4:5:6:7:8]/") + " -", "6:00010002000300040005000600070008"},
		{"parse " + urlHex("http://[::ffff:1.2.3.4]/") + " -", "6:00000000000000000000ffff01020304"},
		{"parse " + urlHex("sc://[fe80::abcd]/") + " -", "6:fe80000000000000000000000000abcd"},
		{"parse " + urlHex("http://1.2.3.4/") + " -", "4:16909060"},
		{"parse " + urlHex("http://0x7f.1/") + " -", "4:2130706433"},
		{"parse " + urlHex("ws://255.255.255.255/") + " -", "4:4294967295"},
		{"parse " + urlHex("sc://1.2.3.4/") + " -", "O:" + urlHex("1.2.3.4")},
		{"parse " + urlHex("sc://%41b/") + " -", "O:" + urlHex("%41b")},
		{"parse " + urlHex("http://Example.COM/") + " -", "D:" + urlHex("example.com")},
		{"parse " + urlHex("file:///x") + " -", "E:"},
		{"parse " + urlHex("sc:x") + " -", "E:"},
		{"parse " + urlHex("sc:///x") + " -", "E:"},
		{"set " + urlHex("http://a/") + " hostname " + urlHex("[1::2]"), "6:00010000000000000000000000000002"},
		{"set " + urlHex("http://[1::2]/") + " pathname " + urlHex("/x"), "6:00010000000000000000000000000002"},
		{"set " + urlHex("http://[1:2:3:4:5:6:7:8]:81/") + " port " + urlHex("82"), "6:00010002000300040005000600070008"},
		{"set " + urlHex("http://1.2.3.4/") + " search " + urlHex("q"), "4:16909060"},
		{"set " + urlHex("sc://h/") + " host " + urlHex("[::1]"), "6:00000000000000000000000000000001"},
	}
	var records []string
	for _, c := range cases {
		records = append(records, c.record)
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, c := range cases {
			u, ok := urlRead(t, answers[i])
			if !ok {
				t.Errorf("%s: %s: %s", mode, c.record, answers[i])
				continue
			}
			if u.kind != c.want {
				t.Errorf("%s: %s (%s): kind %s, want %s", mode, c.record, u.href, u.kind, c.want)
			}
			if m := urlHostKindModel(u.scheme, u.hostname); m != u.kind {
				t.Errorf("%s: %s (%s): kind %s, model %s", mode, c.record, u.href, u.kind, m)
			}
		}
	}
}

// TestStdUrl_BaseReadOnlyWhenNeeded: Parse builds the base's record
// (a copy of the base in the arena) only when the input reads the base
// beyond its scheme.
func TestStdUrl_BaseReadOnlyWhenNeeded(t *testing.T) {
	const web = "https://example.org/a/b?q#f"
	const file = "file:///C:/a/b"
	cases := []struct {
		input, base string
		read        bool
	}{
		{"https://other.example/x", web, false},
		{"HTTPS://Other.example/x", web, false},
		{"https:\\\\other.example\\x", web, true},
		{"http:foo", web, false},
		{"http:foo", "http://example.org/", true},
		{"sc:x", web, false},
		{"sc://h/x", web, false},
		{"x/y", web, true},
		{"//h/x", web, true},
		{"/x", web, true},
		{"?r", web, true},
		{"#g", web, true},
		{"#g", "sc:opaque", true},
		{"file:///d", file, false},
		{"file:///d", web, false},
		{"file://h/d", file, false},
		{"file:x", file, true},
		{"file:/x", file, true},
		{"file:x", web, false},
		{"file:/x", web, false},
		{"x", file, true},
	}
	var records []string
	for _, c := range cases {
		records = append(records, "pbr "+urlHex(c.input)+" "+urlHex(c.base))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, c := range cases {
			if want := fmt.Sprintf("ok %v", c.read); answers[i] != want {
				t.Errorf("%s: %q against %q: got %s, want %s", mode, c.input, c.base, answers[i], want)
			}
		}
	}
}

// TestStdUrl_SameAsFullWithBase: with a base, the input as given
// (SerializeSame where the base is not read) and with a tab added (Serialize
// always) give the same answer: urltestdata.json's inputs against their
// bases, and absolute and relative inputs against the fuzz targets' bases.
func TestStdUrl_SameAsFullWithBase(t *testing.T) {
	type pair struct{ input, base string }
	var pairs []pair
	for _, c := range urlTestData(t) {
		if c.Base != nil {
			pairs = append(pairs, pair{c.Input, *c.Base})
		}
	}
	inputs := []string{"https://other.example/x", "http://a.b/c?d#e", "ws://h:8080/", "HTTP://H/", "sc://h/p", "sc:p", "file:///d", "file://h/x", "x/y", "/x", "?q", "#f", "//h/x", "http:x", "https:x", "file:x"}
	for _, b := range urlFuzzBases {
		if b == "" {
			continue
		}
		for _, in := range inputs {
			pairs = append(pairs, pair{in, b})
		}
	}
	var records []string
	var kept []pair
	for _, p := range pairs {
		tab, ok := urlTabbed(p.input)
		if !ok {
			continue
		}
		kept = append(kept, p)
		records = append(records, "parse "+urlHex(p.input)+" "+urlHex(p.base), "parse "+urlHex(tab)+" "+urlHex(p.base))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		failed := 0
		for i, p := range kept {
			x, y := answers[2*i], answers[2*i+1]
			if strings.HasPrefix(x, "err base") {
				continue
			}
			if !urlSameAnswersTab(x, y, p.input) {
				t.Errorf("%s: %q against %q:\n as given %s\n tabbed   %s", mode, p.input, p.base, x, y)
				failed++
			}
			if failed > 20 {
				t.Fatalf("%s: too many failures", mode)
			}
		}
	}
}

// urlGoQueryPieces build queries Go's net/url.ParseQuery reads as the URL
// Standard does: no ';', and only escapes of whole UTF-8 sequences.
var urlGoQueryPieces = []string{
	"a", "b", "key", "v1", "é", "\u4e2d", "\U0001F600", "\uFEFF", "+", "=", "==", "&", "&&",
	"%41", "%61", "%20", "%2B", "%26", "%3D", "%25", "%C3%A9", "%e4%b8%ad", "%F0%9F%98%80", "%EF%BB%BF", "-", "_", ".", "~", "*",
}

// urlPairsSorted is pairs as sorted "name=value" lines (Go's Values keeps
// no order between keys).
func urlPairsSorted(pairs [][2]string) []string {
	var out []string
	for _, p := range pairs {
		out = append(out, p[0]+"\x00"+p[1])
	}
	sort.Strings(out)
	return out
}

// TestStdUrl_ParseQueryAgainstGo: ParseQuery against Go's
// net/url.ParseQuery, the oracle, on 4,000 generated ';'-free queries with
// whole-sequence escapes, mixing sequences with nothing to decode (one copy,
// no UTF-8 check) and ones with '+' or '%'; and the plain and decoding
// paths at their edges against the Go model of section 5.1.
func TestStdUrl_ParseQueryAgainstGo(t *testing.T) {
	r := urlRng{s: 303}
	var queries []string
	for len(queries) < 4000 {
		var b strings.Builder
		n := r.intn(14)
		for k := 0; k < n; k++ {
			b.WriteString(urlGoQueryPieces[r.intn(len(urlGoQueryPieces))])
		}
		queries = append(queries, b.String())
	}
	edges := []string{"a=b&cc=dd&e=f", "&&x&=y&z=&a=b=c", "a+b=%41%zz&\uFEFFk=v", "é=ü&%C3%A9=1", "=", "&", "a", "a=", "=a", "%", "%4", "+=+", "a=b+c&d=e", "x=%FF&y=z", "==&==="}
	var records []string
	for _, q := range queries {
		records = append(records, "pq "+urlHex(q))
	}
	for _, q := range edges {
		records = append(records, "pq "+urlHex(q))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		failed := 0
		for i, q := range queries {
			values, err := url.ParseQuery(q)
			if err != nil {
				t.Fatalf("Go refused %q: %v", q, err)
			}
			var want [][2]string
			for k, vs := range values {
				for _, v := range vs {
					want = append(want, [2]string{k, v})
				}
			}
			got := urlPairsOf(t, answers[i])
			if g, w := urlPairsSorted(got), urlPairsSorted(want); strings.Join(g, "\n") != strings.Join(w, "\n") {
				t.Errorf("%s: %q:\n got  %q\n want %q", mode, q, g, w)
				failed++
			}
			if failed > 20 {
				t.Fatalf("%s: too many failures", mode)
			}
		}
		for i, q := range edges {
			if got, want := answers[len(queries)+i], urlPairsAnswer(urlFormDecodeModel(q)); got != want {
				t.Errorf("%s: %q:\n got  %s\n want %s", mode, q, got, want)
			}
		}
	}
}

// urlPairsOf reads a pq answer's pairs.
func urlPairsOf(t *testing.T, answer string) [][2]string {
	t.Helper()
	f := strings.Split(answer, " ")
	if f[0] != "ok" {
		t.Fatalf("bad pq answer %q", answer)
	}
	var out [][2]string
	for _, nv := range f[1:] {
		name, value, ok := strings.Cut(nv, "=")
		if !ok {
			t.Fatalf("bad pq pair %q", nv)
		}
		out = append(out, [2]string{urlUnhex(t, name), urlUnhex(t, value)})
	}
	return out
}

// TestStdUrl_SetHrefFailsLikeParse: Set(Href) is Parse of the value,
// so it fails with BadHost and BadUrl as Parse does; the host setter
// ignores the same host and leaves the URL as it was.
func TestStdUrl_SetHrefFailsLikeParse(t *testing.T) {
	cases := []struct{ record, want string }{
		{"set " + urlHex("http://x/") + " href " + urlHex("http://a b/"), "err BadHost"},
		{"set " + urlHex("http://x/") + " href " + urlHex("http://[::1/"), "err BadHost"},
		{"set " + urlHex("http://x/") + " href " + urlHex("http://h:x/"), "err BadUrl(offset: 9)"},
		{"set " + urlHex("http://x/") + " href " + urlHex("noscheme"), "err BadUrl(offset: 0)"},
	}
	var records []string
	for _, c := range cases {
		records = append(records, c.record)
	}
	ignored := "set " + urlHex("http://x/") + " host " + urlHex("a b")
	records = append(records, ignored)
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, c := range cases {
			if answers[i] != c.want {
				t.Errorf("%s: %s: got %s, want %s", mode, c.record, answers[i], c.want)
			}
		}
		u, ok := urlRead(t, answers[len(cases)])
		if !ok || u.href != "http://x/" {
			t.Errorf("%s: host = \"a b\": got %s, want http://x/ unchanged", mode, answers[len(cases)])
		}
	}
}
