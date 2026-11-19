// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: Unix, AcceptUnixArea, UnixListen, UnixUnbind and UnixConnect.
//
// How: stubs of std/sys/linux/unix.mk's exports, each answering
// Unsupported.

/// Unix has std/sys/linux's meaning.
export const Unix: u16 = 1

/// AcceptUnixArea has std/sys/linux's meaning.
export const AcceptUnixArea: int = 256

/// UnixListen has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export UnixListen(path: []u8, backlog: int): OsResult {
    return NotHere()
}

/// UnixUnbind has std/sys/linux's contract; this placeholder removes
/// nothing. O(1); allocates nothing.
export UnixUnbind(path: []u8) {
    _ = path
}

/// UnixConnect has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export UnixConnect(path: []u8): OsResult {
    return NotHere()
}
