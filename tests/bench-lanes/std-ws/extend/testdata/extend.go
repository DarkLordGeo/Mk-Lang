// ws-extend, Go: append(v, data[at:at+k]...) on a nil slice.
// n bytes of splitmix64 output (seed 1); per round r a new, empty growable
// byte list gets the n bytes appended in order, piece j of 1 + (r + j) % piece
// bytes (the last cut at n), then is freed; the checksum adds, per round,
// the byte at (r * 7919) % n and the number of pieces. Usage: ws-extend <n>
// <piece> <rounds>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: ws-extend <n> <piece> <rounds>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	piece, _ := strconv.Atoi(os.Args[2])
	rounds, _ := strconv.Atoi(os.Args[3])
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
	var total uint64
	for r := 0; r < rounds; r++ {
		var v []byte
		at, j := 0, 0
		for at < n {
			k := min(1+(r+j)%piece, n-at)
			v = append(v, data[at:at+k]...)
			at += k
			j++
		}
		total += uint64(v[(r*7919)%n]) + uint64(j)
	}
	fmt.Println(total)
}
