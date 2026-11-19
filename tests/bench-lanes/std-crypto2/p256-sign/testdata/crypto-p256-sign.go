// crypto-p256-sign, Go: the standard library's ECDSA P-256 signing with
// RFC 6979 nonces (ecdsa.PrivateKey.Sign with a nil random source and
// crypto.SHA256), whose DER signature is used as it is, over the input of
// mk/crypto-p256-sign.mk's comment. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	_ "crypto/sha256"
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
		fmt.Fprintln(os.Stderr, "usage: crypto-p256-sign <keys> <reps> <seed>")
		os.Exit(2)
	}
	keys, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	secrets := make([]byte, 32*keys)
	fill(&st, secrets)
	privs := make([]*ecdsa.PrivateKey, keys)
	for i := range keys {
		secrets[32*i] &= 0x7f
		k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), secrets[32*i:32*i+32])
		if err != nil {
			panic(err)
		}
		privs[i] = k
	}
	digests := make([]byte, 32*keys)
	fill(&st, digests)
	var sum uint64
	for r := 0; r < reps; r++ {
		for i := range keys {
			der, err := privs[i].Sign(nil, digests[32*i:32*i+32], crypto.SHA256)
			if err != nil {
				panic(err)
			}
			n := len(der)
			sum ^= binary.LittleEndian.Uint64(der[0:8]) ^ binary.LittleEndian.Uint64(der[n-8:n])
			digests[32*i] ^= der[n-1]
		}
	}
	fmt.Println(sum)
}
