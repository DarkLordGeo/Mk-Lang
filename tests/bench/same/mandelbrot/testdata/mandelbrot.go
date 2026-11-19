// mandelbrot, Go: the reference algorithm of ../README.md.
// Scalar float64, one point at a time, at most 50 iterations, the escape test
// after each step, the README's parenthesisation, every product that meets
// an addition written float64(a*b) so it is never fused. int64
// counters (they wrap). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

const maxIter = 50

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: mandelbrot <n>")
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
