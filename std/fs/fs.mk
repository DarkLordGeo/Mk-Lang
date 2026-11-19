// Why: most programs only open, read, write, rename and remove whole files,
// and those calls should be one line each.
//
// What: Open, Create, ReadBytes, ReadText, WriteAll, Rename and Remove, and
// Check, which maps a runtime error to fs.Error.
//
// How: every operation is IoTable's, reached as core.IoOpen and the like,
// and a SysErr maps to the fs.Error member of the same name, anything else
// to Os(code).

import std.core
import std.rt

// Check is r's value, or the fs.Error its SysErr maps to.
Check(r: core.IoResult): Error!i64 {
    match r.err {
        .None { return r.value }
        .NotFound { fail NotFound }
        .Exists { fail Exists }
        .Denied { fail Denied }
        .IsDirectory { fail IsDirectory }
        .NotDirectory { fail NotDirectory }
        .NoSpace { fail NoSpace }
        .InvalidPath { fail InvalidPath }
        .Canceled { fail Canceled }
        .Unsupported { fail Unsupported }
        _ { fail Os(code: r.os) }
    }
}

// CheckCount is Check for a read or a write of at most max bytes. A count
// below 0 or above max breaks the runtime's contract and is
// Os(code: 0), as Go's io.Copy reports errInvalidWrite, so no caller moves
// a position by it or slices past its buffer with it.
CheckCount(r: core.IoResult, max: int): Error!int {
    n := try Check(r)
    if n < 0 || n > i64(max) {
        fail Os(code: 0)
    }
    return int(n)
}

// OpenHandle opens p with IoTable.open's mode: 0 read-only, 1 create or
// truncate for reading and writing.
OpenHandle(io: Io, p: str, mode: u32): Error!uint {
    h := try Check(core.IoOpen(io, p, mode))
    return Trunc[uint](h)
}

// ReadFirst is ReadBytes's first buffer when the size is unknown (0: a
// pipe, a character device, a procfs file): one page, so a small stream
// takes one read and the end-of-input read, not a doubling from one byte.
const ReadFirst = 4096

/// Reads the whole file at p into one block of a and returns it. Fails
/// with the fs.Error of the open, size query or read that failed, or
/// OutOfMemory. O(n); allocates the n bytes read plus at most one first
/// buffer in a.
///
/// The first buffer is the file's size plus one byte (4 KiB when the size
/// is 0), so a file whose size is right costs two reads and one buffer,
/// and the spare byte goes back to a. A file that grows while it is read,
/// or that reports no size (a pipe, a FIFO, a character device, a procfs
/// file), continues into a spill buffer that is copied once into a block
/// of exactly the bytes read. Nothing is zero-filled, and the peak is at
/// most 3n plus the first buffer. Reads start at the beginning of the
/// stream, so pipes and devices read too. The file is closed on every
/// path; an error from that close is not reported, because every byte is
/// already read.
export ReadBytes(a: &Arena, io: Io, p: str): Error|OutOfMemory![]u8 from a {
    h := try OpenHandle(io, p, 0)
    defer rt.Abandon(h)
    size := try Check(core.IoSize(io, h))
    first := if size > 0 && size < 9_223_372_036_854_775_807 { int(size) + 1 } else { ReadFirst }
    var bad := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    return core.ReadToEnd(a, io, h, first, mut bad) else e {
        match e {
            OutOfMemory {
                fail OutOfMemory
            }
            ReadError {
                // A failed read is its SysErr's member; a count outside
                // 0..len is Os(0) (CheckCount's rule).
                _ = try Check(bad)
                fail Os(code: 0)
            }
        }
    }
}

/// Reads the whole file at p into a as text: ReadBytes, then a check that
/// the bytes are UTF-8. Text that is not UTF-8 fails with the fs.Error
/// member Utf8Error. O(n); allocates as ReadBytes.
///
/// The UTF-8 check is one pass over the bytes, using SIMD where the CPU
/// has it.
export ReadText(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a {
    b := try ReadBytes(a, io, p)
    return rt.StrFrom(b) else {
        fail Utf8Error
    }
}

/// Writes data to the file at p, creating it or truncating it first, then
/// closes it. It does not sync the data to the device: open the file with
/// Create and call File.Sync for that. Fails with the fs.Error of the open,
/// a write or the close; the file is closed on every path. O(n); allocates
/// nothing.
///
/// Writes start at the beginning of the stream, so FIFOs and devices work
/// too, and a partial write is continued until every byte is written. A
/// write that makes no progress is Os(0) rather than an endless loop.
export WriteAll(io: Io, p: str, data: []u8): Error!void {
    h := try OpenHandle(io, p, 1)
    var off := 0
    for off < data.Len() {
        n := CheckCount(core.IoWrite(io, h, data[off..], -1), data.Len() - off) else e {
            rt.Abandon(h)
            fail e
        }
        if n == 0 {
            rt.Abandon(h)
            fail Os(code: 0)
        }
        off += n
    }
    _ = Check(core.IoClose(io, h)) else e {
        rt.Abandon(h)
        fail e
    }
}

/// Opens the file at p for reading, at position 0. NotFound when nothing is
/// at p; any other failure is the OS's, as its fs.Error member (Denied,
/// ...). One runtime call; allocates nothing.
///
/// A directory opens too, on both targets, and File.Read on it fails with
/// IsDirectory. A File.Reader over it reports that as ReadError, so a
/// caller that wants the cause reads once with File.Read or calls Stat
/// first.
export Open(io: Io, p: str): Error!File {
    h := try OpenHandle(io, p, 0)
    return NewFile(h)
}

/// Creates the file at p, or truncates the file already there, and opens
/// it for reading and writing at position 0. NotFound when the parent
/// directory is missing; any other failure is the OS's, as its fs.Error
/// member. One runtime call; allocates nothing.
export Create(io: Io, p: str): Error!File {
    h := try OpenHandle(io, p, 1)
    return NewFile(h)
}

/// Renames src to dst, replacing the file already at dst. NotFound when
/// src is missing; any other failure is the OS's, as its fs.Error member.
/// One runtime call; allocates nothing.
export Rename(io: Io, src: str, dst: str): Error!void {
    _ = try Check(core.IoRename(io, src, dst))
}

/// Removes the file or empty directory at p. NotFound when nothing is at
/// p; any other failure is the OS's, as its fs.Error member. One runtime
/// call; allocates nothing.
export Remove(io: Io, p: str): Error!void {
    _ = try Check(core.IoRemove(io, p))
}
