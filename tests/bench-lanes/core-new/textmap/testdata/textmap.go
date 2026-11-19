// textmap, Go, idiomatic std: the task of ../README.md with a
// map[string]uint64 (Go's Swiss-table map, its runtime hash seeded per
// map). m[string(b)] = v is the idiomatic write; the runtime copies the
// key when it inserts. Built with GOAMD64=v2 -pgo=off.
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

func digits(buf *[20]byte, x uint64) int {
	i := len(buf) - 1
	v := x
	buf[i] = byte(v%10) + '0'
	v /= 10
	for v > 0 {
		i--
		buf[i] = byte(v%10) + '0'
		v /= 10
	}
	return i
}

func round(n int, seed uint64) uint64 {
	m := map[string]uint64{}
	u := uint64(n/4 + 1)
	var buf [20]byte
	st := seed
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		p := digits(&buf, mix(st)%u)
		m[string(buf[p:])] = uint64(i)
	}
	var sum uint64
	st = seed ^ 0x5555555555555555
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		p := digits(&buf, mix(st)%u)
		if old, ok := m[string(buf[p:])]; ok {
			sum += old
		} else {
			sum++
		}
		m[string(buf[p:])] = uint64(i)
	}
	return sum + uint64(len(m))
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: textmap <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	var total uint64
	for r := 0; r < rounds; r++ {
		total += round(n, seed+uint64(r))
	}
	fmt.Println(total)
}
