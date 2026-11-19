// Why: a file handle can be passed to this process (beside a Unix stream's
// bytes, or named by its parent's Command), and it must become an fs.File
// like any other.
// What: File.Adopt, which std.net's Passed.Adopt calls.
// How: wraps the passed runtime handle in a core.FileCursor at position 0;
// a file of its own, so handle passing touches none of std.fs's other files.

import std.core
import std.rt

/// The File for a file handle passed to this process, carried by p (an
/// rt.Passed of kind PassFile). Programs do not call it: no program can
/// build an rt.Passed, so it is reached only through std.net's
/// Passed.Adopt. O(1); allocates nothing.
///
/// The file reads and writes from position 0. A handle with no position
/// (a pipe end, a device) uses its stream position from the first read or
/// write on, as a file opened by path does.
export File.Adopt(p: rt.Passed): File {
    return File(cur: core.FileCursor(h: p.h, pos: 0), flags: 0, nosh: [])
}
