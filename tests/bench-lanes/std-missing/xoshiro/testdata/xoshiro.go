// xoshiro, Go: xoshiro256** seeded with four SplitMix64 outputs and
// Lemire's bounded draw with bits.Mul64, the same steps as every other
// column of README.md. uint64 arithmetic wraps. Built with GOAMD64=v2
// -pgo=off.
package main

import (
	"fmt"
	"math/bits"
	"os"
	"strconv"
)

type x256 struct{ s0, s1, s2, s3 uint64 }

func splitmix(x *uint64) uint64 {
	*x += 0x9E3779B97F4A7C15
	z := *x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (g *x256) next() uint64 {
	result := bits.RotateLeft64(g.s1*5, 7) * 9
	t := g.s1 << 17
	g.s2 ^= g.s0
	g.s3 ^= g.s1
	g.s1 ^= g.s2
	g.s0 ^= g.s3
	g.s2 ^= t
	g.s3 = bits.RotateLeft64(g.s3, 45)
	return result
}

func (g *x256) below(n uint64) uint64 {
	hi, lo := bits.Mul64(g.next(), n)
	if lo < n {
		t := -n % n
		for lo < t {
			hi, lo = bits.Mul64(g.next(), n)
		}
	}
	return hi
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: xoshiro <n> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[1], 10, 64)
	x, _ := strconv.ParseUint(os.Args[2], 10, 64)
	var g x256
	g.s0 = splitmix(&x)
	g.s1 = splitmix(&x)
	g.s2 = splitmix(&x)
	g.s3 = splitmix(&x)
	var total uint64
	for i := int64(0); i < n; i++ {
		total += g.next()
		total += g.below(1000)
	}
	fmt.Println(total)
}
