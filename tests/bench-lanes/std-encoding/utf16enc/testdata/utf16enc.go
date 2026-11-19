// encoding-utf16enc, Go, idiomatic std: the line of README.md repeated n
// times, encoded to UTF-16 rounds times with unicode/utf16.AppendRune over
// the string's runes into one []uint16 reused each round (Go's std has no
// UTF-16 byte encoder; byte i of the little-endian form is read from unit
// i / 2). The checksum adds, per round, the encoding's length in bytes and
// one byte of it at a moving index.
// Usage: encoding-utf16enc <n> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

const line = "Hello, wörld! Привет мир. 水は冷たい \U0001F600\n"

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: encoding-utf16enc <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	s := strings.Repeat(line, n)
	var out []uint16
	var total uint64
	for r := 0; r < rounds; r++ {
		out = out[:0]
		for _, c := range s {
			out = utf16.AppendRune(out, c)
		}
		m := 2 * len(out)
		k := (r * 7919) % m
		total += uint64(m) + uint64(byte(out[k/2]>>(8*(k&1))))
	}
	fmt.Println(total)
}
