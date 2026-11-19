// indexbyte, Go, SIMD: the kernel of README.md with bytes.IndexByte, which
// is assembly-backed in Go's runtime (internal/bytealg, SSE2 and AVX2 with
// its own run-time check, whatever GOAMD64 says), labelled as such: Go has
// no SIMD layer a program can write vector code in. The buffer starts on a
// 64-byte boundary, as in every column (BS-4; the address is read through
// reflect, as the contract bans import "unsafe"). Usage: indexbyte <size>
// <reps> <seed>.
package main

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: indexbyte <size> <reps> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	// BS-4: the buffer starts on a 64-byte boundary in every column.
	raw := make([]byte, n+64)
	off := int(-reflect.ValueOf(raw).Pointer() & 63)
	buf := raw[off : off+n]
	if reflect.ValueOf(buf).Pointer()%64 != 0 {
		fmt.Fprintln(os.Stderr, "indexbyte: buffer not 64-byte aligned")
		os.Exit(3)
	}
	for i := range buf {
		buf[i] = byte((uint64(i)*131 + st) & 0x7F)
	}
	span := uint64(min(n, 64))
	var total int64
	for range reps {
		st += 0x9e3779b97f4a7c15
		pos := n - 1 - int(mix(st)%span)
		old := buf[pos]
		buf[pos] = 0xFF
		total += int64(bytes.IndexByte(buf, 0xFF))
		buf[pos] = old
	}
	fmt.Println(total)
}
