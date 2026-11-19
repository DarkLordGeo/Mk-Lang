// search, Go, idiomatic std: the task of README.md with slices.BinarySearch
// (generic over cmp.Ordered, so no comparator call), which returns the
// lower bound and whether the key is there. Indexing is checked. Usage:
// search <n> <lookups> <seed>.
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

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: search <n> <lookups> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	lookups, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]uint64, n)
	for i := range xs {
		xs[i] = 2*uint64(i) + 1
	}
	span := 2*uint64(n) + 1
	var total uint64
	for range lookups {
		st += 0x9e3779b97f4a7c15
		k := mix(st) % span
		if i, ok := slices.BinarySearch(xs, k); ok {
			total += uint64(i) + 1
		} else {
			total++
		}
	}
	fmt.Println(total)
}
