// sortstable, Go, idiomatic std: the task of README.md with
// slices.SortStableFunc and cmp.Compare (insertion sort of blocks, then
// SymMerge; Go has no stable sort without a comparator function, so each
// comparison is a call of cmp.Compare[uint64], which the compiler may
// inline). Usage: sortstable <n> <rounds> <seed>.
package main

import (
	"cmp"
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

func check(xs []uint64) uint64 {
	var sum uint64
	for i := range xs {
		if i > 0 && xs[i-1] > xs[i] {
			return 0xdead
		}
		sum += xs[i] * uint64(i%1000+1)
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sortstable <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]uint64, n)
	var total uint64
	for range rounds {
		for p := range 4 {
			for i := range xs {
				switch p {
				case 0:
					st += 0x9e3779b97f4a7c15
					xs[i] = mix(st)
				case 1:
					xs[i] = uint64(n - i)
				case 2:
					xs[i] = uint64(i % 1000)
				default:
					st += 0x9e3779b97f4a7c15
					xs[i] = mix(st) >> 60
				}
			}
			slices.SortStableFunc(xs, cmp.Compare[uint64])
			total += check(xs)
		}
	}
	fmt.Println(total)
}
