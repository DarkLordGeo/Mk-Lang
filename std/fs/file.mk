// Why: an open file must be closed exactly once, and its position must be
// one number that Read, Write and File.Writer all share.
//
// What: File, linear, Send and not Share, with Read, Write, WriteAll,
// ReadAt, WriteAt, Sync, Reader, Writer, Close and Abandon.
//
// How: a runtime handle and a position kept together in a core.FileCursor,
// so File.Writer writes where File.Write would; a zero-size Cell field
// keeps it from being Share. A failed Close leaves the file owned. Every
// operation is one IoTable call (WriteAll a loop of them; Read and Write
// two, once, on a handle with no position) and allocates nothing.

import std.core
import std.rt

/// An open file: an OS handle and the file's position. File is linear:
/// end it with Close, or with Abandon on an error path. It is Send but not
/// Share.
export linear struct File {
    cur: core.FileCursor
    // FileAppend and FileSeeks, or'd.
    flags: u8
    nosh: [0]Cell[u8]
}

// FileAppend: opened under append, so a positioned write would land at the
// end (O_APPEND) and WriteAt refuses it. FileSeeks: a Seek has found the
// handle seekable, so later ones need no call (seek.mk).
const FileAppend: u8 = 1
const FileSeeks: u8 = 2

// NewFile is a File over a fresh runtime handle, at position 0.
NewFile(h: uint): File {
    return File(cur: core.FileCursor(h: h, pos: 0), flags: 0, nosh: [])
}

// AppendFile is a File over a handle opened under append: no position of
// its own, every write at the end.
AppendFile(h: uint): File {
    return File(cur: core.FileCursor(h: h, pos: -1), flags: FileAppend, nosh: [])
}

/// Reads into buf at the file's position, moves the position past the
/// bytes read, and returns their count, 0 at the end of the file. One
/// call, which may read fewer bytes than buf holds. Any failure is the
/// OS's, as its fs.Error member. O(n); allocates nothing.
///
/// A count the runtime reports outside 0..buf.Len() is Os(0), and the
/// position does not move. A file with no position (a pipe, a FIFO or a
/// terminal opened by path) reads at its stream position: when the OS
/// refuses the first positioned read, the read is made again there, and
/// the file reads that way from then on.
export File.Read(mut self, io: Io, buf: mut []u8): Error!int {
    max := buf.Len()
    n := try CheckCount(core.CursorRead(io, mut self.cur, buf), max)
    if self.cur.pos >= 0 {
        self.cur.pos += i64(n)
    }
    return n
}

/// Writes data at the file's position, moves the position past the bytes
/// written, and returns their count. One call, which may write fewer bytes
/// than data holds. NoSpace when the device is full, Canceled in a
/// cancelled scope, Os for any other failure (Os(0) when the runtime
/// reports a count out of range). O(n); allocates nothing.
export File.Write(mut self, io: Io, data: []u8): Error!int {
    n := try CheckCount(core.CursorPut(io, mut self.cur, data), data.Len())
    if self.cur.pos >= 0 {
        self.cur.pos += i64(n)
    }
    return n
}

/// Reads into buf at offset pos and returns the count; the file's
/// position does not move. One call, which may read fewer bytes than buf
/// holds. Any failure is the OS's, as its fs.Error member. O(n); allocates
/// nothing.
///
/// A negative pos is Os(0), with no call made. A handle with no position
/// (a pipe) fails as the OS says: ESPIPE, Os(29), on Linux.
export File.ReadAt(&self, io: Io, pos: i64, buf: mut []u8): Error!int {
    if pos < 0 {
        fail Os(code: 0)
    }
    return try CheckCount(core.IoRead(io, self.cur.h, buf, pos), buf.Len())
}

/// Writes data at offset pos and returns the count; the file's position
/// does not move. One call, which may write fewer bytes than data holds.
/// A negative pos is Os(0), as for ReadAt. Unsupported, with no call made,
/// for a file opened under append, where the OS would put the bytes at the
/// end instead. Any other failure is the OS's. O(n); allocates nothing.
export File.WriteAt(&self, io: Io, pos: i64, data: []u8): Error!int {
    if pos < 0 {
        fail Os(code: 0)
    }
    if (self.flags & FileAppend) != 0 {
        fail Unsupported
    }
    return try CheckCount(core.IoWrite(io, self.cur.h, data, pos), data.Len())
}

/// Writes all of data at the file's position, calling Write until every
/// byte is written. Fails with the first failed write's error. O(n);
/// allocates nothing.
///
/// The position moves past what was written, also when a later write
/// fails. A write that makes no progress is Os(0) rather than an endless
/// loop. An empty data writes nothing, but still fails with Canceled in a
/// cancelled scope.
export File.WriteAll(mut self, io: Io, data: []u8): Error!void {
    if data.Len() == 0 {
        // Nothing to write is still a cancellation point, std's
        // one check (core.SyncCancel): a check that fails with
        // anything other than Canceled is not a cancellation.
        core.SyncCancel(io) else {
            fail Canceled
        }
        return
    }
    var off := 0
    for off < data.Len() {
        n := try self.Write(io, data[off..])
        if n == 0 {
            fail Os(code: 0)
        }
        off += n
    }
}

/// Asks the OS to write the file's data through to its device (fsync).
/// NoSpace when the device cannot take it, Canceled in a cancelled scope,
/// Os for any other failure. O(file size) in the worst case, in the OS;
/// allocates nothing.
export File.Sync(mut self, io: Io): Error!void {
    _ = try Check(core.IoSync(io, self.cur.h))
}

/// A buffered Writer for this file, with buf as its buffer. It writes at
/// the file's position and moves it, as File.Write does. O(1); allocates
/// nothing.
///
/// While the Writer lives it borrows the file, so nothing else moves the
/// position. Bytes still in buf when it is dropped are lost: call Flush
/// first.
export File.Writer(mut self, io: Io, buf: mut []u8): Writer from self, buf {
    return core.NewCursorWriter(io, mut self.cur, buf)
}

/// A buffered Reader for this file, with buf as its buffer. It reads at
/// the file's position and moves it past what it takes, as File.Read does.
/// O(1); allocates nothing.
///
/// It reads ahead into buf, so while it lives the file's position may be
/// past bytes it has not returned yet. It borrows the file, so nothing else
/// reads or moves the position meanwhile. A read failure is reported as
/// ReadError, the Reader's error, without its cause.
export File.Reader(mut self, io: Io, buf: mut []u8): Reader from self, buf {
    return core.NewCursorReader(io, mut self.cur, buf)
}

/// Closes the file. On success the File is consumed. On failure it is
/// still owned, so the caller's `onerr f.Abandon()` runs. The error is the
/// OS's, as its fs.Error member. O(1); allocates nothing.
export File.Close(self, io: Io): Error!void {
    _ = try Check(core.IoClose(io, self.cur.h))
    File(cur: _, flags: _, nosh: _) := self
}

/// Closes the file and ignores every error; it cannot be cancelled. Use it
/// on error paths and for a file whose Close already failed. O(1);
/// allocates nothing.
export File.Abandon(self) {
    rt.Abandon(self.cur.h)
    File(cur: _, flags: _, nosh: _) := self
}

// std's handle seam (core.IoHandle): std.proc's Spawn lends the file's
// handle to a child, which gets a duplicate. Not an export.
impl core.IoHandle for File {
    IoHandle(&self): uint {
        return self.cur.h
    }
}
