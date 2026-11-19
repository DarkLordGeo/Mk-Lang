// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: a stub of each export of std/sys/linux/call.mk (Seek, FileLock,
// MapFile, StatEx, CopyFile, SocketOption, TermSize, TimeZone, OnNinep and
// the rest), and NotHere.
//
// How: each answers Unsupported through NotHere; the older exports in sys.mk
// keep answering Other (Unsupported() there).

import std.core

// NotHere is the answer of every export here.
NotHere(): OsResult {
    return OsResult(value: 0, err: core.SysErr.Unsupported, os: 0)
}

/// Seek has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export Seek(h: uint, off: i64, whence: u32): OsResult {
    return NotHere()
}

/// FileLock has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export FileLock(h: uint, exclusive: bool, once: bool): OsResult {
    return NotHere()
}

/// FileUnlock has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export FileUnlock(h: uint): OsResult {
    return NotHere()
}

/// MapFile has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export MapFile(h: uint, off: i64, len: int, w: mut core.IoWords): OsResult {
    return NotHere()
}

/// UnmapFile has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export UnmapFile(base: uint, len: int): OsResult {
    return NotHere()
}

/// StatEx has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export StatEx(p: []u8, nofollow: bool, w: mut core.IoWords): OsResult {
    return NotHere()
}

/// SetModified has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export SetModified(p: []u8, nanos: i64): OsResult {
    return NotHere()
}

/// SetMode has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export SetMode(p: []u8, mode: u32): OsResult {
    return NotHere()
}

/// Symlink has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export Symlink(target: []u8, link: []u8): OsResult {
    return NotHere()
}

/// ReadLink has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export ReadLink(p: []u8, out: mut []u8): OsResult {
    return NotHere()
}

/// Cwd has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export Cwd(out: mut []u8): OsResult {
    return NotHere()
}

/// Absolute has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export Absolute(p: []u8, out: mut []u8): OsResult {
    return NotHere()
}

/// CopyFile has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export CopyFile(src: []u8, dst: []u8, stop: *u32): OsResult {
    return NotHere()
}

/// SocketOption has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export SocketOption(h: uint, opt: i64, b: i64, c: i64): OsResult {
    return NotHere()
}

/// SocketOptionGet has std/sys/linux's contract; this placeholder
/// answers Unsupported. O(1); allocates nothing.
export SocketOptionGet(h: uint, opt: i64): OsResult {
    return NotHere()
}

/// SocketPeer has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export SocketPeer(h: uint, a: mut SockAddr): OsResult {
    return NotHere()
}

/// TermSize has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export TermSize(h: uint, w: mut core.IoWords): OsResult {
    return NotHere()
}

/// TimeZone has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export TimeZone(year: i64, out: mut []u8): OsResult {
    return NotHere()
}

/// IsTerminal has std/sys/linux's contract; this placeholder has no
/// terminal. O(1); allocates nothing.
export IsTerminal(h: uint): bool {
    return false
}

/// OnNinep has std/sys/linux's contract; this placeholder has no 9p mount.
/// O(1); allocates nothing.
export OnNinep(h: uint): bool {
    return false
}
