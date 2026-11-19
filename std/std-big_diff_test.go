// Why: std.big's arithmetic must give exactly math/big's answers, including
// at the sizes and word patterns random inputs rarely reach.
// What: TestSTDBIG_ArithMatchesMathBig, TestSTDBIG_DivisionAddBack and
// TestSTDBIG_MulShapeSweep.
// How: runs the std.big driver (std-big_harness_test.go) over random and
// adversarial operands and compares each result with Go's math/big.

package std

import (
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"strings"
	"testing"
)

// bigSizes are the operand lengths in words the generator draws from: the
// small ones, each side of the Karatsuba threshold and its doublings, and
// a few larger ones.
var bigSizes = []int{0, 1, 1, 2, 2, 3, 4, 5, 7, 8, 15, 16, 31, 32, 33, 39, 40, 41, 63, 64, 65, 79, 80, 81, 100, 127, 128, 129, 160, 200, 257, 333}

// bigGen draws operands with adversarial word patterns.
type bigGen struct{ r *rand.Rand }

func newBigGen(seed uint64) *bigGen {
	return &bigGen{r: rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))}
}

// word is one word of a pattern.
func (g *bigGen) word(pattern int) uint64 {
	switch pattern {
	case 1:
		return math.MaxUint64
	case 2:
		if g.r.IntN(8) == 0 {
			return g.r.Uint64()
		}
		return 0
	case 3:
		return []uint64{0, 1, 1 << 63, math.MaxUint64, math.MaxUint64 - 1, 1<<63 - 1}[g.r.IntN(6)]
	default:
		return g.r.Uint64()
	}
}

// nat is a magnitude of exactly n words (top word not zero when n > 0).
func (g *bigGen) nat(n int) *big.Int {
	pattern := g.r.IntN(5)
	ws := make([]big.Word, n)
	for i := range ws {
		ws[i] = big.Word(g.word(pattern))
	}
	if n > 0 {
		for ws[n-1] == 0 {
			ws[n-1] = big.Word(g.word(pattern) | uint64(g.r.IntN(3)))
		}
		if g.r.IntN(4) == 0 {
			ws[n-1] |= 1 << 63
		}
	}
	return new(big.Int).SetBits(ws)
}

// size draws an operand length.
func (g *bigGen) size() int {
	return bigSizes[g.r.IntN(len(bigSizes))]
}

// int is a signed value of n words.
func (g *bigGen) int(n int) *big.Int {
	x := g.nat(n)
	if g.r.IntN(2) == 0 {
		x.Neg(x)
	}
	return x
}

// operand writes x in the driver's form, sometimes with zero top words
// that FromWords must leave out.
func (g *bigGen) operand(x *big.Int) string {
	s := bigInt(x)
	if g.r.IntN(6) == 0 {
		if x.Sign() < 0 {
			s = "-" + s[1:]
		}
		for k := g.r.IntN(3) + 1; k > 0; k-- {
			s += " 0000000000000000"
		}
	}
	return s
}

func bigBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// bigInfo is the driver's "info" lines for x.
func bigInfo(x *big.Int) []string {
	f, _ := x.Float64()
	i := "none"
	if x.IsInt64() {
		i = fmt.Sprint(x.Int64())
	}
	return []string{fmt.Sprintf("%d %d %016x %s", x.Sign(), x.BitLen(), math.Float64bits(f), i), fmt.Sprintf("%x", x.Bytes())}
}

// TestSTDBIG_ArithMatchesMathBig: Add, Sub, Mul, DivMod (Go's QuoRem), Mod
// (Go's Mod, Euclidean), Cmp, Less, Eq, Hash (a Map lookup by a copy), Shl, Shr (Go's Rsh, toward
// negative infinity), Neg, Clone, Bit (two's complement, Go's Bit), Sign,
// BitLen, ToF64 (Go's Float64), ToI64, Bytes, FromBytes and FromWords, over
// random and adversarial sizes and patterns, in dev and release.
func TestSTDBIG_ArithMatchesMathBig(t *testing.T) {
	g := newBigGen(1)
	var c bigCases
	for i := 0; i < 1500; i++ {
		x := g.int(g.size())
		y := g.int(g.size())
		ox, oy := g.operand(x), g.operand(y)
		switch i % 12 {
		case 0:
			c.add("add", []string{ox, oy}, bigInt(new(big.Int).Add(x, y)))
			c.add("sub", []string{ox, oy}, bigInt(new(big.Int).Sub(x, y)))
			c.add("add", []string{ox, bigInt(new(big.Int).Neg(x))}, bigInt(new(big.Int)))
		case 1, 2:
			c.add("mul", []string{ox, oy}, bigInt(new(big.Int).Mul(x, y)))
		case 3, 4:
			if y.Sign() == 0 {
				y.SetInt64(7)
				oy = bigInt(y)
			}
			q, r := new(big.Int).QuoRem(x, y, new(big.Int))
			c.add("divmod", []string{ox, oy}, bigInt(q), bigInt(r))
			c.add("mod", []string{ox, oy}, bigInt(new(big.Int).Mod(x, y)))
		case 5:
			c.add("cmp", []string{ox, oy}, fmt.Sprintf("%d %s", x.Cmp(y), bigBool(x.Cmp(y) < 0)))
			c.add("cmp", []string{ox, ox}, "0 false")
			c.add("eq", []string{ox, oy}, bigBool(x.Cmp(y) == 0)+" "+bigBool(x.Cmp(y) == 0))
			c.add("eq", []string{ox, bigInt(x)}, "true true")
		case 6:
			n := g.r.IntN(300)
			c.add("shl", []string{ox, fmt.Sprint(n)}, bigInt(new(big.Int).Lsh(x, uint(n))))
			c.add("shr", []string{ox, fmt.Sprint(n)}, bigInt(new(big.Int).Rsh(x, uint(n))))
			k := x.BitLen() + 70
			c.add("shr", []string{ox, fmt.Sprint(k)}, bigInt(new(big.Int).Rsh(x, uint(k))))
		case 7:
			c.add("neg", []string{ox}, bigInt(new(big.Int).Neg(x)))
			c.add("clone", []string{ox}, bigInt(x))
		case 8:
			for _, b := range []int{0, 1, 63, 64, 65, g.r.IntN(x.BitLen() + 130)} {
				c.add("bit", []string{ox, fmt.Sprint(b)}, bigBool(x.Bit(b) == 1))
			}
		case 9, 10:
			c.add("info", []string{ox}, bigInfo(x)...)
		case 11:
			b := make([]byte, g.r.IntN(70))
			for k := range b {
				b[k] = byte(g.word(g.r.IntN(4)))
			}
			if len(b) > 0 && g.r.IntN(3) == 0 {
				b[0] = 0
			}
			c.add("frombytes", []string{fmt.Sprintf("%x", b) + strings.Repeat("", 0)}, bigInt(new(big.Int).SetBytes(b)))
		}
	}
	// Edges: the i64 limits, f64 rounding at the halfway points, and the
	// largest finite f64 and past it.
	edges := []*big.Int{
		big.NewInt(math.MaxInt64), big.NewInt(math.MinInt64),
		new(big.Int).Add(big.NewInt(math.MaxInt64), big.NewInt(1)),
		new(big.Int).Sub(big.NewInt(math.MinInt64), big.NewInt(1)),
		new(big.Int).Lsh(big.NewInt(1), 1023), new(big.Int).Lsh(big.NewInt(1), 1024),
		new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1024), big.NewInt(1)),
		new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1024), new(big.Int).Lsh(big.NewInt(1), 970)),
		new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 1024), new(big.Int).Lsh(big.NewInt(1), 971)),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), new(big.Int).Lsh(big.NewInt(1), 47)),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), new(big.Int).Lsh(big.NewInt(3), 47)),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), big.NewInt(1)),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 100), new(big.Int).Lsh(big.NewInt(1), 48)),
		new(big.Int).SetUint64(1<<53 + 1), new(big.Int).SetUint64(math.MaxUint64),
	}
	for _, e := range edges {
		for _, x := range []*big.Int{e, new(big.Int).Neg(e)} {
			c.add("info", []string{bigInt(x)}, bigInfo(x)...)
		}
	}
	c.add("info", []string{"+"}, bigInfo(new(big.Int))...)
	bigRun(t, &c)
}

// bigKnuthStep is one quotient digit of the driver's DivKnuth (div.mk) for a
// normalized v of n >= 2 words and u of n + 1 words with u < v * 2^64: the
// estimate after the 3-by-2 refinement, and whether it needed the add-back.
func bigKnuthStep(u, v []uint64) (qhat uint64, addBack bool) {
	n := len(v)
	vn1, vn2 := v[n-1], v[n-2]
	qhat = math.MaxUint64
	if u[n] != vn1 {
		var rhat uint64
		qhat, rhat = bits.Div64(u[n], u[n-1], vn1)
		for {
			hi, lo := bits.Mul64(qhat, vn2)
			if !(hi > rhat || hi == rhat && lo > u[n-2]) {
				break
			}
			qhat--
			prev := rhat
			rhat += vn1
			if rhat < prev {
				break
			}
		}
	}
	toInt := func(ws []uint64) *big.Int {
		bw := make([]big.Word, len(ws))
		for i, w := range ws {
			bw[i] = big.Word(w)
		}
		return new(big.Int).SetBits(bw)
	}
	p := new(big.Int).Mul(toInt(v), new(big.Int).SetUint64(qhat))
	return qhat, p.Cmp(toInt(u)) > 0
}

// TestSTDBIG_DivisionAddBack: dividends and divisors found by searching
// structured words for a quotient digit whose 3-by-2 estimate is still one
// too large, so Algorithm D's add-back step (D6) runs, which random words
// reach with probability about 2^-64; and divisors of all-ones words and
// with only the top bit set.
func TestSTDBIG_DivisionAddBack(t *testing.T) {
	g := newBigGen(2)
	pick := []uint64{0, 1, 2, 1 << 63, 1<<63 + 1, math.MaxUint64, math.MaxUint64 - 1, 1<<63 - 1}
	var c bigCases
	found := 0
	for tries := 0; found < 40 && tries < 2_000_000; tries++ {
		n := 2 + g.r.IntN(3)
		v := make([]uint64, n)
		for i := range v {
			v[i] = pick[g.r.IntN(len(pick))]
		}
		v[n-1] |= 1 << 63
		u := make([]uint64, n+1)
		for i := range u {
			u[i] = pick[g.r.IntN(len(pick))]
		}
		// u < v * 2^64: its top n words are below v.
		if bigCmpWords(u[1:], v) >= 0 {
			continue
		}
		if _, back := bigKnuthStep(u, v); !back {
			continue
		}
		found++
		// Put the step under more words, so it runs mid-quotient too.
		low := g.nat(g.r.IntN(3))
		x := new(big.Int).Lsh(bigWordsInt(u), uint(64*len(low.Bits())))
		x.Or(x, low)
		y := bigWordsInt(v)
		for _, sx := range []int{1, -1} {
			xs := new(big.Int).Mul(x, big.NewInt(int64(sx)))
			q, r := new(big.Int).QuoRem(xs, y, new(big.Int))
			c.add("divmod", []string{bigInt(xs), bigInt(y)}, bigInt(q), bigInt(r))
		}
	}
	if found < 40 {
		t.Fatalf("found %d add-back cases, want 40", found)
	}
	for _, n := range []int{2, 3, 5, 40, 41, 100} {
		ones := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(64*n)), big.NewInt(1))
		top := new(big.Int).Lsh(big.NewInt(1), uint(64*n-1))
		for _, y := range []*big.Int{ones, top, new(big.Int).Add(top, big.NewInt(1))} {
			for k := 0; k < 4; k++ {
				x := g.nat(n + g.r.IntN(2*n+2))
				q, r := new(big.Int).QuoRem(x, y, new(big.Int))
				c.add("divmod", []string{bigInt(x), bigInt(y)}, bigInt(q), bigInt(r))
				xm := new(big.Int).Sub(new(big.Int).Mul(y, g.nat(g.r.IntN(n+1)+1)), big.NewInt(1))
				q, r = new(big.Int).QuoRem(xm, y, new(big.Int))
				c.add("divmod", []string{bigInt(xm), bigInt(y)}, bigInt(q), bigInt(r))
			}
		}
	}
	bigRun(t, &c)
}

func bigCmpWords(x, y []uint64) int {
	for i := len(x) - 1; i >= 0; i-- {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func bigWordsInt(ws []uint64) *big.Int {
	bw := make([]big.Word, len(ws))
	for i, w := range ws {
		bw[i] = big.Word(w)
	}
	return new(big.Int).SetBits(bw)
}

// TestSTDBIG_MulShapeSweep: Mul at every pair of lengths (n, k), k <= n,
// from 40 to 130 words, where Karatsuba starts, unbalanced products are
// cut into pieces of k words and the last piece recurses with the roles
// swapped (MulNat, MulScratch), plus every k below 40 against n from 40 to
// 130 in steps of 7 (the schoolbook path beside a long operand). Operands
// alternate all-ones words, which carry furthest, with random ones.
func TestSTDBIG_MulShapeSweep(t *testing.T) {
	g := newBigGen(13)
	var c bigCases
	ones := func(n int) *big.Int {
		x := new(big.Int).Lsh(big.NewInt(1), uint(64*n))
		return x.Sub(x, big.NewInt(1))
	}
	pair := func(n, k int) {
		x, y := g.nat(n), g.nat(k)
		if (n+k)%3 == 0 {
			x, y = ones(n), ones(k)
		}
		if (n*k)%2 == 1 {
			y.Neg(y)
		}
		c.add("mul", []string{bigInt(x), bigInt(y)}, bigInt(new(big.Int).Mul(x, y)))
	}
	for n := 40; n <= 130; n++ {
		for k := 40; k <= n; k++ {
			pair(n, k)
		}
	}
	for n := 40; n <= 130; n += 7 {
		for k := 1; k < 40; k++ {
			pair(n, k)
		}
	}
	bigRun(t, &c)
}
