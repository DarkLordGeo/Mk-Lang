// crypto-p256-verify, Go: the standard library's ECDSA P-256 verification
// (ecdsa.VerifyASN1 of the DER signature), over the input of
// mk/crypto-p256-verify.mk's comment; the signatures are made by
// ecdsa.PrivateKey.Sign with a nil random source (RFC 6979), the same DER
// bytes Mk signs. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	_ "crypto/sha256"
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
		fmt.Fprintln(os.Stderr, "usage: crypto-p256-verify <keys> <reps> <seed>")
		os.Exit(2)
	}
	keys, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	secrets := make([]byte, 32*keys)
	fill(&st, secrets)
	digests := make([]byte, 32*keys)
	fill(&st, digests)
	pubs := make([]*ecdsa.PublicKey, keys)
	sigs := make([][]byte, keys)
	for i := range keys {
		secrets[32*i] &= 0x7f
		k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), secrets[32*i:32*i+32])
		if err != nil {
			panic(err)
		}
		pubs[i] = &k.PublicKey
		der, err := k.Sign(nil, digests[32*i:32*i+32], crypto.SHA256)
		if err != nil {
			panic(err)
		}
		sigs[i] = der
	}
	var sum uint64
	for r := 0; r < reps; r++ {
		for i := range keys {
			digests[32*i] ^= byte(r & 1)
			if ecdsa.VerifyASN1(pubs[i], digests[32*i:32*i+32], sigs[i]) {
				sum++
			}
			digests[32*i] ^= byte(r & 1)
		}
	}
	fmt.Println(sum)
}
