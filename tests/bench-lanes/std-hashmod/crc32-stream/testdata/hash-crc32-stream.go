// hash-crc32-stream, Go: the standard library's CRC-32/ISO-HDLC
// (hash/crc32.Update with crc32.IEEETable) fed in 1,500-byte pieces, the
// input and passes of mk/hash-crc32-stream.mk's comment. Built with
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

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: hash-crc32-stream <mib> <reps> <seed>")
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
		var c uint32
		for j := 0; j < n; {
			e := min(j+1500, n)
			c = crc32.Update(c, crc32.IEEETable, buf[j:e])
			j = e
		}
		h := uint64(c)
		sum ^= h
		buf[r%n] ^= byte(h)
	}
	fmt.Println(sum)
}
