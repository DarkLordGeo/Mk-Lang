// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: SysTestHook.
//
// How: no test point exists; SysTestHook returns 0.

/// SysTestHook has std/sys/linux's contract; this placeholder returns `0`.
/// O(1); allocates nothing.
export SysTestHook(point: u32, cb: fn(*u8), arg: *u8, on: bool): u64 {
    return 0
}
