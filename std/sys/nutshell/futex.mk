// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: SemInit, SemWait and SemPost.
//
// How: no semaphore can be made or waited on.

/// SemInit has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export SemInit(s: mut Sem): bool {
    return false
}

/// SemWait has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export SemWait(s: &Sem, timeout_ns: i64): bool {
    return false
}

/// SemPost has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export SemPost(s: &Sem, n: u32) {
}
