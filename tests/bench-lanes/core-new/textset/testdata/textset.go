// textset, Go, idiomatic std: the task of ../README.md with one []byte
// per round, overwritten by append(b[:0], s...), which keeps the capacity
// (strings.Builder.Reset drops its buffer, so Go code reuses a byte slice).
// Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func round(src string, n int, seed uint64) uint64 {
	var t []byte
	var sum uint64
	st := seed
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		v := mix(st)
		k := int(v % 300)
		off := int((v >> 16) % 3796)
		t = append(t[:0], src[off:off+k]...)
		sum += uint64(len(t))
		if len(t) > 0 {
			sum += uint64(t[0])*7 + uint64(t[len(t)-1])
		}
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: textset <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.ParseUint(os.Args[2], 10, 64)
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	var buf [4096]byte
	st := seed ^ 0x5555555555555555
	for i := range buf {
		st += 0x9e3779b97f4a7c15
		buf[i] = byte(mix(st)%26) + 'a'
	}
	src := string(buf[:])
	var total uint64
	for r := uint64(0); r < rounds; r++ {
		total += round(src, n, seed+r)
	}
	fmt.Println(total)
}
