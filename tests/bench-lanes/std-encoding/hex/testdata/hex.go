// encoding-hex, Go, idiomatic std: encoding/hex. n bytes of splitmix64
// output (seed 1), rounds times: Encode into one buffer and Decode into
// another, both made before the loop. The checksum adds, per round, the
// text's length, the decoded length and one decoded byte at a moving index.
// Usage: encoding-hex <n> <rounds>.
package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: encoding-hex <n> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
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
	text := make([]byte, 2*n)
	out := make([]byte, n)
	var total uint64
	for r := 0; r < rounds; r++ {
		hex.Encode(text, data)
		m, err := hex.Decode(out, text)
		if err != nil {
			panic(err)
		}
		total += uint64(len(text)) + uint64(m) + uint64(out[(r*7919)%n])
	}
	fmt.Println(total)
}
