// strscan, Go, idiomatic std: bytes.LastIndexByte (variant lastindexbyte;
// a backwards byte loop in package bytes) and, for utf16len, a range over
// the string's runes adding unicode/utf16.RuneLen (Go's std has no counter
// of its own), over the text of README.md, reps calls, call r on the text
// from offset r mod 64. The checksum adds each answer and the offset. Go's
// std has no all-ASCII test and no WTF-8 validator (skipped, bench.json).
// Usage: strscan <variant> <size> <reps> <seed>.
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"unicode/utf16"
)

func mix(z uint64) uint64 {
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

func fill(buf []byte, ascii, wtf bool, st uint64) {
	n := len(buf)
	i := 0
	for ; i < n && i < 128; i++ {
		buf[i] = 'a' + byte(i%26)
	}
	for i < n {
		st += 0x9E3779B97F4A7C15
		z := mix(st)
		p := z % 8
		if ascii {
			p = 0
		}
		switch {
		case p < 5 || i+4 > n:
			buf[i] = 'a' + byte((z>>8)%26)
			i++
		case p == 5:
			buf[i], buf[i+1] = 0xC3, 0xA9
			i += 2
		case p == 6:
			buf[i], buf[i+1], buf[i+2] = 0xE2, 0x82, 0xAC
			i += 3
		case wtf && (z>>16)%4 == 0:
			buf[i], buf[i+1], buf[i+2] = 0xED, 0xA0, 0x80
			i += 3
		default:
			buf[i], buf[i+1], buf[i+2], buf[i+3] = 0xF0, 0x9F, 0x98, 0x80
			i += 4
		}
	}
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: strscan <variant> <size> <reps> <seed>")
		os.Exit(2)
	}
	name := os.Args[1]
	n, _ := strconv.Atoi(os.Args[2])
	reps, _ := strconv.Atoi(os.Args[3])
	seed, _ := strconv.ParseUint(os.Args[4], 10, 64)
	buf := make([]byte, n)
	fill(buf, name == "lastindexbyte", false, seed)
	acc := 0
	switch name {
	case "lastindexbyte":
		buf[100] = 0xFF
		for r := 0; r < reps; r++ {
			off := r % 64
			acc += bytes.LastIndexByte(buf[off:], 0xFF) + off
		}
	case "utf16len":
		s := string(buf)
		for r := 0; r < reps; r++ {
			off := r % 64
			units := 0
			for _, c := range s[off:] {
				units += utf16.RuneLen(c)
			}
			acc += units + off
		}
	default:
		fmt.Fprintln(os.Stderr, "strscan: no std form in Go for", name)
		os.Exit(2)
	}
	fmt.Println(acc)
}
