// Why: programs need the working directory and absolute forms of the paths
// they are given, the same way on Linux and Windows.
//
// What: Cwd and Absolute, with Clean, the lexical path cleaner.
//
// How: as Go's os.Getwd and filepath.Abs: getcwd on Linux and
// GetCurrentDirectoryW on Windows; an absolute path is the working
// directory joined with a relative one on Linux and GetFullPathNameW's
// answer on Windows, then cleaned lexically by Clean, Go's filepath.Clean
// (Rob Pike's "Lexical File Names in Plan 9"). Neither resolves links.
// Under a simulated Io, Absolute takes the Linux form on every target, so a
// seed gives one run.

import std.rt

/// The working directory, in a. Utf8Error when its bytes are not UTF-8;
/// OutOfMemory when a cannot hold it; any other failure is the OS's. One
/// runtime call when the path fits in 1 KiB, else one per buffer size,
/// doubling from 4 KiB; allocates the path in a, and for a long path the
/// larger buffers tried too.
export Cwd(a: &Arena, io: Io): Error|OutOfMemory!str from a {
    return try GrowText(a, io, GrowCwd, "".AsBytes())
}

/// p as an absolute, clean path, in a. On Linux it is p itself when p
/// starts with `/`, else the working directory joined with p. On Windows it
/// is the system's full path of p (for `C:x`, joined to the working
/// directory of drive C:). An empty p is the working directory.
///
/// The path is then cleaned from its text alone: `.` and `..` elements,
/// repeated separators and a trailing separator are removed, and `..`
/// never climbs above the root. Links are not resolved, so `..` after a
/// link names the link's parent.
///
/// Errors as Cwd. O(len) plus at most one runtime call; allocates the
/// result in a (on Linux, for a relative p, also the working directory
/// and the joined path).
///
/// Under a simulated Io (a Sim's or a Tape's) it answers as on Linux on
/// every target, so a seed gives one run: a p that starts with `/` is
/// cleaned with `/`, and any other p asks for the working directory, which
/// such an Io does not have (Unsupported). A Recorder over the native Io on
/// Windows counts as simulated here, though its file calls reach the real
/// file system: `/x` answers `/x`, not the drive's `C:\x`, and `\` is no
/// separator, so `/a\..\b` stays as written while the real file system
/// resolves it to `\b`. That answer is unfit for a containment check.
export Absolute(a: &Arena, io: Io, p: str): Error|OutOfMemory!str from a {
    // win is the Windows form (PathWin, temp.mk): the system's answer and
    // its separators. SimBits is a Sim's and a Tape's token. A Sim's file
    // system has one tree of `/` paths whatever the target (std.sim's
    // path.mk), and neither answers IoOpAbsolute. A Recorder over the native Io forwards
    // its file calls to the real file system, so there the Linux form is
    // neither drive-qualified nor cleaned at `\` (see Absolute's doc).
    win := PathWin(io)
    b := if win {
        q := if p.Len() == 0 { "." } else { p }
        try GrowBytes(a, io, GrowAbs, q.AsBytes())
    } else if p.Len() > 0 && p.AsBytes()[0] == 0x2F {
        try Joined(a, "".AsBytes(), p)
    } else {
        wd := try GrowBytes(a, io, GrowCwd, "".AsBytes())
        try Joined(a, wd[..], p)
    }
    n := Clean(b, win)
    return rt.StrFrom(b[0..n]) else {
        fail Utf8Error
    }
}

// Joined is dir, a `/` and p in a fresh block of a; p alone when dir is
// empty.
Joined(a: &Arena, dir: []u8, p: str): OutOfMemory!mut []u8 from a {
    sep := if dir.Len() > 0 { 1 } else { 0 }
    b := try a.Slice[u8](dir.Len() + sep + p.Len())
    Put(b, 0, dir)
    if sep == 1 {
        b[dir.Len()] = 0x2F
    }
    Put(b, dir.Len() + sep, p.AsBytes())
    return b
}

// Put copies src into dst from at.
Put(dst: mut []u8, at: int, src: []u8) {
    for i in 0..src.Len() {
        dst[at + i] = src[i]
    }
}

// IsSep is a separator byte: `/`, and `\` on Windows.
IsSep(c: u8, win: bool): bool {
    return c == 0x2F || (win && c == 0x5C)
}

// VolumeLen is the length of b's volume name on Windows: `X:` or a UNC
// `\host\share` (`\?\C:` and `\.\device` read as a UNC host and share,
// as Go's volumeNameLen reads them); 0 on Linux.
VolumeLen(b: []u8, win: bool): int {
    if !win {
        return 0
    }
    if b.Len() >= 2 && b[1] == 0x3A {
        return 2
    }
    if b.Len() < 2 || !IsSep(b[0], true) || !IsSep(b[1], true) {
        return 0
    }
    var i := 2
    for part in 0..2 {
        for i < b.Len() && !IsSep(b[i], true) {
            i += 1
        }
        if part == 0 && i < b.Len() {
            i += 1
        }
    }
    return i
}

// Clean rewrites b in place as Go's filepath.Clean does and returns the
// cleaned length: the volume name kept (separators made `\` on Windows),
// then, element by element left to right, empty and `.` elements dropped
// and `..` removing the element before it (at the root it is dropped;
// in a relative path with nothing to remove it stays). The write index
// never passes the read index, so the pass is in place. O(len); allocates
// nothing.
Clean(b: mut []u8, win: bool): int {
    n := b.Len()
    sep: u8 = if win { 0x5C } else { 0x2F }
    vol := VolumeLen(b[..], win)
    for i in 0..vol {
        if IsSep(b[i], win) {
            b[i] = sep
        }
    }
    rooted := vol < n && IsSep(b[vol], win)
    var r := vol
    var w := vol
    if rooted {
        b[w] = sep
        r += 1
        w += 1
    }
    // dotdot is where a `..` may no longer back up: the root, or the end
    // of the last `..` a relative path kept.
    var dotdot := w
    for r < n {
        if IsSep(b[r], win) {
            r += 1
        } else if b[r] == 0x2E && (r + 1 == n || IsSep(b[r + 1], win)) {
            r += 1
        } else if b[r] == 0x2E && r + 1 < n && b[r + 1] == 0x2E && (r + 2 == n || IsSep(b[r + 2], win)) {
            r += 2
            if w > dotdot {
                w -= 1
                for w > dotdot && !IsSep(b[w], win) {
                    w -= 1
                }
            } else if !rooted {
                if w > vol {
                    b[w] = sep
                    w += 1
                }
                b[w] = 0x2E
                b[w + 1] = 0x2E
                w += 2
                dotdot = w
            }
        } else {
            if (rooted && w != vol + 1) || (!rooted && w != vol) {
                b[w] = sep
                w += 1
            }
            for r < n && !IsSep(b[r], win) {
                b[w] = b[r]
                w += 1
                r += 1
            }
        }
    }
    if w == vol && w < n {
        // An empty path after the volume is `.`, as Go writes it. A bare
        // volume (n == vol) has no byte to write it into and stays as is;
        // Absolute never cleans one.
        b[w] = 0x2E
        w += 1
    }
    return w
}
