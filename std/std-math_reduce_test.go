// Why: a fault in the Payne-Hanek reduction's word arithmetic
// (std/math/reduce.mk) shows only where the remainder is tiny, and random
// table rows almost never come within 2^-40 of a multiple of pi/2.
// What: a test on the f64 values closest to a multiple of pi/2.
// How: these inputs come within about 2^-55 to 2^-61 of a multiple.

package std

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mathReduceWorst is the f64 closest to a multiple of pi/2 over the whole
// range (6381956970095103 2^797, Muller's "Elementary Functions", table
// 11.1): mathReduceInputs must find it in its binade, which checks the
// search, and it is also run on its own.
const mathReduceWorst = 0x7506AC5B262CA1FF

// mathReduceInputs is, for each binade [2^e, 2^(e+1)) with 20 <= e <=
// 1023, the f64 x = M 2^(e-52) whose x / (pi/2) is nearest an integer,
// and its negative. With alpha the fraction of 2^(e-52) 2/pi (from 2/pi at
// e + 320 bits), the candidates for M in [2^52, 2^53) are the convergents'
// and semiconvergents' denominators of alpha's continued fraction and
// their smallest multiples in range; the one with the least distance of M
// alpha to an integer wins.
func mathReduceInputs() []float64 {
	var out []float64
	for e := 20; e <= 1023; e++ {
		x := math.Ldexp(float64(mathReduceBest(e)), e-52)
		out = append(out, x, -x)
	}
	return out
}

// mathReduceBest is the M of binade e (mathReduceInputs).
func mathReduceBest(e int) uint64 {
	prec := uint(e + 320)
	twoOverPi := new(big.Float).SetPrec(prec).Quo(new(big.Float).SetPrec(prec).SetInt64(2), bigPiAt(prec))
	beta := new(big.Float).SetPrec(prec).SetMantExp(twoOverPi, e-52)
	ip, _ := beta.Int(nil)
	alpha := new(big.Float).SetPrec(prec).Sub(beta, new(big.Float).SetPrec(prec).SetInt(ip))
	ar, _ := alpha.Rat(nil)
	dist := func(m uint64) *big.Float {
		v := new(big.Float).SetPrec(prec).Mul(alpha, new(big.Float).SetPrec(prec).SetUint64(m))
		vi, _ := v.Int(nil)
		f := new(big.Float).SetPrec(prec).Sub(v, new(big.Float).SetPrec(prec).SetInt(vi))
		g := new(big.Float).SetPrec(prec).Sub(big.NewFloat(1), f)
		if g.Cmp(f) < 0 {
			return g
		}
		return f
	}
	const lo, hi = uint64(1) << 52, uint64(1) << 53
	var cands []uint64
	addq := func(q uint64) {
		if q == 0 || q >= hi {
			return
		}
		k := (lo + q - 1) / q
		for j := uint64(0); j < 3; j++ {
			if m := (k + j) * q; m >= lo && m < hi {
				cands = append(cands, m)
			}
		}
	}
	// Continued fraction of alpha: q_{-1} = 0, q_0 = 1.
	qPrev, q := big.NewInt(0), big.NewInt(1)
	rem := new(big.Rat).Set(ar)
	for range 200 {
		if rem.Sign() == 0 {
			break
		}
		inv := new(big.Rat).Inv(rem)
		a := new(big.Int).Quo(inv.Num(), inv.Denom())
		rem.Sub(inv, new(big.Rat).SetInt(a))
		// Semiconvergents q_{n-1} + j q_n, j = 1 .. a (the last is q_{n+1}).
		for j := int64(1); j <= a.Int64() && j <= 4096; j++ {
			s := new(big.Int).Add(qPrev, new(big.Int).Mul(big.NewInt(j), q))
			if !s.IsUint64() || s.Uint64() >= hi {
				break
			}
			addq(s.Uint64())
		}
		next := new(big.Int).Add(qPrev, new(big.Int).Mul(a, q))
		qPrev, q = q, next
		if !q.IsUint64() || q.Uint64() >= hi {
			break
		}
		addq(q.Uint64())
	}
	best, bestD := uint64(0), (*big.Float)(nil)
	for _, m := range cands {
		if d := dist(m); bestD == nil || d.Cmp(bestD) < 0 {
			best, bestD = m, d
		}
	}
	return best
}

// mathReducePin pins TestSTDMATH_Reduction's measurement per function
// (release, x86_64-windows), which each doc line states.
var mathReducePin = map[string]mathTrue{
	"Sin": {2010, 0, "0.500"},
	"Cos": {2010, 0, "0.500"},
	"Tan": {2010, 0, "0.500"},
}

// TestSTDMATH_Reduction runs Sin, Cos and Tan, release, over
// mathReduceInputs and mathReduceWorst (and its negative), requires every
// result within 1 ulp of the 1,664-bit value and the explicit worst case
// correctly rounded, and pins the largest error.
func TestSTDMATH_Reduction(t *testing.T) {
	if got := math.Float64bits(math.Ldexp(float64(mathReduceBest(849)), 849-52)); got != mathReduceWorst {
		t.Fatalf("binade 2^849: the search picks %016x, not the known worst case %016x", got, uint64(mathReduceWorst))
	}
	in := mathReduceInputs()
	worst := math.Float64frombits(mathReduceWorst)
	in = append(in, worst, -worst)
	type ref struct{ sin, cos, tan *big.Float }
	refs := make([]ref, len(in))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, x := range in {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s, c := bigSinCosP(bfp(x, mathTruePrec), mathTruePrec)
			tn := new(big.Float).SetPrec(mathTruePrec).Quo(s, c)
			refs[i] = ref{s, c, tn}
		}()
	}
	wg.Wait()
	exes := mathBuild(t)
	var buf []byte
	for _, x := range in {
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(x))
	}
	file := filepath.Join(t.TempDir(), "reduce.bin")
	if err := os.WriteFile(file, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	docs := mathDocs(t)
	var report []string
	for _, fn := range []string{"Sin", "Cos", "Tan"} {
		res := runNative(t, exes["release"], "", fn, file)
		if res.code != 0 {
			t.Fatalf("%s: exit %d\n%s", fn, res.code, res.stderr)
		}
		lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
		if len(lines) != len(in) {
			t.Fatalf("%s: %d results for %d inputs", fn, len(lines), len(in))
		}
		var max float64
		var maxAt float64
		off := 0
		for i, x := range in {
			b, err := strconv.ParseUint(strings.TrimSpace(lines[i]), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			exact := map[string]*big.Float{"Sin": refs[i].sin, "Cos": refs[i].cos, "Tan": refs[i].tan}[fn]
			cr, _ := exact.Float64()
			got := math.Float64frombits(b)
			if b != math.Float64bits(cr) {
				off++
			}
			e := mathULPError(got, exact)
			if e > max {
				max, maxAt = e, x
			}
			if e >= 1 {
				t.Errorf("%s(%016x = %g) = %016x, %.3f ulp from the true value: not faithfully rounded", fn, math.Float64bits(x), x, b, e)
			}
			if i >= len(in)-2 && b != math.Float64bits(cr) {
				t.Errorf("%s(%016x), the worst case for the reduction, = %016x, correctly rounded %016x", fn, math.Float64bits(x), b, math.Float64bits(cr))
			}
		}
		ulp := mathCeilMilli(max)
		report = append(report, fmt.Sprintf("%q: {%d, %d, %q}, // at %g", fn, len(in), off, ulp, maxAt))
		if pin := mathReducePin[fn]; pin.rows != len(in) || pin.off != off || pin.ulp != ulp {
			t.Errorf("%s: measured %d inputs, %d off, largest %s ulp; pinned %+v", fn, len(in), off, ulp, pin)
		}
		want := fmt.Sprintf("over the %s inputs nearest a multiple of pi/2 of TestSTDMATH_Reduction, largest error %s ulp", mathThousands(len(in)), ulp)
		if !strings.Contains(strings.ToLower(docs[fn]), strings.ToLower(want)) {
			t.Errorf("%s: the doc comment must say %q; it reads:\n  %s", fn, want, docs[fn])
		}
	}
	t.Logf("Sin, Cos and Tan near multiples of pi/2 (release):\n  %s", strings.Join(report, "\n  "))
}
