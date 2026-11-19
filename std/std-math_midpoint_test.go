// Why: in the two-argument f32 forms (std/math/mid32.mk) rounding the f64
// result to f32 can be a tie; Narrow once broke it to even although the true
// value lay to one side, so PowF32 and HypotF32 misrounded.
// What: TestSTDMATH_Midpoint over mathMidCases, and TestSTDMATH_PowSearch.
// How: every expected result is computed exactly or at 512 bits, never
// copied; a true midpoint must stay a tie.

package std

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mathMidCase is one input of TestSTDMATH_Midpoint: the function and its
// arguments' bits in the function's order (Atan2F32 takes y, then x).
type mathMidCase struct {
	fn   string
	a, b uint32
}

// mathPowLandings are PowF32 inputs x = M 2^-23 (the bits here), y = n / 2
// on which Pow's f64 result is exactly an f32 midpoint: every one that
// TestSTDMATH_PowSearch (MK_MATH_SEARCH=1) found over M in [2^23, 2^24)
// and n in 5 .. 400, 50 of its 45,816 candidates (release,
// x86_64-windows). Some are exact ties (x^3 with 8 significant
// bits in x), the rest double roundings. Each is settled exactly at run
// time.
var mathPowLandings = [][2]uint32{
	{0x3F808000, 6},
	{0x3F818000, 6},
	{0x3F820000, 8},
	{0x3F828000, 6},
	{0x3F838000, 6},
	{0x3F848000, 6},
	{0x3F858000, 6},
	{0x3F860000, 8},
	{0x3F868000, 6},
	{0x3F878000, 6},
	{0x3F880000, 12},
	{0x3F888000, 6},
	{0x3F898000, 6},
	{0x3F8A0000, 8},
	{0x3F8A8000, 6},
	{0x3F8B8000, 6},
	{0x3F8C8000, 6},
	{0x3F8D8000, 6},
	{0x3F8E0000, 8},
	{0x3F8E8000, 6},
	{0x3F8F8000, 6},
	{0x3F908000, 6},
	{0x3F918000, 6},
	{0x3F920000, 8},
	{0x3F928000, 6},
	{0x3F938000, 6},
	{0x3F948000, 6},
	{0x3F958000, 6},
	{0x3F960000, 8},
	{0x3F968000, 6},
	{0x3F978000, 6},
	{0x3F988000, 6},
	{0x3F998000, 6},
	{0x3F9A8000, 6},
	{0x3F9B8000, 6},
	{0x3F9C8000, 6},
	{0x3F9D8000, 6},
	{0x3F9E8000, 6},
	{0x3F9F8000, 6},
	{0x3FA08000, 6},
	{0x3FB00000, 14},
	{0x3FB34573, 247}, // the first PowF32 counterexample found
	{0x3FC47BD8, 351}, // the second
	{0x3FC5E3BF, 370},
	{0x3FCE2BBD, 120},
	{0x3FCE5FA9, 229},
	{0x3FD0E078, 300},
	{0x3FE80000, 10},
	{0x3FF20000, 7},
	{0x3FF80000, 10},
}

// mathMidCases builds the inputs: the known PowF32 and HypotF32 counterexamples,
// the Pow landings, exact ties (an f32 midpoint that is the true value),
// HypotF32 inputs built to lie within a few f64 ulps of a midpoint, and
// Atan2F32's y / x case on midpoints.
func mathMidCases() []mathMidCase {
	f := math.Float32bits
	var cs []mathMidCase
	add := func(fn string, a, b float32) { cs = append(cs, mathMidCase{fn, f(a), f(b)}) }
	for _, l := range mathPowLandings {
		cs = append(cs, mathMidCase{"PowF32", l[0], f(float32(l[1]) / 2)})
	}
	// Exact ties: t^2 to the 1.5 is t^3, a 25-bit odd integer for odd t in
	// [257, 321]; 11^7 four ways; powers of two and 3^k scaled to the
	// subnormal midpoints.
	for t := 257; t <= 321; t += 2 {
		add("PowF32", float32(t*t), 1.5)
	}
	add("PowF32", 121, 3.5)
	add("PowF32", 11, 7)
	add("PowF32", -11, 7)
	add("PowF32", 14641, 1.75)
	add("PowF32", 0x1p-75, 2)
	add("PowF32", 3*0x1p-75, 2)
	add("PowF32", 0x1p-50, 3)
	add("PowF32", -3*0x1p-50, 3)
	add("PowF32", 2, -150)
	add("PowF32", 0.5, 150)
	add("PowF32", 0x1p-30, 5)
	cs = append(cs, mathMidCase{"HypotF32", 0x4B23C9BD, 0x48BF8A81}, mathMidCase{"HypotF32", 0x4B048D37, 0x49ADE3F7})
	// Exact HypotF32 ties: Pythagorean triples (m^2 - n^2, 2mn, m^2 + n^2)
	// with the hypotenuse a 25-bit odd integer and both legs below 2^24,
	// at three scales, the legs swapped and negated.
	ties := 0
	for m := 2900; m < 4200 && ties < 60; m += 7 {
		for n := 1; n < m && ties < 60; n += 2 * (1 + m%5) {
			if (m-n)%2 == 0 {
				continue
			}
			a, b, c := m*m-n*n, 2*m*n, m*m+n*n
			if c <= 1<<24 || c >= 1<<25 || a >= 1<<24 || b >= 1<<24 {
				continue
			}
			ties++
			for _, s := range []float32{1, 0x1p-100, 0x1p60} {
				add("HypotF32", float32(a)*s, float32(b)*s)
				add("HypotF32", -float32(b)*s, float32(a)*s)
			}
		}
	}
	// HypotF32 near midpoints: for a midpoint m = M 2^(e-24) (M odd, 25
	// bits), x the f32 t half-steps below it and y the f32 nearest
	// sqrt(m^2 - x^2) and its neighbours, so hypot(x, y) lies within about
	// 8 t f64 ulps of m; mathHypotLandCount of them land on it in f64.
	rng := rand.New(rand.NewPCG(0x5EED_3132, 0x2026_1112))
	for range 2000 {
		M := uint64(1)<<24 | rng.Uint64N(1<<24) | 1
		e := rng.IntN(120) - 60
		for _, t := range []uint64{1, 3} {
			x := float32(math.Ldexp(float64(M-t), e-24))
			bx := new(big.Float).SetPrec(1200).SetFloat64(float64(x))
			bm := new(big.Float).SetPrec(1200).SetMantExp(new(big.Float).SetUint64(M), e-24)
			bm.Mul(bm, bm)
			bm.Sub(bm, new(big.Float).SetPrec(1200).Mul(bx, bx))
			y0, _ := new(big.Float).SetPrec(1200).Sqrt(bm).Float32()
			for _, y := range []float32{math.Nextafter32(y0, 0), y0, math.Nextafter32(y0, 2*y0)} {
				if rng.IntN(2) == 0 {
					add("HypotF32", x, y)
				} else {
					add("HypotF32", -y, x)
				}
			}
		}
	}
	// Atan2F32 where Atan2 returns y / x (|y / x| < 2^-59) and y / x is an
	// odd multiple of 2^-150: atan lies just below it, so the tie rounds
	// toward zero.
	for j := range 16 {
		q := float64(2*j+1) * 0x1p-150
		for _, x := range []float32{0x1p20, 0x1p100} {
			y := float32(q * float64(x))
			add("Atan2F32", y, x)
			add("Atan2F32", -y, x)
		}
	}
	// A y / x that is not exact never lands on a midpoint for f32
	// arguments: with m = M 2^e (M 25 bits) and x = X 2^f (X 24 bits), y -
	// m x is a multiple of 2^(e+f) and m x = M X 2^(e+f) with M X < 2^49,
	// so a nonzero remainder is at least 2^-49 of the quotient. Atan2's
	// general case lands only by chance: mathAtan2Landings.
	for _, l := range mathAtan2Landings {
		cs = append(cs, mathMidCase{"Atan2F32", l[0], l[1]})
	}
	return cs
}

func float32of(b uint32) float32 { return math.Float32frombits(b) }

// mathMid32 is mid32.mk's Mid32 in Go: y is a midpoint between two
// adjacent f32s (or between 0 and the least subnormal).
func mathMid32(y float64) bool {
	a := math.Float64bits(y) &^ (1 << 63)
	if a >= 0x3810000000000000 {
		return a < 0x47F0000000000000 && a&0x1FFFFFFF == 0x10000000
	}
	q := math.Float64frombits(a) * 0x1p150
	return q >= 1 && q == math.Trunc(q) && int64(q)%2 == 1
}

// mathHypotLands says whether HypotF32's f64 root lands on an f32
// midpoint: the same exact squares, the same rounded sum s.hi and the same
// square root as mid32.mk (each product converted, so Go cannot fuse it).
func mathHypotLands(xb, yb uint32) bool {
	a, b := float64(float32of(xb)), float64(float32of(yb))
	return mathMid32(math.Sqrt(float64(a*a) + float64(b*b)))
}

// mathAtan2Landings are Atan2F32 inputs (y, x) on which Atan2's f64 result
// in its general case is exactly an f32 midpoint: every one that
// TestSTDMATH_Atan2Search (MK_MATH_SEARCH=1) found, 16 of its 238
// candidates (release, x86_64-windows), the general case and
// the pi - atan one both. Each is settled at 512 bits at run time.
var mathAtan2Landings = [][2]uint32{
	{0x38C506DC, 0x43E607DA},
	{0x3CD31557, 0x3D7D089E},
	{0x3CE56221, 0xC957108F},
	{0x3EE7532E, 0xBF26357C},
	{0x40E2AD16, 0xB5D390CD},
	{0x4445C541, 0x3BC0008F},
	{0x457D99BD, 0xC987A3F1},
	{0x45D142D1, 0x3C601561},
	{0xBC0A277D, 0xB6C3FEC1},
	{0xBE522846, 0xBFF82945},
	{0xC14D470F, 0xC4EEBC8F},
	{0xC24E38E4, 0xB84AC915},
	{0xC36C1C72, 0x4308061E},
	{0xC6A337DC, 0xBECE672D},
	{0xC6D24671, 0x39E04661},
	{0xC77D9113, 0xBBDBC2B7},
}

// mathPow32Exact is x^y correctly rounded to f32 for a y with y 2^k an
// integer, k <= 3: |x|^|Y| exactly, its reciprocal and k square roots at
// 4,000 bits, which are exact whenever the true value has fewer bits.
func mathPow32Exact(xb, yb uint32) uint32 {
	x, y := float64(float32of(xb)), float64(float32of(yb))
	k := 0
	for y*float64(int64(1)<<k) != math.Trunc(y*float64(int64(1)<<k)) {
		k++
		if k > 3 {
			panic(fmt.Sprintf("mathPow32Exact: y %g is not a multiple of 1/8", y))
		}
	}
	Y := int64(y * float64(int64(1)<<k))
	neg := x < 0 && k == 0 && Y%2 != 0
	ay := Y
	if ay < 0 {
		ay = -ay
	}
	p := new(big.Float).SetPrec(uint(24*ay + 64)).SetInt64(1)
	bx := new(big.Float).SetFloat64(math.Abs(x))
	for range ay {
		p.Mul(p, bx)
	}
	v := new(big.Float).SetPrec(4000)
	if Y < 0 {
		v.Quo(new(big.Float).SetPrec(4000).SetInt64(1), p)
	} else {
		v.Set(p)
	}
	for range k {
		v.Sqrt(v)
	}
	if neg {
		v.Neg(v)
	}
	r, _ := v.Float32()
	return math.Float32bits(r)
}

// mathHypot32Exact is hypot(x, y) correctly rounded to f32: x^2 + y^2 is
// exact at 1,200 bits, and its square root is exact when it has fewer bits.
func mathHypot32Exact(xb, yb uint32) uint32 {
	x := new(big.Float).SetPrec(1200).SetFloat64(float64(float32of(xb)))
	y := new(big.Float).SetPrec(1200).SetFloat64(float64(float32of(yb)))
	s := new(big.Float).SetPrec(1200).Mul(x, x)
	s.Add(s, new(big.Float).SetPrec(1200).Mul(y, y))
	r, _ := s.Sqrt(s).Float32()
	return math.Float32bits(r)
}

// mathMidWant is c's correctly rounded result.
func mathMidWant(c mathMidCase) uint32 {
	switch c.fn {
	case "PowF32":
		return mathPow32Exact(c.a, c.b)
	case "HypotF32":
		return mathHypot32Exact(c.a, c.b)
	case "Atan2F32":
		r, _ := mathBigEvalP("Atan2", []float64{float64(float32of(c.a)), float64(float32of(c.b))}, mathBigPrec).Float32()
		return math.Float32bits(r)
	}
	panic(c.fn)
}

// mathRunPairs runs fn of the driver exe over pairs of f32 bits and returns
// its results.
func mathRunPairs(t *testing.T, exe, fn string, pairs [][2]uint32) []uint64 {
	t.Helper()
	var buf []byte
	for _, p := range pairs {
		buf = binary.LittleEndian.AppendUint64(buf, uint64(p[0]))
		buf = binary.LittleEndian.AppendUint64(buf, uint64(p[1]))
	}
	file := filepath.Join(t.TempDir(), fn+".bin")
	if err := os.WriteFile(file, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	res := runNative(t, exe, "", fn, file)
	if res.code != 0 {
		t.Fatalf("%s: exit %d\n%s", fn, res.code, res.stderr)
	}
	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if len(lines) != len(pairs) {
		t.Fatalf("%s: %d results for %d inputs", fn, len(lines), len(pairs))
	}
	out := make([]uint64, len(lines))
	for i, l := range lines {
		v, err := strconv.ParseUint(strings.TrimSpace(l), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = v
	}
	return out
}

// mathHypotLandCount pins how many HypotF32 inputs of mathMidCases have an
// f64 root exactly on an f32 midpoint (mathHypotLands).
const mathHypotLandCount = 442

// mathMidCounts pins the inputs per function, which PowF32's and
// Atan2F32's doc lines state.
var mathMidCounts = map[string]int{"PowF32": 94, "HypotF32": 12362, "Atan2F32": 80}

// TestSTDMATH_Midpoint runs mathMidCases, dev and release, and requires
// every result correctly rounded.
func TestSTDMATH_Midpoint(t *testing.T) {
	// The exact evaluators against the four known counterexamples.
	for _, w := range []struct {
		fn         string
		a, b, want uint32
	}{
		{"PowF32", 0x3FB34573, math.Float32bits(123.5), 0x5D81E239},
		{"PowF32", 0x3FC47BD8, math.Float32bits(175.5), 0x75B5BD8B},
		{"HypotF32", 0x4B23C9BD, 0x48BF8A81, 0x4B23E5BB},
		{"HypotF32", 0x4B048D37, 0x49ADE3F7, 0x4B06526F},
	} {
		if got := mathMidWant(mathMidCase{w.fn, w.a, w.b}); got != w.want {
			t.Fatalf("%s(%08x, %08x): the exact evaluator gives %08x, the review %08x", w.fn, w.a, w.b, got, w.want)
		}
	}
	exes := mathBuild(t)
	byFn := map[string][]mathMidCase{}
	for _, c := range mathMidCases() {
		byFn[c.fn] = append(byFn[c.fn], c)
	}
	docs := mathDocs(t)
	for _, fn := range []string{"PowF32", "HypotF32", "Atan2F32"} {
		cs := byFn[fn]
		want := make([]uint32, len(cs))
		var wg sync.WaitGroup
		for i := range cs {
			wg.Go(func() { want[i] = mathMidWant(cs[i]) })
		}
		wg.Wait()
		pairs := make([][2]uint32, len(cs))
		for i, c := range cs {
			pairs[i] = [2]uint32{c.a, c.b}
		}
		for _, prof := range []string{"dev", "release"} {
			got := mathRunPairs(t, exes[prof], fn, pairs)
			bad := 0
			for i, c := range cs {
				if uint32(got[i]) != want[i] {
					if bad++; bad <= 10 {
						t.Errorf("%s %s(%08x = %g, %08x = %g) = %08x, correctly rounded %08x", prof, fn, c.a, float32of(c.a), c.b, float32of(c.b), got[i], want[i])
					}
				}
			}
			if bad > 0 {
				t.Errorf("%s %s: %d of %d inputs misrounded", prof, fn, bad, len(cs))
			}
		}
		if n := mathMidCounts[fn]; n != len(cs) {
			t.Errorf("%s: %d inputs, pinned %d", fn, len(cs), n)
		}
		if fn == "HypotF32" {
			// The test is not vacuous: count the inputs whose f64 root lands
			// on a midpoint, the only ones the fix changes.
			lands := 0
			for _, c := range cs {
				if mathHypotLands(c.a, c.b) {
					lands++
				}
			}
			if lands != mathHypotLandCount {
				t.Errorf("HypotF32: %d of %d inputs land on an f32 midpoint in f64, pinned %d", lands, len(cs), mathHypotLandCount)
			}
			if !strings.Contains(docs[fn], "Correctly rounded for every input") {
				t.Errorf("%s: the doc comment must say it is correctly rounded for every input; it reads:\n  %s", fn, docs[fn])
			}
			continue
		}
		line := fmt.Sprintf("the %s of TestSTDMATH_Midpoint, every one correctly rounded; not proved correctly rounded for every input, so its results may change in a minor release", mathThousands(len(cs)))
		if !strings.Contains(docs[fn], line) {
			t.Errorf("%s: the doc comment must say %q; it reads:\n  %s", fn, line, docs[fn])
		}
	}
}

// TestSTDMATH_PowSearch (MK_MATH_SEARCH=1) is the search behind
// mathPowLandings: x = M 2^-23 for every M in [2^23,
// 2^24), y = n / 2 for n in 5 .. 400 while x^y < 2^128; Go's math.Pow
// within 2^12 f64 ulps of an f32 midpoint makes a candidate, and Mk's
// PowF32 must be correctly rounded on every candidate (settled exactly).
// The candidates on which Mk's f64 Pow is exactly a midpoint are logged
// as mathPowLandings' literal.
func TestSTDMATH_PowSearch(t *testing.T) {
	if os.Getenv("MK_MATH_SEARCH") == "" {
		t.Skip("MK_MATH_SEARCH not set: the search takes a minute")
	}
	var mu sync.Mutex
	var cands [][2]uint32
	var wg sync.WaitGroup
	const workers = 4
	for w := range workers {
		wg.Go(func() {
			for M := uint32(1<<23 + w); M < 1<<24; M += workers {
				x := float64(M) / (1 << 23)
				for n := 5; n <= 400; n++ {
					v := math.Pow(x, float64(n)/2)
					if v >= 0x1p128 {
						break
					}
					if d := int64(math.Float64bits(v)&0x1FFFFFFF) - 0x10000000; d >= -4096 && d <= 4096 {
						mu.Lock()
						cands = append(cands, [2]uint32{math.Float32bits(float32(x)), uint32(n)})
						mu.Unlock()
					}
				}
			}
		})
	}
	wg.Wait()
	slices.SortFunc(cands, func(a, b [2]uint32) int {
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		return int(a[1]) - int(b[1])
	})
	exes := mathBuild(t)
	pairs := make([][2]uint32, len(cands))
	for i, c := range cands {
		pairs[i] = [2]uint32{c[0], math.Float32bits(float32(c[1]) / 2)}
	}
	got := mathRunPairs(t, exes["release"], "PowF32", pairs)
	// The f64 Pow on the same pairs, widened: which land on a midpoint.
	var buf []byte
	for _, p := range pairs {
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(float64(float32of(p[0]))))
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(float64(float32of(p[1]))))
	}
	file := filepath.Join(t.TempDir(), "pow64.bin")
	if err := os.WriteFile(file, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	res := runNative(t, exes["release"], "", "Pow", file)
	if res.code != 0 {
		t.Fatalf("Pow: exit %d\n%s", res.code, res.stderr)
	}
	r64 := strings.Split(strings.TrimSpace(res.stdout), "\n")
	bad := 0
	var landings []string
	for i, c := range cands {
		want := mathPow32Exact(pairs[i][0], pairs[i][1])
		if uint32(got[i]) != want {
			bad++
			t.Errorf("PowF32(%08x, %d/2) = %08x, correctly rounded %08x", c[0], c[1], got[i], want)
		}
		v, _ := strconv.ParseUint(strings.TrimSpace(r64[i]), 10, 64)
		if v&0x1FFFFFFF == 0x10000000 {
			landings = append(landings, fmt.Sprintf("{%#08x, %d},", c[0], c[1]))
		}
	}
	t.Logf("%d candidates, %d misrounded; Pow's f64 result on a midpoint for %d:\n%s", len(cands), bad, len(landings), strings.Join(landings, "\n"))
}

// TestSTDMATH_Atan2Search (MK_MATH_SEARCH=1) is the search behind
// mathAtan2Landings: 2^33 pairs of random f32s (exponents -20 .. 20, both
// signs, from a fixed seed), Go's math.Atan2 within 8 f64 ulps of an f32
// midpoint makes a candidate; Mk's Atan2F32 must be correctly rounded on
// every candidate (512 bits), and the candidates on which Mk's f64 Atan2
// is exactly a midpoint are logged as mathAtan2Landings' literal.
func TestSTDMATH_Atan2Search(t *testing.T) {
	if os.Getenv("MK_MATH_SEARCH") == "" {
		t.Skip("MK_MATH_SEARCH not set: the search takes a minute")
	}
	var mu sync.Mutex
	var cands [][2]uint32
	var wg sync.WaitGroup
	const workers, per = 4, 1 << 31
	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(0xA7A2_2026, uint64(w)))
			f := func() float32 {
				z := rng.Uint32()
				e := 127 - 20 + rng.Uint32N(41)
				return math.Float32frombits(z&0x807FFFFF | e<<23)
			}
			for range per {
				y, x := f(), f()
				v := math.Atan2(float64(y), float64(x))
				if d := int64(math.Float64bits(v)&0x1FFFFFFF) - 0x10000000; d >= -8 && d <= 8 {
					mu.Lock()
					cands = append(cands, [2]uint32{math.Float32bits(y), math.Float32bits(x)})
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	slices.SortFunc(cands, func(a, b [2]uint32) int {
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		return int(a[1]) - int(b[1])
	})
	exes := mathBuild(t)
	got := mathRunPairs(t, exes["release"], "Atan2F32", cands)
	var buf []byte
	for _, p := range cands {
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(float64(float32of(p[0]))))
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(float64(float32of(p[1]))))
	}
	file := filepath.Join(t.TempDir(), "atan2.bin")
	if err := os.WriteFile(file, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	res := runNative(t, exes["release"], "", "Atan2", file)
	if res.code != 0 {
		t.Fatalf("Atan2: exit %d\n%s", res.code, res.stderr)
	}
	r64 := strings.Split(strings.TrimSpace(res.stdout), "\n")
	bad := 0
	var landings []string
	for i, c := range cands {
		want := mathMidWant(mathMidCase{"Atan2F32", c[0], c[1]})
		if uint32(got[i]) != want {
			bad++
			t.Errorf("Atan2F32(%08x, %08x) = %08x, correctly rounded %08x", c[0], c[1], got[i], want)
		}
		v, _ := strconv.ParseUint(strings.TrimSpace(r64[i]), 10, 64)
		if mathMid32(math.Float64frombits(v)) {
			landings = append(landings, fmt.Sprintf("{%#08x, %#08x},", c[0], c[1]))
		}
	}
	t.Logf("%d candidates of %d pairs, %d misrounded; Atan2's f64 result on a midpoint for %d:\n%s", len(cands), workers*per, bad, len(landings), strings.Join(landings, "\n"))
}
