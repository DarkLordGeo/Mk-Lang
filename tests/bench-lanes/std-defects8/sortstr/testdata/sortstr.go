// sortstr, Go, idiomatic std: the task of ../README.md with slices.Sort
// (pdqsort) over a []string of 16-byte substrings of one string. Built with
// GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
)

const hex = "0123456789abcdef"

func mix(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func unhex(s string) uint64 {
	var v uint64
	for i := 0; i < len(s); i++ {
		c := s[i]
		d := c - '0'
		if c > '9' {
			d = c - 'a' + 10
		}
		v = v<<4 | uint64(d)
	}
	return v
}

func fold(xs []string) uint64 {
	n := len(xs)
	sum := unhex(xs[n-1])
	for i := 0; i < n; i += 1024 {
		sum += unhex(xs[i]) * uint64(i)
	}
	return sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sortstr <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.ParseUint(os.Args[2], 10, 64)
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	buf := make([]byte, n*16)
	xs := make([]string, n)
	var total uint64
	for r := uint64(0); r < rounds; r++ {
		st := seed + r*0x5555555555555555
		for i := 0; i < n; i++ {
			st += 0x9e3779b97f4a7c15
			z := mix(st)
			for j := 0; j < 16; j++ {
				buf[i*16+j] = hex[(z>>(60-4*j))&15]
			}
		}
		s := string(buf)
		for i := range xs {
			xs[i] = s[i*16 : i*16+16]
		}
		slices.Sort(xs)
		total += fold(xs)
	}
	fmt.Println(total)
}
