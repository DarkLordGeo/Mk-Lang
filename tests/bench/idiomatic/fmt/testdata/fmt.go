// fmt, Go, idiomatic std: the task of README.md with strconv.AppendInt and
// strconv.ParseInt for the int64, and strconv.AppendFloat(..., 'g', -1, 64)
// (the shortest digits that round-trip) and strconv.ParseFloat for the
// float64, into one reused byte buffer. Usage: fmt <n> <rounds> <seed>.
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
		fmt.Fprintln(os.Stderr, "usage: fmt <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	buf := make([]byte, 0, 64)
	var sum, ok int64
	for range rounds {
		for range n {
			st += 0x9e3779b97f4a7c15
			z := mix(st)
			x := int64(z >> 1)
			if z&1 == 1 {
				x = -x
			}
			st += 0x9e3779b97f4a7c15
			f := float64(mix(st)>>11) * (1.0 / 9007199254740992.0) * 1e6
			buf = strconv.AppendInt(buf[:0], x, 10)
			y, err := strconv.ParseInt(string(buf), 10, 64)
			if err != nil {
				panic(err)
			}
			buf = strconv.AppendFloat(buf[:0], f, 'g', -1, 64)
			g, err := strconv.ParseFloat(string(buf), 64)
			if err != nil {
				panic(err)
			}
			sum += y + int64(g)
			if g == f {
				ok++
			}
		}
	}
	fmt.Println(sum + ok)
}
