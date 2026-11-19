// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: ThreadStart, ThreadInit, ThreadYield, StackWords and
// ThreadStackBounds.
//
// How: no thread can be started.

/// ThreadStart has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export ThreadStart(entry: fn(*u8), arg: *u8, stack: int): OsResult {
    return Unsupported()
}

/// ThreadInit has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export ThreadInit(): bool {
    return false
}

/// ThreadYield has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export ThreadYield() {
}

/// StackWords has std/sys/linux's contract; this placeholder returns `[0; 4]`.
/// O(1); allocates nothing.
export StackWords(): [4]u64 {
    return [0; 4]
}

/// ThreadStackBounds has std/sys/linux's contract; this placeholder returns `(lo: 0, hi: 0)`.
/// O(1); allocates nothing.
export ThreadStackBounds(): (lo: uint, hi: uint) {
    return (lo: 0, hi: 0)
}
