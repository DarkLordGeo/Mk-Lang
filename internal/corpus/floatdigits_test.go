// Why: Mk prints floats with its own port of Go's shortest-digits algorithm,
// and every printed float must match Go's digits exactly.
// What: TestDiffFloatDigits (native and evaluator halves) and TestOracle.
// How: 100,000 random patterns of each width plus edge cases, run over a std
// copy with two helper files added; the oracle is strconv.FormatFloat.
//
// TestDiffFloatDigits: a native program reads f64 and f32 bit patterns from
// standard input and prints each with Print, and every line must be Go's
// shortest digits, strconv.FormatFloat(x, 'e', -1, 64 or 32), laid out as
// Mk prints floats (fixed notation for a decimal exponent from -4 to 20,
// else d.ddde+XX or d.ddde-XX with at least two exponent digits; nan, inf,
// -inf, -0). The patterns are 100,000 of each width from floatDigitsSeed
// plus the edges (zeros, subnormals, extremes, nan, inf, powers of ten and
// their neighbours). The evaluator's Format is checked against the same
// oracle at compile time.
//
// User code has no float bit view (std.raw is std-internal), so the test
// runs over a copy of std with two files added: std/zzdigits/bits.mk, a
// module that turns bits into floats for the program, and
// std/core/zz_corpusdigits.mk, the compile-time harness (a Writer over a
// frame buffer, as std's own float tests use). Neither file is in the
// repository's std.

package corpus

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"mk/internal/diag"
	"mk/internal/driver"
)

// floatDigitsSeed seeds the random patterns ("mk-digit" in ASCII).
const floatDigitsSeed = 0x6d6b2d6469676974

// floatDigitsRandom is the number of random patterns of each width.
const floatDigitsRandom = 100000

// layoutDigits lays out Go's shortest digits s = strconv.FormatFloat(x, 'e', -1,
// bits) as Mk prints floats.
func layoutDigits(s string) string {
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
	mant, exps, ok := strings.Cut(s, "e")
	e, err := strconv.Atoi(exps)
	if !ok || err != nil {
		panic("layoutDigits: not an 'e' form: " + s)
	}
	digits := strings.Replace(mant, ".", "", 1)
	var out string
	switch {
	case digits == "0":
		out = "0"
	case e >= -4 && e <= 20:
		point := e + 1 // digits before the decimal point
		switch {
		case point <= 0:
			out = "0." + strings.Repeat("0", -point) + digits
		case point >= len(digits):
			out = digits + strings.Repeat("0", point-len(digits))
		default:
			out = digits[:point] + "." + digits[point:]
		}
	default:
		sign := "+"
		if e < 0 {
			sign, e = "-", -e
		}
		out = fmt.Sprintf("%se%s%02d", mant, sign, e)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// digitsWant is the oracle's text for bit pattern b, an f64 when wide, else
// an f32 in the low 32 bits.
func digitsWant(b uint64, wide bool) string {
	if wide {
		return layoutDigits(strconv.FormatFloat(math.Float64frombits(b), 'e', -1, 64))
	}
	return layoutDigits(strconv.FormatFloat(float64(math.Float32frombits(uint32(b))), 'e', -1, 32))
}

// digitsMix is splitmix64's output step; *s advances by its constant.
func digitsMix(s *uint64) uint64 {
	*s += 0x9E3779B97F4A7C15
	z := *s
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// digitsEdges64 and digitsEdges32 are the edge patterns of each width.
func digitsEdges64() []uint64 {
	xs := []float64{0, math.Copysign(0, -1), math.SmallestNonzeroFloat64, 2 * math.SmallestNonzeroFloat64,
		math.Float64frombits(0x000FFFFFFFFFFFFF), 0x1p-1022, math.MaxFloat64, -math.MaxFloat64,
		math.Inf(1), math.Inf(-1), 1, -1, 0.5, 0.1, 0.2, 0.3, 1.0 / 3, 2.0 / 3, math.Pi, math.E,
		1 << 53, 1<<53 + 2, 1e21 - 65536, 123456789012345680}
	for e := -323; e <= 308; e++ {
		x := math.Pow(10, float64(e))
		xs = append(xs, x, math.Nextafter(x, 0), math.Nextafter(x, math.Inf(1)), -x)
	}
	for e := -1074; e <= 1023; e += 29 {
		xs = append(xs, math.Ldexp(1, e), math.Ldexp(1.75, e))
	}
	bs := make([]uint64, 0, len(xs)+4)
	for _, x := range xs {
		bs = append(bs, math.Float64bits(x))
	}
	// NaNs: quiet, signalling, with the sign bit, the largest payload.
	return append(bs, 0x7FF8000000000000, 0x7FF0000000000001, 0xFFF8000000000000, 0x7FFFFFFFFFFFFFFF)
}

func digitsEdges32() []uint64 {
	xs := []float32{0, float32(math.Copysign(0, -1)), math.SmallestNonzeroFloat32, math.Float32frombits(0x007FFFFF),
		0x1p-126, math.MaxFloat32, -math.MaxFloat32, float32(math.Inf(1)), float32(math.Inf(-1)),
		1, -1, 0.1, 1.0 / 3, 16777216, 16777218, 3.4028235e38}
	for e := -45; e <= 38; e++ {
		x := float32(math.Pow(10, float64(e)))
		xs = append(xs, x, math.Nextafter32(x, 0), math.Nextafter32(x, float32(math.Inf(1))), -x)
	}
	bs := make([]uint64, 0, len(xs)+4)
	for _, x := range xs {
		bs = append(bs, uint64(math.Float32bits(x)))
	}
	return append(bs, 0x7FC00000, 0x7F800001, 0xFFC00000, 0x7FFFFFFF)
}

// digitsPatterns is every pattern of the test: the edges, then the random
// ones (the f32 ones are the high halves of a second stream).
func digitsPatterns() (wide, narrow []uint64) {
	wide, narrow = digitsEdges64(), digitsEdges32()
	s64, s32 := uint64(floatDigitsSeed), uint64(floatDigitsSeed)^0xFFFFFFFFFFFFFFFF
	for range floatDigitsRandom {
		wide = append(wide, digitsMix(&s64))
		narrow = append(narrow, digitsMix(&s32)>>32)
	}
	return wide, narrow
}

// digitsBitsModule is std/zzdigits/bits.mk: bits as an i64, the form
// str.ParseInt gives, to a float.
const digitsBitsModule = `import std.raw

export F64Of(v: i64): f64 {
    if v < 0 {
        return raw.F64From(Cast[u64](v + 9223372036854775807 + 1) | 0x8000000000000000)
    }
    return raw.F64From(Cast[u64](v))
}

export F32Of(v: i64): f32 {
    return raw.F32From(Cast[u32](v))
}
`

// digitsProgram reads a count n, then n f64 patterns, then f32 patterns to
// the end, one decimal i64 per line, and prints each float with Print.
const digitsProgram = `import std.zzdigits

export Main(sys: &System): ReadError|OutOfMemory|WriteError|ParseError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    var left: i64 = -1
    for {
        l := try inp.Line(sys.arena)
        s := l else {
            break
        }
        v := try s.ParseInt()
        if left < 0 {
            left = v
        } else if left > 0 {
            x := zzdigits.F64Of(v)
            try Print(mut out, "{x}\n", (x))
            left -= 1
        } else {
            y := zzdigits.F32Of(v)
            try Print(mut out, "{y}\n", (y))
        }
    }
    try out.Flush()
}
`

// digitsInput is the program's standard input and the expected output.
func digitsInput(wide, narrow []uint64) (in, want string) {
	var i, w strings.Builder
	fmt.Fprintf(&i, "%d\n", len(wide))
	for _, b := range wide {
		fmt.Fprintf(&i, "%d\n", int64(b))
		w.WriteString(digitsWant(b, true) + "\n")
	}
	for _, b := range narrow {
		fmt.Fprintf(&i, "%d\n", int64(b))
		w.WriteString(digitsWant(b, false) + "\n")
	}
	return i.String(), w.String()
}

// digitsHarness is std/core/zz_corpusdigits.mk without its cases: a Writer
// over a frame buffer whose table refuses to grow, and DigitsIs, 0 when bits
// b format (spec 0, Format's) as want.
const digitsHarness = `import std.raw

const DigitsTable = WriterTable(write: DigitsFull, flush: DigitsFlush)

DigitsFull(w: mut Writer, b: []u8): WriteError!void {
    fail WriteError
}

DigitsFlush(w: mut Writer): WriteError!void {
}

DigitsWriter(buf: mut []u8): Writer from buf {
    unsafe "corpus fixture: the writer's buffer is buf" {
        return Writer(io: InertIoValue(), table: &DigitsTable, ctx: raw.DataMut(buf), buf: raw.DataMut(buf), used: 0, cap: buf.Len())
    }
}

DigitsIs(b: u64, wide: bool, want: str): int {
    var buf: [64]u8 = [0; 64]
    var w := DigitsWriter(mut buf[..])
    FmtFloatBits(mut w, b, wide, 0) else {
        return 1
    }
    if StrOf(w.buf, w.used) != want {
        return 1
    }
    return 0
}
`

// digitsEvalChunk is the number of cases per check const, so each const's
// evaluation stays well inside the default fuel of 4 million steps.
const digitsEvalChunk = 40

type digitsCase struct {
	bits uint64
	wide bool
	want string
}

// digitsFixture is the compile-time file: the harness, then per chunk of
// cases a check function and `const DigitsOkK = [0; 1][DigitsCheckK()]`,
// which is an E0901 exactly when a case of the chunk differs. Two more
// consts must draw E0901: DigitsRan (chunk 0's result plus one, so the
// checks ran) and DigitsPlanted (a case whose want is wrong, so DigitsIs
// can fail). lines maps each const's line to its chunk (-1 Ran, -2
// Planted).
func digitsFixture(cases []digitsCase) (src string, lines map[int]int) {
	var b strings.Builder
	b.WriteString(digitsHarness)
	lines = map[int]int{}
	emit := func(name string, cs []digitsCase) {
		var bits, wide, want []string
		for _, c := range cs {
			bits = append(bits, fmt.Sprintf("0x%x", c.bits))
			wide = append(wide, strconv.FormatBool(c.wide))
			want = append(want, strconv.Quote(c.want))
		}
		fmt.Fprintf(&b, "\nconst %[1]sBits: [%[2]d]u64 = [%[3]s]\nconst %[1]sWide: [%[2]d]bool = [%[4]s]\nconst %[1]sWant: [%[2]d]str = [%[5]s]\n\n%[1]sCheck(): int {\n    var bad := 0\n    for k in 0..%[2]d {\n        bad += DigitsIs(%[1]sBits[k], %[1]sWide[k], %[1]sWant[k])\n    }\n    return bad\n}\n",
			name, len(cs), strings.Join(bits, ", "), strings.Join(wide, ", "), strings.Join(want, ", "))
	}
	line := func() int { return strings.Count(b.String(), "\n") + 1 }
	n := 0
	for k := 0; k < len(cases); k += digitsEvalChunk {
		name := "Digits" + strconv.Itoa(n)
		emit(name, cases[k:min(k+digitsEvalChunk, len(cases))])
		b.WriteString("\n")
		lines[line()] = n
		fmt.Fprintf(&b, "const %sOk = [0; 1][%sCheck()]\n", name, name)
		n++
	}
	b.WriteString("\n")
	lines[line()] = -1
	b.WriteString("const DigitsRan = [0; 1][Digits0Check() + 1]\n")
	emit("DigitsPlant", []digitsCase{{math.Float64bits(1), true, "1.0"}})
	b.WriteString("\n")
	lines[line()] = -2
	b.WriteString("const DigitsPlanted = [0; 1][DigitsPlantCheck()]\n")
	return b.String(), lines
}

// digitsStd copies the repository's std (every .mk and .def file) to a new
// directory and adds files (std-relative path to text).
func digitsStd(t *testing.T, files map[string]string) string {
	t.Helper()
	from := filepath.Join(repoRoot(t), "std")
	to := filepath.Join(t.TempDir(), "std")
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".mk") || strings.HasSuffix(p, ".def")) {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return copyFile(p, dst, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, text := range files {
		p := filepath.Join(to, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return to
}

// TestOracle pins the oracle's layout on both sides of each threshold,
// so TestDiffFloatDigits never compares with a wrong oracle.
func TestOracle(t *testing.T) {
	for _, c := range []struct {
		x    float64
		wide bool
		want string
	}{
		{1e20, true, "100000000000000000000"}, {1e21, true, "1e+21"}, {123456789, true, "123456789"},
		{0.0001, true, "0.0001"}, {0.00001, true, "1e-05"}, {1.5e-7, true, "1.5e-07"}, {1e100, true, "1e+100"},
		{5e-324, true, "5e-324"}, {math.MaxFloat64, true, "1.7976931348623157e+308"}, {0, true, "0"},
		{math.Copysign(0, -1), true, "-0"}, {-2.5, true, "-2.5"}, {0.1, true, "0.1"},
		{math.NaN(), true, "nan"}, {math.Inf(1), true, "inf"}, {math.Inf(-1), true, "-inf"},
		{0.1, false, "0.1"}, {16777216, false, "16777216"}, {3.4028234663852886e38, false, "3.4028235e+38"},
		{1e-45, false, "1e-45"},
	} {
		b := math.Float64bits(c.x)
		if !c.wide {
			b = uint64(math.Float32bits(float32(c.x)))
		}
		if got := digitsWant(b, c.wide); got != c.want {
			t.Errorf("oracle(%v, wide %v) = %q, want %q", c.x, c.wide, got, c.want)
		}
	}
}

// TestDiffFloatDigits: the native half on x86_64-windows and x86_64-linux,
// dev and release; the evaluator half once (the evaluator is the same Go
// code for every target).
func TestDiffFloatDigits(t *testing.T) {
	wide, narrow := digitsPatterns()
	t.Run("eval", func(t *testing.T) {
		t.Parallel()
		// The edges and the first 1,000 random patterns of each width.
		var cases []digitsCase
		for _, b := range wide[:len(wide)-floatDigitsRandom+1000] {
			cases = append(cases, digitsCase{b, true, digitsWant(b, true)})
		}
		for _, b := range narrow[:len(narrow)-floatDigitsRandom+1000] {
			cases = append(cases, digitsCase{b, false, digitsWant(b, false)})
		}
		digitsEval(t, cases, -1)
		// Liveness inside a real chunk: one random f64 case's want with its
		// last digit changed must draw exactly its chunk's E0901.
		k := len(digitsEdges64()) + 500
		bad := slices.Clone(cases)
		w := []byte(bad[k].want)
		for i := len(w) - 1; i >= 0; i-- {
			if w[i] >= '0' && w[i] <= '9' {
				w[i] = '0' + (w[i]-'0'+1)%10
				break
			}
		}
		bad[k].want = string(w)
		t.Run("planted", func(t *testing.T) { digitsEval(t, bad, k/digitsEvalChunk) })
	})
	t.Run("native", func(t *testing.T) {
		t.Parallel()
		tools := needLLVM(t)
		dir := t.TempDir()
		in, want := digitsInput(wide, narrow)
		if err := os.WriteFile(filepath.Join(dir, "digits.mk"), []byte(digitsProgram), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "digits.in"), []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		std := digitsStd(t, map[string]string{"zzdigits/bits.mk": digitsBitsModule})
		r := NewRunner(dir, driver.Config{Tools: tools, CacheDir: t.TempDir(), StdDir: std}, t.TempDir())
		r.WSLSlots = testWSLSlots
		for _, target := range []string{"x86_64-windows", "x86_64-linux"} {
			for _, prof := range []string{"dev", "release"} {
				t.Run(target+"/"+prof, func(t *testing.T) {
					t.Parallel()
					// No build for a target this host cannot run: each build
					// of the copied std takes gigabytes.
					if _, err := runArgv(target, dir, "digits", nil, 0); ErrNoRunner(err) {
						skipOrFatal(t, gateRun(), "%v", err)
					}
					if _, err := r.Build("digits.mk", target, prof); err != nil {
						t.Fatal(err)
					}
					needWSLRun(t, target, true)
					start := time.Now()
					got, err := r.Run(ProgramCase{Program: "digits.mk", Stdin: "digits.in", Timeout: 120 * time.Second}, target, prof)
					switch {
					case ErrNoRunner(err):
						if gateRun() {
							t.Fatal(err)
						}
						t.Skip(err)
					case err != nil:
						t.Fatal(err)
					case got.TimedOut:
						t.Fatalf("timed out after 120s")
					case got.Exit != 0 || len(got.Stderr) > 0:
						t.Fatalf("exit %d, stderr %q", got.Exit, got.Stderr)
					}
					t.Logf("%d f64 and %d f32 patterns in %v", len(wide), len(narrow), time.Since(start).Round(time.Millisecond))
					digitsCompare(t, string(got.Stdout), want, wide, narrow)
				})
			}
		}
	})
}

// digitsCompare reports the first ten lines that differ, with their bits.
func digitsCompare(t *testing.T, got, want string, wide, narrow []uint64) {
	t.Helper()
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	if len(g) != len(w) {
		t.Errorf("%d lines, want %d", len(g)-1, len(w)-1)
	}
	bad := 0
	for i := range min(len(g), len(w)) {
		if g[i] == w[i] {
			continue
		}
		bad++
		if bad <= 10 {
			what := "?"
			switch {
			case i < len(wide):
				what = fmt.Sprintf("f64 0x%016x", wide[i])
			case i < len(wide)+len(narrow):
				what = fmt.Sprintf("f32 0x%08x", narrow[i-len(wide)])
			}
			t.Errorf("line %d, %s: got %q, want %q", i+1, what, g[i], w[i])
		}
	}
	if bad > 10 {
		t.Errorf("... %d lines differ in all", bad)
	}
}

// digitsEval checks the fixture with mk check's session over the std copy:
// DigitsRan and DigitsPlanted draw E0901, and so does the check const of
// chunk bad (-1: none); nothing else is reported.
func digitsEval(t *testing.T, cases []digitsCase, bad int) {
	t.Helper()
	src, lines := digitsFixture(cases)
	const rel = "core/zz_corpusdigits.mk"
	std := digitsStd(t, map[string]string{rel: src})
	main := filepath.Join(t.TempDir(), "main.mk")
	if err := os.WriteFile(main, []byte("export Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := driver.NewSession(driver.Config{Root: main, StdDir: std, NoCache: true})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := s.Check()
	t.Logf("%d cases in %d consts checked in %v", len(cases), len(lines)-2, time.Since(start).Round(time.Millisecond))
	// The report names a std file by its std-relative path, "std/" + rel.
	ran, planted, badSeen := false, false, false
	for _, e := range res.Report.Diags {
		d := e.Diag
		pos := res.Sources.Position(d.At.StartPos())
		chunk, at := lines[int(pos.Line)]
		if d.Code != diag.E0901 || filepath.ToSlash(pos.Path) != "std/"+rel || !at {
			t.Errorf("unexpected diagnostic: %s:%d:%d: %s %s", pos.Path, pos.Line, pos.Col, d.Code, d.Message)
			continue
		}
		switch {
		case chunk == -1:
			ran = true
		case chunk == -2:
			planted = true
		case chunk == bad:
			badSeen = true
		default:
			k := chunk * digitsEvalChunk
			var which []string
			for _, c := range cases[k:min(k+digitsEvalChunk, len(cases))] {
				which = append(which, fmt.Sprintf("0x%x (wide %v) %q", c.bits, c.wide, c.want))
			}
			t.Errorf("const Digits%dOk: E0901 %s; one of these cases differs from the oracle: %s", chunk, d.Message, strings.Join(which, ", "))
		}
	}
	if !ran {
		t.Error("DigitsRan drew no E0901: the check consts were not evaluated")
	}
	if !planted {
		t.Error("DigitsPlanted drew no E0901: DigitsIs cannot tell a wrong text")
	}
	if bad >= 0 && !badSeen {
		t.Errorf("const Digits%dOk drew no E0901, though one of its wants was changed", bad)
	}
}
