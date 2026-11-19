// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: ThreadStartJoin and ThreadJoin.
//
// How: no thread can be started.

import std.core

/// ThreadStartJoin has std/sys/linux's contract; this placeholder fails
/// Unsupported (os 38). O(1); allocates nothing.
export ThreadStartJoin(entry: fn(*u8), arg: *u8, stack: int): OsResult {
    return OsResult(value: 0, err: core.SysErr.Unsupported, os: 38)
}

/// ThreadJoin has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export ThreadJoin(h: i64) {
}
