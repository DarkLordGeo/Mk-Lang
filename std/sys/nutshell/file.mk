// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: the file and handle calls (StdHandle, ReadSync, WriteSync, OpenFile,
// Close, the locks, Rename, Remove, the console calls and the rest).
//
// How: every call that returns an OsResult fails with Unsupported; no
// handle is a console.

/// StdHandle: the runtime handle numbers 0, 1 and 2, as on Linux.
/// O(1); allocates nothing.
export StdHandle(i: int): uint {
    return Trunc[uint](i)
}

/// HandleKind: 0, unknown.
/// O(1); allocates nothing.
export HandleKind(h: uint): u32 {
    return 0
}

/// IsConsole has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export IsConsole(h: uint): bool {
    return false
}

/// ConsoleTestType has std/sys/linux's contract; this placeholder returns `-1`.
/// O(1); allocates nothing.
export ConsoleTestType(units: []u16): int {
    return -1
}

/// ReadSync has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export ReadSync(h: uint, buf: mut []u8, pos: i64): OsResult {
    return Unsupported()
}

/// WriteSync has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export WriteSync(h: uint, data: []u8, pos: i64): OsResult {
    return Unsupported()
}

/// ReadOnce has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export ReadOnce(h: uint, buf: mut []u8, pos: i64): OsResult {
    return Unsupported()
}

/// WriteOnce has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export WriteOnce(h: uint, data: []u8, pos: i64): OsResult {
    return Unsupported()
}

/// OpenFile has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export OpenFile(path: str, mode: u32, async: bool): OsResult {
    return Unsupported()
}

/// Close has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export Close(h: uint): OsResult {
    return Unsupported()
}

/// FileSize has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export FileSize(h: uint): OsResult {
    return Unsupported()
}

/// Truncate has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export Truncate(h: uint, size: i64): OsResult {
    return Unsupported()
}

/// FileId has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export FileId(h: uint, out: mut [2]u64): OsResult {
    return Unsupported()
}

/// LockRange has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export LockRange(h: uint, off: i64, n: i64, exclusive: bool, wait: bool): OsResult {
    return Unsupported()
}

/// UnlockRange has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export UnlockRange(h: uint, off: i64, n: i64): OsResult {
    return Unsupported()
}

/// SyncFile has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export SyncFile(h: uint): OsResult {
    return Unsupported()
}

/// Rename has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export Rename(src: str, dst: str): OsResult {
    return Unsupported()
}

/// Remove has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export Remove(p: str): OsResult {
    return Unsupported()
}

/// WriteConsole has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export WriteConsole(h: uint, data: []u8, carry: mut ConsoleCarry): OsResult {
    return Unsupported()
}

/// HandleCount: -1, the count cannot be read.
/// O(1); allocates nothing.
export HandleCount(): int {
    return -1
}

/// TimerHandles: 0; no timed wait here holds a handle. On Windows it
/// counts the high-resolution timer slots' handles (sys/windows/hrtimer.mk).
/// For leak tests (task.KeptHandles).
/// O(1); allocates nothing.
export TimerHandles(): int {
    return 0
}

/// ReOpenSync has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export ReOpenSync(h: uint, write: bool): OsResult {
    return NotHere()
}
