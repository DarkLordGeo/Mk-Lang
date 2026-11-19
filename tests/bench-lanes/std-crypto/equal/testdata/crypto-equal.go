// crypto-equal, Go: crypto/subtle.ConstantTimeCompare over the input of
// mk/crypto-equal.mk's comment. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"crypto/subtle"
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
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: crypto-equal <mib> <reps> <seed>")
		os.Exit(2)
	}
	mib, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	n := mib * 1048576
	a := make([]byte, n)
	for i := 0; i < n; i += 8 {
		st += 0x9e3779b97f4a7c15
		binary.LittleEndian.PutUint64(a[i:], mix(st))
	}
	b := append([]byte(nil), a...)
	var count uint64
	for r := 0; r < reps; r++ {
		j := (r * 7919) % n
		if r%2 == 1 {
			b[j] ^= 1
		}
		if subtle.ConstantTimeCompare(a, b) == 1 {
			count++
		}
		if r%2 == 1 {
			b[j] ^= 1
		}
	}
	fmt.Println(count*1000003 ^ uint64(reps))
}
