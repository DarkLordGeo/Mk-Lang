// Why: FromUtf16's vector write pass once wrote wrong bytes around surrogate
// pairs; this keeps it equal to Go's decoder.
// What: TestSTDTEXT_FromUtf16VectorWrite.
// How: Utf16ToUtf8Vec (std/text/utf.mk) over mixed text and a surrogate pair
// at every lane, against unicode/utf16. It needs no file of the UAX 29 unit.

package std

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

// text3U16Mixed is the mixed text of tests/bench-lanes/std-text2/utf16
// (README.md), at least n bytes, seed 1.
func text3U16Mixed(n int) string {
	var b strings.Builder
	st := uint64(1)
	for b.Len() < n {
		st += 0x9e3779b97f4a7c15
		z := st
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		x := z ^ (z >> 31)
		k, y := x%16, x>>8
		var c uint32
		switch {
		case k < 10:
			c = 0x61 + uint32(y%26)
			if x%7 == 0 {
				c = 0x41 + uint32(y%26)
			}
		case k == 10:
			c = 0x20
		case k == 11:
			c = 0xC0 + uint32(y%64)
		case k == 12:
			c = 0x430 + uint32(y%32)
		case k == 13:
			c = 0x4E00 + uint32(y%0x5000)
		case k == 14:
			c = 0x1F600 + uint32(y%80)
		default:
			c = 0x300 + uint32(y%0x70)
		}
		b.WriteRune(rune(c))
	}
	return b.String()
}

// TestSTDTEXT_FromUtf16VectorWrite checks FromUtf16 against Go's
// unicode/utf16 on what reaches its vector write pass: mixed text with
// ASCII runs of 1 to 3, a surrogate pair at every lane of an eight-unit
// step, one-, two- and three-byte units at their limits, output ending at
// the buffer's last byte, and unpaired surrogates (an error, before
// anything is written).
func TestSTDTEXT_FromUtf16VectorWrite(t *testing.T) {
	r := rand.New(rand.NewPCG(33, 1))
	classes := [][2]rune{{0x20, 0x7F}, {0x80, 0x7FF}, {0x800, 0xD7FF}, {0xE000, 0xFFFF}, {0x10000, 0x10FFFF}}
	edge := []rune{0, 0x7F, 0x80, 0x7FF, 0x800, 0xD7FF, 0xE000, 0xFFFF, 0x10000, 0x10FFFF}
	pick := func(k int) rune {
		c := classes[k]
		if r.IntN(4) == 0 {
			return edge[r.IntN(len(edge))]
		}
		return c[0] + rune(r.IntN(int(c[1]-c[0]+1)))
	}
	var units [][]uint16
	// Mixed: ASCII runs of 1 to 3 between other scalars.
	for i := 0; i < 1500; i++ {
		var s []rune
		n := 1 + r.IntN(120)
		for len(s) < n {
			for k := 1 + r.IntN(3); k > 0; k-- {
				s = append(s, pick(0))
			}
			for k := 1 + r.IntN(2); k > 0; k-- {
				s = append(s, pick(1+r.IntN(4)))
			}
		}
		units = append(units, utf16.Encode(s))
	}
	// A pair at every lane: lead units of each class, then the pair, then
	// enough units to keep the vector loop running.
	for lane := 0; lane < 16; lane++ {
		for k := 0; k < 4; k++ {
			var s []rune
			for j := 0; j < lane; j++ {
				s = append(s, pick(k))
			}
			s = append(s, pick(4))
			for j := 0; j < 30; j++ {
				s = append(s, pick(r.IntN(4)))
			}
			units = append(units, utf16.Encode(s))
		}
	}
	// Every length from 0 to 64 of each one-class text, so the last
	// vector store lands at each distance from the buffer's end.
	for n := 0; n <= 64; n++ {
		for k := 0; k < 5; k++ {
			s := make([]rune, n)
			for j := range s {
				s[j] = pick(k)
			}
			units = append(units, utf16.Encode(s))
		}
	}
	// The bench row's own text (text-utf16-decode), cut at several sizes.
	for _, n := range []int{24, 25, 100, 1000, 4096} {
		units = append(units, utf16.Encode([]rune(text3U16Mixed(n))))
	}
	// Unpaired surrogates anywhere in long valid text.
	for i := 0; i < 300; i++ {
		u := slices.Clone(units[r.IntN(1500)])
		at := r.IntN(len(u) + 1)
		switch r.IntN(3) {
		case 0:
			u = slices.Insert(u, at, uint16(0xD800+r.IntN(0x400)))
		case 1:
			u = slices.Insert(u, at, uint16(0xDC00+r.IntN(0x400)))
		default:
			u = append(u, 0xD800+uint16(r.IntN(0x400)))
		}
		units = append(units, u)
	}
	lines := make([]string, len(units))
	for i, u := range units {
		var b strings.Builder
		for j, x := range u {
			if j > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%x", x)
		}
		lines[i] = b.String()
	}
	text2Builds2(t, func(t *testing.T, release bool) {
		got := text2Run(t, release, "s", lines)
		bad, errs := 0, 0
		for i, u := range units {
			want := "err"
			if valid16(u) {
				want = hexLine(utf16.Decode(u))
			} else {
				errs++
			}
			if got[i] != want {
				bad++
				if bad <= 5 {
					t.Errorf("FromUtf16 %s: got %s, want %s", lines[i], got[i], want)
				}
			}
		}
		if errs < 250 {
			t.Errorf("only %d inputs with an unpaired surrogate", errs)
		}
		t.Logf("%d inputs, %d unpaired, %d wrong", len(units), errs, bad)
	})
}
