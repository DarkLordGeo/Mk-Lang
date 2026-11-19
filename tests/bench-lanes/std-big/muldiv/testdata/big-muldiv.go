// big-muldiv, Go: the Mk row's loop with math/big (standard library): x0
// (1024 words), y (512 words) and d (1024 words, top bit set) are
// splitmix64 words of the seed; n times p = x y (Mul), (q, r) = p divmod d
// (QuoRem) and x = r + q; prints x's low word xor its word count. Usage:
// big-muldiv <n> <seed>.
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
	xw := fill(&st, 1024)
	yw := fill(&st, 512)
	dw := fill(&st, 1024)
	dw[1023] |= 1 << 63
	x := new(big.Int).SetBits(xw)
	y := new(big.Int).SetBits(yw)
	d := new(big.Int).SetBits(dw)
	p, q, r := new(big.Int), new(big.Int), new(big.Int)
	for i := 0; i < n; i++ {
		p.Mul(x, y)
		q.QuoRem(p, d, r)
		x.Add(r, q)
	}
	bits := x.Bits()
	var low uint64
	if len(bits) > 0 {
		low = uint64(bits[0])
	}
	fmt.Println(low ^ uint64(len(bits)))
}
