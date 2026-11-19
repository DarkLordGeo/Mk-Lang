// Why: Toom-3 multiplication has threshold, recursion and sign edges that
// random tests rarely reach.
// What: TestSTDBIG_ToomSweep for std/big/toom.mk.
// How: compares with Go's math/big at every length across the Toom threshold, the
// lengths where Toom recurses into Toom, unbalanced shapes cut into
// Toom-sized pieces, and operands whose evaluations at -1 and -2 are
// negative.

package std

import (
	"math/big"
	"testing"
)

// bigToomWords mirrors std/big/toom.mk's ToomWords.
const bigToomWords = 300

// bigThirds is x0 + x1 B^k + x2 B^2k over words of B = 2^64.
func bigThirds(k int, x0, x1, x2 *big.Int) *big.Int {
	z := new(big.Int).Lsh(x2, uint(128*k))
	z.Add(z, new(big.Int).Lsh(x1, uint(64*k)))
	return z.Add(z, x0)
}

// TestSTDBIG_ToomSweep: Mul at
//   - every balanced length from ToomWords - 5 to ToomWords + 30, so each
//     residue of the length mod 3 (the short top third x2) meets each
//     pattern: random, all ones, sparse, top bit, signs mixed, and squares;
//   - lengths where Toom's products of k + 1 words are Toom again (898 to
//     905 words, 2,701 words: three levels);
//   - unbalanced pairs cut into pieces of at least ToomWords words;
//   - operands built from thirds so that x0 - x1 + x2 and x0 - 2 x1 + 4 x2
//     are negative, zero, or carry into the evaluation's top word, for one
//     or both operands (each sign of the products at -1 and -2).
func TestSTDBIG_ToomSweep(t *testing.T) {
	g := newBigGen(29)
	var c bigCases
	ones := func(n int) *big.Int {
		x := new(big.Int).Lsh(big.NewInt(1), uint(64*n))
		return x.Sub(x, big.NewInt(1))
	}
	mul := func(x, y *big.Int) {
		c.add("mul", []string{bigInt(x), bigInt(y)}, bigInt(new(big.Int).Mul(x, y)))
	}
	for n := bigToomWords - 5; n <= bigToomWords+30; n++ {
		x, y := g.int(n), g.int(n)
		switch n % 4 {
		case 0:
			x, y = ones(n), ones(n)
		case 1:
			y = new(big.Int).Set(x)
		}
		mul(x, y)
	}
	for _, n := range []int{898, 899, 900, 901, 902, 903, 905, 2701} {
		mul(g.int(n), g.int(n))
	}
	mul(ones(903), ones(903))
	for _, nk := range [][2]int{{1000, 300}, {1000, 301}, {700, 350}, {2000, 333}, {950, 600}, {1201, 400}} {
		mul(g.int(nk[0]), g.int(nk[1]))
		mul(ones(nk[0]), ones(nk[1]))
	}
	// Thirds of k words with a top third of r words: n = 300, 301 (the top
	// third two words short) and 303.
	for _, kr := range [][2]int{{100, 100}, {101, 99}, {101, 101}} {
		k, r := kr[0], kr[1]
		bit := func(n int) *big.Int { return new(big.Int).Lsh(big.NewInt(1), uint(64*(n-1))) }
		zero := new(big.Int)
		cases := []*big.Int{
			bigThirds(k, zero, ones(k), bit(r)),                       // x0 - x1 + x2 < 0, x0 - 2x1 + 4x2 < 0
			bigThirds(k, ones(k), ones(k), ones(r)),                   // x0 + x1 + x2 carries
			bigThirds(k, bit(k), ones(k), bit(r)),                     // both evaluations negative
			bigThirds(k, ones(k), zero, ones(r)),                      // x0 + x2 carries, x1 = 0
			bigThirds(k, zero, bit(k), ones(r)),                       // 4x2 - 2x1 carries into the top word
			bigThirds(k, bit(k), new(big.Int).Lsh(bit(k), 1), bit(r)), // r = k: x0 - x1 + x2 = 0
		}
		for i, x := range cases {
			for j, y := range cases {
				if (i+j)%2 == 0 {
					y = new(big.Int).Neg(y)
				}
				mul(x, y)
			}
		}
	}
	bigRun(t, &c)
}
