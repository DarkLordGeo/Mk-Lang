// Why: copying a file through user space is slow and loses what the file
// system can do itself (reflinks, server-side copies, alternate streams).
//
// What: Copy, with the fallbacks CopyOut and CopyThrough.
//
// How: the kernel's own copy, as Rust's std::fs::copy and Go's io.Copy over
// *os.File reach it. Linux: copy_file_range in 1 GiB requests, falling back
// to a read and write loop through 64 KiB on EXDEV, ENOSYS, EINVAL,
// EOPNOTSUPP or EPERM; Windows: CopyFileExW, which copies the data, the
// attributes and the alternate streams. Both run on the pool, and a cancel
// ends them at the next chunk (std.rt's CopyFile), keeping what was copied.
// src is checked to be a regular file first, as Rust's fs::copy checks it,
// so a directory or a FIFO never truncates dst. An Io whose call entry has
// no copy (std.sim, a Tape) is served by a read and write loop through File.

import std.core
import std.rt

/// Copies the file src to dst, creating or replacing dst, and returns the
/// number of bytes copied. The OS copies the data itself where it can:
/// copy_file_range on Linux, with a read and write loop where the file
/// system refuses it, and CopyFileExW on Windows, which also copies the
/// attributes and the alternate streams.
///
/// NotFound when src or dst's parent is missing. IsDirectory when src is a
/// directory, and Unsupported when it is not a regular file (a FIFO, a
/// device), both before dst is touched. Denied, before anything is
/// truncated, when src and dst name one file by any path or link (under an
/// Io with no copy call only the same path is caught). Canceled before the
/// copy starts or while it runs; what it copied stays copied. Any other
/// failure is the OS's, as its fs.Error member.
///
/// O(size), in the kernel: a stat and one runtime call; allocates nothing
/// (16 KiB of stack under an Io with no copy call).
///
/// A failure before dst is opened (src's own failure, the kind check,
/// Denied for one file, Canceled before the start, or dst's open) leaves
/// dst as it was. After that, dst's earlier bytes are gone. On Linux, and
/// under an Io with no copy call, dst holds a prefix of src, possibly
/// empty: dst is truncated first, and nothing is removed.
///
/// On Windows, CopyFileExW may already have made dst src's size (as seen
/// for a lock violation), and on the failure it deletes dst, even a dst
/// that existed before. With no other handle open on dst, dst is then
/// absent. While a handle that shares delete is open (std's own Open
/// does), dst is pending deletion: every call on it, Remove too, is
/// Denied, and it is absent once that handle closes. If a handle that does
/// not share delete was open (Go's os.Open, for one), the delete fails, and
/// dst stays at src's size, a prefix of src then zeros, even after that
/// handle closes.
///
/// A caller that wants nothing left calls Remove and takes NotFound as
/// done; Denied means another handle still holds dst, so Remove is called
/// again once that handle has closed. A caller that must keep an existing
/// dst copies to a new name and renames it over dst.
///
/// Rarer cases: on Linux an fstat or ftruncate failure right after dst is
/// opened leaves an existing dst whole, and on Windows a failed attribute
/// read after the copy returns its error with dst fully written.
export Copy(io: Io, src: str, dst: str): Error!i64 {
    st := try Stat(io, src)
    if st.kind == Kind.Dir {
        fail IsDirectory
    }
    if st.kind != Kind.File {
        fail Unsupported
    }
    r := rt.CopyFile(io, src.AsBytes(), dst.AsBytes())
    if r.err == core.SysErr.Unsupported {
        return try CopyThrough(io, src, dst)
    }
    return try Check(r)
}

// CopyOut is how CopyThrough opens dst: created or truncated, for writing.
const CopyOut: Options = Options(read: false, write: true, append: false, create: true, truncate: true, exclusive: false, permissions: Permissions(mode: 0o666), sealed: 0)

// CopyThrough copies src to dst by reads and writes through Files, for an
// Io whose call entry has no copy. The same path twice is Denied before dst
// is opened (the same-file rule as far as a path shows it). 16 KiB a read, on
// the frame; both files are released on every path.
CopyThrough(io: Io, src: str, dst: str): Error!i64 {
    if src == dst {
        fail Denied
    }
    var from := try Open(io, src)
    var to := OpenWith(io, dst, CopyOut) else e {
        from.Abandon()
        fail e
    }
    var buf: [16384]u8 = [0; 16384]
    var total: i64 = 0
    for {
        n := from.Read(io, mut buf[..]) else e {
            from.Abandon()
            to.Abandon()
            fail e
        }
        if n == 0 {
            break
        }
        to.WriteAll(io, buf[0..n]) else e {
            from.Abandon()
            to.Abandon()
            fail e
        }
        total += i64(n)
    }
    from.Abandon()
    to.Close(io) else e {
        to.Abandon()
        fail e
    }
    return total
}
