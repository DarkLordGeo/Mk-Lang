// encoding-base64mime, Go, idiomatic std: encoding/base64.StdEncoding,
// whose Decode skips CR and LF. n bytes of splitmix64 output (seed 1) are
// encoded once and broken into lines of cols characters, each ending in
// CRLF (cols 0: one line, no break); rounds times the text is decoded by
// Decode into one buffer made before the loop. The checksum adds, per
// round, the decoded length and one decoded byte at a moving index.
// Usage: encoding-base64mime <n> <rounds> <cols>.
package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: encoding-base64mime <n> <rounds> <cols>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	cols, _ := strconv.Atoi(os.Args[3])
	data := make([]byte, n)
	st := uint64(1)
	for i := 0; i < n; {
		st += 0x9E3779B97F4A7C15
		z := st
		z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
		z = (z ^ z>>27) * 0x94D049BB133111EB
		z ^= z >> 31
		for k := 0; k < 8 && i < n; k++ {
			data[i] = byte(z >> (8 * k))
			i++
		}
	}
	enc := base64.StdEncoding
	plain := enc.EncodeToString(data)
	var text []byte
	if cols == 0 {
		text = []byte(plain)
	} else {
		for at := 0; at < len(plain); at += cols {
			text = append(text, plain[at:min(at+cols, len(plain))]...)
			text = append(text, '\r', '\n')
		}
	}
	out := make([]byte, enc.DecodedLen(len(text)))
	var total uint64
	for r := 0; r < rounds; r++ {
		m, err := enc.Decode(out, text)
		if err != nil {
			panic(err)
		}
		total += uint64(m) + uint64(out[(r*7919)%n])
	}
	fmt.Println(total)
}
