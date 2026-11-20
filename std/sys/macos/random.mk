// Why: std needs cryptographically secure random bytes, and on macOS the
// kernel is the source.
//
// What: Random, which fills buf or answers false.
//
// How: getentropy in chunks of at most 256 bytes, its limit.

import std.raw

const RandomChunk: int = 256

/// Random is false only when the OS source fails.
/// O(len(buf)); allocates nothing.
export Random(buf: mut []u8): bool {
    var off := 0
    for off < buf.Len() {
        var n := buf.Len() - off
        if n > RandomChunk {
            n = RandomChunk
        }
        unsafe "buf holds off + n bytes" {
            if getentropy(raw.Offset(raw.DataMut(buf), off), Trunc[uint](n)) != 0 {
                if Errno() == 4 {
                    continue
                }
                return false
            }
        }
        off += n
    }
    return true
}
