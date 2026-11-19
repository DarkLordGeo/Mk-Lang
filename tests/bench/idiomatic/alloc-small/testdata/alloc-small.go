// alloc-small, Go, idiomatic std: the task of README.md with the runtime's
// allocator, garbage collected (GOGC=100, labelled): each object is
// bytes.Clone(src[off:off+l]) (one allocation and the copy), kept in a ring
// of 4,096 slices; each step reads the oldest and puts a new one in its
// slot, and the collector frees the old one. At the end every live object
// is read. Usage: alloc-small <steps> <seed>.
package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
)

const live = 4096

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func make1(src []byte, st *uint64) []byte {
	*st += 0x9e3779b97f4a7c15
	v := mix(*st)
	l := int(v%249) + 8
	off := int((v >> 16) % 3840)
	return bytes.Clone(src[off : off+l])
}

func read(o []byte) uint64 {
	n := len(o)
	return uint64(n) + uint64(o[0]) + uint64(o[n-1])
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: alloc-small <steps> <seed>")
		os.Exit(2)
	}
	steps, _ := strconv.Atoi(os.Args[1])
	st, _ := strconv.ParseUint(os.Args[2], 10, 64)
	var buf [4096]byte
	gs := st ^ 0x5555555555555555
	for i := range buf {
		gs += 0x9e3779b97f4a7c15
		buf[i] = byte(mix(gs)%26) + 'a'
	}
	ring := make([][]byte, live)
	for i := range ring {
		ring[i] = make1(buf[:], &st)
	}
	var total uint64
	for j := range steps {
		slot := j % live
		total += read(ring[slot])
		ring[slot] = make1(buf[:], &st)
	}
	for _, o := range ring {
		total += read(o)
	}
	fmt.Println(total)
}
