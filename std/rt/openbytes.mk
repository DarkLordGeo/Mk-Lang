// Why: fs.Walk and Dir.Next can list names that are not UTF-8 (on Windows a
// name with an unpaired surrogate, on Linux any bytes), and fs.OpenPath must
// be able to open them again.
//
// What: OpenBytes (std-internal), which opens a path given as its bytes.
//
// How: valid UTF-8 goes through io's open entry as text, so the route, the
// cancel class and the errors are the open entry's; other bytes reach the
// OS only through the native Io (on Windows as WTF-8).

import std.core
import std.raw

/// OpenBytes opens the file whose path is the bytes p, with the open
/// entry's mode (core.IoOpen's). A NUL byte is InvalidPath, before any
/// call. UTF-8 bytes go to io's open entry as that text, on every Io.
/// Other bytes reach the OS only through the native Io: Windows takes
/// them as WTF-8 and refuses anything else as InvalidPath (std.sys's Wide),
/// Linux passes them as they are. On any other Io (std.sim, a Recorder's
/// Tape, an inert Io) they are Unsupported, as the call entry's bytes
/// operations are there: no name such an Io holds was made from
/// them. O(len(p)) for the checks, then the open entry's cost; allocates
/// nothing.
export OpenBytes(io: Io, p: []u8, mode: u32): core.IoResult {
    for x in p {
        if x == 0 {
            return core.IoResult(value: 0, err: core.SysErr.InvalidPath, os: 0)
        }
    }
    s := str.From(p) else {
        if !core.IsNative(io) {
            return core.IoResult(value: 0, err: core.SysErr.Unsupported, os: 0)
        }
        unsafe "the native open entry reads a path only as bytes (std.task NativeOpen, std.sys's Wide on Windows validates WTF-8 unit by unit, Linux copies the bytes); p lives until it returns" {
            return core.IoOpen(io, raw.Str(raw.Data(p), p.Len()), mode)
        }
    }
    return core.IoOpen(io, s, mode)
}
