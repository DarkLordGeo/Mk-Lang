// Why: symbolic links must be made and read the same way on Linux and
// Windows, where they differ in privileges, directory flags and buffers.
//
// What: Symlink and ReadLink, with the buffer-growing helpers GrowBytes and
// GrowText.
//
// How: symlinkat and readlinkat on Linux; CreateSymbolicLinkW (with
// SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE, and the directory flag when
// the target is a directory) and FSCTL_GET_REPARSE_POINT on Windows, as
// Go's os.Symlink and os.Readlink.

import std.core
import std.rt

/// Makes a symbolic link at link that points to target. target is stored
/// as written and need not exist; a relative target resolves from link's
/// directory. Exists when something is at link. Unsupported on Windows
/// without the privilege to make links (Developer Mode or an elevated
/// process). Any other failure is the OS's, as its fs.Error member. One
/// runtime call; allocates nothing.
export Symlink(io: Io, target: str, link: str): Error!void {
    _ = try Check(rt.Symlink(io, target.AsBytes(), link.AsBytes()))
}

/// The target of the symbolic link at p, as stored, in a. Os(code) when p
/// is not a link: EINVAL on Linux, 4390 (ERROR_NOT_A_REPARSE_POINT) on
/// Windows. Utf8Error when the target is not UTF-8; NotFound when nothing
/// is at p; OutOfMemory when a cannot hold the target.
///
/// One runtime call when the target fits in 1 KiB, else one per buffer
/// size, doubling from 4 KiB; allocates the target in a, and for a long
/// target the larger buffers tried too.
export ReadLink(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a {
    return try GrowText(a, io, GrowLink, p.AsBytes())
}

// The rt calls GrowText makes: each writes a path into its out and returns
// the bytes written, out.Len() when the path may be cut.
const GrowLink = 0
const GrowCwd = 1
const GrowAbs = 2

// GrowFirst is GrowText's first buffer, on its frame; GrowMax the largest
// it tries, past any path either target can name (Windows 32,767 UTF-16
// units, at most 98,301 bytes of WTF-8).
const GrowFirst = 1024
const GrowMax = 1 << 20

// GrowText is GrowBytes's answer as a str, Utf8Error when it is not UTF-8.
GrowText(a: &Arena, io: Io, op: int, p: []u8): Error|OutOfMemory!str from a {
    b := try GrowBytes(a, io, op, p)
    return rt.StrFrom(b[..]) else {
        fail Utf8Error
    }
}

// GrowBytes runs the rt call op on p into a frame buffer, then into arena
// buffers doubling from 4 KiB while the answer fills the buffer, and
// returns the answer's bytes in a, exactly its length after a frame-buffer
// answer, at the front of the last buffer tried after a longer one.
GrowBytes(a: &Arena, io: Io, op: int, p: []u8): Error|OutOfMemory!mut []u8 from a {
    var first: [1024]u8 = [0; 1024]
    n := try CheckCount(GrowCall(io, op, p, mut first[..]), GrowFirst)
    if n < GrowFirst {
        out := try a.Slice[u8](n)
        for i in 0..n {
            out[i] = first[i]
        }
        return out
    }
    var size := 4096
    for size <= GrowMax {
        buf := try a.Slice[u8](size)
        m := try CheckCount(GrowCall(io, op, p, buf), size)
        if m < size {
            return mut buf[0..m]
        }
        size *= 2
    }
    fail Os(code: 0)
}

// GrowCall is one call of op into out.
GrowCall(io: Io, op: int, p: []u8, out: mut []u8): core.IoResult {
    if op == GrowLink {
        return rt.ReadLink(io, p, out)
    }
    if op == GrowCwd {
        return rt.Cwd(io, out)
    }
    return rt.Absolute(io, p, out)
}
