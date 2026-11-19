// hash-crc32, Go: the standard library's CRC-32/ISO-HDLC (hash/crc32) over the input of
// mk/hash-crc32.mk's comment, with the same write-back. Built with
// GOAMD64=v2 -pgo=off. Go's amd64 code uses the CPU's extensions when
// present (labelled in the row's notes).
package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func lo32(d [32]byte) uint64 { return binary.LittleEndian.Uint64(d[:8]) }

func lo64(d [64]byte) uint64 { return binary.LittleEndian.Uint64(d[:8]) }

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: hash-crc32 <mib> <reps> <seed>")
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
		h := uint64(crc32.ChecksumIEEE(buf))
		sum ^= h
		buf[r%n] ^= byte(h)
	}
	fmt.Println(sum)
}
