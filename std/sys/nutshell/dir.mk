// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: DirBlockBytes, DirFillOnOpen, MakeDir, StatPath, DirOpen, DirFill
// and DirNext.
//
// How: every call fails with Unsupported.

import std.core

/// DirBlockBytes is the size of the block a directory stream reads into: a
/// 32-byte head (the next record's offset at 0, the bytes filled at 8, the
/// state at 16: 0 live, 2 the end), the getdents64 records from DirHead to
/// DirFacts, and from DirFacts one DirSlot-byte facts slot per record (the
/// kind at 0, 0 when not read yet and DirSkip for an entry gone before its
/// statx; the size at 8; the time at 16), the slot of the record at offset
/// o being o / DirSlot: a record is at least 24 bytes (8-byte aligned
/// 19 + name + NUL), so no two records share one. Same value on both
/// targets: DirFill makes every system call, DirNext none.
export const DirBlockBytes: int = 16384

/// DirFillOnOpen has std/sys/linux's meaning; false.
export const DirFillOnOpen: bool = false

/// MakeDir has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export MakeDir(p: str): OsResult {
    return Unsupported()
}

/// StatPath has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export StatPath(p: str, st: mut core.IoStat): OsResult {
    return Unsupported()
}

/// DirOpen has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export DirOpen(p: str): OsResult {
    return Unsupported()
}

/// DirFill has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export DirFill(h: uint, block: mut []u8): OsResult {
    return Unsupported()
}

/// DirNext has std/sys/linux's contract; this placeholder fails with Other.
/// O(1); allocates nothing.
export DirNext(block: mut []u8, name: mut []u8, st: mut core.IoStat): OsResult {
    return Unsupported()
}
