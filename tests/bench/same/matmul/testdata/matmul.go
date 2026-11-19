// matmul, Go: the reference algorithm of ../README.md.
// Three flat []float64 of n * n, row-major, the i-k-j loop indexed
// c[i*n+j] (bounds checked), repeated `rounds` times into the same c. The
// product is written float64(aik*b[k*n+j]) so it is never fused. int
// indices (they wrap). Built with GOAMD64=v2 -pgo=off.
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
		fmt.Fprintln(os.Stderr, "usage: matmul <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	a := make([]float64, n*n)
	b := make([]float64, n*n)
	c := make([]float64, n*n)
	for i := 0; i < n*n; i++ {
		st += 0x9e3779b97f4a7c15
		a[i] = float64(mix(st)&255) / 256.0
		st += 0x9e3779b97f4a7c15
		b[i] = float64(mix(st)&255) / 256.0
	}
	for r := 0; r < rounds; r++ {
		for i := 0; i < n; i++ {
			for k := 0; k < n; k++ {
				aik := a[i*n+k]
				for j := 0; j < n; j++ {
					c[i*n+j] = c[i*n+j] + float64(aik*b[k*n+j])
				}
			}
		}
	}
	total := 0.0
	for i := 0; i < n*n; i++ {
		total = total + c[i]
	}
	fmt.Println(int64(total * 65536.0))
}
