// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: FaultHook, ExternFaultHook, FuncRowHook, FatalWrite, FaultOwnerHook,
// FaultOwned and FaultOwnedBy.
//
// How: no fault is delivered, so the hook is never called, and a fatal
// report has nowhere to go.

/// ExternFaultHook has std/sys/linux's contract; this placeholder does
/// nothing.
/// O(1); allocates nothing.
export ExternFaultHook(f: fn(uint): uint) {
}

/// FuncRowHook has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export FuncRowHook(rows: *u8, n: int) {
}

/// FaultHook has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export FaultHook(h: fn(&FaultInfo): never) {
}

/// FatalWrite has std/sys/linux's contract; this placeholder does nothing.
/// O(1); allocates nothing.
export FatalWrite(data: []u8) {
}

/// FaultOwnerHook is Linux's ownership hook (std/sys/linux/fault.mk); the
/// placeholder target has no fault routing, so it does nothing. O(1); allocates nothing.
export FaultOwnerHook(f: fn(uint): bool) {
}

/// FaultOwned is Linux's ownership test; every fault is taken as Mk's
/// here, so it answers true. O(1).
export FaultOwned(pc: uint, sp: uint): bool {
    return true
}

/// FaultOwnedBy is FaultOwned with the thread's answer given: true here.
/// O(1).
export FaultOwnedBy(pc: uint, sp: uint, made: bool): bool {
    return true
}
