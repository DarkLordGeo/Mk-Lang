// d358, Go: the peer of mk/d358.mk. Modes 0 and 1 are unicode/utf8.Valid
// (Go's validator, scalar: ASCII eight bytes at a time), modes 2 and 3
// bytes.IndexByte (assembly, SSE2/AVX2 on amd64). The buffer starts on a
// 64-byte boundary (BS-4; the address is read through reflect, as the
// contract bans import "unsafe"). Usage: d358 <size> <reps> <seed> <mode>.
package main

import (
	"bytes"
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

var enc = [16][4]byte{
	{0x61}, {0x20}, {0x30}, {0x7E},
	{0xC2, 0x80}, {0xDF, 0xBF}, {0xC3, 0xA9}, {0xD0, 0x96},
	{0xE0, 0xA0, 0x80}, {0xED, 0x9F, 0xBF}, {0xEE, 0x80, 0x80}, {0xE4, 0xB8, 0x96},
	{0xF0, 0x90, 0x80, 0x80}, {0xF4, 0x8F, 0xBF, 0xBF}, {0xF0, 0x9F, 0x98, 0x80}, {0xF3, 0xA0, 0x80, 0x80},
}

func tailByte(k int) byte {
	switch {
	case k == 63:
		return 0x7B
	case k >= 15:
		return byte(0x22 + k)
	}
	return byte(0x21 + k)
}

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: d358 <size> <reps> <seed> <mode>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	mode, _ := strconv.Atoi(os.Args[4])
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
	raw := make([]byte, n+64)
	off := int(-reflect.ValueOf(raw).Pointer() & 63)
	buf := raw[off : off+n]
	i := 0
	for ; i+blen <= n; i += blen {
		copy(buf[i:i+blen], blk)
	}
	for ; i < n; i++ {
		buf[i] = 0x61
	}
	var total uint64
	if mode < 2 {
		for range reps {
			st += 0x9e3779b97f4a7c15
			z := mix(st)
			pos := n - 1 - int((z>>1)%64)
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
	} else {
		j := n - 64
		for j > 0 && buf[j]&0xC0 == 0x80 {
			j--
		}
		if buf[n-64]&0xC0 == 0x80 {
			for q := j; q < n-64; q++ {
				buf[q] = 0x61
			}
		}
		for k := range 64 {
			buf[n-64+k] = tailByte(k)
		}
		if !utf8.Valid(buf) {
			fmt.Fprintln(os.Stderr, "d358: tail broke the text")
			os.Exit(3)
		}
		for range reps {
			st += 0x9e3779b97f4a7c15
			k := int(mix(st) % 64)
			c := buf[n-64+k]
			got := 0
			if f := bytes.IndexByte(buf, c); f >= 0 {
				got = f + 1
			}
			total = total*31 + uint64(got)
		}
	}
	fmt.Println(total)
}
