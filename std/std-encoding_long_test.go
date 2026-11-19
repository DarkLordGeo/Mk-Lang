// Why: std.encoding's block paths only start on long inputs, which short tests never reach.
// What: long inputs for UTF-8 block validation (utf8.mk, 64 to 4,096 bytes), the single-byte table
// path (single.mk), the verbatim text copy (text.mk) and 16-byte UTF-16 encoding (utf16.mk).
// How: every answer is the WHATWG model's, as in TestStdEncoding_DecodeAdversarial.

package std

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// encMixed is valid text of n bytes or a little more, mixing ASCII runs
// with two-, three- and four-byte scalars.
func encMixed(r *encRng, n int) []byte {
	var b []byte
	for len(b) < n {
		switch r.intn(6) {
		case 0:
			b = utf8.AppendRune(b, rune(0x80+r.intn(0x780)))
		case 1:
			c := rune(0x800 + r.intn(0xF800))
			if c >= 0xD800 && c <= 0xDFFF {
				c = 0x4E00
			}
			b = utf8.AppendRune(b, c)
		case 2:
			b = utf8.AppendRune(b, rune(0x10000+r.intn(0x100000)))
		default:
			for k := r.intn(40); k > 0; k-- {
				b = append(b, byte(0x20+r.intn(0x5F)))
			}
		}
	}
	return b
}

// encBreak puts one malformed unit into b at about offset p: a lone
// 0xFF, a stray continuation byte, a sequence cut short, a surrogate, an
// overlong form, a scalar above U+10FFFF, or a lead at the very end.
func encBreak(r *encRng, b []byte, p int) []byte {
	p = min(max(p, 0), len(b))
	var bad []byte
	switch r.intn(7) {
	case 0:
		bad = []byte{0xFF}
	case 1:
		bad = []byte{0x80}
	case 2:
		bad = []byte{0xE6, 0xB0}
	case 3:
		bad = []byte{0xED, 0xA0, 0x80}
	case 4:
		bad = []byte{0xC0, 0x80}
	case 5:
		bad = []byte{0xF4, 0x90, 0x80, 0x80}
	default:
		return append(append([]byte{}, b...), 0xF0, 0x9F)
	}
	out := append([]byte{}, b[:p]...)
	out = append(out, bad...)
	return append(out, b[p:]...)
}

// encCuts is a random split of n bytes into chunks of up to most bytes.
func encCuts(r *encRng, n, most int) []int {
	var cuts []int
	for left := n; left > 0; {
		c := 1 + r.intn(min(left, most))
		cuts = append(cuts, c)
		left -= c
	}
	return cuts
}

func TestStdEncoding_LongInputs(t *testing.T) {
	models := encModels(t)
	r := &encRng{s: 23}
	var cases []encCase
	byName := map[string]encModel{}
	for _, m := range models {
		byName[m.name] = m
	}
	u8 := byName["UTF-8"]

	// UTF-8: valid text across the block sizes, then one to three
	// malformed units near block cuts (64, 128, 256 and the doubling
	// blocks after them) and anywhere.
	for _, n := range []int{60, 63, 64, 65, 66, 67, 100, 127, 128, 129, 200, 300, 1000, 5000, 9000, 20000} {
		for k := 0; k < 4; k++ {
			b := encMixed(r, n)
			cases = append(cases, decCase(u8, b), decsCase(u8, b), pushCase(u8, b, encCuts(r, len(b), 700)))
			nb := 1 + r.intn(3)
			for j := 0; j < nb; j++ {
				spots := []int{61 + r.intn(6), 125 + r.intn(6), 189 + r.intn(6), 445 + r.intn(6), r.intn(len(b) + 1)}
				b = encBreak(r, b, spots[r.intn(len(spots))])
			}
			cases = append(cases, decCase(u8, b), decsCase(u8, b), pushCase(u8, b, encCuts(r, len(b), 300)))
		}
	}
	// Mostly valid text, a malformed unit about every 997 bytes, as the
	// encoding-utf8 bench row has it.
	{
		b := encMixed(r, 30000)
		for p := 996; p < len(b); p += 997 {
			b = encBreak(r, b, p)
		}
		cases = append(cases, decCase(u8, b), decsCase(u8, b), pushCase(u8, b, encCuts(r, len(b), 5000)))
	}
	// Dense errors, where every block is refused: all 0xFF, all
	// continuation bytes, and windows-1252 text read as UTF-8.
	{
		latin := []byte(strings.Repeat("Caf\xe9 cr\xe8me br\xfbl\xe9e, na\xefve \x93quoted\x94 \x96 \x80 5, \xa9 2026\n", 60))
		for _, b := range [][]byte{[]byte(strings.Repeat("\xff", 3000)), []byte(strings.Repeat("\x80", 777)), latin} {
			cases = append(cases, decCase(u8, b), decsCase(u8, b), pushCase(u8, b, encCuts(r, len(b), 400)))
		}
	}

	// Single-byte: the table path from 64 bytes, with high bytes at every
	// position of an 80-byte ASCII line, random mixes, and bytes the index
	// leaves out.
	for _, m := range models {
		if m.index == nil {
			continue
		}
		var holes []byte
		for k, c := range m.index {
			if c == 0 {
				holes = append(holes, byte(0x80+k))
			}
		}
		line := []byte(strings.Repeat("plain ascii text ", 5))[:80]
		for p := 0; p < 80; p += 3 {
			b := append([]byte{}, line...)
			b[p] = byte(0x80 + r.intn(0x80))
			cases = append(cases, decCase(m, b))
			if p%12 == 0 {
				cases = append(cases, decsCase(m, b), pushCase(m, b, encCuts(r, len(b), 70)))
			}
		}
		for k := 0; k < 6; k++ {
			n := 60 + r.intn(600)
			b := make([]byte, n)
			high := []int{0, 50, 4, 1}[k%4]
			for i := range b {
				if high > 0 && r.intn(high) == 0 {
					b[i] = byte(0x80 + r.intn(0x80))
				} else {
					b[i] = byte(0x20 + r.intn(0x5F))
				}
			}
			if k >= 4 && len(holes) > 0 {
				b[r.intn(n)] = holes[r.intn(len(holes))]
			}
			cases = append(cases, decCase(m, b), decsCase(m, b), pushCase(m, b, encCuts(r, n, 200)))
		}
	}

	// UTF-16 encode: ASCII runs of every length around 16 and 32, with a
	// non-ASCII scalar after them, and long mixed text.
	for _, name := range []string{"UTF-16LE", "UTF-16BE", "UTF-8"} {
		m := byName[name]
		for run := 0; run <= 50; run++ {
			s := strings.Repeat("a", run) + "é" + strings.Repeat("b", 17) + "\U0001F600" + strings.Repeat("c", run)
			cases = append(cases, encTextCase(m, s))
		}
		for _, n := range []int{100, 1000, 5000} {
			cases = append(cases, encTextCase(m, string(encMixed(r, n))))
		}
	}
	encRunAll(t, cases)
}
