// Reads a file this program created and holds under its exclusive lock through a read-only
// mapping (fs.Map).
import std.fs

/// Sum adds the bytes of f from offset for len bytes through a mapping. f is this process's
/// own file under its exclusive lock, so no process changes the mapped bytes; len 0 maps nothing.
/// Fails as fs.Map does; O(len); allocates nothing.
export Sum(io: Io, f: &fs.File, offset: i64, len: int): fs.Error!u64 {
    var total: u64 = 0
    unsafe "f is this process's own file under its exclusive lock, so no process changes the mapped bytes while the mapping lives" {
        m := try fs.Map(io, f, offset, len)
        for b in m.Bytes() {
            total += Cast[u64](b)
        }
        m.Unmap()
    }
    return total
}
