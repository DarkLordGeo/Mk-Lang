// encoding-utf8, Go, idiomatic std: the line of README.md repeated n times,
// with the first ASCII byte at or after every 997th offset replaced by 0xFF,
// made valid rounds times by strings.ToValidUTF8 with U+FFFD (each 0xFF is
// alone among valid scalars, so its one U+FFFD is what the WHATWG decoder
// writes). The checksum adds, per round, the text's length and one byte of
// it at a moving index.
// Usage: encoding-utf8 <n> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const line = "Hello, wörld! Привет мир. 水は冷たい \U0001F600\n"

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: encoding-utf8 <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	b := []byte(strings.Repeat(line, n))
	for p := 996; p < len(b); p += 997 {
		q := p
		for q < len(b) && b[q] >= 0x80 {
			q++
		}
		if q < len(b) {
			b[q] = 0xFF
		}
	}
	s := string(b)
	var total uint64
	for r := 0; r < rounds; r++ {
		out := strings.ToValidUTF8(s, "�")
		total += uint64(len(out)) + uint64(out[(r*7919)%len(out)])
	}
	fmt.Println(total)
}
