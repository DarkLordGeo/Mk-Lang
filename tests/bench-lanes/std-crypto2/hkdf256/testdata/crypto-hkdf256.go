// crypto-hkdf256, Go: the standard library's HKDF-SHA256 (crypto/hkdf
// Extract then Expand) over the input of mk/crypto-hkdf256.mk's comment.
// Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto/hkdf"
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
	mib, reps, st := args("crypto-hkdf256")
	n := mib * 1048576
	buf := make([]byte, n)
	fill(&st, buf)
	salt := make([]byte, 32)
	fill(&st, salt)
	const info = "tls13 key iv"
	var sum uint64
	for r := 0; r < reps; r++ {
		for j := 0; j < n/32; j++ {
			prk, err := hkdf.Extract(sha256.New, buf[32*j:32*j+32], salt)
			if err != nil {
				panic(err)
			}
			okm, err := hkdf.Expand(sha256.New, prk, info, 42)
			if err != nil {
				panic(err)
			}
			sum ^= binary.LittleEndian.Uint64(okm)
			salt[0] ^= okm[0]
		}
	}
	fmt.Println(sum)
}
