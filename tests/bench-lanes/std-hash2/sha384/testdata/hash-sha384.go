// hash-sha384, Go: the standard library's SHA-384 (crypto/sha512) over the input of
// mk/hash-sha384.mk's comment, with the same write-back. Built with
// GOAMD64=v2 -pgo=off. Go's amd64 code uses the CPU's extensions when
// present (labelled in the row's notes).
package main

import (
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

func lo(d [48]byte) uint64 { return binary.LittleEndian.Uint64(d[:8]) }

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: hash-sha384 <mib> <reps> <seed>")
		os.Exit(2)
	}
	mib, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	n := mib * 1048576
	buf := make([]byte, n)
	for i := 0; i < n; i += 8 {
		st += 0x9e3779b97f4a7c15
		binary.LittleEndian.PutUint64(buf[i:], mix(st))
	}
	var sum uint64
	for r := 0; r < reps; r++ {
		h := lo(sha512.Sum384(buf))
		sum ^= h
		buf[r%n] ^= byte(h)
	}
	fmt.Println(sum)
}
