// indexany, Go, SIMD: the kernel of README.md with bytes.IndexAny, labelled:
// for an ASCII set and a slice longer than 8 bytes it builds a 256-bit set
// and scans byte by byte (Go has no SIMD layer, and bytes.IndexAny has no
// assembly). The haystack is 7-bit and the set ASCII, so its rune-based
// contract and the byte-set kernel agree. The buffer starts on a 64-byte
// boundary (BS-4; the address is read through reflect, as the contract bans
// import "unsafe"). Usage: indexany <size> <reps> <seed>.
package main

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strconv"
)

const set = "\t\n\r \",:="

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: indexany <size> <reps> <seed>")
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
		fmt.Fprintln(os.Stderr, "indexany: buffer not 64-byte aligned")
		os.Exit(3)
	}
	for i := range buf {
		buf[i] = byte(0x40 | ((uint64(i)*131 + st) & 0x3F))
	}
	span := uint64(64)
	if n < 64 {
		span = uint64(n)
	}
	var total int64
	for range reps {
		st += 0x9e3779b97f4a7c15
		m := mix(st)
		pos := n - 1 - int(m%span)
		old := buf[pos]
		buf[pos] = set[(m>>32)&7]
		total += int64(bytes.IndexAny(buf, set))
		buf[pos] = old
	}
	fmt.Println(total)
}
