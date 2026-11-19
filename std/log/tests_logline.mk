// Why: a Logger gathers a line on the stack and hands it to its Writer in
// runs; the runs, their order and their failures are part of what it promises.
// What: test blocks: a line up to LineMax bytes is one write, a longer one
// arrives whole and in order in runs, a failure stops the line where it
// happened, and a Logger never flushes its Writer.
// How: the Writer is a recording one over a port with no inline buffer, so
// each run the Logger hands over is one counted call of RecWrite.

import std.core

const RecMax = 16384

// Rec is what a Logger handed its Writer: the bytes, the writes and their
// largest, the flushes; the write numbered fail_at (0: none) fails and keeps
// nothing.
struct Rec {
    buf: [RecMax]u8
    n: int
    writes: int
    largest: int
    flushes: int
    fail_at: int
}

const RecPort = core.WritePortMut[Rec].New(RecWrite, RecFlush)

RecWrite(r: mut Rec, io: Io, b: []u8): WriteError!void {
    _ = io
    r.writes += 1
    if r.writes == r.fail_at {
        fail WriteError
    }
    (mut r.buf[r.n..r.n + b.Len()]).CopyFrom(b)
    r.n += b.Len()
    if b.Len() > r.largest {
        r.largest = b.Len()
    }
}

RecFlush(r: mut Rec, io: Io): WriteError!void {
    _ = io
    r.flushes += 1
}

NewRec(fail_at: int): Rec {
    return Rec(buf: [0; RecMax], n: 0, writes: 0, largest: 0, flushes: 0, fail_at: fail_at)
}

// RecText is the bytes r holds, as a str ("not UTF-8" when they are not).
RecText(r: &Rec): str from r {
    return str.From(r.buf[..r.n]) else { "not UTF-8" }
}

// LogInto logs one line at level through a Logger over r (made with
// NewTimed through io when timed), and reports whether the call failed.
LogInto[A: Fields[Format] + Plain](io: Io, r: mut Rec, timed: bool, level: Level, const f: str, args: A): bool {
    var nb: [1]u8 = [0]
    var w := (&RecPort).Writer(io, mut *r, mut nb[0..0])
    var failed := false
    if timed {
        var l := Logger.NewTimed(io, mut w, Level.Debug)
        l.Log(level, f, args) else {
            failed = true
        }
    } else {
        var l := Logger.New(mut w, Level.Debug)
        l.Log(level, f, args) else {
            failed = true
        }
    }
    return failed
}

// Expected is the line an untimed Logger writes, built apart from it: the
// level's word, Print's text for f and args into a Text, and a newline.
Expected[A: Fields[Format] + Plain](a: &Arena, level: Level, const f: str, args: A): str from a {
    var t := Text.New(a)
    var w := t.Writer()
    w.Str(Name(level)) else {}
    Print(mut w, f, args) else {}
    w.Str("\n") else {}
    return t.Done()
}

// Xs is n bytes of x, in a.
Xs(a: &Arena, n: int): str from a {
    var t := Text.New(a)
    for _ in 0..n {
        t.Push("x") else {
            return ""
        }
    }
    return t.Done()
}

test "A Logger hands a line of 255 or 256 bytes to its Writer in one write" (sys: &System) {
    for total in [255, 256] {
        var r := NewRec(0)
        s := Xs(sys.arena, total - 6)
        Assert(!LogInto(sys.io, mut r, false, Level.Info, "{s}", (s)))
        Assert(r.n == total)
        Assert(r.writes == 1)
        Assert(RecText(&r) == Expected(sys.arena, Level.Info, "{s}", (s)))
        Assert(r.flushes == 0)
    }
}

test "A line of 257 or 5000 bytes arrives whole and in order, in runs of the buffer and a rest" (sys: &System) {
    // one field: 257 is the buffer full (256) and the newline; 5000 is
    // the buffer topped up and handed on, the rest of the field straight
    // through, then the newline
    for total in [257, 5000] {
        var r := NewRec(0)
        s := Xs(sys.arena, total - 6)
        Assert(!LogInto(sys.io, mut r, false, Level.Warn, "{s}", (s)))
        Assert(r.n == total)
        Assert(RecText(&r) == Expected(sys.arena, Level.Warn, "{s}", (s)))
        runs := if total == 257 { 2 } else { 3 }
        Assert(r.writes == runs)
        Assert(r.flushes == 0)
    }
    // many small pieces: every run is the full buffer but the last
    var xs: [1000]int = [0; 1000]
    for k in 0..1000 {
        xs[k] = 1000 + k
    }
    var r := NewRec(0)
    Assert(!LogInto(sys.io, mut r, false, Level.Error, "n={n} v={v} end", (n: 1000, v: xs[..])))
    want := Expected(sys.arena, Level.Error, "n={n} v={v} end", (n: 1000, v: xs[..]))
    Assert(want.Len() > 5000)
    Assert(RecText(&r) == want)
    Assert(r.writes == (want.Len() + LineMax - 1) / LineMax)
    Assert(r.largest == LineMax)
}

// FailingAt's Format writes n bytes of y and then fails, as a Format over
// a failing source does.
struct FailingAt {
    n: int
}

impl Format for FailingAt {
    Format(&self, w: mut Writer): WriteError!void {
        for _ in 0..self.n {
            try w.Str("y")
        }
        fail WriteError
    }
}

test "A line whose Format fails leaves none of a short line, and of a long one only the runs before" (sys: &System) {
    var r := NewRec(0)
    Assert(LogInto(sys.io, mut r, false, Level.Info, "a {b} z", (b: FailingAt(n: 7))))
    Assert(r.n == 0 && r.writes == 0)
    Assert(!LogInto(sys.io, mut r, false, Level.Info, "kept", ()))
    Assert(RecText(&r) == "INFO kept\n")
    var q := NewRec(0)
    // "INFO a " and 600 bytes: two full runs of 256 reach the Writer
    Assert(LogInto(sys.io, mut q, false, Level.Info, "a {b} z", (b: FailingAt(n: 600))))
    Assert(q.n == 512 && q.writes == 2)
    Assert(RecText(&q).StartsWith("INFO a yyyy"))
}

test "A Writer that fails a line's write fails the call, and the next line lands whole" (sys: &System) {
    var r := NewRec(1)
    Assert(LogInto(sys.io, mut r, false, Level.Error, "e {k}", (k: 4)))
    Assert(r.n == 0 && r.writes == 1)
    Assert(!LogInto(sys.io, mut r, false, Level.Error, "e {k}", (k: 5)))
    Assert(RecText(&r) == "ERROR e 5\n")
    // the second run of a 5000-byte line fails: the first stays, the rest
    // of the line is not written
    var q := NewRec(2)
    s := Xs(sys.arena, 4994)
    Assert(LogInto(sys.io, mut q, false, Level.Info, "{s}", (s)))
    Assert(q.n == 256 && q.writes == 2)
    Assert(!LogInto(sys.io, mut q, false, Level.Info, "after", ()))
    Assert(RecText(&q).EndsWith("xxxINFO after\n"))
}

// Flushing's Format writes a, flushes its Writer, and writes b.
struct Flushing {
    n: int
}

impl Format for Flushing {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("a")
        try w.Flush()
        try w.Str("b")
    }
}

test "A Logger never flushes its Writer; a Format's flush hands the line so far on" (sys: &System) {
    var r := NewRec(0)
    Assert(!LogInto(sys.io, mut r, false, Level.Info, "x{f}y", (f: Flushing(n: 0))))
    Assert(RecText(&r) == "INFO xaby\n")
    Assert(r.writes == 2)
    Assert(r.flushes == 0)
}

// KindPair is a record with a Format of its own.
struct KindPair {
    a: int
    b: str
}

impl Format for KindPair {
    Format(&self, w: mut Writer): WriteError!void {
        try Print(w, "({a}, {b})", (a: self.a, b: self.b))
    }
}

test "Every level, field kind and spec writes the line Print writes after the level" (sys: &System) {
    i: i8 = -128
    u: u64 = 18446744073709551615
    x := 3.25
    g: f32 = 0.1
    s := "str é"
    b := true
    c := 'λ'
    o: ?int = some(3)
    no: ?int = none
    sl := [1, 2, 3]
    p := KindPair(a: 7, b: "seven")
    levels := [Level.Debug, Level.Info, Level.Warn, Level.Error]
    for lv in levels {
        var r := NewRec(0)
        Assert(!LogInto(sys.io, mut r, false, lv, "i={i} u={u} x={x} g={g} s={s} b={b} c={c} o={o} no={no} sl={sl} p={p}", (i, u, x, g, s, b, c, o, no, sl: sl[..], p)))
        Assert(RecText(&r) == Expected(sys.arena, lv, "i={i} u={u} x={x} g={g} s={s} b={b} c={c} o={o} no={no} sl={sl} p={p}", (i, u, x, g, s, b, c, o, no, sl: sl[..], p)))
        Assert(r.writes == 1)
        var q := NewRec(0)
        Assert(!LogInto(sys.io, mut q, false, lv, "{u:x} {u:X} {i:b} {i:o} {i:+} {i:06} {x:.3} {x:+08.1} {s:>9} {s:*<9} {p:^16}|{{}}", (u, i, x, s, p)))
        Assert(RecText(&q) == Expected(sys.arena, lv, "{u:x} {u:X} {i:b} {i:o} {i:+} {i:06} {x:.3} {x:+08.1} {s:>9} {s:*<9} {p:^16}|{{}}", (u, i, x, s, p)))
        Assert(q.writes == 1)
    }
    // through the methods, not Log
    var r := NewRec(0)
    LogMethods(sys.io, mut r)
    Assert(RecText(&r) == "DEBUG d 1\nINFO i 2\nWARN w 3\nERROR e 4\nINFO plain\n")
    Assert(r.writes == 5)
}

// LogMethods logs one line through each method of a Logger over r.
LogMethods(io: Io, r: mut Rec) {
    var nb: [1]u8 = [0]
    var w := (&RecPort).Writer(io, mut *r, mut nb[0..0])
    var l := Logger.New(mut w, Level.Debug)
    l.Debug("d {n}", (n: 1)).Must()
    l.Info("i {n}", (n: 2)).Must()
    l.Warn("w {n}", (n: 3)).Must()
    l.Error("e {n}", (n: 4)).Must()
    l.Info("plain").Must()
}

// StampedRest is the rest of a timed line after a well-formed stamp
// (digits, a point, six digits and a space), or "bad stamp".
StampedRest(line: str): str from line {
    b := line.AsBytes()
    var k := 0
    for k < b.Len() && b[k] >= 0x30 && b[k] <= 0x39 {
        k += 1
    }
    if k == 0 || k + 8 > b.Len() || b[k] != 0x2E || b[k + 7] != 0x20 {
        return "bad stamp"
    }
    for j in k + 1..k + 7 {
        if b[j] < 0x30 || b[j] > 0x39 {
            return "bad stamp"
        }
    }
    return str.From(b[k + 8..]) else { "bad stamp" }
}

test "A timed line is one write with its stamp first, and a long one arrives whole" (sys: &System) {
    var r := NewRec(0)
    Assert(!LogInto(sys.io, mut r, true, Level.Warn, "tick {k}", (k: 7)))
    Assert(r.writes == 1)
    Assert(StampedRest(RecText(&r)) == "WARN tick 7\n")
    var q := NewRec(0)
    s := Xs(sys.arena, 5000)
    Assert(!LogInto(sys.io, mut q, true, Level.Info, "{s}", (s)))
    Assert(StampedRest(RecText(&q)) == Expected(sys.arena, Level.Info, "{s}", (s)))
    Assert(q.writes == 3)
}
