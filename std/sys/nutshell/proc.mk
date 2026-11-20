// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: PipeRoom, SharedPort, Spawn, PollRelease, ProcKill, ShareListener,
// Inherited and InheritSeal.
//
// How: no child can be spawned or inherited.

import std.core

/// PipeRoom has std/sys/linux's contract; this placeholder returns `-1`.
/// O(1); allocates nothing.
export PipeRoom(h: uint): int {
    return -1
}

/// SharedPort has std/sys/linux's contract; this placeholder never accepts.
export const SharedPort: i64 = 1

/// Spawn has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export Spawn(sp: *u8, args: []str, child: mut core.IoChild, inherit: uint): OsResult {
    return Unsupported()
}

/// PollRelease has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export PollRelease(op: *Op) {
}

/// ProcKill has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export ProcKill(h: uint): OsResult {
    return Unsupported()
}

/// ShareListener has std/sys/linux's contract; this placeholder fails with
/// Other. O(1); allocates nothing.
export ShareListener(fd: uint): OsResult {
    return Unsupported()
}

/// Inherited has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export Inherited(sp: *u8): OsResult {
    return Unsupported()
}

/// InheritSeal has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export InheritSeal(sp: *u8) {
}
