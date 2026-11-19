// crypto-ed25519-keygen, Go: crypto/ed25519.NewKeyFromSeed over the input
// of mk/crypto-ed25519-keygen.mk's comment. Built with GOAMD64=v2
// -pgo=off.
package main

import (
	"crypto/ed25519"
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
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: crypto-ed25519-keygen <n> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	st, _ := strconv.ParseUint(os.Args[2], 10, 64)
	seeds := make([]byte, 32*n)
	fill(&st, seeds)
	var sum uint64
	for i := 0; i < n; i++ {
		k := ed25519.NewKeyFromSeed(seeds[32*i : 32*i+32])
		sum ^= binary.LittleEndian.Uint64(k[32:40]) ^ binary.LittleEndian.Uint64(k[56:64])
	}
	fmt.Println(sum)
}
