// encoding-utf16, Go, idiomatic std: the UTF-16LE form of the line of
// README.md repeated n times, decoded to UTF-8 rounds times with
// unicode/utf16.DecodeRune and utf8.AppendRune into one buffer reused each
// round (Go's std has no UTF-16 byte decoder; the units are read from the
// bytes with binary.LittleEndian in the loop). The checksum adds, per round,
// the text's length and one byte of it at a moving index.
// Usage: encoding-utf16 <n> <rounds>.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const line = "Hello, w\u00f6rld! \u041f\u0440\u0438\u0432\u0435\u0442 \u043c\u0438\u0440. \u6c34\u306f\u51b7\u305f\u3044 \U0001F600\n"

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: encoding-utf16 <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	units := utf16.Encode([]rune(strings.Repeat(line, n)))
	in := make([]byte, 2*len(units))
	for i, u := range units {
		binary.LittleEndian.PutUint16(in[2*i:], u)
	}
	var out []byte
	var total uint64
	for r := 0; r < rounds; r++ {
		out = out[:0]
		for i := 0; i+1 < len(in); i += 2 {
			u := rune(binary.LittleEndian.Uint16(in[i:]))
			if utf16.IsSurrogate(u) && i+3 < len(in) {
				v := rune(binary.LittleEndian.Uint16(in[i+2:]))
				if c := utf16.DecodeRune(u, v); c != utf8.RuneError {
					out = utf8.AppendRune(out, c)
					i += 2
					continue
				}
			}
			if utf16.IsSurrogate(u) {
				u = utf8.RuneError
			}
			out = utf8.AppendRune(out, u)
		}
		total += uint64(len(out)) + uint64(out[(r*7919)%len(out)])
	}
	fmt.Println(total)
}
