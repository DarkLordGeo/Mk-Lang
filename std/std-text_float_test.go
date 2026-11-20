// Why: float formatting and str.ParseFloat are Mk ports of Go's strconv and
// must agree with it bit for bit, at compile time and natively.
// What: TestFloatLayoutThreshold, TestFloatFormatMatchesGo,
// TestParseFloatEdges, TestParseFloatMatchesGo, the Linux leg.
// How: the oracle is strconv's digits laid out by layoutShortest; mk check
// runs the evaluator, native programs over a copy of std compare output.

package std

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// layoutShortest applies std's float layout to Go's shortest digits: s is
// strconv.FormatFloat(x, 'e', -1, bits). Fixed notation when the decimal
// exponent is from -4 to 20, else d[.ddd]e±XX with at least two exponent
// digits; nan, inf, -inf.
func layoutShortest(s string) string {
	switch s {
	case "NaN":
		return "nan"
	case "+Inf":
		return "inf"
	case "-Inf":
		return "-inf"
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	mant, exps, _ := strings.Cut(s, "e")
	e, err := strconv.Atoi(exps)
	if err != nil {
		panic(s)
	}
	digits := strings.Replace(mant, ".", "", 1)
	var out string
	if digits == "0" {
		out = "0"
	} else if e >= -4 && e < 21 {
		dp := e + 1 // digits before the point
		switch {
		case dp <= 0:
			out = "0." + strings.Repeat("0", -dp) + digits
		case dp >= len(digits):
			out = digits + strings.Repeat("0", dp-len(digits))
		default:
			out = digits[:dp] + "." + digits[dp:]
		}
	} else {
		sign := "+"
		if e < 0 {
			sign, e = "-", -e
		}
		out = mant + "e" + sign + fmt.Sprintf("%02d", e)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// goShortest is the oracle for spec 0.
func goShortest(x float64, bits int) string {
	return layoutShortest(strconv.FormatFloat(x, 'e', -1, bits))
}

// goFixed is the oracle for spec 2 with n digits.
func goFixed(x float64, n, bits int) string {
	switch {
	case math.IsNaN(x):
		return "nan"
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	}
	return strconv.FormatFloat(x, 'f', n, bits)
}

// fmtFixture is a compile-time harness: a Writer over a frame buffer whose
// table refuses to grow, and FloatIs, 0 when bits b format by spec as want.
const fmtFixture = `import std.raw

const BufTable = WriterTable(write: BufFull, flush: BufFlush)

BufFull(w: mut Writer, b: []u8): WriteError!void {
    fail WriteError
}

BufFlush(w: mut Writer): WriteError!void {
}

BufWriter(buf: mut []u8): Writer from buf {
    unsafe "fixture: the writer's buffer is buf" {
        return Writer(io: InertIoValue(), table: &BufTable, ctx: raw.DataMut(buf), buf: raw.DataMut(buf), used: 0, cap: buf.Len())
    }
}

FloatIs(b: u64, wide: bool, spec: u32, want: str): int {
    var buf: [512]u8 = [0; 512]
    var w := BufWriter(mut buf[..])
    FmtFloatBits(mut w, b, wide, spec) else {
        return 1
    }
    if StrOf(w.buf, w.used) != want {
        return 1
    }
    return 0
}
`

// floatCase is one formatting case for a compile-time fixture.
type floatCase struct {
	bits uint64
	wide bool
	spec uint32
	want string
}

// floatFixture is fmtFixture plus the cases as const arrays and a check
// const counting failures.
func floatFixture(name string, cases []floatCase) string {
	var bits, wide, spec, want []string
	for _, c := range cases {
		bits = append(bits, fmt.Sprintf("0x%x", c.bits))
		wide = append(wide, strconv.FormatBool(c.wide))
		spec = append(spec, strconv.FormatUint(uint64(c.spec), 10))
		want = append(want, mkString(c.want))
	}
	n := len(cases)
	return fmtFixture + fmt.Sprintf(`
const %[1]sBits: [%[2]d]u64 = [%[3]s]
const %[1]sWide: [%[2]d]bool = [%[4]s]
const %[1]sSpec: [%[2]d]u32 = [%[5]s]
const %[1]sWant: [%[2]d]str = [%[6]s]

%[1]sCheck(): int {
    var bad := 0
    for k in 0..%[2]d {
        bad += FloatIs(%[1]sBits[k], %[1]sWide[k], %[1]sSpec[k], %[1]sWant[k])
    }
    return bad
}

const %[1]sOk = [0; 1][%[1]sCheck()]
`, name, n, strings.Join(bits, ", "), strings.Join(wide, ", "), strings.Join(spec, ", "), strings.Join(want, ", "))
}

// TestFloatLayoutThreshold pins the float layout on both sides of each
// threshold against literal strings, for the oracle and for std at compile
// time.
func TestFloatLayoutThreshold(t *testing.T) {
	t.Parallel()
	lits := []struct {
		x    float64
		f32  bool
		want string
	}{
		{1e6, false, "1000000"}, {123456789.0, false, "123456789"}, {1e20, false, "100000000000000000000"},
		{1e21, false, "1e+21"}, {0.0001, false, "0.0001"}, {0.00001, false, "1e-05"}, {1.5e-7, false, "1.5e-07"},
		{5e-324, false, "5e-324"}, {1.7976931348623157e308, false, "1.7976931348623157e+308"},
		{math.Copysign(0, -1), false, "-0"}, {16777216, true, "16777216"}, {0, false, "0"}, {1.5, false, "1.5"},
		{-2.5, false, "-2.5"}, {0.1, false, "0.1"}, {0.1, true, "0.1"}, {123456.789e3, false, "123456789"},
		{1.25e-5, false, "1.25e-05"}, {9.999999999999999e20, false, "999999999999999900000"},
		{math.NaN(), false, "nan"}, {math.Inf(1), false, "inf"}, {math.Inf(-1), false, "-inf"},
		{3.4028234663852886e38, true, "3.4028235e+38"}, {1e-45, true, "1e-45"},
	}
	var cases []floatCase
	for _, l := range lits {
		bits, b := math.Float64bits(l.x), 64
		if l.f32 {
			bits, b = uint64(math.Float32bits(float32(l.x))), 32
		}
		if got := goShortest(l.x, b); got != l.want {
			t.Errorf("layoutShortest(%v) = %q, want %q", l.x, got, l.want)
		}
		cases = append(cases, floatCase{bits, !l.f32, 0, l.want})
	}
	fixed := []struct {
		x    float64
		n    int
		want string
	}{
		{3.14159, 2, "3.14"}, {2.5, 0, "2"}, {3.5, 0, "4"}, {0.125, 2, "0.12"}, {0.375, 2, "0.38"},
		{-0.0, 3, "0.000"}, {math.Copysign(0, -1), 1, "-0.0"}, {1e-10, 5, "0.00000"}, {123.456, 1, "123.5"},
		{1e22, 1, "10000000000000000000000.0"}, {0.1, 20, "0.10000000000000000555"},
		{5e-324, 17, "0.00000000000000000"},
	}
	for _, f := range fixed {
		if got := goFixed(f.x, f.n, 64); got != f.want {
			t.Errorf("goFixed(%v, %d) = %q, want %q", f.x, f.n, got, f.want)
		}
		cases = append(cases, floatCase{math.Float64bits(f.x), true, 2 | uint32(f.n)<<8, f.want})
	}
	expectClean(t, "std01", floatFixture("Std01", cases))
}

// edgeFloats are the edge cases of S_G183: zeros, subnormals, the extremes,
// powers of ten and their neighbours, halfway-looking values.
func edgeFloats() []float64 {
	xs := []float64{0, math.Copysign(0, -1), 5e-324, 1e-323, 2.2250738585072009e-308, 2.2250738585072014e-308,
		math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64, 1, -1, 0.5, 2, 1.0 / 3, 2.0 / 3,
		math.Pi, math.E, 9007199254740992, 9007199254740993, 4503599627370496.5, 0.3, 123456789012345680}
	for e := -30; e <= 30; e++ {
		x := math.Pow(10, float64(e))
		xs = append(xs, x, math.Nextafter(x, 0), math.Nextafter(x, math.Inf(1)), -x)
	}
	for e := -1074; e <= 1023; e += 37 {
		xs = append(xs, math.Ldexp(1, e), math.Ldexp(1.5, e))
	}
	return xs
}

// splitmix64 is the generator the native program also runs.
func splitmix64(s *uint64) uint64 {
	*s += 0x9E3779B97F4A7C15
	z := *s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// bitsModule is a std module only these tests add: user code cannot reach
// raw's bit views (no public API names them), so it exposes them.
const bitsModule = `import std.raw

export F64From(x: u64): f64 {
    return raw.F64From(x)
}

export F32From(x: u32): f32 {
    return raw.F32From(x)
}

export F64Bits(x: f64): u64 {
    return raw.F64Bits(x)
}

export Mix(s: u64): u64 {
    var z := s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}
`

// TestFloatFormatMatchesGo formats, natively at dev and release, 100,000
// seeded random f64 bit patterns and as many f32 ones with spec 0, 20,000
// of each with every spec .0 to .17, and the edge cases with every spec .0
// to .17 and .40 and .99 (the multiprecision path), against Go's strconv
// laid out by layoutShortest.
func TestFloatFormatMatchesGo(t *testing.T) {
	t.Parallel()
	p := g183Program()
	// Dev and release: optimization changes the code the 128-bit scaling
	// compiles to (a sign-extension bug once showed only with optimization).
	for _, flags := range [][]string{nil, {"--release"}} {
		r := runNative(t, buildWithModule(t, p.rel, p.module, p.src, flags...), p.stdin)
		if r.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, r.code, r.stderr)
		}
		t.Logf("native run %v: %v", flags, r.took)
		compareLines(t, r.stdout, p.want)
	}
}

// g183Program is TestFloatFormatMatchesGo's program: 100,000 shortest pairs, 40,000
// fixed lines, the edge lines.
func g183Program() textProgram {
	const n, nfixed = 100000, 20000
	var edges []string
	for _, x := range edgeFloats() {
		edges = append(edges, fmt.Sprintf("0x%x", math.Float64bits(x)))
	}
	precs := make([]string, 0, 18)
	for p := 0; p <= 17; p++ {
		precs = append(precs, fmt.Sprintf("{x:.%d}", p))
	}
	fixedLine := strings.Join(precs, " ")
	src := fmt.Sprintf(`import std.zzbits

const Edges: [%d]u64 = [%s]

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var s: u64 = 0
    for i in 0..%d {
        s = s +%% 0x9E3779B97F4A7C15
        b := zzbits.Mix(s)
        x := zzbits.F64From(b)
        y := zzbits.F32From(Trunc[u32](b >> 32))
        try Print(mut out, "{x} {y}\n", (x, y))
        if i < %d {
            try Print(mut out, "%s\n", (x))
            try Print(mut out, "%s\n", (x: y))
        }
    }
    for b in Edges {
        x := zzbits.F64From(b)
        try Print(mut out, "{x} %s {x:.40} {x:.99}\n", (x))
    }
    try out.Flush()
}
`, len(edges), strings.Join(edges, ", "), n, nfixed, fixedLine, fixedLine, fixedLine)
	var want strings.Builder
	fixedAll := func(x float64, bits int) string {
		var p []string
		for k := 0; k <= 17; k++ {
			p = append(p, goFixed(x, k, bits))
		}
		return strings.Join(p, " ")
	}
	var s uint64
	for i := 0; i < n; i++ {
		b := splitmix64(&s)
		x := math.Float64frombits(b)
		y := math.Float32frombits(uint32(b >> 32))
		fmt.Fprintf(&want, "%s %s\n", goShortest(x, 64), goShortest(float64(y), 32))
		if i < nfixed {
			fmt.Fprintf(&want, "%s\n%s\n", fixedAll(x, 64), fixedAll(float64(y), 32))
		}
	}
	for _, x := range edgeFloats() {
		fmt.Fprintf(&want, "%s %s %s %s\n", goShortest(x, 64), fixedAll(x, 64), goFixed(x, 40, 64), goFixed(x, 99, 64))
	}
	return textProgram{"g183", "zzbits/bits.mk", bitsModule, src, "", want.String()}
}

// compareLines reports the first few differing lines.
func compareLines(t *testing.T, got, want string) {
	t.Helper()
	if got == want {
		return
	}
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	bad := 0
	for i := 0; i < len(w) || i < len(g); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			if bad < 10 {
				t.Errorf("line %d:\n got %q\nwant %q", i+1, gl, wl)
			}
			bad++
		}
	}
	t.Errorf("%d of %d lines differ", bad, len(w))
}

// parseCase is one ParseFloat case: the text and, when ok, Go's bits.
type parseCase struct {
	s    string
	ok   bool
	bits uint64
}

// inGrammar reports whether s is in str.ParseFloat's grammar: [+-] then
// digits, digits., digits.digits or .digits with an optional exponent, or
// inf, infinity, nan in any case.
func inGrammar(s string) bool {
	t := s
	if t != "" && (t[0] == '+' || t[0] == '-') {
		t = t[1:]
	}
	low := strings.ToLower(t)
	if low == "inf" || low == "infinity" {
		return true
	}
	if strings.ToLower(s) == "nan" {
		return true
	}
	i, digits := 0, 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i, digits = i+1, digits+1
	}
	if i < len(t) && t[i] == '.' {
		i++
		for i < len(t) && t[i] >= '0' && t[i] <= '9' {
			i, digits = i+1, digits+1
		}
	}
	if digits == 0 {
		return false
	}
	if i < len(t) && (t[i] == 'e' || t[i] == 'E') {
		i++
		if i < len(t) && (t[i] == '+' || t[i] == '-') {
			i++
		}
		ed := 0
		for i < len(t) && t[i] >= '0' && t[i] <= '9' {
			i, ed = i+1, ed+1
		}
		if ed == 0 {
			return false
		}
	}
	return i == len(t)
}

// goParse is the oracle: outside the grammar, or an overflow to an
// infinity from finite text, is ParseError; else Go's value, NaN canonical.
func goParse(s string) parseCase {
	if !inGrammar(s) {
		return parseCase{s: s}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		low := strings.ToLower(strings.TrimLeft(s, "+-"))
		if !(low == "inf" || low == "infinity") {
			return parseCase{s: s}
		}
	}
	if math.IsNaN(f) {
		return parseCase{s, true, 0x7ff8000000000000}
	}
	return parseCase{s, true, math.Float64bits(f)}
}

// parseFixture checks ParseFloat over cases at compile time.
func parseFixture(name string, cases []parseCase) string {
	var in, ok, bits []string
	for _, c := range cases {
		in = append(in, mkString(c.s))
		ok = append(ok, strconv.FormatBool(c.ok))
		bits = append(bits, fmt.Sprintf("0x%x", c.bits))
	}
	n := len(cases)
	return fmt.Sprintf(`import std.raw

const %[1]sIn: [%[2]d]str = [%[3]s]
const %[1]sOk: [%[2]d]bool = [%[4]s]
const %[1]sBits: [%[2]d]u64 = [%[5]s]

%[1]sOne(k: int): int {
    // By reference: a const array named by value is copied whole at each
    // index at compile time (STDTEXT-R3).
    strs := &%[1]sIn
    ok := &%[1]sOk
    bits := &%[1]sBits
    v := strs[k].ParseFloat() else {
        if ok[k] {
            return 1
        }
        return 0
    }
    if !ok[k] || raw.F64Bits(v) != bits[k] {
        return 1
    }
    return 0
}

%[1]sCheck(): int {
    var bad := 0
    for k in 0..%[2]d {
        bad += %[1]sOne(k)
    }
    return bad
}

const %[1]sCheckOk = [0; 1][%[1]sCheck()]
`, name, n, strings.Join(in, ", "), strings.Join(ok, ", "), strings.Join(bits, ", "))
}

// TestParseFloatEdges pins ParseFloat's grammar and rounding on
// literal cases at compile time.
func TestParseFloatEdges(t *testing.T) {
	t.Parallel()
	ok := func(s string, x float64) parseCase { return parseCase{s, true, math.Float64bits(x)} }
	bad := func(s string) parseCase { return parseCase{s: s} }
	cases := []parseCase{
		ok("1e-400", 0), ok("-1e-400", math.Copysign(0, -1)), ok("2e-324", 0), ok("3e-324", 5e-324),
		ok("5.", 5), ok(".5", 0.5), ok("5.e3", 5000), ok("Inf", math.Inf(1)), ok("+infinity", math.Inf(1)),
		ok("-inf", math.Inf(-1)), {"NaN", true, 0x7ff8000000000000}, {"nan", true, 0x7ff8000000000000},
		ok("0", 0), ok("-0", math.Copysign(0, -1)), ok("1.7976931348623157e308", math.MaxFloat64),
		ok("4.9406564584124654e-324", 5e-324), ok("0.1", 0.1), ok("1E2", 100), ok("00012.5000e-1", 1.25),
		ok("2.2250738585072011e-308", 2.2250738585072011e-308),
		bad("1e400"), bad("-1e400"), bad("1.7976931348623159e308"), bad("0x1p3"), bad("1_0"), bad("-nan"),
		bad("+nan"), bad("1e"), bad("."), bad("e5"), bad(" 1"), bad("1 "), bad(""), bad("+"), bad("-."),
		bad("1.2.3"), bad("1e+"), bad("infinit"), bad("infx"), bad("nana"), bad("1e5x"), bad("--1"),
	}
	for _, c := range cases {
		if g := goParse(c.s); g != c {
			t.Errorf("oracle disagrees on %q: %+v, want %+v", c.s, g, c)
		}
	}
	expectClean(t, "std02", parseFixture("Std02", cases))
}

// parseCorpus is seeded valid and invalid strings: Go's shortest output
// for random bits, long mantissas (19 to 800 digits, cut-off digits that
// decide the rounding), halfway cases, exponent extremes, mutations.
func parseCorpus(r *rand.Rand, n int) []string {
	var out []string
	for len(out) < n {
		b := r.Uint64()
		x := math.Float64frombits(b)
		switch r.Intn(8) {
		case 0, 1:
			out = append(out, strconv.FormatFloat(x, 'e', -1, 64))
		case 2:
			out = append(out, strconv.FormatFloat(x, 'g', 17+r.Intn(10), 64))
		case 3:
			// halfway between two adjacent floats, written exactly
			y := math.Nextafter(x, math.Inf(1))
			if !math.IsInf(y, 0) && !math.IsNaN(x) {
				h := strconv.FormatFloat(x, 'e', 40, 64)
				out = append(out, h, strconv.FormatFloat(y, 'e', 760, 64))
			}
		case 4:
			digits := 1 + r.Intn(40)
			var sb strings.Builder
			for i := 0; i < digits; i++ {
				sb.WriteByte(byte('0' + r.Intn(10)))
			}
			if r.Intn(2) == 0 {
				sb.WriteString("." + strconv.Itoa(r.Intn(1000)))
			}
			sb.WriteString("e" + strconv.Itoa(r.Intn(700)-350))
			out = append(out, sb.String())
		case 5:
			out = append(out, fmt.Sprintf("%d", r.Int63()>>uint(r.Intn(63))), fmt.Sprintf(".%de-%d", r.Int63(), r.Intn(330)))
		case 6:
			s := []byte(strconv.FormatFloat(x, 'e', -1, 64))
			s[r.Intn(len(s))] = "0123456789.eE+-_x "[r.Intn(18)]
			out = append(out, string(s))
		case 7:
			out = append(out, "9007199254740993", "9007199254740992.5", "1e23", "8.98846567431158e307",
				"2.4703282292062327e-324", "2.4703282292062328e-324", "123456789012345678901234567890e-10")
		}
	}
	return out
}

// TestParseFloatMatchesGo checks ParseFloat against the oracle: 300
// seeded strings at compile time, and 200,000 natively at release, where
// each line of stdin is parsed and its bits printed in hex.
func TestParseFloatMatchesGo(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewSource(3))
	var cases []parseCase
	for _, s := range parseCorpus(r, 300) {
		cases = append(cases, goParse(s))
	}
	expectClean(t, "s_g174_float", parseFixture("Pf", cases))

	p := parseProgram()
	res := runNative(t, buildWithModule(t, p.rel, p.module, p.src, "--release"), p.stdin)
	if res.code != 0 {
		t.Fatalf("exit %d\n%s", res.code, res.stderr)
	}
	t.Logf("native run: %v", res.took)
	compareLines(t, res.stdout, p.want)
}

// parseProgram is TestParseFloatMatchesGo's native program: 200,000 seeded strings on
// stdin, each parsed and its bits printed in hex.
func parseProgram() textProgram {
	lines := parseCorpus(rand.New(rand.NewSource(4)), 200000)
	src := `import std.zzbits

export Main(sys: &System): ReadError|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var buf: [65536]u8 = [0; 65536]
    var inp := sys.Stdin()
    for {
        l := try inp.Line(sys.arena)
        s := l else {
            break
        }
        v := s.ParseFloat() else {
            try Print(mut out, "err\n", ())
            continue
        }
        b := zzbits.F64Bits(v)
        try Print(mut out, "{b:x}\n", (b))
    }
    try out.Flush()
}
`
	src = strings.Replace(src, "    var buf: [65536]u8 = [0; 65536]\n", "", 1)
	var in, want strings.Builder
	for _, s := range lines {
		if strings.ContainsAny(s, "\n\r") {
			continue
		}
		in.WriteString(s + "\n")
		c := goParse(s)
		if c.ok {
			fmt.Fprintf(&want, "%x\n", c.bits)
		} else {
			want.WriteString("err\n")
		}
	}
	return textProgram{"parse", "zzbits/bits.mk", bitsModule, src, in.String(), want.String()}
}

// textProgram is one native differential program: an extra std module
// (rel, module; rel "" for none), the main source, stdin and the wanted
// stdout.
type textProgram struct {
	name, rel, module, src, stdin, want string
}

// buildWithModule is buildNative with one more std module, dir/file.mk
// relative to std/ (copyStd writes fixtures only into existing
// directories), and the files moduleCompanions names for it.
func buildWithModule(t *testing.T, rel, text, src string, flags ...string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{}
	if rel != "" {
		files[rel] = text
		for crel, ctext := range moduleCompanions[rel] {
			files[crel] = ctext
		}
	}
	stdDir := copyStd(t, root, nil)
	for rel, text := range files {
		p := filepath.Join(stdDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "main")
	if runtime.GOOS == "windows" && !slices.Contains(flags, "x86_64-linux") {
		exe += ".exe"
	}
	args := append([]string{"build"}, flags...)
	args = append(args, "-o", exe, "main.mk")
	out, status := mkCmd(t, root, stdDir, args...)
	if status != 0 {
		if strings.Contains(out, "no usable LLVM") {
			t.Skipf("needs the LLVM toolchain: %s", out)
		}
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	return exe
}

// textWSLPath is p, a Windows path, as WSL sees it (C:\x is /mnt/c/x).
func textWSLPath(p string) string {
	p = filepath.ToSlash(p)
	if len(p) > 2 && p[1] == ':' {
		return "/mnt/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

// TestSTDTEXT_LinuxNative runs the native differential programs of the
// std-text tests (float formatting, float parsing, the float remainder, case
// mapping, str.From at every alignment, Reader.Line's limits) as static x86_64-linux release executables in WSL, one wsl.exe
// client at a time. It is not parallel, so Go runs it while every parallel
// test is paused and no other wsl.exe client of this binary is in flight
// (run the package under tools/wsl-lock.ps1 with go test -p 1).
// Without WSL it skips, and fails under MK_GATE=1.
func TestSTDTEXT_LinuxNative(t *testing.T) {
	skip := func(why string) {
		if os.Getenv("MK_GATE") == "1" {
			t.Fatal(why)
		}
		t.Skip(why)
	}
	if runtime.GOOS != "windows" {
		skip("the Linux leg runs from Windows through wsl.exe")
	}
	linuxGate(t)
	if _, err := exec.LookPath("wsl.exe"); err != nil {
		skip("wsl.exe not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	wslGuard()
	err := exec.CommandContext(ctx, "wsl.exe", "-e", "true").Run()
	cancel()
	if err != nil {
		skip(fmt.Sprintf("`wsl.exe -e true` did not answer within 30 s: %v", err))
	}
	for _, p := range []textProgram{g183Program(), parseProgram(), remProgram(), caseProgram(goCase()), fromAlignProgram(), lineProgram(), emptyStrProgram()} {
		exe := buildWithModule(t, p.rel, p.module, p.src, "--release", "--target", "x86_64-linux")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		wslGuard()
		cmd := exec.CommandContext(ctx, "wsl.exe", "-e", textWSLPath(exe))
		cmd.Stdin = strings.NewReader(p.stdin)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		start := time.Now()
		err := cmd.Run()
		cancel()
		if err != nil {
			t.Fatalf("%s: wsl.exe: %v\n%s", p.name, err, errb.String())
		}
		t.Logf("%s on Linux: %v (wall, with wsl.exe)", p.name, time.Since(start))
		compareLines(t, out.String(), p.want)
	}
}
