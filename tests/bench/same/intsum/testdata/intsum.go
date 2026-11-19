// intsum, Go: the reference algorithm of ../README.md.
// A []int64 indexed with xs[i] (bounds checked; gc removes the check where it
// proves i < len). The sum's + wraps (Go's integers wrap). The fill's
// splitmix64 wraps by design. Built with GOAMD64=v2 -pgo=off.
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
		fmt.Fprintln(os.Stderr, "usage: intsum <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]int64, n)
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		xs[i] = int64(mix(st) & 1023)
	}
	var total int64
	for r := 0; r < rounds; r++ {
		var s int64
		for i := 0; i < n; i++ {
			s += xs[i]
		}
		total += s
		xs[r%n] = s & 1023
	}
	fmt.Println(total)
}
