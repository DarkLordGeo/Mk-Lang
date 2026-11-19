// sha256-4k, Go: the standard library's SHA-256 (crypto/sha256) over
// 4096-byte messages, the input and passes of mk/sha256-4k.mk's comment.
// Built with GOAMD64=v2 -pgo=off. Go's amd64 code uses the SHA extensions
// when the CPU has them, whatever GOAMD64 says (labelled in the row's notes).
package main

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
)

const msg = 4096

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sha256-4k <kib> <reps> <seed>")
		os.Exit(2)
	}
	kib, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	n := kib * 1024
	buf := make([]byte, n)
	for i := 0; i < n; i += 8 {
		st += 0x9e3779b97f4a7c15
		binary.LittleEndian.PutUint64(buf[i:], mix(st))
	}
	var sum uint64
	for r := 0; r < reps; r++ {
		for j := 0; j+msg <= n; j += msg {
			d := sha256.Sum256(buf[j : j+msg])
			sum ^= binary.LittleEndian.Uint64(d[:8])
		}
		buf[r%n] ^= byte(sum)
	}
	fmt.Println(sum)
}
