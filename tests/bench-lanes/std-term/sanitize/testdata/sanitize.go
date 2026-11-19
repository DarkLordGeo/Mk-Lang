// The sanitize row (README.md): the same loop in Go.
package main

import (
	"fmt"
	"os"
	"strconv"
)

const g = 0x9e3779b97f4a7c15

func mix(x uint64) uint64 {
	z := x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// gen writes n items into b (3 * n bytes, zeroed) and returns the length:
// indexed stores, as every column does.
func gen(b []byte, n int) int {
	st := uint64(1)
	o := 0
	for i := 0; i < n; i++ {
		st += g
		z := mix(st)
		k := z % 64
		v := byte((z >> 8) % 95)
		w := byte((z >> 8) % 32)
		switch {
		case k < 48:
			b[o] = 0x20 + v
			o++
		case k < 52:
			b[o] = 0x0a
			o++
		case k < 56:
			b[o], b[o+1] = 0xc3, 0xa9
			o += 2
		case k < 58:
			b[o] = 0x1b
			o++
		case k < 60:
			b[o] = w
			o++
		case k < 62:
			b[o], b[o+1] = 0xc2, 0x80+w
			o += 2
		default:
			b[o], b[o+1], b[o+2] = 0xe2, 0x82, 0xac
			o += 3
		}
	}
	return o
}

// sanitize writes s to out with C0 controls but LF and tab, DEL and C1
// controls replaced by U+FFFD, each safe run copied whole, and returns the
// length: a fixed buffer written at an index, as sanitize.c's loop.
func sanitize(out, s []byte) int {
	o, run := 0, 0
	for i := 0; i < len(s); {
		c := s[i]
		bad := 0
		if c < 0x20 && c != 0x0a && c != 0x09 || c == 0x7f {
			bad = 1
		} else if c == 0xc2 && i+1 < len(s) && s[i+1] >= 0x80 && s[i+1] <= 0x9f {
			bad = 2
		}
		if bad == 0 {
			i++
			continue
		}
		o += copy(out[o:], s[run:i])
		out[o], out[o+1], out[o+2] = 0xef, 0xbf, 0xbd
		o += 3
		i += bad
		run = i
	}
	return o + copy(out[o:], s[run:])
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	b := make([]byte, 3*n)
	text := b[:gen(b, n)]
	out := make([]byte, 9*n)
	var total uint64
	for r := 0; r < rounds; r++ {
		m := sanitize(out, text)
		total += uint64(m)
		total += uint64(out[(r*7919)%m])
	}
	fmt.Println(total)
}
