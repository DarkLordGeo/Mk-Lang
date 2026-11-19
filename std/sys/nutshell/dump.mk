// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: MiniDump, which answers -1.
//
// How: the target has no dump format, so runtime.WriteDump answers
// Unsupported.

/// MiniDump is Windows' minidump writer; here -1, none. O(1); allocates
/// nothing.
export MiniDump(path: str): i64 {
    return -1
}
