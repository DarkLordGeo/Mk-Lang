// crypto-hmac256, Go: crypto/hmac over crypto/sha256, one hmac.New, Write
// and Sum per message (the standard library's one-shot form), over the input
// of mk/crypto-hmac256.mk's comment. Built with GOAMD64=v2 -pgo=off; Go's
// SHA-256 uses the SHA extensions when the CPU has them (labelled in the
// row's notes).
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func fill(st *uint64, b []byte) {
	for i := 0; i < len(b); {
		*st += 0x9e3779b97f4a7c15
		x := mix(*st)
		for k := 0; k < 8 && i < len(b); k++ {
			b[i] = byte(x >> (8 * k))
			i++
		}
	}
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: crypto-hmac256 <mib> <reps> <seed>")
		os.Exit(2)
	}
	mib, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	n := mib * 1048576
	buf := make([]byte, n)
	fill(&st, buf)
	key := make([]byte, 32)
	fill(&st, key)
	var sum uint64
	var out [32]byte
	for r := 0; r < reps; r++ {
		for i := 0; i < n; i += 64 {
			m := hmac.New(sha256.New, key)
			m.Write(buf[i : i+64])
			sum ^= binary.LittleEndian.Uint64(m.Sum(out[:0]))
		}
		key[0] ^= byte(sum)
	}
	fmt.Println(sum)
}
