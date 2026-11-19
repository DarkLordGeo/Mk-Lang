// big-modpow, Go: the Mk row's loop with math/big (standard library): m, e
// and x0 are 32 splitmix64 words of the seed each (m odd, top bit set);
// n times x = x^e mod m (Exp, Montgomery with a 4-bit window on amd64
// assembly); prints x's low word xor its word count. Usage: big-modpow
// <n> <seed>.
package main

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
)

func mix(x uint64) uint64 {
	z := x
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func fill(st *uint64, n int) []big.Word {
	z := make([]big.Word, n)
	for i := range z {
		*st += 0x9e3779b97f4a7c15
		z[i] = big.Word(mix(*st))
	}
	return z
}

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	seed, _ := strconv.ParseUint(os.Args[2], 10, 64)
	st := seed
	mw := fill(&st, 32)
	ew := fill(&st, 32)
	xw := fill(&st, 32)
	mw[0] |= 1
	mw[31] |= 1 << 63
	m := new(big.Int).SetBits(mw)
	e := new(big.Int).SetBits(ew)
	x := new(big.Int).SetBits(xw)
	r := new(big.Int)
	for i := 0; i < n; i++ {
		r.Exp(x, e, m)
		x, r = r, x
	}
	bits := x.Bits()
	var low uint64
	if len(bits) > 0 {
		low = uint64(bits[0])
	}
	fmt.Println(low ^ uint64(len(bits)))
}
