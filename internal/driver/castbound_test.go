// Why: a checked float-to-integer Cast must convert or panic at exactly the
// same inputs in the release build, a dev build and the evaluator.
// What: TestFloatCastBoundaries over every float and integer type pair
// (fcPairs, fcInputs, fcWant, fcProgram) and TestFloatCastTrapOrder.
// How: a math/big table of each bound's neighbours, zeros, infinities and
// NaNs, judged by native runs and by const initializers in the evaluator.

package driver

import (
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
)

// The inputs around each bound are the bound, the floats next to it, a half
// and a one either side; then the signed zeros, the halves either side of
// zero, the infinities, the largest finite values, denormals and NaNs of
// either sign and payload. Each input converts, truncating toward zero, or
// panics with PanicCast at the cast's site. The release build runs each
// converting input in one run with its inputs on the command line, so
// nothing is folded, and each panicking input in a run of its own; the
// evaluator computes the converting inputs as a const initializer printed by
// the same binary, and each panicking one as a const of its own, an E0901
// whose trace starts at the cast; a dev build runs the converting inputs.
// codegen's floatFits (one branch on the hot path) must leave every answer
// as it is; codegen's TestFloatCastOneBranch pins the branch.

// fcPair is one cast: from f32 or f64 to an integer type of n bits.
type fcPair struct {
	from   string // "f32" or "f64"
	to     string
	n      int
	signed bool
}

func (p fcPair) f32() bool { return p.from == "f32" }

func fcPairs() []fcPair {
	ints := []fcPair{{to: "i8", n: 8, signed: true}, {to: "i16", n: 16, signed: true}, {to: "i32", n: 32, signed: true},
		{to: "i64", n: 64, signed: true}, {to: "int", n: 64, signed: true},
		{to: "u8", n: 8}, {to: "u16", n: 16}, {to: "u32", n: 32}, {to: "u64", n: 64}, {to: "uint", n: 64}}
	var ps []fcPair
	for _, f := range []string{"f32", "f64"} {
		for _, i := range ints {
			i.from = f
			ps = append(ps, i)
		}
	}
	return ps
}

// fcCase is one input of one pair: the float's bits (an f32's in the low
// 32) and what the cast gives, as u64 bits of the two's complement, or a
// panic.
type fcCase struct {
	pair int
	bits uint64
	val  uint64
	trap bool
}

// fcInputs are the float bits fcPair p is tried on.
func fcInputs(p fcPair) []uint64 {
	var out []uint64
	seen := map[uint64]bool{}
	add := func(b uint64) {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	// near adds x rounded to p's float type and the floats either side.
	near := func(x float64) {
		if p.f32() {
			f := float32(x)
			for _, g := range []float32{f, math.Nextafter32(f, float32(math.Inf(1))), math.Nextafter32(f, float32(math.Inf(-1)))} {
				add(uint64(math.Float32bits(g)))
			}
			return
		}
		for _, g := range []float64{x, math.Nextafter(x, math.Inf(1)), math.Nextafter(x, math.Inf(-1))} {
			add(math.Float64bits(g))
		}
	}
	nans := []uint64{0x7FF8000000000000, 0xFFF8000000000000, 0x7FF0000000000001, 0xFFFFFFFFFFFFFFFF}
	if p.f32() {
		nans = []uint64{0x7FC00000, 0xFFC00000, 0x7F800001, 0xFFFFFFFF}
	}
	for _, b := range nans {
		add(b)
	}
	maxF := math.MaxFloat64
	if p.f32() {
		maxF = math.MaxFloat32
	}
	tiny := math.SmallestNonzeroFloat64
	if p.f32() {
		tiny = float64(math.SmallestNonzeroFloat32)
	}
	for _, x := range []float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1), maxF, -maxF, tiny, -tiny,
		0.5, -0.5, 1, -1, 2, -2, 0.75, -0.75, 100.5, -100.5} {
		near(x)
	}
	hi := math.Ldexp(1, p.n) // the first integer above the type's range
	lo := -1.0               // the first integer below it
	if p.signed {
		hi, lo = math.Ldexp(1, p.n-1), -math.Ldexp(1, p.n-1)-1
	}
	for _, c := range []float64{hi, hi - 1, hi - 0.5, hi + 1, hi + 0.5, lo, lo + 1, lo + 0.5, lo - 0.5, lo + 1.5, hi / 2, -hi / 2} {
		near(c)
	}
	return out
}

// fcWant is what pair p's cast gives the float with bits b.
func fcWant(p fcPair, b uint64) (uint64, bool) {
	x := math.Float64frombits(b)
	if p.f32() {
		x = float64(math.Float32frombits(uint32(b)))
	}
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, true
	}
	t, _ := new(big.Float).SetFloat64(x).Int(nil) // toward zero
	lo, hi := new(big.Int), new(big.Int)
	if p.signed {
		lo.Lsh(big.NewInt(1), uint(p.n-1)).Neg(lo)
		hi.Lsh(big.NewInt(1), uint(p.n-1)).Sub(hi, big.NewInt(1))
	} else {
		hi.Lsh(big.NewInt(1), uint(p.n)).Sub(hi, big.NewInt(1))
	}
	if t.Cmp(lo) < 0 || t.Cmp(hi) > 0 {
		return 0, true
	}
	if t.Sign() < 0 {
		return uint64(t.Int64()), false
	}
	return t.Uint64(), false
}

// fcProgram is the program of the converting cases: one function per pair,
// its cast at line fcLine(k), column 12; Conv dispatches by pair; the
// const AtCompileTime holds every converting case's result as the
// evaluator computes it. Run with no arguments it prints AtCompileTime,
// one value per line; with pairs of arguments (pair, bits as an i64) it
// converts each and prints the results, one per line.
func fcProgram(pairs []fcPair, conv []fcCase, consts string) string {
	var s strings.Builder
	for k, p := range pairs {
		fmt.Fprintf(&s, "P%d(x: %s): %s {\n    return Cast[%s](x)\n}\n\n", k, p.from, p.to, p.to)
	}
	s.WriteString("// Conv is pair p's cast of the float with bits b, as the u64 bits of the result.\nConv(p: int, b: u64): u64 {\n")
	for k, p := range pairs {
		arg := "f64.FromBits(b)"
		if p.f32() {
			arg = "f32.FromBits(Trunc[u32](b))"
		}
		wide := "Cast[u64](P%d(%s))"
		if p.signed {
			wide = "Trunc[u64](Cast[i64](P%d(%s)))"
		}
		fmt.Fprintf(&s, "    if p == %d {\n        return "+wide+"\n    }\n", k, k, arg)
	}
	s.WriteString("    return 0\n}\n\n")
	if consts != "" {
		s.WriteString(consts)
		return s.String()
	}
	fmt.Fprintf(&s, "const Rows: [%d][2]u64 = [", len(conv))
	for i, c := range conv {
		if i > 0 {
			s.WriteString(", ")
		}
		fmt.Fprintf(&s, "[%d, %d]", c.pair, c.bits)
	}
	s.WriteString("]\n\n")
	fmt.Fprintf(&s, `Folded(): [%[1]d]u64 {
    var out: [%[1]d]u64 = [0; %[1]d]
    for i in 0..%[1]d {
        out[i] = Conv(Cast[int](Rows[i][0]), Rows[i][1])
    }
    return out
}

const AtCompileTime: [%[1]d]u64 = Folded()

export Main(sys: &System): ParseError|WriteError!void {
    var out := sys.Stdout()
    if sys.args.Len() == 1 {
        for i in 0..%[1]d {
            v := AtCompileTime[i]
            try Print(mut out, "{v}\n", (v))
        }
        return
    }
    var i := 1
    for i + 1 < sys.args.Len() {
        p := Cast[int](try sys.args[i].ParseInt())
        b := Trunc[u64](try sys.args[i + 1].ParseInt())
        v := Conv(p, b)
        try Print(mut out, "{v}\n", (v))
        i += 2
    }
}
`, len(conv))
	return s.String()
}

// fcLine is the line of pair k's cast in fcProgram.
func fcLine(k int) int { return 4*k + 2 }

func TestFloatCastBoundaries(t *testing.T) {
	pairs := fcPairs()
	var conv, traps []fcCase
	for k, p := range pairs {
		for _, b := range fcInputs(p) {
			v, trap := fcWant(p, b)
			c := fcCase{pair: k, bits: b, val: v, trap: trap}
			if trap {
				traps = append(traps, c)
			} else {
				conv = append(conv, c)
			}
		}
	}
	t.Logf("%d pairs, %d converting inputs, %d panicking", len(pairs), len(conv), len(traps))
	target := HostTarget()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(src, []byte(fcProgram(pairs, conv, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	argv := make([]string, 0, 2*len(conv))
	for _, c := range conv {
		argv = append(argv, strconv.Itoa(c.pair), strconv.FormatInt(int64(c.bits), 10))
	}
	judge := func(t *testing.T, what, out string) {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n")
		if len(lines) != len(conv) {
			t.Fatalf("%s: %d lines, want %d", what, len(lines), len(conv))
		}
		for i, c := range conv {
			if lines[i] != strconv.FormatUint(c.val, 10) {
				p := pairs[c.pair]
				t.Errorf("%s: Cast[%s] of %s bits %#x = %s, want %d", what, p.to, p.from, c.bits, lines[i], c.val)
			}
		}
	}
	for _, prof := range []types.Profile{types.Release, types.Dev} {
		name := "release"
		if prof == types.Dev {
			name = "dev"
		}
		t.Run(name, func(t *testing.T) {
			exe := buildProgram(t, src, target, prof, t.TempDir())
			if prof == types.Release {
				out, errOut, code := runExe(t, target, exe, "")
				if code != 0 {
					t.Fatalf("eval table: exit %d, stderr %q", code, errOut)
				}
				judge(t, "eval", out)
			}
			out, errOut, code := runExe(t, target, exe, "", argv...)
			if code != 0 {
				t.Fatalf("%s: exit %d, stderr %q", name, code, errOut)
			}
			judge(t, name, out)
			if prof != types.Release {
				return
			}
			for _, c := range traps {
				p := pairs[c.pair]
				_, errOut, code := runExe(t, target, exe, "", strconv.Itoa(c.pair), strconv.FormatInt(int64(c.bits), 10))
				first, _, _ := strings.Cut(strings.ReplaceAll(errOut, "\r\n", "\n"), "\n")
				want := fmt.Sprintf("main.mk:%d:12", fcLine(c.pair))
				at, ok := strings.CutPrefix(first, "panic: cast at ")
				if code != 101 || !ok || !(at == want || strings.HasSuffix(at, "/"+want) || strings.HasSuffix(at, "\\"+want)) {
					t.Errorf("Cast[%s] of %s bits %#x: exit %d, %q; want 101, %q", p.to, p.from, c.bits, code, first, want)
				}
			}
		})
	}
	t.Run("eval-panics", func(t *testing.T) {
		var consts strings.Builder
		for i, c := range traps {
			fmt.Fprintf(&consts, "const T%d: u64 = Conv(%d, %d)\n", i, c.pair, c.bits)
		}
		consts.WriteString("\nexport Main(sys: &System) {\n}\n")
		tdir := t.TempDir()
		tsrc := filepath.Join(tdir, "main.mk")
		if err := os.WriteFile(tsrc, []byte(fcProgram(pairs, nil, consts.String())), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := NewSession(Config{Root: tsrc, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r := s.Check()
		text := r.Report.Text(r.Sources)
		head := regexp.MustCompile(`(?m)^(?:[^\n]*[\\/])?main\.mk:(\d+):7: (E\d+) [^\n]*\n(?:[^\n]*\n)*?\s+trace: \[\{"name":"P(\d+)","at":"[^"]*?main\.mk:(\d+):(\d+)"\}`)
		got := map[int]string{}
		for _, m := range head.FindAllStringSubmatch(text, -1) {
			line, _ := strconv.Atoi(m[1])
			got[line] = m[2] + " P" + m[3] + " " + m[4] + ":" + m[5]
		}
		if len(r.Report.Diags) != len(traps) {
			t.Errorf("%d diagnostics, want one E0901 per panicking input (%d)", len(r.Report.Diags), len(traps))
		}
		if n := strings.Count(text, "compile-time evaluation panicked: cast"); n != len(traps) {
			t.Errorf("%d cast panics at compile time, want %d", n, len(traps))
		}
		first := strings.Count(fcProgram(pairs, nil, "\n"), "\n") // the line of T0
		for i, c := range traps {
			want := fmt.Sprintf("E0901 P%d %d:12", c.pair, fcLine(c.pair))
			if g := got[first+i]; g != want {
				p := pairs[c.pair]
				t.Errorf("Cast[%s] of %s bits %#x at compile time: %q, want %q", p.to, p.from, c.bits, g, want)
			}
		}
		if t.Failed() {
			t.Logf("%s", text)
		}
	})
}

// fcTwoSrc has two checked casts in one function, one per line, so a
// panic names which one failed: the first's site when it fails, whatever
// the second would do, and the second's only when the first converts.
const fcTwoSrc = `Two(x: f64, y: f32): i64 {
    a := Cast[i8](x)
    b := Cast[u16](y)
    return Cast[i64](a) + Cast[i64](b)
}

export Main(sys: &System): ParseError|WriteError!void {
    x := f64.FromBits(Trunc[u64](try sys.args[1].ParseInt()))
    y := f32.FromBits(Trunc[u32](try sys.args[2].ParseInt()))
    v := Two(x, y)
    var out := sys.Stdout()
    try Print(mut out, "{v}\n", (v))
}
`

// TestFloatCastTrapOrder: the order of a function's cast traps is the
// order of its casts, in the release build and in the evaluator, the
// one-branch hot path (codegen floatFits) included: first fails, second
// fails, both fail (NaN and NaN), and neither.
func TestFloatCastTrapOrder(t *testing.T) {
	nan32 := uint64(0x7FC00000)
	cases := []struct {
		x    float64
		y    uint64 // f32 bits
		site string // "" when it converts
		want string
	}{
		{300.0, uint64(math.Float32bits(5)), "2:10", ""},
		{5.0, uint64(math.Float32bits(-1)), "3:10", ""},
		{math.NaN(), nan32, "2:10", ""},
		{-128.5, uint64(math.Float32bits(65535.5)), "", "65407"},
	}
	target := HostTarget()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(src, []byte(fcTwoSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := buildProgram(t, src, target, types.Release, t.TempDir())
	var consts strings.Builder
	for i, c := range cases {
		out, errOut, code := runExe(t, target, exe, "", strconv.FormatInt(int64(math.Float64bits(c.x)), 10), strconv.FormatUint(c.y, 10))
		first, _, _ := strings.Cut(strings.ReplaceAll(errOut, "\r\n", "\n"), "\n")
		if c.site == "" {
			if code != 0 || strings.TrimSpace(out) != c.want {
				t.Errorf("case %d: exit %d, %q, stderr %q; want %s", i, code, out, errOut, c.want)
			}
			continue
		}
		at, ok := strings.CutPrefix(first, "panic: cast at ")
		want := "main.mk:" + c.site
		if code != 101 || !ok || !(at == want || strings.HasSuffix(at, "/"+want) || strings.HasSuffix(at, "\\"+want)) {
			t.Errorf("case %d: exit %d, %q; want 101 and a cast panic at %s", i, code, first, want)
		}
		fmt.Fprintf(&consts, "const T%d: i64 = Two(f64.FromBits(%d), f32.FromBits(%d))\n", i, math.Float64bits(c.x), c.y)
	}
	// The evaluator: one const per panicking case, each an E0901 whose
	// trace starts at the cast that failed.
	tdir := t.TempDir()
	tsrc := filepath.Join(tdir, "main.mk")
	evalSrc := strings.Split(fcTwoSrc, "export Main")[0] + consts.String() + "\nexport Main(sys: &System) {\n}\n"
	if err := os.WriteFile(tsrc, []byte(evalSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(Config{Root: tsrc, CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	r := s.Check()
	text := r.Report.Text(r.Sources)
	traceRe := regexp.MustCompile(`trace: \[\{"name":"Two","at":"[^"]*?main\.mk:(\d+:\d+)"\}`)
	var sites []string
	for _, m := range traceRe.FindAllStringSubmatch(text, -1) {
		sites = append(sites, m[1])
	}
	var want []string
	for _, c := range cases {
		if c.site != "" {
			want = append(want, c.site)
		}
	}
	if strings.Join(sites, " ") != strings.Join(want, " ") || strings.Count(text, "compile-time evaluation panicked: cast") != len(want) {
		t.Errorf("compile-time cast panics at %v, want %v:\n%s", sites, want, text)
	}
}
