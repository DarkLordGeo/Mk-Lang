// validutf8, Go, SIMD table: the kernel of README.md with unicode/utf8.Valid,
// labelled scalar: Go's validator takes ASCII eight bytes at a time and
// every other byte through its first-byte table, and Go has no SIMD layer a
// program can write vector code in. The buffer starts on a 64-byte
// boundary, as in every column (BS-4; the address is read through reflect,
// as the contract bans import "unsafe"). Usage: validutf8 <size> <reps>
// <seed>.
package main

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"unicode/utf8"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// enc holds the 16 encodings of README.md; entry k is 1 + k/4 bytes long.
var enc = [16][4]byte{
	{0x61}, {0x20}, {0x30}, {0x7E},
	{0xC2, 0x80}, {0xDF, 0xBF}, {0xC3, 0xA9}, {0xD0, 0x96},
	{0xE0, 0xA0, 0x80}, {0xED, 0x9F, 0xBF}, {0xEE, 0x80, 0x80}, {0xE4, 0xB8, 0x96},
	{0xF0, 0x90, 0x80, 0x80}, {0xF4, 0x8F, 0xBF, 0xBF}, {0xF0, 0x9F, 0x98, 0x80}, {0xF3, 0xA0, 0x80, 0x80},
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: validutf8 <size> <reps> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	blen := min(n, 65536)
	blk := make([]byte, blen)
	p := 0
	for {
		st += 0x9e3779b97f4a7c15
		k := int(mix(st) & 15)
		l := 1 + k/4
		if p+l > blen {
			break
		}
		copy(blk[p:p+l], enc[k][:l])
		p += l
	}
	for ; p < blen; p++ {
		blk[p] = 0x61
	}
	// BS-4: the buffer starts on a 64-byte boundary in every column.
	raw := make([]byte, n+64)
	off := int(-reflect.ValueOf(raw).Pointer() & 63)
	buf := raw[off : off+n]
	if reflect.ValueOf(buf).Pointer()%64 != 0 {
		fmt.Fprintln(os.Stderr, "validutf8: buffer not 64-byte aligned")
		os.Exit(3)
	}
	i := 0
	for ; i+blen <= n; i += blen {
		copy(buf[i:i+blen], blk)
	}
	for ; i < n; i++ {
		buf[i] = 0x61
	}
	span := uint64(min(n, 64))
	var total uint64
	for range reps {
		st += 0x9e3779b97f4a7c15
		z := mix(st)
		pos := n - 1 - int((z>>1)%span)
		old := buf[pos]
		if z&1 == 1 {
			buf[pos] = byte(z >> 56)
		}
		var ok uint64
		if utf8.Valid(buf) {
			ok = 1
		}
		total = total*31 + ok
		buf[pos] = old
	}
	fmt.Println(total)
}
