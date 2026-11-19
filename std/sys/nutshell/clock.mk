// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: Now, WallNow and SleepNanos.
//
// How: there is no clock, so both clocks read 0 and a sleep returns at once.

/// Now has std/sys/linux's contract; this placeholder returns `0`.
/// O(1); allocates nothing.
export Now(): i64 {
    return 0
}

/// WallNow has std/sys/linux's contract; this placeholder returns `0`.
/// O(1); allocates nothing.
export WallNow(): i64 {
    return 0
}

/// SleepNanos has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export SleepNanos(ns: i64) {
}
