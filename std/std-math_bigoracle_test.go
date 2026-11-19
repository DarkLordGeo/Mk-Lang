// Why: std.math's accuracy tests need correctly rounded reference values,
// and math/big has no transcendental functions.
// What: the high-precision oracle (mathBigPrec bits for f32, mathTruePrec
// for f64 error), checked by TestSTDMATH_BigOracleMatchesEvidence.
// How: exp by halving and Taylor, log by Halley on exp, sin and cos after a
// 2 pi reduction (pi by Machin), atan by halving, the rest by identities.

package std

import (
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// / mathBigPrec is the working precision of the f32 oracle: 512 bits, so
// that after the halvings and squarings and the worst cancellation of an
// f32 argument (expm1 and sinh of 2^-149) more than 256 correct bits
// remain. The f64 error measurement (std-math_ulp_test.go) works at
// mathTruePrec instead.
const mathBigPrec = 512

// bfp is x at p bits, exactly.
func bfp(x float64, p uint) *big.Float {
	return new(big.Float).SetPrec(p).SetFloat64(x)
}

// bnp is a zero at p bits.
func bnp(p uint) *big.Float { return new(big.Float).SetPrec(p) }

// bigAtanInv is atan(1/n) at prec bits by its series.
func bigAtanInv(n int64, prec uint) *big.Float {
	x := new(big.Float).SetPrec(prec).Quo(new(big.Float).SetPrec(prec).SetInt64(1), new(big.Float).SetPrec(prec).SetInt64(n))
	x2 := new(big.Float).SetPrec(prec).Mul(x, x)
	sum := new(big.Float).SetPrec(prec).Set(x)
	term := new(big.Float).SetPrec(prec).Set(x)
	eps := new(big.Float).SetPrec(prec).SetMantExp(big.NewFloat(1), -int(prec)-8)
	for k := int64(1); ; k++ {
		term.Mul(term, x2)
		t := new(big.Float).SetPrec(prec).Quo(term, new(big.Float).SetPrec(prec).SetInt64(2*k+1))
		if k%2 == 1 {
			sum.Sub(sum, t)
		} else {
			sum.Add(sum, t)
		}
		if t.Cmp(eps) < 0 {
			return sum
		}
	}
}

var bigPiCache sync.Map // precision in bits -> *big.Float

// bigPiAt is pi at p bits: 16 atan(1/5) - 4 atan(1/239) (Machin),
// computed 16 bits wider and cached per precision. The value is shared:
// callers never change it.
func bigPiAt(p uint) *big.Float {
	if v, ok := bigPiCache.Load(p); ok {
		return v.(*big.Float)
	}
	q := p + 16
	a := bigAtanInv(5, q)
	b := bigAtanInv(239, q)
	a.Mul(a, new(big.Float).SetPrec(q).SetInt64(16))
	b.Mul(b, new(big.Float).SetPrec(q).SetInt64(4))
	pi := new(big.Float).SetPrec(p).Sub(a, b)
	v, _ := bigPiCache.LoadOrStore(p, pi)
	return v.(*big.Float)
}

var bigLn2Cache sync.Map // precision in bits -> *big.Float

// bigLn2At is ln 2 at p bits, cached per precision. The value is shared:
// callers never change it.
func bigLn2At(p uint) *big.Float {
	if v, ok := bigLn2Cache.Load(p); ok {
		return v.(*big.Float)
	}
	v, _ := bigLn2Cache.LoadOrStore(p, bigLogP(bfp(2, p), p))
	return v.(*big.Float)
}

// bigExpP is e^x at p bits for |x| <= about 1100: halvings to |x| <=
// 1/256, Taylor's series, squarings, 64 guard bits.
func bigExpP(x *big.Float, p uint) *big.Float {
	w := p + 64
	y := new(big.Float).SetPrec(w).Set(x)
	m := 0
	lim := big.NewFloat(1.0 / 256)
	for new(big.Float).Abs(y).Cmp(lim) > 0 {
		y.SetMantExp(y, -1)
		m++
	}
	sum := new(big.Float).SetPrec(w).SetInt64(1)
	term := new(big.Float).SetPrec(w).SetInt64(1)
	eps := new(big.Float).SetPrec(w).SetMantExp(big.NewFloat(1), -int(w)-8)
	for k := int64(1); ; k++ {
		term.Mul(term, y)
		term.Quo(term, new(big.Float).SetPrec(w).SetInt64(k))
		sum.Add(sum, term)
		if new(big.Float).Abs(term).Cmp(eps) < 0 {
			break
		}
	}
	for ; m > 0; m-- {
		sum.Mul(sum, sum)
	}
	return new(big.Float).SetPrec(p).Set(sum)
}

// bigLogP is ln x at p bits for x > 0, by Halley's iteration y += 2 (x -
// e^y) / (x + e^y) from an f64 estimate (cubic: 53, 159, 477, 1431, 4293
// bits). The estimate is log(m) + e ln 2 for x = m 2^e, 0.5 <= m < 1:
// Go 1.27's math.Log is wrong at a subnormal argument on amd64
// (math.Log(5e-324) is -709.09, not -744.44), so it never sees one.
func bigLogP(x *big.Float, p uint) *big.Float {
	m := new(big.Float)
	e := x.MantExp(m)
	mf, _ := m.Float64()
	y := bfp(math.Log(mf)+float64(e)*math.Ln2, p)
	iters := 4
	if p > 1400 {
		iters = 5
	}
	for i := 0; i < iters; i++ {
		ey := bigExpP(y, p)
		num := bnp(p).Sub(x, ey)
		den := bnp(p).Add(x, ey)
		num.Quo(num, den)
		num.Mul(num, big.NewFloat(2))
		y.Add(y, num)
	}
	return y
}

// bigSinCosP is sin x and cos x at p bits, x first reduced by 2 pi held to
// p + 128 bits beyond x's exponent, so the reduction is exact to p bits for
// every finite f64.
func bigSinCosP(x *big.Float, p uint) (*big.Float, *big.Float) {
	rp := p + 128
	if e := x.MantExp(nil); e > 0 {
		rp += uint(e)
	}
	pi := bigPiAt(rp)
	twoPi := new(big.Float).SetPrec(rp).Mul(pi, big.NewFloat(2))
	q := new(big.Float).SetPrec(rp).Quo(new(big.Float).SetPrec(rp).Set(x), twoPi)
	k, _ := new(big.Float).SetPrec(rp).Add(q, big.NewFloat(0.5)).Int(nil)
	if q.Sign() < 0 {
		k, _ = new(big.Float).SetPrec(rp).Sub(q, big.NewFloat(0.5)).Int(nil)
	}
	r := new(big.Float).SetPrec(rp).Mul(new(big.Float).SetPrec(rp).SetInt(k), twoPi)
	r.Sub(new(big.Float).SetPrec(rp).Set(x), r)
	pp := p + 64
	y := new(big.Float).SetPrec(pp).Set(r)
	y2 := new(big.Float).SetPrec(pp).Mul(y, y)
	sin := new(big.Float).SetPrec(pp).Set(y)
	cos := new(big.Float).SetPrec(pp).SetInt64(1)
	ts := new(big.Float).SetPrec(pp).Set(y)
	tc := new(big.Float).SetPrec(pp).SetInt64(1)
	eps := new(big.Float).SetPrec(pp).SetMantExp(big.NewFloat(1), -int(pp)-8)
	for k := int64(1); ; k++ {
		ts.Mul(ts, y2)
		ts.Quo(ts, new(big.Float).SetPrec(pp).SetInt64((2*k)*(2*k+1)))
		ts.Neg(ts)
		sin.Add(sin, ts)
		tc.Mul(tc, y2)
		tc.Quo(tc, new(big.Float).SetPrec(pp).SetInt64((2*k-1)*(2*k)))
		tc.Neg(tc)
		cos.Add(cos, tc)
		if new(big.Float).Abs(ts).Cmp(eps) < 0 && new(big.Float).Abs(tc).Cmp(eps) < 0 {
			break
		}
	}
	return sin, cos
}

// bigAtanP is atan x at p bits: pi/2 - atan(1/x) above 1, three halvings
// atan(x) = 2 atan(x / (1 + sqrt(1 + x^2))), then the series.
func bigAtanP(x *big.Float, p uint) *big.Float {
	pp := p + 64
	neg := x.Sign() < 0
	y := new(big.Float).SetPrec(pp).Abs(x)
	inv := false
	if y.Cmp(big.NewFloat(1)) > 0 {
		y.Quo(new(big.Float).SetPrec(pp).SetInt64(1), y)
		inv = true
	}
	for i := 0; i < 3; i++ {
		t := new(big.Float).SetPrec(pp).Mul(y, y)
		t.Add(t, big.NewFloat(1))
		t.Sqrt(t)
		t.Add(t, big.NewFloat(1))
		y.Quo(y, t)
	}
	y2 := new(big.Float).SetPrec(pp).Mul(y, y)
	sum := new(big.Float).SetPrec(pp).Set(y)
	term := new(big.Float).SetPrec(pp).Set(y)
	eps := new(big.Float).SetPrec(pp).SetMantExp(big.NewFloat(1), -int(pp)-8)
	for k := int64(1); ; k++ {
		term.Mul(term, y2)
		term.Neg(term)
		t := new(big.Float).SetPrec(pp).Quo(term, new(big.Float).SetPrec(pp).SetInt64(2*k+1))
		sum.Add(sum, t)
		if new(big.Float).Abs(t).Cmp(eps) < 0 {
			break
		}
	}
	sum.Mul(sum, big.NewFloat(8))
	if inv {
		h := new(big.Float).SetPrec(pp).Quo(bigPiAt(pp), big.NewFloat(2))
		sum.Sub(h, sum)
	}
	if neg {
		sum.Neg(sum)
	}
	return sum
}

// mathBigEval is fn(x) at mathBigPrec bits for a finite x inside fn's
// domain, away from the special cases the f64 path settles exactly.
func mathBigEval(fn string, x float64) *big.Float {
	return mathBigEvalP(fn, []float64{x}, mathBigPrec)
}

// mathBigEvalP is fn(args...) at p bits, for a finite result inside fn's
// domain, away from the special cases the f64 path settles exactly. The
// two-argument functions take std.math's argument order: Atan2(y, x),
// Hypot(x, y), Pow(x, y). At p = mathTruePrec every intermediate
// cancellation of an f64 argument (1 + x and e^x - 1 at x = 2^-1074, the
// reduction of 2^1024 by 2 pi) leaves several hundred correct bits.
func mathBigEvalP(fn string, args []float64, p uint) *big.Float {
	x := args[0]
	X := bfp(x, p)
	one := big.NewFloat(1)
	n := func() *big.Float { return bnp(p) }
	switch fn {
	case "Exp":
		return bigExpP(X, p)
	case "Exp2":
		return bigExpP(n().Mul(X, bigLn2At(p+64)), p)
	case "Expm1":
		return n().Sub(bigExpP(X, p), one)
	case "Log":
		return bigLogP(X, p)
	case "Log2":
		return n().Quo(bigLogP(X, p), bigLn2At(p))
	case "Log10":
		return n().Quo(bigLogP(X, p), bigLogP(bfp(10, p), p))
	case "Log1p":
		return bigLogP(n().Add(X, one), p)
	case "Sin":
		s, _ := bigSinCosP(X, p)
		return s
	case "Cos":
		_, c := bigSinCosP(X, p)
		return c
	case "Tan":
		s, c := bigSinCosP(X, p)
		return n().Quo(s, c)
	case "Atan":
		return bigAtanP(X, p)
	case "Asin":
		d := n().Sqrt(n().Sub(one, n().Mul(X, X)))
		return bigAtanP(n().Quo(X, d), p)
	case "Acos":
		d := n().Sqrt(n().Sub(one, n().Mul(X, X)))
		h := n().Quo(bigPiAt(p+64), big.NewFloat(2))
		return n().Sub(h, bigAtanP(n().Quo(X, d), p))
	case "Sinh", "Cosh", "Tanh":
		e := bigExpP(X, p)
		ie := n().Quo(one, e)
		s := n().Sub(e, ie)
		c := n().Add(e, ie)
		switch fn {
		case "Sinh":
			return s.Quo(s, big.NewFloat(2))
		case "Cosh":
			return c.Quo(c, big.NewFloat(2))
		}
		return s.Quo(s, c)
	case "Asinh":
		a := n().Abs(X)
		r := bigLogP(n().Add(a, n().Sqrt(n().Add(n().Mul(a, a), one))), p)
		if x < 0 {
			r.Neg(r)
		}
		return r
	case "Acosh":
		return bigLogP(n().Add(X, n().Sqrt(n().Sub(n().Mul(X, X), one))), p)
	case "Atanh":
		q := n().Quo(n().Add(one, X), n().Sub(one, X))
		r := bigLogP(q, p)
		return r.Quo(r, big.NewFloat(2))
	case "Cbrt":
		// The estimate cbrt(m 2^(e mod 3)) 2^(e div 3) for |x| = m 2^e, so a
		// subnormal x needs no f64 cube root of its own.
		a := math.Abs(x)
		fr, e := math.Frexp(a)
		for e%3 != 0 {
			fr, e = fr*2, e-1
		}
		y := bfp(math.Ldexp(math.Cbrt(fr), e/3), p)
		A := bfp(a, p)
		for i := 0; i < 7; i++ {
			// y -= (y^3 - a) / (3 y^2)
			y2 := n().Mul(y, y)
			num := n().Sub(n().Mul(y2, y), A)
			den := n().Mul(y2, big.NewFloat(3))
			y.Sub(y, num.Quo(num, den))
		}
		if x < 0 {
			y.Neg(y)
		}
		return y
	case "Sqrt":
		return n().Sqrt(X)
	case "Hypot":
		Y := bfp(args[1], p)
		s := n().Add(n().Mul(X, X), n().Mul(Y, Y))
		return s.Sqrt(s)
	case "Atan2":
		// args are (y, x).
		yv, xv := X, bfp(args[1], p)
		if xv.Sign() == 0 {
			h := n().Quo(bigPiAt(p+64), big.NewFloat(2))
			if yv.Signbit() {
				h.Neg(h)
			}
			return h
		}
		r := bigAtanP(n().Quo(yv, xv), p)
		if xv.Sign() < 0 {
			if yv.Signbit() {
				r.Sub(r, bigPiAt(p+64))
			} else {
				r.Add(r, bigPiAt(p+64))
			}
		}
		return r
	case "Pow":
		y := args[1]
		a := n().Abs(X)
		r := bigExpP(n().Mul(bfp(y, p+64), bigLogP(a, p+64)), p)
		if x < 0 && math.Mod(y, 2) != 0 {
			r.Neg(r)
		}
		return r
	}
	panic("mathBigEvalP: no " + fn)
}

// mathGo64 is Go's f64 function for fn, the first estimate of the oracle,
// and whether fn is exact in f32 (so no midpoint check is needed).
func mathGo64(fn string, x float64) (float64, bool) {
	switch fn {
	case "Sqrt":
		return math.Sqrt(x), true // correctly rounded, then to f32 exactly
	case "Floor":
		return math.Floor(x), true
	case "Ceil":
		return math.Ceil(x), true
	case "RoundToZero":
		return math.Trunc(x), true
	case "Round":
		return math.Round(x), true
	case "RoundEven":
		return math.RoundToEven(x), true
	case "Fract":
		if math.IsInf(x, 0) {
			return math.NaN(), true
		}
		return math.Copysign(x-math.Trunc(x), x), true
	case "Exp":
		return math.Exp(x), false
	case "Exp2":
		return math.Exp2(x), false
	case "Expm1":
		return math.Expm1(x), false
	case "Log":
		return math.Log(x), false
	case "Log2":
		return math.Log2(x), false
	case "Log10":
		return math.Log10(x), false
	case "Log1p":
		return math.Log1p(x), false
	case "Sin":
		return math.Sin(x), false
	case "Cos":
		return math.Cos(x), false
	case "Tan":
		return math.Tan(x), false
	case "Asin":
		return math.Asin(x), false
	case "Acos":
		return math.Acos(x), false
	case "Atan":
		return math.Atan(x), false
	case "Sinh":
		return math.Sinh(x), false
	case "Cosh":
		return math.Cosh(x), false
	case "Tanh":
		return math.Tanh(x), false
	case "Asinh":
		return math.Asinh(x), false
	case "Acosh":
		return math.Acosh(x), false
	case "Atanh":
		return math.Atanh(x), false
	case "Cbrt":
		return math.Cbrt(x), false
	}
	panic("mathGo64: no " + fn)
}

// mathBand is how close, in f32 ulps, Go's f64 value may come to an f32
// rounding boundary before the oracle recomputes it at 512 bits: 2^-20 of
// an f32 ulp is 2^9 f64 ulps, far beyond Go's f64 error.
const mathBand = 1.0 / (1 << 20)

// mathCR32 is the correctly rounded f32 result of fn at the f32 input x,
// as bits, NaN as 0x7FC00000.
func mathCR32(fn string, xb uint32) uint32 {
	x := float64(math.Float32frombits(xb))
	v, exact := mathGo64(fn, x)
	if v != v {
		return 0x7FC00000
	}
	if fn == "Exp2" && x == math.Trunc(x) {
		exact = true // 2^n is exact in f64, so its one rounding to f32 is right
	}
	r := float32(v)
	if exact || math.IsInf(v, 0) || v == 0 || float64(r) == v {
		return math.Float32bits(r)
	}
	// The bracketing f32s, the midpoint between them, and the distance.
	lo, hi := r, r
	if float64(r) < v {
		hi = math.Nextafter32(r, float32(math.Inf(1)))
	} else {
		lo = math.Nextafter32(r, float32(math.Inf(-1)))
	}
	ulp := float64(hi) - float64(lo)
	if math.IsInf(float64(hi), 0) || math.IsInf(float64(lo), 0) {
		ulp = math.Ldexp(1, 104) // f32 max's ulp, for the overflow boundary
	}
	mid := float64(lo) + ulp/2
	if math.Abs(v-mid) > ulp*mathBand {
		return math.Float32bits(r)
	}
	y := mathBigEval(fn, x)
	f, _ := y.Float32()
	return math.Float32bits(f)
}

// TestSTDMATH_BigOracleMatchesEvidence checks the oracle against the f32
// tables: mathCR32 on every row, and the 512-bit evaluation alone on every
// 16th finite row.
func TestSTDMATH_BigOracleMatchesEvidence(t *testing.T) {
	dir := mathEvidence()
	if dir == "" {
		t.Skip("Mk-evidence/math not found (set MK_EVIDENCE)")
	}
	for _, f := range mathFns {
		if f.kind != "f132" {
			continue
		}
		fn := f.mk[:len(f.mk)-3]
		t.Run(fn, func(t *testing.T) {
			t.Parallel()
			bad := 0
			for i, r := range readMathTable(t, dir, f) {
				xb, want := uint32(r.in[0]), uint32(r.want[0])
				if got := mathCR32(fn, xb); got != want {
					bad++
					if bad <= 5 {
						t.Errorf("%s(%08x): oracle %08x, table %08x", fn, xb, got, want)
					}
				}
				x := float64(math.Float32frombits(xb))
				_, exact := mathGo64(fn, x)
				if exact || i%16 != 0 || want&0x7F800000 == 0x7F800000 || want&0x7FFFFFFF == 0 {
					continue
				}
				v, _ := mathGo64(fn, x)
				if v != v || math.IsInf(v, 0) || float64(float32(v)) == v {
					continue // a special case or an exact result: no series needed
				}
				y, _ := mathBigEval(fn, x).Float32()
				if math.Float32bits(y) != want {
					bad++
					if bad <= 5 {
						t.Errorf("%s(%08x): 512-bit value rounds to %08x, table %08x", fn, xb, math.Float32bits(y), want)
					}
				}
			}
			if bad > 5 {
				t.Errorf("%s: %d mismatches in all", fn, bad)
			}
		})
	}
}

// TestSTDMATH_ExceptionsAreCorrect checks every entry of trans32.mk's
// exception tables against the 512-bit evaluation alone, so an entry the
// oracle's f64 path got wrong cannot hide behind the pinned digests that
// the same oracle produced.
func TestSTDMATH_ExceptionsAreCorrect(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("math", "trans32.mk"))
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`const (\w+)Exceptions: \[\d+\]u64 = \[([^\]]*)\]`)
	tables := re.FindAllStringSubmatch(string(src), -1)
	if len(tables) == 0 {
		t.Fatal("no exception tables found in math/trans32.mk")
	}
	n := 0
	for _, m := range tables {
		fn := m[1]
		for _, e := range strings.Split(m[2], ",") {
			w, err := strconv.ParseUint(strings.TrimPrefix(strings.TrimSpace(e), "0x"), 16, 64)
			if err != nil {
				t.Fatalf("%s: entry %q: %v", fn, e, err)
			}
			xb, want := uint32(w>>32), uint32(w)
			y, _ := mathBigEval(fn, float64(math.Float32frombits(xb))).Float32()
			if got := math.Float32bits(y); got != want {
				t.Errorf("%sF32(%08x): table %08x, 512-bit value rounds to %08x", fn, xb, want, got)
			}
			n++
		}
	}
	t.Logf("%d exception entries in %d tables agree with the 512-bit evaluation", n, len(tables))
}
