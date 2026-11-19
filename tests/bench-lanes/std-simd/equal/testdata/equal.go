// equal, Go, SIMD: the kernel of README.md with bytes.Equal, which is
// assembly-backed in Go's runtime (internal/bytealg memequal, SSE2 and AVX2
// with its own run-time check, whatever GOAMD64 says), labelled as such: Go
// has no SIMD layer a program can write vector code in. Both buffers start
// on a 64-byte boundary (BS-4; the address is read through reflect, as the
// contract bans import "unsafe"). Usage: equal <size> <reps> <seed>.
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

func aligned(n int) []byte {
	raw := make([]byte, n+64)
	off := int(-reflect.ValueOf(raw).Pointer() & 63)
	buf := raw[off : off+n]
	if reflect.ValueOf(buf).Pointer()%64 != 0 {
		fmt.Fprintln(os.Stderr, "equal: buffer not 64-byte aligned")
		os.Exit(3)
	}
	return buf
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: equal <size> <reps> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	// BS-4: both buffers start on a 64-byte boundary in every column.
	a, b := aligned(n), aligned(n)
	for i := range a {
		a[i] = byte((uint64(i)*0x9e3779b97f4a7c15 + st) >> 56)
		b[i] = a[i]
	}
	span := uint64(64)
	if n < 64 {
		span = uint64(n)
	}
	var total uint64
	for range reps {
		st += 0x9e3779b97f4a7c15
		m := mix(st)
		pos := n - 1 - int(m%span)
		d := byte((m >> 32) & 1)
		b[pos] ^= d
		var e uint64
		if bytes.Equal(a, b) {
			e = 1
		}
		total = total*31 + e
		b[pos] ^= d
	}
	fmt.Println(total)
}
