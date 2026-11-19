// Why: the Karatsuba square root has many edge paths (normalizing shifts,
// the remainder correction, recursion) that random tests rarely reach.
// What: TestSTDBIG_SqrtSweep for std/big/sqrt.mk.
// How: compares with Go's math/big Sqrt at every length across the
// leaf and both parities of the word count, each shift that normalizes the
// top word, the remainder correction's inputs (squares, squares minus one,
// (r + 1)^2 - 1), and lengths where the divisions recurse.

package std

import (
	"math/big"
	"testing"
)

// TestSTDBIG_SqrtSweep: Sqrt at
//   - every length from 1 to 80 words, each with a random value, all ones,
//     a top word of 1, 2 or 3 (the normalizing shift's extremes) and a
//     power of two;
//   - r^2, r^2 - 1 and (r + 1)^2 - 1 = r^2 + 2r for random r of 1 to 60
//     words and for r = 2^(64k) - 1 and 2^(64k - 1), where the remainder
//     is zero, maximal, or the root's low half is all ones or zero, so the
//     division's quotient reaches B^l and the correction step runs;
//   - lengths where the divisions are recursive (divisors of 60 words and
//     more: x of 240 words and up) and three levels of the recursion
//     (1,001 to 3,001 words).
func TestSTDBIG_SqrtSweep(t *testing.T) {
	g := newBigGen(41)
	var c bigCases
	sqrt := func(x *big.Int) {
		c.add("sqrt", []string{bigInt(x)}, bigInt(new(big.Int).Sqrt(x)))
	}
	ones := func(n int) *big.Int {
		x := new(big.Int).Lsh(big.NewInt(1), uint(64*n))
		return x.Sub(x, big.NewInt(1))
	}
	for n := 1; n <= 80; n++ {
		sqrt(g.nat(n))
		sqrt(ones(n))
		top := new(big.Int).Lsh(big.NewInt(int64(n%3+1)), uint(64*(n-1)))
		sqrt(top.Add(top, g.nat(n-1)))
		sqrt(new(big.Int).Lsh(big.NewInt(1), uint(64*(n-1)+n%64)))
	}
	squares := func(r *big.Int) {
		sq := new(big.Int).Mul(r, r)
		sqrt(sq)
		if sq.Sign() > 0 {
			sqrt(new(big.Int).Sub(sq, big.NewInt(1)))
		}
		sqrt(sq.Add(sq, new(big.Int).Lsh(r, 1)))
	}
	for k := 1; k <= 60; k++ {
		squares(g.nat(k))
		squares(ones(k))
		squares(new(big.Int).Lsh(big.NewInt(1), uint(64*k-1)))
	}
	for _, n := range []int{239, 240, 241, 300, 480, 481, 1001, 1024, 2047, 3001} {
		sqrt(g.nat(n))
		sqrt(ones(n))
		squares(g.nat((n + 1) / 2))
	}
	bigRun(t, &c)
}
