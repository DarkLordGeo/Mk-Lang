// Why: a checked f64 to f32 Cast panics on a finite value that rounds to an
// infinity and passes infinities and NaNs; every build must agree on that edge.
// What: TestF64ToF32CastBoundary with its inputs (f32cInputs), model
// (f32cWant) and program (f32cProgram).
// How: runs each input through the release build, the dev build and the
// evaluator against Go's float32 conversion, and pins the order of three traps.

package driver

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
)

// The inputs are both signs of MaxF32, the f64s just above it, the largest
// f64 that still rounds to MaxF32 (0x47EFFFFFEFFFFFFF) and the next one, the
// midpoint that ties to the infinity (0x47EFFFFFF0000000), 2^128, the largest
// f64, the infinities, NaNs of either sign and payload, the zeros, the
// subnormals at f32's bottom edge and ordinary values. Each converts,
// rounding to nearest even, or panics with PanicCast at the cast's site; the
// evaluator holds a const table of the converting inputs and one const per
// panicking one, each E0901 at the cast. A second function holds a bounds
// check before the cast and an overflow check after it, so the order of the
// three traps is pinned too. codegen's TestF64ToF32CastHotPath pins the one
// compare on the hot path.

// f32cInputs are the f64 bits the cast is tried on.
func f32cInputs() []uint64 {
	pos := []uint64{
		0x47EFFFFFE0000000, // MaxF32
		0x47EFFFFFE0000001, // just above it: MaxF32
		0x47EFFFFFEFFFFFFF, // the largest f64 that rounds to MaxF32
		0x47EFFFFFF0000000, // the midpoint above MaxF32: ties to the infinity
		0x47EFFFFFF0000001,
		0x47F0000000000000, // 2^128
		0x47F0000000000001,
		0x7FEFFFFFFFFFFFFF, // the largest f64
		0x7FF0000000000000, // the infinity
		0x7FF8000000000000, // NaNs
		0x7FF0000000000001,
		0x7FFFFFFFFFFFFFFF,
		0x0000000000000000,
		0x0000000000000001, // the least f64 subnormal: +0
		0x36A0000000000000, // 2^-149, the least f32 subnormal
		0x3690000000000000, // 2^-150: ties to +0
		0x3690000000000001, // just above: 2^-149
		0x3810000000000000, // 2^-126, the least f32 normal
		0x3FF0000000000000, // 1
		0x3FB999999999999A, // 0.1
		0x4415AF1D78B58C40, // 1e20
		0x47E0000000000000, // 2^127
	}
	var out []uint64
	for _, b := range pos {
		out = append(out, b, b|1<<63)
	}
	return out
}

// f32cWant is what Cast[f32] gives the f64 with bits b: the f32 bits, or
// a panic; nan reports a NaN result, whose payload is not pinned.
func f32cWant(b uint64) (val uint32, trap, nan bool) {
	x := math.Float64frombits(b)
	if math.IsNaN(x) {
		return 0, false, true
	}
	f := float32(x)
	if math.IsInf(float64(f), 0) && !math.IsInf(x, 0) {
		return 0, true, false
	}
	return math.Float32bits(f), false, false
}

// f32cSource is the program: C's cast at line 2, column 12; Order's
// bounds check, cast and overflow check at lines 6, 7 and 8. With no
// arguments it prints the const table; with "c" and bits, Cast[f32] of
// them; with "o", bits, i and n, Order's result.
const f32cSource = `C(x: f64): f32 {
    return Cast[f32](x)
}

Order(x: f64, i: int, n: int): f32 {
    a := [x, x][i]
    b := Cast[f32](a)
    m := n + 1
    return b + Cast[f32](Cast[f64](m))
}

`

func f32cProgram(conv []uint64, consts string) string {
	var s strings.Builder
	s.WriteString(f32cSource)
	if consts != "" {
		s.WriteString(consts)
		return s.String()
	}
	fmt.Fprintf(&s, "const In: [%d]u64 = [", len(conv))
	for i, b := range conv {
		if i > 0 {
			s.WriteString(", ")
		}
		fmt.Fprintf(&s, "0x%X", b)
	}
	fmt.Fprintf(&s, `]

Folded(): [%[1]d]u32 {
    var out: [%[1]d]u32 = [0; %[1]d]
    for i in 0..%[1]d {
        out[i] = C(f64.FromBits(In[i])).Bits()
    }
    return out
}

const AtCompileTime: [%[1]d]u32 = Folded()

export Main(sys: &System): ParseError|WriteError!void {
    var out := sys.Stdout()
    if sys.args.Len() == 1 {
        for i in 0..%[1]d {
            v := AtCompileTime[i]
            try Print(mut out, "{v}\n", (v))
        }
        return
    }
    b := Trunc[u64](try sys.args[2].ParseInt())
    if sys.args[1] == "o" {
        i := Cast[int](try sys.args[3].ParseInt())
        n := Cast[int](try sys.args[4].ParseInt())
        v := Order(f64.FromBits(b), i, n).Bits()
        try Print(mut out, "{v}\n", (v))
        return
    }
    var k := 2
    for k < sys.args.Len() {
        v := C(f64.FromBits(Trunc[u64](try sys.args[k].ParseInt()))).Bits()
        try Print(mut out, "{v}\n", (v))
        k += 1
    }
}
`, len(conv))
	return s.String()
}

func TestF64ToF32CastBoundary(t *testing.T) {
	var conv, traps []uint64
	for _, b := range f32cInputs() {
		if _, trap, _ := f32cWant(b); trap {
			traps = append(traps, b)
		} else {
			conv = append(conv, b)
		}
	}
	if len(traps) != 10 {
		t.Fatalf("%d panicking inputs, want 10 (five magnitudes, two signs)", len(traps))
	}
	target := HostTarget()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.mk")
	if err := os.WriteFile(src, []byte(f32cProgram(conv, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	judge := func(t *testing.T, what, out string) {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n")
		if len(lines) != len(conv) {
			t.Fatalf("%s: %d lines, want %d", what, len(lines), len(conv))
		}
		for i, b := range conv {
			want, _, nan := f32cWant(b)
			got, err := strconv.ParseUint(lines[i], 10, 32)
			if err != nil {
				t.Fatalf("%s: line %q: %v", what, lines[i], err)
			}
			isNan := got&0x7F800000 == 0x7F800000 && got&0x7FFFFF != 0
			if nan && !isNan || !nan && uint32(got) != want {
				t.Errorf("%s: Cast[f32] of %#016x = %#08x, want %#08x (NaN %v)", what, b, got, want, nan)
			}
		}
	}
	at := func(errOut string, line, col int) bool {
		first, _, _ := strings.Cut(strings.ReplaceAll(errOut, "\r\n", "\n"), "\n")
		want := fmt.Sprintf("main.mk:%d:%d", line, col)
		s, ok := strings.CutPrefix(first, "panic: ")
		return ok && (strings.HasSuffix(s, " at "+want) || strings.HasSuffix(s, "/"+want) || strings.HasSuffix(s, "\\"+want))
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
			argv := []string{"c"}
			for _, b := range conv {
				argv = append(argv, strconv.FormatInt(int64(b), 10))
			}
			out, errOut, code := runExe(t, target, exe, "", argv...)
			if code != 0 {
				t.Fatalf("%s: exit %d, stderr %q", name, code, errOut)
			}
			judge(t, name, out)
			for _, b := range traps {
				_, errOut, code := runExe(t, target, exe, "", "c", strconv.FormatInt(int64(b), 10))
				if code != 101 || !strings.HasPrefix(errOut, "panic: cast at ") || !at(errOut, 2, 12) {
					t.Errorf("%s: Cast[f32] of %#016x: exit %d, %q; want 101, a cast panic at main.mk:2:12", name, b, code, errOut)
				}
			}
			// The order of Order's traps: the bounds check, then the cast,
			// then the overflow, whichever inputs fail.
			big := strconv.FormatInt(int64(traps[0]), 10)
			one := strconv.FormatInt(int64(math.Float64bits(1)), 10)
			for _, c := range []struct {
				args       []string
				kind       string
				line, col  int
				code       int
				wantOutput string
			}{
				{[]string{"o", big, "2", "9223372036854775807"}, "bounds", 6, 10, 101, ""},
				{[]string{"o", big, "1", "9223372036854775807"}, "cast", 7, 10, 101, ""},
				{[]string{"o", one, "1", "9223372036854775807"}, "overflow", 8, 10, 101, ""},
				{[]string{"o", one, "0", "1"}, "", 0, 0, 0, strconv.FormatUint(uint64(math.Float32bits(3)), 10)},
			} {
				out, errOut, code := runExe(t, target, exe, "", c.args...)
				if c.code == 0 {
					if code != 0 || strings.TrimSpace(out) != c.wantOutput {
						t.Errorf("%s: Order%v: exit %d, %q, %q; want %s", name, c.args[1:], code, out, errOut, c.wantOutput)
					}
					continue
				}
				if code != c.code || !strings.HasPrefix(errOut, "panic: "+c.kind+" at ") || !at(errOut, c.line, c.col) {
					t.Errorf("%s: Order%v: exit %d, %q; want %d, a %s panic at main.mk:%d:%d", name, c.args[1:], code, errOut, c.code, c.kind, c.line, c.col)
				}
			}
		})
	}
	t.Run("eval-panics", func(t *testing.T) {
		var consts strings.Builder
		for i, b := range traps {
			fmt.Fprintf(&consts, "const T%d: f32 = C(f64.FromBits(0x%X))\n", i, b)
		}
		consts.WriteString("\nexport Main(sys: &System) {\n}\n")
		tdir := t.TempDir()
		tsrc := filepath.Join(tdir, "main.mk")
		if err := os.WriteFile(tsrc, []byte(f32cProgram(nil, consts.String())), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := NewSession(Config{Root: tsrc, CacheDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		r := s.Check()
		text := r.Report.Text(r.Sources)
		if len(r.Report.Diags) != len(traps) {
			t.Errorf("%d diagnostics, want one E0901 per panicking input (%d)", len(r.Report.Diags), len(traps))
		}
		if n := strings.Count(text, "compile-time evaluation panicked: cast"); n != len(traps) {
			t.Errorf("%d cast panics at compile time, want %d", n, len(traps))
		}
		trace := regexp.MustCompile(`trace: \[\{"name":"C","at":"[^"]*?main\.mk:2:12"\}`)
		if n := len(trace.FindAllString(text, -1)); n != len(traps) {
			t.Errorf("%d traces at C's cast (main.mk:2:12), want %d", n, len(traps))
		}
		if t.Failed() {
			t.Logf("%s", text)
		}
	})
}
