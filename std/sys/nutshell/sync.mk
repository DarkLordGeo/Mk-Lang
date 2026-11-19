// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: DataSync.
//
// How: it answers Unsupported.

/// DataSync has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export DataSync(h: uint): OsResult {
    return NotHere()
}
