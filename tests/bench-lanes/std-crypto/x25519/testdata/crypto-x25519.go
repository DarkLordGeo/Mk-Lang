// crypto-x25519, Go: one X25519 key exchange per step as
// mk/crypto-x25519.mk's comment says, through crypto/ecdh as the standard
// library is called: NewPrivateKey (which runs the ladder on the base point
// to compute the public key: crypto/ecdh/x25519.go), PublicKey().Bytes(),
// NewPublicKey and ECDH (the ladder on u). Two ladders per step, as Mk and
// Zig; crypto/ecdh has no single-ladder form (CRY-1). Built with
// GOAMD64=v2 -pgo=off.
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

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: crypto-x25519 <n> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	st, _ := strconv.ParseUint(os.Args[2], 10, 64)
	k := make([]byte, 32)
	for w := 0; w < 4; w++ {
		st += 0x9e3779b97f4a7c15
		binary.LittleEndian.PutUint64(k[8*w:], mix(st))
	}
	u := make([]byte, 32)
	u[0] = 9
	c := ecdh.X25519()
	var acc uint64
	for i := 0; i < n; i++ {
		priv, err := c.NewPrivateKey(k)
		if err != nil {
			panic(err)
		}
		acc ^= binary.LittleEndian.Uint64(priv.PublicKey().Bytes()[:8])
		pub, err := c.NewPublicKey(u)
		if err != nil {
			panic(err)
		}
		r, err := priv.ECDH(pub)
		if err != nil {
			panic(err)
		}
		u, k = k, r
	}
	fmt.Println(binary.LittleEndian.Uint64(k[:8]) ^ acc)
}
