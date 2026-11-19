// big-factorial, Go: the Mk row's loop with math/big (standard library):
// for i from 2 to n, acc = acc * (i + seed mod 7) (Mul by a one-word
// value), then acc.Text(10); prints the text's length times 31 plus the sum
// of its digits times their 1-based position, wrapping. Usage:
// big-factorial <n> <seed>.
package main

import (
	"fmt"
	"math/big"
	"os"
	"strconv"
)

func main() {
	n, _ := strconv.Atoi(os.Args[1])
	seed, _ := strconv.ParseInt(os.Args[2], 10, 64)
	acc := big.NewInt(1)
	f := new(big.Int)
	for i := 2; i <= n; i++ {
		f.SetInt64(int64(i) + seed%7)
		acc.Mul(acc, f)
	}
	text := acc.Text(10)
	sum := uint64(len(text)) * 31
	for k := 0; k < len(text); k++ {
		sum += uint64(text[k]-'0') * uint64(k+1)
	}
	fmt.Println(sum)
}
