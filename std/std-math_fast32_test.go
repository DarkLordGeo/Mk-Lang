// Why: SinCosFast32 in std/math/fast32.mk needs constants and a table that
// are exact to the last bit, so they are generated, not typed.
// What: the generator of the "fast32" block, from math/big at mathGenPrec bits.
// How: TestSTDMATH_Tables writes and checks it with the blocks of
// std-math_tables_test.go.

package std

import (
	"fmt"
	"math"
)

// mathGenFast32 adds the "fast32" block to out and appends to bad each
// property fast32.mk relies on that the constants break.
func mathGenFast32(p uint, out map[string]string, bad *[]string) {
	text := ""
	add := func(s string) { text += s }
	pi := bigPiAt(p)

	// SinCosFast32: x = n pi/32 + r.
	pio32 := bnp(p).Quo(pi, bfp(32, p))
	add(mathConst("SinInvPio32", mathRN(bnp(p).Quo(bfp(32, p), pi)), "SinCosFast32: 32 / pi."))
	p1 := mathRN(mathRound(pio32, 29-pio32.MantExp(nil)))
	rest := bnp(p).Sub(pio32, bfp(p1, p))
	p2 := mathRN(mathRound(rest, 29-rest.MantExp(nil)))
	rest.Sub(rest, bfp(p2, p))
	p3 := mathRN(rest)
	for _, c := range []float64{p1, p2} {
		if mathSigBits(c) > 29 {
			*bad = append(*bad, fmt.Sprintf("fast32: a head of pi/32 has %d bits, want <= 29 (n * head exact for |n| < 2^24)", mathSigBits(c)))
		}
	}
	add(mathConst("Pio32P1", p1, "pi/32 to 29 bits, so n * Pio32P1 is exact for |n| < 2^24."))
	add(mathConst("Pio32P2", p2, "The next 29 bits of pi/32."))
	add(mathConst("Pio32P3", p3, "pi/32 less Pio32P1 and Pio32P2."))
	coef := func(name string, n int, sign float64, what string) {
		f := 1.0
		for k := 2; k <= n; k++ {
			f *= float64(k)
		}
		add(mathConst(name, mathRN(bnp(p).Quo(bfp(sign, p), bfp(f, p))), what))
	}
	coef("SinF1", 3, -1, "sin r = r + r^3 (SinF1 + r^2 (SinF2 + r^2 SinF3)), Taylor's series, truncation below 2^-57 for |r| <= pi/64; SinF1 = -1/3!.")
	coef("SinF2", 5, 1, "1/5!.")
	coef("SinF3", 7, -1, "-1/7!.")
	coef("CosF1", 2, -1, "cos r - 1 = r^2 (CosF1 + r^2 (CosF2 + r^2 CosF3)), Taylor's series, truncation below 2^-50 for |r| <= pi/64; CosF1 = -1/2!.")
	coef("CosF2", 4, 1, "1/4!.")
	coef("CosF3", 6, -1, "-1/6!.")
	words := make([]uint64, 64)
	for j := 0; j < 64; j++ {
		var s float64
		switch j {
		case 0, 32:
			s = 0
		case 16:
			s = 1
		case 48:
			s = -1
		default:
			a := bnp(p).Mul(pio32, bfp(float64(j), p))
			sv, _ := bigSinCosP(a, p)
			s = mathRN(sv)
		}
		words[j] = math.Float64bits(s)
	}
	add("// sin(j pi/32) for j in 0..64, exactly 0 and +-1 where it is; cos(j pi/32)\n// is row (j + 16) mod 64.\n")
	add(mathTable("SinPi32T", words, 4))
	out["fast32"] = text
}
