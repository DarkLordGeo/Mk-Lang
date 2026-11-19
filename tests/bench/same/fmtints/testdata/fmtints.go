// fmtints, Go: the reference algorithm of ../README.md. The textbook
// conversion written out (one digit per division by 10 into a [20]byte
// temporary, copied out byte by byte), not strconv: the idiomatic fmt row
// compares the std formatters. A []byte output buffer indexed buf[pos]
// (bounds checked), int positions and sums (they wrap). Built with
// GOAMD64=v2 -pgo=off.
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
		fmt.Fprintln(os.Stderr, "usage: fmtints <n> <rounds> <seed>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	rounds, _ := strconv.Atoi(os.Args[2])
	st, _ := strconv.ParseUint(os.Args[3], 10, 64)
	xs := make([]int64, n)
	for i := 0; i < n; i++ {
		st += 0x9e3779b97f4a7c15
		xs[i] = int64(mix(st)%2000000001) - 1000000000
	}
	buf := make([]byte, n*12)
	var tmp [20]byte
	total := 0
	for r := 0; r < rounds; r++ {
		pos := 0
		for i := 0; i < n; i++ {
			x := xs[i]
			var u uint64
			if x < 0 {
				buf[pos] = 45
				pos += 1
				u = uint64(-x)
			} else {
				u = uint64(x)
			}
			k := 20
			for {
				k -= 1
				tmp[k] = 48 + byte(u%10)
				u = u / 10
				if u == 0 {
					break
				}
			}
			for j := k; j < 20; j++ {
				buf[pos] = tmp[j]
				pos += 1
			}
			buf[pos] = 10
			pos += 1
		}
		sum := 0
		for i := 0; i < pos; i++ {
			sum += int(buf[i])
		}
		total += sum
	}
	fmt.Println(total)
}
