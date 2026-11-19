// crypto-ed25519-sign, Go: crypto/ed25519.Sign over the input of
// mk/crypto-ed25519-sign.mk's comment, the key from NewKeyFromSeed. Built
// with GOAMD64=v2 -pgo=off.
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
		fmt.Fprintln(os.Stderr, "usage: crypto-ed25519-sign <n> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	st, _ := strconv.ParseUint(os.Args[2], 10, 64)
	seed := make([]byte, 32)
	fill(&st, seed)
	k := ed25519.NewKeyFromSeed(seed)
	msgs := make([]byte, 64*n)
	fill(&st, msgs)
	var sum uint64
	for i := 0; i < n; i++ {
		sig := ed25519.Sign(k, msgs[64*i:64*i+64])
		sum ^= binary.LittleEndian.Uint64(sig[0:8]) ^ binary.LittleEndian.Uint64(sig[32:40])
	}
	fmt.Println(sum)
}
