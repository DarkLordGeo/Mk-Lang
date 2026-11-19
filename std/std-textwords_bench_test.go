// Why: a benchmark row's checksum proves nothing unless something
// independent computes the same number.
// What: TestSTDTEXT_BenchChecksums, over tests/bench-lanes/std-text3/bounds.
// How: the row's generators rewritten in Go, the Go models' boundaries summed
// as the row sums them, over one MiB of mixed text and one of prose.

package std

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func text3Mix(x uint64) uint64 {
	z := x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// text3Mixed is the row's Mixed.
func text3Mixed(n int, seed uint64) string {
	var b strings.Builder
	st := seed
	for b.Len() < n {
		st += 0x9e3779b97f4a7c15
		x := text3Mix(st)
		k := x % 16
		y := x >> 8
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

// text3Prose is the row's Prose.
func text3Prose(n int, seed uint64) string {
	var b strings.Builder
	st := seed
	start := true
	for b.Len() < n {
		st += 0x9e3779b97f4a7c15
		x := text3Mix(st)
		if x%16 == 0 {
			b.WriteByte(byte(0x31 + x%9))
			b.WriteByte('.')
			b.WriteByte(byte(0x30 + (x>>8)%10))
			b.WriteByte(byte(0x30 + (x>>12)%10))
		} else {
			k := int((x>>4)%10) + 1
			y := x >> 8
			for i := 0; i < k; i++ {
				c := byte(0x61 + y%26)
				if i == 0 && start {
					c -= 0x20
				}
				b.WriteByte(c)
				y /= 26
			}
		}
		start = false
		switch p := (x >> 40) % 12; {
		case p == 0:
			b.WriteString(". ")
			start = true
		case p == 1 && (x>>48)%4 == 0:
			b.WriteString("? ")
			start = true
		case p == 1 && (x>>48)%4 == 1:
			b.WriteString("! ")
			start = true
		case p == 2:
			b.WriteString(", ")
		default:
			b.WriteString(" ")
		}
	}
	return b.String()
}

// TestSTDTEXT_BenchChecksums requires the row's quick checksums (mib 1,
// reps 2, seed 1) to be the models' sums.
func TestSTDTEXT_BenchChecksums(t *testing.T) {
	d := needSeg(t)
	mixed, prose := text3Mixed(1<<20, 1), text3Prose(1<<20, 1)
	for _, c := range []struct {
		mode  string
		text  string
		words bool
		want  uint64
	}{
		{"words", mixed, true, 245919271040},
		{"sentences", mixed, false, 2097156},
		{"prosewords", prose, true, 367080017414},
		{"prosesentences", prose, false, 21043388146},
	} {
		if !utf8.ValidString(c.text) {
			t.Fatalf("%s: generated text is not UTF-8", c.mode)
		}
		rs := []rune(c.text)
		var idx []int
		if c.words {
			idx = d.mWordBounds(rs)
		} else {
			idx = d.mSentenceBounds(rs)
		}
		var pass uint64
		for _, o := range byteBounds(rs, idx) {
			pass += uint64(o)
		}
		pass += uint64(len(idx))
		if got := 2 * pass; got != c.want {
			t.Errorf("%s: model sum %d, row's quick checksum %d", c.mode, got, c.want)
		}
	}
}
