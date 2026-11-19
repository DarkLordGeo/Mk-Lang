import std.fs

// Sum adds the bytes of the first len bytes of f, which this process
// created and holds exclusively.
export Sum(io: Io, f: &fs.File, len: int): fs.Error!u64 {
    var total: u64 = 0
    unsafe "f is this process's own file, held under its lock, so no other process changes the mapped bytes" {
        m := try fs.Map(io, f, 0, len)
        for b in m.Bytes() {
            total += Cast[u64](b)
        }
        m.Unmap()
        empty := try fs.Map(io, f, 4_096, 0)
        empty.Unmap()
        map := fs.Map
        again := try map(io, f, 0, len)
        again.Unmap()
    }
    return total
}
