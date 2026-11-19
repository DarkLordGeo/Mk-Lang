// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: FaultFilterHook, CodeRegionAdd, CodeRegionRemove and InCodeRegion.
//
// How: no fault reaches a filter, and no code region can be made
// (VmCodeNew fails).

/// FaultFilterHook has std/sys/linux's contract; this placeholder does
/// nothing. O(1); allocates nothing.
export FaultFilterHook(f: fn(*u8, u32, uint, uint): bool) {
}

/// CodeRegionAdd has std/sys/linux's contract; this placeholder returns
/// false. O(1); allocates nothing.
export CodeRegionAdd(lo: uint, hi: uint): bool {
    return false
}

/// CodeRegionRemove has std/sys/linux's contract; this placeholder does
/// nothing. O(1); allocates nothing.
export CodeRegionRemove(lo: uint) {
}

/// InCodeRegion has std/sys/linux's contract; this placeholder returns
/// false. O(1); allocates nothing.
export InCodeRegion(pc: uint): bool {
    return false
}
