// mapiter, Go, idiomatic std: the task of README.md with map[uint64]uint64
// (the runtime's swiss table), filled once, then iterated <passes> times
// with `for k, v := range m` (a randomised start each pass); the sum is
// order-free. Usage: mapiter <n> <passes> <seed>.
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

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: mapiter <n> <passes> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	passes, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	m := map[uint64]uint64{}
	for i := range n {
		st += 0x9e3779b97f4a7c15
		m[mix(st)] = uint64(i)
	}
	var total uint64
	for range passes {
		for k, v := range m {
			total += k * (v + 1)
		}
		total += uint64(len(m))
	}
	fmt.Println(total)
}
