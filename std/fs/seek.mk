// Why: random access needs Seek, Size and Truncate, and seeking should not
// cost a system call when the file already knows its position.
//
// What: File.Seek, File.Size, File.Truncate and Whence.
//
// How: a File keeps its own position in its cursor (file.mk), which Read,
// Write, Writer and Reader use, so Seek on a file with a position moves
// only that number: no system call except End's size, once the first Seek
// has asked the OS whether the handle seeks at all (Go's Seek asks every
// time). A file with no position (opened under append, or a pipe or a
// terminal opened by path) uses the OS's, which Seek moves through std.rt.

import std.core
import std.rt

/// Where File.Seek counts from.
export enum Whence {
    /// The start of the file.
    Start
    /// The file's position.
    Current
    /// The end of the file.
    End
}

/// Moves the file's position to pos counted from from, and returns the new
/// position. The position may be past the end of the file; a write there
/// fills the gap with zeros.
///
/// Os(0) when the result would be before 0 or past the largest i64; the
/// position is unchanged. Os(29) (ESPIPE, on Linux) for a pipe, or a FIFO
/// that the OS seeks (one served by the blocking pool). Any other failure
/// is the OS's.
///
/// O(1). The first Seek of a file asks the runtime once whether the handle
/// seeks. After that, Start and Current make no call and End makes one size
/// query; a file under append, or a pipe, makes one OS seek each time.
/// It allocates nothing.
export File.Seek(mut self, io: Io, pos: i64, from: Whence): Error!i64 {
    whence: u32 = match from {
        .Start { 0 }
        .Current { 1 }
        .End { 2 }
    }
    if self.cur.pos < 0 {
        return try Check(rt.FileSeek(io, self.cur.h, pos, whence))
    }
    if (self.flags & FileSeeks) == 0 {
        // A pipe or a FIFO opened by path has a position until a
        // positioned read is refused (file.mk); std.rt says which it is.
        // A handle on the poller answers from its record today, so a FIFO
        // there is not found yet.
        probe := rt.FileSeek(io, self.cur.h, 0, 1)
        if probe.err != core.SysErr.Unsupported {
            _ = try Check(probe)
        }
        self.flags = self.flags | FileSeeks
    }
    base := match from {
        .Start { i64(0) }
        .Current { self.cur.pos }
        .End { try Check(core.IoSize(io, self.cur.h)) }
    }
    at := base +% pos
    // Signed overflow of base + pos, then a result before 0.
    if ((base ^ at) & (pos ^ at)) < 0 || at < 0 {
        fail Os(code: 0)
    }
    self.cur.pos = at
    return at
}

/// The file's size in bytes, read from the open handle (fstat on Linux,
/// GetFileSizeEx on Windows). Any failure is the OS's, as its fs.Error
/// member. One runtime call; allocates nothing.
export File.Size(&self, io: Io): Error!i64 {
    return try Check(core.IoSize(io, self.cur.h))
}

/// Sets the file's size: bytes past size are dropped, and a larger size
/// extends the file with zeros. The position does not move. Os(0) for a
/// negative size; any other failure is the OS's. One runtime call
/// (ftruncate on Linux, SetFileInformationByHandle on Windows); allocates
/// nothing.
///
/// On Windows a file opened under append is Denied, and a file with a live
/// Mapping cannot shrink (Os(1224)).
export File.Truncate(mut self, io: Io, size: i64): Error!void {
    if size < 0 {
        fail Os(code: 0)
    }
    _ = try Check(rt.FileTruncate(io, self.cur.h, size))
}
