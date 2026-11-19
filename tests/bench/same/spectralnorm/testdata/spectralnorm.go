// spectralnorm, Go: the reference algorithm of ../README.md (the Benchmarks
// Game's spectral-norm, scalar and single-threaded). A(i, j) computed from
// int on every use, []float64 vectors indexed v[j] (bounds checked),
// math.Sqrt, every product that meets an addition written float64(a*b) so
// it is never fused. Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

func a(i, j int) float64 {
	return 1.0 / float64((i+j)*(i+j+1)/2+i+1)
}

func mulAv(v, out []float64) {
	n := len(v)
	for i := 0; i < n; i++ {
		s := 0.0
		for j := 0; j < n; j++ {
			s = s + float64(a(i, j)*v[j])
		}
		out[i] = s
	}
}

func mulAtv(v, out []float64) {
	n := len(v)
	for i := 0; i < n; i++ {
		s := 0.0
		for j := 0; j < n; j++ {
			s = s + float64(a(j, i)*v[j])
		}
		out[i] = s
	}
}

func mulAtAv(v, out, tmp []float64) {
	mulAv(v, tmp)
	mulAtv(tmp, out)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: spectralnorm <n>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	u := make([]float64, n)
	v := make([]float64, n)
	tmp := make([]float64, n)
	for i := range u {
		u[i] = 1.0
	}
	for k := 0; k < 10; k++ {
		mulAtAv(u, v, tmp)
		mulAtAv(v, u, tmp)
	}
	vbv, vv := 0.0, 0.0
	for i := 0; i < n; i++ {
		vbv = vbv + float64(u[i]*v[i])
		vv = vv + float64(v[i]*v[i])
	}
	fmt.Println(int64(math.Sqrt(vbv/vv) * 1e9))
}
