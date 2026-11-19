// digits, Go, idiomatic std: strconv.AppendFloat(buf, x, 'e', -1, 64)
// (variant shortest), 'e' with 16 (variant exact17) or 'f' with 6
// (variant fixed) over 4,096 positive f64 inputs built from splitmix64
// bits (README.md), reps passes, pass r starting at input r. The digits and exponent are read back from the
// text, trailing zeros dropped, and folded as every column folds them.
// Usage: digits <variant> <reps> <seed>.
package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
)

const n = 4096

func mix(z uint64) uint64 {
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

// fold reads d.ddde±XX.
func fold(b []byte) uint64 {
	var ln, sum uint64
	k := 0
	for k < len(b) && b[k] != 'e' {
		k++
	}
	last := 0
	for i := 0; i < k; i++ {
		if b[i] != '.' && b[i] != '0' {
			last = i + 1
		}
	}
	for i := 0; i < last; i++ {
		if b[i] != '.' {
			ln++
			if i < last-1 {
				sum += uint64(b[i] - '0')
			}
		}
	}
	e, _ := strconv.Atoi(string(b[k+1:]))
	return ln*1000003 + uint64(e+400)*1009 + sum
}

// foldF reads ddd.dddddd: the digits with leading and trailing zeros
// dropped, the exponent from the point's place.
func foldF(b []byte) uint64 {
	k := len(b)
	for i, c := range b {
		if c == '.' {
			k = i
		}
	}
	var ds []byte
	for _, c := range b {
		if c != '.' {
			ds = append(ds, c)
		}
	}
	dp := k
	for len(ds) > 0 && ds[0] == '0' {
		ds = ds[1:]
		dp--
	}
	for len(ds) > 0 && ds[len(ds)-1] == '0' {
		ds = ds[:len(ds)-1]
	}
	if len(ds) == 0 {
		return 400 * 1009
	}
	var sum uint64
	for _, c := range ds[:len(ds)-1] {
		sum += uint64(c - '0')
	}
	return uint64(len(ds))*1000003 + uint64(dp-1+400)*1009 + sum
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: digits <variant> <reps> <seed>")
		os.Exit(2)
	}
	name := os.Args[1]
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]float64, n)
	for i := range xs {
		st += 0x9E3779B97F4A7C15
		z := mix(st)
		e := 723 + (z>>53)%601
		if name == "fixed" {
			e = 1003 + (z>>53)%81
		}
		xs[i] = math.Float64frombits(e<<52 | z&0x000FFFFFFFFFFFFF)
	}
	prec, f := -1, byte('e')
	switch name {
	case "exact17":
		prec = 16
	case "fixed":
		prec, f = 6, 'f'
	}
	var acc uint64
	buf := make([]byte, 0, 64)
	for r := 0; r < reps; r++ {
		for i := 0; i < n; i++ {
			buf = strconv.AppendFloat(buf[:0], xs[(i+r)&(n-1)], f, prec, 64)
			if f == 'f' {
				acc += foldF(buf)
			} else {
				acc += fold(buf)
			}
		}
	}
	fmt.Println(acc)
}
