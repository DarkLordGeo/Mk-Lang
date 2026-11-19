// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: SignalWatch, SignalCount, Raise and ThreadInterrupt.
//
// How: no signal is watched, counted or raised.

/// SignalWatch has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export SignalWatch(sig: u32): bool {
    return false
}

/// SignalCount has std/sys/linux's contract; this placeholder returns `0`.
/// O(1); allocates nothing.
export SignalCount(sig: u32): u64 {
    return 0
}

/// Raise has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export Raise(sig: u32): bool {
    return false
}

/// ThreadInterrupt has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export ThreadInterrupt(thread: uint): bool {
    return false
}
