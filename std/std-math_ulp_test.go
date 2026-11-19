// Why: std.math's f64 functions are faithfully rounded, and each doc line
// states the measured largest error and names the test that measured it.
// What: TestSTDMATH_TrueError, its pins in mathTrueError, and
// TestSTDMATH_Edges over mathDDInputs.
// How: rows that differ from the correctly rounded table are evaluated again
// with math/big at mathTruePrec bits; every error must stay below 1 ulp.

package std

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mathTruePrec is the working precision of the f64 measurement: 1,664 bits,
// so 1 + 2^-1074 is exact and more than 500 bits remain after it cancels.
const mathTruePrec = 1664

// mathTrue is one function's pinned measurement: rows is the table's row
// count, off the rows not correctly rounded, and ulp the largest error,
// rounded up to the thousandth, as the doc line prints it.
type mathTrue struct {
	rows, off int
	ulp       string
}

// mathTrueError pins the measurement per f64 function (release build,
// x86_64-windows; the same bits on every target is TestSTDMATH_SameBitsEverywhere's).
var mathTrueError = map[string]mathTrue{
	"Exp":   {20243, 5, "0.502"},    // at [-393.66731202666267]
	"Exp2":  {20283, 933, "0.803"},  // at [-620.4839025132164]
	"Expm1": {20221, 922, "0.743"},  // at [122.35016517779721]
	"Log":   {20181, 0, "0.500"},    // every row correctly rounded
	"Log2":  {20181, 7, "0.518"},    // at [1.13347420880587]
	"Log10": {20203, 14, "0.576"},   // at [0.7521486377873196]
	"Log1p": {20181, 630, "0.727"},  // at [1.6481258069082014]
	"Sin":   {20271, 501, "0.715"},  // at [1.2277711843865693e+172]
	"Cos":   {20271, 485, "0.725"},  // at [-5.240447281849396e+253]
	"Tan":   {20257, 580, "0.778"},  // at [-35.49574528029173]
	"Asin":  {20151, 624, "0.762"},  // at [-0.5016243541351768]
	"Acos":  {20151, 794, "0.829"},  // at [-0.5304142330834256]
	"Atan":  {20173, 231, "0.695"},  // at [-3.484107527416171]
	"Atan2": {21681, 7, "0.557"},    // at [0.75 2]
	"Sinh":  {20185, 0, "0.500"},    // every row correctly rounded
	"Cosh":  {20185, 0, "0.500"},    // every row correctly rounded
	"Tanh":  {20193, 0, "0.500"},    // every row correctly rounded
	"Asinh": {20173, 0, "0.500"},    // every row correctly rounded
	"Acosh": {20161, 0, "0.500"},    // every row correctly rounded
	"Atanh": {20153, 0, "0.500"},    // every row correctly rounded
	"Hypot": {21681, 890, "0.856"},  // at [-2.3098610580608686e+06 -2.6523390745675694e+06]
	"Cbrt":  {20187, 1641, "0.664"}, // at [-1.5646538455772404e-12]
	"Pow":   {21681, 11, "0.503"},   // at [0.5458250574074336 -52.120777446070285]
}

// mathULPError is |got - exact| in ulps of exact's binade, as a float.
func mathULPError(got float64, exact *big.Float) float64 {
	e := exact.MantExp(nil) // 2^(e-1) <= |exact| < 2^e
	ue := e - 1 - 52
	if ue < -1074 {
		ue = -1074
	}
	if ue > 971 {
		ue = 971
	}
	g := new(big.Float).SetPrec(mathTruePrec)
	if math.IsInf(got, 0) {
		g.SetMantExp(big.NewFloat(math.Copysign(1, got)), 1024)
	} else {
		g.SetFloat64(got)
	}
	d := new(big.Float).SetPrec(mathTruePrec).Sub(g, exact)
	d.SetMantExp(d, -ue)
	f, _ := d.Float64()
	return math.Abs(f)
}

// mathTrueRow is one evaluated row.
type mathTrueRow struct {
	fn   string
	args []float64
	got  uint64
	want uint64
}

// mathTrueMeasured is one function's measurement.
type mathTrueMeasured struct {
	rows, off int
	worst     float64
	worstIn   []float64
	bad       []string // evaluator disagreements with the table
}

// mathCeilMilli prints e rounded up to the thousandth, and 0.500 when no
// row is off (every row then within half an ulp).
func mathCeilMilli(e float64) string {
	if e < 0.5 {
		e = 0.5
	}
	return strconv.FormatFloat(math.Ceil(e*1000-1e-9)/1000, 'f', 3, 64)
}

// TestSTDMATH_TrueError measures, pins and checks the largest error of
// every f64 function that is not exact.
//
// TestSTDMATH_Oracle shows only how far a result is from the correctly
// rounded value, which bounds the true error by 1.5 ulp and cannot show
// faithful rounding. This test measures the true error: every row of a
// function's Mk-evidence/math table on which the release build's result
// differs from the table's correctly rounded value is evaluated again with
// math/big at mathTruePrec bits (mathBigEvalP), and the error is
// |result - exact| in units of the last place of the exact value's binade
// (2^(e - 52) for 2^e <= |exact| < 2^(e+1), at least 2^-1074). A row whose
// result equals the table's is within 0.5 ulp, so the largest error over
// the table is the largest over the rows evaluated. The 1,664-bit value
// must itself round to the table's value on every row evaluated, so an
// evaluator fault fails the test instead of passing silently.
//
// The measured largest error and the number of rows off are pinned in
// mathTrueError, every function must stay below 1 ulp (faithful), and each
// doc line in std/math must state the pinned error and name this test.
func TestSTDMATH_TrueError(t *testing.T) {
	dir := mathEvidence()
	if dir == "" {
		t.Skip("Mk-evidence/math not found (set MK_EVIDENCE)")
	}
	exes := mathBuild(t)
	tmp := t.TempDir()
	var fns []mathFn
	for _, f := range mathFns {
		if _, ok := mathBudget[f.mk]; ok && !strings.HasSuffix(f.kind, "32") {
			fns = append(fns, f)
		}
	}
	measured := map[string]*mathTrueMeasured{}
	var jobs []mathTrueRow
	for _, f := range fns {
		rows := readMathTable(t, dir, f)
		in := filepath.Join(tmp, f.mk+".bin")
		var buf []byte
		for _, r := range rows {
			for _, w := range r.in {
				buf = binary.LittleEndian.AppendUint64(buf, w)
			}
		}
		if err := os.WriteFile(in, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		res := runNative(t, exes["release"], "", f.mk, in)
		if res.code != 0 {
			t.Fatalf("%s: exit %d\n%s", f.mk, res.code, res.stderr)
		}
		lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
		if len(lines) != len(rows) {
			t.Fatalf("%s: %d results for %d rows", f.mk, len(lines), len(rows))
		}
		m := &mathTrueMeasured{rows: len(rows)}
		measured[f.mk] = m
		for i, r := range rows {
			got, err := strconv.ParseUint(strings.TrimSpace(lines[i]), 10, 64)
			if err != nil {
				t.Fatalf("%s: line %d: %v", f.mk, i, err)
			}
			if got == r.want[0] {
				continue
			}
			m.off++
			if mathIsNaN(got, false) || mathIsNaN(r.want[0], false) {
				m.bad = append(m.bad, fmt.Sprintf("in %x: got %x want %x (NaN)", r.in, got, r.want[0]))
				continue
			}
			args := make([]float64, len(r.in))
			for j, w := range r.in {
				args[j] = math.Float64frombits(w)
			}
			jobs = append(jobs, mathTrueRow{fn: f.mk, args: args, got: got, want: r.want[0]})
		}
	}

	// Evaluate the rows off on a few workers (the machine is shared).
	workers := min(4, runtime.NumCPU())
	type result struct {
		job mathTrueRow
		err float64
		bad string
	}
	ch := make(chan mathTrueRow)
	out := make(chan result)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				exact := mathBigEvalP(j.fn, j.args, mathTruePrec)
				r := result{job: j}
				if f, _ := exact.Float64(); math.Float64bits(f) != j.want {
					r.bad = fmt.Sprintf("in %v: the %d-bit value rounds to %x, table %x", j.args, mathTruePrec, math.Float64bits(f), j.want)
				}
				r.err = mathULPError(math.Float64frombits(j.got), exact)
				out <- r
			}
		}()
	}
	go func() {
		for _, j := range jobs {
			ch <- j
		}
		close(ch)
		wg.Wait()
		close(out)
	}()
	for r := range out {
		m := measured[r.job.fn]
		if r.bad != "" {
			m.bad = append(m.bad, r.bad)
			continue
		}
		if r.err > m.worst {
			m.worst, m.worstIn = r.err, r.job.args
		}
	}

	var report []string
	for _, f := range fns {
		m := measured[f.mk]
		sort.Strings(m.bad)
		if len(m.bad) > 0 {
			if len(m.bad) > 8 {
				m.bad = append(m.bad[:8], fmt.Sprintf("... %d more", len(m.bad)-8))
			}
			t.Errorf("%s: the evaluator disagrees with the table:\n  %s", f.mk, strings.Join(m.bad, "\n  "))
		}
		ulp := mathCeilMilli(m.worst)
		report = append(report, fmt.Sprintf("%q: {%d, %d, %q}, // at %v", f.mk, m.rows, m.off, ulp, m.worstIn))
		if m.worst >= 1 {
			t.Errorf("%s: largest error %s ulp at %v: not faithfully rounded", f.mk, ulp, m.worstIn)
		}
		pin, ok := mathTrueError[f.mk]
		if !ok {
			t.Errorf("%s: no pin in mathTrueError", f.mk)
			continue
		}
		if pin.rows != m.rows || pin.off != m.off || pin.ulp != ulp {
			t.Errorf("%s: measured %d rows, %d off, largest %s ulp; pinned %d, %d, %s", f.mk, m.rows, m.off, ulp, pin.rows, pin.off, pin.ulp)
		}
	}
	t.Logf("measured (release, %d-bit evaluation of every row off):\n  %s", mathTruePrec, strings.Join(report, "\n  "))
	mathCheckDocLines(t)
}

// mathCheckDocLines requires each pinned function's doc comment in
// std/math to state its pinned largest error and name TestSTDMATH_TrueError.
func mathCheckDocLines(t *testing.T) {
	docs := mathDocs(t)
	for fn, pin := range mathTrueError {
		d, ok := docs[fn]
		if !ok {
			t.Errorf("%s: no doc comment found in std/math", fn)
			continue
		}
		want := fmt.Sprintf("largest error %s ulp", pin.ulp)
		if !strings.Contains(strings.ToLower(d), want) || !strings.Contains(d, "TestSTDMATH_TrueError") {
			t.Errorf("%s: the doc comment must state %q and name TestSTDMATH_TrueError; it reads:\n  %s", fn, want, d)
		}
	}
}

// mathDocs is each function's doc comment in std/math, joined into one
// line.
func mathDocs(t *testing.T) map[string]string {
	files, err := filepath.Glob(filepath.Join("math", "*.mk"))
	if err != nil {
		t.Fatal(err)
	}
	docs := map[string]string{}
	head := regexp.MustCompile(`^(?:export )?([A-Z]\w*)\(`)
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var doc []string
		for _, line := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
			if c, ok := strings.CutPrefix(line, "///"); ok {
				doc = append(doc, strings.TrimSpace(c))
				continue
			}
			if m := head.FindStringSubmatch(line); m != nil && len(doc) > 0 {
				docs[m[1]] = strings.Join(doc, " ")
			}
			doc = nil
		}
	}
	return docs
}

// mathDDInputs is the adversarial and random input set of the functions
// built on dd.mk's kernels and of the table-driven Exp, Log and Pow: each
// branch's boundaries and their neighbours, subnormals, the overflow
// thresholds, and about 3,000 log-uniform random points
// per function from a fixed seed. Atan2's rows are (y, x) pairs.
func mathDDInputs() map[string][][]float64 {
	rng := uint64(0x5EED_3A7B_2026_1112)
	next := func() uint64 {
		rng += 0x9E3779B97F4A7C15
		z := rng
		z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
		z = (z ^ z>>27) * 0x94D049BB133111EB
		return z ^ z>>31
	}
	// logu is a log-uniform value in [2^lo, 2^hi) with a random mantissa.
	logu := func(lo, hi int) float64 {
		e := lo + int(next()%uint64(hi-lo))
		return math.Ldexp(1+float64(next()>>11)/(1<<53), e)
	}
	sign := func(x float64) float64 {
		if next()&1 == 1 {
			return -x
		}
		return x
	}
	around := func(xs ...float64) []float64 {
		var out []float64
		for _, x := range xs {
			out = append(out, x, math.Nextafter(x, math.Inf(1)), math.Nextafter(x, math.Inf(-1)))
		}
		return out
	}
	one := func(vs []float64) [][]float64 {
		var r [][]float64
		for _, v := range vs {
			r = append(r, []float64{v})
		}
		return r
	}
	in := map[string][][]float64{}
	sinhOver := math.Float64frombits(0x408633CE8FB9F87D)
	base := around(0x1p-28, 0.5, 1, 22, 0x1p-1022, 5e-324, sinhOver, 709.78, -709.78)
	var sc []float64
	for _, x := range base {
		sc = append(sc, x, -x)
	}
	for i := 0; i < 3000; i++ {
		sc = append(sc, sign(logu(-30, 10)))
	}
	for i := range sc {
		if math.Abs(sc[i]) > sinhOver {
			sc[i] = math.Copysign(sinhOver, sc[i])
		}
	}
	in["Sinh"], in["Cosh"] = one(sc), one(sc)
	var th []float64
	for _, x := range around(0x1p-28, 0.125, 0.5, 1, 22, 5e-324) {
		th = append(th, x, -x)
	}
	for i := 0; i < 3000; i++ {
		th = append(th, sign(logu(-30, 5)))
	}
	in["Tanh"] = one(th)
	var as []float64
	for _, x := range around(0x1p-28, 0.5, 1, 2, 0x1p28, 0x1p500, math.MaxFloat64) {
		as = append(as, x, -x)
	}
	for i := 0; i < 3000; i++ {
		as = append(as, sign(logu(-30, 40)))
	}
	in["Asinh"] = one(as)
	ac := around(1, 1.5, 2, 0x1p28, 0x1p500)
	ac = append(ac, math.MaxFloat64)
	for k := 1; k <= 52; k++ {
		ac = append(ac, 1+math.Ldexp(1, -k), 1+3*math.Ldexp(1, -k-1))
	}
	for i := 0; i < 1500; i++ {
		ac = append(ac, 1+logu(-52, 0), 1+logu(0, 40))
	}
	in["Acosh"] = one(slices.DeleteFunc(ac, func(x float64) bool { return x < 1 }))
	var at []float64
	for _, x := range around(0x1p-28, 0.5, 0.25, 0.75) {
		at = append(at, x, -x)
	}
	for k := 1; k <= 53; k++ {
		at = append(at, 1-math.Ldexp(1, -k), -1+math.Ldexp(1, -k))
	}
	for i := 0; i < 3000; i++ {
		x := sign(logu(-30, 0))
		if math.Abs(x) >= 1 {
			x = math.Copysign(1-0x1p-53, x)
		}
		at = append(at, x)
	}
	in["Atanh"] = one(at)
	var a2 [][]float64
	for _, k := range []int{-62, -61, -60, -59, -1, 0, 1, 59, 60, 61, 62} {
		for _, f := range []float64{0.5, 0.75, 1 - 0x1p-53} {
			for _, s := range [][2]float64{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
				a2 = append(a2, []float64{s[0] * math.Ldexp(f, k), s[1] * 0.75})
			}
		}
	}
	for _, p := range [][2]float64{{5e-324, 5e-324}, {5e-324, 1}, {1, 5e-324}, {0x1p-1022, 3e-310}, {math.MaxFloat64, math.MaxFloat64}, {math.MaxFloat64, 1}, {1, -math.MaxFloat64}} {
		a2 = append(a2, []float64{p[0], p[1]}, []float64{-p[0], -p[1]})
	}
	for i := 0; i < 3000; i++ {
		x := sign(logu(-60, 60))
		y := sign(math.Ldexp(logu(-1, 1), int(next()%140)-70) * math.Abs(x))
		a2 = append(a2, []float64{y, x})
	}
	in["Atan2"] = a2
	// Exp (exptab.mk): the branch cut-overs (2^-54, 512, 1024), the
	// overflow threshold, the least normal and the least subnormal result,
	// points where r reaches +-ln2/256, and the subnormal range.
	expOver := math.Float64frombits(0x40862E42FEFA39EF)
	var ex []float64
	for _, x := range around(0x1p-54, 0x1p-28, 0.5, 1, 512, 708, 1023) {
		ex = append(ex, x, -x)
	}
	ex = append(ex, around(expOver, math.Float64frombits(0xC086232BDD7ABCD2), -745.1332191019411, -745.1332191019412)...)
	for i := 0; i < 400; i++ {
		m := float64(int(next()%180000)) - 95000
		ex = append(ex, (m+0.5)*math.Ln2/128, -708.4-37*float64(next()>>11)/(1<<53))
	}
	for i := 0; i < 2400; i++ {
		x := sign(logu(-30, 10))
		if x > expOver || x < -745.1 {
			x = math.Copysign(700*float64(next()>>11)/(1<<53), x)
		}
		ex = append(ex, x)
	}
	in["Exp"] = one(ex)
	// Log (logtab.mk): the ends of LogNear1's range, each table row's ends
	// at a random scale, subnormals, and x near 1.
	lg := around(1-0x1p-4, 1+0x1.09p-4, 1, 0.6875, 1.375, 0.5, 2, 5e-324, 0x1p-1022, math.MaxFloat64)
	for i := 0; i < 128; i++ {
		a := math.Float64frombits(0x3FE6000000000000 + uint64(i)<<45)
		k := int(next()%2000) - 1000
		lg = append(lg, around(math.Ldexp(a, k))...)
	}
	for i := 0; i < 600; i++ {
		lg = append(lg, 1+sign(logu(-53, -3)), math.Float64frombits(1+next()%(1<<52)))
	}
	for i := 0; i < 1800; i++ {
		lg = append(lg, math.Float64frombits(next()%0x7FEFFFFFFFFFFFFF+1))
	}
	in["Log"] = one(slices.DeleteFunc(lg, func(x float64) bool { return x <= 0 }))
	// Pow (pow.mk): y ln x near the overflow threshold, the subnormal range
	// and 0, x near 1 with a large y, negative x with integer y, subnormal
	// x, and y near 2^63 and 2^-65.
	// ln x correctly rounded, from math/big (not Go's math.Log, so the inputs
	// do not depend on Go's log bits).
	lnCR := func(x float64) float64 { return mathRN(bigLogP(bfp(x, 256), 256)) }
	var pw [][]float64
	for i := 0; i < 600; i++ {
		x := 0.5 + 3.5*float64(next()>>11)/(1<<53)
		if x == 1 {
			continue
		}
		t := []float64{709.7, -708.3, -744.4, 1e-10, -1e-10}[i%5] - float64(next()%1000)/1000*float64(i%2)
		pw = append(pw, []float64{x, t / lnCR(x)})
	}
	for k := 1; k <= 52; k++ {
		x := 1 + math.Ldexp(1, -k)
		pw = append(pw, []float64{x, 700 / lnCR(x)}, []float64{x, -740 / lnCR(x)}, []float64{1 - math.Ldexp(1, -k-1), 650 / lnCR(1-math.Ldexp(1, -k-1))})
	}
	for i := 0; i < 300; i++ {
		x := -sign(logu(-3, 3))
		x = -math.Abs(x)
		pw = append(pw, []float64{x, float64(int(next()%200) - 100)})
	}
	for _, x := range []float64{5e-324, 3e-310, 0x1p-1022} {
		pw = append(pw, []float64{x, 0.25}, []float64{x, -0.7}, []float64{x, 1.0 / 3})
	}
	pw = append(pw, []float64{1.5, 0x1p-66}, []float64{0.75, -0x1p-66}, []float64{1 + 0x1p-52, 0x1p62}, []float64{1 - 0x1p-53, 0x1p62})
	for i := 0; i < 2000; i++ {
		x := logu(-20, 20)
		y := sign(logu(-10, 6))
		if math.Abs(y*lnCR(x)) > 700 {
			y = math.Copysign(700, y) / math.Abs(lnCR(x))
		}
		pw = append(pw, []float64{x, y})
	}
	in["Pow"] = pw
	// Only finite inputs: the special values are the oracle test's.
	for fn, rows := range in {
		in[fn] = slices.DeleteFunc(rows, func(r []float64) bool {
			return slices.ContainsFunc(r, func(v float64) bool { return math.IsInf(v, 0) || v != v })
		})
	}
	return in
}

// TestSTDMATH_Edges runs the functions built on dd.mk, and Exp, Log and
// Pow, over mathDDInputs, release, and requires every result within 1 ulp of the
// 1,664-bit value (faithful), logging the largest error and the rows off.
func TestSTDMATH_Edges(t *testing.T) {
	exes := mathBuild(t)
	tmp := t.TempDir()
	type job struct {
		fn   string
		args []float64
		got  uint64
	}
	var jobs []job
	inputs := mathDDInputs()
	fns := []string{"Exp", "Log", "Pow", "Sinh", "Cosh", "Tanh", "Asinh", "Acosh", "Atanh", "Atan2"}
	for _, fn := range fns {
		var buf []byte
		for _, r := range inputs[fn] {
			for _, v := range r {
				buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(v))
			}
		}
		file := filepath.Join(tmp, fn+".bin")
		if err := os.WriteFile(file, buf, 0o644); err != nil {
			t.Fatal(err)
		}
		res := runNative(t, exes["release"], "", fn, file)
		if res.code != 0 {
			t.Fatalf("%s: exit %d\n%s", fn, res.code, res.stderr)
		}
		lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
		if len(lines) != len(inputs[fn]) {
			t.Fatalf("%s: %d results for %d inputs", fn, len(lines), len(inputs[fn]))
		}
		for i, r := range inputs[fn] {
			got, err := strconv.ParseUint(strings.TrimSpace(lines[i]), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			jobs = append(jobs, job{fn, r, got})
		}
	}
	type res struct {
		j   job
		err float64
		cr  bool
	}
	ch := make(chan job)
	out := make(chan res)
	var wg sync.WaitGroup
	for w := 0; w < min(4, runtime.NumCPU()); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				exact := mathBigEvalP(j.fn, j.args, mathTruePrec)
				f, _ := exact.Float64()
				g := math.Float64frombits(j.got)
				if math.IsInf(f, 0) && math.Float64bits(f) == j.got {
					// An overflow rounded to the infinity: correct, and no
					// ulp of 2^1024 measures it.
					out <- res{j, 0, true}
					continue
				}
				out <- res{j, mathULPError(g, exact), math.Float64bits(f) == j.got}
			}
		}()
	}
	go func() {
		for _, j := range jobs {
			ch <- j
		}
		close(ch)
		wg.Wait()
		close(out)
	}()
	worst := map[string]float64{}
	worstIn := map[string][]float64{}
	off := map[string]int{}
	for r := range out {
		if !r.cr {
			off[r.j.fn]++
		}
		if r.err > worst[r.j.fn] {
			worst[r.j.fn], worstIn[r.j.fn] = r.err, r.j.args
		}
	}
	var report []string
	docs := mathDocs(t)
	for _, fn := range fns {
		ulp := mathCeilMilli(worst[fn])
		report = append(report, fmt.Sprintf("%q: {%d, %d, %q}, // at %v", fn, len(inputs[fn]), off[fn], ulp, worstIn[fn]))
		if worst[fn] >= 1 {
			t.Errorf("%s: largest error %.3f ulp at %v: not faithfully rounded", fn, worst[fn], worstIn[fn])
		}
		pin, ok := mathDDEdges[fn]
		if !ok || pin.rows != len(inputs[fn]) || pin.off != off[fn] || pin.ulp != ulp {
			t.Errorf("%s: measured %d inputs, %d off, largest %s ulp; pinned %+v", fn, len(inputs[fn]), off[fn], ulp, pin)
		}
		want := fmt.Sprintf("over the %s adversarial and random inputs of TestSTDMATH_Edges, largest error %s ulp", mathThousands(len(inputs[fn])), ulp)
		if !strings.Contains(strings.ToLower(docs[fn]), strings.ToLower(want)) {
			t.Errorf("%s: the doc comment must say %q; it reads:\n  %s", fn, want, docs[fn])
		}
	}
	t.Logf("dd.mk's functions over their edge and random inputs (release):\n  %s", strings.Join(report, "\n  "))
}

// mathDDEdges pins TestSTDMATH_Edges' measurement: inputs,
// inputs not correctly rounded, largest error (release, x86_64-windows).
var mathDDEdges = map[string]mathTrue{
	"Exp":   {3254, 9, "0.506"},
	"Log":   {3412, 0, "0.500"},
	"Pow":   {3069, 2, "0.504"},
	"Sinh":  {3054, 4, "0.543"},
	"Cosh":  {3054, 0, "0.500"},
	"Tanh":  {3036, 1, "0.500"},
	"Asinh": {3040, 0, "0.500"},
	"Acosh": {3119, 0, "0.500"},
	"Atanh": {3130, 0, "0.500"},
	"Atan2": {3146, 0, "0.500"},
}

// mathThousands prints n with commas, as the doc lines do.
func mathThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
