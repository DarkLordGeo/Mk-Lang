// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: UnwindsFrames (false) and UnwindFrames, which answers -1.
//
// How: the target has no unwinder of std's own, so std.rt's backtrace
// follows the frame chain when the build keeps frame pointers, and says they
// were off otherwise.

/// UnwindsFrames is whether UnwindFrames walks the stack (Windows) or
/// returns -1 for the caller to follow the frame chain: a constant, so a
/// program links only its target's walk (hello's 64 KiB budget).
export const UnwindsFrames: bool = false

/// UnwindFrames is Windows' walk by unwind entries; here -1: the caller
/// follows the frame chain. O(1); allocates nothing.
export UnwindFrames(pcs: mut []uint): int {
    return -1
}
