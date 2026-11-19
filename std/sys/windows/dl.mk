// Why: std.dl loads libraries at run time, and on Windows a careless
// LoadLibrary searches the current directory and PATH, which lets a
// planted DLL be loaded in place of the real one.
//
// What: the Windows loader calls behind std.dl: DlOpen, DlSymbol and
// DlClose, with the os codes DlInvalid and DlInitFailed.
//
// How: LoadLibraryExW with one of three LOAD_LIBRARY_SEARCH_* sets, never
// the current directory or PATH; GetProcAddress by name only; FreeLibrary.
// Every import is kernel32's. The calls run on the calling task's stack
// (the loader's initializers included), which carries the task's own
// NT_TIB words, so the loader's exception handling works there; they
// block the carrier and are not cancellable.

import std.core
import std.raw

// The LOAD_LIBRARY_SEARCH_* flag sets of DlOpen's searches:
// SYSTEM32 alone; APPLICATION_DIR with SYSTEM32, so a private DLL's own
// system dependencies resolve; DLL_LOAD_DIR with SYSTEM32 for an absolute
// path.
const DlSearchSystem: u32 = 0x800
const DlSearchApplication: u32 = 0xA00
const DlSearchPath: u32 = 0x900

// DlNameUnits is the longest library name, in UTF-16 units;
// the buffer holds one more for the NUL.
const DlNameUnits: int = 1024

// DlSymbolBytes is the longest symbol name in bytes; the buffer holds one
// more for the NUL.
const DlSymbolBytes: int = 255

// Win32 codes DlOpen reports as itself: ERROR_MOD_NOT_FOUND is
// NotFound.
const ErrorModNotFound: u32 = 126

/// DlInvalid is the os code of an Other failure that std.dl reports as
/// Invalid: not a loadable library for this target (ERROR_BAD_EXE_FORMAT).
/// The same value on every target.
export const DlInvalid: i64 = 193

/// DlInitFailed is the os code of an Other failure that std.dl reports as
/// InitFailed: the library's initializer failed (ERROR_DLL_INIT_FAILED).
/// The same value on every target.
export const DlInitFailed: i64 = 1114

/// DlOpen loads the library name. search is 0 for a bare name in System32,
/// 1 for a bare name in the executable's directory (System32 for the
/// library's dependencies), 2 for an absolute path. value is the module
/// handle. Errors: InvalidPath for a name that is empty, holds a NUL, is
/// over 1,024 UTF-16 units, holds a separator under 0 or 1, or is not
/// absolute under 2 (BadName); NotFound for error 126; Other with the
/// Win32 code otherwise (DlInvalid, DlInitFailed or any other). Runs the
/// library's initializers on the calling stack; blocking, not cancellable.
/// O(name) plus the loader's work; allocates nothing of its own.
export DlOpen(name: str, search: int): OsResult {
    var flags := DlSearchSystem
    if search == 1 {
        flags = DlSearchApplication
    } else if search == 2 {
        flags = DlSearchPath
    } else if search != 0 {
        return DlBadName()
    }
    if !DlNameForm(name.AsBytes(), search == 2) {
        return DlBadName()
    }
    var name16: [DlNameUnits + 1]u16 = [0; DlNameUnits + 1]
    if Wide(name, mut name16[..]) < 0 {
        return DlBadName()
    }
    unsafe "kernel32; name16 is NUL-terminated and lives across the call" {
        m := LoadLibraryExW(raw.Data(name16[..]), 0, flags)
        if m != 0 {
            return OsResult(value: Trunc[i64](m), err: core.SysErr.None, os: 0)
        }
        e := GetLastError()
        if e == ErrorModNotFound {
            return OsResult(value: 0, err: core.SysErr.NotFound, os: i64(e))
        }
        return OsResult(value: 0, err: core.SysErr.Other, os: i64(e))
    }
}

// DlBadName is DlOpen's answer for a name of the wrong form.
DlBadName(): OsResult {
    return OsResult(value: 0, err: core.SysErr.InvalidPath, os: 0)
}

// DlNameForm reports whether b has the form its search needs: not empty;
// for a bare name no '/', '\' or ':' (a drive-relative "C:x.dll" is not a
// bare name); for a path, absolute: a drive letter, a
// colon and a separator, or two separators (UNC or \\?\).
DlNameForm(b: []u8, path: bool): bool {
    if b.Len() == 0 {
        return false
    }
    if !path {
        for c in b {
            if DlSep(c) || c == b':' {
                return false
            }
        }
        return true
    }
    if b.Len() >= 3 && DlLetter(b[0]) && b[1] == b':' && DlSep(b[2]) {
        return true
    }
    return b.Len() >= 2 && DlSep(b[0]) && DlSep(b[1])
}

// DlSep reports whether c is a Windows path separator.
DlSep(c: u8): bool {
    return c == b'/' || c == b'\\'
}

// DlLetter reports whether c is an ASCII letter.
DlLetter(c: u8): bool {
    return (c >= b'a' && c <= b'z') || (c >= b'A' && c <= b'Z')
}

/// DlSymbol is the address GetProcAddress gives for the exported name in
/// the module h, or none: also for a name that is empty, over 255 bytes or
/// holds a NUL. By name only, never an ordinal. O(name) plus the lookup;
/// allocates nothing.
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
    unsafe "kernel32; h is a loaded module and buf is NUL-terminated" {
        p := GetProcAddress(h, raw.Data(buf[..]))
        if raw.IsNull(p) {
            return none
        }
        return some(p)
    }
}

/// DlClose releases the module h (FreeLibrary); a failure leaves it
/// loaded, which is harmless, so nothing is reported. Runs the library's
/// detach code when this was the last reference; blocking, not
/// cancellable. O(1) plus the loader's work; allocates nothing.
export DlClose(h: uint) {
    unsafe "kernel32; h is a loaded module the caller owns" {
        _ = FreeLibrary(h)
    }
}
