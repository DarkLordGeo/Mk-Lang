// Why: runtime.WriteDump calls the target's crash-dump writer, and macOS,
// like Linux, has no dump format of std's own.
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
