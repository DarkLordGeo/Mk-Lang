// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: Random.
//
// How: there is no entropy source, so Random fails.

/// Random has std/sys/linux's contract; this placeholder returns `false`.
/// O(1); allocates nothing.
export Random(buf: mut []u8): bool {
    return false
}
