// Why: a log line's exact bytes and a sink's cut and failure paths are
// contract, and only in-module blocks reach Stamp and Finish.
// What: test blocks for std.log, run through mk test --std=std.log at dev
// and release: Stamp's exact bytes, Finish's cut and mark at each
// boundary, and the sinks' failure on an Io with no process and on a field
// whose Format fails.
// How: the format is Print's, so a format that does not match its record
// is check's E0706 at the Logger call; the Go tests compile such calls and
// judge the code, and run the sinks from many tasks.

import std.core
import std.fs

// StampIs is whether Stamp writes exactly want for nanos.
StampIs(sys: &System, nanos: i64, want: str): bool {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    // a Text writer over the arena fails only when the arena does, which a
    // nine-byte stamp in a test arena never makes it
    Stamp(mut w, nanos).Must()
    return t.Str() == want
}

test "Stamp writes seconds and six digits of microseconds" (sys: &System) {
    Assert(StampIs(sys, 1_234_567_891, "1.234567 "))
    Assert(StampIs(sys, 3_000_000_000, "3.000000 "))
    Assert(StampIs(sys, 0, "0.000000 "))
    Assert(StampIs(sys, 999, "0.000000 "))
    Assert(StampIs(sys, 1000, "0.000001 "))
    Assert(StampIs(sys, 999_999_999, "0.999999 "))
    Assert(StampIs(sys, 1_000_000_000, "1.000000 "))
    Assert(StampIs(sys, 86_400_000_000_123, "86400.000000 "))
    Assert(StampIs(sys, 9_223_372_036_854_775_807, "9223372036.854775 "))
}

test "Stamp writes a negative difference as zero" (sys: &System) {
    Assert(StampIs(sys, -1, "0.000000 "))
    Assert(StampIs(sys, -9_223_372_036_854_775_807 - 1, "0.000000 "))
}

test "Logger drops a call below its level and writes the rest with the level first" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    var l := Logger.New(mut w, Level.Warn)
    l.Debug("hidden {n}", (n: 1)).Must()
    l.Info("hidden too\n").Must()
    l.Warn("disk at {pct}%", (pct: 91)).Must()
    l.Log(Level.Info, "dropped").Must()
    l.Error("gone").Must()
    Assert(t.Str() == "WARN disk at 91%\nERROR gone\n")
}

// FinishOf is the record Finish makes from text, formatted as the Capped
// writer of Sink.Log keeps it: its first RecordMax - 1 bytes kept, the rest
// counted as dropped.
FinishOf(a: &Arena, text: str): str from a {
    var buf: [RecordMax]u8 = [0; RecordMax]
    b := text.AsBytes()
    kept := if b.Len() < RecordMax - 1 { b.Len() } else { RecordMax - 1 }
    (mut buf[..kept]).CopyFrom(b[..kept])
    n := Finish(mut buf[..], kept, b.Len() - kept)
    s := str.From(buf[..n]) else {
        return "not UTF-8"
    }
    return a.Dup(s) else { "" }
}

// Repeat is s repeated n times, in a.
Repeat(a: &Arena, s: str, n: int): str from a {
    var t := Text.New(a)
    for _ in 0..n {
        t.Push(s) else {
            return ""
        }
    }
    return t.Done()
}

test "Finish ends a record that fits with a newline, and one exactly RecordMax long is not marked" (sys: &System) {
    Assert(FinishOf(sys.arena, "") == "\n")
    Assert(FinishOf(sys.arena, "INFO x") == "INFO x\n")
    r := FinishOf(sys.arena, Repeat(sys.arena, "a", RecordMax - 1))
    Assert(r.Len() == RecordMax)
    Assert(r.EndsWith("aaa\n"))
}

test "Finish cuts a record one byte over and marks the bytes it did not write" (sys: &System) {
    r := FinishOf(sys.arena, Repeat(sys.arena, "a", RecordMax))
    // the total, 4096, has four digits: 4096 - 6 - 4 - 8 = 4078 bytes kept
    Assert(r.Len() == 4078 + " [cut 18 bytes]\n".Len())
    Assert(r.EndsWith("a [cut 18 bytes]\n"))
}

test "Finish cuts on a UTF-8 boundary" (sys: &System) {
    // two-byte scalars after one byte start at odd offsets; 4078 is even,
    // so the cut backs one byte to 4077, and the mark counts 6001 - 4077
    t2 := FinishOf(sys.arena, sys.arena.Fmt("a{s}", (s: Repeat(sys.arena, "é", 3000))) else { "" })
    Assert(t2.Len() == 4077 + " [cut 1924 bytes]\n".Len())
    Assert(t2.EndsWith("é [cut 1924 bytes]\n"))
    // four-byte scalars after one byte start at 1 + 4k: 4077 is the last
    // start at or before 4078
    t4 := FinishOf(sys.arena, sys.arena.Fmt("a{s}", (s: Repeat(sys.arena, "😀", 2000))) else { "" })
    Assert(t4.Len() == 4077 + " [cut 3924 bytes]\n".Len())
    Assert(t4.EndsWith("😀 [cut 3924 bytes]\n"))
}

test "Finish backs over at most three continuation bytes of text that is not UTF-8" (sys: &System) {
    var buf: [RecordMax]u8 = [0x80; RecordMax]
    n := Finish(mut buf[..], RecordMax - 1, 10)
    // total 4105: keep 4078, back three to 4075, mark 4105 - 4075 = 30
    Assert(n == 4075 + " [cut 30 bytes]\n".Len())
    Assert(buf[4075] == 0x20)
    Assert(buf[n - 1] == 0x0A)
}

test "Finish saturates the count of a record far past RecordMax" (sys: &System) {
    var buf: [RecordMax]u8 = [0x61; RecordMax]
    n := Finish(mut buf[..], RecordMax - 1, IntMax)
    Assert(n <= RecordMax)
    s := str.From(buf[..n]) else {
        Assert(false)
        return
    }
    // the total saturates at IntMax, 19 digits: keep 4096 - 33 = 4063
    Assert(s.EndsWith(" [cut 9223372036854771744 bytes]\n"))
}

test "Digits and PutInt write every count the mark can hold" (sys: &System) {
    Assert(Digits(0) == 1)
    Assert(Digits(9) == 1)
    Assert(Digits(10) == 2)
    Assert(Digits(IntMax) == 19)
    var buf: [24]u8 = [0; 24]
    Assert(PutInt(mut buf[..], 2, IntMax) == 21)
    Assert(str.From(buf[2..21]).Must() == "9223372036854775807")
    Assert(PutInt(mut buf[..], 0, 0) == 1)
    Assert(buf[0] == 0x30)
}

test "A sink on an Io that reaches no process or file fails with WriteError" (sys: &System) {
    var t := Text.New(sys.arena)
    w := t.Writer()
    inert := core.WriterIo(&w)
    var failed := 0
    Stderr(Level.Debug).Info(inert, "lost") else {
        failed += 1
    }
    Stdout(Level.Debug).Error(inert, "lost {n}", (n: 1)) else {
        failed += 1
    }
    // a name of its own, since dev and release may run side by side
    var r: [8]u8 = [0; 8]
    sys.io.Random(mut r[..]) else {}
    var id: u64 = 0
    for b in r {
        id = id << 8 | u64(b)
    }
    path := sys.arena.Fmt("std-log-inert-{id:x}.log", (id)) else {
        Assert(false)
        return
    }
    f := fs.Create(sys.io, path) else {
        Assert(false)
        return
    }
    m := Mutex[fs.File].New(f)
    sink := ToFile(&m, Level.Info)
    sink.Warn(inert, "lost") else {
        failed += 1
    }
    sink.Warn(sys.io, "kept") else {
        failed += 10
    }
    Assert(failed == 3)
    var g := m.Into()
    g.Close(sys.io) else {
        g.Abandon()
    }
    text := fs.ReadText(sys.arena, sys.io, path) else {
        Assert(false)
        return
    }
    Assert(text == "WARN kept\n")
    fs.Remove(sys.io, path) else {}
}

test "A sink drops a call below its level and writes nothing for it" (sys: &System) {
    var t := Text.New(sys.arena)
    w := t.Writer()
    inert := core.WriterIo(&w)
    // a dropped call writes nothing, so even an inert Io does not fail it
    sink := Stderr(Level.Error)
    var failed := 0
    sink.Debug(inert, "x") else {
        failed += 1
    }
    sink.Info(inert, "x") else {
        failed += 1
    }
    sink.Warn(inert, "x") else {
        failed += 1
    }
    sink.Log(inert, Level.Warn, "x") else {
        failed += 1
    }
    Assert(failed == 0)
    sink.Error(inert, "x") else {
        failed += 1
    }
    Assert(failed == 1)
}

// FailingField's Format writes part of its text and then fails, as a
// Format over a failing source does.
struct FailingField {
    n: int
}

impl Format for FailingField {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("partial")
        fail WriteError
    }
}

test "A field whose Format fails fails the call and writes nothing of the record" (sys: &System) {
    // a name of its own, since dev and release may run side by side
    var r: [8]u8 = [0; 8]
    sys.io.Random(mut r[..]) else {}
    var id: u64 = 0
    for b in r {
        id = id << 8 | u64(b)
    }
    path := sys.arena.Fmt("std-log-format-{id:x}.log", (id)) else {
        Assert(false)
        return
    }
    f := fs.Create(sys.io, path) else {
        Assert(false)
        return
    }
    m := Mutex[fs.File].New(f)
    sink := ToFile(&m, Level.Info)
    var failed := 0
    sink.Info(sys.io, "a {b} z", (b: FailingField(n: 1))) else {
        failed += 1
    }
    sink.Error(sys.io, "{b}", (b: FailingField(n: 2))) else {
        failed += 1
    }
    sink.Info(sys.io, "kept") else {
        failed += 10
    }
    Assert(failed == 2)
    var g := m.Into()
    g.Close(sys.io) else {
        g.Abandon()
    }
    text := fs.ReadText(sys.arena, sys.io, path) else {
        Assert(false)
        return
    }
    Assert(text == "INFO kept\n")
    fs.Remove(sys.io, path) else {}
}
