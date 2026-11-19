// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: a stub of each export of std/sys/linux/child.mk: pipes, SpawnEx,
// ChildSignal, the pty and terminal calls, process groups and the reaper set.
//
// How: each answers Unsupported (call.mk's NotHere) or its empty value.

import std.core

/// Pipe has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export Pipe(): OsResult {
    return NotHere()
}

/// PipeAttach has std/sys/linux's contract; this placeholder answers false
/// (no poller end). O(1); allocates nothing.
export PipeAttach(fd: uint): bool {
    return false
}

/// PipeLend has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export PipeLend(fd: uint): OsResult {
    return NotHere()
}

/// SpawnEx has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export SpawnEx(sp: *u8, args: []str, env: []str, dir: str, s: mut core.IoSpawnReq, src: [3]uint, pty: uint,
    named: []uint, reopen: u64): OsResult {
    return NotHere()
}

/// ChildSignal has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export ChildSignal(h: uint, group: bool, pid: i64, job: uint, sig: u32): OsResult {
    return NotHere()
}

/// TryReap has std/sys/linux's contract; this placeholder has no child to
/// reap: true. O(1); allocates nothing.
export TryReap(h: uint): bool {
    return true
}

/// PtyOpen has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export PtyOpen(cols: int, rows: int, w: mut core.IoWords): OsResult {
    return NotHere()
}

/// PtyResize has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export PtyResize(fd: uint, cols: int, rows: int): OsResult {
    return NotHere()
}

/// PtyClose has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export PtyClose(hpc: uint) {
}

/// GroupEnd has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export GroupEnd(job: uint): OsResult {
    return NotHere()
}

/// GroupClose has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export GroupClose(job: uint) {
}

/// TermOpen has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export TermOpen(): OsResult {
    return NotHere()
}

/// TermMode has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export TermMode(h: uint, out: uint, mode: i64, saved: []u8, old: mut []u8): OsResult {
    return NotHere()
}

/// TermFatalInstall has std/sys/linux's contract; this placeholder has no
/// signal to install for. O(1); allocates nothing.
export TermFatalInstall(restore: fn()) {
}

/// RaiseFatal has std/sys/linux's contract; this placeholder answers
/// false. O(1); allocates nothing.
export RaiseFatal(sig: u32): bool {
    return false
}

/// TermRecords has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export TermRecords(h: uint, out: mut []u8): OsResult {
    return NotHere()
}

/// ReapSetOpen has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export ReapSetOpen(): OsResult {
    return NotHere()
}

/// ReapSetAdd has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export ReapSetAdd(set: uint, h: uint, tag: u64, again: bool): OsResult {
    return NotHere()
}

/// ReapSetWait has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export ReapSetWait(set: uint, tags: mut []u64): OsResult {
    return NotHere()
}
