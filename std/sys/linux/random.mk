// Why: std needs cryptographically secure random bytes, and on Linux the
// kernel is the source.
//
// What: Random, which fills buf or answers false.
//
// How: getrandom with flags 0 in chunks of at most 33,554,431 bytes,
// repeated on a short read, EINTR or EAGAIN until buf is full.

import std.raw

const RandomChunk: int = 33_554_431

/// Random is false only when the OS source is unusable (ENOSYS or another
/// error).
/// O(len(buf)); allocates nothing.
export Random(buf: mut []u8): bool {
    var off := 0
    for off < buf.Len() {
        var n := buf.Len() - off
        if n > RandomChunk {
            n = RandomChunk
        }
        var r := 0
        unsafe "system call; buf holds off + n bytes" {
            r = Syscall(Nrs().getrandom, raw.Addr(raw.Offset(raw.DataMut(buf), off)), Trunc[uint](n), 0, 0, 0, 0)
        }
        if r == -4 || r == -11 {
            continue
        }
        if IsErr(r) {
            return false
        }
        off += r
    }
    return true
}
