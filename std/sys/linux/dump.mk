// Why: runtime.WriteDump calls the target's crash-dump writer, and Linux has
// no dump format of std's own, so this file supplies the stub.
//
// What: MiniDump, which answers -1 (no dump written).
//
// How: a constant answer; runtime.WriteDump reports Unsupported on -1.

/// MiniDump is Windows' minidump writer; here -1, none. O(1); allocates
/// nothing.
export MiniDump(path: str): i64 {
    return -1
}
