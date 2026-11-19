// Why: std.encoding's decoders and encoders must behave as the WHATWG Encoding Standard says, byte for byte.
// What: a Go model of the standard's UTF-8, UTF-16 and single-byte algorithms, the web-platform-tests
// encoding/ cases (labels, invalid labels, fatal, surrogates, byte order marks, end of input), and index round trips.
// How: the model reads the index files of Mk-evidence/whatwg/encoding; every byte of every single-byte
// encoding decodes to its index entry and every code point of the index encodes back.

package std

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// encModel is one encoding of the model: its name and, for a single-byte
// encoding, its index.
type encModel struct {
	name  string
	index *[128]uint16
}

func encModels(t *testing.T) []encModel {
	t.Helper()
	dir := encEvidence()
	if dir == "" {
		t.Skip("Mk-evidence/whatwg/encoding not found (set MK_EVIDENCE)")
	}
	var out []encModel
	for _, s := range encSpecs {
		m := encModel{name: s.name}
		if s.index != "" {
			idx, err := encIndex(dir, s.index)
			if err != nil {
				t.Fatal(err)
			}
			m.index = &idx
		}
		out = append(out, m)
	}
	return out
}

// whatwgDecode decodes b by the standard's decoder for m, removing a
// leading byte order mark of m's own as TextDecoder does, and returns the
// text and the offset of the first malformed unit's start (-1 for none).
func whatwgDecode(m encModel, b []byte) (string, int) {
	return whatwgDecodeBom(m, b, false)
}

// whatwgDecodeBom is whatwgDecode, with the mark kept as text when keep
// is set (TextDecoder's ignoreBOM, DecodeKeepBom).
func whatwgDecodeBom(m encModel, b []byte, keep bool) (string, int) {
	var out []rune
	bad := -1
	errAt := func(i int) {
		out = append(out, 0xFFFD)
		if bad < 0 {
			bad = i
		}
	}
	skip := 0
	switch {
	case keep:
	case m.name == "UTF-8" && len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF:
		skip = 3
	case m.name == "UTF-16LE" && len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE:
		skip = 2
	case m.name == "UTF-16BE" && len(b) >= 2 && b[0] == 0xFE && b[1] == 0xFF:
		skip = 2
	}
	switch m.name {
	case "UTF-8":
		needed, seen, cp, start := 0, 0, rune(0), 0
		lower, upper := byte(0x80), byte(0xBF)
		for i := skip; i < len(b); i++ {
			c := b[i]
			if needed == 0 {
				start = i
				switch {
				case c <= 0x7F:
					out = append(out, rune(c))
				case c >= 0xC2 && c <= 0xDF:
					needed, cp = 1, rune(c&0x1F)
				case c >= 0xE0 && c <= 0xEF:
					if c == 0xE0 {
						lower = 0xA0
					}
					if c == 0xED {
						upper = 0x9F
					}
					needed, cp = 2, rune(c&0xF)
				case c >= 0xF0 && c <= 0xF4:
					if c == 0xF0 {
						lower = 0x90
					}
					if c == 0xF4 {
						upper = 0x8F
					}
					needed, cp = 3, rune(c&0x7)
				default:
					errAt(i)
				}
				continue
			}
			if c < lower || c > upper {
				needed, seen, cp = 0, 0, 0
				lower, upper = 0x80, 0xBF
				errAt(start)
				i-- // the byte is read again
				continue
			}
			lower, upper = 0x80, 0xBF
			cp = cp<<6 | rune(c&0x3F)
			seen++
			if seen == needed {
				out = append(out, cp)
				needed, seen, cp = 0, 0, 0
			}
		}
		if needed != 0 {
			errAt(start)
		}
	case "UTF-16LE", "UTF-16BE":
		be := m.name == "UTF-16BE"
		leadByte, leadSur := -1, -1
		byteAt, surAt := 0, 0
		for i := skip; i < len(b); i++ {
			if leadByte < 0 {
				leadByte, byteAt = int(b[i]), i
				continue
			}
			cu := leadByte | int(b[i])<<8
			if be {
				cu = leadByte<<8 | int(b[i])
			}
			leadByte = -1
			if leadSur >= 0 {
				ls := leadSur
				leadSur = -1
				if cu >= 0xDC00 && cu <= 0xDFFF {
					out = append(out, rune(0x10000+(ls-0xD800)<<10+(cu-0xDC00)))
					continue
				}
				errAt(surAt)
				i -= 2 // the two bytes of cu are read again
				continue
			}
			if cu >= 0xD800 && cu <= 0xDBFF {
				leadSur, surAt = cu, byteAt
				continue
			}
			if cu >= 0xDC00 && cu <= 0xDFFF {
				errAt(byteAt)
				continue
			}
			out = append(out, rune(cu))
		}
		if leadSur >= 0 {
			errAt(surAt)
		} else if leadByte >= 0 {
			errAt(byteAt)
		}
	case "replacement":
		if len(b) > 0 {
			errAt(0)
		}
	case "x-user-defined":
		for _, c := range b {
			if c < 0x80 {
				out = append(out, rune(c))
			} else {
				out = append(out, rune(0xF780+int(c)-0x80))
			}
		}
	default:
		for i, c := range b {
			switch {
			case c < 0x80:
				out = append(out, rune(c))
			case m.index[c-0x80] == 0:
				errAt(i)
			default:
				out = append(out, rune(m.index[c-0x80]))
			}
		}
	}
	return string(out), bad
}

// whatwgEncode is s in m, or the offset of the first scalar m has no bytes
// for (-2 for replacement, which encodes nothing).
func whatwgEncode(m encModel, s string) ([]byte, int) {
	var out []byte
	switch m.name {
	case "replacement":
		return nil, -2
	case "UTF-8":
		return []byte(s), -1
	case "UTF-16LE", "UTF-16BE":
		for _, r := range s {
			var units []int
			if r >= 0x10000 {
				v := int(r) - 0x10000
				units = []int{0xD800 | v>>10, 0xDC00 | v&0x3FF}
			} else {
				units = []int{int(r)}
			}
			for _, u := range units {
				if m.name == "UTF-16BE" {
					out = append(out, byte(u>>8), byte(u))
				} else {
					out = append(out, byte(u), byte(u>>8))
				}
			}
		}
		return out, -1
	}
	for i, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case m.name == "x-user-defined":
			if r < 0xF780 || r > 0xF7FF {
				return nil, i
			}
			out = append(out, byte(r-0xF780+0x80))
		default:
			p := -1
			for k, cp := range m.index {
				if rune(cp) == r {
					p = k
					break
				}
			}
			if p < 0 {
				return nil, i
			}
			out = append(out, byte(0x80+p))
		}
	}
	return out, -1
}

func decCase(m encModel, b []byte) encCase {
	s, _ := whatwgDecode(m, b)
	return encCase{in: "dec " + m.name + " " + encHex(b), want: "ok " + encHex([]byte(s))}
}

func decsCase(m encModel, b []byte) encCase {
	s, bad := whatwgDecode(m, b)
	want := "ok " + encHex([]byte(s))
	if bad >= 0 {
		want = "err " + b64Bad(bad)
	}
	return encCase{in: "decs " + m.name + " " + encHex(b), want: want}
}

func encTextCase(m encModel, s string) encCase {
	b, bad := whatwgEncode(m, s)
	want := "ok " + encHex(b)
	switch {
	case bad == -2:
		want = "err Unsupported"
	case bad >= 0:
		want = "err " + b64Bad(bad)
	}
	return encCase{in: "enc " + m.name + " " + encHex([]byte(s)), want: want}
}

func pushCase(m encModel, b []byte, cuts []int) encCase {
	s, _ := whatwgDecode(m, b)
	var c []string
	for _, k := range cuts {
		c = append(c, fmt.Sprint(k))
	}
	if len(c) == 0 {
		c = []string{"0"}
	}
	return encCase{in: "push " + m.name + " " + strings.Join(c, ",") + " " + encHex(b), want: "ok " + encHex([]byte(s)) + " true"}
}

// encLabelTable is encodings.json: name -> labels, and the CJK names.
func encLabelTable(t *testing.T) (map[string][]string, map[string]bool) {
	t.Helper()
	dir := encEvidence()
	labels, err := encLabels(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string][]string{}
	cjk := map[string]bool{}
	names := map[int]string{}
	for _, s := range encSpecs {
		names[s.id] = s.name
	}
	for l, id := range labels {
		if id == encUnsupported {
			cjk[l] = true
			continue
		}
		byName[names[id]] = append(byName[names[id]], l)
	}
	return byName, cjk
}

func TestStdEncoding_Labels(t *testing.T) {
	encModels(t)
	byName, cjk := encLabelTable(t)
	var cases []encCase
	look := func(label, want string) {
		cases = append(cases, encCase{in: "look " + encHex([]byte(label)), want: want})
	}
	ws := []string{" ", "\t", "\n", "\f", "\r"}
	n := 0
	for name, labels := range byName {
		for _, l := range labels {
			n++
			want := "ok " + name
			look(l, want)
			look(strings.ToUpper(l), want)
			for _, w := range ws {
				look(w+l, want)
				look(l+w, want)
				look(w+l+w, want)
			}
			// api-invalid-label: other whitespace is part of the label.
			for _, w := range []string{"\x00", "\x0b", " ", " ", " "} {
				look(w+l, "err UnknownLabel")
				look(l+w, "err UnknownLabel")
			}
		}
	}
	for l := range cjk {
		n++
		look(l, "err Unsupported")
		look(" "+strings.ToUpper(l)+"\t", "err Unsupported")
	}
	if n != 228 {
		// encodings.json at the pinned commit has 228 labels; a new pin
		// that changes the count is a change to read.
		t.Errorf("%d labels in encodings.json", n)
	}
	// unsupported-labels.window.js: labels the standard does not have.
	src, err := os.ReadFile(filepath.Join(encEvidence(), "wpt-encoding", "unsupported-labels.window.js"))
	if err != nil {
		t.Fatal(err)
	}
	quoted := regexp.MustCompile(`(?m)^\s*"([^"]+)",?$`).FindAllStringSubmatch(string(src), -1)
	if len(quoted) < 50 {
		t.Fatalf("read %d unsupported labels", len(quoted))
	}
	for _, q := range quoted {
		look(q[1], "err UnknownLabel")
	}
	for _, l := range []string{"", " ", "utf-8x", "utf8 ", "Koi8-r", strings.Repeat("a", 100), "invalid-invalidLabel", "UTF-8\x00"} {
		want := "err UnknownLabel"
		if l == "utf8 " {
			want = "ok UTF-8"
		}
		look(l, want)
	}
	encRunAll(t, cases)
}

func TestStdEncoding_SingleByteIndexRoundTrip(t *testing.T) {
	models := encModels(t)
	var cases []encCase
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for _, m := range models {
		if m.index == nil && m.name != "x-user-defined" {
			continue
		}
		cases = append(cases, decCase(m, all))
		// Every byte alone, strictly: its index entry, or BadInput at 0.
		for i := 0; i < 256; i++ {
			cases = append(cases, decsCase(m, []byte{byte(i)}))
		}
		// Every code point the index maps encodes to its byte; the whole
		// decoded range encodes back to the bytes it came from.
		var mapped strings.Builder
		var back []byte
		for i := 0; i < 256; i++ {
			s, bad := whatwgDecode(m, []byte{byte(i)})
			if bad < 0 {
				mapped.WriteString(s)
				back = append(back, byte(i))
			}
		}
		c := encTextCase(m, mapped.String())
		if c.want != "ok "+encHex(back) {
			t.Fatalf("%s: the model's index does not round-trip", m.name)
		}
		cases = append(cases, c)
		// Scalars the encoding has no byte for.
		for _, s := range []string{"€Āx", "a\U0001F600", "ab�", "éあ"} {
			cases = append(cases, encTextCase(m, s))
		}
	}
	encRunAll(t, cases)
}

func encUnhexList(t *testing.T, xs ...string) [][]byte {
	var out [][]byte
	for _, x := range xs {
		out = append(out, cryptoUnhex(t, x))
	}
	return out
}

// TestStdEncoding_WptCases holds the decoders to web-platform-tests'
// encoding cases for UTF-8 and UTF-16, each case also checked against the
// model so the model is held to them too.
func TestStdEncoding_WptCases(t *testing.T) {
	models := encModels(t)
	byName := map[string]encModel{}
	for _, m := range models {
		byName[m.name] = m
	}
	u8, le, be := byName["UTF-8"], byName["UTF-16LE"], byName["UTF-16BE"]
	var cases []encCase
	// textdecoder-fatal: every input is malformed.
	fatal := encUnhexList(t, "ff", "c0", "e0", "c000", "c0c0", "e000", "e0c0", "e08000", "e080c0", "fc8080808080", "fe8080808080",
		"c080", "e08080", "f0808080", "f880808080", "fc8080808080", "c1bf", "e081bf", "f08081bf", "f8808081bf", "fc80808081bf",
		"e09fbf", "f0809fbf", "f880809fbf", "fc8080809fbf", "f08fbfbf", "f8808fbfbf", "fc80808fbfbf", "f8848fbfbf", "fc80848fbfbf",
		"eda080", "edb080", "eda080edb080")
	for _, b := range fatal {
		if _, bad := whatwgDecode(u8, b); bad < 0 {
			t.Fatalf("the model accepts %x", b)
		}
		cases = append(cases, decsCase(u8, b), decCase(u8, b))
	}
	if _, bad := whatwgDecode(le, []byte{0}); bad != 0 {
		t.Fatal("the model accepts a truncated UTF-16 unit")
	}
	cases = append(cases, decsCase(le, []byte{0}))
	// textdecoder-utf16-surrogates.
	sur := [][2]string{{"00d8", "�"}, {"00dc", "�"}, {"00d80000", "�\u0000"}, {"00dc0000", "�\u0000"}, {"00dc00d8", "��"}}
	for _, c := range sur {
		b := cryptoUnhex(t, c[0])
		if s, _ := whatwgDecode(le, b); s != c[1] {
			t.Fatalf("model: %x decodes to %q", b, s)
		}
		cases = append(cases, decCase(le, b), decsCase(le, b))
	}
	// textdecoder-byte-order-marks: a matching mark is removed, another is
	// text.
	text := "z¢水\U0001d11e\U0010fffd"
	boms := map[string][]byte{"UTF-8": {0xEF, 0xBB, 0xBF}, "UTF-16LE": {0xFF, 0xFE}, "UTF-16BE": {0xFE, 0xFF}}
	for _, m := range []encModel{u8, le, be} {
		body, _ := whatwgEncode(m, text)
		for name, bom := range boms {
			b := append(append([]byte(nil), bom...), body...)
			s, _ := whatwgDecode(m, b)
			if (s == text) != (name == m.name) {
				t.Fatalf("model: %s with the mark of %s gives %q", m.name, name, s)
			}
			cases = append(cases, decCase(m, b), pushCase(m, b, []int{1, 1}))
		}
		cases = append(cases, decCase(m, body))
	}
	// textdecoder-eof.
	eof := [][2]string{{"f0", "�"}, {"f09f", "�"}, {"f09f92", "�"}, {"f09f41", "�A"}, {"f04142", "�AB"},
		{"f041f0", "�A�"}, {"f08f92", "���"}}
	for _, c := range eof {
		b := cryptoUnhex(t, c[0])
		if s, _ := whatwgDecode(u8, b); s != c[1] {
			t.Fatalf("model: %x decodes to %q", b, s)
		}
		for cut := 0; cut <= len(b); cut++ {
			cases = append(cases, pushCase(u8, b, []int{cut}))
		}
		cases = append(cases, decCase(u8, b))
	}
	// api-replacement-encodings: any input is one U+FFFD.
	rep := byName["replacement"]
	for _, b := range [][]byte{{}, {0x41}, {0x41, 0x42, 0x80}} {
		cases = append(cases, decCase(rep, b), decsCase(rep, b), pushCase(rep, b, []int{1}))
	}
	cases = append(cases, encTextCase(rep, "a"))
	encRunAll(t, cases)
}

// encFuzzBytes is a byte string biased to the units that make decoders
// branch: lead and continuation bytes, surrogate halves, BOMs.
func encFuzzBytes(r *encRng, n int) []byte {
	pool := []byte{0x41, 0x00, 0x7F, 0x80, 0x8F, 0x90, 0x9F, 0xA0, 0xBF, 0xC0, 0xC1, 0xC2, 0xDF, 0xE0, 0xED, 0xEF, 0xF0, 0xF4, 0xF5, 0xFF,
		0xD8, 0xDB, 0xDC, 0xDF, 0xFE, 0xBB}
	b := make([]byte, n)
	for i := range b {
		if r.intn(4) == 0 {
			b[i] = byte(r.next())
		} else {
			b[i] = pool[r.intn(len(pool))]
		}
	}
	return b
}

func TestStdEncoding_DecodeAdversarial(t *testing.T) {
	models := encModels(t)
	r := &encRng{s: 11}
	var cases []encCase
	for _, m := range models {
		count := 120
		if m.index != nil {
			count = 15
		}
		for k := 0; k < count; k++ {
			b := encFuzzBytes(r, r.intn(40))
			cases = append(cases, decCase(m, b), decsCase(m, b))
			var cuts []int
			for left := len(b); left > 0 && r.intn(5) != 0; {
				c := r.intn(min(left, 6) + 1)
				cuts = append(cuts, c)
				left -= c
			}
			cases = append(cases, pushCase(m, b, cuts))
		}
		// Long valid text, through the whole-buffer paths, split anywhere.
		text := strings.Repeat("plain ascii text, ", 5) + "café € \U0001F600 水"
		body, bad := whatwgEncode(m, text)
		if bad == -1 {
			cases = append(cases, decCase(m, body), decsCase(m, body))
			for _, cut := range []int{1, 2, 3, 31, 32, 33, len(body) - 1} {
				if cut < len(body) {
					cases = append(cases, pushCase(m, body, []int{cut}))
				}
			}
		}
	}
	// UTF-16 of up to 100 units mixing every class the vector step tells
	// apart (ASCII, two- and three-byte units, pairs, lone halves), so
	// blocks of eight units with and without surrogates follow each other.
	for _, m := range models {
		if m.name != "UTF-16LE" && m.name != "UTF-16BE" {
			continue
		}
		for k := 0; k < 300; k++ {
			var units []int
			for j := r.intn(100); j > 0; j-- {
				switch r.intn(8) {
				case 0, 1, 2:
					units = append(units, 0x20+r.intn(0x5F))
				case 3:
					units = append(units, 0x80+r.intn(0x780))
				case 4:
					units = append(units, 0x800+r.intn(0xD000))
				case 5:
					units = append(units, 0xD800+r.intn(0x400), 0xDC00+r.intn(0x400))
				case 6:
					units = append(units, 0xD800+r.intn(0x800))
				default:
					units = append(units, 0xE000+r.intn(0x2000))
				}
			}
			var b []byte
			for _, u := range units {
				if m.name == "UTF-16BE" {
					b = append(b, byte(u>>8), byte(u))
				} else {
					b = append(b, byte(u), byte(u>>8))
				}
			}
			if r.intn(4) == 0 {
				b = append(b, 0x41)
			}
			cases = append(cases, decCase(m, b), decsCase(m, b), pushCase(m, b, []int{r.intn(len(b) + 1), 1}))
		}
	}
	// Encode: valid text of every kind.
	for _, m := range models {
		for k := 0; k < 30; k++ {
			var sb strings.Builder
			for j := r.intn(12); j > 0; j-- {
				switch r.intn(4) {
				case 0:
					sb.WriteByte(byte(0x20 + r.intn(0x5F)))
				case 1:
					sb.WriteRune(rune(0x80 + r.intn(0x780)))
				case 2:
					c := rune(0x800 + r.intn(0xF800))
					if c >= 0xD800 && c <= 0xDFFF {
						c = 0xFFFD
					}
					sb.WriteRune(c)
				default:
					sb.WriteRune(rune(0x10000 + r.intn(0x100000)))
				}
			}
			if !utf8.ValidString(sb.String()) {
				t.Fatal("bad generator")
			}
			cases = append(cases, encTextCase(m, sb.String()))
		}
	}
	encRunAll(t, cases)
}
