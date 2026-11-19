// sortstr, Go, idiomatic std: the task of README.md with slices.Sort
// (pdqsort, unstable; string's < compares bytes) over substrings of one
// string, the generated buffer converted once. Usage: sortstr <n> <rounds>
// <seed>.
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

func fnv(w string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(w); i++ {
		h = (h ^ uint64(w[i])) * 0x100000001b3
	}
	return h
}

func check(xs []string) uint64 {
	var sum uint64
	for i := range xs {
		if i > 0 && xs[i-1] > xs[i] {
			return 0xdead
		}
		sum += fnv(xs[i]) * uint64(i%1000+1)
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sortstr <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	buf := make([]byte, 0, n*16)
	offs := make([]int, 0, n+1)
	for range n {
		offs = append(offs, len(buf))
		st += 0x9e3779b97f4a7c15
		l := 1 + int(mix(st)%16)
		for range l {
			st += 0x9e3779b97f4a7c15
			buf = append(buf, byte(0x61+mix(st)%26))
		}
	}
	offs = append(offs, len(buf))
	text := string(buf)
	orig := make([]string, n)
	for i := range orig {
		orig[i] = text[offs[i]:offs[i+1]]
	}
	work := make([]string, n)
	var total uint64
	for range rounds {
		copy(work, orig)
		slices.Sort(work)
		total += check(work)
	}
	fmt.Println(total)
}
