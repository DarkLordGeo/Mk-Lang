// Why: std.dl loads shared libraries at run time, but a static Linux binary
// has no dynamic loader and no libc; only a system-libc build can load one.
//
// What: DlInvalid and DlInitFailed (the same os codes as on Windows),
// DlOpen, the symbol lookup and close behind std.dl.
//
// How: in a static build std's libc table is zero, so DlOpen answers
// Unsupported and the binary names no libc symbol. In system-libc mode the
// table holds dlopen, dlsym, dlclose and dlerror, which the entry bound
// (libc.mk), and DlOpen calls dlopen(name, RTLD_NOW | RTLD_LOCAL) on the
// calling task's stack; it blocks the carrier and is not cancellable.

import std.core
import std.raw

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

// DlPathBytes is the buffer of the name dlopen gets: the executable's
// directory (at most PATH_MAX), a '/', the name and the NUL.
const DlPathBytes: int = 4096 + DlNameBytes + 2

// RtldNow is RTLD_NOW; RTLD_LOCAL is 0.
const RtldNow: uint = 2

// Enoent and Enotdir are the stat errors that mean the file is absent.
const Enoent: int = 2
const Enotdir: int = 20

/// DlOpen has std/sys/windows' contract. Outside system-libc mode it
/// answers Unsupported for every name. In the mode, search 0 passes a bare
/// soname to dlopen (ld.so's order, LD_LIBRARY_PATH included), 1 the
/// directory of /proc/self/exe, a '/' and the bare name, 2 an absolute
/// path. InvalidPath for a name of the wrong form (empty, holding a NUL,
/// over 1,024 UTF-16 units, a '/' under 0 or 1, not absolute under 2). A
/// failed dlopen is NotFound under 0, and under 1 and 2 when stat finds no
/// file; otherwise Other with DlInvalid. Runs the library's constructors on
/// the calling stack; blocking, not cancellable.
/// O(name) plus the loader's work; allocates nothing of its own.
export DlOpen(name: str, search: int): OsResult {
    var w := LibcFn(LcDlopen)
    if w == 0 {
        return OsResult(value: 0, err: core.SysErr.Unsupported, os: 0)
    }
    b := name.AsBytes()
    if search < 0 || search > 2 || !DlNameForm(b, search == 2) {
        return DlBadName()
    }
    var buf: [DlPathBytes]u8 = [0; DlPathBytes]
    var n := 0
    if search == 1 {
        n = ExePath(mut buf[..4096])
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
    unsafe "dlopen through the table the entry filled; buf is NUL-terminated and lives across the call" {
        open := raw.Read(raw.PtrCast[u64, fn(*u8, i32): *u8](raw.OfMut(mut w)))
        h := open(raw.Data(buf[..]), i32(RtldNow))
        if !raw.IsNull(h) {
            return OsResult(value: Trunc[i64](raw.Addr(h)), err: core.SysErr.None, os: 0)
        }
        var e := LibcFn(LcDlerror)
        dlerror := raw.Read(raw.PtrCast[u64, fn(): *u8](raw.OfMut(mut e)))
        _ = dlerror() // dlopen reports no cause; this clears the thread's message
    }
    if search == 0 || DlAbsent(raw.Data(buf[..])) {
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

// DlAbsent reports whether statx finds no file at the NUL-terminated path
// p (ENOENT or ENOTDIR).
DlAbsent(p: *u8): bool {
    var st: [32]u64 = [0; 32]
    unsafe "system call; p is NUL-terminated and st is a statx buffer" {
        r := Syscall(Nrs().statx, AtFdcwd, raw.Addr(p), 0, 0, raw.Addr(raw.DataMut(mut st[..])), 0)
        return r == -Enoent || r == -Enotdir
    }
}

/// DlSymbol has std/sys/windows' contract: dlsym's address for the name in
/// the library h, or none, also for a name that is empty, over 255 bytes or
/// holds a NUL, and outside system-libc mode, where no library is ever open.
/// O(name) plus the lookup; allocates nothing.
export DlSymbol(h: uint, name: str): ?*u8 {
    var w := LibcFn(LcDlsym)
    b := name.AsBytes()
    if w == 0 || b.Len() == 0 || b.Len() > DlSymbolBytes {
        return none
    }
    var buf: [DlSymbolBytes + 1]u8 = [0; DlSymbolBytes + 1]
    for i in 0..b.Len() {
        if b[i] == 0 {
            return none
        }
        buf[i] = b[i]
    }
    unsafe "dlsym through the table; h is a handle dlopen gave and buf is NUL-terminated" {
        sym := raw.Read(raw.PtrCast[u64, fn(*u8, *u8): *u8](raw.OfMut(mut w)))
        p := sym(raw.FromAddr[u8](h), raw.Data(buf[..]))
        if raw.IsNull(p) {
            return none
        }
        return some(p)
    }
}

/// DlClose has std/sys/windows' contract: dlclose on h; a failure leaves the
/// library loaded, which is harmless, so nothing is reported. Outside
/// system-libc mode no library is ever open, so it does nothing.
/// O(1) plus the loader's work; allocates nothing.
export DlClose(h: uint) {
    var w := LibcFn(LcDlclose)
    if w == 0 {
        return
    }
    unsafe "dlclose through the table; h is a handle dlopen gave, which the caller owns" {
        close := raw.Read(raw.PtrCast[u64, fn(*u8): i32](raw.OfMut(mut w)))
        _ = close(raw.FromAddr[u8](h))
    }
}
