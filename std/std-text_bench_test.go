// Why: the text kernels (formatting, parsing, str) need rerunnable timings.
// What: release benchmark programs, one per kernel, with Go baselines.
// How: each program times its kernel with time.Now, three runs; the test logs
// the best run beside Go's. Only with MK_TEXT_BENCH=1; logged, never judged.

package std

import (
	"bufio"
	"bytes"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// tbenchFixture is a std.core module: a Writer that discards (so Print and
// FmtF64 are timed without I/O) and a Reader over a str that serves step
// bytes per table call.
const tbenchFixture = `import std.raw

const ZzSinkTable = WriterTable(write: ZzSinkWrite, flush: ZzSinkFlush)

ZzSinkWrite(w: mut Writer, b: []u8): WriteError!void {
}

ZzSinkFlush(w: mut Writer): WriteError!void {
}

export ZzSink(buf: mut []u8): Writer from buf {
    unsafe "bench: the writer's buffer is buf" {
        return Writer(io: InertIoValue(), table: &ZzSinkTable, ctx: raw.DataMut(buf), buf: raw.DataMut(buf), used: 0, cap: buf.Len())
    }
}

export ZzFmtF64(w: mut Writer, x: f64): WriteError!void {
    try FmtF64(w, x, 0)
}

struct ZzMemSrc {
    data: str
    pos: int
    step: int
}

const ZzMemTable = ReaderTable(read: ZzMemRead, line: ZzMemRead)

ZzMemRead(r: mut Reader, buf: mut []u8): ReadError!int {
    unsafe "bench: ctx is the ZzMemSrc" {
        src := mut *raw.PtrCast[u8, ZzMemSrc](r.ctx)
        var n := src.data.Len() - src.pos
        if n > src.step {
            n = src.step
        }
        if n > buf.Len() {
            n = buf.Len()
        }
        b := src.data.AsBytes()
        for i in 0..n {
            buf[i] = b[src.pos + i]
        }
        src.pos += n
        return n
    }
}

// ZzMemLines counts the lines of data read through a Reader with a buffer
// of cap bytes over a table that serves step bytes per call; a ReadError
// or OutOfMemory is -1.
export ZzMemLines(a: &Arena, data: str, step: int, cap: int): int {
    var src := ZzMemSrc(data: data, pos: 0, step: step)
    buf := a.Slice[u8](cap) else {
        return -1
    }
    var r := ZzMemReader(mut src, buf)
    var n := 0
    for {
        l := r.Line(a) else {
            return -1
        }
        _ = l else {
            return n
        }
        n += 1
    }
}

ZzMemReader(src: mut ZzMemSrc, buf: mut []u8): Reader from src, buf {
    unsafe "bench" {
        return Reader(io: InertIoValue(), table: &ZzMemTable, ctx: raw.PtrCast[ZzMemSrc, u8](raw.OfMut(src)),
            buf: raw.DataMut(buf), start: 0, end: 0, cap: buf.Len())
    }
}
`

// tbenchCompanion is the std module a program reaches the fixture through.
const tbenchCompanion = `import std.core

export Sink(buf: mut []u8): Writer from buf {
    return core.ZzSink(buf)
}

export F64(w: mut Writer, x: f64): WriteError!void {
    try core.ZzFmtF64(w, x)
}

export MemLines(a: &Arena, data: str, step: int, cap: int): int {
    return core.ZzMemLines(a, data, step, cap)
}
`

// tbenchMain prints one line per kernel: name, nanoseconds, checksum.
const tbenchMain = `import std.time
import std.zzbits
import std.zztbench

Mib(a: &Arena, n: int, c: u8): OutOfMemory!str from a {
    b := try a.Slice[u8](n)
    for i in 0..n {
        b[i] = c
    }
    s := str.From(b[0..n]) else {
        return ""
    }
    return s
}

Since(io: Io, t0: time.Instant): i64 {
    return time.Now(io).Since(t0).Nanos()
}

export Main(sys: &System): OutOfMemory|WriteError!void {
    var out := sys.Stdout()
    io := sys.io
    a := sys.arena
    h := try Mib(a, 1048576, 0x61)
    var nt := Text.New(a)
    for _ in 0..1024 {
        try nt.Push("a")
    }
    try nt.Push("b")
    needle := nt.Str()

    var t0 := time.Now(io)
    var c := 0
    for _ in 0..10 {
        if i := h.Find(needle) {
            c += i
        } else {
            c += 1
        }
    }
    var ns := Since(io, t0)
    try Print(mut out, "find_worst {ns} {c}\n", (ns, c))

    t0 = time.Now(io)
    c = 0
    for k in 0..1000 {
        sub := h[k % 8..h.Len()]
        if i := sub.Find("\n") {
            c += i
        } else {
            c += 1
        }
    }
    ns = Since(io, t0)
    try Print(mut out, "find_byte {ns} {c}\n", (ns, c))

    var sbuf: [65536]u8 = [0; 65536]
    var w := zztbench.Sink(mut sbuf[..])
    var s: u64 = 0
    t0 = time.Now(io)
    for _ in 0..10000000 {
        s = s +% 0x9E3779B97F4A7C15
        x := zzbits.F64From(zzbits.Mix(s))
        try Print(mut w, "{x}\n", (x))
    }
    ns = Since(io, t0)
    c = Trunc[int](s & 0xFF)
    try Print(mut out, "print_f64 {ns} {c}\n", (ns, c))

    s = 0
    t0 = time.Now(io)
    for _ in 0..10000000 {
        s = s +% 0x9E3779B97F4A7C15
        try zztbench.F64(mut w, zzbits.F64From(zzbits.Mix(s)))
    }
    ns = Since(io, t0)
    c = Trunc[int](s & 0xFF)
    try Print(mut out, "fmt_f64 {ns} {c}\n", (ns, c))

    s = 0
    t0 = time.Now(io)
    for i in 0..10000000 {
        s = s +% 0x9E3779B97F4A7C15
        v := Trunc[i64](zzbits.Mix(s) >> Trunc[u64](i % 60))
        try Print(mut w, "{v}\n", (v))
    }
    ns = Since(io, t0)
    c = Trunc[int](sbuf[0]) + Trunc[int](sbuf[65535])
    try Print(mut out, "print_i64 {ns} {c}\n", (ns, c))

    var ft := Text.New(a)
    var fw := ft.Writer()
    s = 0
    for _ in 0..1024 {
        s = s +% 0x9E3779B97F4A7C15
        x := zzbits.F64From(zzbits.Mix(s) & 0x7FEFFFFFFFFFFFFF)
        try Print(mut fw, "{x}\n", (x))
    }
    floats := ft.Str()
    var bits: u64 = 0
    t0 = time.Now(io)
    for _ in 0..10000 {
        var it := floats.Lines()
        for {
            l := it.Next() else {
                break
            }
            v := l.ParseFloat() else {
                bits += 1
                continue
            }
            bits = bits +% zzbits.F64Bits(v)
        }
    }
    ns = Since(io, t0)
    try Print(mut out, "parse_float {ns} {bits:x}\n", (ns, bits))

    var it := Text.New(a)
    var iw := it.Writer()
    s = 0
    for i in 0..1024 {
        s = s +% 0x9E3779B97F4A7C15
        v := Trunc[i64](zzbits.Mix(s) % 1000000000000000000) >> Trunc[u64](i % 60)
        try Print(mut iw, "{v}\n", (v))
    }
    ints := it.Str()
    var sum: i64 = 0
    t0 = time.Now(io)
    for _ in 0..10000 {
        var li := ints.Lines()
        for {
            l := li.Next() else {
                break
            }
            v := l.ParseInt() else {
                sum += 1
                continue
            }
            sum = sum +% v
        }
    }
    ns = Since(io, t0)
    try Print(mut out, "parse_int {ns} {sum}\n", (ns, sum))

    ascii := try Mib(a, 16777216, 0x61)
    var et := Text.New(a)
    for _ in 0..8388608 {
        try et.Push("\u{E9}")
    }
    two := et.Str()
    t0 = time.Now(io)
    c = 0
    for k in 0..16 {
        var src := ascii
        if k % 2 == 1 {
            src = two
        }
        v := str.From(src.AsBytes()) else {
            continue
        }
        c += v.Len()
    }
    ns = Since(io, t0)
    try Print(mut out, "from_utf8 {ns} {c}\n", (ns, c))

    pre := try Mib(a, 4096, 0x61)
    base := try Mib(a, 8192, 0x61)
    t0 = time.Now(io)
    c = 0
    for k in 0..100000 {
        r := base[k % 8..base.Len()]
        if r.StartsWith(pre) {
            c += 1
        }
    }
    ns = Since(io, t0)
    try Print(mut out, "starts_with {ns} {c}\n", (ns, c))

    var lt := Text.New(a)
    for _ in 0..131072 {
        try lt.Push("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n")
    }
    lines := lt.Str()
    t0 = time.Now(io)
    c = zztbench.MemLines(a, lines, 4000, 65536)
    ns = Since(io, t0)
    try Print(mut out, "reader_line {ns} {c}\n", (ns, c))

    var mt := Text.New(a)
    for _ in 0..524288 {
        try mt.Push("Ab\u{E9}\u{3B1}z\u{10428}")
    }
    mixed := mt.Str()
    var sub := try a.Sub()
    defer sub.Free()
    t0 = time.Now(io)
    c = 0
    for _ in 0..4 {
        u := try mixed.ToUpper(&sub)
        c += u.Len()
        l := try u.ToLower(&sub)
        c += l.Len()
        sub.Reset()
    }
    ns = Since(io, t0)
    try Print(mut out, "case_map {ns} {c}\n", (ns, c))
    try out.Flush()
}
`

// tbenchUnits is what each kernel's time is divided by, and the unit.
var tbenchUnits = map[string]struct {
	n    float64
	unit string
}{
	"find_worst":  {10 * 1048576, "ps/byte"},
	"find_byte":   {1000 * 1048576, "ps/byte"},
	"print_f64":   {1e7, "ns/value"},
	"fmt_f64":     {1e7, "ns/value"},
	"print_i64":   {1e7, "ns/value"},
	"parse_float": {10000 * 1024, "ns/parse"},
	"parse_int":   {10000 * 1024, "ns/parse"},
	"from_utf8":   {16 * 16777216, "ps/byte"},
	"starts_with": {100000, "ns/call"},
	"reader_line": {131072 * 49, "ps/byte"},
	"case_map":    {4 * 2 * 524288 * 11, "ps/byte"},
}

// TestSTDTEXT_Bench builds tbenchMain at release, runs it three times and
// logs each kernel's best time with Go baselines of the same work.
func TestSTDTEXT_Bench(t *testing.T) {
	if os.Getenv("MK_TEXT_BENCH") != "1" {
		t.Skip("set MK_TEXT_BENCH=1 to run the std-text benchmarks")
	}
	root := t.TempDir()
	stdDir := copyStd(t, root, map[string]string{
		"core/zz_tbench.mk":  tbenchFixture,
		"zztbench/tbench.mk": tbenchCompanion,
		"zzbits/bits.mk":     bitsModule,
	})
	if err := os.WriteFile(root+"/main.mk", []byte(tbenchMain), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := root + "/tbench.exe"
	if out, status := mkCmd(t, root, stdDir, "build", "--release", "-o", exe, "main.mk"); status != 0 {
		t.Fatalf("mk build: exit %d\n%s", status, out)
	}
	best := map[string]int64{}
	sums := map[string]string{}
	var order []string
	for run := 0; run < 3; run++ {
		r := runNative(t, exe, "")
		if r.code != 0 {
			t.Fatalf("exit %d\n%s", r.code, r.stderr)
		}
		for _, line := range strings.Split(strings.TrimSpace(r.stdout), "\n") {
			f := strings.Fields(line)
			if len(f) != 3 {
				t.Fatalf("bad line %q", line)
			}
			ns, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := best[f[0]]; !ok {
				order = append(order, f[0])
				best[f[0]] = ns
				sums[f[0]] = f[2]
			}
			if sums[f[0]] != f[2] {
				t.Errorf("%s: checksum %s, then %s", f[0], sums[f[0]], f[2])
			}
			best[f[0]] = min(best[f[0]], ns)
		}
	}
	goNs := tbenchGo()
	for _, k := range order {
		u := tbenchUnits[k]
		per := float64(best[k]) / u.n
		if strings.HasPrefix(u.unit, "ps") {
			per *= 1000
		}
		line := k + ": best of 3 " + strconv.FormatFloat(float64(best[k])/1e6, 'f', 1, 64) + " ms, " + strconv.FormatFloat(per, 'f', 1, 64) + " " + u.unit
		if g, ok := goNs[k]; ok {
			gp := g / u.n
			if strings.HasPrefix(u.unit, "ps") {
				gp *= 1000
			}
			line += "; Go " + strconv.FormatFloat(gp, 'f', 1, 64) + " " + u.unit
		}
		t.Log(line)
	}
}

// tbenchGo times the Go standard library on the same work as each kernel,
// best of three, in nanoseconds for the whole kernel.
func tbenchGo() map[string]float64 {
	res := map[string]float64{}
	bestOf := func(name string, f func()) {
		b := time.Duration(1 << 62)
		for i := 0; i < 3; i++ {
			t0 := time.Now()
			f()
			b = min(b, time.Since(t0))
		}
		res[name] = float64(b.Nanoseconds())
	}
	h := strings.Repeat("a", 1048576)
	needle := strings.Repeat("a", 1024) + "b"
	sink := 0
	bestOf("find_worst", func() {
		for i := 0; i < 10; i++ {
			sink += strings.Index(h, needle)
		}
	})
	bestOf("find_byte", func() {
		for k := 0; k < 1000; k++ {
			sink += strings.IndexByte(h[k%8:], '\n')
		}
	})
	mix := func(z uint64) uint64 {
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		return z ^ (z >> 31)
	}
	buf := make([]byte, 0, 64)
	bestOf("fmt_f64", func() {
		var s uint64
		for i := 0; i < 10000000; i++ {
			s += 0x9E3779B97F4A7C15
			buf = strconv.AppendFloat(buf[:0], math.Float64frombits(mix(s)), 'e', -1, 64)
		}
	})
	bestOf("print_i64", func() {
		var s uint64
		for i := 0; i < 10000000; i++ {
			s += 0x9E3779B97F4A7C15
			buf = strconv.AppendInt(buf[:0], int64(mix(s)>>(uint(i)%60)), 10)
		}
	})
	var floats, ints []string
	var s uint64
	for i := 0; i < 1024; i++ {
		s += 0x9E3779B97F4A7C15
		floats = append(floats, strconv.FormatFloat(math.Float64frombits(mix(s)&0x7FEFFFFFFFFFFFFF), 'g', -1, 64))
		ints = append(ints, strconv.FormatInt(int64(mix(s)%1000000000000000000)>>(uint(i)%60), 10))
	}
	bestOf("parse_float", func() {
		for p := 0; p < 10000; p++ {
			for _, f := range floats {
				v, _ := strconv.ParseFloat(f, 64)
				sink += int(v)
			}
		}
	})
	bestOf("parse_int", func() {
		for p := 0; p < 10000; p++ {
			for _, f := range ints {
				v, _ := strconv.ParseInt(f, 10, 64)
				sink += int(v)
			}
		}
	})
	ascii := bytes.Repeat([]byte("a"), 16777216)
	two := bytes.Repeat([]byte("é"), 8388608)
	bestOf("from_utf8", func() {
		for k := 0; k < 16; k++ {
			if k%2 == 1 {
				sink += len(two) * b2i(utf8.Valid(two))
			} else {
				sink += len(ascii) * b2i(utf8.Valid(ascii))
			}
		}
	})
	pre := strings.Repeat("a", 4096)
	base := strings.Repeat("a", 8192)
	bestOf("starts_with", func() {
		for k := 0; k < 100000; k++ {
			sink += b2i(strings.HasPrefix(base[k%8:], pre))
		}
	})
	lines := strings.Repeat(strings.Repeat("x", 48)+"\n", 131072)
	bestOf("reader_line", func() {
		sc := bufio.NewScanner(strings.NewReader(lines))
		sc.Buffer(make([]byte, 65536), 65536)
		for sc.Scan() {
			sink += len(sc.Text())
		}
	})
	mixed := strings.Repeat("Abéαz\U00010428", 524288)
	bestOf("case_map", func() {
		for i := 0; i < 4; i++ {
			sink += len(strings.ToLower(strings.ToUpper(mixed)))
		}
	})
	_ = sink
	return res
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
