// text-utf16, Go: unicode/utf16, the idiomatic conversions
// utf16.Encode([]rune(s)) (encode) and string(utf16.Decode(u)) (decode),
// over the input and passes of mk/text-utf16.mk's comment (README.md). Go
// has no direct UTF-8 to UTF-16 function, so the []rune conversion is part
// of the idiom, both ways. utf16.Decode never fails: it replaces an
// unpaired surrogate with U+FFFD. Usage: text-utf16 <encode|decode> <mib>
// <reps> <seed>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

func mix(x uint64) uint64 {
	z := x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func gen(n int, seed uint64) string {
	var b strings.Builder
	st := seed
	for b.Len() < n {
		st += 0x9e3779b97f4a7c15
		x := mix(st)
		k := x % 16
		y := x >> 8
		var c rune
		switch {
		case k < 10:
			c = 0x61 + rune(y%26)
			if x%7 == 0 {
				c = 0x41 + rune(y%26)
			}
		case k == 10:
			c = 0x20
		case k == 11:
			c = 0xC0 + rune(y%64)
		case k == 12:
			c = 0x430 + rune(y%32)
		case k == 13:
			c = 0x4E00 + rune(y%0x5000)
		case k == 14:
			c = 0x1F600 + rune(y%80)
		default:
			c = 0x300 + rune(y%0x70)
		}
		b.WriteRune(c)
	}
	return b.String()
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: text-utf16 <encode|decode> <mib> <reps> <seed>")
		os.Exit(1)
	}
	mode := os.Args[1]
	mib, _ := strconv.Atoi(os.Args[2])
	reps, _ := strconv.Atoi(os.Args[3])
	seed, _ := strconv.ParseUint(os.Args[4], 10, 64)
	s := gen(mib*1048576, seed)
	var sum uint64
	switch mode {
	case "encode":
		for r := 0; r < reps; r++ {
			u := utf16.Encode([]rune(s))
			sum += uint64(len(u)) + uint64(u[r%len(u)])
		}
	case "decode":
		u := utf16.Encode([]rune(s))
		for r := 0; r < reps; r++ {
			back := string(utf16.Decode(u))
			sum += uint64(len(back)) + uint64(back[r%len(back)])
		}
	default:
		fmt.Fprintln(os.Stderr, "mode is encode or decode")
		os.Exit(1)
	}
	fmt.Println(sum)
}
