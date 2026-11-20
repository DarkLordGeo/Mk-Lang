// Why: Reader.Line, std's large tables at compile time and str.From each had
// a bug that only a targeted test shows.
// What: Reader.Line on a full buffer or a lying table, one-const table
// tests, str.From at every alignment, the text Mk test blocks, str panics.
// How: compile-time fixtures and native programs, and mk test over
// core/tests_str.mk, tests_fmt.mk, tests_parse.mk and math/tests_math.mk.

package std

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// lineFixture is a ReaderTable over a ZzLineSrc and ZzLineCases, the set of
// failed Reader.Line cases, as a std.core module. It is
// the same text at compile time and natively.
const lineFixture = `// A ReaderTable over a ZzLineSrc that serves step bytes per read
// (fill bytes of a, then data) and lies about the count when told to.

struct ZzLineSrc {
    data: str
    fill: int
    pos: int
    step: int
    lie: int
    lie_at: int
    lie_probe: bool
    calls: int
}

const ZzLineTable = ReaderTable(read: ZzLineRead, line: ZzLineEntry)

ZzLineSrcOf(data: str, fill: int, step: int): ZzLineSrc {
    return ZzLineSrc(data: data, fill: fill, pos: 0, step: step, lie: 0, lie_at: 0, lie_probe: false, calls: 0)
}

ZzLineByte(fill: int, data: str, k: int): u8 {
    if k < fill {
        return 0x61
    }
    return data.Byte(k - fill)
}

// ZzLineLie is the count to report instead of an honest one, or 0.
ZzLineLie(src: mut ZzLineSrc, room: int): int {
    src.calls += 1
    if src.lie == 0 || src.calls <= src.lie_at || (src.lie_probe && room != 1) {
        return 0
    }
    if src.lie == 1 {
        return room + 1
    }
    return -1
}

ZzLineRead(r: mut Reader, buf: mut []u8): ReadError!int {
    unsafe "fixture: ctx is the ZzLineSrc" {
        src := mut *raw.PtrCast[u8, ZzLineSrc](r.ctx)
        lie := ZzLineLie(src, buf.Len())
        if lie != 0 {
            return lie
        }
        var n := src.fill + src.data.Len() - src.pos
        if n > src.step {
            n = src.step
        }
        if n > buf.Len() {
            n = buf.Len()
        }
        for i in 0..n {
            buf[i] = ZzLineByte(src.fill, src.data, src.pos + i)
        }
        src.pos += n
        return n
    }
}

// ZzLineEntry is the line entry of a reader with no inline buffer: up to
// and including the first newline, at most buf.Len() bytes.
ZzLineEntry(r: mut Reader, buf: mut []u8): ReadError!int {
    unsafe "fixture: ctx is the ZzLineSrc" {
        src := mut *raw.PtrCast[u8, ZzLineSrc](r.ctx)
        lie := ZzLineLie(src, buf.Len())
        if lie != 0 {
            return lie
        }
        total := src.fill + src.data.Len()
        var n := 0
        for n < buf.Len() && src.pos < total {
            c := ZzLineByte(src.fill, src.data, src.pos)
            buf[n] = c
            n += 1
            src.pos += 1
            if c == 0x0A {
                break
            }
        }
        return n
    }
}

ZzLineReader(src: mut ZzLineSrc, buf: mut []u8): Reader from src, buf {
    unsafe "fixture" {
        return Reader(io: InertIoValue(), table: &ZzLineTable, ctx: raw.PtrCast[ZzLineSrc, u8](raw.OfMut(src)),
            buf: raw.DataMut(buf), start: 0, end: 0, cap: buf.Len())
    }
}

ZzLineIs(r: mut Reader, a: &Arena, want: str): int {
    l := r.Line(a) else { return 1 }
    s := l else { return 1 }
    if s != want {
        return 1
    }
    return 0
}

ZzLineNone(r: mut Reader, a: &Arena): int {
    l := r.Line(a) else { return 1 }
    if l.IsSome() {
        return 1
    }
    return 0
}

ZzLineFails(r: mut Reader, a: &Arena): int {
    _ = r.Line(a) else e {
        match e {
            ReadError {
                return 0
            }
            OutOfMemory {
                return 1
            }
        }
    }
    return 1
}

// ZzLines reads w1, then w2 unless empty, then none; a want of "!" is
// ReadError and ends the case.
ZzLines(r: mut Reader, a: &Arena, w1: str, w2: str): int {
    if w1 == "!" {
        return ZzLineFails(r, a)
    }
    var bad := ZzLineIs(r, a, w1)
    if w2 != "" {
        if w2 == "!" {
            return bad + ZzLineFails(r, a)
        }
        bad += ZzLineIs(r, a, w2)
    }
    return bad + ZzLineNone(r, a)
}

// ZzBuf runs a case through a 16-byte inline buffer.
ZzBuf(src: mut ZzLineSrc, a: &Arena, w1: str, w2: str): int {
    var buf: [16]u8 = [0; 16]
    var r := ZzLineReader(src, mut buf[..])
    return ZzLines(mut r, a, w1, w2)
}

// ZzUnbuf runs a case through a reader with no inline buffer.
ZzUnbuf(src: mut ZzLineSrc, a: &Arena, w1: str, w2: str): int {
    var buf: [0]u8 = []
    var r := ZzLineReader(src, mut buf[..])
    return ZzLines(mut r, a, w1, w2)
}

ZzBit(k: int, fails: int): int {
    if fails != 0 {
        return 1 << k
    }
    return 0
}

ZzLie(src: ZzLineSrc, lie: int, at: int, probe: bool): ZzLineSrc {
    var s := src
    s.lie = lie
    s.lie_at = at
    s.lie_probe = probe
    return s
}

// ZzAs is n bytes of a, in a.
ZzAs(a: &Arena, n: int): str from a {
    var t := Text.New(a)
    for _ in 0..n / 64 {
        t.Push("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") else {
            return ""
        }
    }
    for _ in 0..n % 64 {
        t.Push("a") else {
            return ""
        }
    }
    return TextInto(t)
}

// ZzLineCases is the set of failed cases, bit k for case k.
export ZzLineCases(a: &Arena, big: bool): int {
    var bad := 0
    // STDTEXT-R1: a last line that exactly fills the buffer is a line.
    var s0 := ZzLineSrcOf("0123456789abcdef", 0, 5)
    bad |= ZzBit(0, ZzBuf(mut s0, a, "0123456789abcdef", ""))
    var s1 := ZzLineSrcOf("0123456789abcdef", 0, 16)
    bad |= ZzBit(1, ZzBuf(mut s1, a, "0123456789abcdef", ""))
    var s2 := ZzLineSrcOf("0123456789abcdef\n", 0, 5)
    bad |= ZzBit(2, ZzBuf(mut s2, a, "0123456789abcdef", ""))
    var s3 := ZzLineSrcOf("0123456789abcde\r\n", 0, 5)
    bad |= ZzBit(3, ZzBuf(mut s3, a, "0123456789abcde", ""))
    var s4 := ZzLineSrcOf("0123456789abcdefg", 0, 5)
    bad |= ZzBit(4, ZzBuf(mut s4, a, "!", ""))
    var s5 := ZzLineSrcOf("0123456789abcdefg\n", 0, 5)
    bad |= ZzBit(5, ZzBuf(mut s5, a, "!", ""))
    var s6 := ZzLineSrcOf("x\n0123456789abcdef", 0, 5)
    bad |= ZzBit(6, ZzBuf(mut s6, a, "x", "0123456789abcdef"))
    var s7 := ZzLineSrcOf("0123456789abcdef\nnext", 0, 7)
    bad |= ZzBit(7, ZzBuf(mut s7, a, "0123456789abcdef", "next"))
    var s8 := ZzLineSrcOf("0123456789abcde\r", 0, 5)
    bad |= ZzBit(8, ZzBuf(mut s8, a, "0123456789abcde\r", ""))
    // STDTEXT-R2: a count above the room or below zero is ReadError, never
    // none, a line, or a read past the buffer.
    var s9 := ZzLie(ZzLineSrcOf("abc\n", 0, 5), 1, 0, false)
    bad |= ZzBit(9, ZzBuf(mut s9, a, "!", ""))
    var s10 := ZzLie(ZzLineSrcOf("abc\n", 0, 5), 2, 0, false)
    bad |= ZzBit(10, ZzBuf(mut s10, a, "!", ""))
    var s11 := ZzLie(ZzLineSrcOf("ab\ncdefgh\n", 0, 5), 1, 1, false)
    bad |= ZzBit(11, ZzBuf(mut s11, a, "ab", "!"))
    var s12 := ZzLie(ZzLineSrcOf("ab\ncdefgh\n", 0, 5), 2, 1, false)
    bad |= ZzBit(12, ZzBuf(mut s12, a, "ab", "!"))
    var s13 := ZzLie(ZzLineSrcOf("0123456789abcdef", 0, 16), 1, 0, true)
    bad |= ZzBit(13, ZzBuf(mut s13, a, "!", ""))
    var s14 := ZzLie(ZzLineSrcOf("0123456789abcdef", 0, 16), 2, 0, true)
    bad |= ZzBit(14, ZzBuf(mut s14, a, "!", ""))
    // The same through a reader with no inline buffer (the line entry).
    var u0 := ZzLie(ZzLineSrcOf("abc\n", 0, 5), 1, 0, false)
    bad |= ZzBit(15, ZzUnbuf(mut u0, a, "!", ""))
    var u1 := ZzLie(ZzLineSrcOf("abc\n", 0, 5), 2, 0, false)
    bad |= ZzBit(16, ZzUnbuf(mut u1, a, "!", ""))
    var u2 := ZzLie(ZzLineSrcOf("ab\ncd\n", 0, 5), 2, 1, false)
    bad |= ZzBit(17, ZzUnbuf(mut u2, a, "ab", "!"))
    var u3 := ZzLineSrcOf("ab\ncd", 0, 5)
    bad |= ZzBit(18, ZzUnbuf(mut u3, a, "ab", "cd"))
    if big {
        // LineMax: a line of exactly 65,536 bytes is a line, one more is
        // ReadError (a CR before the newline counts, as in a buffered
        // reader); a lie on a later chunk is ReadError.
        var b0 := ZzLineSrcOf("", 65536, 5)
        bad |= ZzBit(19, ZzUnbuf(mut b0, a, ZzAs(a, 65536), ""))
        var b1 := ZzLineSrcOf("\r\nz", 65535, 5)
        bad |= ZzBit(20, ZzUnbuf(mut b1, a, ZzAs(a, 65535), "z"))
        var b2 := ZzLineSrcOf("", 65537, 5)
        bad |= ZzBit(21, ZzUnbuf(mut b2, a, "!", ""))
        var b3 := ZzLie(ZzLineSrcOf("", 5000, 5), 1, 2, false)
        bad |= ZzBit(22, ZzUnbuf(mut b3, a, "!", ""))
        var b4 := ZzLie(ZzLineSrcOf("", 5000, 5), 2, 2, false)
        bad |= ZzBit(23, ZzUnbuf(mut b4, a, "!", ""))
    }
    return bad
}
`

// lineCaseCT runs lineFixture's small cases at compile time, over an arena
// of evaluation memory.
const lineCaseCT = `
ZzLineCT(): int {
    var mem: [16384]u8 = [0; 16384]
    unsafe "fixture" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ProbeHeapTable))
        var a := Arena.New(&heap) else { return 1 << 30 }
        r := ZzLineCases(&a, false)
        a.Free()
        return r
    }
}

const ZzLineOk = [0; 1][ZzLineCT()]
`

// TestSTDTEXT_ReaderLineAtCompileTime: through a 16-byte buffer, a last
// line of exactly 16 bytes is a line (with no newline, with \n, with \r\n
// whose \n arrives after the buffer filled), 17 bytes are ReadError; and a
// table that reports one byte more than the room or -1, on the first read,
// on a later one or on Line's probe, is ReadError, buffered and through the
// line entry of a reader with no inline buffer. Before the fix this stopped with E0901 pointer-bytes: Line
// read past its buffer.
func TestSTDTEXT_ReaderLineAtCompileTime(t *testing.T) {
	t.Parallel()
	expectClean(t, "stdtext_r1", probeHeap+lineFixture+lineCaseCT, ptrCastICE)
}

// moduleCompanions are more std files buildWithModule writes beside a
// module: std.core is internal to std, so a program reaches a core fixture
// through a std module of its own.
var moduleCompanions = map[string]map[string]string{
	"core/zz_line.mk": {"zzline/line.mk": `import std.core

export Cases(a: &Arena): int {
    return core.ZzLineCases(a, true)
}
`},
}

// lineProgram runs every lineFixture case natively, the LineMax ones (a line
// of exactly 65,536 bytes is a line, 65,537 is ReadError) included, and
// prints the set of failed cases.
func lineProgram() textProgram {
	src := `import std.zzline

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    bad := zzline.Cases(sys.arena)
    try Print(mut out, "failed {bad}\n", (bad))
    try out.Flush()
}
`
	return textProgram{"line", "core/zz_line.mk", "import std.raw\n" + lineFixture, src, "", "failed 0\n"}
}

// TestSTDTEXT_ReaderLineNative is the compile-time test's cases and
// the LineMax ones natively, at dev and release.
func TestSTDTEXT_ReaderLineNative(t *testing.T) {
	t.Parallel()
	p := lineProgram()
	for _, flags := range [][]string{nil, {"--release"}} {
		r := runNative(t, buildWithModule(t, p.rel, p.module, p.src, flags...), p.stdin)
		if r.code != 0 || r.stdout != p.want {
			t.Fatalf("%v: exit %d, stdout %q (bit k is case k of ZzLineCases)\n%s", flags, r.code, r.stdout, r.stderr)
		}
	}
}

// fromCases is TestSTDTEXT_FromAtEveryAlignment's corpus: every lead byte with each boundary
// continuation byte and tails of 0 to 3 bytes; ASCII runs of 8 to 96 bytes
// with one bad byte, or one truncated or valid scalar, at every position;
// and seeded strings over the boundary alphabet, at least n cases in all.
func fromCases(r *rand.Rand, n int) [][]byte {
	var out [][]byte
	seconds := []byte{0x00, 0x41, 0x7f, 0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0xc0, 0xff}
	tails := [][]byte{{}, {0x80}, {0x80, 0x80}, {0xbf, 0xbf}, {0x80, 0x80, 0x80}, {0x41}}
	for b0 := 0; b0 < 256; b0++ {
		out = append(out, []byte{byte(b0)})
		for _, b1 := range seconds {
			for _, tl := range tails {
				out = append(out, append([]byte{byte(b0), b1}, tl...))
			}
		}
	}
	inserts := [][]byte{{0x80}, {0xbf}, {0xc0}, {0xc1, 0xbf}, {0xf5}, {0xff}, {0xe2, 0x82}, {0xf0, 0x9f, 0x98},
		{0xed, 0xa0, 0x80}, {0xe0, 0x80, 0x80}, {0xf4, 0x90, 0x80, 0x80}, {0xc3, 0xa9}, {0xe2, 0x82, 0xac},
		{0xf0, 0x9f, 0x98, 0x80}, {0xef, 0xbf, 0xbf}, {0xf4, 0x8f, 0xbf, 0xbf}}
	for _, l := range []int{8, 15, 16, 17, 31, 32, 33, 63, 64, 96} {
		for p := 0; p <= l; p++ {
			for _, ins := range inserts {
				b := []byte(strings.Repeat("a", p))
				b = append(b, ins...)
				b = append(b, strings.Repeat("z", l-p)...)
				out = append(out, b)
			}
		}
	}
	alphabet := []byte{0x41, 0x7f, 0x80, 0xbf, 0xc2, 0xdf, 0xe0, 0xed, 0xef, 0xf0, 0xf4, 0xf5, 0xff, 0xa0, 0x9f, 0x90, 0x8f, 0x20, 0x00}
	for len(out) < n {
		c := make([]byte, r.Intn(40))
		for i := range c {
			if r.Intn(3) == 0 {
				c[i] = alphabet[r.Intn(len(alphabet))]
			} else {
				c[i] = byte(0x61 + r.Intn(26))
			}
		}
		out = append(out, c)
	}
	return out
}

// fromAlignProgram reads each case as hex, copies it to offsets 0 to 7 of a
// buffer whose other bytes are 0xBF (a continuation byte, so a validator
// that reads before the start or past the end misjudges), and prints the set
// of offsets at which str.From accepted it: 255 for valid UTF-8, else 0.
func fromAlignProgram() textProgram {
	cases := fromCases(rand.New(rand.NewSource(15)), 100000)
	var in, want strings.Builder
	for _, c := range cases {
		fmt.Fprintf(&in, "%x\n", c)
		if utf8.Valid(c) {
			want.WriteString("255\n")
		} else {
			want.WriteString("0\n")
		}
	}
	src := `HexVal(c: u8): u8 {
    if c <= 0x39 {
        return c - 0x30
    }
    return c - 0x57
}

FromOk(b: []u8): bool {
    s := str.From(b) else {
        return false
    }
    return s.Len() == b.Len()
}

export Main(sys: &System): ReadError|OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    var inp := sys.Stdin()
    var bytes: [256]u8 = [0; 256]
    var buf: [272]u8 = [0xBF; 272]
    for {
        l := try inp.Line(sys.arena)
        s := l else {
            break
        }
        n := s.Len() / 2
        for i in 0..n {
            bytes[i] = HexVal(s.Byte(2 * i)) * 16 + HexVal(s.Byte(2 * i + 1))
        }
        var mask := 0
        for k in 0..8 {
            for i in 0..n {
                buf[k + i] = bytes[i]
            }
            if FromOk(buf[k..k + n]) {
                mask |= 1 << k
            }
            for i in 0..n {
                buf[k + i] = 0xBF
            }
        }
        try Print(mut out, "{mask}\n", (mask))
    }
    try out.Flush()
}
`
	return textProgram{"from", "", "", src, in.String(), want.String()}
}

// TestSTDTEXT_FromAtEveryAlignment checks str.From (ValidUtf8, whose fast
// path loads 8 bytes with one align 1 load) natively at release, on at least
// 100,000 cases each at offsets 0 to 7, against Go's utf8.Valid. The Linux
// leg is in
// TestSTDTEXT_LinuxNative.
func TestSTDTEXT_FromAtEveryAlignment(t *testing.T) {
	t.Parallel()
	p := fromAlignProgram()
	for _, flags := range [][]string{nil, {"--release"}} {
		r := runNative(t, buildWithModule(t, p.rel, p.module, p.src, flags...), p.stdin)
		if r.code != 0 {
			t.Fatalf("%v: exit %d\n%s", flags, r.code, r.stderr)
		}
		t.Logf("%v: %d cases at 8 offsets in %v", flags, strings.Count(p.want, "\n"), r.took)
		compareLines(t, r.stdout, p.want)
	}
}

// TestSTDTEXT_CaseMapOneConst maps every caseEdges scalar (1,121)
// both ways in one const. When tables were read by value, each CaseOf copied the 348-entry
// table per access and one const of 240 scalars took most of the 4,000,000
// steps of fuel, so this stopped with E0902.
func TestSTDTEXT_CaseMapOneConst(t *testing.T) {
	t.Parallel()
	sc := goCase()
	edges := caseEdges(sc)
	var in, lo, up []string
	for i := 0; i < len(edges); i += 120 {
		s := string(edges[i:min(i+120, len(edges))])
		in = append(in, mkString(s))
		lo = append(lo, mkString(sc.mapString(s, false)))
		up = append(up, mkString(sc.mapString(s, true)))
	}
	n := len(in)
	fixture := probeHeap + fmt.Sprintf(`
const R3In: [%[1]d]str = [%[2]s]
const R3Lo: [%[1]d]str = [%[3]s]
const R3Up: [%[1]d]str = [%[4]s]

R3Check(): int {
    var mem: [16384]u8 = [0; 16384]
    unsafe "fixture" {
        heap := MakeHeap(raw.DataMut(mut mem[..]), raw.Of(&ProbeHeapTable))
        var a := Arena.New(&heap) else { return 90 }
        var bad := 0
        for k in 0..%[1]d {
            l := R3In[k].ToLower(&a) else {
                a.Free()
                return 91
            }
            if l != R3Lo[k] {
                bad += 1
            }
            u := R3In[k].ToUpper(&a) else {
                a.Free()
                return 92
            }
            if u != R3Up[k] {
                bad += 1
            }
            a.Reset()
        }
        a.Free()
        return bad
    }
}

const R3CaseOk = [0; 1][R3Check()]
`, n, strings.Join(in, ", "), strings.Join(lo, ", "), strings.Join(up, ", "))
	t.Logf("%d scalars in one const", len(edges))
	expectClean(t, "stdtext_r3_case", fixture)
}

// TestSTDTEXT_ParseFloatScaledOneConst parses 1,100 strings that take
// ParseScaled64 (exponents 40 to 300 away from zero, so Clinger's exact path
// does not apply; a third with 19 to 25 digits, so the d + 1 check runs too)
// in one const, against strconv. When tables were read by value, each
// FpPrescale copied
// the 696-entry Pow10Tab, and the first 950 of these cases already
// exhausted the fuel (E0902); now 1,300 fit.
func TestSTDTEXT_ParseFloatScaledOneConst(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewSource(33))
	var cases []parseCase
	for len(cases) < 1100 {
		digits := 1 + r.Intn(17)
		if r.Intn(3) == 0 {
			digits = 19 + r.Intn(7)
		}
		var sb strings.Builder
		sb.WriteByte(byte('1' + r.Intn(9)))
		for i := 1; i < digits; i++ {
			if i == 1 {
				sb.WriteByte('.')
			}
			sb.WriteByte(byte('0' + r.Intn(10)))
		}
		e := 40 + r.Intn(261)
		if r.Intn(2) == 0 {
			e = -e
		}
		sb.WriteString("e" + strconv.Itoa(e))
		c := goParse(sb.String())
		if !c.ok || math.IsInf(math.Float64frombits(c.bits), 0) {
			continue
		}
		cases = append(cases, c)
	}
	expectClean(t, "stdtext_r3_parse", parseFixture("R3pf", cases))
}

// mkTestName is a test block's header.
var mkTestName = regexp.MustCompile(`(?m)^test "([^"]*)"`)

// textBlockFiles are the text test block files, by module.
var textBlockFiles = map[string][]string{
	"std.core": {"core/tests_str.mk", "core/tests_fmt.mk", "core/tests_parse.mk"},
	"std.math": {"math/tests_math.mk"},
}

// TestSTDTEXT_MkTestBlocks runs the blocks of textBlockFiles with mk
// test, at dev (contracts on, the counting heap: a leak fails with exit 102)
// and at release. Each block must PASS, under a name that is not empty and
// is unique in its module. The other core/tests_*.mk files are
// left out of the copy of std, so a block there that does not check cannot
// hide these; they run in their own tests. An empty str literal once had a
// null data pointer under codegen's !nonnull and failed at release.
func TestSTDTEXT_MkTestBlocks(t *testing.T) {
	t.Parallel()
	names := map[string][]string{}
	mine := map[string]bool{}
	for mod, files := range textBlockFiles {
		for _, f := range files {
			mine[f] = true
			text, err := os.ReadFile(filepath.FromSlash(f))
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range mkTestName.FindAllStringSubmatch(string(text), -1) {
				names[mod] = append(names[mod], m[1])
			}
		}
		if len(names[mod]) == 0 {
			t.Fatalf("%s: no test blocks in %v", mod, files)
		}
	}
	for mod, ns := range names {
		seen := map[string]bool{}
		for _, n := range ns {
			if strings.TrimSpace(n) == "" {
				t.Errorf("%s: a test block has an empty name", mod)
			}
			if seen[n] {
				t.Errorf("%s: two test blocks are named %q", mod, n)
			}
			seen[n] = true
		}
	}
	root := t.TempDir()
	stdDir := copyStd(t, root, nil)
	others, _ := filepath.Glob(filepath.Join(stdDir, "core", "tests_*.mk"))
	for _, p := range others {
		if !mine["core/"+filepath.Base(p)] {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, "main.mk"), []byte("export Main(sys: &System) {\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mod := range []string{"std.core", "std.math"} {
		for _, flags := range [][]string{nil, {"--release"}} {
			args := append([]string{"test", "--std=" + mod}, flags...)
			out, status := mkCmd(t, root, stdDir, args...)
			if strings.Contains(out, "no usable LLVM") {
				t.Skipf("needs the LLVM toolchain: %s", out)
			}
			for _, n := range names[mod] {
				if !strings.Contains(out, "PASS "+mod+" "+strconv.Quote(n)+"\n") {
					t.Errorf("mk %s: %q did not pass (exit %d)\n%s", strings.Join(args, " "), n, status, out)
				}
			}
		}
	}
}

// TestSTDTEXT_StrPanics: the documented str and Text panics, each in its own
// process, since a panic fails a test block: Byte out of range is a bounds
// panic (code 2), an empty Split separator code 8, Text.Drain off a scalar
// boundary code 10.
func TestSTDTEXT_StrPanics(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, body, want string }{
		{"Byte out of range", "s := \"abc\"\n    _ = s.Byte(sys.args.Len() + 2)", "panic: bounds"},
		{"Byte below zero", "s := \"abc\"\n    _ = s.Byte(sys.args.Len() - 2)", "panic: bounds"},
		{"Split with an empty separator", "var it := \"a,b\".Split(\"\")\n    _ = it.Next()", "str.Split: empty separator"},
		{"Drain off a scalar boundary", "var t := Text.New(sys.arena)\n    t.Push(\"\\u{E9}\") else {\n        return\n    }\n    t.Drain(1)", "Text.Drain: not on a scalar boundary"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			src := "export Main(sys: &System) {\n    " + c.body + "\n}\n"
			r := runNative(t, buildNative(t, nil, src), "")
			if r.code != 101 || !strings.Contains(r.stderr, c.want) {
				t.Errorf("exit %d, want 101 with %q\nstderr:\n%s", r.code, c.want, r.stderr)
			}
		})
	}
}
