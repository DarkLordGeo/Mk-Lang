// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: PassMax, UnixSendFds, UnixRecvFds, PassKind, PassReady and
// HandlesValue, the handle-passing calls.
//
// How: each answers Unsupported: no handle can be passed or inherited.

/// PassMax has std/sys/linux's meaning.
export const PassMax: int = 64

/// UnixSendFds has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export UnixSendFds(fd: uint, data: []u8, fds: []uint): OsResult {
    return NotHere()
}

/// UnixRecvFds has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export UnixRecvFds(fd: uint, buf: mut []u8, fds: mut []uint): OsResult {
    return NotHere()
}

/// PassKind has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export PassKind(fd: uint): OsResult {
    return NotHere()
}

/// PassReady has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export PassReady(fd: uint, socket: bool): OsResult {
    return NotHere()
}

/// HandlesValue has std/sys/linux's contract; this placeholder answers -1,
/// none set. O(1); allocates nothing.
export HandlesValue(sp: *u8, out: mut []uint): int {
    return -1
}
