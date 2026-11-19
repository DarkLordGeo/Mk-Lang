// math, Go, idiomatic std: package math's f64 functions over 4,096 inputs
// built from splitmix64 bits (README.md), reps passes, pass r starting at
// input r. The f32 rows call the f64 function and convert (Go has no f32
// math). The checksum adds bits >> 32 of each f64 result (bits >> 16 of
// each f32 result) with wrapping adds. One loop per function, each call
// direct.
// Usage: math <fn> <reps> <seed>.
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

func f64of(z uint64, lo, hi int, signed bool) float64 {
	e := uint64(lo+1023) + (z>>53)%uint64(hi-lo+1)
	var sign uint64
	if signed {
		sign = (z & 0x0010000000000000) << 11
	}
	return math.Float64frombits(sign | e<<52 | z&0x000FFFFFFFFFFFFF)
}

func f32of(z uint64, lo, hi int, signed bool) float32 {
	e := uint64(lo+127) + (z>>53)%uint64(hi-lo+1)
	var sign uint64
	if signed {
		sign = (z & 0x0010000000000000) >> 21
	}
	return math.Float32frombits(uint32(sign | e<<23 | z&0x7FFFFF))
}

type spec struct {
	lo, hi int
	signed bool
}

func specOf(name string) spec {
	switch name {
	case "exp", "expf":
		return spec{-10, 5, true}
	case "log", "logf":
		return spec{-100, 100, false}
	case "sin", "sinf":
		return spec{-10, 2, true}
	case "sinlarge":
		return spec{20, 60, true}
	case "pow":
		return spec{-4, 3, false}
	case "sinh":
		return spec{-10, 4, true}
	case "cbrt":
		return spec{-200, 200, true}
	case "fma":
		return spec{-5, 5, true}
	}
	return spec{-10, 10, true} // atan2
}

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: math <fn> <reps> <seed>")
		os.Exit(2)
	}
	name := os.Args[1]
	reps, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	sp := specOf(name)
	f32row := name == "expf" || name == "logf" || name == "sinf"
	xs := make([]float64, n)
	ys := make([]float64, n)
	zs := make([]float64, n)
	fs := make([]float32, n)
	for i := 0; i < n; i++ {
		st += 0x9E3779B97F4A7C15
		a := mix(st)
		st += 0x9E3779B97F4A7C15
		b := mix(st)
		st += 0x9E3779B97F4A7C15
		c := mix(st)
		xs[i] = f64of(a, sp.lo, sp.hi, sp.signed)
		if f32row {
			fs[i] = f32of(a, sp.lo, sp.hi, sp.signed)
		}
		if name == "pow" {
			ys[i] = f64of(b, -4, 4, true)
		} else {
			ys[i] = f64of(b, sp.lo, sp.hi, sp.signed)
		}
		zs[i] = f64of(c, sp.lo, sp.hi, sp.signed)
	}
	var acc uint64
	for r := 0; r < reps; r++ {
		switch name {
		case "exp":
			for i := 0; i < n; i++ {
				acc += math.Float64bits(math.Exp(xs[(i+r)&(n-1)])) >> 32
			}
		case "log":
			for i := 0; i < n; i++ {
				acc += math.Float64bits(math.Log(xs[(i+r)&(n-1)])) >> 32
			}
		case "sin", "sinlarge":
			for i := 0; i < n; i++ {
				acc += math.Float64bits(math.Sin(xs[(i+r)&(n-1)])) >> 32
			}
		case "pow":
			for i := 0; i < n; i++ {
				k := (i + r) & (n - 1)
				acc += math.Float64bits(math.Pow(xs[k], ys[k])) >> 32
			}
		case "atan2":
			for i := 0; i < n; i++ {
				k := (i + r) & (n - 1)
				acc += math.Float64bits(math.Atan2(ys[k], xs[k])) >> 32
			}
		case "sinh":
			for i := 0; i < n; i++ {
				acc += math.Float64bits(math.Sinh(xs[(i+r)&(n-1)])) >> 32
			}
		case "cbrt":
			for i := 0; i < n; i++ {
				acc += math.Float64bits(math.Cbrt(xs[(i+r)&(n-1)])) >> 32
			}
		case "fma":
			for i := 0; i < n; i++ {
				k := (i + r) & (n - 1)
				acc += math.Float64bits(math.FMA(xs[k], ys[k], zs[k])) >> 32
			}
		case "expf":
			for i := 0; i < n; i++ {
				acc += uint64(math.Float32bits(float32(math.Exp(float64(fs[(i+r)&(n-1)])))) >> 16)
			}
		case "logf":
			for i := 0; i < n; i++ {
				acc += uint64(math.Float32bits(float32(math.Log(float64(fs[(i+r)&(n-1)])))) >> 16)
			}
		case "sinf":
			for i := 0; i < n; i++ {
				acc += uint64(math.Float32bits(float32(math.Sin(float64(fs[(i+r)&(n-1)])))) >> 16)
			}
		}
	}
	fmt.Println(acc)
}
