// Why: the CSV reader's limits, sticky errors and resumption are easiest
// to pin with exact inputs on a simulated disk and network.
// What: test blocks for Records, Record, WriteRecord and the cancel path.
// How: reads files of a simulated disk (std.sim) and compares digests of
// the records; the writer's bytes are compared exactly.

// Test blocks for std.csv, run through mk test --std=std.csv at dev and
// release: the reader over a file of a simulated disk (std.sim), its
// limits and its sticky errors, its resumption after a ReadError, and the
// writer's exact bytes. The differential tests against encoding/csv are
// the Go tests std/std-csv_*_test.go.

import std.core
import std.fs
import std.net
import std.raw
import std.sim

// Digest reads every record of text through a buf of size bytes and
// returns them as `field|field;` per record, then `.` at the end, `Q l c`
// or `L l` after an error, and `R` after a ReadError. A Sim's disk holds
// the text, and the read after record fault (from 1) fails; 0 is none.
// A field that is not UTF-8 is `!`.
Digest(a: &Arena, text: str, size: int, o: &Options, fault: int): str from a {
    return DigestBytes(a, text.AsBytes(), size, o, fault)
}

// DigestBytes is Digest of bytes that need not be UTF-8.
DigestBytes(a: &Arena, text: []u8, size: int, o: &Options, fault: int): str from a {
    var s := sim.Sim.New(a, 7).Must()
    defer s.Free()
    io := s.Io()
    var t := Text.New(a)
    // The arena and the simulated disk hold a test's few KiB: Must cannot
    // fail here.
    fs.WriteAll(io, "in", text).Must()
    var f := fs.Open(io, "in").Must()
    rb := a.Slice[u8](16).Must()
    buf := a.Slice[u8](size).Must()
    var r := f.Reader(io, rb)
    var rs := Records.New(mut r, buf, o)
    var seen := 0
    for {
        match rs.Next() {
            ok(some(rec)) {
                for i in 0..rec.Len() {
                    if i > 0 {
                        t.Push("|").Must()
                    }
                    t.Push(rec.Field(i) else { "!" }).Must()
                }
                t.Push(";").Must()
                seen += 1
                if seen == fault {
                    s.FailNext(sim.Op.Read, sim.Fault.Denied)
                }
            }
            ok(none) {
                t.Push(".").Must()
                break
            }
            err(Quote(line, column)) {
                t.Push(a.Fmt("Q {line} {column}", (line, column)).Must()).Must()
                Assert(Sticky(mut rs))
                break
            }
            err(TooLarge(line)) {
                t.Push(a.Fmt("L {line}", (line)).Must()).Must()
                Assert(Sticky(mut rs))
                break
            }
            err(_) {
                t.Push("R").Must()
            }
        }
    }
    f.Close(io) else {
        f.Abandon()
    }
    return a.Dup(t.Str()).Must()
}

// Sticky reports whether the next two calls of Next fail as the last did:
// Quote and TooLarge stay.
Sticky(rs: mut Records): bool {
    var first := ""
    for k in 0..3 {
        what := match rs.Next() {
            ok(_) { "record" }
            err(Quote(line, column)) {
                if line < 0 || column < 0 { "bad" } else { "quote" }
            }
            err(TooLarge(line: _)) { "large" }
            err(_) { "read" }
        }
        if k == 0 {
            first = what
        } else if what != first || what == "record" || what == "read" {
            return false
        }
    }
    return true
}

// Reads is Digest with the default options, a 4 KiB buffer and no fault.
Reads(a: &Arena, text: str): str from a {
    return Digest(a, text, 4096, &DefaultOptions, 0)
}

test "Records splits RFC 4180 records, quoted fields and escapes" (sys: &System) {
    a := sys.arena
    Assert(Reads(a, "a,b,c\r\n1,2,3\r\n") == "a|b|c;1|2|3;.")
    Assert(Reads(a, "a,b\nc,d") == "a|b;c|d;.")
    Assert(Reads(a, "\"a,b\",\"x\"\"y\",\"\"\n") == "a,b|x\"y|;.")
    Assert(Reads(a, "\"line\r\nbreak\",z\n") == "line\r\nbreak|z;.")
    Assert(Reads(a, "\"lf\nonly\",\"cr\rx\"\n") == "lf\nonly|cr\rx;.")
    Assert(Reads(a, "a,\n,\n") == "a|;|;.")
    Assert(Reads(a, "a\rb,c\r") == "a\rb|c;.")
}

test "Records skips blank lines and drops a CR before the end" (sys: &System) {
    a := sys.arena
    Assert(Reads(a, "\n\r\na\r\n\r\n\nb") == "a;b;.")
    Assert(Reads(a, "") == ".")
    Assert(Reads(a, "\r") == ".")
    Assert(Reads(a, "\"q\"\r") == "q;.")
    Assert(Reads(a, " \n") == " ;.")
}

test "Records refuses a stray quote at encoding/csv's place, and keeps refusing" (sys: &System) {
    a := sys.arena
    Assert(Reads(a, "ab\"c\n") == "Q 1 3")
    Assert(Reads(a, "x\n\"a\"b\n") == "x;Q 2 3")
    Assert(Reads(a, "\"abc") == "Q 1 5")
    Assert(Reads(a, "\"abc\n") == "Q 1 6")
    Assert(Reads(a, "\"a\nbc") == "Q 2 3")
}

test "Records fails with TooLarge past each limit and the buffer" (sys: &System) {
    a := sys.arena
    var o := DefaultOptions
    o.field_bytes = 3
    Assert(Digest(a, "abc,\"d\"\"e\"\nabcd\n", 4096, &o, 0) == "abc|d\"e;L 2")
    Assert(Digest(a, "\"abcd\"\n", 4096, &o, 0) == "L 1")
    var p := DefaultOptions
    p.record_fields = 2
    Assert(Digest(a, "a,b\na,b,c\n", 4096, &p, 0) == "a|b;L 2")
    Assert(Digest(a, "a,\n", 4096, &p, 0) == "a|;.")
    Assert(Digest(a, "a,b,\n", 4096, &p, 0) == "L 1")
    // 64 bytes: two field slots of the index, 48 bytes of data.
    Assert(Digest(a, "a,b\nc\n", 64, &DefaultOptions, 0) == "a|b;c;.")
    Assert(Digest(a, "a,b,c\n", 64, &DefaultOptions, 0) == "L 1")
    Assert(Digest(a, "x\n0123456789012345678901234567890123456789012345678\n", 64, &DefaultOptions, 0) == "x;L 2")
}

test "Records refills mid-record without changing a record" (sys: &System) {
    a := sys.arena
    one := "alpha,\"be\"\"ta\",gamma\r\n\"multi\r\nline\",x\r\nlast,\"\"\r\n"
    var text := Text.New(a)
    var want := Text.New(a)
    for _ in 0..4 {
        text.Push(one).Must()
        want.Push("alpha|be\"ta|gamma;multi\r\nline|x;last|;").Must()
    }
    want.Push(".").Must()
    Assert(Reads(a, text.Str()) == want.Str())
    // From 96 bytes: three index slots, 72 bytes of data, so every record
    // fits and most cross a refill.
    var size := 96
    for size <= 160 {
        Assert(Digest(a, text.Str(), size, &DefaultOptions, 0) == want.Str())
        size += 7
    }
}

test "Next resumes where it was after a ReadError (repeatable)" (sys: &System) {
    a := sys.arena
    var text := Text.New(a)
    for i in 0..40 {
        text.Push(a.Fmt("{i},\"q\"\"{i}\",tail\r\n\"multi\r\nline {i}\",x\r\n", (i)).Must()).Must()
    }
    plain := Digest(a, text.Str(), 96, &DefaultOptions, 0)
    // A read fails after each record in turn, so the fault lands in every
    // state a refill happens in: before a record, in an unquoted field, in
    // a quoted one and after a closing quote.
    var faulted := 0
    for k in 1..81 {
        got := Digest(a, text.Str(), 96, &DefaultOptions, k)
        if got.Find("R").IsSome() {
            faulted += 1
        }
        var sb := Text.New(a)
        for p in got.Split("R") {
            sb.Push(p).Must()
        }
        Assert(sb.Str() == plain)
    }
    Assert(faulted > 40)
}

test "Record.Field gives UTF-8 fields whole" (sys: &System) {
    a := sys.arena
    Assert(Reads(a, "é,\"€\"\n") == "é|€;.")
}

// Next checks a record once and Field slices it; a record that
// is not UTF-8 as a whole falls back to each field's own check.
test "Record.Field is none exactly for a field that is not UTF-8 alone" (sys: &System) {
    a := sys.arena
    // One bad field: the others still read.
    bad: [8]u8 = [0x6F, 0x6B, 0x2C, 0xFF, 0x2C, 0xC3, 0xA9, 0x0A]
    Assert(DigestBytes(a, bad[..], 4096, &DefaultOptions, 0) == "ok|!|é;.")
    // The unquoting of `"é""ü"` leaves ü's last byte behind the field, so
    // the record is not UTF-8 as a whole, and the field still reads.
    Assert(Reads(a, "\"é\"\"ü\",x\n") == "é\"ü|x;.")
    Assert(Reads(a, "a,,\"\",é\n") == "a|||é;.")
    // A delimiter above 0x7F can end a field inside a scalar of a record
    // that is UTF-8 as a whole: C3 A9 is é, split by a delimiter A9 into
    // C3 and an empty field, and by a delimiter C3 into a and A9 62.
    var o := DefaultOptions
    o.delimiter = 0xA9
    e: [3]u8 = [0xC3, 0xA9, 0x0A]
    Assert(DigestBytes(a, e[..], 4096, &o, 0) == "!|;.")
    o.delimiter = 0xC3
    aeb: [5]u8 = [0x61, 0xC3, 0xA9, 0x62, 0x0A]
    Assert(DigestBytes(a, aeb[..], 4096, &o, 0) == "a|!;.")
}

// Written is what WriteRecord writes for values with options o.
Written(a: &Arena, values: []str, o: &Options): str from a {
    var t := Text.New(a)
    var w := t.Writer()
    // A Text writer fails only when its arena does, which a test's few
    // bytes never make it.
    WriteRecord(mut w, values, o).Must()
    return a.Dup(t.Str()).Must()
}

test "WriteRecord quotes only where RFC 4180 needs it" (sys: &System) {
    a := sys.arena
    Assert(Written(a, ["a", "b c", " lead", "\\."][..], &DefaultOptions) == "a,b c, lead,\\.\r\n")
    Assert(Written(a, ["a,b", "say \"hi\"", "x\ny", "cr\r"][..], &DefaultOptions) == "\"a,b\",\"say \"\"hi\"\"\",\"x\ny\",\"cr\r\"\r\n")
    Assert(Written(a, [""][..], &DefaultOptions) == "\"\"\r\n")
    Assert(Written(a, ["", ""][..], &DefaultOptions) == ",\r\n")
    Assert(Written(a, ["\""][..], &DefaultOptions) == "\"\"\"\"\r\n")
    var o := DefaultOptions
    o.delimiter = 0x3B
    Assert(Written(a, ["a,b", "c;d"][..], &o) == "a,b;\"c;d\"\r\n")
}

test "csv-1: field_bytes of the largest int reads fields that end the bytes read" (sys: &System) {
    a := sys.arena
    var o := DefaultOptions
    o.field_bytes = 9223372036854775807
    Assert(Digest(a, "abc", 4096, &o, 0) == "abc;.")
    Assert(Digest(a, "a,b\nc,d", 96, &o, 0) == "a|b;c|d;.")
    Assert(Digest(a, "\"q\"\"x\",y\r\nlast", 96, &o, 0) == "q\"x|y;last;.")
    // The 16-byte file reader cuts a long bare field across refills.
    var body := Text.New(a)
    for _ in 0..30 {
        body.Push("0123456789").Must()
    }
    Assert(Digest(a, a.Fmt("{b},z", (b: body.Str())).Must(), 4096, &o, 0) == a.Fmt("{b}|z;.", (b: body.Str())).Must())
}

test "csv-3: a CR LF inside a quoted field reads back as WriteRecord wrote it" (sys: &System) {
    a := sys.arena
    Assert(Reads(a, Written(a, ["a\r\nb"][..], &DefaultOptions)) == "a\r\nb;.")
    Assert(Reads(a, Written(a, ["x", "\r\n", "\"\r\n\"", "\r", "\n\r"][..], &DefaultOptions)) == "x|\r\n|\"\r\n\"|\r|\n\r;.")
    // Across refills too: a 96-byte buffer and the 16-byte file reader.
    var t := Text.New(a)
    for _ in 0..12 {
        t.Push("ab\r\n").Must()
    }
    once := Written(a, [t.Str(), "z"][..], &DefaultOptions)
    var want := Text.New(a)
    want.Push(t.Str()).Must()
    want.Push("|z;.").Must()
    Assert(Digest(a, once, 96, &DefaultOptions, 0) == want.Str())
}

test "csv-12: a last record with no line break may fill the data area" (sys: &System) {
    a := sys.arena
    // 64 bytes: two index slots, 48 bytes of data.
    var x46 := Text.New(a)
    for _ in 0..46 {
        x46.Push("x").Must()
    }
    var x47 := Text.New(a)
    x47.Push(x46.Str()).Must()
    x47.Push("x").Must()
    var x48 := Text.New(a)
    x48.Push(x47.Str()).Must()
    x48.Push("x").Must()
    var want := Text.New(a)
    want.Push(x48.Str()).Must()
    want.Push(";.").Must()
    Assert(Digest(a, x48.Str(), 64, &DefaultOptions, 0) == want.Str())
    // A record with its line break holds at most 48 bytes.
    Assert(Digest(a, a.Fmt("{x}\n", (x: x47.Str())).Must(), 64, &DefaultOptions, 0) == a.Fmt("{x};.", (x: x47.Str())).Must())
    Assert(Digest(a, a.Fmt("{x}\n", (x: x48.Str())).Must(), 64, &DefaultOptions, 0) == "L 1")
    Assert(Digest(a, a.Fmt("{x}y", (x: x48.Str())).Must(), 64, &DefaultOptions, 0) == "L 1")
    // A final CR at the 48th byte is dropped; a quoted field that fills
    // the area and the input ends inside is Quote, as encoding/csv says.
    Assert(Digest(a, a.Fmt("{x}\r", (x: x47.Str())).Must(), 64, &DefaultOptions, 0) == a.Fmt("{x};.", (x: x47.Str())).Must())
    Assert(Digest(a, a.Fmt("\"{x}", (x: x47.Str())).Must(), 64, &DefaultOptions, 0) == "Q 1 49")
    Assert(Digest(a, a.Fmt("\"{x}\"", (x: x46.Str())).Must(), 64, &DefaultOptions, 0) == a.Fmt("{x};.", (x: x46.Str())).Must())
}

// The test that Records.Next is repeatable across a cancel:
// Next reads, through a Reader with no inline buffer (so every Read is one
// fill), a stream of the simulated network that a writer task sends in
// 23-byte pieces 40 us apart, so the reader's fills wait for bytes in
// flight. In run k the k-th fill is called in a cancelled scope (the
// interruption: at a fill that has to wait, the Sim answers Canceled,
// which the Reader reports as ReadError), and Next is then called again
// outside it. What every run delivers equals the uninterrupted run's.
// Scope.CancelAfter arms no timer yet (std/task/scope.mk, CancelAfter), so
// a deadline cannot be tested here; a deadline cancels as Cancel does,
// so this is the path a fired deadline takes.
const CsvCancelText: str = "0,\"q\"\"0\",tail\r\n\"multi\r\nline 0\",x\r\n1,\"q\"\"1\",tail\r\n\"multi\r\nline 1\",x\r\n2,\"q\"\"2\",tail\r\n\"multi\r\nline 2\",x\r\n3,\"q\"\"3\",tail\r\n\"multi\r\nline 3\",x\r\n4,\"q\"\"4\",tail\r\n\"multi\r\nline 4\",x\r\n5,\"q\"\"5\",tail\r\n\"multi\r\nline 5\",x\r\n6,\"q\"\"6\",tail\r\n\"multi\r\nline 6\",x\r\n7,\"q\"\"7\",tail\r\n\"multi\r\nline 7\",x\r\n8,\"q\"\"8\",tail\r\n\"multi\r\nline 8\",x\r\n9,\"q\"\"9\",tail\r\n\"multi\r\nline 9\",x\r\n10,\"q\"\"10\",tail\r\n\"multi\r\nline 10\",x\r\n11,\"q\"\"11\",tail\r\n\"multi\r\nline 11\",x\r\n12,\"q\"\"12\",tail\r\n\"multi\r\nline 12\",x\r\n13,\"q\"\"13\",tail\r\n\"multi\r\nline 13\",x\r\n"

// CsvSrc is the fill state: the stream, the fills so far, the fill to
// interrupt (0: none) and the fills that were interrupted.
view struct CsvSrc {
    c: &net.Stream
    n: int
    k: int
    hits: int
}

const CsvCancelPort: core.ReadPortMut[CsvSrc] = core.ReadPortMut[CsvSrc].New(CsvCancelFill)

// CsvCancelFill is one read of the stream; the k-th runs in a cancelled
// scope.
CsvCancelFill(s: mut CsvSrc, io: Io, buf: mut []u8): ReadError!int {
    s.n += 1
    if s.n != s.k {
        return s.c.Read(io, buf) else {
            fail ReadError
        }
    }
    var got := -1
    scope sc := io.Scope() {
        sc.Cancel()
        got = s.c.Read(io, buf) else {
            -1
        }
    }
    if got < 0 {
        s.hits += 1
        fail ReadError
    }
    return got
}

// CsvHash folds the record into h: two polynomial hashes, mod 1e9 + 7 and
// 998244353, of its fields and separators, in the two halves of a u64.
CsvHash(h: u64, rec: &Record): u64 {
    var a := h >> 32
    var b := h & 0xFFFF_FFFF
    for i in 0..rec.Len() {
        f := rec.Field(i) else {
            "!"
        }
        for c in f.AsBytes() {
            a = (a * 257 + u64(c) + 1) % 1_000_000_007
            b = (b * 263 + u64(c) + 1) % 998_244_353
        }
        a = (a * 257 + 300) % 1_000_000_007
        b = (b * 263 + 300) % 998_244_353
    }
    a = (a * 257 + 301) % 1_000_000_007
    b = (b * 263 + 301) % 998_244_353
    return (a << 32) | b
}

// CsvReadAll digests every record of src's stream; 0 after a Quote or
// TooLarge, a ReadError that does not end when Next is called again, or
// another count of records than CsvCancelText's 28.
CsvReadAll(io: Io, src: mut CsvSrc): u64 {
    var none_buf: [1]u8 = [0]
    port := CsvCancelPort
    var r := port.Reader(io, src, mut none_buf[0..0])
    var buf: [96]u8 = [0; 96]
    var rs := Records.New(mut r, mut buf[..], &DefaultOptions)
    var h: u64 = 1
    var errs := 0
    var n := 0
    for {
        match rs.Next() {
            ok(some(rec)) {
                h = CsvHash(h, &rec)
                errs = 0
                n += 1
            }
            ok(none) {
                return if n == 28 { h } else { 0 }
            }
            err(Quote(line: _, column: _)) {
                return 0
            }
            err(TooLarge(line: _)) {
                return 0
            }
            err(_) {
                errs += 1
                if errs > 1 {
                    return 0
                }
            }
        }
    }
}

// CsvCancelReader serves one connection: it reads it as CsvReadAll does,
// interrupting fill k, and stores the digest, the fills and the
// interrupted fills.
CsvCancelReader(io: Io, k: int, out: &raw.Atomic[u64], fills: &raw.Atomic[u64], hits: &raw.Atomic[u64]) {
    l := net.Listen(io, "127.0.0.1:7300") else {
        return
    }
    c := l.Accept(io) else {
        l.Close()
        return
    }
    var src := CsvSrc(c: &c, n: 0, k: k, hits: 0)
    out.Store(CsvReadAll(io, mut src))
    fills.Store(Cast[u64](src.n))
    hits.Store(Cast[u64](src.hits))
    c.Close()
    l.Close()
}

// CsvCancelWriter dials the reader (yielding until it listens) and sends
// CsvCancelText in 23-byte pieces 40 us apart, then closes.
CsvCancelWriter(io: Io) {
    for _ in 0..1000 {
        c := net.Dial(io, "127.0.0.1:7300") else {
            io.Yield() else {
                return
            }
            continue
        }
        b := CsvCancelText.AsBytes()
        var at := 0
        for at < b.Len() {
            e := if at + 23 < b.Len() { at + 23 } else { b.Len() }
            c.WriteAll(io, b[at..e]) else {
            }
            at = e
            io.Sleep(core.DurationOf(40_000)) else {
            }
        }
        c.Close()
        return
    }
}

// CsvCancelRun is one run under a fresh Sim of the seed: (digest, fills,
// interrupted).
CsvCancelRun(a: &Arena, seed: u64, k: int): (h: u64, fills: int, hits: int) {
    var s := sim.Sim.New(a, seed).Must()
    io := s.Io()
    out := raw.Atomic[u64].New(0)
    fills := raw.Atomic[u64].New(0)
    hits := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        sc.go CsvCancelReader(io, k, &out, &fills, &hits)
        sc.go CsvCancelWriter(io)
    }
    s.Free()
    return (h: out.Load(), fills: Cast[int](fills.Load()), hits: Cast[int](hits.Load()))
}

test "Next is repeatable: a cancel at each await point, then Next again" (sys: &System) {
    a := sys.arena
    // Five seeds: five schedules of the pieces' arrivals, so the fills
    // end at other places in the records.
    var fills := 0
    var interrupted := 0
    var first: u64 = 0
    for seed in 0..5 {
        plain := CsvCancelRun(a, Cast[u64](seed), 0)
        Assert(plain.h != 0 && plain.hits == 0)
        if seed == 0 {
            first = plain.h
        }
        Assert(plain.h == first)
        // Every fill in turn, and one past the last.
        for k in 1..plain.fills + 2 {
            got := CsvCancelRun(a, Cast[u64](seed), k)
            Assert(got.h == plain.h)
            interrupted += got.hits
        }
        fills += plain.fills
    }
    // Most fills wait for bytes in flight, so most are interrupted.
    Assert(interrupted * 2 > fills)
}

test "a buffer of 32 bytes per field and four thirds of a record's bytes holds the record; less is TooLarge" (sys: &System) {
    a := sys.arena
    // a,b,c: 3 fields, 6 bytes. 80 bytes give 2 index slots, 96 give 3.
    Assert(Digest(a, "a,b,c\n", 80, &DefaultOptions, 0) == "L 1")
    Assert(Digest(a, "a,b,c\n", 96, &DefaultOptions, 0) == "a|b|c;.")
    // 2 fields of 34 bytes in 64: 2 slots, 48 bytes of data.
    Assert(Digest(a, "0123456789abcdef,0123456789abcdef\n", 64, &DefaultOptions, 0) == "0123456789abcdef|0123456789abcdef;.")
    // The rule's bound: 32 * 2 = 64 and 4 * 48 / 3 = 64, so a record of 2
    // fields and 48 bytes, its line break included, fits in 64 bytes; one
    // of 50 does not (48 bytes of data).
    Assert(Digest(a, "0123456789012345678901,012345678901234567890123\n", 64, &DefaultOptions, 0) == "0123456789012345678901|012345678901234567890123;.")
    Assert(Digest(a, "0123456789012345678901,01234567890123456789012345\n", 64, &DefaultOptions, 0) == "L 1")
}
