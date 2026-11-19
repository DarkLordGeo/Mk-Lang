// crypto-hmac384, Go: the standard library's HMAC-SHA384 (crypto/hmac.New
// with crypto/sha512.New384, Write, Sum, a new HMAC per message as a
// one-shot call is written) over the input of mk/crypto-hmac384.mk's
// comment. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto/hmac"
	"crypto/sha512"
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

func args(name string) (int, int, uint64) {
	if len(os.Args) != 4 {
		fmt.Fprintf(os.Stderr, "usage: %s <mib> <reps> <seed>\n", name)
		os.Exit(2)
	}
	mib, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	return mib, reps, st
}
func main() {
	mib, reps, st := args("crypto-hmac384")
	n := mib * 1048576
	buf := make([]byte, n)
	fill(&st, buf)
	key := make([]byte, 48)
	fill(&st, key)
	var sum uint64
	var tag [48]byte
	for r := 0; r < reps; r++ {
		for j := 0; j < n/64; j++ {
			m := hmac.New(sha512.New384, key)
			m.Write(buf[64*j : 64*j+64])
			t := m.Sum(tag[:0])
			sum ^= binary.LittleEndian.Uint64(t)
			key[0] ^= t[0]
		}
	}
	fmt.Println(sum)
}
