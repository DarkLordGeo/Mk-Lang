// fannkuch, Go: the reference algorithm of ../README.md (the Benchmarks
// Game's fannkuch-redux, sequential). Three []int of n indexed perm[i]
// (bounds checked), the same loops and next-permutation step as every other
// column. int arithmetic (it wraps). Built with GOAMD64=v2 -pgo=off.
package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fannkuch <n>")
		os.Exit(2)
	}
	n, _ := strconv.Atoi(os.Args[1])
	perm := make([]int, n)
	perm1 := make([]int, n)
	count := make([]int, n)
	for i := 0; i < n; i++ {
		perm1[i] = i
	}
	r, permCount, checksum, maxFlips := n, 0, 0, 0
	done := false
	for !done {
		for r != 1 {
			count[r-1] = r
			r -= 1
		}
		for i := 0; i < n; i++ {
			perm[i] = perm1[i]
		}
		flips := 0
		k := perm[0]
		for k != 0 {
			i, j := 0, k
			for i < j {
				t := perm[i]
				perm[i] = perm[j]
				perm[j] = t
				i += 1
				j -= 1
			}
			flips += 1
			k = perm[0]
		}
		if flips > maxFlips {
			maxFlips = flips
		}
		if permCount%2 == 0 {
			checksum += flips
		} else {
			checksum -= flips
		}
		for {
			if r == n {
				done = true
				break
			}
			perm0 := perm1[0]
			for i := 0; i < r; i++ {
				perm1[i] = perm1[i+1]
			}
			perm1[r] = perm0
			count[r] -= 1
			if count[r] > 0 {
				break
			}
			r += 1
		}
		permCount += 1
	}
	fmt.Println(checksum*1000 + maxFlips)
}
