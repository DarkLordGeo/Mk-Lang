// Why: std.big's number theory and text conversion must match math/big at
// every size, modulus parity and base.
// What: the ModPow, ModInverse, Gcd, Sqrt, Text, Format, Parse, recursive
// division and Lehmer tests (TestSTDBIG_ModPowMatchesMathBig and others).
// How: random and adversarial operands, odd and even moduli, every base and
// Parse's error cases, through the std.big driver, compared with math/big.

package std

import (
	"fmt"
	"math/big"
	"strings"
	"testing"
)

// TestSTDBIG_ModPowMatchesMathBig: ModPow (Go's Exp of x mod |m|) for odd
// moduli (Montgomery) and even ones (Algorithm D reduction), exponents
// from zero to 2,048 bits across the window widths, negative x, x above m,
// m = 1 and powers of two; ModInverse (Go's ModInverse, none when not
// invertible) and Gcd (Go's GCD of the magnitudes).
func TestSTDBIG_ModPowMatchesMathBig(t *testing.T) {
	g := newBigGen(3)
	mSizes := []int{1, 1, 2, 3, 4, 5, 8, 16, 17, 32, 40, 64}
	eSizes := []int{0, 1, 1, 2, 3, 4, 8, 16, 32}
	var c bigCases
	for i := 0; i < 260; i++ {
		m := g.nat(mSizes[g.r.IntN(len(mSizes))])
		if m.Sign() == 0 {
			m.SetInt64(3)
		}
		switch i % 4 {
		case 0:
			m.SetBit(m, 0, 1)
		case 1:
			m.SetBit(m, 0, 0)
			if m.Sign() == 0 {
				m.SetInt64(2)
			}
		}
		if g.r.IntN(5) == 0 {
			m.Neg(m)
		}
		am := new(big.Int).Abs(m)
		x := g.int(g.r.IntN(2*len(am.Bits()) + 2))
		e := g.nat(eSizes[g.r.IntN(len(eSizes))])
		want := new(big.Int).Exp(new(big.Int).Mod(x, am), e, am)
		c.add("modpow", []string{g.operand(x), g.operand(e), g.operand(m)}, bigInt(want))
		inv := "none"
		if am.Cmp(big.NewInt(1)) == 0 {
			inv = bigInt(new(big.Int))
		} else if v := new(big.Int).ModInverse(new(big.Int).Mod(x, am), am); v != nil {
			inv = bigInt(v)
		}
		c.add("modinv", []string{g.operand(x), g.operand(m)}, inv)
		y := g.int(g.size())
		gcd := new(big.Int).GCD(nil, nil, new(big.Int).Abs(x), new(big.Int).Abs(y))
		c.add("gcd", []string{g.operand(x), g.operand(y)}, bigInt(gcd))
	}
	// Edges: m = 1, e = 0, x = 0, x = m - 1, m = 2^k, a common factor,
	// and Gcd with zero.
	for _, k := range []int{1, 63, 64, 65, 128, 1000} {
		m := new(big.Int).Lsh(big.NewInt(1), uint(k))
		x := g.nat(3)
		e := g.nat(2)
		c.add("modpow", []string{bigInt(x), bigInt(e), bigInt(m)}, bigInt(new(big.Int).Exp(x, e, m)))
	}
	one := big.NewInt(1)
	p := new(big.Int).Sub(new(big.Int).Lsh(one, 127), one) // a Mersenne prime
	for _, x := range []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Sub(p, one), big.NewInt(-1), new(big.Int).Mul(p, big.NewInt(5))} {
		for _, e := range []*big.Int{big.NewInt(0), big.NewInt(1), big.NewInt(2), new(big.Int).Sub(p, big.NewInt(2))} {
			c.add("modpow", []string{bigInt(x), bigInt(e), bigInt(p)}, bigInt(new(big.Int).Exp(new(big.Int).Mod(x, p), e, p)))
		}
		c.add("modpow", []string{bigInt(x), bigInt(big.NewInt(5)), bigInt(one)}, "+")
	}
	c.add("modinv", []string{bigInt(big.NewInt(6)), bigInt(big.NewInt(9))}, "none")
	c.add("modinv", []string{bigInt(big.NewInt(0)), bigInt(big.NewInt(9))}, "none")
	c.add("modinv", []string{bigInt(big.NewInt(-1)), bigInt(big.NewInt(9))}, bigInt(big.NewInt(8)))
	c.add("modinv", []string{bigInt(big.NewInt(3)), bigInt(big.NewInt(1))}, "+")
	c.add("gcd", []string{"+", "+"}, "+")
	c.add("gcd", []string{"+", bigInt(big.NewInt(-12))}, bigInt(big.NewInt(12)))
	bigRun(t, &c)
}

// TestSTDBIG_SqrtMatchesMathBig: Sqrt (Go's Sqrt) over random sizes and
// at squares, squares minus one and powers of two.
func TestSTDBIG_SqrtMatchesMathBig(t *testing.T) {
	g := newBigGen(4)
	var c bigCases
	for i := 0; i < 300; i++ {
		x := g.nat(g.size())
		c.add("sqrt", []string{g.operand(x)}, bigInt(new(big.Int).Sqrt(x)))
		r := g.nat(g.r.IntN(20))
		sq := new(big.Int).Mul(r, r)
		c.add("sqrt", []string{bigInt(sq)}, bigInt(r))
		if sq.Sign() > 0 {
			sq.Sub(sq, big.NewInt(1))
			c.add("sqrt", []string{bigInt(sq)}, bigInt(new(big.Int).Sqrt(sq)))
		}
	}
	for k := 0; k < 300; k += 7 {
		x := new(big.Int).Lsh(big.NewInt(1), uint(k))
		c.add("sqrt", []string{bigInt(x)}, bigInt(new(big.Int).Sqrt(x)))
	}
	bigRun(t, &c)
}

// TestSTDBIG_TextFormatParseMatchMathBig: Text in every base from 2 to 36
// (Go's Text), Format (Go's String, or the 0x form past 512 words), and
// Parse (Go's SetString with the base given) on valid text with signs,
// mixed case and leading zeros, and on its errors: a bad base, no digits,
// a bad digit, and TooLarge at max_digits + 1 but not at max_digits.
func TestSTDBIG_TextFormatParseMatchMathBig(t *testing.T) {
	g := newBigGen(5)
	var c bigCases
	for i := 0; i < 600; i++ {
		x := g.int(g.size())
		base := 2 + g.r.IntN(35)
		if i%3 == 0 {
			base = 10
		}
		text := x.Text(base)
		c.add("text", []string{g.operand(x), fmt.Sprint(base)}, text)
		if i%4 == 0 {
			c.add("format", []string{g.operand(x)}, x.String())
		}
		// Back through Parse: mixed case, a + sometimes, leading zeros.
		s := text
		if g.r.IntN(2) == 0 {
			s = strings.ToUpper(s)
		}
		if x.Sign() >= 0 && g.r.IntN(3) == 0 {
			s = "+" + s
		}
		if g.r.IntN(4) == 0 {
			if s[0] == '-' || s[0] == '+' {
				s = s[:1] + "000" + s[1:]
			} else {
				s = "000" + s
			}
		}
		digits := len(strings.TrimLeft(s, "+-"))
		c.add("parse", []string{fmt.Sprint(base), fmt.Sprint(digits), s}, bigInt(x))
		c.add("parse", []string{fmt.Sprint(base), fmt.Sprint(digits - 1), s}, "TooLarge")
	}
	for _, n := range []int{512, 513, 600} {
		x := g.nat(n)
		x.SetBit(x, 64*n-1, 1)
		want := x.String()
		if n > 512 {
			want = "0x" + x.Text(16)
		}
		c.add("format", []string{bigInt(x)}, want)
		c.add("format", []string{bigInt(new(big.Int).Neg(x))}, "-"+want)
	}
	// The divide-and-conquer path of Text (above 30 words): long values,
	// and powers of the chunk whose low blocks are all zero digits.
	for _, n := range []int{29, 30, 31, 61, 200, 1000, 2500} {
		for _, base := range []int{10, 7, 36} {
			x := g.int(n)
			c.add("text", []string{bigInt(x), fmt.Sprint(base)}, x.Text(base))
		}
	}
	for _, e := range []int{19 * 32, 19*64 + 1, 19 * 200, 19*256 - 1} {
		p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(e)), nil)
		for _, x := range []*big.Int{p, new(big.Int).Sub(p, big.NewInt(1)), new(big.Int).Add(p, big.NewInt(1))} {
			c.add("text", []string{bigInt(x), "10"}, x.Text(10))
		}
	}
	c.add("format", []string{"+"}, "0")
	c.add("text", []string{"+", "2"}, "0")
	bad := []struct{ base, s string }{
		{"10", ""}, {"10", "+"}, {"10", "-"}, {"10", "12a"}, {"10", " 1"}, {"10", "1 "},
		{"10", "1_0"}, {"16", "0x10"}, {"0", "10"}, {"1", "10"}, {"37", "10"}, {"-5", "1"},
		{"36", "z!"}, {"2", "102"}, {"10", "--1"}, {"10", "+-1"}, {"10", "é"},
	}
	for _, b := range bad {
		c.add("parse", []string{b.base, "100", b.s}, "ParseError")
	}
	// TooLarge is decided from the length alone, before the digits.
	c.add("parse", []string{"10", "3", "12x4"}, "TooLarge")
	c.add("parse", []string{"10", "0", "1"}, "TooLarge")
	c.add("parse", []string{"10", "-1", "1"}, "TooLarge")
	c.add("parse", []string{"36", "40", "-Zz09"}, bigInt(bigMustParse("-Zz09", 36)))
	c.add("parse", []string{"10", "30", "-0"}, "+")
	c.add("parse", []string{"10", "30", "+0000"}, "+")
	bigRun(t, &c)
}

func bigMustParse(s string, base int) *big.Int {
	x, ok := new(big.Int).SetString(s, base)
	if !ok {
		panic("bigMustParse: " + s)
	}
	return x
}

// TestSTDBIG_RecursiveDivisionMatchesMathBig: DivMod and Mod with divisors
// from 59 to 1,200 words (Burnikel-Ziegler from 60, divrec.mk), quotients
// from 1 word to three times the divisor, all-ones and top-bit patterns,
// and dividends q y + r with r = y - 1 and r = 0, where the block estimate
// is most often too large.
func TestSTDBIG_RecursiveDivisionMatchesMathBig(t *testing.T) {
	g := newBigGen(7)
	var c bigCases
	for _, n := range []int{59, 60, 61, 64, 90, 99, 100, 101, 119, 120, 121, 150, 199, 200, 257, 400, 777, 1200} {
		for k := 0; k < 6; k++ {
			y := g.int(n)
			x := g.int(n + []int{0, 1, 2, n / 2, n, 2*n + 3}[k])
			q, r := new(big.Int).QuoRem(x, y, new(big.Int))
			c.add("divmod", []string{bigInt(x), bigInt(y)}, bigInt(q), bigInt(r))
			c.add("mod", []string{bigInt(x), bigInt(y)}, bigInt(new(big.Int).Mod(x, y)))
		}
		y := g.nat(n)
		qq := g.nat(n/2 + 1)
		for _, r := range []*big.Int{new(big.Int).Sub(y, big.NewInt(1)), new(big.Int)} {
			x := new(big.Int).Add(new(big.Int).Mul(qq, y), r)
			q, rr := new(big.Int).QuoRem(x, y, new(big.Int))
			c.add("divmod", []string{bigInt(x), bigInt(y)}, bigInt(q), bigInt(rr))
		}
		ones := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(64*n)), big.NewInt(1))
		x := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(64*(2*n))), big.NewInt(1))
		q, r := new(big.Int).QuoRem(x, ones, new(big.Int))
		c.add("divmod", []string{bigInt(x), bigInt(ones)}, bigInt(q), bigInt(r))
	}
	bigRun(t, &c)
}

// TestSTDBIG_ParseLargeMatchesMathBig: Parse's subquadratic path
// (parsedc.mk) past ParseLeafDigits = 20,000 digits and its bit path for
// bases that are powers of two, at the leaf boundary, at digit counts k 2^t
// and k 2^t + 1 where a split leaves a one-digit high half, with all-zero
// high halves (leading zeros), all-zero low halves (powers of the base),
// and the largest value of each length (all digits base - 1).
func TestSTDBIG_ParseLargeMatchesMathBig(t *testing.T) {
	g := newBigGen(11)
	var c bigCases
	digitsOf := func(base int) int {
		k := 0
		for p := new(big.Int).SetInt64(1); p.BitLen() <= 64; p.Mul(p, big.NewInt(int64(base))) {
			k++
		}
		return k - 1
	}
	add := func(base int, s string, x *big.Int) {
		c.add("parse", []string{fmt.Sprint(base), fmt.Sprint(len(strings.TrimLeft(s, "+-"))), s}, bigInt(x))
	}
	const leaf = 20000 // ParseLeafDigits
	for _, base := range []int{10, 7, 36, 3, 16, 2, 8, 32} {
		k := digitsOf(base)
		counts := []int{1, 63, 64, 65, 1000, 5000}
		if base&(base-1) != 0 {
			counts = []int{leaf - 1, leaf, leaf + 1, 3*leaf + 7, 90001}
			for tt := 0; k<<tt <= 4*leaf; tt++ {
				if k<<tt > leaf {
					counts = append(counts, k<<tt, k<<tt+1, k<<tt-1)
				}
			}
		}
		for _, nd := range counts {
			bs := big.NewInt(int64(base))
			// A random value of exactly nd digits.
			lo := new(big.Int).Exp(bs, big.NewInt(int64(nd-1)), nil)
			span := new(big.Int).Sub(new(big.Int).Mul(lo, bs), lo)
			x := new(big.Int).Add(lo, new(big.Int).Mod(g.nat(span.BitLen()/64+1), span))
			s := x.Text(base)
			if len(s) != nd {
				t.Fatalf("generator: %d digits, want %d", len(s), nd)
			}
			add(base, s, x)
			add(base, "-"+strings.ToUpper(s), new(big.Int).Neg(x))
			// All digits base - 1: the largest value of nd digits.
			mx := new(big.Int).Sub(new(big.Int).Mul(lo, bs), big.NewInt(1))
			add(base, mx.Text(base), mx)
			// The power base^(nd - 1): every low digit zero.
			add(base, lo.Text(base), lo)
			// Leading zeros: a short value in a long text.
			short := new(big.Int).Mod(g.nat(3), lo)
			st := short.Text(base)
			add(base, strings.Repeat("0", nd-len(st))+st, short)
			// Zero, nd digits of it.
			add(base, strings.Repeat("0", nd), new(big.Int))
		}
	}
	// TooLarge still comes before any work at the long sizes.
	c.add("parse", []string{"10", "90000", strings.Repeat("9", 90001)}, "TooLarge")
	bigRun(t, &c)
}

// TestSTDBIG_ParseSplitSweep: every high-half length of Parse's first split
// in base 10 (19 digits a word): nd = 19 * 2^10 + hd digits for hd from 545
// (nd = 20,001, just past the leaf) to 19 * 2^10 in steps of 19, one case
// per high-half word count, so every (hn, pl) pair the multiplication and
// its scratch sizing (ParseTmp, MulScratch) see at that split is run.
func TestSTDBIG_ParseSplitSweep(t *testing.T) {
	g := newBigGen(12)
	var c bigCases
	const lo = 19 << 10
	for hd := 545; hd <= lo; hd += 19 {
		nd := lo + hd
		var b strings.Builder
		b.Grow(nd)
		b.WriteByte(byte('1' + g.r.IntN(9)))
		for b.Len() < nd {
			b.WriteByte(byte('0' + g.r.IntN(10)))
		}
		s := b.String()
		c.add("parse", []string{"10", fmt.Sprint(nd), s}, bigInt(bigMustParse(s, 10)))
	}
	bigRun(t, &c)
}

// TestSTDBIG_LehmerMatchesMathBig: Gcd and ModInverse (gcd.mk, Lehmer's
// algorithm) on the inputs that stress it: consecutive Fibonacci numbers
// (every quotient 1, the longest remainder sequence), operands with a
// large common factor, equal operands, a multiple of the other, operands
// one word apart in length and many words apart, top words that are
// all ones or a single bit (where the simulation's top-word extraction
// shifts by 0 or 63), and random sizes from 1 to 300 words, signs mixed.
func TestSTDBIG_LehmerMatchesMathBig(t *testing.T) {
	g := newBigGen(14)
	var c bigCases
	gcd := func(x, y *big.Int) {
		want := new(big.Int).GCD(nil, nil, new(big.Int).Abs(x), new(big.Int).Abs(y))
		c.add("gcd", []string{bigInt(x), bigInt(y)}, bigInt(want))
		c.add("gcd", []string{bigInt(y), bigInt(x)}, bigInt(want))
	}
	inv := func(x, m *big.Int) {
		if m.Sign() == 0 {
			return
		}
		want := "none"
		if r := new(big.Int).ModInverse(x, new(big.Int).Abs(m)); r != nil && new(big.Int).GCD(nil, nil, new(big.Int).Abs(x), new(big.Int).Abs(m)).Cmp(big.NewInt(1)) == 0 {
			want = bigInt(r)
		}
		if new(big.Int).Abs(m).Cmp(big.NewInt(1)) == 0 {
			want = "+"
		}
		c.add("modinv", []string{bigInt(x), bigInt(m)}, want)
	}
	// Fibonacci pairs up to about 300 words.
	f0, f1 := big.NewInt(0), big.NewInt(1)
	for i := 0; i < 27000; i++ {
		f0, f1 = f1, new(big.Int).Add(f0, f1)
		if i%997 == 0 || i == 26999 {
			gcd(f1, f0)
			inv(f0, f1)
			inv(f1, f0)
		}
	}
	for _, n := range []int{1, 2, 3, 5, 8, 31, 64, 65, 130, 300} {
		for k := 0; k < 6; k++ {
			x, y := g.int(n), g.int(n-g.r.IntN(n)/2)
			gcd(x, y)
			inv(x, y)
			inv(y, x)
			// A large common factor.
			f := g.nat(1 + n/2)
			xf, yf := new(big.Int).Mul(x, f), new(big.Int).Mul(y, f)
			gcd(xf, yf)
			inv(xf, yf)
			// Equal, and a multiple.
			gcd(x, x)
			gcd(new(big.Int).Mul(x, g.nat(2)), x)
			// Lengths far apart.
			gcd(g.nat(n+40), g.nat(1+n/8))
			inv(g.nat(n+40), g.nat(1+n/8))
		}
		// Top words all ones, or one bit, in both operands.
		ones := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(64*n)), big.NewInt(1))
		top := new(big.Int).Lsh(big.NewInt(1), uint(64*n-1))
		for _, x := range []*big.Int{ones, top, new(big.Int).Add(top, big.NewInt(1))} {
			y := g.nat(n)
			gcd(x, y)
			inv(y, x)
			inv(x, y)
		}
	}
	// Odd modulus with a prime: every non-zero residue has an inverse.
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 521), big.NewInt(1))
	for k := 0; k < 30; k++ {
		inv(g.int(1+g.r.IntN(12)), p)
	}
	bigRun(t, &c)
}

// TestSTDBIG_ModPowEvenCRT: ModPow modulo m = m1 2^k (modpow.mk's
// Chinese-remainder path) for k = 1, 63, 64, 65, 128 and 130 (zero low
// words, a mask of 1, 63 or 64 bits), m1 of 1 to 40 words (and m1 = 1:
// m a power of two), x even, odd, negative and longer than m, and
// exponents of 1 bit to 20 words.
func TestSTDBIG_ModPowEvenCRT(t *testing.T) {
	g := newBigGen(15)
	var c bigCases
	for _, k := range []int{1, 63, 64, 65, 128, 130} {
		for _, n1 := range []int{0, 1, 2, 7, 40} {
			m1 := big.NewInt(1)
			if n1 > 0 {
				m1 = g.nat(n1)
				m1.SetBit(m1, 0, 1)
			}
			m := new(big.Int).Lsh(m1, uint(k))
			for j := 0; j < 4; j++ {
				x := g.int(1 + g.r.IntN(2*m.BitLen()/64+2))
				if j == 0 {
					x.Lsh(x, 3)
				}
				e := g.nat(1 + g.r.IntN(20))
				if j == 1 {
					e = big.NewInt(1)
				}
				want := new(big.Int).Exp(new(big.Int).Mod(x, m), e, m)
				c.add("modpow", []string{bigInt(x), bigInt(e), bigInt(m)}, bigInt(want))
			}
		}
	}
	bigRun(t, &c)
}
