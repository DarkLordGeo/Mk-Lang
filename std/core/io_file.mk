// Why: a buffered file Writer or Reader must read and write at the file's own
// position, the same one File.Read and File.Write move, or buffered and
// direct calls would overwrite each other.
//
// What: FileCursor, CursorRead and CursorPut (the cursor calls behind
// File.Read and File.Write), NewCursorWriter and NewCursorReader (behind
// File.Writer and File.Reader), and ReadToEnd, the read loop behind
// fs.ReadBytes.
//
// How: a file's handle and position live in a FileCursor inside fs.File; the
// Writer's and Reader's ctx is the cursor's address, so each buffered
// transfer is a positioned read or write that moves the cursor. A writer at
// the handle's stream position would overwrite from 0 on Linux, where pwrite
// never moves the stream position, and after any WriteAt on Windows, where
// it does. Handles with no position (pipes, consoles) fall back to stream
// reads and writes (NoSeek).

import std.raw

/// A file handle and the position the next File.Read or File.Write uses.
/// Std-internal: fs.File holds one, and only std.fs builds it.
///
/// pos is -1 when the handle has no position (a pipe, FIFO or terminal
/// opened by path); File.Read and File.Write then use the stream position.
export struct FileCursor {
    export h: uint
    export pos: i64
}

// Espipe is ESPIPE, the error POSIX pread and pwrite give on a descriptor
// with no file position (29 on Linux, every architecture, and on macOS).
const Espipe: i64 = 29

// NoSeek: r is a positioned read's or write's failure on a handle that has
// no position. Only POSIX targets report it: Windows ReadFile and WriteFile
// ignore the OVERLAPPED offset of a pipe or a console, so the positioned
// call already works there. Nothing was read or written (pread and pwrite
// check the descriptor first), so the call may be made again at -1.
NoSeek(r: IoResult, pos: i64): bool {
    return pos >= 0 && Target.os != Os.Windows && r.err == SysErr.Other && r.os == Espipe
}

/// Makes one read at c's position, or at the stream position when c has
/// none. Std-internal: the read behind File.Read. When the OS refuses a
/// positioned read because the handle has no position (ESPIPE), the read
/// is made again at the stream position and c.pos becomes -1, so every
/// later read and write of the file goes there at once. The caller checks
/// the count and moves c.pos when it is >= 0. O(1): one or two IoTable
/// calls; it allocates nothing.
export CursorRead(io: Io, c: mut FileCursor, buf: mut []u8): IoResult {
    r := IoRead(io, c.h, mut buf[..], c.pos)
    if !NoSeek(r, c.pos) {
        return r
    }
    c.pos = -1
    return IoRead(io, c.h, buf, -1)
}

/// Makes one write at c's position, as CursorRead makes one read.
/// Std-internal. O(1): one or two IoTable calls; it allocates nothing.
export CursorPut(io: Io, c: mut FileCursor, data: []u8): IoResult {
    r := IoWrite(io, c.h, data, c.pos)
    if !NoSeek(r, c.pos) {
        return r
    }
    c.pos = -1
    return IoWrite(io, c.h, data, -1)
}

const CursorWriteTable = WriterTable(write: CursorWrite, flush: CursorFlush)

/// Returns a Writer over buf for the file behind c, writing at the file's
/// own position: the Writer behind File.Writer. Std-internal. The Writer
/// borrows c as well as buf, so nothing else moves the position while it
/// is alive. Its Flush writes the buffer out and does not sync the device:
/// File.Sync does. O(1); it allocates nothing.
export NewCursorWriter(io: Io, c: mut FileCursor, buf: mut []u8): Writer from c, buf {
    return NewWriter(io, raw.Of(&CursorWriteTable), raw.Addr(raw.OfMut(c)), buf)
}

// Writes every byte of b at the cursor's position (CursorPut: the stream
// position on a handle that has none), looping over partial writes, and
// advances the position by what was written, also when a later piece
// fails. An error, or a write that makes no progress, is WriteError.
CursorWrite(w: mut Writer, b: []u8): WriteError!void {
    io := w.io
    unsafe "a cursor writer's ctx is the live FileCursor its loan covers (NewCursorWriter)" {
        c := raw.PtrCast[u8, FileCursor](w.ctx)
        var off := 0
        for off < b.Len() {
            r := CursorPut(io, mut *c, b[off..])
            if r.err != SysErr.None || r.value <= 0 || r.value > i64(b.Len() - off) {
                fail WriteError
            }
            if (*c).pos >= 0 {
                (*c).pos += r.value
            }
            off += int(r.value)
        }
    }
}

// Flush is a no-op for files: Sync is explicit.
CursorFlush(w: mut Writer): WriteError!void {
}

const CursorReadTable = ReaderTable(read: CursorReaderRead, line: ReadLineBytewise)

/// Returns a Reader over buf for the file behind c, reading at the file's
/// own position: the Reader behind File.Reader. Std-internal. The Reader
/// borrows c as well as buf. O(1); it allocates nothing.
///
/// A read fills buf ahead of the caller, and the position moves by what
/// the Reader took from the file, so afterwards it may be past bytes the
/// caller has not read. With an empty buf every Read goes straight to the
/// file and Line reads one byte per call, so nothing is taken past the
/// newline.
export NewCursorReader(io: Io, c: mut FileCursor, buf: mut []u8): Reader from c, buf {
    return NewReader(io, raw.Of(&CursorReadTable), raw.Addr(raw.OfMut(c)), buf)
}

// One read at the cursor's position (CursorRead: the stream position on a
// handle that has none), and the position moves by the count. An error or
// a count outside 0..buf.Len() is ReadError, and the position stays.
CursorReaderRead(r: mut Reader, buf: mut []u8): ReadError!int {
    io := r.io
    unsafe "a cursor reader's ctx is the live FileCursor its loan covers (NewCursorReader)" {
        c := raw.PtrCast[u8, FileCursor](r.ctx)
        res := CursorRead(io, mut *c, buf)
        if res.err != SysErr.None || res.value < 0 || res.value > i64(buf.Len()) {
            fail ReadError
        }
        if (*c).pos >= 0 {
            (*c).pos += res.value
        }
        return int(res.value)
    }
}

// SpillMax bounds ReadToEnd's spill segments: they double from at least 2
// bytes, so 62 of them hold more bytes than an int can count.
const SpillMax = 62

/// Reads handle h at its stream position until a read returns 0, and
/// returns every byte read as one slice of a of exactly that length: the
/// read loop behind fs.ReadBytes. Std-internal. first, the size of the
/// first buffer, must be at least 1. O(n) time; it allocates the n bytes
/// in a, plus at most log2(n / first) + 2 heap blocks.
///
/// Fails with ReadError on a read failure or a count outside 0..len, with
/// the read's result in bad for the caller to map; with OutOfMemory when
/// a's heap is out, or for more bytes than an int can count.
///
/// How the memory is used. An arena cannot free a buffer it outgrows, so
/// growth by doubling would leave about 4n bytes of a behind. Instead:
/// - While the bytes fit the first buffer, nothing else happens: a file
///   whose size hint is right costs a buffer of size + 1 and two reads,
///   and the spare byte goes back to a.
/// - When the buffer fills, it doubles in place if it is a's last block
///   and its chunk has room. Otherwise the rest goes to segments in a
///   sub-arena of a (same heap), doubling from the first buffer's size and
///   filled in order with no copy.
/// - At the end of input, one block of a gets the n bytes (the first
///   buffer grown in place when it can), the segments are copied in once,
///   and the sub-arena is freed. Every path frees the sub-arena.
/// So a holds the n bytes of the result, plus the first buffer (at most
/// first bytes) when a could neither grow it nor give it back. Each byte is
/// copied at most once, nothing is zero-filled, and the peak is at most
/// 3n + first bytes (n in a, the segments at most 2n).
export ReadToEnd(a: &Arena, io: Io, h: uint, first: int, bad: mut IoResult): OutOfMemory|ReadError![]u8 from a {
    al := a.Alloc()
    p := AllocBytes(al, first, 1)
    if raw.IsNull(p) {
        fail OutOfMemory
    }
    var cap := first
    var got := 0
    for {
        if got == cap {
            if cap > MaxBytes / 2 || !ResizeBytes(al, p, cap, cap * 2, 1) {
                break
            }
            cap = cap * 2
        }
        n := try ReadSome(io, h, ByteViewMut(ByteAt(p, got), cap - got), bad)
        if n == 0 {
            _ = ResizeBytes(al, p, cap, got, 1)
            return ByteView(p, got)
        }
        got += n
    }
    // The first buffer is full and cannot grow in place: spill.
    var sub := try a.Sub()
    var segs: [62]*u8 = [raw.Null[u8](); 62]
    var lens: [62]int = [0; 62]
    var k := 0
    var size := cap
    var total := got
    var done := false
    for !done {
        if k == SpillMax || size > MaxBytes / 2 || total > MaxBytes - size * 2 {
            sub.Free()
            fail OutOfMemory
        }
        size = size * 2
        s := AllocBytes(sub.Alloc(), size, 1)
        if raw.IsNull(s) {
            sub.Free()
            fail OutOfMemory
        }
        var fill := 0
        for fill < size {
            n := ReadSome(io, h, ByteViewMut(ByteAt(s, fill), size - fill), bad) else {
                sub.Free()
                fail ReadError
            }
            if n == 0 {
                done = true
                break
            }
            fill += n
        }
        segs[k] = s
        lens[k] = fill
        k += 1
        total += fill
    }
    q := GrowBytes(al, p, got, cap, total, true)
    if raw.IsNull(q) {
        sub.Free()
        fail OutOfMemory
    }
    var at := got
    for i in 0..k {
        CopyBytes(ByteAt(q, at), segs[i], lens[i])
        at += lens[i]
    }
    sub.Free()
    return ByteView(q, total)
}

// ReadSome is one read at the stream position into buf; a failure, or a
// count outside 0..buf.Len(), is ReadError with the result in bad.
ReadSome(io: Io, h: uint, buf: mut []u8, bad: mut IoResult): ReadError!int {
    max := buf.Len()
    r := IoRead(io, h, buf, -1)
    if r.err != SysErr.None || r.value < 0 || r.value > i64(max) {
        bad.value = r.value
        bad.err = r.err
        bad.os = r.os
        fail ReadError
    }
    return int(r.value)
}
