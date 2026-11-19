// Why: std.math's basics and Rem64/Rem32, which codegen calls for a float %,
// must match IEEE results bit for bit.
// What: Min, Max, Abs, Sqrt and Rem64/Rem32 tests against Go's math package.
// How: at compile time through mk check and natively at release.

package std

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
)

// expectCleanIn is expectClean for a fixture file at rel (relative to std/),
// so a fixture can sit in a module other than std.core.
func expectCleanIn(t *testing.T, rel, fixture string) {
	t.Helper()
	m := checkConst.FindStringSubmatch(fixture)
	if m == nil {
		t.Fatalf("fixture %s has no check const", rel)
	}
	fixture += "\nconst Ran = [0; 1][" + m[1] + " + 1]\n"
	out, status := mkCheckWith(t, map[string]string{rel: fixture})
	witness := "std/" + rel + ":" + strconv.Itoa(strings.Count(fixture, "\n")) + ":"
	seen := 0
	for _, line := range strings.Split(out, "\n") {
		if !diagLine.MatchString(line) {
			continue
		}
		if strings.HasPrefix(line, witness) && strings.Contains(line, " E0901 ") {
			seen++
			continue
		}
		t.Errorf("unexpected diagnostic: %s", line)
	}
	if seen != 1 || status != 1 {
		t.Errorf("want only the witness's E0901 and exit 1; status %d, output:\n%s", status, out)
	}
}

// remEdges are operands whose remainders reach every branch: zeros,
// subnormals, infinities, NaN, equal magnitudes, huge exponent gaps.
func remEdges() []float64 {
	return []float64{0, math.Copysign(0, -1), 5e-324, -5e-324, 1e-310, 2.2250738585072014e-308, 1, -1, 1.5, 2,
		3, 7.5, -7.5, 0.1, 1e10, 1e300, math.MaxFloat64, -math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(),
		math.Pi, 1 << 53, 1<<53 + 2}
}

// goRemBits is Go's math.Mod with NaN canonical.
func goRemBits(a, b float64) uint64 {
	r := math.Mod(a, b)
	if math.IsNaN(r) {
		return 0x7ff8000000000000
	}
	return math.Float64bits(r)
}

func goRem32Bits(a, b float32) uint32 {
	r := float32(math.Mod(float64(a), float64(b)))
	if r != r {
		return 0x7fc00000
	}
	return math.Float32bits(r)
}

// TestMathCompileTime runs std.math in the evaluator: Min and Max
// on ints, floats and str; Abs and Sqrt on the IEEE edge cases; Rem64 and
// Rem32 on every pair of remEdges against Go's math.Mod.
func TestMathCompileTime(t *testing.T) {
	t.Parallel()
	var a, b, want []string
	var a32, b32, want32 []string
	for _, x := range remEdges() {
		for _, y := range remEdges() {
			a = append(a, fmt.Sprintf("0x%x", math.Float64bits(x)))
			b = append(b, fmt.Sprintf("0x%x", math.Float64bits(y)))
			want = append(want, fmt.Sprintf("0x%x", goRemBits(x, y)))
			x32, y32 := float32(x), float32(y)
			a32 = append(a32, fmt.Sprintf("0x%x", math.Float32bits(x32)))
			b32 = append(b32, fmt.Sprintf("0x%x", math.Float32bits(y32)))
			want32 = append(want32, fmt.Sprintf("0x%x", goRem32Bits(x32, y32)))
		}
	}
	n := len(a)
	sqrt := []float64{0, math.Copysign(0, -1), 2, 0.25, 1e-310, math.MaxFloat64, -1, math.Inf(1), math.Inf(-1), 3}
	var sx, sw []string
	for _, x := range sqrt {
		r := math.Sqrt(x)
		w := math.Float64bits(r)
		if math.IsNaN(r) {
			w = 0x7ff8000000000000
		}
		sx = append(sx, fmt.Sprintf("0x%x", math.Float64bits(x)))
		sw = append(sw, fmt.Sprintf("0x%x", w))
	}
	fixture := fmt.Sprintf(`import std.raw

const RemA: [%[1]d]u64 = [%[2]s]
const RemB: [%[1]d]u64 = [%[3]s]
const RemW: [%[1]d]u64 = [%[4]s]
const RemA32: [%[1]d]u32 = [%[5]s]
const RemB32: [%[1]d]u32 = [%[6]s]
const RemW32: [%[1]d]u32 = [%[7]s]
const SqrtX: [%[8]d]u64 = [%[9]s]
const SqrtW: [%[8]d]u64 = [%[10]s]

MathCheck(): int {
    var bad := 0
    for k in 0..%[1]d {
        if raw.F64Bits(Rem64(raw.F64From(RemA[k]), raw.F64From(RemB[k]))) != RemW[k] {
            bad += 1
        }
        if raw.F32Bits(Rem32(raw.F32From(RemA32[k]), raw.F32From(RemB32[k]))) != RemW32[k] {
            bad += 1
        }
    }
    for k in 0..%[8]d {
        if raw.F64Bits(Sqrt(raw.F64From(SqrtX[k]))) != SqrtW[k] {
            bad += 1
        }
    }
    if Min(3, 4) != 3 || Max(3, 4) != 4 || Min(-2.5, 1.0) != -2.5 || Max("ab", "b") != "b" || Min('x', 'a') != 'a' {
        bad += 1
    }
    if raw.F64Bits(Abs(-0.0)) != 0 || Abs(-3.5) != 3.5 || raw.F64Bits(Abs(raw.F64From(0xFFF0000000000000))) != 0x7FF0000000000000 {
        bad += 1
    }
    return bad
}

const MathOk = [0; 1][MathCheck()]
`, n, strings.Join(a, ", "), strings.Join(b, ", "), strings.Join(want, ", "), strings.Join(a32, ", "),
		strings.Join(b32, ", "), strings.Join(want32, ", "), len(sqrt), strings.Join(sx, ", "), strings.Join(sw, ", "))
	expectCleanIn(t, "math/zz_lib148.mk", fixture)
}

// remModule exposes std.math's private Rem64 and Rem32, and bit views, to a
// test program; it is a file of std.math itself.
const remModule = `import std.raw

export RemBits64(a: u64, b: u64): u64 {
    return raw.F64Bits(Rem64(raw.F64From(a), raw.F64From(b)))
}

export RemBits32(a: u32, b: u32): u32 {
    return raw.F32Bits(Rem32(raw.F32From(a), raw.F32From(b)))
}
`

// TestFloatRemMatchesGo computes, natively at release, the remainder
// of 300,000 seeded operand pairs at each width: random bit patterns, and
// every other pair with y's exponent 0 to 63 below x's, where the result
// is not simply x. The program draws the operands with splitmix64 and Go
// replays it; the bits must equal Go's math.Mod (canonical NaN).
func TestFloatRemMatchesGo(t *testing.T) {
	t.Parallel()
	p := remProgram()
	r := runNative(t, buildWithModule(t, p.rel, p.module, p.src, "--release"), p.stdin)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	t.Logf("native run: %v", r.took)
	compareLines(t, r.stdout, p.want)
}

// remProgram is TestFloatRemMatchesGo's program.
func remProgram() textProgram {
	const n = 300000
	mod := remModule + `
export Mix(s: u64): u64 {
    var z := s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// Near puts y's exponent k below x's, k from 0 to 63.
export Near(x: u64, y: u64, k: u64): u64 {
    e := (((x >> 52) & 0x7FF) -% (k & 63)) & 0x7FF
    return (y & 0x800FFFFFFFFFFFFF) | (e << 52)
}
`
	src := fmt.Sprintf(`import std.math

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var s: u64 = 0
    for i in 0..%d {
        s = s +%% 0x9E3779B97F4A7C15
        x := math.Mix(s)
        s = s +%% 0x9E3779B97F4A7C15
        var y := math.Mix(s)
        if i %% 2 == 1 {
            y = math.Near(x, y, x ^ y)
        }
        r := math.RemBits64(x, y)
        r32 := math.RemBits32(Trunc[u32](x >> 32), Trunc[u32](y >> 32))
        try Print(mut out, "{r:x} {r32:x}\n", (r, r32))
    }
    try out.Flush()
}
`, n)
	var want strings.Builder
	var s uint64
	for i := 0; i < n; i++ {
		x := splitmix64(&s)
		y := splitmix64(&s)
		if i%2 == 1 {
			e := ((x >> 52 & 0x7ff) - ((x ^ y) & 63)) & 0x7ff
			y = y&0x800FFFFFFFFFFFFF | e<<52
		}
		fmt.Fprintf(&want, "%x %x\n", goRemBits(math.Float64frombits(x), math.Float64frombits(y)),
			goRem32Bits(math.Float32frombits(uint32(x>>32)), math.Float32frombits(uint32(y>>32))))
	}
	return textProgram{"rem", "math/zz_rem.mk", mod, src, "", want.String()}
}
