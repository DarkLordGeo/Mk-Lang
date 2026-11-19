// chunks, Go, idiomatic std: the task of ../README.md with slices.Chunk
// (Go 1.23), whose pieces alias the slice, for both passes. Built with
// GOAMD64=v2 -pgo=off.
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

func round(xs []uint64, k int) uint64 {
	var j uint64
	for c := range slices.Chunk(xs, k) {
		for i := range c {
			c[i] = c[i]*3 + j
		}
		j++
	}
	var sum uint64
	for c := range slices.Chunk(xs, k) {
		sum += c[0] ^ c[len(c)-1]
		for _, x := range c {
			sum += x
		}
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: chunks <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]uint64, n)
	st := seed
	for i := range xs {
		st += 0x9e3779b97f4a7c15
		xs[i] = mix(st)
	}
	var total uint64
	for r := 0; r < rounds; r++ {
		k := (r%16)*4 + 1
		total += round(xs, k)
	}
	fmt.Println(total)
}
