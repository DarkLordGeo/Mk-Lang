// Why: std.dl loads shared libraries at run time, and on macOS the loader,
// dyld, is reached only through libSystem's dlopen family.
//
// What: DlInvalid and DlInitFailed (the same os codes as on Windows),
// DlOpen, the symbol lookup and close behind std.dl.
//
// How: dlopen(name, RTLD_NOW | RTLD_LOCAL) (Apple's <dlfcn.h>: RTLD_NOW 2,
// RTLD_LOCAL 4), dlsym and dlclose, called on the calling task's stack;
// dlopen blocks the carrier and is not cancellable. Every macOS program
// links libSystem, so loading is always available.

import std.core
import std.raw
import std.sys.lc

/// DlInvalid has std/sys/windows' meaning: the os code of an Other failure
/// that std.dl reports as Invalid. The same value on every target.
export const DlInvalid: i64 = 193

/// DlInitFailed has std/sys/windows' meaning: the os code of an Other
/// failure that std.dl reports as InitFailed. The same value on every
/// target.
export const DlInitFailed: i64 = 1114

// DlNameUnits is the longest library name, in UTF-16 units, as on Windows;
// such a name is at most 3,072 UTF-8 bytes.
const DlNameUnits: int = 1024
const DlNameBytes: int = 3072

// DlSymbolBytes is the longest symbol name in bytes; the buffer holds one
// more for the NUL.
const DlSymbolBytes: int = 255

// DlExeBytes is the room for the executable's path (Darwin's PATH_MAX, 1024,
// with slack for a longer one _NSGetExecutablePath may give).
const DlExeBytes: int = 4096
// DlPathBytes is the buffer of the name dlopen gets: the executable's
// directory, a '/', the name and the NUL.
const DlPathBytes: int = DlExeBytes + DlNameBytes + 2

// DlRtldNowLocal is RTLD_NOW | RTLD_LOCAL.
const DlRtldNowLocal: i32 = 6

// DlEnoent and DlEnotdir are the stat errors that mean the file is absent.
const DlEnoent: i64 = 2
const DlEnotdir: i64 = 20

/// DlOpen has std/sys/windows' contract. search 0 passes a bare name to
/// dlopen, which dyld looks up in its own order for a leaf name (the
/// DYLD_ environment variables honoured, then its fallback directories),
/// 1 the directory of the executable (_NSGetExecutablePath),
/// a '/' and the bare name, 2 an absolute path. InvalidPath for a name of
/// the wrong form (empty, holding a NUL, over 1,024 UTF-16 units, a '/'
/// under 0 or 1, not absolute under 2). A failed dlopen is NotFound under
/// 0, and under 1 and 2 when stat finds no file; otherwise Other with
/// DlInvalid. Runs the library's initializers on the calling stack;
/// blocking, not cancellable.
/// O(name) plus the loader's work; allocates nothing of its own.
export DlOpen(name: str, search: int): OsResult {
    b := name.AsBytes()
    if search < 0 || search > 2 || !DlNameForm(b, search == 2) {
        return DlBadName()
    }
    var buf: [DlPathBytes]u8 = [0; DlPathBytes]
    var n := 0
    if search == 1 {
        n = ExePath(mut buf[..DlExeBytes])
        for n > 0 && buf[n - 1] != b'/' {
            n -= 1
        }
        if n == 0 {
            return OsResult(value: 0, err: core.SysErr.NotFound, os: 0)
        }
    }
    for c in b {
        buf[n] = c
        n += 1
    }
    buf[n] = 0
    unsafe "buf is NUL-terminated and lives across the call" {
        h := lc.DynOpen(raw.Data(buf[..]), DlRtldNowLocal)
        if !raw.IsNull(h) {
            return OsResult(value: Trunc[i64](raw.Addr(h)), err: core.SysErr.None, os: 0)
        }
        _ = dlerror() // dlopen's message names no cause std.dl reports; this clears it
    }
    if search == 0 || DlAbsent(buf[..n + 1]) {
        return OsResult(value: 0, err: core.SysErr.NotFound, os: 0)
    }
    return OsResult(value: 0, err: core.SysErr.Other, os: DlInvalid)
}

// DlBadName is DlOpen's answer for a name of the wrong form.
DlBadName(): OsResult {
    return OsResult(value: 0, err: core.SysErr.InvalidPath, os: 0)
}

// DlNameForm reports whether b has the form its search needs: not empty,
// no NUL, at most DlNameUnits UTF-16 units (a 4-byte UTF-8 sequence is
// two), and no '/' in a bare name or a leading '/' in a path.
DlNameForm(b: []u8, path: bool): bool {
    if b.Len() == 0 || b.Len() > DlNameBytes {
        return false
    }
    var units := 0
    for c in b {
        if c == 0 || !path && c == b'/' {
            return false
        }
        if c & 0xC0 != 0x80 {
            units += if c >= 0xF0 { 2 } else { 1 }
        }
    }
    return units <= DlNameUnits && (!path || b[0] == b'/')
}

// DlAbsent reports whether stat finds no file at the NUL-terminated path c
// (ENOENT or ENOTDIR).
DlAbsent(c: []u8): bool {
    var st: [18]u64 = [0; 18]
    if PathStat(c, false, mut st) == 0 {
        return false
    }
    e := Errno()
    return e == DlEnoent || e == DlEnotdir
}

/// DlSymbol has std/sys/windows' contract: dlsym's address for the name in
/// the library h, or none, also for a name that is empty, over 255 bytes or
/// holds a NUL. The name is the C name, without the leading underscore
/// Mach-O adds (dlsym adds it).
/// O(name) plus the lookup; allocates nothing.
export DlSymbol(h: uint, name: str): ?*u8 {
    b := name.AsBytes()
    if b.Len() == 0 || b.Len() > DlSymbolBytes {
        return none
    }
    var buf: [DlSymbolBytes + 1]u8 = [0; DlSymbolBytes + 1]
    for i in 0..b.Len() {
        if b[i] == 0 {
            return none
        }
        buf[i] = b[i]
    }
    unsafe "h is a handle dlopen gave and buf is NUL-terminated" {
        p := dlsym(raw.FromAddr[u8](h), raw.Data(buf[..]))
        if raw.IsNull(p) {
            _ = dlerror() // clears the thread's message
            return none
        }
        return some(p)
    }
}

/// DlClose has std/sys/windows' contract: dlclose on h; a failure leaves the
/// library loaded, which is harmless, so nothing is reported.
/// O(1) plus the loader's work; allocates nothing.
export DlClose(h: uint) {
    unsafe "h is a handle dlopen gave, which the caller owns" {
        _ = lc.DynClose(raw.FromAddr[u8](h))
    }
}
