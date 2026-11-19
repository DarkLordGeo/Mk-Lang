// dot, Go, SIMD: the kernel of README.md written scalar, labelled so: Go has
// no SIMD layer a program can write vector code in, and its std has no dot
// product. The 16 partial sums are a [16]float32, in the same order as the
// vector columns; float32(x*y) keeps the compiler from fusing the product
// into the sum at GOAMD64=v3. Both buffers start on a 64-byte
// boundary, as in every column (BS-4; the address is read through reflect,
// as the contract bans import "unsafe"). Usage: dot <n> <reps> <seed>.
package main

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
)

func dot(x, y []float32) float32 {
	n := min(len(x), len(y))
	var s [16]float32
	i := 0
	for ; i+16 <= n; i += 16 {
		xs, ys := x[i:i+16], y[i:i+16]
		for j := range 16 {
			s[j] = s[j] + float32(xs[j]*ys[j])
		}
	}
	for w := 8; w >= 1; w /= 2 {
		for j := range w {
			s[j] = s[j] + s[j+w]
		}
	}
	r := s[0]
	for ; i < n; i++ {
		r = r + float32(x[i]*y[i])
	}
	return r
}

// aligned64 is n float32s starting on a 64-byte boundary.
func aligned64(n int) []float32 {
	raw := make([]float32, n+16)
	off := int(-reflect.ValueOf(raw).Pointer()&63) / 4
	return raw[off : off+n]
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: dot <n> <reps> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	// BS-4: both buffers start on a 64-byte boundary in every column.
	x, y := aligned64(n), aligned64(n)
	if reflect.ValueOf(x).Pointer()%64 != 0 || reflect.ValueOf(y).Pointer()%64 != 0 {
		fmt.Fprintln(os.Stderr, "dot: buffer not 64-byte aligned")
		os.Exit(3)
	}
	for i := range x {
		u := uint64(i)
		x[i] = float32((u*0x9e3779b97f4a7c15+seed)>>48)*(1.0/65536.0) - 0.5
		y[i] = float32((u*0xbf58476d1ce4e5b9+seed)>>48)*(1.0/65536.0) - 0.5
	}
	var total uint64
	for r := range reps {
		x[r%n] += 1.0
		total += uint64(math.Float32bits(dot(x, y)))
	}
	fmt.Println(total)
}
