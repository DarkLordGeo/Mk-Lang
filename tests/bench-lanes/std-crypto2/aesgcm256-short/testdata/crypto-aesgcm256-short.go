// crypto-aesgcm256-short, Go: the standard library's AES-256-GCM (crypto/aes,
// crypto/cipher.NewGCM, Seal then Open, one AEAD for the run) over the
// input and records of mk/crypto-aesgcm256-short.mk's comment. Built with
// GOAMD64=v2 -pgo=off. Go's amd64 GCM uses AES-NI and PCLMULQDQ when the
// CPU has them (run-time dispatch in the library).
package main

import (
	"crypto/aes"
	"crypto/cipher"
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
		fmt.Fprintln(os.Stderr, "usage: crypto-aesgcm256-short <mib> <reps> <seed>")
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
	c, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(c)
	sealed := make([]byte, 0, 64+16)
	back := make([]byte, 0, 64)
	recs := n / 64
	nonce := make([]byte, 12)
	aad := make([]byte, 13)
	var sum uint64
	for r := 0; r < reps; r++ {
		for j := 0; j < recs; j++ {
			binary.LittleEndian.PutUint64(nonce, uint64(r*recs+j))
			binary.LittleEndian.PutUint64(aad, uint64(j))
			s := g.Seal(sealed[:0], nonce, buf[64*j:64*j+64], aad)
			sum ^= binary.LittleEndian.Uint64(s[64:])
			p, err := g.Open(back[:0], nonce, s, aad)
			if err != nil {
				panic(err)
			}
			sum ^= uint64(p[0])
		}
	}
	fmt.Println(sum)
}
