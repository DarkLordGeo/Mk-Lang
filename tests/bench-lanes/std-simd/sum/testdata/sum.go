// sum, Go, SIMD: the kernel of README.md written scalar, labelled so: Go has
// no SIMD layer a program can write vector code in, and its std has no
// float sum. The 16 partial sums are a [16]float32, in the same order as
// the vector columns. The buffer starts on a 64-byte boundary, as in every
// column (BS-4; the address is read through reflect, as the contract bans
// import "unsafe"). Usage: sum <n> <reps> <seed>.
package main

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
)

func sum(x []float32) float32 {
	n := len(x)
	var s [16]float32
	i := 0
	for ; i+16 <= n; i += 16 {
		xs := x[i : i+16]
		for j := range 16 {
			s[j] = s[j] + xs[j]
		}
	}
	for w := 8; w >= 1; w /= 2 {
		for j := range w {
			s[j] = s[j] + s[j+w]
		}
	}
	r := s[0]
	for ; i < n; i++ {
		r = r + x[i]
	}
	return r
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sum <n> <reps> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	reps, _ := strconv.Atoi(os.Args[2])
	seed, _ := strconv.ParseUint(os.Args[3], 10, 64)
	// BS-4: the buffer starts on a 64-byte boundary in every column.
	raw := make([]float32, n+16)
	off := int(-reflect.ValueOf(raw).Pointer()&63) / 4
	x := raw[off : off+n]
	if reflect.ValueOf(x).Pointer()%64 != 0 {
		fmt.Fprintln(os.Stderr, "sum: buffer not 64-byte aligned")
		os.Exit(3)
	}
	for i := range x {
		u := uint64(i)
		x[i] = float32((u*0x9e3779b97f4a7c15+seed)>>48)*(1.0/65536.0) - 0.5
	}
	var total uint64
	for r := range reps {
		x[r%n] += 1.0
		total += uint64(math.Float32bits(sum(x)))
	}
	fmt.Println(total)
}
