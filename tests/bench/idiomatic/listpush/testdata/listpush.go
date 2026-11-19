// listpush, Go, idiomatic std: the task of README.md with a slice grown by
// append from nil (no capacity): n single appends, one append(l, ext...) of
// n/4, a read of every element; the runtime's garbage-collected allocator
// (GOGC=100) frees it. Usage: listpush <n> <rounds> <seed>.
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

func round(n int, ext []uint64, st *uint64) uint64 {
	var l []uint64
	for range n {
		*st += 0x9e3779b97f4a7c15
		l = append(l, mix(*st))
	}
	l = append(l, ext...)
	var sum uint64
	for _, x := range l {
		sum += x
	}
	return sum + uint64(len(l))
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: listpush <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	ext := make([]uint64, n/4)
	for i := range ext {
		ext[i] = 3*uint64(i) + 1
	}
	var total uint64
	for range rounds {
		total += round(n, ext, &st)
	}
	fmt.Println(total)
}
