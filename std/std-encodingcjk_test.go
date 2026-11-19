// Why: the CJK decoders and encoders have huge input spaces; all of them must match the standard.
// What: one Mk driver over std.encoding.cjk, checked against the Go model and the pinned web-platform-tests
// cases (encoding/legacy-mb-*, encoding/*.any.js).
// How: every two-byte sequence of every decoder, EUC-JP three-byte and gb18030 four-byte sequences,
// every BMP and plane 2 scalar through every encoder; the driver is fed stdin records as in std-encoding_harness_test.go.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// cjkDriver is the Mk side. Encodings are named by a label; bytes and
// text are lower-case hex.
const cjkDriver = `import std.encoding.cjk

Nib(c: u8): u8 {
    if c >= b'a' {
        return c - 87
    }
    return c - 48
}

Unhex(a: &Arena, s: str): OutOfMemory![]u8 from a {
    return try UnhexBytes(a, s.AsBytes())
}

UnhexBytes(a: &Arena, b: []u8): OutOfMemory![]u8 from a {
    out := try a.Slice[u8](b.Len() / 2)
    for i in 0..out.Len() {
        out[i] = (Nib(b[2 * i]) << 4) | Nib(b[2 * i + 1])
    }
    return out[..]
}

const Digits: str = "0123456789abcdef"

HexOf(a: &Arena, b: []u8): OutOfMemory!str from a {
    d := Digits.AsBytes()
    out := try a.Slice[u8](2 * b.Len())
    for i in 0..b.Len() {
        out[2 * i] = d[int(b[i] >> 4)]
        out[2 * i + 1] = d[int(b[i] & 15)]
    }
    return str.From(out[..]).Must()
}

TextOf(a: &Arena, s: str): OutOfMemory!str from a {
    b := try Unhex(a, s)
    return str.From(b) else { "" }
}

// Number is the decimal int s spells.
Number(s: str): int {
    var n := 0
    for c in s.AsBytes() {
        n = n * 10 + int(c - 48)
    }
    return n
}

// Case answers one record: an op, then its words: "look <label>", or
// "<op> <encoding> <bytes or text>", or "push <encoding> <cuts> <bytes>",
// or "seq <encoding> <chunks>".
Case(a: &Arena into a, line: str): OutOfMemory!str from a {
    var parts := line.Split(" ")
    op := parts.Next() else { "" }
    name := parts.Next() else { "" }
    w2 := parts.Next() else { "" }
    w3 := parts.Next() else { "" }
    if op == "look" {
        e := cjk.Lookup(try TextOf(a, name)) else err {
            return try a.Fmt("err {e}", (e: err))
        }
        return try a.Fmt("ok {n}", (n: e.Name()))
    }
    e := cjk.Lookup(name) else {
        return "bad encoding"
    }
    x := if op == "push" { w3 } else { w2 }
    match op {
        "dec" {
            t := try e.Decode(a, try Unhex(a, x))
            return try a.Fmt("ok {h}", (h: try HexOf(a, t.AsBytes())))
        }
        "decs" {
            t := e.DecodeStrict(a, try Unhex(a, x)) else err {
                return try a.Fmt("err {e}", (e: err))
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, t.AsBytes())))
        }
        "push" {
            // w2 is the offsets the chunks end at, joined by ",".
            b := try Unhex(a, x)
            var d := e.Decoder()
            var t := Text.New(a)
            var at := 0
            for cut in w2.Split(",") {
                if cut.Len() == 0 {
                    continue
                }
                end := Number(cut)
                try t.Push(try d.Push(a, b[at..end], false))
                at = end
            }
            try t.Push(try d.Push(a, b[at..], true))
            return try a.Fmt("ok {h}", (h: try HexOf(a, t.Str().AsBytes())))
        }
        "seq" {
            // w2 is the chunks, joined by ",", pushed in turn to one
            // Decoder: each is "s" (more follows) or "e" (the stream ends)
            // and its bytes. The answer is each Push's text, joined by ",".
            var d := e.Decoder()
            var t := Text.New(a)
            try t.Push("ok ")
            var n := 0
            for ch in w2.Split(",") {
                cb := ch.AsBytes()
                if cb.Len() == 0 {
                    continue
                }
                got := try d.Push(a, try UnhexBytes(a, cb[1..]), cb[0] == b'e')
                if n > 0 {
                    try t.Push(",")
                }
                try t.Push(try HexOf(a, got.AsBytes()))
                n += 1
            }
            return try a.Fmt("{s}", (s: t.Str()))
        }
        "enc" {
            out := e.Encode(a, try TextOf(a, x)) else err {
                return try a.Fmt("err {e}", (e: err))
            }
            return try a.Fmt("ok {h}", (h: try HexOf(a, out)))
        }
        "each" {
            // Each scalar of the text alone: its bytes, or "-".
            b := try Unhex(a, x)
            var t := Text.New(a)
            try t.Push("ok")
            var i := 0
            for i < b.Len() {
                var n := 1
                if b[i] >= 0xF0 {
                    n = 4
                } else if b[i] >= 0xE0 {
                    n = 3
                } else if b[i] >= 0xC0 {
                    n = 2
                }
                one := str.From(b[i..i + n]) else { "" }
                try t.Push(" ")
                out := e.Encode(a, one) else {
                    try t.Push("-")
                    i += n
                    continue
                }
                try t.Push(try HexOf(a, out))
                i += n
            }
            return try a.Fmt("{s}", (s: t.Str()))
        }
        "name" {
            return try a.Fmt("ok {n}", (n: e.Name()))
        }
        _ {
            return "bad op"
        }
    }
}

export Main(sys: &System): ReadError|WriteError|OutOfMemory!void {
    var r := sys.Stdin()
    var out := sys.Stdout()
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    for {
        line := try r.Line(&arena) else {
            break
        }
        answer := try Case(&arena, line)
        try out.Str(answer)
        try out.Str("\n")
        arena.Reset()
    }
    try out.Flush()
}
`

var cjkBuilt struct {
	sync.Mutex
	exe map[string]string
	err map[string]string
}

// cjkExe builds the driver for mode once per test binary.
func cjkExe(t testing.TB, mode string) string {
	t.Helper()
	cjkBuilt.Lock()
	defer cjkBuilt.Unlock()
	if cjkBuilt.exe == nil {
		cjkBuilt.exe = map[string]string{}
		cjkBuilt.err = map[string]string{}
	}
	if exe, ok := cjkBuilt.exe[mode]; ok {
		return exe
	}
	if msg, ok := cjkBuilt.err[mode]; ok {
		t.Skip(msg)
	}
	tt, ok := t.(*testing.T)
	if !ok {
		t.Fatal("cjkExe: the first build needs a *testing.T")
	}
	dir, err := packageTempDir("mk-encodingcjk-" + mode)
	if err != nil {
		t.Fatal(err)
	}
	stdDir := encStdDir(tt, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.mk"), []byte(cjkDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "main")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	args := []string{"build"}
	if mode == "release" {
		args = append(args, "--release")
	}
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(tt, dir, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			cjkBuilt.err[mode] = "needs the LLVM toolchain: " + out
			t.Skip(cjkBuilt.err[mode])
		}
		t.Fatalf("mk build of the cjk driver (%s): exit %d\n%s", mode, status, out)
	}
	cjkBuilt.exe[mode] = exe
	return exe
}

// cjkRun sends cases to mode's driver in batches and reports differences,
// up to ten.
func cjkRun(t *testing.T, mode string, cases []encCase) {
	t.Helper()
	exe := cjkExe(t, mode)
	bad := 0
	const batch = 4000
	for start := 0; start < len(cases); start += batch {
		end := min(start+batch, len(cases))
		var in strings.Builder
		for _, c := range cases[start:end] {
			in.WriteString(c.in)
			in.WriteString("\n")
		}
		r := runNative(t, exe, in.String())
		if r.code != 0 {
			t.Fatalf("driver (%s) exit %d\nstderr: %s\nlast answers: %s", mode, r.code, r.stderr, encTail(r.stdout))
		}
		got := strings.Split(strings.TrimSuffix(strings.ReplaceAll(r.stdout, "\r\n", "\n"), "\n"), "\n")
		if len(got) != end-start {
			t.Fatalf("driver (%s): %d answers for %d records\nstderr: %s", mode, len(got), end-start, r.stderr)
		}
		for i, c := range cases[start:end] {
			if got[i] != c.want {
				t.Errorf("%s: %s\n got  %s\n want %s", mode, encShort(c.in), encShort(got[i]), encShort(c.want))
				bad++
				if bad == 10 {
					t.Fatalf("%s: stopping after 10 differences", mode)
				}
			}
		}
	}
}

func cjkRunAll(t *testing.T, cases []encCase) {
	t.Helper()
	for _, mode := range encModes {
		cjkRun(t, mode, cases)
	}
}

// cjkNames are the encodings, by name, which is also a label of each.
var cjkNames = []string{"GBK", "gb18030", "Big5", "EUC-JP", "ISO-2022-JP", "Shift_JIS", "EUC-KR"}

// cjkDecCase is Decode of b, cjkDecsCase DecodeStrict, by the model.
func cjkDecCase(m *cjkModel, name string, b []byte) encCase {
	s, _ := m.decode(name, b)
	return encCase{in: "dec " + name + " " + encHex(b), want: "ok " + encHex([]byte(s))}
}

func cjkDecsCase(m *cjkModel, name string, b []byte) encCase {
	s, bad := m.decode(name, b)
	if bad >= 0 {
		return encCase{in: "decs " + name + " " + encHex(b), want: fmt.Sprintf("err BadInput(offset: %d)", bad)}
	}
	return encCase{in: "decs " + name + " " + encHex(b), want: "ok " + encHex([]byte(s))}
}

func cjkEncCase(m *cjkModel, name, s string) encCase {
	out, bad := m.encode(name, s)
	if bad >= 0 {
		return encCase{in: "enc " + name + " " + encHex([]byte(s)), want: fmt.Sprintf("err BadInput(offset: %d)", bad)}
	}
	return encCase{in: "enc " + name + " " + encHex([]byte(s)), want: "ok " + encHex(out)}
}

// cjkPushCase decodes b in pieces ending at cuts.
func cjkPushCase(m *cjkModel, name string, b []byte, cuts []int) encCase {
	s, _ := m.decode(name, b)
	var cs []string
	for _, c := range cuts {
		cs = append(cs, strconv.Itoa(c))
	}
	return encCase{in: "push " + name + " " + strings.Join(cs, ",") + ", " + encHex(b), want: "ok " + encHex([]byte(s))}
}

func TestStdEncodingCjk_Labels(t *testing.T) {
	dir := cjkDir(t)
	labels, err := cjkLabels(dir)
	if err != nil {
		t.Fatal(err)
	}
	all, err := encLabels(dir)
	if err != nil {
		t.Fatal(err)
	}
	var cases []encCase
	look := func(label, want string) {
		cases = append(cases, encCase{in: "look " + encHex([]byte(label)), want: want})
	}
	for l, i := range labels {
		want := "ok " + cjkEncodings[i].name
		look(l, want)
		look(strings.ToUpper(l), want)
		look(" \t"+l+"\n\f\r ", want)
	}
	// encoding.Lookup's labels come back through cjk.Lookup.
	for l, id := range all {
		if id != encUnsupported {
			look(l, "ok "+encSpecs[id].name)
		}
	}
	for _, l := range []string{"", "x-sjis2", "shift_jis\x00", "\vshift_jis", "gb18030 x", "big5-hkscs-2008", "iso-2022-jp-2", strings.Repeat("a", 40)} {
		look(l, "err UnknownLabel")
	}
	for _, n := range cjkNames {
		cases = append(cases, encCase{in: "name " + n + " x", want: "ok " + n})
	}
	cjkRunAll(t, cases)
}

func TestStdEncodingCjk_Pairs(t *testing.T) {
	m := cjkModelGet(t)
	var cases []encCase
	for _, name := range cjkNames[1:] {
		for lead := 0x80; lead <= 0xFF; lead++ {
			for trail := 0; trail <= 0xFF; trail++ {
				b := []byte{byte(lead), byte(trail)}
				cases = append(cases, cjkDecCase(m, name, b))
				if trail%5 == 0 {
					cases = append(cases, cjkDecsCase(m, name, b))
				}
			}
			cases = append(cases, cjkDecCase(m, name, []byte{byte(lead)}), cjkDecsCase(m, name, []byte{byte(lead)}))
		}
	}
	// EUC-JP's three-byte form, every byte after 0x8F and a second byte.
	for y := 0xA0; y <= 0xFF; y++ {
		for z := 0; z <= 0xFF; z++ {
			cases = append(cases, cjkDecCase(m, "EUC-JP", []byte{0x8F, byte(y), byte(z)}))
		}
	}
	cjkRunAll(t, cases)
}

func TestStdEncodingCjk_Gb18030Four(t *testing.T) {
	m := cjkModelGet(t)
	var cases []encCase
	for _, b1 := range []byte{0x81, 0x84, 0x90, 0xE3, 0xFE} {
		for b2 := byte(0x2F); b2 <= 0x3A; b2++ {
			for b3 := 0x80; b3 <= 0xFF; b3++ {
				for b4 := byte(0x2F); b4 <= 0x3A; b4++ {
					b := []byte{b1, b2, byte(b3), b4}
					cases = append(cases, cjkDecCase(m, "gb18030", b))
					if b4 == 0x2F || b4 == 0x35 {
						cases = append(cases, cjkDecsCase(m, "gb18030", b))
					}
				}
				cases = append(cases, cjkDecCase(m, "gb18030", []byte{b1, b2, byte(b3)}))
			}
		}
	}
	cjkRunAll(t, cases)
}

// cjkEachCase encodes each scalar of rs alone.
func cjkEachCase(m *cjkModel, name string, rs []rune) encCase {
	var want strings.Builder
	want.WriteString("ok")
	for _, r := range rs {
		out, bad := m.encode(name, string(r))
		if bad >= 0 {
			want.WriteString(" -")
		} else {
			want.WriteString(" " + encHex(out))
		}
	}
	return encCase{in: "each " + name + " " + encHex([]byte(string(rs))), want: want.String()}
}

func TestStdEncodingCjk_EncodeEvery(t *testing.T) {
	m := cjkModelGet(t)
	var cases []encCase
	for _, name := range cjkNames {
		block := func(lo, hi rune) {
			var rs []rune
			for r := lo; r < hi; r++ {
				if r >= 0xD800 && r <= 0xDFFF {
					continue
				}
				rs = append(rs, r)
			}
			if len(rs) > 0 {
				cases = append(cases, cjkEachCase(m, name, rs))
			}
		}
		for lo := rune(0); lo < 0x30000; lo += 256 {
			if lo >= 0x10000 && lo < 0x20000 && lo != 0x1F600 {
				continue
			}
			block(lo, lo+256)
		}
		block(0x10FF00, 0x110000)
	}
	cjkRunAll(t, cases)
}

// cjkWptFile reads a file of wpt-encoding.
func cjkWptFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "wpt-encoding", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cjkUnhexSpaced is "81 40" as bytes.
func cjkUnhexSpaced(t *testing.T, s string) []byte {
	t.Helper()
	var out []byte
	for _, f := range strings.Fields(s) {
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			t.Fatalf("bad byte %q", f)
		}
		out = append(out, byte(v))
	}
	return out
}

// cjkJsString unquotes a JavaScript string literal's body: \uXXXX,
// \u{X...}, \xXX, \\ and \", and UTF-8 as is. Surrogate pairs join.
func cjkJsString(t *testing.T, s string) string {
	t.Helper()
	var rs []rune
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			r, n := utf8.DecodeRuneInString(s[i:])
			rs = append(rs, r)
			i += n
			continue
		}
		switch s[i+1] {
		case 'u':
			if s[i+2] == '{' {
				j := strings.IndexByte(s[i:], '}')
				v, _ := strconv.ParseUint(s[i+3:i+j], 16, 32)
				rs = append(rs, rune(v))
				i += j + 1
				continue
			}
			v, _ := strconv.ParseUint(s[i+2:i+6], 16, 32)
			rs = append(rs, rune(v))
			i += 6
		case 'x':
			v, _ := strconv.ParseUint(s[i+2:i+4], 16, 32)
			rs = append(rs, rune(v))
			i += 4
		default:
			rs = append(rs, rune(s[i+1]))
			i += 2
		}
	}
	var out []rune
	for k := 0; k < len(rs); k++ {
		if rs[k] >= 0xD800 && rs[k] <= 0xDBFF && k+1 < len(rs) && rs[k+1] >= 0xDC00 && rs[k+1] <= 0xDFFF {
			out = append(out, 0x10000+(rs[k]-0xD800)<<10+(rs[k+1]-0xDC00))
			k++
			continue
		}
		out = append(out, rs[k])
	}
	return string(out)
}

// cjkPctDecode undoes a URL query's percent-encoding, leaving a % that no
// two hex digits follow.
func cjkPctDecode(s string) []byte {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				out = append(out, byte(v))
				i += 2
				continue
			}
		}
		out = append(out, s[i])
	}
	return out
}

func TestStdEncodingCjk_Wpt(t *testing.T) {
	m := cjkModelGet(t)
	dir := cjkDir(t)
	var cases []encCase
	// The character lists: each span's bytes are the encoder's output for
	// its code point, and decode as the model says.
	span := regexp.MustCompile(`data-cp="([0-9A-F]+)" data-bytes="([0-9A-F ]+)"`)
	lists := map[string][]string{
		"Shift_JIS":   {"legacy-mb-japanese/shift_jis/sjis_chars.html"},
		"EUC-JP":      {"legacy-mb-japanese/euc-jp/eucjp_chars.html"},
		"ISO-2022-JP": {"legacy-mb-japanese/iso-2022-jp/iso2022jp_chars.html"},
		"EUC-KR":      {"legacy-mb-korean/euc-kr/euckr_chars.html"},
		"Big5":        {"legacy-mb-tchinese/big5/big5_chars.html", "legacy-mb-tchinese/big5/big5_chars_extra.html"},
	}
	total := 0
	for name, files := range lists {
		for _, f := range files {
			n := 0
			for _, mt := range span.FindAllStringSubmatch(cjkWptFile(t, dir, f), -1) {
				cp, _ := strconv.ParseUint(mt[1], 16, 32)
				b := cjkUnhexSpaced(t, mt[2])
				if strings.HasSuffix(f, "_extra.html") {
					// big5_chars_extra is the pointers below 5024, which the
					// encoder leaves out: their bytes decode to the code
					// point, but for the pairs, which the model knows.
					if got, _ := m.decode(name, b); got != string(rune(cp)) && len([]rune(got)) != 2 {
						t.Fatalf("model: Big5 %x decodes to %q, the page says U+%X", b, got, cp)
					}
					cases = append(cases, cjkDecCase(m, name, b))
				} else if name == "EUC-JP" && b[0] == 0x8F {
					// eucjp_chars lists index jis0212 too, which only the
					// decoder reads: the bytes decode to the code point.
					cases = append(cases, encCase{in: "dec " + name + " " + encHex(b), want: "ok " + encHex([]byte(string(rune(cp))))})
				} else {
					cases = append(cases, encCase{in: "enc " + name + " " + encHex([]byte(string(rune(cp)))), want: "ok " + encHex(b)})
					cases = append(cases, cjkDecCase(m, name, b))
				}
				n++
			}
			if n < 100 {
				t.Fatalf("%s: %d spans", f, n)
			}
			total += n
		}
	}
	// The error lists: span i of *_errors.html decodes, followed in the
	// page by "<", to assertion i of *-decode-errors.html.
	errs := map[string][2]string{
		"Shift_JIS":   {"legacy-mb-japanese/shift_jis/sjis_errors.html", "legacy-mb-japanese/shift_jis/sjis-decode-errors.html"},
		"EUC-JP":      {"legacy-mb-japanese/euc-jp/eucjp_errors.html", "legacy-mb-japanese/euc-jp/eucjp-decode-errors.html"},
		"ISO-2022-JP": {"legacy-mb-japanese/iso-2022-jp/iso2022jp_errors.html", "legacy-mb-japanese/iso-2022-jp/iso2022jp-decode-errors.html"},
		"EUC-KR":      {"legacy-mb-korean/euc-kr/euckr_errors.html", "legacy-mb-korean/euc-kr/euckr-decode-errors.html"},
		"Big5":        {"legacy-mb-tchinese/big5/big5_errors.html", "legacy-mb-tchinese/big5/big5-decode-errors.html"},
	}
	spanRe := regexp.MustCompile(`(?s)<span>(.*?)</span>`)
	assertRe := regexp.MustCompile(`assert_equals\(\s*nodes\[\w+\]\.textContent,\s*"((?:[^"\\]|\\.)*)"`)
	for name, f := range errs {
		ins := spanRe.FindAllStringSubmatch(cjkWptFile(t, dir, f[0]), -1)
		wants := assertRe.FindAllStringSubmatch(cjkWptFile(t, dir, f[1]), -1)
		if len(ins) == 0 || len(ins) != len(wants) {
			t.Fatalf("%s: %d spans, %s: %d assertions", f[0], len(ins), f[1], len(wants))
		}
		for i := range ins {
			b := append([]byte(ins[i][1]), '<')
			want := cjkJsString(t, wants[i][1]) + "<"
			cases = append(cases, encCase{in: "dec " + name + " " + encHex(b), want: "ok " + encHex([]byte(want))})
			cases = append(cases, cjkDecCase(m, name, b))
		}
		total += len(ins)
	}
	// decode([bytes], "text") in the decoder scripts.
	decRe := regexp.MustCompile(`decode\(\s*\[([^\]]*)\],\s*"((?:[^"\\]|\\.)*)"`)
	for _, d := range []struct{ file, name string }{
		{"legacy-mb-schinese/gb18030/gb18030-decoder.any.js", "gb18030"},
		{"legacy-mb-schinese/gb18030/gb18030-decoder.any.js", "GBK"},
		{"iso-2022-jp-decoder.any.js", "ISO-2022-JP"},
	} {
		n := 0
		for _, mt := range decRe.FindAllStringSubmatch(cjkWptFile(t, dir, d.file), -1) {
			var b []byte
			for _, f := range strings.Split(mt[1], ",") {
				f = strings.TrimSpace(f)
				if f == "" {
					continue
				}
				v, err := strconv.ParseUint(f, 0, 8)
				if err != nil {
					t.Fatalf("%s: byte %q", d.file, f)
				}
				b = append(b, byte(v))
			}
			want := cjkJsString(t, mt[2])
			cases = append(cases, encCase{in: "dec " + d.name + " " + encHex(b), want: "ok " + encHex([]byte(want))})
			n++
		}
		if n == 0 {
			t.Fatalf("%s: no cases", d.file)
		}
		total += n
	}
	// gbk-decoder.any.js: gbkPointers[i], as GBK's two bytes, decodes to
	// codePoints[i].
	gbk := cjkWptFile(t, dir, "legacy-mb-schinese/gbk/gbk-decoder.any.js")
	arr := func(name string) []int {
		at := strings.Index(gbk, name+" = [")
		end := strings.Index(gbk[at:], "]")
		if at < 0 || end < 0 {
			t.Fatalf("gbk-decoder.any.js: no %s", name)
		}
		var out []int
		for _, f := range strings.Split(gbk[at+len(name)+4:at+end], ",") {
			if f = strings.TrimSpace(f); f != "" {
				v, err := strconv.ParseUint(f, 0, 32)
				if err != nil {
					t.Fatalf("gbk-decoder.any.js: %q", f)
				}
				out = append(out, int(v))
			}
		}
		return out
	}
	ptrs, cps := arr("gbkPointers"), arr("codePoints")
	if len(ptrs) == 0 || len(ptrs) != len(cps) {
		t.Fatalf("gbk-decoder.any.js: %d pointers, %d code points", len(ptrs), len(cps))
	}
	for i, p := range ptrs {
		trail := p % 190
		off := 0x41
		if trail < 0x3F {
			off = 0x40
		}
		b := []byte{byte(p/190 + 0x81), byte(trail + off)}
		cases = append(cases, encCase{in: "dec GBK " + encHex(b), want: "ok " + encHex([]byte(string(rune(cps[i]))))})
	}
	total += len(ptrs)
	// The ranges loop of gb18030-decoder.any.js: each range's first
	// pointer as four bytes decodes to its code point.
	for _, r := range m.x.ranges {
		p := int(r[0])
		if p > 39419 && p < 189000 {
			continue
		}
		b := []byte{byte(p/12600 + 0x81), byte(p%12600/1260 + 0x30), byte(p%1260/10 + 0x81), byte(p%10 + 0x30)}
		cases = append(cases, encCase{in: "dec gb18030 " + encHex(b), want: "ok " + encHex([]byte(string(rune(r[1]))))})
	}
	// encode("text", "query") in the encoder pages: a query holding
	// "%26%23" is the error mode's &#...; and Encode fails.
	encRe := regexp.MustCompile(`encode\(\s*"((?:[^"\\]|\\.)*)",\s*"((?:[^"\\]|\\.)*)"`)
	for _, d := range []struct{ file, name string }{
		{"legacy-mb-schinese/gb18030/gb18030-encoder.html", "gb18030"},
		{"legacy-mb-schinese/gbk/gbk-encoder.html", "GBK"},
		{"big5-encoder.html", "Big5"},
		{"iso-2022-jp-encoder.html", "ISO-2022-JP"},
	} {
		n := 0
		for _, mt := range encRe.FindAllStringSubmatch(cjkWptFile(t, dir, d.file), -1) {
			in := cjkJsString(t, mt[1])
			q := cjkJsString(t, mt[2])
			if strings.Contains(q, "%26%23") {
				_, bad := m.encode(d.name, in)
				if bad < 0 {
					t.Fatalf("%s: %q: the page says error, the model encodes it", d.file, in)
				}
				cases = append(cases, encCase{in: "enc " + d.name + " " + encHex([]byte(in)), want: fmt.Sprintf("err BadInput(offset: %d)", bad)})
			} else {
				cases = append(cases, encCase{in: "enc " + d.name + " " + encHex([]byte(in)), want: "ok " + encHex(cjkPctDecode(q))})
			}
			n++
		}
		if n == 0 {
			t.Fatalf("%s: no cases", d.file)
		}
		total += n
	}
	extra := cjkWptExtra(t, m, dir)
	cases = append(cases, extra...)
	t.Logf("%d WPT cases, then %d records of textdecoder-mistakes, textdecoder-eof and streams/decode-non-utf8", total, len(extra))
	cjkRunAll(t, cases)
}

// cjkWptLabels are the WPT files' labels by the model's names.
var cjkWptLabels = map[string]string{
	"gbk": "GBK", "gb18030": "gb18030", "big5": "Big5", "euc-jp": "EUC-JP",
	"iso-2022-jp": "ISO-2022-JP", "shift_jis": "Shift_JIS", "euc-kr": "EUC-KR",
}

// cjkWptName is the model's name of a WPT label.
func cjkWptName(t *testing.T, label string) string {
	t.Helper()
	n, ok := cjkWptLabels[strings.ToLower(label)]
	if !ok {
		t.Fatalf("WPT label %q is not a CJK encoding's", label)
	}
	return n
}

// cjkJsBytes is a JavaScript list of byte literals, "0x1b, 27".
func cjkJsBytes(t *testing.T, s string) []byte {
	t.Helper()
	var b []byte
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		v, err := strconv.ParseUint(strings.ReplaceAll(f, "_", ""), 0, 8)
		if err != nil {
			t.Fatalf("byte %q", f)
		}
		b = append(b, byte(v))
	}
	return b
}

// cjkWptDec is Decode of b to text, and DecodeStrict of it: text, or,
// when invalid, the model's offset of the first malformed unit, which
// must exist.
func cjkWptDec(t *testing.T, m *cjkModel, name string, b []byte, text string, invalid bool) []encCase {
	t.Helper()
	h := encHex(b)
	cs := []encCase{{in: "dec " + name + " " + h, want: "ok " + encHex([]byte(text))}}
	if !invalid {
		return append(cs, encCase{in: "decs " + name + " " + h, want: "ok " + encHex([]byte(text))})
	}
	_, bad := m.decode(name, b)
	if bad < 0 {
		t.Fatalf("model: %s %x is valid, WPT says DecodeStrict fails", name, b)
	}
	return append(cs, encCase{in: "decs " + name + " " + h, want: fmt.Sprintf("err BadInput(offset: %d)", bad)})
}

// cjkWptPush is b decoded by one Decoder in pieces ending at cuts.
func cjkWptPush(name string, b []byte, cuts []int, text string) encCase {
	var cs []string
	for _, c := range cuts {
		cs = append(cs, strconv.Itoa(c))
	}
	return encCase{in: "push " + name + " " + strings.Join(cs, ",") + ", " + encHex(b), want: "ok " + encHex([]byte(text))}
}

// cjkWptSeq is one Decoder fed chunks in turn, each ending the stream when
// its last is true, and the text each Push returns.
func cjkWptSeq(name string, chunks [][]byte, last []bool, texts []string) encCase {
	var in, want []string
	for i, c := range chunks {
		k := "s"
		if last[i] {
			k = "e"
		}
		in = append(in, k+encHex(c))
		want = append(want, encHex([]byte(texts[i])))
	}
	return encCase{in: "seq " + name + " " + strings.Join(in, ","), want: "ok " + strings.Join(want, ",")}
}

// cjkWptWindow is the text of src from the last "from" before the title
// to the title, which must occur once.
func cjkWptWindow(t *testing.T, src, from, title string) string {
	t.Helper()
	at := strings.Index(src, title)
	if at < 0 || strings.Count(src, title) != 1 {
		t.Fatalf("WPT: title %q occurs %d times", title, strings.Count(src, title))
	}
	start := strings.LastIndex(src[:at], from)
	if start < 0 {
		t.Fatalf("WPT: no %q before %q", from, title)
	}
	return src[start:at]
}

// cjkWptExtra is the CJK cases of three pinned WPT files that the lists
// and decoder scripts above do not hold: textdecoder-mistakes.any.js
// (ASCII bytes, Big5's 0x80, concatenated ISO-2022-JP outputs, gb18030's
// and GBK's pointer ranges, bytes pushed back after U+FFFD, EUC-JP's
// JIS X 0212 state whole and split at every byte, sticky state across
// decode calls, streams with an empty chunk, WPT's mislabelled pages and a
// non-ASCII label), textdecoder-eof.any.js (Big5 at the end of the queue,
// whole and streamed) and streams/decode-non-utf8.any.js. Mk has no strict
// Decoder, so the two fatal-streaming tests of textdecoder-mistakes (EUC-JP
// after a fatal error, and ISO-2022-JP's mode kept across one) are left
// out; every other fatal assertion is DecodeStrict. Expected texts are the
// files'; only a failing DecodeStrict's offset is the model's.
func cjkWptExtra(t *testing.T, m *cjkModel, dir string) []encCase {
	t.Helper()
	var cases []encCase
	add := func(cs ...encCase) { cases = append(cases, cs...) }
	const bad = "�"
	js := `'((?:[^'\\]|\\.)*)'`
	jsq := `"((?:[^"\\]|\\.)*)"`

	mis := strings.ReplaceAll(cjkWptFile(t, dir, "textdecoder-mistakes.any.js"), "\r\n", "\n")
	// The blocks of [[bytes], 'text'] literals (and check() calls), each
	// read by the test whose title follows it.
	vecRe := regexp.MustCompile(`\[\[([0-9a-fx_, ]+)\],\s*` + js + `\]`)
	checkRe := regexp.MustCompile(`\bcheck\(\[([0-9a-fx_, ]+)\],\s*(?:` + js + `|String\.fromCodePoint\((0x[0-9a-f_]+)\))(,\s*true)?\)`)
	keyRe := regexp.MustCompile(`^\s*'?([a-z0-9_-]+)'?: \[`)
	titleRe := regexp.MustCompile("^\\s*\\}, [`']([^`']+)[`']\\)")
	type vec struct {
		label   string
		b       []byte
		text    string
		invalid bool
	}
	var pending []vec
	label := ""
	blocks := map[string][]vec{}
	for _, line := range strings.Split(mis, "\n") {
		switch {
		case strings.Contains(line, "const vectors = "):
			pending, label = nil, ""
		case strings.Contains(line, "vectors.gbk = vectors.gb18030"):
			for _, v := range pending {
				if v.label == "gb18030" {
					v.label = "gbk"
					pending = append(pending, v)
				}
			}
		case strings.HasPrefix(line, "for (const encoding of ['gb18030', 'gbk'])"):
			pending, label = nil, ""
		}
		if k := keyRe.FindStringSubmatch(line); k != nil {
			label = k[1]
		}
		for _, mt := range vecRe.FindAllStringSubmatch(line, -1) {
			pending = append(pending, vec{label: label, b: cjkJsBytes(t, mt[1]), text: cjkJsString(t, mt[2])})
		}
		for _, mt := range checkRe.FindAllStringSubmatch(line, -1) {
			text := cjkJsString(t, mt[2])
			if mt[3] != "" {
				cp, _ := strconv.ParseUint(strings.ReplaceAll(mt[3][2:], "_", ""), 16, 32)
				text = string(rune(cp))
			}
			pending = append(pending, vec{b: cjkJsBytes(t, mt[1]), text: text, invalid: mt[4] != ""})
		}
		if tt := titleRe.FindStringSubmatch(line); tt != nil {
			if _, seen := blocks[tt[1]]; !seen && len(pending) > 0 {
				blocks[tt[1]] = pending
			}
			pending, label = nil, ""
		}
	}
	// Each block's size, so that a regexp that stops matching fails here.
	sizes := map[string]int{
		"${encoding} version and ranges":                       27,
		"Replacement, push back ASCII characters: ${encoding}": 18,
		"EUC-JP JIS0212 state: ${bytes}":                       4,
		"Sticky multibyte state: ${encoding}":                  31,
		"stream: ${encoding}":                                  9,
		"WPT mislabels: ${encoding}":                           9,
	}
	for title, n := range sizes {
		if len(blocks[title]) != n {
			t.Fatalf("textdecoder-mistakes.any.js: %d vectors before %q, want %d", len(blocks[title]), title, n)
		}
	}
	for _, v := range blocks["${encoding} version and ranges"] {
		for _, name := range []string{"gb18030", "GBK"} {
			add(cjkWptDec(t, m, name, v.b, v.text, v.invalid)...)
		}
	}
	for _, v := range blocks["Replacement, push back ASCII characters: ${encoding}"] {
		add(cjkWptDec(t, m, cjkWptName(t, v.label), v.b, v.text, true)...)
	}
	for _, v := range blocks["EUC-JP JIS0212 state: ${bytes}"] {
		add(cjkWptDec(t, m, "EUC-JP", v.b, v.text, strings.Contains(v.text, bad))...)
		for split := 1; split < len(v.b); split++ {
			add(cjkWptPush("EUC-JP", v.b, []int{split}, v.text))
		}
	}
	for _, v := range blocks["Sticky multibyte state: ${encoding}"] {
		name := cjkWptName(t, v.label)
		add(cjkWptDec(t, m, name, v.b, v.text, strings.Contains(v.text, bad))...)
		// One decoder: "@", the vector, then "@", "*" and "B", each a
		// whole stream.
		add(cjkWptSeq(name, [][]byte{{0x40}, v.b, {0x40}, {0x2a}, {0x42}}, []bool{true, true, true, true, true}, []string{"@", v.text, "@", "*", "B"}))
	}
	for _, v := range blocks["stream: ${encoding}"] {
		name := cjkWptName(t, v.label)
		n := len(v.b)
		add(cjkWptDec(t, m, name, v.b, v.text, strings.Contains(v.text, bad))[0])
		// The test's four chunkings; the third pushes an empty chunk.
		add(cjkWptPush(name, v.b, []int{1}, v.text), cjkWptPush(name, v.b, []int{1, n}, v.text),
			cjkWptPush(name, v.b, []int{1, 1, n}, v.text), cjkWptPush(name, v.b, []int{min(2, n), n}, v.text))
	}
	for _, v := range blocks["WPT mislabels: ${encoding}"] {
		add(cjkWptDec(t, m, cjkWptName(t, v.label), v.b, v.text, false)...)
	}
	// Most legacy multi-byte encodings are ASCII supersets.
	w := cjkWptWindow(t, mis, "for (const encoding of [", "Most legacy multi-byte encodings are ASCII supersets")
	encs := regexp.MustCompile(`for \(const encoding of \[([^\]]*)\]\)`).FindStringSubmatch(w)
	bs := regexp.MustCompile(`for \(const b of \[([^\]]*)\]\)`).FindStringSubmatch(w)
	if encs == nil || bs == nil {
		t.Fatalf("textdecoder-mistakes.any.js: the ASCII supersets test has no lists")
	}
	nAscii := 0
	for _, l := range regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(encs[1], -1) {
		for _, c := range cjkJsBytes(t, bs[1]) {
			add(cjkWptDec(t, m, cjkWptName(t, l[1]), []byte{c}, string(rune(c)), false)...)
			nAscii++
		}
	}
	// specific: big5.
	w = cjkWptWindow(t, mis, "test(() => {", "'specific: big5'")
	loose := regexp.MustCompile(`loose\.decode\(u\(([^)]*)\)\), `+js+`\)`).FindAllStringSubmatch(w, -1)
	if len(loose) != 1 || !strings.Contains(w, "fatal.decode(u("+loose[0][1]+"))") {
		t.Fatalf("textdecoder-mistakes.any.js: specific: big5 has %d cases", len(loose))
	}
	add(cjkWptDec(t, m, "Big5", cjkJsBytes(t, loose[0][1]), cjkJsString(t, loose[0][2]), true)...)
	// Concatenating two ISO-2022-JP outputs: a is valid, a twice is not.
	w = cjkWptWindow(t, mis, "test(() => {", "'Concatenating two ISO-2022-JP outputs is not always valid'")
	one := regexp.MustCompile(`const a = u\(([^)]*)\)[^\n]*\n\s*assert_equals\(fatal\.decode\(a\), `+js+`\)`).FindAllStringSubmatch(w, -1)
	two := regexp.MustCompile(`assert_equals\(loose\.decode\(u\(\.\.\.a, \.\.\.a\)\), `+js+`\)`).FindAllStringSubmatch(w, -1)
	if len(one) != 2 || len(two) != 2 {
		t.Fatalf("textdecoder-mistakes.any.js: concatenated ISO-2022-JP: %d and %d cases", len(one), len(two))
	}
	for i := range one {
		a := cjkJsBytes(t, one[i][1])
		add(cjkWptDec(t, m, "ISO-2022-JP", a, cjkJsString(t, one[i][2]), false)...)
		add(cjkWptDec(t, m, "ISO-2022-JP", append(append([]byte{}, a...), a...), cjkJsString(t, two[i][1]), true)...)
	}
	// gbk decoder is gb18030 decoder: no texts, so the model's, which must
	// agree for the two.
	w = cjkWptWindow(t, mis, "test(() => {", "'gbk decoder is gb18030 decoder'")
	pairs := regexp.MustCompile(`\[(\d+), (\d+)\]`).FindAllStringSubmatch(w, -1)
	if len(pairs) != 8 {
		t.Fatalf("textdecoder-mistakes.any.js: gbk decoder is gb18030 decoder: %d pairs", len(pairs))
	}
	for _, p := range pairs {
		b := cjkJsBytes(t, p[1]+","+p[2])
		g, _ := m.decode("gb18030", b)
		if k, _ := m.decode("GBK", b); k != g {
			t.Fatalf("model: GBK and gb18030 differ on %x", b)
		}
		add(encCase{in: "dec GBK " + encHex(b), want: "ok " + encHex([]byte(g))}, encCase{in: "dec gb18030 " + encHex(b), want: "ok " + encHex([]byte(g))})
	}
	// Sticky multibyte state: euc-jp, three whole streams on one decoder.
	w = cjkWptWindow(t, mis, "test(() => {", "'Sticky multibyte state: euc-jp'")
	var chunks [][]byte
	var texts []string
	for _, mt := range regexp.MustCompile(`assert_equals\(d\.decode\(Uint8Array\.of\(([^)]*)\)\), `+js+`\)`).FindAllStringSubmatch(w, -1) {
		chunks, texts = append(chunks, cjkJsBytes(t, mt[1])), append(texts, cjkJsString(t, mt[2]))
	}
	if len(chunks) != 3 {
		t.Fatalf("textdecoder-mistakes.any.js: sticky euc-jp has %d calls", len(chunks))
	}
	add(cjkWptSeq("EUC-JP", chunks, []bool{true, true, true}, texts))
	// labels: invalid non-ascii (U+212A KELVIN SIGN folds to k only
	// outside the standard's ASCII lower-casing).
	w = cjkWptWindow(t, mis, "test(() => {", "'labels: invalid non-ascii'")
	labs := regexp.MustCompile(`const bad = \[([^\]]*)\]`).FindStringSubmatch(w)
	if labs == nil {
		t.Fatalf("textdecoder-mistakes.any.js: no invalid labels")
	}
	for _, l := range regexp.MustCompile(js).FindAllStringSubmatch(labs[1], -1) {
		add(encCase{in: "look " + encHex([]byte(cjkJsString(t, l[1]))), want: "err UnknownLabel"})
	}

	// textdecoder-eof.any.js: Big5 at the end of the queue, whole, then
	// streamed on one decoder, each decode() ending a stream.
	eof := cjkWptFile(t, dir, "textdecoder-eof.any.js")
	whole := regexp.MustCompile(`new TextDecoder\("Big5"\)\.decode\(new Uint8Array\(\[([^\]]*)\]\)\), `+jsq+`\)`).FindAllStringSubmatch(eof, -1)
	if len(whole) != 3 {
		t.Fatalf("textdecoder-eof.any.js: %d Big5 cases", len(whole))
	}
	for _, mt := range whole {
		text := cjkJsString(t, mt[2])
		add(cjkWptDec(t, m, "Big5", cjkJsBytes(t, mt[1]), text, strings.Contains(text, bad))...)
	}
	chunks, texts = nil, nil
	var last []bool
	nSeq := 0
	for _, mt := range regexp.MustCompile(`big5Decoder\.decode\((?:new Uint8Array\(\[([^\]]*)\]\), \{ stream: true \})?\), `+jsq+`\)`).FindAllStringSubmatch(eof, -1) {
		end := !strings.Contains(mt[0], "stream: true")
		chunks, texts, last = append(chunks, cjkJsBytes(t, mt[1])), append(texts, cjkJsString(t, mt[2])), append(last, end)
		if end {
			add(cjkWptSeq("Big5", chunks, last, texts))
			chunks, texts, last = nil, nil, nil
			nSeq++
		}
	}
	if nSeq != 2 || len(chunks) != 0 {
		t.Fatalf("textdecoder-eof.any.js: %d Big5 streams", nSeq)
	}

	// streams/decode-non-utf8.any.js: value decodes to expected, and
	// invalid to one U+FFFD, which a strict decode refuses.
	sn := cjkWptFile(t, dir, "streams/decode-non-utf8.any.js")
	objRe := regexp.MustCompile(`name: '([^']+)',\s*value: \[([^\]]*)\],\s*expected: ` + jsq + `,\s*invalid: (?:\[([^\]]*)\]|undefined)`)
	nStream := 0
	for _, mt := range objRe.FindAllStringSubmatch(sn, -1) {
		if _, ok := cjkWptLabels[strings.ToLower(mt[1])]; !ok {
			continue
		}
		name := cjkWptName(t, mt[1])
		text := cjkJsString(t, mt[3])
		add(cjkWptDec(t, m, name, cjkJsBytes(t, mt[2]), text, strings.Contains(text, bad))...)
		add(cjkWptDec(t, m, name, cjkJsBytes(t, mt[4]), bad, true)...)
		nStream++
	}
	if nStream != 2 {
		t.Fatalf("streams/decode-non-utf8.any.js: %d CJK encodings", nStream)
	}
	if nAscii != 30 {
		t.Fatalf("textdecoder-mistakes.any.js: %d ASCII cases", nAscii)
	}
	return cases
}
