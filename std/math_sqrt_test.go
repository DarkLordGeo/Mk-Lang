// Why: math.Sqrt's NaN guard tests the root's bits, not the argument's
// (std/math/math.mk), so it vectorises; the answers must not change.
// What: TestMath_SqrtBitsUnchanged: the canonical NaN for a NaN or a negative,
// -0 for -0, the correctly rounded root otherwise.
// How: Go's math.Sqrt, every NaN made 0x7FF8000000000000, judges roots taken in
// a vectorised loop (sqrtpd), one at a time, and by the compile-time evaluator.

package std

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// m11SqrtSpecials are the edge patterns: zeros, infinities, quiet and
// signalling NaNs of both signs with low and high payloads, subnormals and
// normals at both ends, of both signs.
var m11SqrtSpecials = []uint64{
	0x0000000000000000, 0x8000000000000000, 0x7FF0000000000000, 0xFFF0000000000000,
	0x7FF0000000000001, 0x7FF4000000000000, 0x7FF7FFFFFFFFFFFF, 0x7FF8000000000000,
	0x7FF8000000000001, 0x7FFFFFFFFFFFFFFF, 0xFFF0000000000001, 0xFFF4000000000000,
	0xFFF8000000000000, 0xFFF8000000000001, 0xFFFFFFFFFFFFFFFF, 0x0000000000000001,
	0x8000000000000001, 0x000FFFFFFFFFFFFF, 0x800FFFFFFFFFFFFF, 0x0010000000000000,
	0x8010000000000000, 0x7FEFFFFFFFFFFFFF, 0xFFEFFFFFFFFFFFFF, 0x3FF0000000000000,
	0xBFF0000000000000, 0x4000000000000000, 0xC000000000000000, 0x3FE0000000000000,
}

// m11SqrtRandom is how many splitmix64 patterns follow the specials.
const m11SqrtRandom = 4000

// m11SqrtProgram prints, per pattern, "V" and Sqrt's bits from the
// vectorisable loop, then "S" and the bits one call at a time through a
// call the optimiser cannot see through (the pattern xored with the
// argument count less one, which is 0), then "E" and the compile-time
// root of each special.
func m11SqrtProgram() string {
	var b strings.Builder
	b.WriteString(strings.Replace(h2Prelude, "import std.hash\n", "import std.math\n", 1))
	fmt.Fprintf(&b, "\nconst Specials: [%d]u64 = [", len(m11SqrtSpecials))
	for i, p := range m11SqrtSpecials {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "0x%016X", p)
	}
	b.WriteString("]\n")
	for i := range m11SqrtSpecials {
		fmt.Fprintf(&b, "const E%d: u64 = math.Sqrt(f64.FromBits(Specials[%d])).Bits()\n", i, i)
	}
	fmt.Fprintf(&b, "const N: int = %d\n", len(m11SqrtSpecials)+m11SqrtRandom)
	b.WriteString(`
HexU64(out: mut Writer, x: u64): WriteError!void {
    var w: [8]u8 = [0; 8]
    for k in 0..8 {
        w[k] = Trunc[u8](x >> (56 - 8 * k))
    }
    try Hex(out, w[..])
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    var out := sys.Stdout()
    z := Trunc[u64](sys.args.Len() - 1)
    xs := try sys.arena.Slice[f64](N)
    roots := try sys.arena.Slice[f64](N)
    var r := Rng(s: 11)
    for i in 0..N {
        p := if i < Specials.Len() { Specials[i] } else { r.Next() }
        xs[i] = f64.FromBits(p ^ z)
    }
    for i in 0..N {
        roots[i] = math.Sqrt(xs[i])
    }
    for i in 0..N {
        try Print(mut out, "V ")
        try HexU64(mut out, roots[i].Bits())
        try Print(mut out, " S ")
        try HexU64(mut out, math.Sqrt(f64.FromBits(xs[i].Bits() ^ z)).Bits())
        try Print(mut out, "\n")
    }
`)
	for i := range m11SqrtSpecials {
		fmt.Fprintf(&b, "    try Print(mut out, \"E \")\n    try HexU64(mut out, E%d)\n    try Print(mut out, \"\\n\")\n", i)
	}
	b.WriteString("}\n")
	return b.String()
}

// m11SqrtBits is math.Sqrt's answer for the pattern p.
func m11SqrtBits(p uint64) uint64 {
	r := math.Sqrt(math.Float64frombits(p))
	if math.IsNaN(r) {
		return 0x7FF8000000000000
	}
	return math.Float64bits(r)
}

// m11SqrtWant is the oracle's output for m11SqrtProgram.
func m11SqrtWant() string {
	var lines []string
	pats := append([]uint64{}, m11SqrtSpecials...)
	r := hmSplitmix{11}
	for range m11SqrtRandom {
		pats = append(pats, r.next())
	}
	for _, p := range pats {
		w := m11SqrtBits(p)
		lines = append(lines, fmt.Sprintf("V %016x S %016x", w, w))
	}
	for _, p := range m11SqrtSpecials {
		lines = append(lines, fmt.Sprintf("E %016x", m11SqrtBits(p)))
	}
	return strings.Join(lines, "\n")
}

// TestMath_SqrtBitsUnchanged: math.Sqrt gives m11SqrtBits on every
// special pattern and on random ones, vectorised and scalar, at dev and
// release, and at compile time; the oracle's own edge cases are checked
// first (the root of -0 is -0, of a negative or a NaN Nan's bits).
func TestMath_SqrtBitsUnchanged(t *testing.T) {
	t.Parallel()
	for p, w := range map[uint64]uint64{0x8000000000000000: 0x8000000000000000, 0xBFF0000000000000: 0x7FF8000000000000, 0xFFF8000000000001: 0x7FF8000000000000, 0x7FF0000000000000: 0x7FF0000000000000, 0x4010000000000000: 0x4000000000000000} {
		if got := m11SqrtBits(p); got != w {
			t.Fatalf("oracle: Sqrt(%#x) = %#x, want %#x", p, got, w)
		}
	}
	want := m11SqrtWant()
	for _, c := range []struct {
		name   string
		forces []sdForce
		flags  []string
	}{
		{"dev", nil, nil},
		{"release", nil, []string{"--release"}},
		{"old-guard-release", m11SqrtOldGuard, []string{"--release"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := h2Run(t, sdFixtures(t, c.forces), m11SqrtProgram(), c.flags...)
			diffLines(t, strings.TrimSuffix(got, "\n"), want)
		})
	}
}

// m11SqrtOldGuard restores the guard on the argument (main at 2a817da),
// so the test shows the oracle is the old form's too.
var m11SqrtOldGuard = []sdForce{{"math/math.mk", "    if r.Bits() & AbsMask64 > ExpMask64 {\n", "    b := x.Bits()\n    if b > ExpMask64 && b != SignBit64 {\n", 1}}
