// Why: Writer and Reader carry every byte a program reads or writes; their
// buffers must stay in bounds even when a table entry re-enters its Reader.
// What: test blocks for Text.Writer's validation, the process streams' Flush,
// Stdin at end of input, System.Env, Reader.Read, Until and Line over the
// inline buffer, re-entering entries, and Reader.Of.
// How: TestStdIo_CoreTestBlocks (std/std-io_coreblocks_test.go) runs them in
// child processes with the counting heap. A block sees no input and no
// output of its own, so the byte-exact cases stay whole programs judged in
// Go, in std/std-io_rw_test.go. Re-entry probes keep a canary after the buffer
// (TrwReMem). Helper names start with Trw: they share std.core's namespace.

import std.raw

// TrwFails is 1 when w.Bytes(b) is WriteError, else 0.
TrwFails(w: mut Writer, b: []u8): int {
    w.Bytes(b) else {
        return 1
    }
    return 0
}

// TrwFill writes valid and invalid runs through t's Writer and returns the
// number of failed writes.
TrwFill(t: mut Text): WriteError!int {
    var w := t.Writer()
    try w.Str("h\u{E9}llo")
    try w.Bytes(" w\u{F6}rld".AsBytes())
    var f := 0
    var b1: [2]u8 = [0xC3, 0x28]             // a lead byte, then no continuation
    var b2: [1]u8 = [0x80]                   // a lone continuation byte
    var b3: [2]u8 = [0xE2, 0x82]             // a truncated three-byte sequence
    var b4: [2]u8 = [0xC0, 0xAF]             // an overlong '/'
    var b5: [3]u8 = [0xED, 0xA0, 0x80]       // a surrogate
    var b6: [4]u8 = [0xF4, 0x90, 0x80, 0x80] // above U+10FFFF
    f += TrwFails(mut w, b1[..])
    f += TrwFails(mut w, b2[..])
    f += TrwFails(mut w, b3[..])
    f += TrwFails(mut w, b4[..])
    f += TrwFails(mut w, b5[..])
    f += TrwFails(mut w, b6[..])
    try Print(mut w, " {n} {s}", (n: -42, s: "\u{1F600}"))
    try w.Bytes("".AsBytes())
    try w.Flush()
    return f
}

test "Text.Writer appends valid UTF-8 and refuses every invalid run whole" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    f := try TrwFill(mut t)
    Assert(f == 6)
    // A refused run adds no byte: only the valid writes are there.
    Assert(t.Str() == "h\u{E9}llo w\u{F6}rld -42 \u{1F600}")
}

test "Stdout and Stderr Flush with no error" (sys: &System) {
    var out := sys.Stdout()
    var err := sys.Stderr()
    try out.Flush()
    try err.Flush()
    // Two Writers of one stream share its buffer: a Flush of either works.
    var out2 := sys.Stdout()
    try out2.Flush()
    try out.Flush()
}

// An empty Str literal written as the first write of a function to a
// Writer with no inline buffer (a process stream, a Text) writes nothing;
// in a release build a miscompile once sent it to an unreachable block.
test "an empty write to a process stream or a Text is nothing" (sys: &System) {
    var out := sys.Stdout()
    try out.Str("")
    try out.Bytes("".AsBytes())
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    var w := t.Writer()
    try w.Str("")
    try w.Str("x")
    try w.Str("")
    Assert(t.Str() == "x")
}

test "Stdin at end of input: Read, ReadAll and Line" (sys: &System) {
    // The runner gives a test no input: every read is at its end.
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var r := sys.Stdin()
    var buf: [16]u8 = [0; 16]
    var empty: [0]u8 = []
    Assert(try r.Read(mut empty[..]) == 0)
    Assert(try r.Read(mut buf[..]) == 0)
    Assert(try r.ReadAll(mut buf[..]) == 0)
    l := try r.Line(&ar)
    Assert(l.IsNone())
}

test "Env finds a set name and nothing for an unset or malformed one" (sys: &System) {
    // PATH is set for every process both targets start from a shell or a
    // service; the runner passes its environment on.
    p := sys.Env("PATH")
    Assert(p.IsSome())
    Assert(sys.Env("MK_STDIO_SURELY_UNSET_7d1f").IsNone())
    Assert(sys.Env("").IsNone())
    Assert(sys.Env("PATH=").IsNone())
    Assert(sys.Env("=").IsNone())
}

// TrwSrc gives the bytes 0, 1, 2, ... (each mod 256) up to total, at most
// chunk of them per fill.
struct TrwSrc {
    next: int
    total: int
    chunk: int
}

TrwSrcFill(s: mut TrwSrc, io: Io, dst: mut []u8): ReadError!int {
    var n := s.total - s.next
    if n > s.chunk {
        n = s.chunk
    }
    if n > dst.Len() {
        n = dst.Len()
    }
    for i in 0..n {
        dst[i] = Trunc[u8](s.next + i)
    }
    s.next += n
    return n
}

const TrwSrcPort = ReadPortMut[TrwSrc].New(TrwSrcFill)

// TrwFrom is whether b holds pos, pos + 1, ... (each mod 256).
TrwFrom(b: []u8, pos: int): bool {
    for i in 0..b.Len() {
        if b[i] != Trunc[u8](pos + i) {
            return false
        }
    }
    return true
}

// Reader.Read's served path copies raw: every count and byte at
// its edges, a read that ends exactly at the buffered bytes and at cap, one
// as large as cap (straight from the table), one that wants more than is
// buffered, and the end of input.
test "Reader.Read serves the inline buffer to its edges" (sys: &System) {
    var s := TrwSrc(next: 0, total: 40, chunk: 8)
    var ib: [8]u8 = [0; 8]
    var r := (&TrwSrcPort).Reader(sys.io, mut s, mut ib[..])
    var d: [16]u8 = [0; 16]
    wants: [9]int = [3, 5, 8, 1, 7, 9, 2, 10, 4]
    gots: [9]int = [3, 5, 8, 1, 7, 8, 2, 6, 0]
    var pos := 0
    for j in 0..9 {
        got := try r.Read(mut d[0..wants[j]])
        Assert(got == gots[j])
        Assert(TrwFrom(d[0..got], pos))
        pos += got
    }
    Assert(pos == 40)
    // Fills shorter than the buffer: each Read serves what one fill gave.
    var s2 := TrwSrc(next: 0, total: 13, chunk: 5)
    var ib2: [8]u8 = [0; 8]
    var r2 := (&TrwSrcPort).Reader(sys.io, mut s2, mut ib2[..])
    wants2: [5]int = [6, 6, 3, 2, 6]
    gots2: [5]int = [5, 5, 3, 0, 0]
    var pos2 := 0
    for j in 0..5 {
        got := try r2.Read(mut d[0..wants2[j]])
        Assert(got == gots2[j])
        Assert(TrwFrom(d[0..got], pos2))
        pos2 += got
    }
    Assert(pos2 == 13)
}

// Reader.Until's copies are raw too: a delimiter in the first
// fill, one on the last buffered byte that also fills buf, a full buf
// across three refills, one found after a refill, and the end of input.
test "Reader.Until copies to the delimiter across refills" (sys: &System) {
    var s := TrwSrc(next: 0, total: 40, chunk: 8)
    var ib: [8]u8 = [0; 8]
    var r := (&TrwSrcPort).Reader(sys.io, mut s, mut ib[..])
    var d: [20]u8 = [0; 20]
    delims: [6]u8 = [5, 7, 5, 37, 37, 37]
    wants: [6]int = [20, 2, 20, 20, 20, 20]
    gots: [6]int = [6, 2, 20, 10, 2, 0]
    var pos := 0
    for j in 0..6 {
        got := try r.Until(delims[j], mut d[0..wants[j]])
        Assert(got == gots[j])
        Assert(TrwFrom(d[0..got], pos))
        pos += got
    }
    Assert(pos == 40)
}

// TrwRe counts the calls of TrwReRead, a table entry that re-enters the
// Reader it is given on its first call: the nested Read fills the inline
// buffer (8 bytes of 7) and takes 3, so start is 3 when this entry writes
// a newline at nl (when nl >= 0) and returns ret. No std entry re-enters;
// the blocks hold Read's, Until's and Line's raw copies in bounds if one
// did: start is reset after the fill, so the raw copy stays inside the
// buffer. Inside Line's fill, with ret 5, adding 5 to the end the nested
// Read left would give 13 against cap 8, past the buffer; such a re-entry
// that moves end is ReadError, and the cases ZRL and ZRV below check that
// the canary after the buffer stays intact for Line and the next Read or
// Until.
struct TrwRe {
    calls: int
    ret: int
    nl: int
}

// TrwReMem is an inline buffer with a canary after it (fields in
// declaration order), so a read past the buffer shows as a canary byte:
// 0xCC for ZRV, 'Z' for ZRL.
struct TrwReMem {
    a: [8]u8
    canary: [8]u8
}

const TrwReTable = ReaderTable(read: TrwReRead, line: ReadLineBytewise)

TrwReRead(r: mut Reader, dst: mut []u8): ReadError!int {
    var first := false
    var ret := 0
    var nl := -1
    unsafe "a TrwReTable reader's ctx is the live TrwRe of its block" {
        p := raw.FromAddr[TrwRe](ReaderCtx(&*r))
        (*p).calls += 1
        first = (*p).calls == 1
        ret = (*p).ret
        nl = (*p).nl
    }
    if first {
        var small: [3]u8 = [0; 3]
        _ = try r.Read(mut small[..])
        if nl >= 0 {
            dst[nl] = 0x0A
        }
        return ret
    }
    for i in 0..dst.Len() {
        dst[i] = 7
    }
    return dst.Len()
}

// TrwNoCanary is whether no byte of b is 0xCC and every byte of m's canary
// still is.
TrwNoCanary(b: []u8, m: &TrwReMem): bool {
    for x in b {
        if x == 0xCC {
            return false
        }
    }
    for x in m.canary[..] {
        if x != 0xCC {
            return false
        }
    }
    return true
}

test "a table entry that re-enters its Reader leaves Read and Until in bounds during their own fills" (sys: &System) {
    var s := TrwRe(calls: 0, ret: 2, nl: -1)
    var ib: [8]u8 = [0; 8]
    var r := NewReader(sys.io, raw.Of(&TrwReTable), raw.Addr(raw.OfMut(mut s)), mut ib[..])
    var d: [4]u8 = [0; 4]
    // The outer fill's 2 bytes, served from 0.
    Assert(try r.Read(mut d[..]) == 2)
    Assert(d[0] == 7 && d[1] == 7)
    var s2 := TrwRe(calls: 0, ret: 2, nl: -1)
    var ib2: [8]u8 = [0; 8]
    var r2 := NewReader(sys.io, raw.Of(&TrwReTable), raw.Addr(raw.OfMut(mut s2)), mut ib2[..])
    var d2: [4]u8 = [0; 4]
    // The 2 bytes, then 2 of the next fill: no 'A' in them, buf full.
    Assert(try r2.Until(0x41, mut d2[..]) == 4)
    Assert(s2.calls == 3)
}

// Case ZRV: the entry re-enters inside Line's fill,
// writes a newline at 4 and returns 5. Line is ReadError, leaving start 3
// and end 8 as the nested Read set them, over 7 7 7 7 \n 7 7 7; the next
// Read serves those 5 bytes, and the next Until them and two refills, with
// no canary byte.
test "Line is ReadError when its entry moves end, and the next Read and Until stay in the buffer" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s := TrwRe(calls: 0, ret: 5, nl: 4)
    var m := TrwReMem(a: [0; 8], canary: [0xCC; 8])
    var r := NewReader(sys.io, raw.Of(&TrwReTable), raw.Addr(raw.OfMut(mut s)), mut m.a[..])
    var refused := false
    _ = r.Line(&ar) else {
        refused = true
    }
    Assert(refused)
    Assert(s.calls == 2)
    var d: [16]u8 = [0; 16]
    n := try r.Read(mut d[..])
    Assert(n == 5)
    Assert(d[0] == 7 && d[1] == 0x0A && d[2] == 7 && d[3] == 7 && d[4] == 7)
    Assert(TrwNoCanary(d[0..n], &m))
    var s2 := TrwRe(calls: 0, ret: 5, nl: 4)
    var m2 := TrwReMem(a: [0; 8], canary: [0xCC; 8])
    var r2 := NewReader(sys.io, raw.Of(&TrwReTable), raw.Addr(raw.OfMut(mut s2)), mut m2.a[..])
    var refused2 := false
    _ = r2.Line(&ar) else {
        refused2 = true
    }
    Assert(refused2)
    var d2: [16]u8 = [0; 16]
    n2 := try r2.Until(0x41, mut d2[..])
    Assert(n2 == 16)
    Assert(d2[1] == 0x0A)
    Assert(TrwNoCanary(d2[0..n2], &m2))
    Assert(s2.calls == 4)
}

// Case ZRL: as ZRV, but the entry writes no newline, and
// the canary is 'Z' bytes with a newline at its byte 2, all valid UTF-8,
// so a Line that read past the buffer would return a line ending in "ZZ"
// (a 0xCC canary made that line invalid
// UTF-8, ReadError for another reason). The first Line itself is
// ReadError. So is the next, since the 8 bytes then buffered hold no
// newline and the probe byte is not one, and the Read after them gets
// those 8 bytes of 7 and no 'Z'.
test "Line is ReadError when its entry moves end with no newline, and never reads past the buffer" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s := TrwRe(calls: 0, ret: 5, nl: -1)
    var m := TrwReMem(a: [0; 8], canary: [0x5A, 0x5A, 0x0A, 0x5A, 0x5A, 0x5A, 0x5A, 0x5A])
    var r := NewReader(sys.io, raw.Of(&TrwReTable), raw.Addr(raw.OfMut(mut s)), mut m.a[..])
    var refused := false
    _ = r.Line(&ar) else {
        refused = true
    }
    Assert(refused)
    Assert(s.calls == 2)
    var refused2 := false
    _ = r.Line(&ar) else {
        refused2 = true
    }
    Assert(refused2)
    Assert(s.calls == 4)
    var d: [16]u8 = [0; 16]
    n := try r.Read(mut d[..])
    Assert(n == 8)
    for i in 0..n {
        Assert(d[i] == 7)
    }
    Assert(m.canary[2] == 0x0A && m.canary[0] == 0x5A && m.canary[7] == 0x5A)
}

// TrwMv counts the calls of TrwMvRead, an entry that re-enters its Reader
// and moves only start. With
// probe false, call 1 gives "abcde", and call 2, Line's next fill, Reads
// those 5 buffered bytes with no fill and returns 0. With probe true, call
// 1 gives 8 'a', and call 2, Line's probe read, Reads the 8 buffered
// bytes, then 3 of a fill (call 3, 8 'b'), and gives a newline. Line used
// to return "abcde" and "bbbbbbbb" from buf's start, bytes the nested
// Reads had taken; now each is ReadError.
struct TrwMv {
    calls: int
    probe: bool
}

const TrwMvTable = ReaderTable(read: TrwMvRead, line: ReadLineBytewise)

TrwMvRead(r: mut Reader, dst: mut []u8): ReadError!int {
    var calls := 0
    var probe := false
    unsafe "a TrwMvTable reader's ctx is the live TrwMv of its block" {
        p := raw.FromAddr[TrwMv](ReaderCtx(&*r))
        (*p).calls += 1
        calls = (*p).calls
        probe = (*p).probe
    }
    if calls == 1 {
        if probe {
            for i in 0..8 {
                dst[i] = 0x61
            }
            return 8
        }
        for i in 0..5 {
            dst[i] = 0x61 + Trunc[u8](i)
        }
        return 5
    }
    if calls == 2 {
        if probe {
            var big: [16]u8 = [0; 16]
            _ = try r.Read(mut big[..])
            var small: [3]u8 = [0; 3]
            _ = try r.Read(mut small[..])
            dst[0] = 0x0A
            return 1
        }
        var five: [5]u8 = [0; 5]
        _ = try r.Read(mut five[..])
        return 0
    }
    if calls == 3 && probe {
        for i in 0..dst.Len() {
            dst[i] = 0x62
        }
        return dst.Len()
    }
    return 0
}

test "Line is ReadError when its entry moves only start, in a fill or in the probe read" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s := TrwMv(calls: 0, probe: false)
    var b: [8]u8 = [0; 8]
    var r := NewReader(sys.io, raw.Of(&TrwMvTable), raw.Addr(raw.OfMut(mut s)), mut b[..])
    var refused := false
    _ = r.Line(&ar) else {
        refused = true
    }
    Assert(refused)
    Assert(s.calls == 2)
    // The nested Read took all 5 bytes, so the input ends with no line.
    l := try r.Line(&ar)
    Assert(l.IsNone())
    Assert(s.calls == 3)
    var s2 := TrwMv(calls: 0, probe: true)
    var b2: [8]u8 = [0; 8]
    var r2 := NewReader(sys.io, raw.Of(&TrwMvTable), raw.Addr(raw.OfMut(mut s2)), mut b2[..])
    var refused2 := false
    _ = r2.Line(&ar) else {
        refused2 = true
    }
    Assert(refused2)
    Assert(s2.calls == 3)
    // The 5 'b' the nested Read left are the input's last line.
    l2 := try r2.Line(&ar)
    Assert(l2.IsSome() && l2.Must() == "bbbbb")
    Assert(s2.calls == 4)
}

// TrwEnd counts the calls of TrwEndRead, an entry that re-enters its Reader
// and moves only end, leaving start 0, so only the end half of Line's
// compares can catch it. Mode 1, Line's fill: call 1 calls Line, whose fill
// (call 2) gives 8 'x' and whose probe read (call 3) gives 'y', so the
// nested Line is ReadError with start 0 and end 8; call 1 swallows that,
// writes 5 'w' and returns 5; adding 5 to end 8 against cap 8 would
// return "wwwwwxxxZZ", two canary bytes past the buffer. Mode 2, Line's
// probe read: call 1 gives 8 'a'; call 2, the probe read, Reads the 8
// buffered bytes, then 3 more, whose fill (call 3) is end of input, which
// leaves start 0 and end 0, and gives a newline; trusting that would
// return an empty line. Each mode is ReadError.
struct TrwEnd {
    calls: int
    mode: int
    ar: *Arena
}

const TrwEndTable = ReaderTable(read: TrwEndRead, line: ReadLineBytewise)

TrwEndFill(dst: mut []u8, b: u8, n: int): int {
    for i in 0..n {
        dst[i] = b
    }
    return n
}

TrwEndRead(r: mut Reader, dst: mut []u8): ReadError!int {
    var calls := 0
    var mode := 0
    var ap := raw.Null[Arena]()
    unsafe "a TrwEndTable reader's ctx is the live TrwEnd of its block" {
        p := raw.FromAddr[TrwEnd](ReaderCtx(&*r))
        (*p).calls += 1
        calls = (*p).calls
        mode = (*p).mode
        ap = (*p).ar
    }
    if mode == 1 {
        if calls == 1 {
            unsafe "ar is the block's arena, which outlives its reader" {
                _ = r.Line(&*ap) else {
                    _ = 0
                }
            }
            return TrwEndFill(dst, 0x77, 5)
        }
        if calls == 2 {
            return TrwEndFill(dst, 0x78, dst.Len())
        }
        if calls == 3 {
            dst[0] = 0x79
            return 1
        }
        return 0
    }
    if calls == 1 {
        return TrwEndFill(dst, 0x61, 8)
    }
    if calls == 2 {
        var big: [16]u8 = [0; 16]
        _ = try r.Read(mut big[..])
        var small: [3]u8 = [0; 3]
        _ = try r.Read(mut small[..])
        dst[0] = 0x0A
        return 1
    }
    return 0
}

// TrwEndLine is r.Line's result as text: the line, "<none>" or "<err>".
TrwEndLine(r: mut Reader, a: &Arena): str from a {
    l := r.Line(a) else {
        return "<err>"
    }
    if v := l {
        return v
    }
    return "<none>"
}

// TrwNoZ is whether no byte of s is 'Z', the canary of a TrwReMem.
TrwNoZ(s: str): bool {
    for x in s.AsBytes() {
        if x == 0x5A {
            return false
        }
    }
    return true
}

test "Line is ReadError when a nested Line moves only end in its fill, and never reads past the buffer" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s := TrwEnd(calls: 0, mode: 1, ar: raw.Of(&ar))
    var m := TrwReMem(a: [0; 8], canary: [0x5A, 0x5A, 0x0A, 0x5A, 0x5A, 0x5A, 0x5A, 0x5A])
    var r := NewReader(sys.io, raw.Of(&TrwEndTable), raw.Addr(raw.OfMut(mut s)), mut m.a[..])
    l := TrwEndLine(mut r, &ar)
    Assert(TrwNoZ(l))
    Assert(l == "<err>")
    Assert(s.calls == 3)
    Assert(m.canary[0] == 0x5A && m.canary[2] == 0x0A && m.canary[7] == 0x5A)
}

test "Line is ReadError when nested Reads move only end in its probe read" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s := TrwEnd(calls: 0, mode: 2, ar: raw.Null[Arena]())
    var m := TrwReMem(a: [0; 8], canary: [0x5A, 0x5A, 0x0A, 0x5A, 0x5A, 0x5A, 0x5A, 0x5A])
    var r := NewReader(sys.io, raw.Of(&TrwEndTable), raw.Addr(raw.OfMut(mut s)), mut m.a[..])
    l := TrwEndLine(mut r, &ar)
    Assert(TrwNoZ(l))
    Assert(l == "<err>")
    Assert(s.calls == 3)
}

// TrwOfBytes is n bytes of 0, 1, 2, ... (each mod 256) in a, for the
// Reader.Of blocks.
TrwOfBytes(a: &Arena, n: int): OutOfMemory![]u8 from a {
    b := try a.Slice[u8](n)
    for i in 0..n {
        b[i] = Trunc[u8](i)
    }
    return b[..]
}

// Reader.Of serves its bytes with no table call until they are read, then
// 0 every time; a read of one byte, of more than is left, and of exactly
// what is left; ReadAll; an empty b.
test "Reader.Of reads every byte once, then 0" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    b := try TrwOfBytes(&ar, 300)
    var r := Reader.Of(b)
    var d: [128]u8 = [0; 128]
    wants: [8]int = [1, 7, 128, 100, 128, 64, 128, 1]
    gots: [8]int = [1, 7, 128, 100, 64, 0, 0, 0]
    var pos := 0
    for j in 0..8 {
        got := try r.Read(mut d[0..wants[j]])
        Assert(got == gots[j])
        Assert(TrwFrom(d[0..got], pos))
        pos += got
    }
    Assert(pos == 300)
    var all: [512]u8 = [0; 512]
    var r2 := Reader.Of(b)
    Assert(try r2.ReadAll(mut all[..]) == 300)
    Assert(TrwFrom(all[0..300], 0))
    Assert(try r2.ReadAll(mut all[..]) == 0)
    var r3 := Reader.Of(b[0..0])
    Assert(try r3.Read(mut d[..]) == 0)
    Assert(try r3.Until(0x0A, mut d[..]) == 0)
    Assert(try r3.Until(5, mut d[..]) == 0)
    Assert((try r3.Line(&ar)).IsNone())
}

// Until scans a Reader.Of's bytes in place, for \n and for any other
// delimiter: one found at once, one on the last byte of buf, buf full
// before it, Read and Until mixed, and the rest with no delimiter, then 0.
test "Reader.Of's Until stops at the delimiter" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    b := try TrwOfBytes(&ar, 300)
    var r := Reader.Of(b)
    var d: [300]u8 = [0; 300]
    // 10 is \n: the line entry is not used while bytes are left.
    delims: [7]u8 = [10, 20, 40, 41, 200, 7, 7]
    wants: [7]int = [300, 10, 5, 300, 300, 300, 300]
    gots: [7]int = [11, 10, 5, 16, 156, 63, 36]
    var pos := 0
    for j in 0..7 {
        got := try r.Until(delims[j], mut d[0..wants[j]])
        Assert(got == gots[j])
        Assert(TrwFrom(d[0..got], pos))
        pos += got
        if j == 3 {
            // A Read between two Untils takes the next bytes.
            n := try r.Read(mut d[0..3])
            Assert(n == 3 && TrwFrom(d[0..3], pos))
            pos += 3
        }
    }
    Assert(pos == 300)
    // At the end, the line entry and the per-byte path give 0 too.
    Assert(try r.Until(0x0A, mut d[..]) == 0)
    Assert(try r.Until(1, mut d[..]) == 0)
    Assert(try r.Until(7, mut d[..]) == 0)
    Assert(try r.Read(mut d[..]) == 0)
}

// Line over a Reader.Of reads through its line entry: \n and \r\n
// stripped, an empty line, a line longer than LineUnbuffered's 1024-byte
// chunk, and a last line with no \n, then none. The bytes are a str
// constant's, which may be read-only memory: no path writes them.
const TrwOfText = "a\r\nbb\n\nccc"

test "Reader.Of's Line reads lines and never writes its bytes" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var r := Reader.Of(TrwOfText.AsBytes())
    Assert((try r.Line(&ar)).Must() == "a")
    Assert((try r.Line(&ar)).Must() == "bb")
    Assert((try r.Line(&ar)).Must() == "")
    Assert((try r.Line(&ar)).Must() == "ccc")
    Assert((try r.Line(&ar)).IsNone())
    Assert(TrwOfText == "a\r\nbb\n\nccc")
    // 3,000 x's, \n, then 5 y's.
    var long := Text.New(&ar)
    for _ in 0..3000 {
        try long.Push("x")
    }
    try long.Push("\nyyyyy")
    var r2 := Reader.Of(long.Str().AsBytes())
    l := (try r2.Line(&ar)).Must()
    Assert(l.Len() == 3000 && l.StartsWith("xxx") && l.EndsWith("xxx"))
    Assert((try r2.Line(&ar)).Must() == "yyyyy")
    Assert((try r2.Line(&ar)).IsNone())
    // Read, Until and Line mixed over one Reader: each takes what follows.
    var r3 := Reader.Of(TrwOfText.AsBytes())
    var d: [8]u8 = [0; 8]
    Assert(try r3.Read(mut d[0..1]) == 1 && d[0] == 0x61)
    Assert(try r3.Until(0x62, mut d[..]) == 3)
    Assert((try r3.Line(&ar)).Must() == "b")
    Assert(try r3.Until(0x63, mut d[..]) == 2 && d[0] == 0x0A && d[1] == 0x63)
    Assert(try r3.Read(mut d[..]) == 2 && d[0] == 0x63 && d[1] == 0x63)
    Assert(try r3.Read(mut d[..]) == 0)
}

// TrwOfSum reads b through a Reader.Of: Until for each ',' field (its
// length times its index), then the sum of every byte that Read gives.
TrwOfSum(b: []u8): int {
    var r := Reader.Of(b)
    var d: [4]u8 = [0; 4]
    var sum := 0
    for k in 1..4 {
        n := r.Until(0x2C, mut d[..]) else {
            return -1
        }
        sum += n * k
    }
    for {
        n := r.Read(mut d[0..3]) else {
            return -1
        }
        if n == 0 {
            break
        }
        for i in 0..n {
            sum += int(d[i])
        }
    }
    return sum
}

// TrwOfSum's fields: "ab," (3), "c," (2), "defgh" with 4 at most (4), then
// "h" (104) and "12" (49 + 50).
const TrwOfAtCompileTime = TrwOfSum("ab,c,defgh12".AsBytes())

// Reader.Of takes no Io, so the evaluator runs it and its methods: the
// same value at compile time and at run time.
test "Reader.Of runs at compile time" (sys: &System) {
    Assert(TrwOfAtCompileTime == 3 + 2 * 2 + 4 * 3 + 104 + 49 + 50)
    Assert(TrwOfSum("ab,c,defgh12".AsBytes()) == TrwOfAtCompileTime)
}

// Line over a Reader.Of takes lines up to LineMax (65,536 bytes before
// the \n), as a Stdin() reader's, and a longer one is ReadError.
test "Reader.Of's Line takes 64 KiB lines and refuses a longer one" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var t := Text.New(&ar)
    for _ in 0..65536 {
        try t.Push("x")
    }
    try t.Push("\nz")
    var r := Reader.Of(t.Str().AsBytes())
    Assert((try r.Line(&ar)).Must().Len() == 65536)
    Assert((try r.Line(&ar)).Must() == "z")
    var u := Text.New(&ar)
    for _ in 0..65537 {
        try u.Push("x")
    }
    var r2 := Reader.Of(u.Str().AsBytes())
    var refused := false
    _ = r2.Line(&ar) else {
        refused = true
    }
    Assert(refused)
}

// TrwOfCalls counts the entry calls of a Reader shaped as Reader.Of's
// (cap 0, b's bytes at buf[start..end]) over TrwOfCountTable, whose
// entries count and return 0: Read and Until serve the bytes left with
// no entry call, which pins Until's scan for a cap-0 Reader with bytes
// left; once they are used up, each asks its entry once.
struct TrwOfCalls {
    n: int
}

const TrwOfCountTable = ReaderTable(read: TrwOfCount, line: TrwOfCount)

TrwOfCount(r: mut Reader, dst: mut []u8): ReadError!int {
    unsafe "a TrwOfCountTable reader's ctx is the live TrwOfCalls of its block" {
        p := raw.PtrCast[u8, TrwOfCalls](r.ctx)
        (*p).n += 1
    }
    return 0
}

TrwOfReader(c: mut TrwOfCalls, data: []u8): Reader from c, data {
    unsafe "c and data outlive the Reader under the loans it holds; cap is 0, so nothing writes data" {
        return Reader(io: InertIoValue(), table: &TrwOfCountTable, ctx: raw.PtrCast[TrwOfCalls, u8](raw.OfMut(c)), buf: raw.Data(data), start: 0, end: data.Len(), cap: 0)
    }
}

// TrwOfSeen reads the count through r, which holds c exclusively.
TrwOfSeen(r: &Reader): int {
    unsafe "a TrwOfCountTable reader's ctx is the live TrwOfCalls of its block" {
        return (*raw.PtrCast[u8, TrwOfCalls](r.ctx)).n
    }
}

test "Until scans a cap-0 Reader's bytes with no entry call" (sys: &System) {
    var c := TrwOfCalls(n: 0)
    data := "ab,cd,efgh".AsBytes()
    var r := TrwOfReader(mut c, data)
    var d: [8]u8 = [0; 8]
    Assert(try r.Until(0x2C, mut d[..]) == 3)
    Assert(try r.Until(0x0A, mut d[0..2]) == 2)
    Assert(try r.Read(mut d[0..1]) == 1 && d[0] == 0x2C)
    Assert(try r.Until(0x2C, mut d[0..4]) == 4 && d[3] == 0x68)
    Assert(TrwOfSeen(&r) == 0)
    Assert(try r.Until(0x2C, mut d[..]) == 0)
    Assert(try r.Until(0x0A, mut d[..]) == 0)
    Assert(try r.Read(mut d[..]) == 0)
    Assert(c.n == 3)
}

// BytesRead with bytes left. Read and Until call a Reader.Of's
// read entry only once start == end, so their blocks never run its copy;
// this block calls it directly: a copy of a whole dst from the start, a
// copy after a Read moved start, a copy of fewer bytes than dst holds at
// the end of b (the rest of dst untouched), an empty dst, then 0.
test "Reader.Of's read entry copies the next bytes when called with bytes left" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    b := try TrwOfBytes(&ar, 10)
    var r := Reader.Of(b)
    var d: [4]u8 = [0xEE; 4]
    Assert(try BytesRead(mut r, mut d[..]) == 4)
    Assert(TrwFrom(d[..], 0) && r.start == 4 && r.end == 10)
    var one: [1]u8 = [0; 1]
    Assert(try r.Read(mut one[..]) == 1 && one[0] == 4)
    Assert(try BytesRead(mut r, mut d[..]) == 4)
    Assert(TrwFrom(d[..], 5) && r.start == 9)
    d = [0xEE; 4]
    Assert(try BytesRead(mut r, mut d[0..0]) == 0 && r.start == 9)
    Assert(try BytesRead(mut r, mut d[..]) == 1)
    Assert(d[0] == 9 && d[1] == 0xEE && d[2] == 0xEE && d[3] == 0xEE)
    Assert(r.start == 10 && r.end == 10)
    Assert(try BytesRead(mut r, mut d[..]) == 0 && r.start == 10)
    Assert(try r.Read(mut d[..]) == 0)
}

// Reader.Until's served case, each step with the count, the reader's start
// and end and the source's position after it: a delimiter absent from the
// buffered bytes and found after a refill, a buf shorter than the buffered
// run, an empty buf with bytes buffered, a delimiter on the last buffered
// byte, a Reader.Of read to its end, and a reader with no inline buffer for
// \n (its line entry) and for another delimiter (one byte per read), which
// takes no byte past the delimiter from its source.
test "Reader.Until keeps its counts and the reader's state in the served case and past it" (sys: &System) {
    var s := TrwSrc(next: 0, total: 40, chunk: 8)
    var ib: [8]u8 = [0; 8]
    var r := (&TrwSrcPort).Reader(sys.io, mut s, mut ib[..])
    var d: [20]u8 = [0; 20]
    Assert(try r.Until(10, mut d[..]) == 11 && TrwFrom(d[0..11], 0))
    Assert(r.start == 3 && r.end == 8)
    Assert(try r.Until(200, mut d[0..2]) == 2 && TrwFrom(d[0..2], 11))
    Assert(r.start == 5 && r.end == 8)
    Assert(try r.Until(13, mut d[0..0]) == 0)
    Assert(r.start == 5 && r.end == 8)
    Assert(try r.Until(15, mut d[..]) == 3 && TrwFrom(d[0..3], 13))
    Assert(r.start == 8 && r.end == 8)
    Assert(try r.Until(200, mut d[..]) == 20 && TrwFrom(d[0..20], 16))
    Assert(try r.Until(200, mut d[..]) == 4 && TrwFrom(d[0..4], 36))
    Assert(try r.Until(200, mut d[..]) == 0)
    Assert(s.next == 40)
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    b := try TrwOfBytes(&ar, 12)
    var o := Reader.Of(b)
    Assert(try o.Until(200, mut d[0..5]) == 5 && TrwFrom(d[0..5], 0))
    // The last 7 bytes, then the refill that ends the input, which sets
    // start and end to 0 before it asks the entry, as every refill does.
    Assert(try o.Until(200, mut d[..]) == 7 && TrwFrom(d[0..7], 5))
    Assert(o.start == 0 && o.end == 0)
    Assert(try o.Until(0x0A, mut d[..]) == 0)
    Assert(try o.Until(3, mut d[..]) == 0)
    var s0 := TrwSrc(next: 0, total: 40, chunk: 8)
    var empty: [0]u8 = []
    var r0 := (&TrwSrcPort).Reader(sys.io, mut s0, mut empty[..])
    Assert(try r0.Until(0x0A, mut d[..]) == 11 && TrwFrom(d[0..11], 0))
    Assert(try r0.Until(15, mut d[..]) == 5 && TrwFrom(d[0..5], 11))
    Assert(try r0.Until(200, mut d[0..3]) == 3 && TrwFrom(d[0..3], 16))
    Assert(r0.start == 0 && r0.end == 0)
    Assert(s0.next == 19)
}

// The Until table: Reader.Until over every reader shape against the
// outcomes of a simpler Until (each fill searched once by IndexByteIn),
// pinned below, so a faster Until has to give the same result, error, bytes and
// reader state for every input. A case is a source, an inline buffer of
// 0, 1, 3, 5 or 64 bytes (or a Reader.Of), a delimiter and a schedule of
// buf lengths: 1, 2, 4 or 100 on every call, or 0, 4, 0, 1, 100, 0, 2 in
// turn, so an empty buf meets bytes already buffered. Each case calls Until
// until two calls with a non-empty buf return 0 (at most 300 calls) and
// folds every call's result (-1 for ReadError), all of buf (filled with
// 0xEE before the call, so a byte written past the count shows), the
// reader's start and end and the source's position (FNV-1a). The texts:
// TrwUntilText (repeated delimiters, an empty line, a last byte with no
// \n), and the 70
// bytes 0, 1, ..., 69, where each delimiter is at one position, \n at 10,
// and cap 64 refills before the last six. The delimiters: each byte value
// of the text, then one it never holds; with the small buffers, a
// delimiter past the first fill is absent from the buffered bytes and
// found after a refill, and the last byte of the input is one. The
// sources: whole fills; short fills (2 bytes at most); one that fails once
// with ReadError at position 9 (4 bytes a fill), then goes on; and one
// whose third fill reports one byte more than it was given and whose sixth
// reports -1, a broken contract each.
const TrwUntilText = "ab\ncdefgh\n\nxyzc0123456789abcdefghijklmnopqrstuvwxyz\nq"

// TrwUs is a source of the Until table: text 0 is TrwUntilText and text 1
// the bytes 0 to 69; next is the position, chunk the most bytes a fill
// gives, fail_at the position whose fill fails once (-1: none), lie the
// fill that reports dst's length plus one (and lie * 2 the fill that
// reports -1; 0: none), fills the fills so far.
struct TrwUs {
    text: int
    next: int
    chunk: int
    fail_at: int
    failed: bool
    lie: int
    fills: int
}

// TrwUsLen and TrwUsByte are text's length and its byte at pos.
TrwUsLen(text: int): int {
    if text == 0 {
        return TrwUntilText.Len()
    }
    return 70
}

TrwUsByte(text: int, pos: int): u8 {
    if text == 0 {
        return TrwUntilText.AsBytes()[pos]
    }
    return Trunc[u8](pos)
}

TrwUsFill(s: mut TrwUs, io: Io, dst: mut []u8): ReadError!int {
    s.fills += 1
    if s.lie > 0 && s.fills == s.lie {
        return dst.Len() + 1
    }
    if s.lie > 0 && s.fills == s.lie * 2 {
        return -1
    }
    if s.fail_at >= 0 && !s.failed && s.next >= s.fail_at {
        s.failed = true
        fail ReadError
    }
    var n := TrwUsLen(s.text) - s.next
    if n > s.chunk {
        n = s.chunk
    }
    if n > dst.Len() {
        n = dst.Len()
    }
    if s.fail_at >= 0 && !s.failed && n > s.fail_at - s.next {
        n = s.fail_at - s.next
    }
    for i in 0..n {
        dst[i] = TrwUsByte(s.text, s.next + i)
    }
    s.next += n
    return n
}

const TrwUsPort = ReadPortMut[TrwUs].New(TrwUsFill)

// TrwUsSource is source kind's TrwUs over text: 0 whole fills, 1 short
// fills, 2 the one that fails, 3 the one that lies.
TrwUsSource(text: int, kind: int): TrwUs {
    var s := TrwUs(text: text, next: 0, chunk: 1000, fail_at: -1, failed: false, lie: 0, fills: 0)
    if kind == 1 {
        s.chunk = 2
    }
    if kind == 2 {
        s.chunk = 4
        s.fail_at = 9
    }
    if kind == 3 {
        s.lie = 3
    }
    return s
}

// TrwUsDelims puts text's delimiters in out and returns how many: each
// byte value it holds, once, in the order it first holds them, then one it
// never holds.
TrwUsDelims(text: int, out: mut []u8): int {
    var m := 0
    for p in 0..TrwUsLen(text) {
        c := TrwUsByte(text, p)
        var first := true
        for q in 0..m {
            if out[q] == c {
                first = false
                break
            }
        }
        if first {
            out[m] = c
            m += 1
        }
    }
    var absent: u8 = 200
    if text == 0 {
        absent = 0x7E
    }
    out[m] = absent
    return m + 1
}

// TrwUsWant is the buf length of call i under schedule sched.
TrwUsWant(sched: int, i: int): int {
    if sched == 4 {
        mixed: [7]int = [0, 4, 0, 1, 100, 0, 2]
        return mixed[i % 7]
    }
    fixed: [4]int = [1, 2, 4, 100]
    return fixed[sched]
}

// TrwUsFold folds v into the FNV-1a hash h.
TrwUsFold(h: u64, v: int): u64 {
    return (h ^ Trunc[u64](v)) *% 1099511628211
}

// TrwUsCalls runs one case's calls on r and folds them into h; src is
// the source's position after each call (r's ctx, a TrwUs), or r's start
// for a Reader.Of (ported false).
TrwUsCalls(h0: u64, r: mut Reader, delim: u8, sched: int, ported: bool): u64 {
    var h := h0
    var d: [100]u8 = [0; 100]
    var zeros := 0
    for i in 0..300 {
        want := TrwUsWant(sched, i)
        for j in 0..want {
            d[j] = 0xEE
        }
        k := r.Until(delim, mut d[0..want]) else { -1 }
        h = TrwUsFold(h, k)
        for j in 0..want {
            h = TrwUsFold(h, int(d[j]))
        }
        h = TrwUsFold(h, r.start)
        h = TrwUsFold(h, r.end)
        if ported {
            unsafe "a TrwUsPort reader's ctx is the live TrwUs of its case" {
                h = TrwUsFold(h, (*raw.PtrCast[u8, TrwUs](r.ctx)).next)
            }
        }
        if want > 0 && k == 0 {
            zeros += 1
            if zeros == 2 {
                return TrwUsFold(h, i)
            }
        } else if k != 0 {
            zeros = 0
        }
    }
    return TrwUsFold(h, 300)
}

// TrwUsRow is the fold of every case of text over source kind with an
// inline buffer of cap bytes.
TrwUsRow(io: Io, text: int, kind: int, cap: int): u64 {
    var delims: [72]u8 = [0; 72]
    m := TrwUsDelims(text, mut delims[..])
    var h: u64 = 14695981039346656037
    for j in 0..m {
        for sched in 0..5 {
            var s := TrwUsSource(text, kind)
            var ib: [64]u8 = [0; 64]
            var r := (&TrwUsPort).Reader(io, mut s, mut ib[0..cap])
            h = TrwUsCalls(h, mut r, delims[j], sched, true)
        }
    }
    return h
}

// TrwUsOfRow is TrwUsRow for a Reader.Of over text: variant 0 over its
// bytes, 1 over them from the fourth, 2 after a Read of 5 bytes, 3 after
// every byte is read.
TrwUsOfRow(text: int, variant: int): u64 {
    var bytes: [70]u8 = [0; 70]
    n := TrwUsLen(text)
    for p in 0..n {
        bytes[p] = TrwUsByte(text, p)
    }
    var delims: [72]u8 = [0; 72]
    m := TrwUsDelims(text, mut delims[..])
    var h: u64 = 14695981039346656037
    for j in 0..m {
        for sched in 0..5 {
            var r := Reader.Of(if variant == 1 { bytes[3..n] } else { bytes[0..n] })
            var skip: [128]u8 = [0; 128]
            if variant == 2 {
                _ = r.Read(mut skip[0..5]) else { -1 }
            }
            if variant == 3 {
                _ = r.ReadAll(mut skip[..]) else { -1 }
            }
            h = TrwUsCalls(h, mut r, delims[j], sched, false)
        }
    }
    return h
}

// The rows: for text 0 then text 1, each source kind 0 to 3 with caps 0,
// 1, 3, 5 and 64 (40 rows), then each text's Reader.Of variants 0 to 3.
// Computed with that simpler Until, dev and release alike; equal rows are shapes that read alike (a cap at or
// over the source's fill size; cap 0, one byte a read, for any chunk).
const TrwUntilPinned: [48]u64 = [
    6759819250987438392, 6044005182475335924, 17543600373812860494, 953884598601979458, 12397630472387685632,
    6759819250987438392, 6044005182475335924, 11146027758642465978, 11146027758642465978, 11146027758642465978,
    11664395623556240175, 11265475717337884045, 14071714090167435757, 5173698544709999343, 5173698544709999343,
    11379956695137791341, 8757172320006962289, 9994094980676678341, 10566114566005468025, 5440489127714040286,
    6697253787553276472, 13890213441436875550, 8195863016121001920, 12292476958927692396, 381147165241018240,
    6697253787553276472, 13890213441436875550, 77659567304784252, 77659567304784252, 77659567304784252,
    6317042244729498595, 11626187476020671477, 5399423966232604167, 5207676516669852835, 5207676516669852835,
    15197920405184098572, 16238775077024690508, 12600583535913269788, 988455469803226639, 16203635818337646275,
    17119689868272493900, 17557615400811535140, 15633818123706335889, 76783007784540641,
    5148843819749353156, 15084043228214500487, 12242932219385393863, 8013161683657064270,
]

test "the Until table: every reader shape gives the pinned results, errors, bytes and state" (sys: &System) {
    var got: [48]u64 = [0; 48]
    caps: [5]int = [0, 1, 3, 5, 64]
    var row := 0
    for text in 0..2 {
        for kind in 0..4 {
            for c in 0..5 {
                got[row] = TrwUsRow(sys.io, text, kind, caps[c])
                row += 1
            }
        }
    }
    for text in 0..2 {
        for variant in 0..4 {
            got[row] = TrwUsOfRow(text, variant)
            row += 1
        }
    }
    var bad := -1
    for i in 0..48 {
        if got[i] != TrwUntilPinned[i] && bad < 0 {
            bad = i
        }
    }
    if bad >= 0 {
        var ar := try Arena.New(sys.heap)
        defer ar.Free()
        var t := Text.New(&ar)
        var w := t.Writer()
        try Print(mut w, "Until table row {bad} differs; the rows now: [", (bad))
        for i in 0..48 {
            try Print(mut w, "{v}, ", (v: got[i]))
        }
        try w.Str("]")
        try w.Flush()
        Panic(t.Str())
    }
}
