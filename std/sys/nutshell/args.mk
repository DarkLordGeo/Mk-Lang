// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: ReadArgs, ReadEnv, GetEnvRaw, ExePath and ImageBounds.
//
// How: no arguments, no environment and no image path: each call answers
// the empty value of its result.

/// ReadArgs: no arguments.
/// O(1); allocates nothing.
export ReadArgs(sp: *u8, bytes: mut []u8, ends: mut []int): ArgsInfo {
    return ArgsInfo(count: 0, bytes: 0)
}

/// ReadEnv: no environment.
/// O(1); allocates nothing.
export ReadEnv(sp: *u8, bytes: mut []u8, ends: mut []int): ArgsInfo {
    return ArgsInfo(count: 0, bytes: 0)
}

/// GetEnvRaw: -1, no variable is set.
/// O(1); allocates nothing.
export GetEnvRaw(sp: *u8, name: str, out: mut []u8): int {
    return -1
}

/// ExePath: 0, the failure value.
/// O(1); allocates nothing.
export ExePath(out: mut []u8): int {
    return 0
}

/// ImageBounds: (0, 0), the bounds are unknown.
/// O(1); allocates nothing.
export ImageBounds(sp: *u8): (lo: uint, hi: uint) {
    return (lo: 0, hi: 0)
}
