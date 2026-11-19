// crypto-p256-ecdh, Go: the standard library's P-256 ECDH (crypto/ecdh),
// over the input of mk/crypto-p256-ecdh.mk's comment. The private key is
// made from its bytes inside the loop, as Mk's EcSecret.FromBytes is.
// Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto/ecdh"
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
		fmt.Fprintln(os.Stderr, "usage: crypto-p256-ecdh <keys> <reps> <seed>")
		os.Exit(2)
	}
	keys, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	secrets := make([]byte, 32*keys)
	fill(&st, secrets)
	privs := make([]*ecdh.PrivateKey, keys)
	pubs := make([]*ecdh.PublicKey, keys)
	for i := range keys {
		secrets[32*i] &= 0x7f
		k, err := ecdh.P256().NewPrivateKey(secrets[32*i : 32*i+32])
		if err != nil {
			panic(err)
		}
		privs[i] = k
		pubs[i] = k.PublicKey()
	}
	var sum uint64
	for r := 0; r < reps; r++ {
		for i := range keys {
			// The key is made once above: NewPrivateKey computes the public
			// point, a second multiplication Mk's FromBytes does not make.
			s, err := privs[i].ECDH(pubs[(i+1)%keys])
			if err != nil {
				panic(err)
			}
			sum = sum*31 + binary.LittleEndian.Uint64(s)
		}
	}
	fmt.Println(sum)
}
