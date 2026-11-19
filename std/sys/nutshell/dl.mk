// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: DlInvalid, DlInitFailed, DlOpen, DlSymbol and DlClose, the loader
// calls behind std.dl.
//
// How: each call answers Unsupported; the constants match the other
// targets.

/// DlInvalid has std/sys/linux's meaning.
export const DlInvalid: i64 = 193

/// DlInitFailed has std/sys/linux's meaning.
export const DlInitFailed: i64 = 1114

/// DlOpen has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export DlOpen(name: str, search: int): OsResult {
    return NotHere()
}

/// DlSymbol has std/sys/linux's contract; this placeholder answers none.
/// O(1); allocates nothing.
export DlSymbol(h: uint, name: str): ?*u8 {
    return none
}

/// DlClose has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export DlClose(h: uint) {
}
