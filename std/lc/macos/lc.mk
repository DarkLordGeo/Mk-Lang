// Why: Mk compares module-level names ignoring case and underscores, so
// libSystem's close, exit, pipe, raise, readlink, rename and symlink cannot
// be declared in std.sys beside its own Close, Exit, Pipe, Raise, ReadLink,
// Rename and Symlink.
//
// What: those externs, private here, with __bzero (beside std.sys's bzero),
// and one wrapper each for std.sys
// (CloseFd, ExitProcess, PipeFds, RaiseSignal, ReadLinkAt, RenamePath,
// SymlinkPath).
//
// How: each wrapper is the call and nothing more; errno is read by the
// caller through __error(), on the same thread, right after. Every name is
// in std/sys/macos/libSystem.def.

// __bzero is never called by std: x86_64's LTO code generator lowers a
// zeroing memset to it on Darwin (arm64's and the compiler's own to bzero,
// in std.sys), so the link must find it in libSystem.
extern __bzero(p: *u8, n: uint)
extern close(fd: i32): i32
extern exit(code: i32)
extern pipe(fds: *i32): i32
extern raise(sig: i32): i32
extern readlink(path: *u8, buf: *u8, size: uint): int
extern rename(from: *u8, to: *u8): i32
extern symlink(target: *u8, link: *u8): i32

/// CloseFd is close(2): 0, or -1 with errno set.
/// O(1); allocates nothing.
export CloseFd(fd: i32): i32 {
    unsafe "close passes no memory" {
        return close(fd)
    }
}

/// ExitProcess is exit(3); it does not return.
/// Allocates nothing.
export ExitProcess(code: i32) {
    unsafe "exit passes no memory" {
        exit(code)
    }
}

/// PipeFds is pipe(2) into fds[0] (read) and fds[1] (write). Needs unsafe:
/// fds must hold two i32.
/// O(1); allocates nothing.
export PipeFds(fds: *i32): i32 {
    unsafe "the caller's fds holds two i32" {
        return pipe(fds)
    }
}

/// RaiseSignal is raise(3) on the calling thread.
/// O(1); allocates nothing.
export RaiseSignal(sig: i32): i32 {
    unsafe "raise passes no memory" {
        return raise(sig)
    }
}

/// ReadLinkAt is readlink(2): path NUL-terminated, buf holding size bytes;
/// the byte count, or -1 with errno set. Needs unsafe: both pointers are
/// the caller's.
/// O(1); allocates nothing.
export ReadLinkAt(path: *u8, buf: *u8, size: uint): int {
    unsafe "path is a NUL-terminated string and buf holds size bytes, both the caller's" {
        return readlink(path, buf, size)
    }
}

/// RenamePath is rename(2) of two NUL-terminated paths. Needs unsafe.
/// O(1); allocates nothing.
export RenamePath(from: *u8, to: *u8): i32 {
    unsafe "both are NUL-terminated strings the caller holds" {
        return rename(from, to)
    }
}

/// SymlinkPath is symlink(2) of two NUL-terminated paths. Needs unsafe.
/// O(1); allocates nothing.
export SymlinkPath(target: *u8, link: *u8): i32 {
    unsafe "both are NUL-terminated strings the caller holds" {
        return symlink(target, link)
    }
}
