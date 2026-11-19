// Why: math.Rng promises xoshiro256** seeded by SplitMix64, so its streams
// must match the reference bit for bit.
// What: TestSTDMISS_RngMatchesModel (U64, Below, Float and Fill over fixed
// and random seeds) and TestSTDMISS_RngBelowUniform (a chi-square test).
// How: a Go model transcribed from Blackman and Vigna's reference code;
// Below uses Lemire's method, so the draws are the same.

package std

import (
	"fmt"
	"math"
	"math/bits"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

type xoshiro struct{ s [4]uint64 }

func missSplitmix(x *uint64) uint64 {
	*x += 0x9E3779B97F4A7C15
	z := *x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func newXoshiro(seed uint64) *xoshiro {
	r := &xoshiro{}
	for i := range r.s {
		r.s[i] = missSplitmix(&seed)
	}
	return r
}

func (r *xoshiro) u64() uint64 {
	s := &r.s
	res := bits.RotateLeft64(s[1]*5, 7) * 9
	t := s[1] << 17
	s[2] ^= s[0]
	s[3] ^= s[1]
	s[1] ^= s[2]
	s[0] ^= s[3]
	s[2] ^= t
	s[3] = bits.RotateLeft64(s[3], 45)
	return res
}

func (r *xoshiro) below(n uint64) uint64 {
	hi, lo := bits.Mul64(r.u64(), n)
	if lo < n {
		t := -n % n
		for lo < t {
			hi, lo = bits.Mul64(r.u64(), n)
		}
	}
	return hi
}

func (r *xoshiro) float() float64 { return float64(r.u64()>>11) * 0x1p-53 }

var missBelowNs = []uint64{1, 2, 3, 7, 10, 1000, 1 << 32, 1<<63 + 1, math.MaxUint64}

func TestSTDMISS_RngMatchesModel(t *testing.T) {
	seeds := []uint64{0, 1, 42, math.MaxUint64, 0x123456789ABCDEF0}
	src := rand.New(rand.NewPCG(1, 2))
	for range 5 {
		seeds = append(seeds, src.Uint64())
	}
	var ns []string
	for _, n := range missBelowNs {
		ns = append(ns, strconv.FormatUint(n, 10))
	}
	var ss []string
	for _, s := range seeds {
		ss = append(ss, strconv.FormatUint(s, 10))
	}
	prog := `import std.math

const Seeds: []u64 = [` + strings.Join(ss, ", ") + `]
const Ns: []u64 = [` + strings.Join(ns, ", ") + `]

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for s in Seeds {
        var r := math.Rng.New(s)
        for _ in 0..16 {
            try Print(mut out, "u {x}\n", (x: r.U64()))
        }
        for n in Ns {
            for _ in 0..4 {
                try Print(mut out, "b {x}\n", (x: r.Below(n)))
            }
        }
        for _ in 0..8 {
            try Print(mut out, "f {x}\n", (x: r.Float()))
        }
        var buf: [21]u8 = [0; 21]
        r.Fill(mut buf[..])
        for b in buf {
            try Print(mut out, "y {x}\n", (x: b))
        }
    }
}
`
	exe := missBuild(t, prog)
	got := strings.Split(strings.TrimSuffix(missRun(t, exe, t.TempDir()), "\n"), "\n")
	var want []string
	for _, s := range seeds {
		r := newXoshiro(s)
		for range 16 {
			want = append(want, fmt.Sprintf("u %d", r.u64()))
		}
		for _, n := range missBelowNs {
			for range 4 {
				want = append(want, fmt.Sprintf("b %d", r.below(n)))
			}
		}
		for range 8 {
			want = append(want, fmt.Sprintf("f %v", r.float()))
		}
		var buf [21]byte
		for i := 0; i < len(buf); i += 8 {
			x := r.u64()
			for k := 0; k < 8 && i+k < len(buf); k++ {
				buf[i+k] = byte(x >> (8 * k))
			}
		}
		for _, b := range buf {
			want = append(want, fmt.Sprintf("y %d", b))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if strings.HasPrefix(w, "f ") {
			// Mk prints the shortest round-trip digits; compare values.
			gv, err1 := strconv.ParseFloat(strings.TrimPrefix(g, "f "), 64)
			wv, _ := strconv.ParseFloat(strings.TrimPrefix(w, "f "), 64)
			if err1 != nil || gv != wv {
				t.Errorf("line %d: got %q, want %q", i, g, w)
			}
			continue
		}
		if g != w {
			t.Errorf("line %d: got %q, want %q", i, g, w)
		}
	}
}

// Below(10) over 1,000,000 draws: chi-square with 9 degrees of freedom
// below 27.88 (p = 0.001), and Below(3) the same with 2 degrees below 13.82.
func TestSTDMISS_RngBelowUniform(t *testing.T) {
	prog := `import std.math

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    var r := math.Rng.New(2026)
    var c10: [10]int = [0; 10]
    for _ in 0..1000000 {
        c10[Cast[int](r.Below(10))] += 1
    }
    for c in c10 {
        try Print(mut out, "{c}\n", (c: c))
    }
}
`
	exe := missBuild(t, prog, "--release")
	lines := strings.Fields(missRun(t, exe, t.TempDir()))
	if len(lines) != 10 {
		t.Fatalf("got %q", lines)
	}
	var chi float64
	for _, l := range lines {
		c, _ := strconv.Atoi(l)
		d := float64(c) - 100000
		chi += d * d / 100000
	}
	if chi > 27.88 {
		t.Errorf("chi-square %.2f over 9 degrees of freedom: Below(10) is not uniform (%v)", chi, lines)
	}
	t.Logf("chi-square %.2f (9 dof, p=0.001 bound 27.88)", chi)
}
