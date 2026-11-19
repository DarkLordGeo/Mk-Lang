// Why: each std.math function's doc line states its error, and that number
// must come from a measurement against correctly rounded values.
// What: the oracle test over Mk-evidence/math's tables (mpmath, 256 bits),
// natively at dev and release, with each function's budget in mathBudget.
// How: compares result bits; see TestSTDMATH_Oracle for NaN and ulp rules.

package std

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// mathFn is one table and the std.math function it checks.
type mathFn struct {
	table string // Mk-evidence/math/<table>.txt
	mk    string // the std.math function
	kind  string // f1 f2 f3 frexp ldexp pred; with a 32 suffix in f32
}

// mathFns lists the functions the oracle checks. A table row whose
// function std.math does not declare yet is left out of the list.
var mathFns = []mathFn{
	{"f64/sqrt", "Sqrt", "f1"}, {"f64/floor", "Floor", "f1"}, {"f64/ceil", "Ceil", "f1"},
	{"f64/trunc", "RoundToZero", "f1"}, {"f64/round", "Round", "f1"}, {"f64/roundeven", "RoundEven", "f1"},
	{"f64/fract_trunc", "Fract", "f1"}, {"f64/mod", "Mod", "f2"}, {"f64/copysign", "CopySign", "f2"},
	{"f64/nextafter", "NextAfter", "f2"}, {"f64/fma", "Fma", "f3"}, {"f64/frexp", "FrExp", "frexp"},
	{"f64/ldexp", "LdExp", "ldexp"}, {"f64/isnan", "IsNan", "pred"}, {"f64/isinf", "IsInf", "pred"},
	{"f64/isfinite", "IsFinite", "pred"},
	{"f64/exp", "Exp", "f1"}, {"f64/exp2", "Exp2", "f1"}, {"f64/expm1", "Expm1", "f1"},
	{"f64/log", "Log", "f1"}, {"f64/log2", "Log2", "f1"}, {"f64/log10", "Log10", "f1"}, {"f64/log1p", "Log1p", "f1"},
	{"f64/sin", "Sin", "f1"}, {"f64/cos", "Cos", "f1"}, {"f64/tan", "Tan", "f1"},
	{"f64/asin", "Asin", "f1"}, {"f64/acos", "Acos", "f1"}, {"f64/atan", "Atan", "f1"}, {"f64/atan2", "Atan2", "f2"},
	{"f64/sinh", "Sinh", "f1"}, {"f64/cosh", "Cosh", "f1"}, {"f64/tanh", "Tanh", "f1"},
	{"f64/asinh", "Asinh", "f1"}, {"f64/acosh", "Acosh", "f1"}, {"f64/atanh", "Atanh", "f1"},
	{"f64/hypot", "Hypot", "f2"}, {"f64/cbrt", "Cbrt", "f1"}, {"f64/pow", "Pow", "f2"},
	{"f32/sqrt", "SqrtF32", "f132"},
	{"f32/exp", "ExpF32", "f132"}, {"f32/exp2", "Exp2F32", "f132"}, {"f32/expm1", "Expm1F32", "f132"},
	{"f32/log", "LogF32", "f132"}, {"f32/log2", "Log2F32", "f132"}, {"f32/log10", "Log10F32", "f132"}, {"f32/log1p", "Log1pF32", "f132"},
	{"f32/sin", "SinF32", "f132"}, {"f32/cos", "CosF32", "f132"}, {"f32/tan", "TanF32", "f132"},
	{"f32/asin", "AsinF32", "f132"}, {"f32/acos", "AcosF32", "f132"}, {"f32/atan", "AtanF32", "f132"}, {"f32/atan2", "Atan2F32", "f232"},
	{"f32/sinh", "SinhF32", "f132"}, {"f32/cosh", "CoshF32", "f132"}, {"f32/tanh", "TanhF32", "f132"},
	{"f32/asinh", "AsinhF32", "f132"}, {"f32/acosh", "AcoshF32", "f132"}, {"f32/atanh", "AtanhF32", "f132"},
	{"f32/hypot", "HypotF32", "f232"}, {"f32/cbrt", "CbrtF32", "f132"}, {"f32/pow", "PowF32", "f232"}, {"f32/floor", "FloorF32", "f132"}, {"f32/ceil", "CeilF32", "f132"},
	{"f32/trunc", "RoundToZeroF32", "f132"}, {"f32/round", "RoundF32", "f132"}, {"f32/roundeven", "RoundEvenF32", "f132"},
	{"f32/fract_trunc", "FractF32", "f132"}, {"f32/mod", "ModF32", "f232"}, {"f32/copysign", "CopySignF32", "f232"},
	{"f32/nextafter", "NextAfterF32", "f232"}, {"f32/fma", "FmaF32", "f332"}, {"f32/frexp", "FrExpF32", "frexp32"},
	{"f32/ldexp", "LdExpF32", "ldexp32"}, {"f32/isnan", "IsNanF32", "pred32"}, {"f32/isinf", "IsInfF32", "pred32"},
	{"f32/isfinite", "IsFiniteF32", "pred32"},
}

// mathBudget is the largest distance in ulps a function may show over its
// table; a function not listed is exact or correctly rounded (0).
var mathBudget = map[string]uint64{
	"Exp": 1, "Exp2": 1, "Expm1": 1, "Log": 1, "Log2": 1, "Log10": 1, "Log1p": 1, "Sin": 1, "Cos": 1, "Tan": 1, "Asin": 1, "Acos": 1, "Atan": 1, "Atan2": 1,
	"Sinh": 1, "Cosh": 1, "Tanh": 1, "Asinh": 1, "Acosh": 1, "Atanh": 1, "Hypot": 1, "Cbrt": 1, "Pow": 1,
}

// mathEvidence is the folder of the tables, or "" when it is absent.
func mathEvidence() string {
	ev := os.Getenv("MK_EVIDENCE")
	if ev == "" {
		ev = filepath.Join("..", "..", "Mk-evidence")
	}
	dir := filepath.Join(ev, "math")
	if _, err := os.Stat(filepath.Join(dir, "SOURCE.txt")); err != nil {
		return ""
	}
	return dir
}

// mathRow is one table row: the inputs as words (an f32 in the low half,
// an int as its two's complement) and the expected result words.
type mathRow struct {
	in   []uint64
	want []uint64
}

func readMathTable(t *testing.T, dir string, f mathFn) []mathRow {
	t.Helper()
	fh, err := os.Open(filepath.Join(dir, f.table+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	nIn := map[string]int{"f1": 1, "f2": 2, "f3": 3, "frexp": 1, "ldexp": 2, "pred": 1}[strings.TrimSuffix(f.kind, "32")]
	var rows []mathRow
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		fs := strings.Fields(line)
		var ws []uint64
		for i, s := range fs {
			// ldexp's n (column 1) and frexp's exp (column 2) are decimal.
			dec := strings.HasPrefix(f.kind, "ldexp") && i == 1 || strings.HasPrefix(f.kind, "frexp") && i == 2 || strings.HasPrefix(f.kind, "pred") && i == 1
			var w uint64
			if dec {
				v, err := strconv.ParseInt(s, 10, 64)
				if err != nil {
					t.Fatalf("%s: %q: %v", f.table, line, err)
				}
				w = uint64(v)
			} else {
				w, err = strconv.ParseUint(s, 16, 64)
				if err != nil {
					t.Fatalf("%s: %q: %v", f.table, line, err)
				}
			}
			ws = append(ws, w)
		}
		rows = append(rows, mathRow{in: ws[:nIn], want: ws[nIn:]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// mathDriver is a program that runs one std.math function, named by its
// first argument, over the words of the file its second argument names,
// and prints each result's words in decimal, one row a line.
func mathDriver(fns []mathFn) string {
	var b strings.Builder
	b.WriteString(`// Generated by std/std-math_oracle_test.go.
import std.fs
import std.math

Word(d: []u8, i: int): u64 {
    var w: u64 = 0
    for k in 0..8 {
        w = w | u64(d[i * 8 + k]) << (8 * k)
    }
    return w
}

D(d: []u8, i: int): f64 {
    return f64.FromBits(Word(d, i))
}

S(d: []u8, i: int): f32 {
    return f32.FromBits(Trunc[u32](Word(d, i)))
}

Run1(out: mut Writer, d: []u8, f: fn(x: f64): f64): WriteError!void {
    for i in 0..d.Len() / 8 {
        try Print(out, "{b}\n", (b: f(D(d, i)).Bits()))
    }
}

Run2(out: mut Writer, d: []u8, f: fn(x: f64, y: f64): f64): WriteError!void {
    for i in 0..d.Len() / 16 {
        try Print(out, "{b}\n", (b: f(D(d, 2 * i), D(d, 2 * i + 1)).Bits()))
    }
}

Run3(out: mut Writer, d: []u8, f: fn(x: f64, y: f64, z: f64): f64): WriteError!void {
    for i in 0..d.Len() / 24 {
        try Print(out, "{b}\n", (b: f(D(d, 3 * i), D(d, 3 * i + 1), D(d, 3 * i + 2)).Bits()))
    }
}

RunPred(out: mut Writer, d: []u8, f: fn(x: f64): bool): WriteError!void {
    for i in 0..d.Len() / 8 {
        b := if f(D(d, i)) { 1 } else { 0 }
        try Print(out, "{b}\n", (b))
    }
}

Run132(out: mut Writer, d: []u8, f: fn(x: f32): f32): WriteError!void {
    for i in 0..d.Len() / 8 {
        try Print(out, "{b}\n", (b: f(S(d, i)).Bits()))
    }
}

Run232(out: mut Writer, d: []u8, f: fn(x: f32, y: f32): f32): WriteError!void {
    for i in 0..d.Len() / 16 {
        try Print(out, "{b}\n", (b: f(S(d, 2 * i), S(d, 2 * i + 1)).Bits()))
    }
}

Run332(out: mut Writer, d: []u8, f: fn(x: f32, y: f32, z: f32): f32): WriteError!void {
    for i in 0..d.Len() / 24 {
        try Print(out, "{b}\n", (b: f(S(d, 3 * i), S(d, 3 * i + 1), S(d, 3 * i + 2)).Bits()))
    }
}

RunPred32(out: mut Writer, d: []u8, f: fn(x: f32): bool): WriteError!void {
    for i in 0..d.Len() / 8 {
        b := if f(S(d, i)) { 1 } else { 0 }
        try Print(out, "{b}\n", (b))
    }
}

RunFrExp(out: mut Writer, d: []u8): WriteError!void {
    for i in 0..d.Len() / 8 {
        r := math.FrExp(D(d, i))
        try Print(out, "{a} {e}\n", (a: r.frac.Bits(), e: r.exp))
    }
}

RunFrExp32(out: mut Writer, d: []u8): WriteError!void {
    for i in 0..d.Len() / 8 {
        r := math.FrExpF32(S(d, i))
        try Print(out, "{a} {e}\n", (a: r.frac.Bits(), e: r.exp))
    }
}

RunLdExp(out: mut Writer, d: []u8): WriteError!void {
    for i in 0..d.Len() / 16 {
        n := Trunc[int](Trunc[i64](Word(d, 2 * i + 1)))
        try Print(out, "{b}\n", (b: math.LdExp(D(d, 2 * i), n).Bits()))
    }
}

RunLdExp32(out: mut Writer, d: []u8): WriteError!void {
    for i in 0..d.Len() / 16 {
        n := Trunc[int](Trunc[i64](Word(d, 2 * i + 1)))
        try Print(out, "{b}\n", (b: math.LdExpF32(S(d, 2 * i), n).Bits()))
    }
}

export Main(sys: &System): fs.Error|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    name := sys.args[1]
    d := try fs.ReadBytes(sys.arena, sys.io, sys.args[2])
`)
	for _, f := range fns {
		call := ""
		switch f.kind {
		case "f1", "f2", "f3", "f132", "f232", "f332":
			call = fmt.Sprintf("Run%s(mut out, d, math.%s)", strings.TrimPrefix(f.kind, "f"), f.mk)
		case "pred":
			call = fmt.Sprintf("RunPred(mut out, d, math.%s)", f.mk)
		case "pred32":
			call = fmt.Sprintf("RunPred32(mut out, d, math.%s)", f.mk)
		case "frexp":
			call = "RunFrExp(mut out, d)"
		case "frexp32":
			call = "RunFrExp32(mut out, d)"
		case "ldexp":
			call = "RunLdExp(mut out, d)"
		case "ldexp32":
			call = "RunLdExp32(mut out, d)"
		}
		fmt.Fprintf(&b, "    if name == %q {\n        try %s\n    }\n", f.mk, call)
	}
	b.WriteString("}\n")
	return b.String()
}

// mathOrdered maps an encoding to an integer in the order of the values,
// so the distance between two finite results is their distance in ulps.
func mathOrdered(w uint64, is32 bool) int64 {
	if is32 {
		v := int64(int32(uint32(w)))
		if v < 0 {
			v = math.MinInt32 - v
		}
		return v
	}
	v := int64(w)
	if v < 0 {
		v = math.MinInt64 - v
	}
	return v
}

func mathIsNaN(w uint64, is32 bool) bool {
	if is32 {
		return uint32(w)&0x7FFFFFFF > 0x7F800000
	}
	return w&0x7FFFFFFFFFFFFFFF > 0x7FF0000000000000
}

// mathDistance is the distance between got and want: 0 when equal, the
// ulp distance between two non-NaN values, and the maximum when one is a
// NaN and the other not, or a NaN is not the canonical one.
func mathDistance(got, want uint64, is32 bool) uint64 {
	if got == want {
		return 0
	}
	if mathIsNaN(want, is32) || mathIsNaN(got, is32) {
		return math.MaxUint64
	}
	d := mathOrdered(got, is32) - mathOrdered(want, is32)
	if d < 0 {
		d = -d
	}
	return uint64(d)
}

var mathExes struct {
	once sync.Once
	exes map[string]string
}

// mathBuild builds the driver once per test binary, dev and release, and
// keeps both executables in a package temp directory, so every test of the
// binary can run them after the first test's own directory is gone.
func mathBuild(t *testing.T) map[string]string {
	mathExes.once.Do(func() {
		src := mathDriver(mathFns)
		dir, err := packageTempDir("mkmath")
		if err != nil {
			t.Fatal(err)
		}
		exes := map[string]string{}
		for prof, flags := range map[string][]string{"dev": nil, "release": {"--release"}} {
			b, err := os.ReadFile(buildNative(t, nil, src, flags...))
			if err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "math-"+prof+".exe")
			if err := os.WriteFile(exe, b, 0o755); err != nil {
				t.Fatal(err)
			}
			exes[prof] = exe
		}
		mathExes.exes = exes
	})
	if mathExes.exes == nil {
		t.Fatal("the std.math driver did not build")
	}
	return mathExes.exes
}

// TestSTDMATH_Oracle runs every function of mathFns over its table, dev
// and release, and logs the largest distance found. A NaN must be the
// canonical NaN exactly (0x7FF8000000000000, 0x7FC00000), which is stronger
// than the table's "compare as is-NaN". Exact functions must match every
// row; the others are measured in units in the last place of the result,
// the distance between the two encodings in the order of the values, and
// must stay within their budget in mathBudget (1 ulp from the correctly
// rounded value, which bounds the true error by 1.5 ulp; it does not show
// faithful rounding). The largest distance per function is what each doc
// line states.
func TestSTDMATH_Oracle(t *testing.T) {
	dir := mathEvidence()
	if dir == "" {
		t.Skip("Mk-evidence/math not found (set MK_EVIDENCE)")
	}
	exes := mathBuild(t)
	tmp := t.TempDir()
	var report []string
	for _, f := range mathFns {
		rows := readMathTable(t, dir, f)
		is32 := strings.HasSuffix(f.kind, "32")
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
		devOut := ""
		for _, prof := range []string{"dev", "release"} {
			res := runNative(t, exes[prof], "", f.mk, in)
			if res.code != 0 {
				t.Errorf("%s %s: exit %d\n%s", f.mk, prof, res.code, res.stderr)
				continue
			}
			// The two profiles give the same bits on every row.
			if prof == "dev" {
				devOut = res.stdout
			} else if res.stdout != devOut {
				dl, rl := strings.Split(devOut, "\n"), strings.Split(res.stdout, "\n")
				for i := range min(len(dl), len(rl)) {
					if dl[i] != rl[i] {
						t.Errorf("%s: row %d (in %x): dev %s, release %s", f.mk, i, rows[min(i, len(rows)-1)].in, dl[i], rl[i])
						break
					}
				}
			}
			lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
			if len(lines) != len(rows) {
				t.Errorf("%s %s: %d results for %d rows", f.mk, prof, len(lines), len(rows))
				continue
			}
			var worst uint64
			off := 0
			var bad []string
			for i, r := range rows {
				fs := strings.Fields(lines[i])
				for j, want := range r.want {
					got, _ := strconv.ParseUint(fs[j], 10, 64)
					if j > 0 || strings.HasPrefix(f.kind, "pred") {
						// an integer column: exp or a truth value
						v, _ := strconv.ParseInt(fs[j], 10, 64)
						got = uint64(v)
						if got != want {
							bad = append(bad, fmt.Sprintf("in %x: column %d got %d want %d", r.in, j, int64(got), int64(want)))
						}
						continue
					}
					d := mathDistance(got, want, is32)
					if d > 0 {
						off++
					}
					if d > worst {
						worst = d
					}
					if d > mathBudget[f.mk] {
						bad = append(bad, fmt.Sprintf("in %x: got %x want %x (%d ulps)", r.in, got, want, d))
					}
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				if len(bad) > 12 {
					bad = append(bad[:12], fmt.Sprintf("... %d more", len(bad)-12))
				}
				t.Errorf("%s %s over %d rows: %d mismatches past the budget %d:\n  %s", f.mk, prof, len(rows), len(bad), mathBudget[f.mk], strings.Join(bad, "\n  "))
			}
			if prof == "release" {
				report = append(report, fmt.Sprintf("%s: %d rows, largest distance %d ulp, %d rows off", f.mk, len(rows), worst, off))
			}
		}
	}
	t.Logf("std.math against Mk-evidence/math:\n  %s", strings.Join(report, "\n  "))
}
