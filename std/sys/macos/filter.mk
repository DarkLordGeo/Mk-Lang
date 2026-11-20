// Why: runtime.AddFaultFilter and std.vm's code regions route faults
// through the target's filters, which the macOS layer does not provide: no
// code region can be made there (VmCodeNew fails, vm.mk).
//
// What: FaultFilterHook, CodeRegionAdd, CodeRegionRemove and InCodeRegion.
//
// How: no fault reaches a filter, and no code region can be made
// (VmCodeNew fails).

/// FaultFilterHook has std/sys/linux's contract; on macOS it does
/// nothing. O(1); allocates nothing.
export FaultFilterHook(f: fn(*u8, u32, uint, uint): bool) {
}

/// CodeRegionAdd has std/sys/linux's contract; on macOS it returns
/// false. O(1); allocates nothing.
export CodeRegionAdd(lo: uint, hi: uint): bool {
    return false
}

/// CodeRegionRemove has std/sys/linux's contract; on macOS it does
/// nothing. O(1); allocates nothing.
export CodeRegionRemove(lo: uint) {
}

/// InCodeRegion has std/sys/linux's contract; on macOS it returns
/// false. O(1); allocates nothing.
export InCodeRegion(pc: uint): bool {
    return false
}
