// Why: str.ParseFloat must round correctly on very long texts and huge
// exponents, where Go 1.27's strconv (its origin) is wrong.
// What: long-text, long-offset and huge-exponent tests of str.ParseFloat.
// How: the oracle is math/big's exact Rat (ratParse), not strconv. The bugs
// were a point placed by the 800 kept digits and an exponent capped at 10000.

package std

import (
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// ratParse is the correctly rounded f64 of a decimal text in ParseFloat's
// grammar with a modest exponent (math/big's Rat, exact), with ParseFloat's
// rules: overflow is ParseError, underflow a zero with the text's sign.
func ratParse(t *testing.T, s string) parseCase {
	t.Helper()
	neg := strings.HasPrefix(s, "-")
	mag := strings.TrimLeft(s, "+-")
	if strings.HasSuffix(mag, ".") {
		mag += "0"
	}
	if i := strings.IndexAny(mag, "eE"); i > 0 && mag[i-1] == '.' {
		mag = mag[:i] + "0" + mag[i:]
	}
	r, ok := new(big.Rat).SetString(mag)
	if !ok {
		t.Fatalf("ratParse: %.40q is not a decimal", s)
	}
	f, _ := r.Float64()
	if math.IsInf(f, 0) {
		return parseCase{s: s}
	}
	b := math.Float64bits(f)
	if neg {
		b |= 1 << 63
	}
	return parseCase{s, true, b}
}

// exactDecimal is the exact decimal expansion of r > 0, whose denominator
// is a power of two: its significant digits and the decimal exponent of
// the last one (r = digits * 10^exp).
func exactDecimal(r *big.Rat) (string, int) {
	num, den := new(big.Int).Set(r.Num()), new(big.Int).Set(r.Denom())
	exp := 0
	ten := big.NewInt(10)
	for den.Cmp(big.NewInt(1)) != 0 {
		num.Mul(num, ten)
		exp--
		g := new(big.Int).GCD(nil, nil, num, den)
		num.Quo(num, g)
		den.Quo(den, g)
	}
	d := num.String()
	for strings.HasSuffix(d, "0") && len(d) > 1 {
		d = d[:len(d)-1]
		exp++
	}
	return d, exp
}

// longTextCases are the long texts and long offsets that a Mk const can
// hold and the native program can read (lines under LineMax, 65,536).
func longTextCases(t *testing.T, seeded int) []parseCase {
	t.Helper()
	var ss []string
	half := "9007199254740993" // 2^53 + 1: halfway between two f64
	for _, k := range []int{0, 10, 784, 785, 790, 799, 800, 801, 900, 2000} {
		z := strings.Repeat("0", k)
		ss = append(ss,
			half+z+"1"+"e-"+strconv.Itoa(k+1), // above halfway: up
			half+z+"e-"+strconv.Itoa(k),       // exactly halfway: even, down
			"-"+half+z+"1"+"e-"+strconv.Itoa(k+1),
			half+z+"1."+"e-"+strconv.Itoa(k+1),
			half+z+"."+"1"+"e-"+strconv.Itoa(k), // the point after the zeros
			"0."+strings.Repeat("0", k)+half+"1e"+strconv.Itoa(k+16),
		)
	}
	for _, k := range []int{9999, 10000, 10001, 20000, 60000} {
		z := strings.Repeat("0", k)
		ss = append(ss,
			"0."+z+"1e"+strconv.Itoa(k+5),       // 1e4
			"1"+z+"e-"+strconv.Itoa(k),          // 1
			"-0."+z+"15e"+strconv.Itoa(k+1),     // -1.5
			"1"+z+"e-"+strconv.Itoa(k+400),      // 1e-400: zero
			"1"+z+"e-"+strconv.Itoa(k-400),      // 1e400: ParseError
			"0."+z+"25e"+strconv.Itoa(k-300),    // 2.5e-301
			"0."+z+half+"1e"+strconv.Itoa(k+16), // above halfway, behind zeros
		)
	}
	var cases []parseCase
	for _, s := range ss {
		cases = append(cases, ratParse(t, s))
	}
	// Seeded halfway points between neighbouring f64, written exactly with a
	// long integer part (the point moved right by the exponent's offset),
	// then nudged just above with a far "1": only the Decimal can decide.
	r := rand.New(rand.NewSource(51))
	for i := 0; i < seeded; i++ {
		var x float64
		for {
			x = math.Float64frombits(r.Uint64() &^ (1 << 63))
			if !math.IsNaN(x) && !math.IsInf(x, 0) && x != 0 && math.Nextafter(x, math.Inf(1)) < math.MaxFloat64 {
				break
			}
		}
		lo := new(big.Rat).SetFloat64(x)
		hi := new(big.Rat).SetFloat64(math.Nextafter(x, math.Inf(1)))
		h := new(big.Rat).Add(lo, hi)
		h.Quo(h, big.NewRat(2, 1))
		d, exp := exactDecimal(h)
		pad := 801 + r.Intn(200) - len(d)
		if pad < 0 {
			pad = r.Intn(50)
		}
		z := strings.Repeat("0", pad)
		e := exp - pad
		s := d + z
		if i%2 == 1 {
			s += "1"
			e--
		}
		cases = append(cases, ratParse(t, s+"e"+strconv.Itoa(e)))
	}
	return cases
}

// TestSTDTEXT_ParseFloatLongText: ParseFloat is correctly rounded on
// texts whose significant digits before the point pass the Decimal's 800,
// and on exponents that offset more than 10000 leading zeros or digits.
// At compile time on a short list, natively (dev and release) on all.
// Before the fix, the first case was 9.007199254740992e-102, as in Go.
func TestSTDTEXT_ParseFloatLongText(t *testing.T) {
	t.Parallel()
	cases := longTextCases(t, 400)
	wrong := 0
	for _, c := range cases {
		g := goParse(c.s)
		if g != c {
			wrong++
		}
	}
	t.Logf("%d of %d cases: Go 1.27 strconv.ParseFloat differs from the correctly rounded value", wrong, len(cases))
	if wrong == 0 {
		t.Errorf("no case exercises the Go 1.27 behaviour this test guards against")
	}
	// At compile time: the k = 0 and 10 texts, and one of k = 801 (each
	// Decimal shift walks 800 digits, so more would exhaust the 4M fuel).
	short := append(append([]parseCase(nil), cases[:12]...), cases[6*7])
	if !strings.HasPrefix(short[12].s, "9007199254740993"+strings.Repeat("0", 801)+"1e") {
		t.Fatalf("case order changed: %.40q", short[12].s)
	}
	expectClean(t, "stdtext_m5_long", parseFixture("M5pf", short))

	var in, want strings.Builder
	for _, c := range cases {
		in.WriteString(c.s + "\n")
		if c.ok {
			fmt.Fprintf(&want, "%x\n", c.bits)
		} else {
			want.WriteString("err\n")
		}
	}
	p := parseProgram()
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildWithModule(t, p.rel, p.module, p.src, flags...), in.String())
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		t.Logf("%v: %d texts, %d bytes, %v", flags, len(cases), in.Len(), res.took)
		compareLines(t, res.stdout, want.String())
	}
}

// TestSTDTEXT_ParseFloatLongOffset: the exponent cap shows only with an
// exponent of six digits offsetting as many zeros, past what stdin's LineMax lets the
// program above read, so these texts are string literals of the program.
// Before the fix the first printed 0 and the second err; Go 1.27 agrees
// with that.
func TestSTDTEXT_ParseFloatLongOffset(t *testing.T) {
	t.Parallel()
	half := "9007199254740993"
	z := strings.Repeat("0", 100000)
	texts := []string{
		"0." + z + "1e100005",
		"1" + z + "e-100000",
		"-0." + z + "15e100001",
		"0." + z + half + "1e100016",
		half + z + "1e-100001",
		"1" + z + "e-99600",
	}
	var src, want strings.Builder
	src.WriteString("P(out: mut Writer, s: str): WriteError!void {\n    v := s.ParseFloat() else {\n        try out.Str(\"err\\n\")\n        return\n    }\n    b := zzbits.F64Bits(v)\n    try Print(out, \"{b:x}\\n\", (b))\n}\n\n")
	src.WriteString("export Main(sys: &System): WriteError!void {\n    var out := sys.Stdout()\n")
	for _, s := range texts {
		fmt.Fprintf(&src, "    try P(mut out, \"%s\")\n", s)
		c := ratParse(t, s)
		if c.ok {
			fmt.Fprintf(&want, "%x\n", c.bits)
		} else {
			want.WriteString("err\n")
		}
		if g := goParse(s); g == c {
			t.Logf("Go 1.27 agrees on %.24q...", s)
		}
	}
	src.WriteString("    try out.Flush()\n}\n")
	prog := "import std.zzbits\n\n" + src.String()
	for _, flags := range [][]string{nil, {"--release"}} {
		res := runNative(t, buildWithModule(t, "zzbits/bits.mk", bitsModule, prog, flags...), "")
		if res.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, res.code, res.stderr)
		}
		compareLines(t, res.stdout, want.String())
	}
}

// TestSTDTEXT_ParseFloatHugeExponent: an exponent with more digits than
// any int holds saturates, so it still overflows or underflows (no wrap).
func TestSTDTEXT_ParseFloatHugeExponent(t *testing.T) {
	t.Parallel()
	cases := []parseCase{
		{"1e99999999999999999999", false, 0},
		{"1e-99999999999999999999", true, 0},
		{"-1e-99999999999999999999", true, 1 << 63},
		{"0e99999999999999999999", true, 0},
		{"-0.0e+99999999999999999999", true, 1 << 63},
		{"123456789012345678901234567890e-99999999999999999999999999", true, 0},
		{"1e9223372036854775807", false, 0},
		{"1e-9223372036854775808", true, 0},
	}
	expectClean(t, "stdtext_m5_hugeexp", parseFixture("M5he", cases))
}
