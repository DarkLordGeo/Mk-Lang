// sort, Go, idiomatic std: the task of ../README.md with slices.Sort
// (pdqsort). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
)

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func fold(xs []uint64) uint64 {
	n := len(xs)
	sum := xs[n-1]
	for i := 0; i < n; i += 1024 {
		sum += xs[i] * uint64(i)
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sort <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.ParseUint(os.Args[2], 10, 64)
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]uint64, n)
	var total uint64
	for r := uint64(0); r < rounds; r++ {
		st := seed + r*0x5555555555555555
		for i := range xs {
			st += 0x9e3779b97f4a7c15
			xs[i] = mix(st)
		}
		slices.Sort(xs)
		total += fold(xs)
	}
	fmt.Println(total)
}
