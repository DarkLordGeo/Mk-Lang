// sortstable, Go, idiomatic std: the task of ../README.md with
// slices.SortStableFunc (insertion sort of blocks, then SymMerge, in
// place). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strconv"
)

type rec struct {
	key uint32
	id  uint32
}

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func fold(xs []rec) uint64 {
	n := len(xs)
	sum := uint64(xs[n-1].key)*1000003 + uint64(xs[n-1].id)
	for i := 0; i < n; i += 1024 {
		sum += uint64(xs[i].id) * uint64(i)
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sortstable <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.ParseUint(os.Args[2], 10, 64)
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]rec, n)
	var total uint64
	for r := uint64(0); r < rounds; r++ {
		st := seed + r*0x5555555555555555
		for i := range xs {
			st += 0x9e3779b97f4a7c15
			xs[i] = rec{key: uint32(mix(st) % 1000), id: uint32(i)}
		}
		slices.SortStableFunc(xs, func(p, q rec) int { return cmp.Compare(p.key, q.key) })
		total += fold(xs)
	}
	fmt.Println(total)
}
