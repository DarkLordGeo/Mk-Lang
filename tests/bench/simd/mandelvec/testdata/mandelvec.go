// mandelvec, Go, SIMD: the kernel of README.md written scalar, one point at
// a time, labelled so: Go has no SIMD layer a program can write vector code
// in. It is same/mandelbrot's Go program (float64(a*b) wherever a product
// meets a sum, so nothing is fused at GOAMD64=v3). Usage: mandelvec <n>.
package main

import (
	"fmt"
	"os"
	"strconv"
)

const maxIter = 50

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mandelvec <n>")
		os.Exit(2)
	}
	n, _ := strconv.ParseInt(os.Args[1], 10, 64)
	fn := float64(n)
	var total int64
	for y := int64(0); y < n; y++ {
		ci := float64(2.0*float64(y))/fn - 1.0
		for x := int64(0); x < n; x++ {
			cr := float64(2.0*float64(x))/fn - 1.5
			zr, zi := 0.0, 0.0
			var i int64
			for i < maxIter {
				tr := (float64(zr*zr) - float64(zi*zi)) + cr
				ti := float64(float64(2.0*zr)*zi) + ci
				zr = tr
				zi = ti
				if float64(zr*zr)+float64(zi*zi) > 4.0 {
					break
				}
				i += 1
			}
			total += i
		}
	}
	fmt.Println(total)
}
