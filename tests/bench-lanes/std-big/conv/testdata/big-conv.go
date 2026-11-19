// big-conv, Go: the Mk row's loop with math/big (standard library): x0 is
// 2000 splitmix64 words of the seed; n times s = x.Text(10), x = SetString(s,
// 10) + 1; prints x's low word xor the sum of the texts' lengths. Usage:
// big-conv <n> <seed>.
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

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	seed, _ := strconv.ParseUint(os.Args[2], 10, 64)
	st := seed
	xw := make([]big.Word, 2000)
	for i := range xw {
		st += 0x9e3779b97f4a7c15
		xw[i] = big.Word(mix(st))
	}
	x := new(big.Int).SetBits(xw)
	one := big.NewInt(1)
	var digits uint64
	for i := 0; i < n; i++ {
		s := x.Text(10)
		if _, ok := x.SetString(s, 10); !ok {
			panic("SetString")
		}
		x.Add(x, one)
		digits += uint64(len(s))
	}
	bits := x.Bits()
	var low uint64
	if len(bits) > 0 {
		low = uint64(bits[0])
	}
	fmt.Println(low ^ digits)
}
