// Why: utf16.mk decodes UTF-16 by vector blocks (eight units with no surrogate, written as UTF-8 by
// two 16-byte shuffles while 32 output bytes remain), and that path must equal the scalar one.
// What: every mix of one-, two- and three-byte scalars in blocks, every input length, and Decoder.Push split at every byte.
// How: the WHATWG model is the oracle, as in TestStdEncoding_DecodeAdversarial.

package std

import (
	"testing"
	"unicode/utf16"
)

// encUnits is n UTF-16 units drawn by kind: "1" ASCII, "2" U+0080 to
// U+07FF, "3" U+0800 to U+FFFF outside the surrogates, "s" a surrogate
// pair (two units), "l" a lone surrogate.
func encUnits(r *encRng, n int, kinds string) []uint16 {
	var u []uint16
	for len(u) < n {
		switch kinds[r.intn(len(kinds))] {
		case '1':
			u = append(u, uint16(r.intn(0x80)))
		case '2':
			u = append(u, uint16(0x80+r.intn(0x780)))
		case '3':
			c := 0x800 + r.intn(0xF800)
			if c >= 0xD800 && c <= 0xDFFF {
				c = 0xFFFF - (c - 0xD800)
			}
			u = append(u, uint16(c))
		case 's':
			a, b := utf16.EncodeRune(rune(0x10000 + r.intn(0x100000)))
			u = append(u, uint16(a), uint16(b))
		case 'l':
			u = append(u, uint16(0xD800+r.intn(0x800)))
		}
	}
	return u
}

// encUnitBytes is u as UTF-16LE or BE bytes.
func encUnitBytes(u []uint16, be bool) []byte {
	b := make([]byte, 0, 2*len(u))
	for _, x := range u {
		if be {
			b = append(b, byte(x>>8), byte(x))
		} else {
			b = append(b, byte(x), byte(x>>8))
		}
	}
	return b
}

func TestStdEncoding_Utf16Blocks(t *testing.T) {
	models := encModels(t)
	byName := map[string]encModel{}
	for _, m := range models {
		byName[m.name] = m
	}
	r := &encRng{s: 31}
	var cases []encCase
	for _, name := range []string{"UTF-16LE", "UTF-16BE"} {
		m := byName[name]
		be := name == "UTF-16BE"
		// Every length from 0 to 80 units, so the input ends at every
		// offset of a block and the output's last 32 bytes fall anywhere
		// in one, for all-three-byte text (the most output a block makes),
		// all-two-byte text, and mixes.
		for _, kinds := range []string{"3", "2", "123", "13", "12", "1233", "123s", "123l"} {
			for n := 0; n <= 80; n++ {
				b := encUnitBytes(encUnits(r, n, kinds), be)
				cases = append(cases, decCase(m, b), decsCase(m, b))
				if n%7 == 0 {
					cases = append(cases, pushCase(m, b, encCuts(r, len(b), 1+r.intn(40))))
				}
			}
		}
		// Every mix of the three kinds in one block: the 3^8 patterns of
		// eight units, sampled, each after an ASCII prefix of 0 to 7 units
		// so the block starts at each unit offset, with text after it.
		for k := 0; k < 600; k++ {
			pre := encUnits(r, r.intn(8), "1")
			block := encUnits(r, 8, "123")
			post := encUnits(r, r.intn(40), "123")
			u := append(append(pre, block...), post...)
			b := encUnitBytes(u, be)
			cases = append(cases, decCase(m, b))
			if k%10 == 0 {
				cases = append(cases, pushCase(m, b, encCuts(r, len(b), 1+r.intn(17))))
			}
		}
		// An odd byte at the end, and long mixed text split at every byte
		// count up to 33.
		for n := 30; n < 40; n++ {
			b := encUnitBytes(encUnits(r, n, "123"), be)
			cases = append(cases, decCase(m, append(b, 0x41)), decsCase(m, append(b, 0x41)))
		}
		long := encUnitBytes(encUnits(r, 3000, "1233s"), be)
		for c := 1; c <= 33; c++ {
			var cuts []int
			for left := len(long); left > 0; left -= c {
				cuts = append(cuts, min(c, left))
			}
			cases = append(cases, pushCase(m, long, cuts))
		}
		cases = append(cases, decCase(m, long), decsCase(m, long))
	}
	encRunAll(t, cases)
}
