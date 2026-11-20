// Why: std.sys's test points drive interleavings tests cannot reach from
// outside; the macOS layer has none.
//
// What: SysTestHook.
//
// How: no test point exists; SysTestHook returns 0.

/// SysTestHook has std/sys/linux's contract; on macOS it returns `0`.
/// O(1); allocates nothing.
export SysTestHook(point: u32, cb: fn(*u8), arg: *u8, on: bool): u64 {
    return 0
}
