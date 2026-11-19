// Why: percent-encoding and query parsing have exact rules, and hostile input
// must not cost unbounded time or memory.
// What: TestStdUrl_EncodeSets, TestStdUrl_QueryModel, TestStdUrl_Adversarial
// and TestStdUrl_ErrorOffsets.
// How: encode sets against std-encoding's model (pctIn), queries against Go
// models of form encoding, and huge or deep inputs in bounded time.

package std

import (
	"fmt"
	"strings"
	"testing"
)

// urlSetIndex is pctSets' index of each set std.url's encoder names.
var urlSetIndex = map[string]int{"c0": 0, "fragment": 1, "query": 2, "specialquery": 3, "path": 4, "userinfo": 5}

// TestStdUrl_EncodeSets: every byte, alone and in a run, through each of
// std.url's percent-encode sets, against std-encoding's model of the URL
// Standard's sets.
func TestStdUrl_EncodeSets(t *testing.T) {
	var records, wants []string
	for name, k := range urlSetIndex {
		var all []byte
		for c := 0; c < 256; c++ {
			b := []byte{byte(c)}
			records = append(records, "enc "+name+" "+urlHex(string(b)))
			wants = append(wants, "ok "+urlHex(pctEncodeModel(k, b)))
			all = append(all, byte(c))
		}
		records = append(records, "enc "+name+" "+urlHex(string(all)))
		wants = append(wants, "ok "+urlHex(pctEncodeModel(k, all)))
		// Nothing to encode: the view of the input.
		records = append(records, "enc "+name+" "+urlHex("abc"))
		wants = append(wants, "ok "+urlHex("abc"))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, a := range answers {
			if a != wants[i] {
				t.Errorf("%s: %s: got %s, want %s", mode, records[i], a, wants[i])
			}
		}
	}
}

// urlFormDecodeModel is the URL Standard's application/x-www-form-urlencoded
// parser (section 5.1) in Go: name and value pairs.
func urlFormDecodeModel(s string) [][2]string {
	var out [][2]string
	utf8 := encModel{name: "UTF-8"}
	text := func(b []byte) string {
		// UTF-8 decode without BOM: a leading byte keeps any mark from
		// being read as one, and is dropped after.
		d, _ := whatwgDecode(utf8, append([]byte{'A'}, b...))
		return d[1:]
	}
	for _, seq := range strings.Split(s, "&") {
		if seq == "" {
			continue
		}
		name, value, _ := strings.Cut(seq, "=")
		name = strings.ReplaceAll(name, "+", " ")
		value = strings.ReplaceAll(value, "+", " ")
		out = append(out, [2]string{text(pctDecodeModel([]byte(name))), text(pctDecodeModel([]byte(value)))})
	}
	return out
}

// urlFormEncodeModel is the serializer (section 5.2) in Go.
func urlFormEncodeModel(pairs [][2]string) string {
	var parts []string
	for _, p := range pairs {
		parts = append(parts, pctEncodeModel(7, []byte(p[0]))+"="+pctEncodeModel(7, []byte(p[1])))
	}
	return strings.Join(parts, "&")
}

// urlQueryPieces are the pieces random queries are made of: separators,
// escapes good and bad, '+', non-ASCII text, byte order marks, and
// malformed UTF-8 once decoded.
var urlQueryPieces = []string{"&", "=", "+", "a", "b", "z9", "%", "%4", "%41", "%2B", "%26", "%3D", "%zz", "%e9", "%C3%A9", "%C3", "%E2%82", "%E2%82%AC", "%F0%9F%98%80", "%EF%BB%BF", "%ff%fe", "%ED%A0%80", "é", "€", "😀", " ", "~", "*", "-._", "\x01", "%00", "\uFEFF"}

func urlRandomQuery(r *urlRng, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(urlQueryPieces[r.intn(len(urlQueryPieces))])
	}
	return b.String()
}

// urlPairsAnswer is the driver's pq answer for pairs.
func urlPairsAnswer(pairs [][2]string) string {
	var b strings.Builder
	b.WriteString("ok")
	for _, p := range pairs {
		b.WriteString(" " + urlHex(p[0]) + "=" + urlHex(p[1]))
	}
	return b.String()
}

// TestStdUrl_QueryModel: ParseQuery on random queries and FormatQuery on
// random pairs, against the Go models.
func TestStdUrl_QueryModel(t *testing.T) {
	r := &urlRng{s: 0x5EED}
	var records, wants []string
	fixed := []string{"", "&", "&&a&&", "a", "a=", "=a", "a=b=c", "+", "a+b=c+d", "%", "%%", "%2", "%41%42", "%EF%BB%BFa=1", "%EF%BB%BF%EF%BB%BF=%EF%BB%BF", "a=%ff", "%C3%28=x", "q=%E2%82%AC&q=2"}
	for _, q := range fixed {
		records = append(records, "pq "+urlHex(q))
		wants = append(wants, urlPairsAnswer(urlFormDecodeModel(q)))
	}
	for i := 0; i < 3000; i++ {
		q := urlRandomQuery(r, r.intn(24))
		records = append(records, "pq "+urlHex(q))
		wants = append(wants, urlPairsAnswer(urlFormDecodeModel(q)))
	}
	for i := 0; i < 2000; i++ {
		var pairs [][2]string
		var arg []string
		for k := r.intn(5); k > 0; k-- {
			name := pctDecodeModelText(urlRandomQuery(r, r.intn(6)))
			value := pctDecodeModelText(urlRandomQuery(r, r.intn(6)))
			pairs = append(pairs, [2]string{name, value})
			arg = append(arg, urlHex(name)+"="+urlHex(value))
		}
		a := "-"
		if len(arg) > 0 {
			a = strings.Join(arg, ",")
		}
		records = append(records, "fq "+a)
		wants = append(wants, "ok "+urlHex(urlFormEncodeModel(pairs)))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		bad := 0
		for i, a := range answers {
			if a != wants[i] {
				t.Errorf("%s: %s:\n got  %s\n want %s", mode, records[i], a, wants[i])
				bad++
				if bad == 10 {
					t.Fatalf("%s: stopping after 10 differences", mode)
				}
			}
		}
	}
}

// pctDecodeModelText is s percent-decoded when that is UTF-8, else s:
// FormatQuery takes text.
func pctDecodeModelText(s string) string {
	d := string(pctDecodeModel([]byte(s)))
	if strings.ToValidUTF8(d, "?") == d {
		return d
	}
	return s
}

// urlAdversarial is one adversarial input, prefix + unit * count + suffix
// (built in the driver, past standard input's 64 KiB line), its base, and
// the href it parses to, or "" for failure.
type urlAdversarial struct {
	name, prefix, unit string
	count              int
	suffix, base, want string
}

func urlAdversarialCases() []urlAdversarial {
	rep := strings.Repeat
	return []urlAdversarial{
		{"200,000 a/.. segments", "http://h/", "a/../", 200000, "", "", "http://h/"},
		{"200,000 .. segments", "http://h/", "../", 200000, "x", "", "http://h/x"},
		{"200,000 %2e%2e segments", "http://h/", "%2e%2E/", 200000, "", "", "http://h/"},
		{"100,000 empty segments", "http://h", "/", 100000, "", "", "http://h" + rep("/", 100000)},
		{"hex IPv4 of 100,000 digits", "http://0x", "f", 100000, "", "", ""},
		{"decimal IPv4 of 100,000 digits", "http://", "1", 100000, "", "", ""},
		{"octal IPv4 of 100,000 zeros", "http://0", "0", 100000, ".1", "", "http://0.0.0.1/"},
		{"IPv4 with 5 parts", "http://1.2.3.4.5", "", 0, "", "", ""},
		{"IPv4 at 2^32", "http://4294967296", "", 0, "", "", ""},
		{"IPv4 at 2^32 - 1", "http://4294967295", "", 0, "", "", "http://255.255.255.255/"},
		{"port of 100,000 zeros then 80", "http://h:", "0", 100000, "80/", "", "http://h/"},
		{"port of 100,000 nines", "http://h:", "9", 100000, "/", "", ""},
		{"port 65535", "http://h:65535/", "", 0, "", "", "http://h:65535/"},
		{"port 65536", "http://h:65536/", "", 0, "", "", ""},
		{"100,000 colons in brackets", "http://[", ":", 100000, "]/", "", ""},
		{"50,000 pieces in brackets", "http://[", "1:", 50000, "1]/", "", ""},
		{"unclosed bracket", "http://[", "a", 100000, "", "", ""},
		{"100,000 at signs", "http://", "@", 100000, "h/", "", "http://" + rep("%40", 99999) + "@h/"},
		{"100,000 colons in userinfo", "http://", ":", 100000, "@h/", "", "http://:" + rep("%3A", 99999) + "@h/"},
		{"100,000 tabs around", "", "\t", 100000, "http://h/", "", "http://h/"},
		{"100,000 CRLFs inside", "ht", "\r\n", 100000, "tp://h/", "", "http://h/"},
		{"1 MB opaque path", "a:", "b c", 350000, "", "", "a:" + rep("b c", 350000)},
		{"1 MB query to encode", "http://h/?", "\"<>", 350000, "", "", "http://h/?" + rep("%22%3C%3E", 350000)},
		{"1 MB fragment", "http://h/#", "`", 1000000, "", "", "http://h/#" + rep("%60", 1000000)},
		{"relative 100,000 levels up", "", "../", 100000, "x", "http://h/a/b/c/", "http://h/x"},
		{"backslashes in a special URL", "http:", "\\", 100000, "h/", "", "http://h/"},
		{"empty input", "", "", 0, "", "", ""},
		{"spaces only", "", " ", 100000, "", "", ""},
		{"empty input against a base", "", "", 0, "", "http://h/a?b#c", "http://h/a?b"},
		{"file drive letters", "file:", "/c:", 10000, "", "", "file:///c:" + rep("/c:", 9999)},
		{"a non-special host of 100,000 bytes", "x://", "h", 100000, "/", "", "x://" + rep("h", 100000) + "/"},
		{"a domain of 100,000 capitals", "http://", "H", 100000, "/", "", "http://" + rep("h", 100000) + "/"},
		// Each refuses PathRun only at its end: a run retried at every
		// segment would read the path 100,000 times.
		{"100,000 segments, then a byte to encode", "http://h/", "a/", 100000, "{", "", "http://h/" + rep("a/", 100000) + "%7B"},
		{"100,000 segments, then a space", "http://h/", "a/", 100000, " x", "", "http://h/" + rep("a/", 100000) + "%20x"},
		{"100,000 segments, then ..", "http://h/", "a/", 100000, "..", "", "http://h/" + rep("a/", 99999)},
		{"100,000 segments with a backslash each", "http://h/", "a\\", 100000, "", "", "http://h/" + rep("a/", 100000)},
	}
}

// TestStdUrl_Adversarial: each adversarial input parses to its href or
// fails, in dev and release, with no panic (the driver would exit non-zero)
// and within runNative's time bound.
func TestStdUrl_Adversarial(t *testing.T) {
	cases := urlAdversarialCases()
	var records []string
	for _, c := range cases {
		base := "-"
		if c.base != "" {
			base = urlHex(c.base)
		}
		unit, prefix := c.unit, c.prefix
		records = append(records, fmt.Sprintf("rep %s %s %d %s %s", urlHex(prefix), urlHex(unit), c.count, urlHex(c.suffix), base))
	}
	for _, mode := range urlModes {
		answers := urlAsk(t, mode, records)
		for i, c := range cases {
			u, ok := urlRead(t, answers[i])
			switch {
			case c.want == "" && ok:
				t.Errorf("%s: %s: parsed to %s, want failure", mode, c.name, urlShortText(u.href))
			case c.want != "" && !ok:
				t.Errorf("%s: %s: %s, want %s", mode, c.name, urlShortText(answers[i]), urlShortText(c.want))
			case ok && u.href != c.want:
				t.Errorf("%s: %s: got %s, want %s", mode, c.name, urlShortText(u.href), urlShortText(c.want))
			case ok && !u.reparses:
				t.Errorf("%s: %s: href does not parse back to itself", mode, c.name)
			}
		}
	}
}

func urlShortText(s string) string {
	if len(s) > 200 {
		return fmt.Sprintf("%q... (%d bytes)", s[:200], len(s))
	}
	return fmt.Sprintf("%q", s)
}

// TestStdUrl_ErrorOffsets: BadUrl's offset is the input's as given, past
// trimmed bytes and removed tabs and newlines; a host error is BadHost.
func TestStdUrl_ErrorOffsets(t *testing.T) {
	cases := []struct{ input, want string }{
		{"http://h:x/", "err BadUrl(offset: 9)"},
		{"  http://h:x/", "err BadUrl(offset: 11)"},
		{"ht\ttp://h\n:x/", "err BadUrl(offset: 11)"},
		{"http://h:99999/", "err BadUrl(offset: 14)"},
		{"noscheme", "err BadUrl(offset: 0)"},
		{"http://a b/", "err BadHost"},
		{"http://[::1/", "err BadHost"},
		{"http://user@/", "err BadUrl(offset: 12)"},
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
