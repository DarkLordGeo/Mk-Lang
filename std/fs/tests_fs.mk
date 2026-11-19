// Why: the basic calls of std.fs (Open, Create, ReadBytes, WriteAll, Rename,
// Remove, File) need direct cases besides the differential tests.
// What: test blocks for them, run by mk test --std=std.fs and
// std/std-io_blocks_test.go.
// How: each block works on files named mk_tfs_<id> in the working
// directory, removes them before and after, and ends with no leak.

import std.core
import std.raw

// TfsGone removes p, ignoring every error (NotFound included).
TfsGone(io: Io, p: str) {
    Remove(io, p) else {
    }
}

// TfsName is e as it prints, for asserting which error a call
// failed with: the member's name, and its fields when it has some.
TfsName(a: &Arena, e: Error | OutOfMemory): str from a {
    // a test arena holds a few dozen bytes more
    return a.Fmt("{e}", (e)).Must()
}

test "Open and ReadBytes of a missing file are NotFound" (sys: &System) {
    p := "mk_tfs_132"
    TfsGone(sys.io, p)
    f := Open(sys.io, p) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    f.Abandon()
    Assert(false)
}

test "ReadBytes and Rename of a missing file are NotFound" (sys: &System) {
    p := "mk_tfs_132b"
    TfsGone(sys.io, p)
    var got := ""
    _ = ReadBytes(sys.arena, sys.io, p) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "NotFound")
    got = ""
    Rename(sys.io, p, "mk_tfs_132c") else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "NotFound")
    got = ""
    Remove(sys.io, p) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "NotFound")
}

test "WriteAll then ReadBytes round-trips every byte value" (sys: &System) {
    p := "mk_tfs_133"
    var data: [10000]u8 = [0; 10000]
    for i in 0..10000 {
        data[i] = Trunc[u8](i * 7 + i / 256)
    }
    for n in [0, 1, 255, 4096, 4097, 10000] {
        WriteAll(sys.io, p, data[0..n]).Must()
        b := ReadBytes(sys.arena, sys.io, p).Must()
        Assert(b.Len() == n)
        for i in 0..n {
            Assert(b[i] == data[i])
        }
    }
    TfsGone(sys.io, p)
}

test "ReadText validates UTF-8" (sys: &System) {
    p := "mk_tfs_136"
    WriteAll(sys.io, p, "h\u{e9}llo \u{1F600}\n".AsBytes()).Must()
    s := ReadText(sys.arena, sys.io, p).Must()
    Assert(s == "h\u{e9}llo \u{1F600}\n")
    // An overlong '/', a lone surrogate, a truncated sequence.
    bad: [3][3]u8 = [[0xC0, 0xAF, 0x41], [0xED, 0xA0, 0x80], [0x41, 0xE2, 0x82]]
    for k in 0..3 {
        WriteAll(sys.io, p, bad[k][..]).Must()
        var got := ""
        _ = ReadText(sys.arena, sys.io, p) else e {
            got = TfsName(sys.arena, e)
        }
        Assert(got == "Utf8Error")
    }
    TfsGone(sys.io, p)
}

// TfsFromFails reports whether str.From refuses b.
TfsFromFails(b: []u8): bool {
    _ = str.From(b) else {
        return true
    }
    return false
}

// TfsTextIs reads p with ReadText: 0 and the same bytes as b, 1 for
// Utf8Error, 2 for any other error.
TfsTextIs(a: &Arena, io: Io, p: str, b: []u8): int {
    s := ReadText(a, io, p) else e {
        if TfsName(a, e) == "Utf8Error" {
            return 1
        }
        return 2
    }
    Assert(s.Len() == b.Len())
    for i in 0..b.Len() {
        Assert(s.Byte(i) == b[i])
    }
    return 0
}

test "ReadText of a large file agrees with str.From at every size and fault" (sys: &System) {
    // rt.StrFrom (std.simd.ValidUtf8) behind ReadText, against str.From
    // (std.core's SWAR) on the same bytes: sizes on both sides of the
    // vector widths, a bad byte planted at the start, the middle and the
    // last byte.
    p := "mk_tfs_readtext"
    enc := "a\u{e9}\u{4e16}\u{1F600}".AsBytes()
    sizes: [6]int = [63, 64, 255, 256, 4099, 70001]
    var buf := sys.arena.Slice[u8](70001).Must()
    for z in 0..6 {
        n := sizes[z]
        for i in 0..n {
            buf[i] = enc[i % enc.Len()]
        }
        WriteAll(sys.io, p, buf[0..n]).Must()
        got := TfsTextIs(sys.arena, sys.io, p, buf[0..n])
        if TfsFromFails(buf[0..n]) {
            Assert(got == 1)
        } else {
            Assert(got == 0)
        }
        at: [3]int = [0, n / 2, n - 1]
        for k in 0..3 {
            old := buf[at[k]]
            buf[at[k]] = 0xFF
            WriteAll(sys.io, p, buf[0..n]).Must()
            Assert(TfsTextIs(sys.arena, sys.io, p, buf[0..n]) == 1)
            Assert(TfsFromFails(buf[0..n]))
            buf[at[k]] = old
        }
    }
    TfsGone(sys.io, p)
}

test "Create truncates an existing file" (sys: &System) {
    p := "mk_tfs_137"
    WriteAll(sys.io, p, "a longer first content".AsBytes()).Must()
    var f := Create(sys.io, p).Must()
    f.WriteAll(sys.io, "short".AsBytes()) else {
        f.Abandon()
        Assert(false)
        return
    }
    f.Close(sys.io) else {
        f.Abandon()
        Assert(false)
        return
    }
    Assert(ReadText(sys.arena, sys.io, p).Must() == "short")
    TfsGone(sys.io, p)
}

test "ReadAt and WriteAt keep the position, Read and Write move it" (sys: &System) {
    p := "mk_tfs_139"
    io := sys.io
    var f := Create(io, p).Must()
    var buf: [4]u8 = [0; 4]
    Assert(f.Write(io, "abcdefgh".AsBytes()).Must() == 8)
    Assert(f.WriteAt(io, 1, "XY".AsBytes()).Must() == 2)
    // The position is still 8: this write appends.
    Assert(f.Write(io, "ij".AsBytes()).Must() == 2)
    Assert(f.ReadAt(io, 0, mut buf[..]).Must() == 4)
    Assert(buf[0] == 0x61 && buf[1] == 0x58 && buf[2] == 0x59 && buf[3] == 0x64)
    // At the end of the file: Read reads nothing and keeps the position.
    Assert(f.Read(io, mut buf[..]).Must() == 0)
    Assert(f.ReadAt(io, 8, mut buf[..]).Must() == 2)
    Assert(buf[0] == 0x69 && buf[1] == 0x6A)
    Assert(f.ReadAt(io, 100, mut buf[..]).Must() == 0)
    f.Close(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    Assert(ReadText(sys.arena, io, p).Must() == "aXYdefghij")
    TfsGone(io, p)
}

test "File.Writer writes at the position and Flush hands the bytes over" (sys: &System) {
    p := "mk_tfs_140"
    io := sys.io
    var f := Create(io, p).Must()
    Assert(f.Write(io, "0123".AsBytes()).Must() == 4)
    var wb: [8]u8 = [0; 8]
    var w := f.Writer(io, mut wb[..])
    var ok := true
    w.Str("ab") else {
        ok = false
    }
    // Buffered, not yet in the file.
    Assert(ReadText(sys.arena, io, p).Must() == "0123")
    w.Str("cdefghijklmnop") else {
        ok = false
    }
    w.Flush() else {
        ok = false
    }
    Assert(ok)
    Assert(ReadText(sys.arena, io, p).Must() == "0123abcdefghijklmnop")
    // The Writer moved the file's position: Write continues after it.
    Assert(f.Write(io, "Z".AsBytes()).Must() == 1)
    f.Close(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    Assert(ReadText(sys.arena, io, p).Must() == "0123abcdefghijklmnopZ")
    TfsGone(io, p)
}

test "Rename replaces an existing target" (sys: &System) {
    a := "mk_tfs_141a"
    b := "mk_tfs_141b"
    WriteAll(sys.io, a, "new".AsBytes()).Must()
    WriteAll(sys.io, b, "old content".AsBytes()).Must()
    Rename(sys.io, a, b).Must()
    Assert(ReadText(sys.arena, sys.io, b).Must() == "new")
    var got := ""
    _ = ReadBytes(sys.arena, sys.io, a) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "NotFound")
    TfsGone(sys.io, b)
}

test "Remove deletes a file once" (sys: &System) {
    p := "mk_tfs_142"
    WriteAll(sys.io, p, "x".AsBytes()).Must()
    Remove(sys.io, p).Must()
    var got := ""
    Remove(sys.io, p) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "NotFound")
}

test "Close consumes the file and Abandon releases one never closed" (sys: &System) {
    p := "mk_tfs_143"
    io := sys.io
    var f := Create(io, p).Must()
    Assert(f.Write(io, "kept".AsBytes()).Must() == 4)
    f.Sync(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    f.Close(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    g := Open(io, p).Must()
    g.Abandon()
    // The handles are gone: the file can be replaced and removed.
    WriteAll(io, p, "again".AsBytes()).Must()
    Assert(ReadText(sys.arena, io, p).Must() == "again")
    TfsGone(io, p)
}

// TfsCanc is 1 for Canceled, 2 for any other error.
TfsCanc(e: Error): int {
    return match e {
        Canceled { 1 }
        _ { 2 }
    }
}

// TfsEmpty makes call k of f with nothing to move: 0 Read, 1 Write, 2
// WriteAll, 3 ReadAt, 4 WriteAt, 5 Sync. 0 on success, else TfsCanc.
TfsEmpty(f: mut File, io: Io, k: int): int {
    var empty: [0]u8 = []
    if k == 0 {
        _ = f.Read(io, mut empty[..]) else e {
            return TfsCanc(e)
        }
    } else if k == 1 {
        _ = f.Write(io, empty[..]) else e {
            return TfsCanc(e)
        }
    } else if k == 2 {
        f.WriteAll(io, empty[..]) else e {
            return TfsCanc(e)
        }
    } else if k == 3 {
        _ = f.ReadAt(io, 0, mut empty[..]) else e {
            return TfsCanc(e)
        }
    } else if k == 4 {
        _ = f.WriteAt(io, 0, empty[..]) else e {
            return TfsCanc(e)
        }
    } else {
        f.Sync(io) else e {
            return TfsCanc(e)
        }
    }
    return 0
}

test "a File call with nothing to move is Canceled in a cancelled scope" (sys: &System) {
    p := "mk_tfs_cancelempty"
    io := sys.io
    var f := Create(io, p).Must()
    var got: [6]int = [9; 6]
    var shielded: [6]int = [9; 6]
    var whole := 9
    scope s := io.Scope() {
        s.Cancel()
        for k in 0..6 {
            got[k] = TfsEmpty(mut f, io, k)
            shielded[k] = TfsEmpty(mut f, io.NoCancel(), k)
        }
        WriteAll(io, "mk_tfs_cancelwhole", "".AsBytes()) else e {
            whole = TfsCanc(e)
        }
    }
    for k in 0..6 {
        Assert(got[k] == 1)
        Assert(shielded[k] == 0)
    }
    Assert(whole == 1)
    // Outside the scope the same calls succeed.
    for k in 0..6 {
        Assert(TfsEmpty(mut f, io, k) == 0)
    }
    f.Close(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    TfsGone(io, p)
    TfsGone(io, "mk_tfs_cancelwhole")
}

// File.WriteAll with nothing to write makes only std's one check
// (core.SyncCancel). On an Io over core.InertIo, whose sleep entry fails
// with Other, the check fails with something other than Canceled: not a
// cancellation, so the call succeeds, plain or NoCancel, and in a
// cancelled scope too, which an InertIo does not see.
test "File.WriteAll's check with nothing to write is a cancellation only when it answers Canceled" (sys: &System) {
    p := "mk_tfs_writecheck"
    io := sys.io
    TfsGone(io, p)
    var f := Create(io, p).Must()
    inert := core.MakeIo(raw.Of(&core.InertIo), 0)
    plain := TfsEmpty(mut f, inert, 2)
    masked := TfsEmpty(mut f, inert.NoCancel(), 2)
    var inside := 9
    var native := 9
    scope s := io.Scope() {
        s.Cancel()
        inside = TfsEmpty(mut f, inert, 2)
        native = TfsEmpty(mut f, io, 2)
    }
    Assert(plain == 0)
    Assert(masked == 0)
    Assert(inside == 0)
    Assert(native == 1)
    f.Close(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    TfsGone(io, p)
}

test "File.Writer with no buffer writes each piece at the position at once" (sys: &System) {
    p := "mk_tfs_nobuffer"
    io := sys.io
    var f := Create(io, p).Must()
    Assert(f.Write(io, "01".AsBytes()).Must() == 2)
    var nb: [1]u8 = [0]
    var w := f.Writer(io, mut nb[0..0])
    var ok := true
    w.Str("ab") else {
        ok = false
    }
    // No buffer: the piece is in the file before any Flush.
    Assert(ReadText(sys.arena, io, p).Must() == "01ab")
    var e: [4]u8 = [0; 4]
    w.Bytes(e[0..0]) else {
        ok = false
    }
    w.Str("cdefgh") else {
        ok = false
    }
    w.Flush() else {
        ok = false
    }
    Assert(ok)
    Assert(ReadText(sys.arena, io, p).Must() == "01abcdefgh")
    // The position moved by every byte written: ReadAt is unchanged by it,
    // and Write continues after it.
    var rb: [4]u8 = [0; 4]
    Assert(f.ReadAt(io, 2, mut rb[..]).Must() == 4)
    Assert(rb[0] == 0x61 && rb[3] == 0x64)
    Assert(f.Write(io, "Z".AsBytes()).Must() == 1)
    f.Close(io) else {
        f.Abandon()
        Assert(false)
        return
    }
    Assert(ReadText(sys.arena, io, p).Must() == "01abcdefghZ")
    TfsGone(io, p)
}

// File.Reader, the mirror of File.Writer.

// TfsSame is true when a and b hold the same bytes.
TfsSame(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}
test "File.Reader streams a file through Line, Until and Read" (sys: &System) {
    p := "mk_tfs_349"
    var data: [3000]u8 = [0; 3000]
    for i in 0..3000 {
        data[i] = Trunc[u8](0x61 + i % 23)
        if i % 37 == 36 {
            data[i] = 0x0A
        }
    }
    WriteAll(sys.io, p, data[..]).Must()
    // 81 lines of 36 bytes and a last one of 3 with no newline, through a
    // 64-byte buffer (a buffered Line must fit the buffer).
    var f := Open(sys.io, p).Must()
    var buf: [64]u8 = [0; 64]
    var r := f.Reader(sys.io, mut buf[..])
    var lines := 0
    var at := 0
    for {
        l := r.Line(sys.arena).Must() else {
            break
        }
        Assert(TfsSame(l.AsBytes(), data[at..at + l.Len()]))
        at += l.Len() + 1
        lines += 1
    }
    Assert(lines == 82 && at == 3001)
    f.Close(sys.io).Must()
    // Until with another delimiter, through buffers of several sizes.
    for cap in [0, 1, 7, 64] {
        var g := Open(sys.io, p).Must()
        var r2 := g.Reader(sys.io, mut buf[0..cap])
        var got: [50]u8 = [0; 50]
        var pos := 0
        for {
            n := r2.Until(0x62, mut got[..]).Must()
            if n == 0 {
                break
            }
            Assert(n <= 50 && TfsSame(got[0..n], data[pos..pos + n]))
            Assert(got[n - 1] == 0x62 || n == 50 || pos + n == 3000)
            pos += n
        }
        Assert(pos == 3000)
        g.Close(sys.io).Must()
    }
    TfsGone(sys.io, p)
}

// TfsViaReader reads want bytes (at most 16) through a Reader of f over
// buf, and returns the last of them.
TfsViaReader(f: mut File, io: Io, buf: mut []u8, want: int): ReadError!u8 {
    var r := f.Reader(io, buf)
    var got: [16]u8 = [0; 16]
    n := try r.ReadAll(mut got[0..want])
    Assert(n == want)
    return got[want - 1]
}

test "File.Reader moves the file's position by what it took" (sys: &System) {
    p := "mk_tfs_349b"
    var data: [100]u8 = [0; 100]
    for i in 0..100 {
        data[i] = Trunc[u8](i)
    }
    WriteAll(sys.io, p, data[..]).Must()
    var f := Open(sys.io, p).Must()
    var four: [4]u8 = [0; 4]
    // An unbuffered Reader takes exactly what it returns.
    Assert(TfsViaReader(mut f, sys.io, mut four[0..0], 10).Must() == 9)
    var next: [1]u8 = [0]
    Assert(f.Read(sys.io, mut next[..]).Must() == 1 && next[0] == 10)
    // A buffered one reads ahead by its buffer: 4 bytes from 11.
    Assert(TfsViaReader(mut f, sys.io, mut four[..], 1).Must() == 11)
    Assert(f.Read(sys.io, mut next[..]).Must() == 1 && next[0] == 15)
    f.Close(sys.io).Must()
    TfsGone(sys.io, p)
}
