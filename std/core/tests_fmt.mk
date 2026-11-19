// Why: formatting is how every program prints; its holes, specs and field
// paths must write exactly the expected bytes.
// What: test blocks for every primitive Format, Print through a Writer, user
// Formats, specs (radix, sign, zero, width, fill, precision), field paths,
// the digit-pair decimal loops and FlushStdCode.
// How: run by mk test --std=std.core with contracts on and the counting heap;
// every block frees what it allocates.

import std.raw

// FmtBad's Format always fails, as a user Format may.
struct FmtBad {
    n: int
}

impl Format for FmtBad {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("partial")
        fail WriteError
    }
}

// FmtPair is a user record with a Format of its own.
struct FmtPair {
    a: int
    b: str
}

impl Format for FmtPair {
    Format(&self, w: mut Writer): WriteError!void {
        try Print(w, "({a}, {b})", (a: self.a, b: self.b))
    }
}

FmtDump[A: Fields[Format] + Plain](w: mut Writer, args: A): WriteError!void {
    for name, value in fields(&args) {
        try w.Str(name)
        try value.Format(w)
    }
}

test "every primitive Format" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a: i8 = -128
    b: i16 = -32768
    c: i32 = -2147483648
    d: i64 = -9223372036854775807 - 1
    e: u8 = 255
    f: u16 = 65535
    g: u32 = 4294967295
    h: u64 = 18446744073709551615
    i: int = -1
    j: uint = 0
    Assert(try ar.Fmt("x{a} {b} {c} {d}", (a, b, c, d)) == "x-128 -32768 -2147483648 -9223372036854775808")
    Assert(try ar.Fmt("x{e} {f} {g} {h} {i} {j}", (e, f, g, h, i, j)) == "x255 65535 4294967295 18446744073709551615 -1 0")
    k: f32 = 0.1
    l: f64 = 0.1
    m: f64 = 1e21
    n: f64 = -0.0
    Assert(try ar.Fmt("x{k} {l} {m} {n}", (k, l, m, n)) == "x0.1 0.1 1e+21 -0")
    t := true
    u := 'é'
    v := "s\u{E9}"
    Assert(try ar.Fmt("x{t} {u} {v}", (t, u, v)) == "xtrue \u{E9} s\u{E9}")
    r := &a
    Assert(try ar.Fmt("x{r}", (r)) == "x-128")
}

test "Print writes through a Text writer" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    var w := t.Writer()
    x := 42
    try Print(mut w, "a{x}b\n", (x))
    try Print(mut w, "done\n")
    try w.Flush()
    Assert(t.Str() == "a42b\ndone\n")
}

test "a user Format in a hole" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    p := FmtPair(a: 7, b: "seven")
    Assert(try ar.Fmt("p={p}", (p)) == "p=(7, seven)")
}

test "hex, precision and braces" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a: i32 = -1
    b: i8 = -2
    c: u64 = 0xDEADBEEF
    d := 0
    Assert(try ar.Fmt("x{a:x} {b:x} {c:x} {d:x}", (a, b, c, d)) == "xffffffff fe deadbeef 0")
    x := 3.14159
    y := 2.5
    z := 0.125
    Assert(try ar.Fmt("x{x:.2} {y:.0} {z:.2} {x:.0}", (x, y, z)) == "x3.14 2 0.12 3")
    Assert(try ar.Fmt("{{x}} {{") == "{x} {")
    Assert(try ar.Fmt("}}") == "}")
}

test "a named const format string" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    n := 3
    Assert(try ar.Fmt(FmtLine, (n)) == "n=3;")
}

const FmtLine = "n={n};"

test "many holes in one call" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a := 1
    b := 2
    c := 3
    d := 4
    e := 5
    f := 6
    g := 7
    h := 8
    Assert(try ar.Fmt("<{a}{b}{c}{d}{e}{f}{g}{h}>", (a, b, c, d, e, f, g, h)) == "<12345678>")
}

test "FmtDump writes each field's name and value" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    var w := t.Writer()
    try FmtDump(mut w, (x: 1, y: "two"))
    Assert(t.Str() == "x1ytwo")
}

test "a user Format that fails inside Arena.Fmt is OutOfMemory" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    bad := FmtBad(n: 1)
    var got := 0
    _ = ar.Fmt("x{bad}", (bad)) else e {
        match e {
            OutOfMemory {
                got = 1
            }
        }
    }
    Assert(got == 1)
    // The arena still works after the failed call.
    Assert(try ar.Fmt("y{got}", (got)) == "y1")
}

test "Text.Writer refuses invalid UTF-8" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    var w := t.Writer()
    bad: [2]u8 = [0xC3, 0x28]
    var refused := false
    w.Bytes(bad[..]) else {
        refused = true
    }
    w.Flush() else {
        refused = true
    }
    Assert(refused)
    Assert(t.Str() == "")
}

// PrintCon is the kernel's shape: a console that owns its
// Writer and prints through a forwarding Print method, as System.Print does.
view struct PrintCon {
    w: mut Writer
}

PrintCon.Print[A: Fields[Format] + Plain](mut self, const f: str, args: A): WriteError!void {
    try Print(mut *self.w, f, args)
}

// PrintVia forwards twice: the short form reaches Print through two
// forwarders.
PrintVia[A: Fields[Format] + Plain](c: mut PrintCon, const f: str, args: A): WriteError!void {
    try c.Print(f, args)
}

test "System.Print and PrintErr print, with and without a record" (sys: &System) {
    try sys.Print("")
    try sys.Print("{n}", (n: ""))
    try sys.PrintErr("")
}

test "a forwarding Print method takes the short form" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    var c := PrintCon(w: mut w)
    try c.Print("a ")
    try c.Print("{n} ", (n: 1))
    try PrintVia(mut c, "b ")
    try PrintVia(mut c, "{x}", (x: 2.5))
    Assert(t.Str() == "a 1 b 2.5")
}

test "Format for optionals, slices and lists" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    o: ?int = some(3)
    n: ?int = none
    s: ?str = some("x y")
    arr := ["a", "b c"]
    xs := arr[..]
    ys := arr[0..0]
    Assert(try ar.Fmt("{o} {n} {s} {xs} {ys}", (o, n, s, xs, ys)) == "some(3) none some(x y) [a, b c] []")
    var l := List[?int].New(&ar)
    try l.Push(some(1))
    try l.Push(none)
    var e := List[int].New(&ar)
    var ll := List[[]str].New(&ar)
    try ll.Push(xs)
    try ll.Push(ys)
    Assert(try ar.Fmt("{l} {e} {ll}", (l: &l, e: &e, ll: &ll)) == "[some(1), none] [] [[a, b c], []]")
    p: ?FmtPair = some(FmtPair(a: 1, b: "q"))
    Assert(try ar.Fmt("{p}", (p)) == "some((1, q))")
    var h := HeapList[i8].New(sys.heap.Alloc())
    defer h.Free(sys.heap.Alloc())
    try h.Push(sys.heap.Alloc(), -128)
    try h.Push(sys.heap.Alloc(), 127)
    Assert(try ar.Fmt("{h}", (h: &h)) == "[-128, 127]")
}

// PrintRow is a row printed by field paths.
struct PrintRow {
    export name: str
    export at: PrintAt
    export n: i64
}

struct PrintAt {
    export x: int
    export y: f64
}

impl Format for PrintRow {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str(self.name)
    }
}

impl Format for PrintAt {
    Format(&self, w: mut Writer): WriteError!void {
        try Print(w, "({x}, {y})", (x: self.x, y: self.y))
    }
}

test "Print of field paths, nested, through a reference and through forwarders" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    r := PrintRow(name: "ann", at: PrintAt(x: 255, y: 0.5), n: -9223372036854775807 - 1)
    Assert(try ar.Fmt("{r.name} {r.at.x} {r.at.y:.2} {r.at} {r} {r.n}", (r)) == "ann 255 0.50 (255, 0.5) ann -9223372036854775808")
    p := &r
    Assert(try ar.Fmt("{p.at.x:x}|{p.name}|{k}", (p, k: 1)) == "ff|ann|1")
    var t := Text.New(sys.arena)
    var w := t.Writer()
    var c := PrintCon(w: mut w)
    try PrintVia(mut c, "{r.name}={r.at.x}", (r))
    Assert(t.Str() == "ann=255")
}

test "format specs: radix, sign and zero at every integer width and limit" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a: i8 = -128
    b: i16 = -32768
    c: i32 = -2147483648
    d: i64 = -9223372036854775807 - 1
    e: int = -9223372036854775807 - 1
    Assert(try ar.Fmt("{a:x} {a:X} {a:b} {a:o} {a:+} {a:05}", (a)) == "80 80 10000000 200 -128 -0128")
    Assert(try ar.Fmt("{b:x} {b:o} {b:+08}", (b)) == "8000 100000 -0032768")
    Assert(try ar.Fmt("{c:X} {c:o} {c:b}", (c)) == "80000000 20000000000 10000000000000000000000000000000")
    Assert(try ar.Fmt("{d:x} {d:o} {d:+} {d:021}", (d)) == "8000000000000000 1000000000000000000000 -9223372036854775808 -09223372036854775808")
    Assert(try ar.Fmt("{d:b}", (d)) == "1000000000000000000000000000000000000000000000000000000000000000")
    Assert(try ar.Fmt("{e:X} {e:+}", (e)) == "8000000000000000 -9223372036854775808")
    f: u8 = 255
    g: u16 = 65535
    h: u32 = 4294967295
    i: u64 = 18446744073709551615
    j: uint = 18446744073709551615
    Assert(try ar.Fmt("{f:x} {f:X} {f:b} {f:o} {f:+} {f:06} {f:+06}", (f)) == "ff FF 11111111 377 +255 000255 +00255")
    Assert(try ar.Fmt("{g:x} {g:o} {h:X} {h:o}", (g, h)) == "ffff 177777 FFFFFFFF 37777777777")
    Assert(try ar.Fmt("{i:x} {i:o} {i:+} {j:X}", (i, j)) == "ffffffffffffffff 1777777777777777777777 +18446744073709551615 FFFFFFFFFFFFFFFF")
    Assert(try ar.Fmt("{i:b}", (i)) == "1111111111111111111111111111111111111111111111111111111111111111")
    z := 0
    q: i8 = 127
    Assert(try ar.Fmt("{z:x} {z:b} {z:o} {z:+} {z:03} {q:+04} {q:+05}", (z, q)) == "0 0 0 +0 000 +127 +0127")
}

test "format specs: width, fill and align on any Format" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    u: u8 = 255
    s := "ab"
    Assert(try ar.Fmt("{u:6}|{u:<6}|{u:^7}|{u:*^8}|{u:2}", (u)) == "   255|255   |  255  |**255***|255")
    Assert(try ar.Fmt("{s:5}|{s:>5}|{s:^5}|{s:-<4}|{s::>3}", (s)) == "ab   |   ab| ab  |ab--|:ab")
    Assert(try ar.Fmt("{u:#>6x}|{u:#<10b}|{u}{u:x}{u:4}", (u)) == "####ff|11111111##|255ff 255")
    Assert((try ar.Fmt("{s:255}", (s))).Len() == 255)
    a: i8 = -128
    Assert(try ar.Fmt("{a:6}|{a:06}|{a:<6}|", (a)) == "  -128|-00128|-128  |")
    p := FmtPair(a: 7, b: "seven")
    Assert(try ar.Fmt("{p:>14}|{p:<14}|{p:3}", (p)) == "    (7, seven)|(7, seven)    |(7, seven)")
    t := true
    v := "\u{E9}"
    o: ?int = some(3)
    Assert(try ar.Fmt("{t:^6}|{v:4}|{o:>8}", (t, v, o)) == " true |\u{E9}  | some(3)")
    r := PrintRow(name: "ann", at: PrintAt(x: 255, y: 0.5), n: 1)
    Assert(try ar.Fmt("{r.name:>5}|{r.at.x:04x}|{r.at:<11}|", (r)) == "  ann|00ff|(255, 0.5) |")
}

test "format specs: floats with sign, zero, width and precision" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    x := 3.14159
    n := -2.5
    z: f64 = -0.0
    Assert(try ar.Fmt("{x:+.2} {n:+} {x:+} {z:+} {z}", (x, n, z)) == "+3.14 -2.5 +3.14159 -0 -0")
    Assert(try ar.Fmt("{x:08.2} {n:08.2} {x:+08.2} {z:05} {z:+08.2}", (x, n, z)) == "00003.14 -0002.50 +0003.14 -0000 -0000.00")
    Assert(try ar.Fmt("{x:>10.3}|{x:<10.3}|{x:^9.1}", (x)) == "     3.142|3.142     |   3.1   ")
    nan := try "nan".ParseFloat()
    inf := try "inf".ParseFloat()
    ninf := try "-inf".ParseFloat()
    Assert(try ar.Fmt("{nan:+08.2}|{inf:+08.2}|{ninf:08}|{nan:+}|{inf:+}|{ninf:<6}|", (nan, inf, ninf)) == "     nan|    +inf|    -inf|nan|+inf|-inf  |")
    k: f32 = 0.1
    Assert(try ar.Fmt("{k:+} {k:06.2} {k:.3}", (k)) == "+0.1 000.10 0.100")
}

test "format specs through Print and a forwarder" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    try Print(mut w, "[{n:>4}|{x:+.1}]", (n: 7, x: 2.25))
    var c := PrintCon(w: mut w)
    try PrintVia(mut c, "[{n:03}|{s:^5}|{h:X}]", (n: 7, s: "ab", h: 255))
    Assert(t.Str() == "[   7|+2.2][007| ab  |FF]")
}

test "radix by shift and mask, decimal by digit pairs, a shared entry" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    a: u64 = 9
    b: u64 = 10
    c: u64 = 99
    d: u64 = 100
    e: u64 = 1000
    f: u64 = 18446744073709551615
    Assert(try ar.Fmt("{a:+} {b:+} {c:+} {d:+} {e:+} {f:+}", (a, b, c, d, e, f)) == "+9 +10 +99 +100 +1000 +18446744073709551615")
    Assert(try ar.Fmt("{a:03} {b:03} {c:03} {d:03} {e:03}", (a, b, c, d, e)) == "009 010 099 100 1000")
    Assert(try ar.Fmt("{a:x} {b:x} {b:X} {d:x} {f:X} {f:o}", (a, b, d, f)) == "9 a A 64 FFFFFFFFFFFFFFFF 1777777777777777777777")
    Assert(try ar.Fmt("{a:b} {b:b} {c:o} {d:o} {e:o} {e:010b}", (a, b, c, d, e)) == "1001 1010 143 144 1750 1111101000")
    n := -1234567
    Assert(try ar.Fmt("{n} {n:x} {n:+} {n:010}", (n)) == "-1234567 ffffffffffed2979 -1234567 -001234567")
    m: i32 = 255
    Assert(try ar.Fmt("{m}|{m:X}|{m}|{m:o}", (m)) == "255|FF|255|377")
}

// TfDecimalMatches is whether "{v}" writes v's decimal digits as one
// digit at a time, the last first, would.
TfDecimalMatches(ar: &Arena, v: u64): OutOfMemory!bool {
    var want: [20]u8 = [0; 20]
    var i := 20
    var x := v
    for {
        i -= 1
        want[i] = Trunc[u8](x % 10) + 0x30
        x = x / 10
        if x == 0 {
            break
        }
    }
    got := (try ar.Fmt("{v}", (v))).AsBytes()
    if got.Len() != 20 - i {
        return false
    }
    for k in 0..got.Len() {
        if got[k] != want[i + k] {
            return false
        }
    }
    return true
}

// TfSpecMatches is whether "{v:+}" and "{v:021}", which take FmtIntSpec's
// decimal loop, write "+" and zeros before the same digits.
TfSpecMatches(ar: &Arena, v: u64): OutOfMemory!bool {
    var want: [20]u8 = [0; 20]
    var i := 20
    var x := v
    for {
        i -= 1
        want[i] = Trunc[u8](x % 10) + 0x30
        x = x / 10
        if x == 0 {
            break
        }
    }
    nd := 20 - i
    plus := (try ar.Fmt("{v:+}", (v))).AsBytes()
    if plus.Len() != nd + 1 || plus[0] != 0x2b {
        return false
    }
    zero := (try ar.Fmt("{v:021}", (v))).AsBytes()
    if zero.Len() != 21 {
        return false
    }
    for k in 0..21 - nd {
        if zero[k] != 0x30 {
            return false
        }
    }
    for k in 0..nd {
        if plus[1 + k] != want[i + k] || zero[21 - nd + k] != want[i + k] {
            return false
        }
    }
    return true
}

test "decimal by digit pairs matches one digit at a time" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // Every value below 100,000: each pair at the last two positions, and
    // the odd and even tails.
    var v: u64 = 0
    for v < 100000 {
        Assert(try TfDecimalMatches(&ar, v))
        ar.Reset()
        v += 1
    }
    // Each power of ten and its neighbours, up to the largest u64.
    var p: u64 = 10
    for _ in 0..19 {
        Assert(try TfDecimalMatches(&ar, p - 1))
        Assert(try TfDecimalMatches(&ar, p))
        Assert(try TfDecimalMatches(&ar, p + 1))
        if p > 1844674407370955161 {
            break
        }
        p = p * 10
    }
    Assert(try TfDecimalMatches(&ar, 18446744073709551615))
    Assert(try TfDecimalMatches(&ar, 18446744073709551614))
    // Spread values, from a splitmix64 sequence.
    var st: u64 = 7
    for _ in 0..20000 {
        st = st +% 0x9e3779b97f4a7c15
        var z := st
        z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
        z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
        z = z ^ (z >> 31)
        Assert(try TfDecimalMatches(&ar, z))
        Assert(try TfDecimalMatches(&ar, z >> Trunc[int](z & 63)))
        ar.Reset()
    }
}

test "a decimal with a sign or zeros by digit pairs matches one digit at a time" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // The values of the block above, through FmtIntSpec ({v:+}, {v:021}).
    var v: u64 = 0
    for v < 100000 {
        Assert(try TfSpecMatches(&ar, v))
        ar.Reset()
        v += 1
    }
    var p: u64 = 10
    for _ in 0..19 {
        Assert(try TfSpecMatches(&ar, p - 1))
        Assert(try TfSpecMatches(&ar, p))
        Assert(try TfSpecMatches(&ar, p + 1))
        if p > 1844674407370955161 {
            break
        }
        p = p * 10
    }
    Assert(try TfSpecMatches(&ar, 18446744073709551615))
    Assert(try TfSpecMatches(&ar, 18446744073709551614))
    var st: u64 = 7
    for _ in 0..20000 {
        st = st +% 0x9e3779b97f4a7c15
        var z := st
        z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
        z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
        z = z ^ (z >> 31)
        Assert(try TfSpecMatches(&ar, z))
        Assert(try TfSpecMatches(&ar, z >> Trunc[int](z & 63)))
        ar.Reset()
    }
}

test "a hole with the zero flag pads itself, nan and inf with spaces" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    nan := try "nan".ParseFloat()
    inf := try "inf".ParseFloat()
    ninf := try "-inf".ParseFloat()
    x := 3.14159
    Assert(try ar.Fmt("{nan:08}|{inf:010}|{ninf:+08.2}|{inf:+03}|{nan:02}|", (nan, inf, ninf)) == "     nan|       inf|    -inf|+inf|nan|")
    Assert(try ar.Fmt("{x:010}|{x:02.3}|{x:+09.4}|{x:>9.1}|", (x)) == "0003.14159|3.142|+003.1416|      3.1|")
    k: f32 = -1.5
    kn: f32 = 0.0 / 0.0
    Assert(try ar.Fmt("{k:07.2}|{kn:+06}|", (k, kn)) == "-001.50|   nan|")
    i := 123456
    j: i8 = -5
    Assert(try ar.Fmt("{i:05}|{i:+09}|{j:04}|{j:4}|", (i, j)) == "123456|+00123456|-005|  -5|")
    var t := Text.New(sys.arena)
    var w := t.Writer()
    try Print(mut w, "[{x:08.2}|{inf:+06}|{nan:6}]", (x, inf, nan))
    Assert(t.Str() == "[00003.14|  +inf|   nan]")
}

// FmtStream is a process stream over b with no handle (0): a test's
// writes fit, so nothing is flushed and no handle is written.
FmtStream(b: mut []u8): BufState {
    unsafe "b outlives the Process built from this stream (the test holds both)" {
        return BufState(lock: raw.Atomic[u32].New(0), h: 0, buf: raw.DataMut(b), len: 0, cap: b.Len(), start: 0)
    }
}

test "sys.Print reaches stdout and sys.PrintErr stderr" (sys: &System) {
    var ob: [64]u8 = [0; 64]
    var eb: [64]u8 = [0; 64]
    var ib: [8]u8 = [0; 8]
    none_env := [EnvVar(name: "", value: "")]
    var p := Process(stdout: FmtStream(mut ob[..]), stderr: FmtStream(mut eb[..]), stdin: FmtStream(mut ib[..]), env: none_env[0..0])
    unsafe "p outlives s, which only this block uses" {
        s := NewSystem(sys.args, sys.io, sys.arena, sys.heap, raw.OfMut(mut p))
        try s.Print("out {n}\n", (n: 1))
        try s.PrintErr("err\n")
        try s.Print("done\n")
        try s.PrintErr("{k:>3}\n", (k: 7))
        Assert(raw.Str(p.stdout.buf, p.stdout.len) == "out 1\ndone\n")
        Assert(raw.Str(p.stderr.buf, p.stderr.len) == "err\n  7\n")
    }
}

// The failing half of FlushStdCode needs a runtime handle whose write
// fails, which only a process can give (a stdout opened read-only), so
// only the working half is tested here.
test "FlushStdCode keeps the status when the last flush works" (sys: &System) {
    none_env := [EnvVar(name: "", value: "")]
    var ob: [16]u8 = [0; 16]
    var eb: [16]u8 = [0; 16]
    var ib: [8]u8 = [0; 8]
    var p := Process(stdout: FmtStream(mut ob[..]), stderr: FmtStream(mut eb[..]), stdin: FmtStream(mut ib[..]), env: none_env[0..0])
    unsafe "p outlives every use of its address here" {
        Assert(FlushStdCode(raw.OfMut(mut p), sys.io, 0) == 0)
    }
    var q := Process(stdout: FmtStream(mut ob[..]), stderr: FmtStream(mut eb[..]), stdin: FmtStream(mut ib[..]), env: none_env[0..0])
    unsafe "q outlives every use of its address here" {
        Assert(FlushStdCode(raw.OfMut(mut q), sys.io, 3) == 3)
    }
}

// FmtRow has no Format and no derive.Format: it prints by field paths
// alone, each path ending at a field whose type has one.
struct FmtRow {
    export name: str
    export qty: int
    export price: f64
}

// FmtShelf holds a FmtRow, so a path reaches its fields two deep.
struct FmtShelf {
    export row: FmtRow
    export at: int
}

test "Print of field paths through a struct with no Format" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    r := FmtRow(name: "bolt", qty: 12, price: 0.25)
    Assert(try ar.Fmt("{r.name:<12}{r.qty:>6}{r.price:>10.2}|", (r)) == "bolt            12      0.25|")
    s := FmtShelf(row: r, at: 3)
    Assert(try ar.Fmt("{s.at}:{s.row.name}x{s.row.qty}", (s)) == "3:boltx12")
    q := &r
    Assert(try ar.Fmt("{q.name} {k}", (q, k: 1)) == "bolt 1")
    var t := Text.New(sys.arena)
    var w := t.Writer()
    try Print(mut w, "{r.name}={r.qty};", (r))
    var c := PrintCon(w: mut w)
    try c.Print("{r.price:.1};", (r))
    try PrintVia(mut c, "{s.row.name:>5}", (s))
    Assert(t.Str() == "bolt=12;0.2; bolt")
    var ob: [64]u8 = [0; 64]
    var eb: [64]u8 = [0; 64]
    var ib: [8]u8 = [0; 8]
    none_env := [EnvVar(name: "", value: "")]
    var p := Process(stdout: FmtStream(mut ob[..]), stderr: FmtStream(mut eb[..]), stdin: FmtStream(mut ib[..]), env: none_env[0..0])
    unsafe "p outlives y, which only this block uses" {
        y := NewSystem(sys.args, sys.io, sys.arena, sys.heap, raw.OfMut(mut p))
        try y.Print("{r.name:<6}{r.qty:>4}{r.price:>6.2}\n", (r))
        try y.PrintErr("{r.qty:03}\n", (r))
        Assert(raw.Str(p.stdout.buf, p.stdout.len) == "bolt    12  0.25\n")
        Assert(raw.Str(p.stderr.buf, p.stderr.len) == "012\n")
    }
}
