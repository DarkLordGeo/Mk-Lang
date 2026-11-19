// Why: std's text code must give the same result at compile time as at run
// time; std-coll_parity_test.go covers the collections.
// What: TestStdEvalParity rows for integer and float formatting, Arena.Fmt,
// ParseInt, ParseFloat, ToLower and ToUpper, a planted bug, a Linux leg.
// How: each row is a digest run as a const initializer and natively, at dev
// and release; both must equal the Go model's (strconv, strings) digest.

package std

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
)

// memParityHeap is a bump heap over a caller's byte array: 16-byte units,
// the count in the first two bytes; never frees. Each digest resets its
// arena after every item, so one 4 KiB chunk is reused throughout.
const memParityHeap = `import std.raw

const MemParHeapBytes = 65536
const MemParHeapTable = AllocTable(alloc: MemParHeapAlloc, resize: MemParHeapResize, free: MemParHeapFree)

MemParHeapAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "ctx is the fixture's byte array" {
        used := int(raw.Read(ctx)) + int(raw.Read(raw.Offset(ctx, 1))) * 256
        units := (size + 15) / 16
        if 16 + (used + units) * 16 > MemParHeapBytes {
            return raw.Null[u8]()
        }
        n := used + units
        raw.Write(ctx, Trunc[u8](n))
        raw.Write(raw.Offset(ctx, 1), Trunc[u8](n / 256))
        return raw.Offset(ctx, 16 + used * 16)
    }
}

MemParHeapResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

MemParHeapFree(ctx: *u8, p: *u8, size: int, align: int) {
}

MemParFnv(h: u64, x: u64): u64 {
    return (h ^ x) *% 1099511628211
}

// MemParStr absorbs the length of s, then each byte.
MemParStr(h: u64, s: str): u64 {
    var r := MemParFnv(h, Trunc[u64](s.Len()))
    for i in 0..s.Len() {
        r = MemParFnv(r, u64(s.Byte(i)))
    }
    return r
}
`

// memParityBody is the digest functions; the %-verbs are the Go-generated
// input tables.
const memParityBody = `
const MemParIntEdges: [%[1]d]i64 = [%[2]s]
const MemParF64In: [%[3]d]u64 = [%[4]s]
const MemParF32In: [%[5]d]u32 = [%[6]s]
const MemParIntIn: [%[7]d]str = [%[8]s]
const MemParFloatIn: [%[9]d]str = [%[10]s]
const MemParCaseIn: [%[11]d]str = [%[12]s]
const MemParChars: [%[13]d]char = [%[14]s]
const MemParWords: [%[13]d]str = [%[15]s]

// Integers through Arena.Fmt: i64 decimal and hex, u64 decimal and hex, i32
// decimal and hex (a negative one is its 32-bit two's complement), u8: the
// edges, then 400 seeded values whose magnitude varies with the shift.
export MemParityInts(): u64 {
    var mem: [65536]u8 = [0; 65536]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&MemParHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var h: u64 = 14695981039346656037
        var s: u64 = 5
        for i in 0..(MemParIntEdges.Len() + 400) {
            s = s *%% 6364136223846793005 +%% 1442695040888963407
            sh: u64 = Trunc[u64](i %% 64)
            var x: i64 = Trunc[i64](s >> sh)
            if i < MemParIntEdges.Len() {
                x = MemParIntEdges[i]
            }
            xh := x
            u := s >> sh
            uh := s
            v: i32 = Trunc[i32](s >> 7)
            vh := v
            b: u8 = Trunc[u8](s >> 3)
            t := a.Fmt("i{x}h{xh:x}u{u}g{uh:x}v{v}w{vh:x}b{b};", (x, xh, u, uh, v, vh, b)) else {
                a.Free()
                return 2
            }
            h = MemParStr(h, t)
            a.Reset()
        }
        for k in 0..MemParChars.Len() {
            c := MemParChars[k]
            w := MemParWords[k]
            ok := k %% 2 == 0
            t := a.Fmt("x{{{w}}}c{c}o{ok};", (w, c, ok)) else {
                a.Free()
                return 3
            }
            h = MemParStr(h, t)
            a.Reset()
        }
        a.Free()
        return h
    }
}

// Floats through Arena.Fmt: f64 shortest and at .3, .17 and .0; f32
// shortest and at .2.
export MemParityFloats(): u64 {
    var mem: [65536]u8 = [0; 65536]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&MemParHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var h: u64 = 14695981039346656037
        for k in 0..MemParF64In.Len() {
            x := raw.F64From(MemParF64In[k])
            y := x
            z := x
            w := x
            t := a.Fmt("d{x}p{y:.3}q{z:.17}r{w:.0};", (x, y, z, w)) else {
                a.Free()
                return 2
            }
            h = MemParStr(h, t)
            a.Reset()
        }
        for k in 0..MemParF32In.Len() {
            x := raw.F32From(MemParF32In[k])
            y := x
            t := a.Fmt("e{x}p{y:.2};", (x, y)) else {
                a.Free()
                return 3
            }
            h = MemParStr(h, t)
            a.Reset()
        }
        a.Free()
        return h
    }
}

MemParIntStep(h: u64, s: str): u64 {
    v := s.ParseInt() else {
        return MemParFnv(h, 7)
    }
    return MemParFnv(MemParFnv(h, 8), Trunc[u64](v))
}

MemParFloatStep(h: u64, s: str): u64 {
    v := s.ParseFloat() else {
        return MemParFnv(h, 7)
    }
    return MemParFnv(MemParFnv(h, 8), raw.F64Bits(v))
}

// ParseInt and ParseFloat: each result, or the error, in order.
export MemParityParse(): u64 {
    var h: u64 = 14695981039346656037
    for k in 0..MemParIntIn.Len() {
        h = MemParIntStep(h, MemParIntIn[k])
    }
    h = MemParFnv(h, 1)
    for k in 0..MemParFloatIn.Len() {
        h = MemParFloatStep(h, MemParFloatIn[k])
    }
    return h
}

// ToLower and ToUpper of each text.
export MemParityCase(): u64 {
    var mem: [65536]u8 = [0; 65536]
    unsafe "fixture: a heap over mem, which outlives every use below" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&MemParHeapTable))
        var a := Arena.New(&heap) else { return 1 }
        var h: u64 = 14695981039346656037
        for k in 0..MemParCaseIn.Len() {
            l := MemParCaseIn[k].ToLower(&a) else {
                a.Free()
                return 2
            }
            h = MemParStr(h, l)
            u := MemParCaseIn[k].ToUpper(&a) else {
                a.Free()
                return 3
            }
            h = MemParStr(h, u)
            a.Reset()
        }
        a.Free()
        return h
    }
}

const MemParIntsC = MemParityInts() fuel 200_000_000
const MemParFloatsC = MemParityFloats() fuel 400_000_000
const MemParParseC = MemParityParse() fuel 400_000_000
const MemParCaseC = MemParityCase() fuel 200_000_000

// The compile-time values beside the same calls run natively.
export MemParityAll(): [8]u64 {
    return [MemParIntsC, MemParityInts(), MemParFloatsC, MemParityFloats(), MemParParseC, MemParityParse(), MemParCaseC, MemParityCase()]
}
`

const memParityModule = `import std.core

export All(): [8]u64 {
    return core.MemParityAll()
}
`

const memParityMain = `import std.memparity

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    for x in memparity.All() {
        try Print(mut out, "v{x}\n", (x))
    }
}
`

// memParityInputs is every input table, generated once from fixed seeds.
type memParityInputs struct {
	intEdges []int64
	f64s     []uint64
	f32s     []uint32
	intIn    []string
	floatIn  []string
	caseIn   []string
	chars    []rune
	words    []string
}

func memParityData() memParityInputs {
	var in memParityInputs
	in.intEdges = []int64{0, 1, -1, 9, 10, -10, 99, 100, -100, 12345, math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64, math.MinInt64 + 1, 1e18, -999999999999999999, 4294967296}
	f64 := []float64{0, math.Copysign(0, -1), 5e-324, 1e-323, 2.2250738585072009e-308, 2.2250738585072014e-308,
		math.MaxFloat64, -math.MaxFloat64, 1, -1, 0.5, 0.1, 0.3, 1.0 / 3, 2.0 / 3, math.Pi, math.E,
		9007199254740992, 9007199254740993, 4503599627370496.5, 123456789012345680, 1e6, 123456789.0, 1e20, 1e21,
		0.0001, 0.00001, 1.5e-7, 0.0005, 0.0015, 2.5, 3.5, -2.5, 1e23, 8.98846567431158e307,
		math.Inf(1), math.Inf(-1), math.NaN()}
	for e := -30; e <= 30; e += 3 {
		x := math.Pow(10, float64(e))
		f64 = append(f64, x, math.Nextafter(x, 0), math.Nextafter(x, math.Inf(1)))
	}
	for _, x := range f64 {
		in.f64s = append(in.f64s, math.Float64bits(x))
	}
	r := rand.New(rand.NewSource(55))
	for k := 0; k < 160; k++ {
		in.f64s = append(in.f64s, r.Uint64())
	}
	for _, x := range []float32{0, float32(math.Copysign(0, -1)), 1, 0.1, 16777216, 16777217, math.MaxFloat32,
		math.SmallestNonzeroFloat32, 1e-10, 3.4e38, 1.0 / 3, float32(math.Inf(1)), float32(math.NaN())} {
		in.f32s = append(in.f32s, math.Float32bits(x))
	}
	for k := 0; k < 80; k++ {
		in.f32s = append(in.f32s, r.Uint32())
	}
	in.intIn = []string{"", "+", "-", "0", "-0", "+0", "7", "-7", "+42", "00012", "9223372036854775807",
		"9223372036854775808", "-9223372036854775808", "-9223372036854775809", "99999999999999999999",
		" 1", "1 ", "1_0", "0x10", "1e3", "--1", "+-1", "12a", "١", "18446744073709551616"}
	for k := 0; k < 150; k++ {
		in.intIn = append(in.intIn, strconv.FormatInt(r.Int63()>>uint(r.Intn(63)), 10), "-"+strconv.FormatInt(r.Int63()>>uint(r.Intn(63)), 10))
	}
	in.floatIn = []string{"1e-400", "-1e-400", "2e-324", "3e-324", "5.", ".5", "5.e3", "1e400", "-1e400", "0x1p3",
		"1_0", "-nan", "+nan", "1e", ".", "e5", " 1", "", "Inf", "+infinity", "-inf", "NaN", "nAn", "-0", "0e0",
		"9007199254740993", "2.4703282292062327e-324", "2.4703282292062328e-324", "1.7976931348623157e308",
		"1.7976931348623158e308", "1.7976931348623159e308"}
	in.floatIn = append(in.floatIn, parseCorpus(rand.New(rand.NewSource(56)), 220)...)
	cs := []rune{'A', 'z', 0xC0, 0xDF, 0xFF, 0x130, 0x131, 0x178, 0x1C5, 0x3A3, 0x3C2, 0x400, 0x42F, 0x1E9E,
		0x2C65, 0xA640, 0xFF21, 0x10400, 0x1E900, 0x1F600}
	in.caseIn = []string{"", "Hello, World!", "ÉCOLE école", "İstanbul ı",
		"ΟΔΥΣΣΕΥΣ ς", "Straße ẞ", "Добро",
		"\U00010400\U00010428 \U0001e900", "mixed 123 Ⓐⓐ", string(cs)}
	for i := 0; i < len(cs); i += 4 {
		in.caseIn = append(in.caseIn, strings.Repeat(string(cs[i:i+4]), 30))
	}
	in.chars = []rune{'a', '{', 0xE9, 0x20AC, 0x1F600, 0x10FFFF}
	in.words = []string{"", "plain", "{brace}", "été", "a b\tc", "\U0001F600"}
	return in
}

// fixture is the std.core fixture file for in.
func (in memParityInputs) fixture() string {
	lits := func(n int, f func(int) string) string {
		var parts []string
		for i := 0; i < n; i++ {
			parts = append(parts, f(i))
		}
		return strings.Join(parts, ", ")
	}
	edge := func(i int) string {
		if in.intEdges[i] == math.MinInt64 {
			return "-9223372036854775807 - 1"
		}
		return strconv.FormatInt(in.intEdges[i], 10)
	}
	char := func(i int) string { return fmt.Sprintf(`'\u{%X}'`, in.chars[i]) }
	return memParityHeap + fmt.Sprintf(memParityBody,
		len(in.intEdges), lits(len(in.intEdges), edge),
		len(in.f64s), lits(len(in.f64s), func(i int) string { return fmt.Sprintf("0x%x", in.f64s[i]) }),
		len(in.f32s), lits(len(in.f32s), func(i int) string { return fmt.Sprintf("0x%x", in.f32s[i]) }),
		len(in.intIn), lits(len(in.intIn), func(i int) string { return mkString(in.intIn[i]) }),
		len(in.floatIn), lits(len(in.floatIn), func(i int) string { return mkString(in.floatIn[i]) }),
		len(in.caseIn), lits(len(in.caseIn), func(i int) string { return mkString(in.caseIn[i]) }),
		len(in.chars), lits(len(in.chars), char),
		lits(len(in.words), func(i int) string { return mkString(in.words[i]) }))
}

// memParStr is MemParStr.
func memParStr(h uint64, s string) uint64 {
	h = fnvStep(h, uint64(len(s)))
	for i := 0; i < len(s); i++ {
		h = fnvStep(h, uint64(s[i]))
	}
	return h
}

// want is the Go model's four digests, in MemParityAll's order.
func (in memParityInputs) want() [4]uint64 {
	var d [4]uint64
	// Integers.
	h := uint64(fnvStart)
	s := uint64(5)
	for i := 0; i < len(in.intEdges)+400; i++ {
		s = s*6364136223846793005 + 1442695040888963407
		sh := uint(i % 64)
		x := int64(s >> sh)
		if i < len(in.intEdges) {
			x = in.intEdges[i]
		}
		u := s >> sh
		v := int32(uint32(s >> 7))
		b := uint8(s >> 3)
		t := "i" + strconv.FormatInt(x, 10) + "h" + strconv.FormatUint(uint64(x), 16) +
			"u" + strconv.FormatUint(u, 10) + "g" + strconv.FormatUint(s, 16) +
			"v" + strconv.FormatInt(int64(v), 10) + "w" + strconv.FormatUint(uint64(uint32(v)), 16) +
			"b" + strconv.FormatUint(uint64(b), 10) + ";"
		h = memParStr(h, t)
	}
	for k := range in.chars {
		t := "x{" + in.words[k] + "}c" + string(in.chars[k]) + "o" + strconv.FormatBool(k%2 == 0) + ";"
		h = memParStr(h, t)
	}
	d[0] = h
	// Floats.
	h = fnvStart
	for _, bits := range in.f64s {
		x := math.Float64frombits(bits)
		t := "d" + goShortest(x, 64) + "p" + goFixed(x, 3, 64) + "q" + goFixed(x, 17, 64) + "r" + goFixed(x, 0, 64) + ";"
		h = memParStr(h, t)
	}
	for _, bits := range in.f32s {
		x := float64(math.Float32frombits(bits))
		t := "e" + goShortest(x, 32) + "p" + goFixed(x, 2, 32) + ";"
		h = memParStr(h, t)
	}
	d[1] = h
	// Parsing.
	h = fnvStart
	for _, c := range in.intIn {
		if v, err := strconv.ParseInt(c, 10, 64); err == nil {
			h = fnvStep(fnvStep(h, 8), uint64(v))
		} else {
			h = fnvStep(h, 7)
		}
	}
	h = fnvStep(h, 1)
	for _, c := range in.floatIn {
		if p := goParse(c); p.ok {
			h = fnvStep(fnvStep(h, 8), p.bits)
		} else {
			h = fnvStep(h, 7)
		}
	}
	d[2] = h
	// Case.
	h = fnvStart
	for _, c := range in.caseIn {
		h = memParStr(memParStr(h, strings.ToLower(c)), strings.ToUpper(c))
	}
	d[3] = h
	return d
}

// TestStdEvalParity: each digest's compile-time value equals its native
// value and the Go model's, at dev and at release. The collection rows are
// TestStdColl_EvalParity. The const leg runs in the evaluator with a fuel
// clause, and the arena runs on a bump heap over a local byte array, so the
// same code runs in both places.
func TestStdEvalParity(t *testing.T) {
	t.Parallel()
	in := memParityData()
	var want strings.Builder
	for _, w := range in.want() {
		fmt.Fprintf(&want, "v%d\nv%d\n", w, w)
	}
	fix := map[string]string{"core/tests_memparity.mk": in.fixture(), "memparity/memparity.mk": memParityModule}
	names := []string{"ints, const", "ints, native", "floats, const", "floats, native", "parse, const", "parse, native", "case, const", "case, native"}
	for _, flags := range [][]string{nil, {"--release"}} {
		r := runNative(t, buildNative(t, fix, memParityMain, flags...), "")
		if r.code != 0 {
			t.Fatalf("%v: exit %d\nstdout:\n%s\nstderr:\n%s", flags, r.code, r.stdout, r.stderr)
		}
		got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
		exp := strings.Split(strings.TrimSuffix(want.String(), "\n"), "\n")
		if len(got) != len(exp) {
			t.Fatalf("%v: %d lines, want %d\n%s", flags, len(got), len(exp), r.stdout)
		}
		for i := range exp {
			if got[i] != exp[i] {
				t.Errorf("%v: %s: %s, want the Go model's %s", flags, names[i], got[i], exp[i])
			}
		}
	}
}

// TestStdEvalParity_SeesPlantedBug: with FmtIntSpec's radix digits planted
// to print uppercase for `:x` (hex digits come from there, not FmtHex), both
// integer digests (the evaluator's and the native one) differ
// from the Go model's and every other digest still equals it, so the
// digests see a one-byte change in one path of the formatter.
func TestStdEvalParity_SeesPlantedBug(t *testing.T) {
	t.Parallel()
	in := memParityData()
	src := readText(t, "core/core_fmt.mk")
	planted := strings.Replace(src, "var letter: u8 = 0x57", "var letter: u8 = 0x37", 1)
	if planted == src {
		t.Fatal("core/core_fmt.mk: FmtIntSpec's letter line not found")
	}
	fix := map[string]string{"core/tests_memparity.mk": in.fixture(), "memparity/memparity.mk": memParityModule, "core/core_fmt.mk": planted}
	r := runNative(t, buildNative(t, fix, memParityMain), "")
	got := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	if r.code != 0 || len(got) != 8 {
		t.Fatalf("exit %d, %d lines\n%s\n%s", r.code, len(got), r.stdout, r.stderr)
	}
	w := in.want()
	for i := range got {
		same := got[i] == fmt.Sprintf("v%d", w[i/2])
		if same != (i >= 2) {
			t.Errorf("line %d (%s): equal to the model %v, want %v", i+1, got[i], same, i >= 2)
		}
	}
}

// TestStdEvalParity_Linux: the same program as a static x86_64-linux
// release executable, run in WSL. Its const digests are the evaluator's
// with the Linux target's facts; its native digests are the Linux
// backend's (codegen, the System V ABI, the Linux std.sys layer). All
// eight must equal the Go model's. Not parallel, so no other wsl.exe
// client of this binary runs at the same time (parallel WSL clients hang
// WSL); without WSL it
// skips, and fails under MK_GATE=1 (needWSL).
func TestStdEvalParity_Linux(t *testing.T) {
	needWSL(t)
	in := memParityData()
	var want strings.Builder
	for _, w := range in.want() {
		fmt.Fprintf(&want, "v%d\nv%d\n", w, w)
	}
	fix := map[string]string{"core/tests_memparity.mk": in.fixture(), "memparity/memparity.mk": memParityModule}
	exe := buildNative(t, fix, memParityMain, "--release", "--target", "x86_64-linux")
	if b, err := os.ReadFile(exe); err != nil || len(b) < 4 || string(b[:4]) != "\x7fELF" {
		t.Fatalf("%s is not an ELF executable (err %v)", exe, err)
	}
	stdout, stderr, code := runWSL(t, exe, nil)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	got := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	exp := strings.Split(strings.TrimSuffix(want.String(), "\n"), "\n")
	if len(got) != len(exp) {
		t.Fatalf("%d lines, want %d\n%s", len(got), len(exp), stdout)
	}
	names := []string{"ints, const", "ints, native", "floats, const", "floats, native", "parse, const", "parse, native", "case, const", "case, native"}
	for i := range exp {
		if got[i] != exp[i] {
			t.Errorf("x86_64-linux: %s: %s, want the Go model's %s", names[i], got[i], exp[i])
		}
	}
}
